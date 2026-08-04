package envgen

// referent_alternate_spaces_test.go covers Phase 4 of the design (see
// docs/superpowers/specs/2026-08-04-referent-alternate-id-spaces.md): envgen
// publishing and resolving alternate identifier spaces at plan time --
// referent-root sibling outputs, per-space lookup locals, and the resolver
// rewrite/selector pattern that ties them together.

import (
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"testing"

	"github.com/dvmrry/infrawright-dev/go/internal/metadata"
)

// referentAlternateSpaceEnvironmentFixture is a minimal provider-neutral
// universe: one generated referent (sample_groups) publishing an alternate
// "val" space, and one referrer (sample_rule) with two reference fields into
// it -- group_id citing the canonical id space, group_val citing "val".
// Both fields tokenize to the SAME per-key token shape
// (sample_groups.<key>); only the covering edge's declared space differs.
type referentAlternateSpaceEnvironmentFixture struct {
	root           metadata.LoadedPackRoot
	deploymentPath string
	outputRoot     string
	workspace      string
}

func newReferentAlternateSpaceEnvironmentFixture(t *testing.T) referentAlternateSpaceEnvironmentFixture {
	t.Helper()
	workspace := t.TempDir()
	packsRoot := filepath.Join(workspace, "packs")

	optionalString := func() metadata.JsonObject { return terraformTestAttribute("string", "optional") }
	computedString := func() metadata.JsonObject { return terraformTestAttribute("string", "computed") }
	computedNumber := func() metadata.JsonObject { return terraformTestAttribute("number", "computed") }

	resourceSchemas := metadata.JsonObject{
		"sample_groups": terraformTestBlock(metadata.JsonObject{
			"id":   computedString(),
			"val":  computedNumber(),
			"name": optionalString(),
		}),
		"sample_rule": terraformTestBlock(metadata.JsonObject{
			"id":        computedString(),
			"group_id":  optionalString(),
			"group_val": optionalString(),
		}),
	}
	registry := metadata.JsonObject{
		"sample_groups": metadata.JsonObject{"generate": true, "product": "sample"},
		"sample_rule":   metadata.JsonObject{"generate": true, "product": "sample"},
	}
	references := metadata.JsonObject{
		"sample_rule": metadata.JsonObject{
			"group_id": metadata.JsonObject{
				"referent": "sample_groups", "name_field": "name",
			},
			"group_val": metadata.JsonObject{
				"referent": "sample_groups", "name_field": "name", "referent_id_field": "val",
			},
		},
	}
	manifest := metadata.JsonObject{
		"pin":               "1.0.0",
		"provider_prefixes": metadata.JsonObject{"sample_": "sample"},
		"provider_sources":  metadata.JsonObject{"sample": "example/sample"},
		"references":        references,
	}
	writeSyntheticTopologyPack(t, packsRoot, "sample", manifest, registry, resourceSchemas)
	profilePath := filepath.Join(packsRoot, "sample.packset.json")
	writeJSONFile(t, profilePath, metadata.JsonObject{
		"kind":    metadata.PackSetKind,
		"version": 1,
		"packs":   []any{"sample"},
		"shared":  []any{},
	})
	root, err := metadata.LoadPackRoot(metadata.LoadPackRootOptions{
		PacksRoot: packsRoot, ProfilePath: &profilePath,
	})
	if err != nil {
		t.Fatalf("LoadPackRoot(alternate-space fixture) error = %v", err)
	}

	deploymentPath := filepath.Join(workspace, "deployment.json")
	writeJSONFile(t, deploymentPath, metadata.JsonObject{
		"module_dir": filepath.Join(workspace, "modules"),
		"overlay":    workspace,
		"roots": metadata.JsonObject{
			"sample": metadata.JsonObject{"cross_state_references": true},
		},
	})

	configDirectory := filepath.Join(workspace, "config", "tenant")
	writeJSONFile(t, filepath.Join(configDirectory, "sample_rule.auto.tfvars.json"), metadata.JsonObject{
		"items": metadata.JsonObject{
			"rule_one": metadata.JsonObject{
				"group_id":  "sample_groups.group_one",
				"group_val": "sample_groups.group_two",
			},
		},
	})
	writeJSONFile(t, filepath.Join(configDirectory, "lookups", "sample_groups.lookup.json"), metadata.JsonObject{
		"by_id":     metadata.JsonObject{"id-1": "Group One", "id-2": "Group Two"},
		"id_by_key": metadata.JsonObject{"group_one": "id-1", "group_two": "id-2"},
		"key_by_id": metadata.JsonObject{"id-1": "group_one", "id-2": "group_two"},
		"spaces": metadata.JsonObject{
			"val": metadata.JsonObject{
				"id_by_key": metadata.JsonObject{"group_one": "501", "group_two": "502"},
				"key_by_id": metadata.JsonObject{"501": "group_one", "502": "group_two"},
			},
		},
	})
	writeJSONFile(t, filepath.Join(configDirectory, "sample_rule.generated.expressions.json"), metadata.JsonObject{
		"resources": metadata.JsonObject{
			"sample_rule.rule_one": metadata.JsonObject{
				"group_id": metadata.JsonObject{
					"expression": `data.terraform_remote_state.sample_groups.outputs.iw_reference_ids.sample_groups["group_one"]`,
				},
				"group_val": metadata.JsonObject{
					"expression": `data.terraform_remote_state.sample_groups.outputs.iw_reference_ids_val.sample_groups["group_two"]`,
				},
			},
		},
	})

	return referentAlternateSpaceEnvironmentFixture{
		root:           root,
		deploymentPath: deploymentPath,
		outputRoot:     filepath.Join(workspace, "generated"),
		workspace:      workspace,
	}
}

