package metadata

// loader_test.go ports the library-level tests from
// the original test corpus (there is no CLI-subprocess test in
// that file to skip -- every test there exercises this package's library
// surface directly).

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"
)

func committedPackSetPaths(t *testing.T, packsRoot string) []string {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(packsRoot, "*.packset.json"))
	if err != nil {
		t.Fatalf("discover committed pack profiles: %v", err)
	}
	if len(paths) == 0 {
		t.Fatalf("discover committed pack profiles: found no *.packset.json files under %s", packsRoot)
	}
	return paths
}

// TestLoadPackRootExposesGenericResourceSurface ports "committed pack
// metadata exposes the complete generic resource surface".
func TestLoadPackRootExposesGenericResourceSurface(t *testing.T) {
	_, loaded := syntheticLoadedPackRoot(t, "sample")
	metadata := loaded.Packs

	var names []string
	for _, manifest := range metadata.Manifests {
		names = append(names, manifest.Name)
	}
	wantNames := []string{"sample"}
	if !reflect.DeepEqual(names, wantNames) {
		t.Fatalf("manifest names = %v, want %v", names, wantNames)
	}

	wantPrefixes := map[string]string{}
	for _, manifest := range metadata.Manifests {
		for prefix, provider := range manifest.ProviderPrefixes {
			wantPrefixes[prefix] = provider
		}
	}
	if !reflect.DeepEqual(metadata.ProviderPrefixes, wantPrefixes) {
		t.Fatalf("providerPrefixes = %v, want %v", metadata.ProviderPrefixes, wantPrefixes)
	}

	registry := loaded.Registry
	if len(registry.Entries) != len(loaded.Resources) {
		t.Fatalf("registry entries = %d, want one per loaded resource (%d)", len(registry.Entries), len(loaded.Resources))
	}
	resourceTypes := make([]string, 0, len(loaded.Resources))
	for resourceType := range loaded.Resources {
		resourceTypes = append(resourceTypes, resourceType)
	}
	sort.Strings(resourceTypes)
	if len(resourceTypes) == 0 {
		return
	}

	resourceType := resourceTypes[0]
	resource := loaded.Resources[resourceType]
	entry := registry.Entries[resourceType]
	product, _ := entry["product"].(string)
	if resource.Type != resourceType || resource.Product != product || resource.Provider == "" {
		t.Fatalf("unexpected resource shape: %+v", resource)
	}
	if resource.Pack == nil {
		t.Fatalf("resource.Pack = nil, want selected pack owner for %s", resourceType)
	}
	if !reflect.DeepEqual(resource.Registry, entry) {
		t.Fatalf("resource.Registry mismatch")
	}
	if !reflect.DeepEqual(resource.Override, loaded.Overrides.Entries[resourceType]) {
		t.Fatalf("resource.Override mismatch")
	}

	schema, err := loaded.LoadResourceSchema(resourceType)
	if err != nil {
		t.Fatalf("LoadResourceSchema: %v", err)
	}
	if _, ok := schema["block"].(JsonObject); !ok {
		t.Fatalf("%s schema block is not an object: %T", resourceType, schema["block"])
	}
}

func TestLoadPackRootRejectsDataReferentReferenceReferrer(t *testing.T) {
	directory := t.TempDir()
	writeJSONFile(t, filepath.Join(directory, "sample", "pack.json"), JsonObject{
		"pin":               "1.0.0",
		"provider_prefixes": JsonObject{"sample_": "sample"},
		"provider_sources":  JsonObject{"sample": "example/sample"},
		"lookup_sources": JsonObject{
			"sample_data": JsonObject{"name_field": "name"},
		},
		"references": JsonObject{
			"sample_data": JsonObject{
				"target_id": JsonObject{"name_field": "name", "referent": "sample_target"},
			},
		},
	})
	writeJSONFile(t, filepath.Join(directory, "sample", "registry.json"), JsonObject{
		"sample_data": JsonObject{
			"data_referent": true,
			"fetch":         JsonObject{"pagination": "single", "path": "items"},
			"product":       "sample",
		},
		"sample_target": JsonObject{"product": "sample"},
	})
	writeJSONFile(t, filepath.Join(directory, "sample", "schemas", "provider", "sample.json"), JsonObject{
		"resource_schemas": JsonObject{},
		"data_source_schemas": JsonObject{
			"sample_data": JsonObject{},
		},
	})

	_, err := LoadPackRoot(LoadPackRootOptions{PacksRoot: directory})
	if err == nil || !strings.Contains(err.Error(), "references.sample_data.target_id") || !strings.Contains(err.Error(), "sample_data") {
		t.Fatalf("LoadPackRoot(data referent referrer) error = %v, want a refusal naming references.sample_data.target_id and sample_data", err)
	}
}

