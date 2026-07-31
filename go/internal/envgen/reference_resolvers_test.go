package envgen

// These tests pin Task 3 of the reference-tokens plan: when a root's
// committed config carries qualified reference tokens, the renderer emits
// lookup-first resolvers -- try(<canonical remote-state selector>,
// local.<lookup>["<key>"]) -- plus one plan-time lookup local per referent
// reading the committed lookup sidecar, and the state-probe drop filter is
// superseded (the fallback now lives in-language). Untokenised roots render
// byte-identically to today. See
// docs/superpowers/specs/2026-07-30-reference-tokens-design.md.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dvmrry/infrawright-dev/go/internal/metadata"
)

// segmentGroupLookupContent is the referent lookup tokenise/tokeniseAtLegacyPath
// commit, factored out so both write the same bytes to their different
// locations.
func segmentGroupLookupContent() map[string]any {
	return map[string]any{
		"by_id":     map[string]any{"sg-1": "Segment One"},
		"id_by_key": map[string]any{"segment_one": "sg-1"},
		"key_by_id": map[string]any{"sg-1": "segment_one"},
	}
}

// tokenise rewrites the state-aware fixture's committed artifacts into the
// post-substitution shape: the tfvars reference leaf carries the qualified
// token and the referent's lookup exists (at its current
// config/<tenant>/lookups/ location) with both directions.
func (f stateAwareFixture) tokenise(t *testing.T) {
	t.Helper()
	config := filepath.Join(f.workspace, "config", "tenant")
	writeJSONFile(t, filepath.Join(config, "zpa_application_segment.auto.tfvars.json"), map[string]any{
		"items": map[string]any{"app_one": map[string]any{"segment_group_id": "zpa_segment_group.segment_one"}},
	})
	writeJSONFile(t, filepath.Join(config, "lookups", "zpa_segment_group.lookup.json"), segmentGroupLookupContent())
}

// tokeniseAtLegacyPath is tokenise's Part B migration-bridge counterpart:
// the tfvars reference leaf carries the qualified token exactly as tokenise
// writes it, but the referent's lookup is committed only at its pre-migration
// location (config/<tenant>/<type>.lookup.json, no lookups/ subdirectory) --
// the shape a tenant that has not re-transformed since the lookup migration
// still has on disk.
func (f stateAwareFixture) tokeniseAtLegacyPath(t *testing.T) {
	t.Helper()
	config := filepath.Join(f.workspace, "config", "tenant")
	writeJSONFile(t, filepath.Join(config, "zpa_application_segment.auto.tfvars.json"), map[string]any{
		"items": map[string]any{"app_one": map[string]any{"segment_group_id": "zpa_segment_group.segment_one"}},
	})
	writeJSONFile(t, filepath.Join(config, "zpa_segment_group.lookup.json"), segmentGroupLookupContent())
}

func (f stateAwareFixture) readReferrerFile(t *testing.T, name string) string {
	t.Helper()
	content, err := os.ReadFile(f.referrerFile(name))
	if err != nil {
		t.Fatalf("os.ReadFile(%s) error = %v, want nil", name, err)
	}
	return string(content)
}

// committedBindingsFile is the generated-bindings cache the migration bridge
// prefers when it exists: the file Part A of the sidecar-minimization design
// makes optional.
func (f stateAwareFixture) committedBindingsFile() string {
	return filepath.Join(f.workspace, "config", "tenant", "zpa_application_segment.generated.expressions.json")
}

func (f stateAwareFixture) removeCommittedBindings(t *testing.T) {
	t.Helper()
	if err := os.Remove(f.committedBindingsFile()); err != nil {
		t.Fatalf("remove committed bindings: %v", err)
	}
}

// TestTokenisedRootEmitsLookupFirstResolvers pins the resolver shape: the
// canonical selector wrapped lookup-first with the lookup literal as the
// fallback arm, and the lookup local reading the committed sidecar at plan
// time through the relative path -- the renderer inlines no value from it.
func TestTokenisedRootEmitsLookupFirstResolvers(t *testing.T) {
	fixture := newStateAwareFixture(t)
	fixture.tokenise(t)

	fixture.generate(t, false)

	bindings := fixture.readReferrerFile(t, "expression_bindings.tf")
	wantResolver := `try(data.terraform_remote_state.zpa_segment_group.outputs.infrawright_reference_ids.zpa_segment_group["segment_one"], local.infrawright_reference_lookup_zpa_segment_group["segment_one"])`
	if !strings.Contains(bindings, wantResolver) {
		t.Errorf("expression_bindings.tf = %q, want resolver %q", bindings, wantResolver)
	}
	if !strings.Contains(bindings, "infrawright_reference_lookup_zpa_segment_group") ||
		!strings.Contains(bindings, "fileexists(") ||
		!strings.Contains(bindings, "zpa_segment_group.lookup.json") {
		t.Errorf("expression_bindings.tf = %q, want a fileexists-guarded lookup local over the committed sidecar", bindings)
	}
	main := fixture.readReferrerFile(t, "main.tf")
	if !strings.Contains(main, `data "terraform_remote_state" "zpa_segment_group"`) {
		t.Errorf("main.tf = %q, want the referent's remote-state reader", main)
	}
}