func (f referentAlternateSpaceEnvironmentFixture) generate(t *testing.T, stateAware bool, probe StateProbe) (referentMain, referrerBindings string) {
	t.Helper()
	outputRoot := f.outputRoot
	if _, err := GenerateEnvironmentRoots(GenerateEnvironmentRootsOptions{
		Deployment: loadDeploymentFile(t, f.deploymentPath),
		FormatHcl:  identityFormatter,
		OutputRoot: &outputRoot,
		Root:       f.root,
		Selectors:  []string{"sample_rule"},
		StateAware: stateAware,
		StateProbe: probe,
		Tenant:     "tenant",
	}); err != nil {
		t.Fatalf("GenerateEnvironmentRoots(alternate-space fixture, stateAware=%t) error = %v", stateAware, err)
	}
	mainPath := filepath.Join(f.outputRoot, "tenant", "sample_groups", "main.tf")
	mainBytes, err := os.ReadFile(mainPath)
	if err != nil {
		t.Fatalf("ReadFile(%s) error = %v", mainPath, err)
	}
	bindingsPath := filepath.Join(f.outputRoot, "tenant", "sample_rule", "expression_bindings.tf")
	bindingsBytes, err := os.ReadFile(bindingsPath)
	if err != nil {
		t.Fatalf("ReadFile(%s) error = %v", bindingsPath, err)
	}
	return string(mainBytes), string(bindingsBytes)
}

// TestReferentAlternateSpaceSiblingOutputPinned is the primary Phase 4
// characterization: the referent root (sample_groups) publishes BOTH the
// canonical iw_reference_ids output and the iw_reference_ids_val sibling,
// and the referrer root's expression_bindings.tf resolves both edges
// through their own space with committed-lookup fallback, including both
// the canonical and the _val lookup locals.
func TestReferentAlternateSpaceSiblingOutputPinned(t *testing.T) {
	fixture := newReferentAlternateSpaceEnvironmentFixture(t)
	main, bindings := fixture.generate(t, false, nil)

	wantCanonicalOutput := `output "iw_reference_ids" {
  description = "Minimal stable-key to provider ID map for opted-in cross-state consumers."
  sensitive   = true
  value = {
    sample_groups = { for key, item in module.sample_groups.items : key => item.id }
  }
}`
	if !containsNormalized(main, wantCanonicalOutput) {
		t.Errorf("sample_groups main.tf = %q, want canonical output block %q", main, wantCanonicalOutput)
	}
	wantSiblingOutput := `output "iw_reference_ids_val" {
  description = "Minimal stable-key to provider ID map for opted-in cross-state consumers."
  sensitive   = true
  value = {
    sample_groups = { for key, item in module.sample_groups.items : key => item.val }
  }
}`
	if !containsNormalized(main, wantSiblingOutput) {
		t.Errorf("sample_groups main.tf = %q, want sibling output block %q", main, wantSiblingOutput)
	}

	wantCanonicalResolver := `try(data.terraform_remote_state.sample_groups.outputs.iw_reference_ids.sample_groups["group_one"], local.iw_reference_lookup_sample_groups["group_one"])`
	if !contains(bindings, wantCanonicalResolver) {
		t.Errorf("sample_rule expression_bindings.tf = %q, want canonical resolver %q", bindings, wantCanonicalResolver)
	}
	wantAlternateResolver := `try(data.terraform_remote_state.sample_groups.outputs.iw_reference_ids_val.sample_groups["group_two"], local.iw_reference_lookup_sample_groups_val["group_two"])`
	if !contains(bindings, wantAlternateResolver) {
		t.Errorf("sample_rule expression_bindings.tf = %q, want alternate-space resolver %q", bindings, wantAlternateResolver)
	}
	wantCanonicalLocal := `iw_reference_lookup_sample_groups = fileexists`
	if !contains(bindings, wantCanonicalLocal) {
		t.Errorf("sample_rule expression_bindings.tf = %q, want canonical lookup local", bindings)
	}
	wantAlternateLocalFragment := `iw_reference_lookup_sample_groups_val = fileexists`
	if !contains(bindings, wantAlternateLocalFragment) {
		t.Errorf("sample_rule expression_bindings.tf = %q, want _val lookup local", bindings)
	}
	// Pin the _val local's exact within-space fallback shape: id_by_key,
	// then invert THAT SPACE's key_by_id, never the canonical maps.
	wantAlternateLocalShape := regexp.MustCompile(
		`iw_reference_lookup_sample_groups_val = fileexists\([^)]*\) \? try\(jsondecode\(file\([^)]*\)\)\.spaces\.val\.id_by_key, \{ for id, k in jsondecode\(file\([^)]*\)\)\.spaces\.val\.key_by_id : k => id \}, \{\}\) : \{\}`,
	)
	if !wantAlternateLocalShape.MatchString(bindings) {
		t.Errorf("sample_rule expression_bindings.tf = %q, want _val local matching %s", bindings, wantAlternateLocalShape.String())
	}
}