// TestProviderSchemasResolveThroughPackOwnership ports "provider schemas
// resolve through pack ownership and fail on misspellings".
func TestProviderSchemasResolveThroughPackOwnership(t *testing.T) {
	root := repoRoot(t)
	packsRoot := filepath.Join(root, "packs")
	metadata, err := LoadPackMetadata(packsRoot)
	if err != nil {
		t.Fatalf("LoadPackMetadata: %v", err)
	}
	want := map[string]int{"zcc": 7, "zia": 83, "zpa": 55, "ztc": 16}
	for _, provider := range []string{"zcc", "zia", "zpa", "ztc"} {
		provider := provider
		t.Run(provider, func(t *testing.T) {
			requirePackSelectionAvailable(t, packsRoot, PackSelection{Packs: []string{provider}})
			schema, err := LoadProviderSchema(metadata, provider)
			if err != nil {
				t.Fatalf("LoadProviderSchema(%s): %v", provider, err)
			}
			if got := len(schema.ResourceSchemas); got != want[provider] {
				t.Fatalf("resourceSchemas count = %d, want %d", got, want[provider])
			}
		})
	}

	t.Run("zia resource ownership", func(t *testing.T) {
		requirePackSelectionAvailable(t, packsRoot, PackSelection{Packs: []string{"zia"}})
		category, err := LoadResourceSchema(metadata, "zia_url_categories")
		if err != nil {
			t.Fatalf("LoadResourceSchema: %v", err)
		}
		if _, ok := category["block"].(JsonObject); !ok {
			t.Fatalf("category.block is not an object: %T", category["block"])
		}

		if _, err := LoadResourceSchema(metadata, "zia_url_categoriess"); err == nil || !strings.Contains(err.Error(), "not in zia schema") {
			t.Fatalf("expected 'not in zia schema' error, got %v", err)
		}

		if got := ProviderForResource(metadata, "zia_url_categories"); got != "zia" {
			t.Fatalf("ProviderForResource = %q, want zia", got)
		}
	})
}

// TestPackSetValidationCountsManifestlessDirectoriesFailClosed ports
// "pack-set validation counts manifestless runtime directories
// fail-closed".
func TestPackSetValidationCountsManifestlessDirectoriesFailClosed(t *testing.T) {
	directory := t.TempDir()
	if err := os.Mkdir(filepath.Join(directory, "ghost"), 0o755); err != nil {
		t.Fatalf("mkdir ghost: %v", err)
	}
	profile := filepath.Join(directory, "profile.json")
	writeJSONFile(t, profile, JsonObject{
		"kind": PackSetKind, "version": float64(1), "packs": []string{}, "shared": []string{},
	})
	_, err := ValidateActivePackSet(ValidateActivePackSetOptions{ProfilePath: profile, Root: directory})
	if err == nil || !strings.Contains(err.Error(), "undeclared packs: ghost") {
		t.Fatalf("expected undeclared packs error, got %v", err)
	}
}

// TestPackOwnershipAndSharedComponentsHardFailures ports "pack ownership
// and required shared components remain hard failures".
func TestPackOwnershipAndSharedComponentsHardFailures(t *testing.T) {
	directory := t.TempDir()
	writeJSONFile(t, filepath.Join(directory, "one", "pack.json"), JsonObject{
		"provider_prefixes": map[string]string{"one_": "same"},
		"requires_shared":   []string{"common"},
	})
	writeJSONFile(t, filepath.Join(directory, "two", "pack.json"), JsonObject{
		"provider_prefixes": map[string]string{"two_": "same"},
	})
	if _, err := ValidatePackAuthoring(ValidatePackAuthoringOptions{Root: directory}); err == nil ||
		!strings.Contains(err.Error(), `provider "same" is declared by multiple packs: one, two`) {
		t.Fatalf("expected multiple-packs error, got %v", err)
	}
	if err := os.RemoveAll(filepath.Join(directory, "two")); err != nil {
		t.Fatalf("remove two: %v", err)
	}
	if _, err := ValidatePackAuthoring(ValidatePackAuthoringOptions{Root: directory}); err == nil ||
		!strings.Contains(err.Error(), "pack one requires missing shared component common") {
		t.Fatalf("expected missing shared component error, got %v", err)
	}
}

func TestPackAuthoringIgnoresUnrelatedFiles(t *testing.T) {
	directory := t.TempDir()
	writeJSONFile(t, filepath.Join(directory, "bad-name", "pack.json"), JsonObject{
		"provider_prefixes": map[string]string{"sample_": "sample"},
		"provider_sources":  map[string]string{"sample": "example/sample"},
	})
	writeRawFile(t, filepath.Join(directory, "bad-name", "notes.txt"), "ignored by pack authoring\n")
	pack := "bad-name"
	result, err := ValidatePackAuthoring(ValidatePackAuthoringOptions{Pack: &pack, Root: directory})
	if err != nil {
		t.Fatalf("ValidatePackAuthoring: %v", err)
	}
	if !reflect.DeepEqual(result.Names, []string{"bad-name"}) {
		t.Fatalf("names = %v, want [bad-name]", result.Names)
	}
}