// TestTokenisedRootFallsBackToLegacyLookupPath pins Part B's migration bridge
// end to end: a tenant that has not re-transformed since the lookup moved to
// config/<tenant>/lookups/ still has its lookup only at the pre-migration
// config/<tenant>/<type>.lookup.json path. Render-derivation must still
// resolve the token through it, and -- the plan-breaking hazard Part B's
// design doc calls out -- the emitted lookup local's file() expression must
// name the path that actually exists on disk (the legacy one), never the
// absent lookups/ path: pointing fileexists() at a file that will never
// exist would silently and permanently disable the plan-time fallback arm.
func TestTokenisedRootFallsBackToLegacyLookupPath(t *testing.T) {
	fixture := newStateAwareFixture(t)
	fixture.tokeniseAtLegacyPath(t)
	fixture.removeCommittedBindings(t)

	diagnostics := fixture.generate(t, false)
	if !containsString(diagnostics, "NOTE bindings: zpa_application_segment: 1 bound, 0 skipped") {
		t.Fatalf("diagnostics = %v, want the render-derivation accounting over the legacy-path lookup", diagnostics)
	}

	bindings := fixture.readReferrerFile(t, expressionBindingsTF)
	wantResolver := `try(data.terraform_remote_state.zpa_segment_group.outputs.infrawright_reference_ids.zpa_segment_group["segment_one"], local.infrawright_reference_lookup_zpa_segment_group["segment_one"])`
	if !strings.Contains(bindings, wantResolver) {
		t.Errorf("expression_bindings.tf = %q, want resolver %q derived through the legacy-path lookup", bindings, wantResolver)
	}
	if !strings.Contains(bindings, "config/tenant/zpa_segment_group.lookup.json") {
		t.Errorf("expression_bindings.tf = %q, want the lookup local's file() path naming the legacy location", bindings)
	}
	if strings.Contains(bindings, "config/tenant/lookups/zpa_segment_group.lookup.json") {
		t.Errorf("expression_bindings.tf = %q, want the lookup local NOT to name the (nonexistent) current lookups/ path", bindings)
	}
}

// TestTokenisedRootSupersedesProbeFilter pins the probe hand-off: with the
// fallback in-language, state-aware generation must keep every binding and
// never consult the probe for a tokenised root.
func TestTokenisedRootSupersedesProbeFilter(t *testing.T) {
	fixture := newStateAwareFixture(t)
	fixture.tokenise(t)

	var probeCalls []string
	diagnostics := make([]string, 0)
	outputRoot := fixture.outputRoot
	if _, err := GenerateEnvironmentRoots(GenerateEnvironmentRootsOptions{
		Deployment:   loadDeploymentFile(t, fixture.deploymentPath),
		FormatHcl:    identityFormatter,
		OnDiagnostic: func(message string) { diagnostics = append(diagnostics, message) },
		OutputRoot:   &outputRoot,
		Root:         syntheticRootForTopology(t),
		Selectors:    []string{"zpa_application_segment"},
		StateAware:   true,
		StateProbe: func(rootLabel, referentType string) (StateProbeResult, error) {
			probeCalls = append(probeCalls, rootLabel+"/"+referentType)
			return StateProbeResult{Usable: false}, nil
		},
		Tenant: "tenant",
	}); err != nil {
		t.Fatalf("GenerateEnvironmentRoots error = %v, want nil", err)
	}
	if len(probeCalls) != 0 {
		t.Errorf("probe calls = %v, want none for a tokenised root (fallback is in-language)", probeCalls)
	}
	bindings := fixture.readReferrerFile(t, "expression_bindings.tf")
	if !strings.Contains(bindings, "try(data.terraform_remote_state.zpa_segment_group") {
		t.Errorf("expression_bindings.tf = %q, want the resolver kept despite absent state", bindings)
	}
}

// TestTokenisedRootWithoutBindingsFailsLoudly pins the mid-migration
// misconfiguration: tokens in committed config with no binding evidence at
// all cannot silently reach the module's type boundary; generation refuses,
// naming the root and the remedy. Since Part A the absent cache alone is no
// longer "no evidence" -- render-derivation would serve it from the lookup --
// so the lookup is removed too, leaving the tokens genuinely unresolvable.
func TestTokenisedRootWithoutBindingsFailsLoudly(t *testing.T) {
	fixture := newStateAwareFixture(t)
	fixture.tokenise(t)
	config := filepath.Join(fixture.workspace, "config", "tenant")
	fixture.removeCommittedBindings(t)
	if err := os.Remove(filepath.Join(config, "lookups", "zpa_segment_group.lookup.json")); err != nil {
		t.Fatalf("remove lookup: %v", err)
	}

	diagnostics := make([]string, 0)
	outputRoot := fixture.outputRoot
	_, err := GenerateEnvironmentRoots(GenerateEnvironmentRootsOptions{
		Deployment:   loadDeploymentFile(t, fixture.deploymentPath),
		FormatHcl:    identityFormatter,
		OnDiagnostic: func(message string) { diagnostics = append(diagnostics, message) },
		OutputRoot:   &outputRoot,
		Root:         syntheticRootForTopology(t),
		Selectors:    []string{"zpa_application_segment"},
		Tenant:       "tenant",
	})
	if err == nil || !strings.Contains(err.Error(), "zpa_application_segment") ||
		!strings.Contains(err.Error(), "token") {
		t.Fatalf("GenerateEnvironmentRoots error = %v, want a refusal naming the tokenised root", err)
	}
}