// TestReferentAlternateSpaceStateAwareAbsentProbeKeepsLookupOnly proves the
// resolver rewrite's lookup-only arm reaches a sibling selector exactly the
// way it already reaches the canonical one: rewriteResolverFallbacks keys
// lookupOnlyReferents on the bare referent (resource type), independent of
// which space a binding's selector cites.
func TestRewriteResolverFallbacksAlternateSpaceLookupOnly(t *testing.T) {
	bindingsByType := map[string][]ExpressionBinding{
		"sample_rule": {
			{
				Key:        "rule_one",
				Path:       "group_id",
				Expression: `data.terraform_remote_state.sample_groups.outputs.iw_reference_ids.sample_groups["group_one"]`,
			},
			{
				Key:        "rule_one",
				Path:       "group_val",
				Expression: `data.terraform_remote_state.sample_groups.outputs.iw_reference_ids_val.sample_groups["group_two"]`,
			},
		},
	}
	operatorIdentitiesByType := map[string]map[string]bool{"sample_rule": {}}
	lookupOnlyReferents := map[string]bool{"sample_groups": true}
	rewriteResolverFallbacks(bindingsByType, operatorIdentitiesByType, lookupOnlyReferents)

	got := bindingsByType["sample_rule"]
	wantCanonical := `local.iw_reference_lookup_sample_groups["group_one"]`
	if got[0].Expression != wantCanonical {
		t.Errorf("group_id expression = %q, want lookup-only %q", got[0].Expression, wantCanonical)
	}
	wantAlternate := `local.iw_reference_lookup_sample_groups_val["group_two"]`
	if got[1].Expression != wantAlternate {
		t.Errorf("group_val expression = %q, want lookup-only %q", got[1].Expression, wantAlternate)
	}
	for _, binding := range got {
		if contains(binding.Expression, "try(") || contains(binding.Expression, "data.terraform_remote_state") {
			t.Errorf("expression %q retains a try()/remote-state arm, want lookup-only for a probed-unusable referent regardless of space", binding.Expression)
		}
	}
}

// TestRewriteResolverFallbacksWrapsAlternateSpaceWithTryFallback is the
// companion control: absent a lookup-only decision, an alternate-space
// selector gets the ordinary try(remote, lookup) wrap, naming the _<field>
// lookup local.
func TestRewriteResolverFallbacksWrapsAlternateSpaceWithTryFallback(t *testing.T) {
	bindingsByType := map[string][]ExpressionBinding{
		"sample_rule": {
			{
				Key:        "rule_one",
				Path:       "group_val",
				Expression: `data.terraform_remote_state.sample_groups.outputs.iw_reference_ids_val.sample_groups["group_two"]`,
			},
		},
	}
	operatorIdentitiesByType := map[string]map[string]bool{"sample_rule": {}}
	wrapResolverFallbacks(bindingsByType, operatorIdentitiesByType)

	want := `try(data.terraform_remote_state.sample_groups.outputs.iw_reference_ids_val.sample_groups["group_two"], local.iw_reference_lookup_sample_groups_val["group_two"])`
	if got := bindingsByType["sample_rule"][0].Expression; got != want {
		t.Errorf("expression = %q, want %q", got, want)
	}
}

