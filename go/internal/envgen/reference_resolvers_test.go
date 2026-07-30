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