// TestUntokenisedRootRendersByteIdentically pins the no-flag-day contract:
// an old-shape (raw-ID) config renders exactly today's bytes -- no try
// wrapper, no lookup local, probe behaviour unchanged.
func TestUntokenisedRootRendersByteIdentically(t *testing.T) {
	fixture := newStateAwareFixture(t)

	fixture.generate(t, false)

	bindings := fixture.readReferrerFile(t, "expression_bindings.tf")
	if strings.Contains(bindings, "try(") || strings.Contains(bindings, "infrawright_reference_book") {
		t.Errorf("expression_bindings.tf = %q, want no resolver machinery for an old-shape config", bindings)
	}
	main := fixture.readReferrerFile(t, "main.tf")
	if strings.Contains(main, "infrawright_reference_book") {
		t.Errorf("main.tf = %q, want no lookup machinery for an old-shape config", main)
	}
}

// TestUnboundTokenFailsGenerationNamingLeaf pins adversarial-review blocker
// 1: one unrelated binding used to satisfy the root-level guard while other
// tokens passed unresolved through the root's opaque variable -- and on a
// string-typed provider field, straight to the provider. The totality gate
// is leaf-granular: generation refuses, naming the uncovered token.
func TestUnboundTokenFailsGenerationNamingLeaf(t *testing.T) {
	fixture := newStateAwareFixture(t)
	fixture.tokenise(t)
	config := filepath.Join(fixture.workspace, "config", "tenant")
	writeJSONFile(t, filepath.Join(config, "zpa_application_segment.auto.tfvars.json"), map[string]any{
		"items": map[string]any{"app_one": map[string]any{
			"segment_group_id": "zpa_segment_group.segment_one",
			"description":      "text",
		}},
	})
	// The bindings file covers a DIFFERENT, existing leaf, so the old
	// root-level guard (len(bindings) > 0) is satisfied while the tokenised
	// leaf has no resolver.
	writeJSONFile(t, filepath.Join(config, "zpa_application_segment.generated.expressions.json"), map[string]any{
		"resources": map[string]any{
			"zpa_application_segment.app_one": map[string]any{
				"description": map[string]any{
					"expression": "var.decoy_description",
				},
			},
		},
	})

	outputRoot := fixture.outputRoot
	_, err := GenerateEnvironmentRoots(GenerateEnvironmentRootsOptions{
		Deployment: loadDeploymentFile(t, fixture.deploymentPath),
		FormatHcl:  identityFormatter, OnDiagnostic: func(string) {},
		OutputRoot: &outputRoot, Root: syntheticRootForTopology(t),
		Selectors: []string{"zpa_application_segment"}, Tenant: "tenant",
	})
	if err == nil || !strings.Contains(err.Error(), "app_one.segment_group_id") ||
		!strings.Contains(err.Error(), "zpa_segment_group.segment_one") {
		t.Fatalf("GenerateEnvironmentRoots error = %v, want a refusal naming the uncovered token leaf", err)
	}
}

// TestDisabledModeWithCommittedTokensRefused pins adversarial-review
// blocker 2: disabling cross_state_references after tokens are committed
// used to blind the edge-keyed scan and pass tokens bare through
// var.<name>. Detection is now binding-mode-independent, and the mismatch
// is a loud refusal with the remedy named.
func TestDisabledModeWithCommittedTokensRefused(t *testing.T) {
	fixture := newStateAwareFixture(t)
	fixture.tokenise(t)
	writeJSONFile(t, fixture.deploymentPath, map[string]any{
		"overlay": fixture.workspace,
		"roots": map[string]any{
			"zpa": map[string]any{"cross_state_references": false},
		},
	})

	outputRoot := fixture.outputRoot
	_, err := GenerateEnvironmentRoots(GenerateEnvironmentRootsOptions{
		Deployment: loadDeploymentFile(t, fixture.deploymentPath),
		FormatHcl:  identityFormatter, OnDiagnostic: func(string) {},
		OutputRoot: &outputRoot, Root: syntheticRootForTopology(t),
		Selectors: []string{"zpa_application_segment"}, Tenant: "tenant",
	})
	if err == nil || !strings.Contains(err.Error(), "disabled") ||
		!strings.Contains(err.Error(), "re-run transform") {
		t.Fatalf("GenerateEnvironmentRoots error = %v, want the disabled-mode-with-tokens refusal", err)
	}
}