// TestStrictVocabulariesRejectSilentTypos ports "strict profile, registry,
// and override vocabularies reject silent typos".
func TestStrictVocabulariesRejectSilentTypos(t *testing.T) {
	if _, err := ValidatePackSetDocument(JsonObject{
		"kind": PackSetKind, "version": float64(1), "packs": []any{"two", "one"}, "shared": []any{},
	}, "profile.json", PackSetKind); err == nil || !strings.Contains(err.Error(), "packs must be sorted") {
		t.Fatalf("expected packs-must-be-sorted error, got %v", err)
	}

	if _, err := ValidateRegistry(JsonObject{
		"sample_resource": JsonObject{
			"product": "sample",
			"fetch":   JsonObject{"path": "/items", "pagination": "singel"},
		},
	}, "registry.json"); err == nil || !strings.Contains(err.Error(), `unsupported value "singel"`) {
		t.Fatalf("expected unsupported pagination error, got %v", err)
	}

	if _, err := ValidateRegistry(JsonObject{
		"sample_resource": JsonObject{"product": "sample", "slug_group": "false"},
	}, "registry.json"); err == nil || !strings.Contains(err.Error(), "slug_group has been removed; see docs/state-topology.md") {
		t.Fatalf("expected slug_group retirement error, got %v", err)
	}

	if _, err := ValidateOverride(JsonObject{"rename": JsonObject{"one": "two"}}, "override.json"); err == nil ||
		!strings.Contains(err.Error(), "unknown override key rename") {
		t.Fatalf("expected unknown override key error, got %v", err)
	}
}

func TestModuleSingleBlockOverrideRequiresCanonicalUniquePaths(t *testing.T) {
	valid, err := ValidateOverride(JsonObject{
		"module_single_blocks": []any{"outer.inner", "singleton"},
	}, "override.json")
	if err != nil {
		t.Fatalf("ValidateOverride(valid module_single_blocks) error = %v, want nil", err)
	}
	if got := valid["module_single_blocks"].([]any); !reflect.DeepEqual(got, []any{"outer.inner", "singleton"}) {
		t.Errorf("module_single_blocks = %v, want preserved paths", got)
	}

	tests := []struct {
		name      string
		value     any
		wantError string
	}{
		{"not an array", "singleton", "must be an array"},
		{"empty", []any{}, "must not be empty"},
		{"non-string", []any{float64(1)}, "must be a non-empty dotted block path"},
		{"empty segment", []any{"outer..inner"}, "must be a canonical dotted block path"},
		{"noncanonical segment", []any{"outer.Inner"}, "must be a canonical dotted block path"},
		{"duplicate", []any{"singleton", "singleton"}, "contains duplicate path"},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := ValidateOverride(JsonObject{"module_single_blocks": testCase.value}, "override.json")
			if err == nil || !strings.Contains(err.Error(), testCase.wantError) {
				t.Fatalf("ValidateOverride(module_single_blocks) error = %v, want error containing %q", err, testCase.wantError)
			}
		})
	}
}

func TestRegistryResourceKeysRequireCanonicalTerraformTypes(t *testing.T) {
	valid, err := ValidateRegistry(JsonObject{
		"sample_resource_2": JsonObject{"product": "sample"},
	}, "registry.json")
	if err != nil {
		t.Fatalf("ValidateRegistry(valid key) error = %v, want nil", err)
	}
	if _, ok := valid["sample_resource_2"]; !ok {
		t.Error("ValidateRegistry(valid key) omitted sample_resource_2")
	}

	invalid := []string{
		"sample_Foo",
		"sample-foo",
		"2sample_resource",
		"sample_caf\u00e9",
		"sample_cafe\u0301",
	}
	for _, resourceType := range invalid {
		t.Run(resourceType, func(t *testing.T) {
			_, err := ValidateRegistry(JsonObject{
				resourceType: JsonObject{"product": "sample"},
			}, "registry.json")
			if err == nil || !strings.Contains(err.Error(), "must match ^[a-z][a-z0-9_]*$") {
				t.Errorf("ValidateRegistry(resourceType=%q) error = %v, want canonical-resource-type error", resourceType, err)
			}
		})
	}
}