// TestCanonicalRemoteStateSelectorPatternDisambiguatesUnderscoreReferent
// pins the regexp's non-ambiguity when the referent name ITSELF contains an
// underscore run that could be mistaken for a field suffix if the field
// group were allowed to cross the literal "." separating the output name
// from the referent. "sample_val" as a referent (not "val" as the field)
// must still parse correctly whether or not a field suffix is present.
func TestCanonicalRemoteStateSelectorPatternDisambiguatesUnderscoreReferent(t *testing.T) {
	cases := []struct {
		name         string
		expression   string
		wantField    string
		wantReferent string
		wantKey      string
	}{
		{
			name:         "field and underscore referent",
			expression:   `data.terraform_remote_state.root1.outputs.iw_reference_ids_val.sample_val["k"]`,
			wantField:    "_val",
			wantReferent: "sample_val",
			wantKey:      `"k"`,
		},
		{
			name:         "no field, underscore referent",
			expression:   `data.terraform_remote_state.root1.outputs.iw_reference_ids.sample_val["k"]`,
			wantField:    "",
			wantReferent: "sample_val",
			wantKey:      `"k"`,
		},
		{
			name:         "legacy spelling, underscore referent, field",
			expression:   `data.terraform_remote_state.root1.outputs.infrawright_reference_ids_val.sample_val["k"]`,
			wantField:    "_val",
			wantReferent: "sample_val",
			wantKey:      `"k"`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			parts := canonicalRemoteStateSelectorPattern.FindStringSubmatch(tc.expression)
			if len(parts) != 4 {
				t.Fatalf("FindStringSubmatch(%q) = %#v, want 4 parts (whole match + field + referent + key)", tc.expression, parts)
			}
			if parts[1] != tc.wantField || parts[2] != tc.wantReferent || parts[3] != tc.wantKey {
				t.Errorf("parts = field=%q referent=%q key=%q, want field=%q referent=%q key=%q",
					parts[1], parts[2], parts[3], tc.wantField, tc.wantReferent, tc.wantKey)
			}
		})
	}
}

// TestExpressionRemoteStateReferencesParsesAlternateSpaceField pins
// ExpressionRemoteStateReferences's (expression_bindings.go)
// remoteStateSelectorPattern extension: it must admit the sibling selector
// shape and populate RemoteStateReference.IDField, using the same
// referent-with-underscore disambiguation as the renderer-side pattern.
func TestExpressionRemoteStateReferencesParsesAlternateSpaceField(t *testing.T) {
	expression := `data.terraform_remote_state.root1.outputs.iw_reference_ids_val.sample_val["k"]`
	refs, err := ExpressionRemoteStateReferences(expression)
	if err != nil {
		t.Fatalf("ExpressionRemoteStateReferences(%q) error = %v", expression, err)
	}
	want := []RemoteStateReference{{IDField: "val", Key: "k", ResourceType: "sample_val", Root: "root1"}}
	if !reflect.DeepEqual(refs, want) {
		t.Fatalf("refs = %+v, want %+v", refs, want)
	}

	canonicalExpression := `data.terraform_remote_state.root1.outputs.iw_reference_ids.sample_val["k"]`
	canonicalRefs, err := ExpressionRemoteStateReferences(canonicalExpression)
	if err != nil {
		t.Fatalf("ExpressionRemoteStateReferences(%q) error = %v", canonicalExpression, err)
	}
	wantCanonical := []RemoteStateReference{{ResourceType: "sample_val", Root: "root1", Key: "k"}}
	if !reflect.DeepEqual(canonicalRefs, wantCanonical) {
		t.Fatalf("canonical refs = %+v, want %+v", canonicalRefs, wantCanonical)
	}
}

func contains(haystack, needle string) bool {
	return regexp.MustCompile(regexp.QuoteMeta(needle)).MatchString(haystack)
}

// containsNormalized checks for a block's presence ignoring HCL formatting
// differences the identityFormatter (a no-op) leaves in place -- the
// fixture's FormatHcl is the identity function, so bytes are exactly what
// the renderer emitted and this reduces to substring containment; kept as
// its own helper so the test's intent (does this block appear) reads
// independently of that renderer detail.
func containsNormalized(haystack, needle string) bool {
	return contains(haystack, needle)
}