// TestInnocentDottedStringKeepsProbeActive pins the false-positive fix: a
// non-reference string value that happens to look dotted must not flip the
// root into token mode -- the probe filter still runs and no resolver
// machinery is emitted.
//
// The referent's lookup is committed deliberately. It is what puts
// zpa_segment_group in the dropped-edge orphan scan's referent set, so the
// wide scan does look at this value and classify it -- and "note" is not a
// key in that lookup, which is the disambiguator that keeps an innocent dotted
// string innocent. Without the lookup on disk the scan would skip the prefix
// outright and this test would prove nothing about lookup membership.
func TestInnocentDottedStringKeepsProbeActive(t *testing.T) {
	fixture := newStateAwareFixture(t)
	config := filepath.Join(fixture.workspace, "config", "tenant")
	writeJSONFile(t, filepath.Join(config, "zpa_application_segment.auto.tfvars.json"), map[string]any{
		"items": map[string]any{"app_one": map[string]any{
			"segment_group_id": "sg-1",
			"description":      "zpa_segment_group.note",
		}},
	})
	writeJSONFile(t, filepath.Join(config, "lookups", "zpa_segment_group.lookup.json"), segmentGroupLookupContent())

	var probeCalls []string
	outputRoot := fixture.outputRoot
	if _, err := GenerateEnvironmentRoots(GenerateEnvironmentRootsOptions{
		Deployment: loadDeploymentFile(t, fixture.deploymentPath),
		FormatHcl:  identityFormatter, OnDiagnostic: func(string) {},
		OutputRoot: &outputRoot, Root: syntheticRootForTopology(t),
		Selectors: []string{"zpa_application_segment"}, StateAware: true,
		StateProbe: func(rootLabel, referentType string) (StateProbeResult, error) {
			probeCalls = append(probeCalls, rootLabel)
			return StateProbeResult{Usable: true}, nil
		},
		Tenant: "tenant",
	}); err != nil {
		t.Fatalf("GenerateEnvironmentRoots error = %v, want nil", err)
	}
	if len(probeCalls) == 0 {
		t.Errorf("probe calls = none, want the probe consulted for an old-shape root")
	}
	bindings := fixture.readReferrerFile(t, "expression_bindings.tf")
	if strings.Contains(bindings, "try(") || strings.Contains(bindings, "infrawright_reference_book") {
		t.Errorf("expression_bindings.tf = %q, want no resolver machinery for an old-shape root", bindings)
	}
}

// TestForeignReferentTokenRefused pins the re-review's sharpest finding: a
// pack referent reassignment strands committed tokens carrying the OLD
// referent prefix. Detection is shape-based at reference leaves, so a
// foreign token with no covering binding is a loud refusal, never an
// invisible passthrough to a string-typed provider field.
func TestForeignReferentTokenRefused(t *testing.T) {
	fixture := newStateAwareFixture(t)
	fixture.tokenise(t)
	config := filepath.Join(fixture.workspace, "config", "tenant")
	writeJSONFile(t, filepath.Join(config, "zpa_application_segment.auto.tfvars.json"), map[string]any{
		"items": map[string]any{"app_one": map[string]any{
			"segment_group_id": "zpa_application_server.stale_key",
		}},
	})
	fixture.removeCommittedBindings(t)

	outputRoot := fixture.outputRoot
	_, err := GenerateEnvironmentRoots(GenerateEnvironmentRootsOptions{
		Deployment: loadDeploymentFile(t, fixture.deploymentPath),
		FormatHcl:  identityFormatter, OnDiagnostic: func(string) {},
		OutputRoot: &outputRoot, Root: syntheticRootForTopology(t),
		Selectors: []string{"zpa_application_segment"}, Tenant: "tenant",
	})
	if err == nil || !strings.Contains(err.Error(), "zpa_application_server.stale_key") {
		t.Fatalf("GenerateEnvironmentRoots error = %v, want a refusal naming the foreign token", err)
	}
}

// useHclTfvars rewrites the fixture's deployment to HCL-format tfvars and
// drops the committed bindings cache, which addresses a JSON config that no
// longer exists under that format.
func (f stateAwareFixture) useHclTfvars(t *testing.T) {
	t.Helper()
	writeJSONFile(t, f.deploymentPath, map[string]any{
		"overlay":       f.workspace,
		"tfvars_format": "hcl",
		"roots": map[string]any{
			"zpa": map[string]any{"cross_state_references": true},
		},
	})
	f.removeCommittedBindings(t)
}

func (f stateAwareFixture) writeHclConfig(t *testing.T, resourceType, body string) {
	t.Helper()
	file := filepath.Join(f.workspace, "config", "tenant", resourceType+".auto.tfvars")
	if err := os.WriteFile(file, []byte(body), 0o666); err != nil {
		t.Fatalf("write hcl config: %v", err)
	}
}

// TestHclConfigWithTokenShapedValueRefused pins the JSON-only contract at
// the render side: an HCL-format config carrying a value the referent's
// committed lookup decodes as a token is refused outright -- no binding mode,
// no silent lane.
//
// The lookup is committed deliberately: since the round-5 re-review, the HCL
// refusal keys on lookup membership exactly as the JSON scan does, so without a
// lookup on disk there is nothing to distinguish this value from any other
// dotted string.
func TestHclConfigWithTokenShapedValueRefused(t *testing.T) {
	fixture := newStateAwareFixture(t)
	fixture.useHclTfvars(t)
	writeJSONFile(t,
		filepath.Join(fixture.workspace, "config", "tenant", "lookups", "zpa_segment_group.lookup.json"),
		segmentGroupLookupContent())
	fixture.writeHclConfig(t, "zpa_application_segment",
		"items = {\n  app_one = {\n    segment_group_id = \"zpa_segment_group.segment_one\"\n  }\n}\n")

	outputRoot := fixture.outputRoot
	_, err := GenerateEnvironmentRoots(GenerateEnvironmentRootsOptions{
		Deployment: loadDeploymentFile(t, fixture.deploymentPath),
		FormatHcl:  identityFormatter, OnDiagnostic: func(string) {},
		OutputRoot: &outputRoot, Root: syntheticRootForTopology(t),
		Selectors: []string{"zpa_application_segment"}, Tenant: "tenant",
	})
	if err == nil || !strings.Contains(err.Error(), "JSON tfvars only") {
		t.Fatalf("GenerateEnvironmentRoots error = %v, want the HCL token refusal", err)
	}
}