func TestRegistryDataReferent(t *testing.T) {
	tests := []struct {
		name      string
		entry     JsonObject
		wantError string
	}{
		{
			name: "valid",
			entry: JsonObject{
				"data_referent": true,
				"fetch":         JsonObject{"pagination": "zia", "path": "locations/groups"},
				"product":       "sample",
			},
		},
		{
			name: "requires fetch",
			entry: JsonObject{
				"data_referent": true,
				"product":       "sample",
			},
			wantError: "data_referent=true requires registry.json.sample_resource.fetch",
		},
		{
			name: "requires a fetch object, not a declared-unfetchable entry",
			entry: JsonObject{
				"data_referent":     true,
				"fetch":             false,
				"fetch_skip_reason": "no list endpoint",
				"product":           "sample",
			},
			wantError: "data_referent=true requires registry.json.sample_resource.fetch",
		},
		{
			name: "allows explicit generate false",
			entry: JsonObject{
				"data_referent": true,
				"fetch":         JsonObject{"pagination": "zia", "path": "locations/groups"},
				"generate":      false,
				"product":       "sample",
			},
		},
		{
			name: "false leaves generate and adopt unconstrained",
			entry: JsonObject{
				"adopt":         JsonObject{"key_field": "name"},
				"data_referent": false,
				"fetch":         JsonObject{"pagination": "zia", "path": "locations"},
				"generate":      true,
				"product":       "sample",
			},
		},
		{
			name: "forbids generated resource",
			entry: JsonObject{
				"data_referent": true,
				"fetch":         JsonObject{"pagination": "zia", "path": "locations/groups"},
				"generate":      true,
				"product":       "sample",
			},
			wantError: "data_referent=true forbids registry.json.sample_resource.generate=true",
		},
		{
			name: "forbids adoption",
			entry: JsonObject{
				"adopt":         JsonObject{"key_field": "name"},
				"data_referent": true,
				"fetch":         JsonObject{"pagination": "zia", "path": "locations/groups"},
				"product":       "sample",
			},
			wantError: "data_referent=true forbids registry.json.sample_resource.adopt",
		},
		{
			name: "forbids derivation",
			entry: JsonObject{
				"data_referent": true,
				"derive":        JsonObject{"from": "sample_source"},
				"fetch":         JsonObject{"pagination": "zia", "path": "locations/groups"},
				"product":       "sample",
			},
			wantError: "data_referent=true forbids registry.json.sample_resource.derive",
		},
		{
			name: "requires boolean",
			entry: JsonObject{
				"data_referent": "yes",
				"product":       "sample",
			},
			wantError: "registry.json.sample_resource.data_referent must be a boolean",
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			got, err := ValidateRegistry(JsonObject{"sample_resource": testCase.entry}, "registry.json")
			if testCase.wantError == "" {
				if err != nil {
					t.Fatalf("ValidateRegistry(%q) error = %v, want nil", testCase.name, err)
				}
				resource, ok := got["sample_resource"].(JsonObject)
				if !ok {
					t.Fatalf("ValidateRegistry(%q) resource = %T, want JsonObject", testCase.name, got["sample_resource"])
				}
				if want, present := testCase.entry["data_referent"]; present && resource["data_referent"] != want {
					t.Errorf("ValidateRegistry(%q)[data_referent] = %v, want %v", testCase.name, resource["data_referent"], want)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), testCase.wantError) {
				t.Errorf("ValidateRegistry(%q) error = %v, want error containing %q", testCase.name, err, testCase.wantError)
			}
		})
	}
}

func cloneRule(rule JsonObject, overrides JsonObject) JsonObject {
	out := make(JsonObject, len(rule)+len(overrides))
	for k, v := range rule {
		out[k] = v
	}
	for k, v := range overrides {
		out[k] = v
	}
	return out
}

