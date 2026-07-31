package envgen

// These tests pin Task 3 of the reference-tokens plan: when a root's
// committed config carries qualified reference tokens, the renderer emits
// lookup-first resolvers -- try(<canonical remote-state selector>,
// local.<book>["<key>"]) -- plus one plan-time book local per referent
// reading the committed lookup sidecar, and the state-probe drop filter is
// superseded (the fallback now lives in-language). Untokenised roots render
// byte-identically to today. See
// docs/superpowers/specs/2026-07-30-reference-tokens-design.md.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// tokenise rewrites the state-aware fixture's committed artifacts into the
// post-substitution shape: the tfvars reference leaf carries the qualified
// token and the referent's book exists with both directions.
func (f stateAwareFixture) tokenise(t *testing.T) {
	t.Helper()
	config := filepath.Join(f.workspace, "config", "tenant")
	writeJSONFile(t, filepath.Join(config, "zpa_application_segment.auto.tfvars.json"), map[string]any{
		"items": map[string]any{"app_one": map[string]any{"segment_group_id": "zpa_segment_group.segment_one"}},
	})
	writeJSONFile(t, filepath.Join(config, "zpa_segment_group.lookup.json"), map[string]any{
		"by_id":     map[string]any{"sg-1": "Segment One"},
		"id_by_key": map[string]any{"segment_one": "sg-1"},
		"key_by_id": map[string]any{"sg-1": "segment_one"},
	})
}

func (f stateAwareFixture) readReferrerFile(t *testing.T, name string) string {
	t.Helper()
	content, err := os.ReadFile(f.referrerFile(name))
	if err != nil {
		t.Fatalf("os.ReadFile(%s) error = %v, want nil", name, err)
	}
	return string(content)
}

// TestTokenisedRootEmitsLookupFirstResolvers pins the resolver shape: the
// canonical selector wrapped lookup-first with the book literal as the
// fallback arm, and the book local reading the committed sidecar at plan
// time through the relative path -- the renderer inlines no value from it.
func TestTokenisedRootEmitsLookupFirstResolvers(t *testing.T) {
	fixture := newStateAwareFixture(t)
	fixture.tokenise(t)

	fixture.generate(t, false)

	bindings := fixture.readReferrerFile(t, "expression_bindings.tf")
	wantResolver := `try(data.terraform_remote_state.zpa_segment_group.outputs.infrawright_reference_ids.zpa_segment_group["segment_one"], local.infrawright_reference_book_zpa_segment_group["segment_one"])`
	if !strings.Contains(bindings, wantResolver) {
		t.Errorf("expression_bindings.tf = %q, want resolver %q", bindings, wantResolver)
	}
	if !strings.Contains(bindings, "infrawright_reference_book_zpa_segment_group") ||
		!strings.Contains(bindings, "fileexists(") ||
		!strings.Contains(bindings, "zpa_segment_group.lookup.json") {
		t.Errorf("expression_bindings.tf = %q, want a fileexists-guarded book local over the committed sidecar", bindings)
	}
	main := fixture.readReferrerFile(t, "main.tf")
	if !strings.Contains(main, `data "terraform_remote_state" "zpa_segment_group"`) {
		t.Errorf("main.tf = %q, want the referent's remote-state reader", main)
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
// naming the root and the remedy.
func TestTokenisedRootWithoutBindingsFailsLoudly(t *testing.T) {
	fixture := newStateAwareFixture(t)
	fixture.tokenise(t)
	config := filepath.Join(fixture.workspace, "config", "tenant")
	if err := os.Remove(filepath.Join(config, "zpa_application_segment.generated.expressions.json")); err != nil {
		t.Fatalf("remove bindings: %v", err)
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
// wrapper, no book local, probe behaviour unchanged.
func TestUntokenisedRootRendersByteIdentically(t *testing.T) {
	fixture := newStateAwareFixture(t)

	fixture.generate(t, false)

	bindings := fixture.readReferrerFile(t, "expression_bindings.tf")
	if strings.Contains(bindings, "try(") || strings.Contains(bindings, "infrawright_reference_book") {
		t.Errorf("expression_bindings.tf = %q, want no resolver machinery for an old-shape config", bindings)
	}
	main := fixture.readReferrerFile(t, "main.tf")
	if strings.Contains(main, "infrawright_reference_book") {
		t.Errorf("main.tf = %q, want no book machinery for an old-shape config", main)
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
func TestInnocentDottedStringKeepsProbeActive(t *testing.T) {
	fixture := newStateAwareFixture(t)
	config := filepath.Join(fixture.workspace, "config", "tenant")
	writeJSONFile(t, filepath.Join(config, "zpa_application_segment.auto.tfvars.json"), map[string]any{
		"items": map[string]any{"app_one": map[string]any{
			"segment_group_id": "sg-1",
			"description":      "zpa_segment_group.note",
		}},
	})

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
	if err := os.Remove(filepath.Join(config, "zpa_application_segment.generated.expressions.json")); err != nil {
		t.Fatalf("remove bindings: %v", err)
	}

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

// TestHclConfigWithTokenShapedValueRefused pins the JSON-only contract at
// the render side: an HCL-format config carrying a token-shaped value is
// refused outright -- no book, no binding mode, no silent lane.
func TestHclConfigWithTokenShapedValueRefused(t *testing.T) {
	fixture := newStateAwareFixture(t)
	writeJSONFile(t, fixture.deploymentPath, map[string]any{
		"overlay":       fixture.workspace,
		"tfvars_format": "hcl",
		"roots": map[string]any{
			"zpa": map[string]any{"cross_state_references": true},
		},
	})
	config := filepath.Join(fixture.workspace, "config", "tenant")
	hcl := "items = {\n  app_one = {\n    segment_group_id = \"zpa_segment_group.segment_one\"\n  }\n}\n"
	if err := os.WriteFile(filepath.Join(config, "zpa_application_segment.auto.tfvars"), []byte(hcl), 0o666); err != nil {
		t.Fatalf("write hcl config: %v", err)
	}

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