// TestHclDeclaredEdgeTokenWithNoBookRefused pins the round-3 re-review's
// second repro. Requiring lookup membership everywhere lost a fail-closed path:
// with the pack edge STILL declared, no committed lookup, and no bindings cache,
// a token at the declared reference field produced no claim, left tokensPresent
// false, and rode the opaque module variable to the provider untouched.
//
// Membership cannot be checked without a lookup, so this lane matches on shape
// alone. Its confinement is MEMBER-level, not field-level -- unparsed HCL
// cannot say which field a quoted value sits at -- so what narrows it is the
// candidate set: only referents declared by THIS member's reference fields,
// and only those with no committed lookup anywhere.
func TestHclDeclaredEdgeTokenWithNoBookRefused(t *testing.T) {
	fixture := newStateAwareFixture(t)
	fixture.useHclTfvars(t)
	fixture.writeHclConfig(t, "zpa_application_segment",
		"items = {\n  app_one = {\n    segment_group_id = \"zpa_segment_group.segment_one\"\n  }\n}\n")

	outputRoot := fixture.outputRoot
	_, err := GenerateEnvironmentRoots(GenerateEnvironmentRootsOptions{
		Deployment: loadDeploymentFile(t, fixture.deploymentPath),
		FormatHcl:  identityFormatter, OnDiagnostic: func(string) {},
		OutputRoot: &outputRoot, Root: syntheticRootForTopology(t),
		Selectors: []string{"zpa_application_segment"}, Tenant: "tenant",
	})
	if err == nil {
		t.Fatalf("GenerateEnvironmentRoots error = nil, want a refusal for a declared-edge token with no lookup to decode it")
	}
	for _, want := range []string{"zpa_segment_group.segment_one", "zpa_segment_group", "no committed lookup"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("GenerateEnvironmentRoots error = %q, want it to name %q", err, want)
		}
	}
}

// TestHclInnocentDottedStringNotRefused is the round-5 re-review's concrete
// valid-input repro. zpa_segment_group declares no outgoing reference field of
// its own but IS a pack referent elsewhere, and `description` is ordinary
// module input. Under HCL tfvars, a shape-only scan over the widened referent
// set refused that config outright -- a legitimate deployment made
// ungenerable.
//
// Both halves of the disambiguator are pinned: no lookup at all, and a lookup that
// simply does not decode the value's remainder.
func TestHclInnocentDottedStringNotRefused(t *testing.T) {
	for _, testCase := range []struct {
		name   string
		lookup bool
	}{
		{name: "no lookup committed", lookup: false},
		{name: "lookup without the key", lookup: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			fixture := newStateAwareFixture(t)
			fixture.useHclTfvars(t)
			if testCase.lookup {
				writeJSONFile(t,
					filepath.Join(fixture.workspace, "config", "tenant", "lookups", "zpa_segment_group.lookup.json"),
					segmentGroupLookupContent())
			}
			fixture.writeHclConfig(t, "zpa_segment_group",
				"items = {\n  segment_one = {\n    description = \"zpa_segment_group.note\"\n    name        = \"Segment One\"\n  }\n}\n")

			outputRoot := fixture.outputRoot
			if _, err := GenerateEnvironmentRoots(GenerateEnvironmentRootsOptions{
				Deployment: loadDeploymentFile(t, fixture.deploymentPath),
				FormatHcl:  identityFormatter, OnDiagnostic: func(string) {},
				OutputRoot: &outputRoot, Root: syntheticRootForTopology(t),
				Selectors: []string{"zpa_segment_group"}, Tenant: "tenant",
			}); err != nil {
				t.Fatalf("GenerateEnvironmentRoots error = %v, want nil for an ordinary dotted description", err)
			}
		})
	}
}

// TestStaleBindingAtTokenPathRefused pins round-3's re-aimed finding: a
// generated binding at the token's own path that resolves a DIFFERENT
// referent or key must not count as coverage -- path adjacency alone would
// let a stale binding silently resolve the wrong value. Operator overrides
// remain exempt by identity.
func TestStaleBindingAtTokenPathRefused(t *testing.T) {
	fixture := newStateAwareFixture(t)
	fixture.tokenise(t)
	config := filepath.Join(fixture.workspace, "config", "tenant")
	// Same item, same path -- but the expression resolves a different key
	// than the committed token names.
	writeJSONFile(t, filepath.Join(config, "zpa_application_segment.generated.expressions.json"), map[string]any{
		"resources": map[string]any{
			"zpa_application_segment.app_one": map[string]any{
				"segment_group_id": map[string]any{
					"expression": `data.terraform_remote_state.zpa_segment_group.outputs.infrawright_reference_ids.zpa_segment_group["some_other_key"]`,
				},
			},
		},
	})

	outputRoot := fixture.outputRoot
	_, err := GenerateEnvironmentRoots(GenerateEnvironmentRootsOptions{
		Deployment: loadDeploymentFile(t, fixture.deploymentPath),
		FormatHcl:  identityFormatter, OnDiagnostic: func(string) {},
		OutputRoot: &outputRoot, Root: syntheticRootForTopology(t),
		Selectors: []string{"zpa_application_segment"}, Tenant: "tenant",
	})
	if err == nil || !strings.Contains(err.Error(), "no binding that resolves it") {
		t.Fatalf("GenerateEnvironmentRoots error = %v, want the stale-binding refusal", err)
	}
}