// TestUnsupportedAdoptionMetadataClosedVersionScopedForbiddenOnDerived
// ports "unsupported adoption metadata is closed, version-scoped, and
// forbidden on derived resources".
func TestUnsupportedAdoptionMetadataClosedVersionScopedForbiddenOnDerived(t *testing.T) {
	rule := JsonObject{
		"evidence": []any{"https://example.invalid/provider-source"},
		"match":    JsonObject{"action": "ISOLATE"},
		"provider": JsonObject{"source": "example/sample", "version": "1.2.3"},
		"reason":   "provider cannot round-trip this object",
	}
	nonemptyRule := JsonObject{
		"evidence":           []any{"https://example.invalid/provider-source"},
		"match_any_nonempty": []any{"endPointApplications", "endPointApplicationGroups"},
		"provider":           JsonObject{"source": "example/sample", "version": "1.2.3"},
		"reason":             "provider cannot round-trip populated collections",
	}

	if _, err := ValidateRegistry(JsonObject{
		"sample_resource": JsonObject{
			"adopt":   JsonObject{"unsupported_if": []any{rule}},
			"product": "sample",
		},
	}, "registry.json"); err != nil {
		t.Fatalf("expected valid unsupported_if rule to pass, got %v", err)
	}
	if _, err := ValidateRegistry(JsonObject{
		"sample_resource": JsonObject{
			"adopt":   JsonObject{"unsupported_if": []any{nonemptyRule}},
			"product": "sample",
		},
	}, "registry.json"); err != nil {
		t.Fatalf("expected valid match_any_nonempty rule to pass, got %v", err)
	}

	cases := []any{
		JsonObject{},
		[]any{},
		[]any{cloneRule(rule, JsonObject{"evidence": []any{}})},
		[]any{cloneRule(rule, JsonObject{"evidence": []any{"same", "same"}})},
		[]any{cloneRule(rule, JsonObject{"match": JsonObject{}})},
		[]any{cloneRule(rule, JsonObject{"match": JsonObject{"nested": JsonObject{"value": true}}})},
		[]any{cloneRule(rule, JsonObject{"provider": JsonObject{"source": "example/sample"}})},
		[]any{cloneRule(rule, JsonObject{"reason": ""})},
		[]any{cloneRule(rule, JsonObject{"unexpected": true})},
		[]any{cloneRule(rule, JsonObject{"match_any_nonempty": []any{"items"}})},
		[]any{JsonObject{
			"evidence": []any{"fixture"}, "provider": JsonObject{"source": "example/sample", "version": "1.2.3"}, "reason": "missing predicate",
		}},
		[]any{cloneRule(nonemptyRule, JsonObject{"match_any_nonempty": []any{}})},
		[]any{cloneRule(nonemptyRule, JsonObject{"match_any_nonempty": []any{"items", json.Number("1")}})},
		[]any{cloneRule(nonemptyRule, JsonObject{"match_any_nonempty": []any{"endPointApplications", "end_point_applications"}})},
		[]any{rule, rule},
		[]any{nonemptyRule, cloneRule(nonemptyRule, JsonObject{"match_any_nonempty": []any{"endPointApplicationGroups", "endPointApplications"}})},
	}
	keywords := regexp.MustCompile(`unsupported_if|evidence|match|predicate|provider|reason|unknown`)
	for i, unsupportedIf := range cases {
		_, err := ValidateRegistry(JsonObject{
			"sample_resource": JsonObject{
				"adopt":   JsonObject{"unsupported_if": unsupportedIf},
				"product": "sample",
			},
		}, "registry.json")
		if err == nil {
			t.Fatalf("case %d: expected error, got none", i)
		}
		if !keywords.MatchString(err.Error()) {
			t.Fatalf("case %d: error %q does not match expected keywords", i, err.Error())
		}
	}
	if _, err := ValidateRegistry(JsonObject{
		"sample_resource": JsonObject{
			"adopt": JsonObject{
				"identity_renames": JsonObject{"endPointApplications": "applications"},
				"unsupported_if":   []any{nonemptyRule},
			},
			"product": "sample",
		},
	}, "registry.json"); err == nil || !strings.Contains(err.Error(), "reference renamed field") {
		t.Fatalf("expected match_any_nonempty rename-conflict error, got %v", err)
	}

	if _, err := ValidateRegistry(JsonObject{
		"sample_resource": JsonObject{
			"adopt":   JsonObject{"unsupported_if": []any{rule}},
			"derive":  JsonObject{"from": "sample_source"},
			"product": "sample",
		},
	}, "registry.json"); err == nil || !strings.Contains(err.Error(), "not valid for a derived resource") {
		t.Fatalf("expected derived-resource error, got %v", err)
	}

	if _, err := ValidateOverride(JsonObject{"unsupported_if": []any{rule}}, "override.json"); err == nil ||
		!strings.Contains(err.Error(), "unknown override key unsupported_if") {
		t.Fatalf("expected unknown override key error, got %v", err)
	}

	directory := t.TempDir()
	writeJSONFile(t, filepath.Join(directory, "sample", "pack.json"), JsonObject{
		"pin":               "1.2.3",
		"provider_prefixes": map[string]string{"sample_": "sample"},
		"provider_sources":  map[string]string{"sample": "example/sample"},
	})
	writeJSONFile(t, filepath.Join(directory, "sample", "registry.json"), JsonObject{
		"sample_resource": JsonObject{
			"adopt":   JsonObject{"unsupported_if": []any{rule}},
			"product": "sample",
		},
	})
	if _, err := LoadPackRoot(LoadPackRootOptions{PacksRoot: directory}); err != nil {
		t.Fatalf("expected LoadPackRoot to succeed, got %v", err)
	}

	for _, mutation := range []struct{ field, value string }{
		{"source", "example/other"},
		{"version", "9.9.9"},
	} {
		mutatedProvider := JsonObject{"source": "example/sample", "version": "1.2.3"}
		mutatedProvider[mutation.field] = mutation.value
		writeJSONFile(t, filepath.Join(directory, "sample", "registry.json"), JsonObject{
			"sample_resource": JsonObject{
				"adopt": JsonObject{"unsupported_if": []any{JsonObject{
					"evidence": []any{"https://example.invalid/provider-source"},
					"match":    JsonObject{"action": "ISOLATE"},
					"provider": mutatedProvider,
					"reason":   "provider cannot round-trip this object",
				}}},
				"product": "sample",
			},
		})
		_, err := LoadPackRoot(LoadPackRootOptions{PacksRoot: directory})
		pattern := regexp.MustCompile(`provider\.` + mutation.field + `.*does not match active provider`)
		if err == nil || !pattern.MatchString(err.Error()) {
			t.Fatalf("mutation %s: expected mismatch error, got %v", mutation.field, err)
		}
	}
}