// The three tests below pin Part A of the sidecar-minimization design: the
// committed .generated.expressions.json becomes a cache the renderer can
// recompute, so gen-env derives the generated layer itself when the file is
// absent and the config carries tokens. See
// docs/superpowers/specs/2026-07-31-sidecar-minimization-design.md.

// TestDerivedBindingsMatchCommittedCacheByteForByte is the parity pin: over
// one fully tokenised fixture, the tree rendered from the committed
// (token-derived) cache and the tree rendered with that cache deleted must be
// byte-identical. Same derivation engine, same inputs, so any divergence is a
// defect in the render-time wiring, not in the derivation.
//
// The fixture is all-token deliberately. Parity is claimed for that shape
// only: a MIXED config's cache carries raw-ID bindings render-derivation
// correctly declines to reproduce, which
// TestMixedConfigRenderDerivationBindsTokensOnly pins below.
func TestDerivedBindingsMatchCommittedCacheByteForByte(t *testing.T) {
	fixture := newStateAwareFixture(t)
	fixture.tokenise(t)

	fixture.generate(t, false)
	bridged := fixture.referrerTree(t)
	bridgedBindings, present := bridged[expressionBindingsTF]
	if !present {
		t.Fatalf("bridge tree = %v, want %s to compare against", mapKeysForTest(bridged), expressionBindingsTF)
	}
	// Guards against a vacuous pass: the bytes being compared must actually
	// carry the resolver the cache encodes.
	if !strings.Contains(bridgedBindings, "try(data.terraform_remote_state.zpa_segment_group") {
		t.Fatalf("%s = %q, want the bridge path's lookup-first resolver", expressionBindingsTF, bridgedBindings)
	}

	fixture.removeCommittedBindings(t)
	diagnostics := fixture.generate(t, false)
	// The derivation accounting proves the second run actually derived rather
	// than reusing anything the first run left in the output tree.
	if !containsString(diagnostics, "NOTE bindings: zpa_application_segment: 1 bound, 0 skipped") {
		t.Fatalf("diagnostics = %v, want the render-derivation accounting", diagnostics)
	}
	derived := fixture.referrerTree(t)
	if got := derived[expressionBindingsTF]; got != bridgedBindings {
		t.Errorf("%s derived at render =\n%q\nwant the bridge path's bytes:\n%q", expressionBindingsTF, got, bridgedBindings)
	}
	if !equalTrees(derived, bridged) {
		t.Errorf("render-derived tree differs from the bridged tree:\ngot  %v\nwant %v",
			mapKeysForTest(derived), mapKeysForTest(bridged))
	}
}

// TestCommittedBindingsCacheWinsOverDerivation pins the migration bridge: a
// committed cache still serves untouched when it exists. The cache carries an
// extra binding derivation can never produce (no reference field declares
// `description`), so its presence in the emitted root proves the file served
// rather than the deriver.
func TestCommittedBindingsCacheWinsOverDerivation(t *testing.T) {
	fixture := newStateAwareFixture(t)
	fixture.tokenise(t)
	config := filepath.Join(fixture.workspace, "config", "tenant")
	writeJSONFile(t, filepath.Join(config, "zpa_application_segment.auto.tfvars.json"), map[string]any{
		"items": map[string]any{"app_one": map[string]any{
			"segment_group_id": "zpa_segment_group.segment_one",
			"description":      "text",
		}},
	})
	writeJSONFile(t, fixture.committedBindingsFile(), map[string]any{
		"resources": map[string]any{
			"zpa_application_segment.app_one": map[string]any{
				"segment_group_id": map[string]any{
					"expression": `data.terraform_remote_state.zpa_segment_group.outputs.infrawright_reference_ids.zpa_segment_group["segment_one"]`,
				},
				"description": map[string]any{"expression": "var.cache_only_description"},
			},
		},
	})

	fixture.generate(t, false)

	bindings := fixture.readReferrerFile(t, expressionBindingsTF)
	if !strings.Contains(bindings, "var.cache_only_description") {
		t.Errorf("%s = %q, want the committed cache's un-derivable binding kept", expressionBindingsTF, bindings)
	}
	if !strings.Contains(bindings, "try(data.terraform_remote_state.zpa_segment_group") {
		t.Errorf("%s = %q, want the cache's token resolver kept", expressionBindingsTF, bindings)
	}
}

// TestOldShapeConfigNeverDerivesFromTheLookup pins the bridge's other edge:
// raw-ID derivation stays transform-only. With no committed cache, an
// old-shape tree must render exactly the same bytes whether or not the
// referent's lookup is on disk -- render output never depends on lookup contents
// for a config that carries no tokens.
func TestOldShapeConfigNeverDerivesFromTheLookup(t *testing.T) {
	fixture := newStateAwareFixture(t)
	fixture.removeCommittedBindings(t)

	fixture.generate(t, false)
	cacheless := fixture.referrerTree(t)
	if bindings, present := cacheless[expressionBindingsTF]; present {
		t.Fatalf("%s = %q, want no bindings emitted without a committed cache", expressionBindingsTF, bindings)
	}

	// The lookup that WOULD resolve "sg-1" if raw-ID derivation ran at render.
	writeJSONFile(t, filepath.Join(fixture.workspace, "config", "tenant", "lookups", "zpa_segment_group.lookup.json"), segmentGroupLookupContent())
	diagnostics := fixture.generate(t, false)
	for _, message := range diagnostics {
		if strings.HasPrefix(message, "NOTE bindings:") {
			t.Errorf("diagnostics carry %q, want no derivation over an old-shape config", message)
		}
	}
	withLookup := fixture.referrerTree(t)
	if !equalTrees(withLookup, cacheless) {
		t.Errorf("old-shape tree changed once the lookup existed:\ngot  %v\nwant %v",
			mapKeysForTest(withLookup), mapKeysForTest(cacheless))
	}
}