// TestRegistryFetchPathsRejectUnsafeInputs ports "registry fetch paths
// reject inputs that WHATWG URLs would silently normalize".
func TestRegistryFetchPathsRejectUnsafeInputs(t *testing.T) {
	registryFor := func(pathValue string, expansion *string) JsonObject {
		fetch := JsonObject{"pagination": "single", "path": pathValue}
		if expansion != nil {
			fetch["expand"] = JsonObject{"item": []any{*expansion}}
		}
		return JsonObject{"sample_resource": JsonObject{"product": "sample", "fetch": fetch}}
	}

	mustContainPattern := regexp.MustCompile(`fetch\.path must not contain`)
	for _, value := range []string{
		`items\admin`, "items?scope=admin", "items#admin", "items/../admin",
		"items/.%2E/admin", "items/%2e./admin",
	} {
		_, err := ValidateRegistry(registryFor(value, nil), "registry.json")
		if err == nil || !mustContainPattern.MatchString(err.Error()) {
			t.Fatalf("value %q: expected fetch.path violation, got %v", value, err)
		}
	}

	rfcPattern := regexp.MustCompile(`RFC 3986 path characters`)
	for _, value := range []string{"items admin", "items/é", "items/%zz"} {
		_, err := ValidateRegistry(registryFor(value, nil), "registry.json")
		if err == nil || !rfcPattern.MatchString(err.Error()) {
			t.Fatalf("value %q: expected RFC 3986 violation, got %v", value, err)
		}
	}

	bracePattern := regexp.MustCompile(`undeclared expansion braces`)
	for _, value := range []string{"items/{literal}", "items/{item}/{other}"} {
		var expansion *string
		if strings.Contains(value, "{item}") {
			safe := "safe"
			expansion = &safe
		}
		_, err := ValidateRegistry(registryFor(value, expansion), "registry.json")
		if err == nil || !bracePattern.MatchString(err.Error()) {
			t.Fatalf("value %q: expected undeclared braces violation, got %v", value, err)
		}
	}

	dotPattern := regexp.MustCompile(`fetch\.expand\.item\[0\] must not be`)
	for _, value := range []string{".", ".."} {
		value := value
		_, err := ValidateRegistry(registryFor("items/{item}", &value), "registry.json")
		if err == nil || !dotPattern.MatchString(err.Error()) {
			t.Fatalf("expansion %q: expected dot-segment violation, got %v", value, err)
		}
	}

	okCases := []struct {
		path      string
		expansion string
	}{
		{"items/{item}", "slash/value"},
		{"items/{item}/{item}", "safe"},
		{"items/{item}", "nested/../value?#\\"},
		{"items/{item}", "%2e"},
	}
	for _, c := range okCases {
		expansion := c.expansion
		if _, err := ValidateRegistry(registryFor(c.path, &expansion), "registry.json"); err != nil {
			t.Fatalf("path %q expansion %q: expected success, got %v", c.path, c.expansion, err)
		}
	}
}