// The tests below pin the two round-4 adversarial-review findings against the
// sidecar-minimization branch: a committed token orphaned by a dropped pack
// edge (which used to fail OPEN once the bindings cache was no longer
// committed), and render-derivation rebinding raw-ID leaves that share a
// config with a tokenised one.

// zpaReferencesWithoutSegmentGroupEdge is the pack version that RETIRED
// zpa_application_segment.segment_group_id. Every other edge survives, so the
// referrer is still a reference-carrying type and the root topology is
// unchanged in shape -- only the edge the committed token depends on is gone.
func zpaReferencesWithoutSegmentGroupEdge() metadata.JsonObject {
	references := defaultSyntheticZpaReferences()
	referrer, ok := references["zpa_application_segment"].(metadata.JsonObject)
	if !ok {
		panic("synthetic zpa references no longer declare zpa_application_segment")
	}
	delete(referrer, "segment_group_id")
	return references
}

// TestDroppedPackEdgeOrphansCommittedTokenRefused pins round-4 blocker 1.
// Retiring a reference edge is ordinary pack evolution, and a tenant that has
// not re-transformed still carries the token that edge minted. Before the
// bindings cache was retired the stale cache's selector reached
// validateRemoteStateReferences and was refused as an undeclared edge; with no
// cache committed, the edge-keyed enumeration skipped the field entirely, the
// root's token gates never ran, and the literal token flowed through
// var.<items> to a string-typed provider field.
//
// The refusal must name the leaf, the token, and the remedy.
func TestDroppedPackEdgeOrphansCommittedTokenRefused(t *testing.T) {
	fixture := newStateAwareFixture(t)
	fixture.tokenise(t)
	fixture.removeCommittedBindings(t)

	outputRoot := fixture.outputRoot
	_, err := GenerateEnvironmentRoots(GenerateEnvironmentRootsOptions{
		Deployment: loadDeploymentFile(t, fixture.deploymentPath),
		FormatHcl:  identityFormatter, OnDiagnostic: func(string) {},
		OutputRoot: &outputRoot,
		Root:       syntheticRootWithZpaReferences(t, zpaReferencesWithoutSegmentGroupEdge()),
		Selectors:  []string{"zpa_application_segment"}, Tenant: "tenant",
	})
	if err == nil {
		t.Fatalf("GenerateEnvironmentRoots error = nil, want a refusal for the orphaned token")
	}
	for _, want := range []string{
		"app_one.segment_group_id",
		"zpa_segment_group.segment_one",
		"re-run transform",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("GenerateEnvironmentRoots error = %q, want it to name %q", err, want)
		}
	}
}

// TestDroppedPackEdgeOrphanDetectedWithLegacyPathLookup is the same refusal on a
// tenant that has not re-transformed since the lookup moved to
// config/<tenant>/lookups/. The orphaned token's ONLY decoder is the lookup at
// the pre-migration path, so a scan that inventoried the current location
// alone would find no referent, classify the token as an ordinary string, and
// fail open exactly as before.
func TestDroppedPackEdgeOrphanDetectedWithLegacyPathLookup(t *testing.T) {
	fixture := newStateAwareFixture(t)
	fixture.tokeniseAtLegacyPath(t)
	fixture.removeCommittedBindings(t)

	outputRoot := fixture.outputRoot
	_, err := GenerateEnvironmentRoots(GenerateEnvironmentRootsOptions{
		Deployment: loadDeploymentFile(t, fixture.deploymentPath),
		FormatHcl:  identityFormatter, OnDiagnostic: func(string) {},
		OutputRoot: &outputRoot,
		Root:       syntheticRootWithZpaReferences(t, zpaReferencesWithoutSegmentGroupEdge()),
		Selectors:  []string{"zpa_application_segment"}, Tenant: "tenant",
	})
	if err == nil || !strings.Contains(err.Error(), "zpa_segment_group.segment_one") ||
		!strings.Contains(err.Error(), "app_one.segment_group_id") {
		t.Fatalf("GenerateEnvironmentRoots error = %v, want the orphan refusal through the legacy-path lookup", err)
	}
}