// TestMetadataLoadingPreservesFetchQueryNumberTokens ports "metadata
// loading preserves fetch query number tokens and wide integers".
func TestMetadataLoadingPreservesFetchQueryNumberTokens(t *testing.T) {
	directory := t.TempDir()
	writeJSONFile(t, filepath.Join(directory, "sample", "pack.json"), JsonObject{
		"provider_prefixes": map[string]string{"sample_": "sample"},
	})
	if err := os.MkdirAll(filepath.Join(directory, "sample", "overrides"), 0o755); err != nil {
		t.Fatalf("mkdir overrides: %v", err)
	}
	writeRawFile(t, filepath.Join(directory, "sample", "registry.json"),
		`{"sample_resource":{"product":"sample","fetch":{"pagination":"single","path":"/items","query":{"safe":9007199254740991,"wide":9007199254740993,"decimal":1.0,"exponent":1e0,"negative_zero":-0.0}}}}`,
	)
	writeRawFile(t, filepath.Join(directory, "sample", "overrides", "sample_resource.json"),
		`{"defaults":{"wide":9007199254740993}}`,
	)
	profile := filepath.Join(directory, "profile.json")
	writeRawFile(t, profile, `{"kind":"infrawright.pack-set","version":1,"packs":["sample"],"shared":[]}`)

	loaded, err := LoadPackRoot(LoadPackRootOptions{PacksRoot: directory, ProfilePath: &profile})
	if err != nil {
		t.Fatalf("LoadPackRoot: %v", err)
	}
	resource, ok := loaded.Resources["sample_resource"]
	if !ok {
		t.Fatal("sample_resource missing")
	}
	fetch, ok := resource.Registry["fetch"].(JsonObject)
	if !ok {
		t.Fatalf("fetch is not an object: %T", resource.Registry["fetch"])
	}
	query, ok := fetch["query"].(JsonObject)
	if !ok {
		t.Fatalf("query is not an object: %T", fetch["query"])
	}
	checkToken := func(key, want string) {
		t.Helper()
		n, ok := query[key].(json.Number)
		if !ok {
			t.Fatalf("query[%s] is not a json.Number: %T (%v)", key, query[key], query[key])
		}
		if n.String() != want {
			t.Fatalf("query[%s] = %s, want %s", key, n.String(), want)
		}
	}
	checkToken("safe", "9007199254740991")
	checkToken("wide", "9007199254740993")
	checkToken("decimal", "1.0")
	checkToken("exponent", "1e0")
	checkToken("negative_zero", "-0.0")

	if resource.Override == nil {
		t.Fatal("override missing")
	}
	defaults, ok := resource.Override["defaults"].(JsonObject)
	if !ok {
		t.Fatalf("defaults is not an object: %T", resource.Override["defaults"])
	}
	wideDefault, ok := defaults["wide"].(json.Number)
	if !ok {
		t.Fatalf("defaults.wide is not a json.Number: %T", defaults["wide"])
	}
	if wideDefault.String() != "9007199254740993" {
		t.Fatalf("defaults.wide = %s, want 9007199254740993", wideDefault.String())
	}

	writeRawFile(t, profile, `{"kind":"infrawright.pack-set","version":1.0,"packs":["sample"],"shared":[]}`)
	if _, err := ValidateActivePackSet(ValidateActivePackSetOptions{ProfilePath: profile, Root: directory}); err == nil ||
		!strings.Contains(err.Error(), "version must be 1") {
		t.Fatalf("expected version-must-be-1 error, got %v", err)
	}

	writeRawFile(t, filepath.Join(directory, "sample", "registry.json"),
		`{"sample_resource":{"product":"sample","fetch":{"pagination":"single","path":"/items","query":9007199254740993}}}`,
	)
	if _, err := LoadPackRoot(LoadPackRootOptions{PacksRoot: directory}); err == nil ||
		!strings.Contains(err.Error(), "fetch.query must be an object") {
		t.Fatalf("expected fetch.query object error, got %v", err)
	}
}

// TestAllCommittedPackProfilesLoadFromReducedRoots ports "all committed
// pack profiles load from physically reduced roots".
func TestAllCommittedPackProfilesLoadFromReducedRoots(t *testing.T) {
	root := repoRoot(t)
	for _, profilePath := range committedPackSetPaths(t, filepath.Join(root, "packs")) {
		name := strings.TrimSuffix(filepath.Base(profilePath), ".packset.json")
		t.Run(name, func(t *testing.T) {
			raw, err := os.ReadFile(profilePath)
			if err != nil {
				t.Fatalf("reading %s: %v", profilePath, err)
			}
			var profile struct {
				Packs  []string `json:"packs"`
				Shared []string `json:"shared"`
			}
			if err := json.Unmarshal(raw, &profile); err != nil {
				t.Fatalf("unmarshal %s: %v", profilePath, err)
			}
			requirePackSelectionAvailable(t, filepath.Join(root, "packs"), PackSelection{
				Packs: profile.Packs, Shared: profile.Shared,
			})

			directory := t.TempDir()
			for _, packName := range profile.Packs {
				if err := copyDir(filepath.Join(root, "packs", packName), filepath.Join(directory, packName)); err != nil {
					t.Fatalf("copy pack %s: %v", packName, err)
				}
			}
			for _, sharedName := range profile.Shared {
				if err := copyDir(
					filepath.Join(root, "packs", "_shared", sharedName),
					filepath.Join(directory, "_shared", sharedName),
				); err != nil {
					t.Fatalf("copy shared %s: %v", sharedName, err)
				}
			}

			loaded, err := LoadPackRoot(LoadPackRootOptions{
				PacksRoot:   directory,
				ProfilePath: &profilePath,
			})
			if err != nil {
				t.Fatalf("LoadPackRoot: %v", err)
			}
			wantActive := PackSelection{Packs: profile.Packs, Shared: profile.Shared}
			if !reflect.DeepEqual(loaded.Active, wantActive) {
				t.Fatalf("loaded.Active = %+v, want %+v", loaded.Active, wantActive)
			}
			active, err := ActivePackSelection(directory)
			if err != nil {
				t.Fatalf("ActivePackSelection: %v", err)
			}
			if !reflect.DeepEqual(active, loaded.Active) {
				t.Fatalf("ActivePackSelection(directory) = %+v, want %+v (loaded.Active)", active, loaded.Active)
			}
		})
	}
}

func TestCommittedPackSetPathsTrackTheAvailableProfileSet(t *testing.T) {
	root := t.TempDir()
	first := filepath.Join(root, "first.packset.json")
	second := filepath.Join(root, "second.packset.json")
	writeRawFile(t, first, `{}`)
	writeRawFile(t, second, `{}`)

	if got, want := committedPackSetPaths(t, root), []string{first, second}; !reflect.DeepEqual(got, want) {
		t.Fatalf("committedPackSetPaths() = %v, want %v", got, want)
	}
	if err := os.Remove(first); err != nil {
		t.Fatalf("remove profile mutation: %v", err)
	}
	if got, want := committedPackSetPaths(t, root), []string{second}; !reflect.DeepEqual(got, want) {
		t.Fatalf("committedPackSetPaths() after removal = %v, want %v", got, want)
	}
}

// TestCommittedPackProfilesAreDerivable proves every checked-in profile names
// installed packs and carries exactly their declared shared-component closure.
// Profile composition is distribution policy, not a name-based engine rule.
func TestCommittedPackProfilesAreDerivable(t *testing.T) {
	root := repoRoot(t)
	packsRoot := filepath.Join(root, "packs")
	metadata, err := LoadPackMetadata(packsRoot)
	if err != nil {
		t.Fatalf("LoadPackMetadata(%q) error = %v, want nil", packsRoot, err)
	}

	manifestByName := make(map[string]PackManifest, len(metadata.Manifests))
	for _, manifest := range metadata.Manifests {
		manifestByName[manifest.Name] = manifest
	}
	for _, profilePath := range committedPackSetPaths(t, packsRoot) {
		profileName := strings.TrimSuffix(filepath.Base(profilePath), ".packset.json")
		t.Run(profileName, func(t *testing.T) {
			profile, loadErr := LoadPackSetDocument(profilePath, PackSetKind)
			if loadErr != nil {
				t.Fatalf("LoadPackSetDocument(%q) error = %v, want nil", profilePath, loadErr)
			}
			requirePackSelectionAvailable(t, packsRoot, profile.PackSelection)
			sharedSet := make(map[string]struct{})
			for _, packName := range profile.Packs {
				manifest, ok := manifestByName[packName]
				if !ok {
					t.Fatalf("profile %q names missing pack %q", profileName, packName)
				}
				for _, dependency := range manifest.RequiresShared {
					sharedSet[dependency] = struct{}{}
				}
			}
			selectedShared := make([]string, 0, len(sharedSet))
			for name := range sharedSet {
				selectedShared = append(selectedShared, name)
			}
			sort.Strings(selectedShared)

			if !reflect.DeepEqual(profile.Shared, selectedShared) {
				t.Errorf("LoadPackSetDocument(%q).Shared = %v, want required shared closure %v", profilePath, profile.Shared, selectedShared)
			}
		})
	}
}

// TestRegistryFetchSkipMustBeDeclaredAsData pins the escape hatch that makes
// the committed-config-implies-fetchable invariant safe to enforce. Every rule
// here exists so the declaration cannot decay into a silent gap: a reason
// without a skip is stale, a skip without a reason is the same blindness in a
// different spelling, and registry.json is strict JSON so a comment cannot
// carry any of it.
func TestRegistryFetchSkipMustBeDeclaredAsData(t *testing.T) {
	tests := []struct {
		name    string
		entry   JsonObject
		wantErr string
	}{
		{
			name:    "skip without a reason",
			entry:   JsonObject{"product": "sample", "fetch": false},
			wantErr: "fetch is false but registry.json.sample_resource.fetch_skip_reason is missing",
		},
		{
			name:    "empty reason",
			entry:   JsonObject{"product": "sample", "fetch": false, "fetch_skip_reason": ""},
			wantErr: "fetch_skip_reason",
		},
		{
			name:    "reason without a skip",
			entry:   JsonObject{"product": "sample", "fetch_skip_reason": "stale"},
			wantErr: `fetch_skip_reason is only valid with "fetch": false`,
		},
		{
			name: "reason alongside a real fetch block",
			entry: JsonObject{
				"product":           "sample",
				"fetch":             JsonObject{"path": "/items", "pagination": "single"},
				"fetch_skip_reason": "contradicts the block above",
			},
			wantErr: `fetch_skip_reason is only valid with "fetch": false`,
		},
		{
			name:    "fetch true says nothing about how to fetch",
			entry:   JsonObject{"product": "sample", "fetch": true},
			wantErr: "true does not describe how to fetch",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := ValidateRegistry(JsonObject{"sample_resource": test.entry}, "registry.json")
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("ValidateRegistry(%s) error = %v, want one containing %q", test.name, err, test.wantErr)
			}
		})
	}

	if _, err := ValidateRegistry(JsonObject{
		"sample_resource": JsonObject{
			"product":           "sample",
			"fetch":             false,
			"fetch_skip_reason": "generate-only; the API exposes no read for this type",
		},
	}, "registry.json"); err != nil {
		t.Errorf("ValidateRegistry(declared unfetchable) error = %v, want nil", err)
	}
}