// TestDroppedPackEdgeLeavesUnindexedValuesAlone is the orphan gate's negative
// half. The same dropped edge, but the committed value's remainder is not a
// key in the referent's lookup, so nothing was ever minted there and the value
// is an ordinary string. Generation must succeed. Without this, a gate that
// refused every dotted string at every leaf would satisfy the test above.
func TestDroppedPackEdgeLeavesUnindexedValuesAlone(t *testing.T) {
	fixture := newStateAwareFixture(t)
	fixture.tokenise(t)
	config := filepath.Join(fixture.workspace, "config", "tenant")
	writeJSONFile(t, filepath.Join(config, "zpa_application_segment.auto.tfvars.json"), map[string]any{
		"items": map[string]any{"app_one": map[string]any{
			"segment_group_id": "zpa_segment_group.no_such_key",
		}},
	})
	fixture.removeCommittedBindings(t)

	outputRoot := fixture.outputRoot
	if _, err := GenerateEnvironmentRoots(GenerateEnvironmentRootsOptions{
		Deployment: loadDeploymentFile(t, fixture.deploymentPath),
		FormatHcl:  identityFormatter, OnDiagnostic: func(string) {},
		OutputRoot: &outputRoot,
		Root:       syntheticRootWithZpaReferences(t, zpaReferencesWithoutSegmentGroupEdge()),
		Selectors:  []string{"zpa_application_segment"}, Tenant: "tenant",
	}); err != nil {
		t.Fatalf("GenerateEnvironmentRoots error = %v, want nil for a value no lookup decodes", err)
	}
}

// segmentGroupBookWithSecondEntry is segmentGroupLookupContent extended with a
// second referent whose raw tenant ID ("sg-2") a mixed config still carries
// literally -- the ID render-derivation must never resolve.
func segmentGroupBookWithSecondEntry() map[string]any {
	return map[string]any{
		"by_id":     map[string]any{"sg-1": "Segment One", "sg-2": "Segment Two"},
		"id_by_key": map[string]any{"segment_one": "sg-1", "segment_two": "sg-2"},
		"key_by_id": map[string]any{"sg-1": "segment_one", "sg-2": "segment_two"},
	}
}

// tokeniseMixed writes the shape the committed demo tree actually has: one
// item tokenised by a past transform and a sibling still carrying a raw
// tenant ID, under one config for one resource type.
func (f stateAwareFixture) tokeniseMixed(t *testing.T, lookup map[string]any) {
	t.Helper()
	config := filepath.Join(f.workspace, "config", "tenant")
	writeJSONFile(t, filepath.Join(config, "zpa_application_segment.auto.tfvars.json"), map[string]any{
		"items": map[string]any{
			"app_one": map[string]any{"segment_group_id": "zpa_segment_group.segment_one"},
			"app_two": map[string]any{"segment_group_id": "sg-2"},
		},
	})
	writeJSONFile(t, filepath.Join(config, "lookups", "zpa_segment_group.lookup.json"), lookup)
}

// TestMixedConfigRenderDerivationBindsTokensOnly pins round-4 finding 2. The
// derivation trigger is per resource type, so one tokenised item drags every
// other item in the same config through DeriveGeneratedBindings -- and the
// deriver's raw-ID branch would resolve any ID the lookup happens to know.
// Raw-ID derivation is transform-only: binding it here makes the emitted root
// depend on lookup contents for a leaf no transform touched.
func TestMixedConfigRenderDerivationBindsTokensOnly(t *testing.T) {
	fixture := newStateAwareFixture(t)
	fixture.tokeniseMixed(t, segmentGroupBookWithSecondEntry())
	fixture.removeCommittedBindings(t)

	diagnostics := fixture.generate(t, false)

	bindings := fixture.readReferrerFile(t, expressionBindingsTF)
	if !strings.Contains(bindings, `local.infrawright_reference_lookup_zpa_segment_group["segment_one"]`) {
		t.Errorf("%s = %q, want the tokenised leaf bound", expressionBindingsTF, bindings)
	}
	if strings.Contains(bindings, "app_two") || strings.Contains(bindings, "segment_two") {
		t.Errorf("%s = %q, want NO binding for the raw-ID leaf (raw ids bind at transform, not render)", expressionBindingsTF, bindings)
	}
	if !containsString(diagnostics, "NOTE bindings: zpa_application_segment: 1 bound, 1 skipped (raw_id_render_only=1)") {
		t.Errorf("diagnostics = %v, want the raw-ID leaf accounted as skipped", diagnostics)
	}
	if !containsDiagnostic(diagnostics, "raw ids bind at transform") {
		t.Errorf("diagnostics = %v, want the skip reason named", diagnostics)
	}
}

// TestMixedConfigRenderIsIndependentOfLookupGrowth is the same invariant stated
// as an observable: a referent-only transform that adds an ID to the lookup must
// not change the referrer's emitted root. Only a re-transform of the referrer
// may rewrite its leaves.
func TestMixedConfigRenderIsIndependentOfLookupGrowth(t *testing.T) {
	fixture := newStateAwareFixture(t)
	fixture.tokeniseMixed(t, segmentGroupLookupContent())
	fixture.removeCommittedBindings(t)

	fixture.generate(t, false)
	before := fixture.referrerTree(t)
	if bindings, present := before[expressionBindingsTF]; !present ||
		!strings.Contains(bindings, "try(data.terraform_remote_state.zpa_segment_group") {
		t.Fatalf("%s = %q, want the tokenised leaf's resolver to compare against", expressionBindingsTF, bindings)
	}

	// The referent re-transforms and its lookup gains the sibling's raw ID.
	writeJSONFile(t,
		filepath.Join(fixture.workspace, "config", "tenant", "lookups", "zpa_segment_group.lookup.json"),
		segmentGroupBookWithSecondEntry())

	fixture.generate(t, false)
	if after := fixture.referrerTree(t); !equalTrees(after, before) {
		t.Errorf("referrer root changed when the referent's lookup gained an id:\ngot  %v\nwant %v",
			after[expressionBindingsTF], before[expressionBindingsTF])
	}
}
