package envgen

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// stateAwareFixture materializes the minimal two-root cross-state workspace
// used by the state-aware fallback tests: a referent root
// (zpa_segment_group), a referrer root (zpa_application_segment) whose
// generated binding resolves through the referent's remote state, and no
// state for the referent unless a test writes one.
//
// The shape follows TestSingletonCrossStateDisableRemovesStaleGeneratedBindings
// in environment_generator_test.go, which is the existing template for
// exercising the generated-binding layer end to end.
type stateAwareFixture struct {
	deploymentPath string
	outputRoot     string
	workspace      string
}

func newStateAwareFixture(t *testing.T) stateAwareFixture {
	t.Helper()
	workspace := temporaryDirectory(t, "infrawright-gen-env-state-aware-")
	deploymentPath := filepath.Join(workspace, "deployment.json")
	writeJSONFile(t, deploymentPath, map[string]any{
		"overlay": workspace,
		"roots": map[string]any{
			"zpa": map[string]any{"cross_state_references": true},
		},
	})

	config := filepath.Join(workspace, "config", "tenant")
	writeJSONFile(t, filepath.Join(config, "zpa_segment_group.auto.tfvars.json"), map[string]any{
		"items": map[string]any{
			"segment_one": map[string]any{"description": "Segment", "enabled": true, "name": "Segment One"},
		},
	})
	writeJSONFile(t, filepath.Join(config, "zpa_application_segment.auto.tfvars.json"), map[string]any{
		"items": map[string]any{"app_one": map[string]any{"segment_group_id": "sg-1"}},
	})
	writeJSONFile(t, filepath.Join(config, "zpa_application_segment.generated.expressions.json"), map[string]any{
		"resources": map[string]any{
			"zpa_application_segment.app_one": map[string]any{
				"segment_group_id": map[string]any{
					"expression": `data.terraform_remote_state.zpa_segment_group.outputs.infrawright_reference_ids.zpa_segment_group["segment_one"]`,
				},
			},
		},
	})

	return stateAwareFixture{
		deploymentPath: deploymentPath,
		outputRoot:     filepath.Join(workspace, "generated"),
		workspace:      workspace,
	}
}

func (f stateAwareFixture) referrerFile(name string) string {
	return filepath.Join(f.outputRoot, "tenant", "zpa_application_segment", name)
}

// generate runs GenerateEnvironmentRoots over the referrer root and returns
// the diagnostics it emitted.
func (f stateAwareFixture) generate(t *testing.T, stateAware bool) []string {
	t.Helper()
	diagnostics := make([]string, 0)
	outputRoot := f.outputRoot
	if _, err := GenerateEnvironmentRoots(GenerateEnvironmentRootsOptions{
		Deployment:   loadDeploymentFile(t, f.deploymentPath),
		FormatHcl:    identityFormatter,
		OnDiagnostic: func(message string) { diagnostics = append(diagnostics, message) },
		OutputRoot:   &outputRoot,
		Root:         committedRootForTopology(t),
		Selectors:    []string{"zpa_application_segment"},
		StateAware:   stateAware,
		Tenant:       "tenant",
	}); err != nil {
		t.Fatalf("GenerateEnvironmentRoots(StateAware=%v) error = %v, want nil", stateAware, err)
	}
	return diagnostics
}

// writeReferentState writes a Terraform state file for the referent root at
// the path renderRemoteStateBlocks points its data blocks at. outputs is the
// state's "outputs" object verbatim, so a test can express a fully applied
// root, a destroyed one, or anything between.
func (f stateAwareFixture) writeReferentState(t *testing.T, outputs map[string]any) {
	t.Helper()
	writeJSONFile(t, filepath.Join(f.outputRoot, "tenant", "zpa_segment_group", "terraform.tfstate"), map[string]any{
		"version":           4,
		"terraform_version": "1.15.4",
		"serial":            1,
		"lineage":           "state-aware-fixture",
		"outputs":           outputs,
		"resources":         []any{},
	})
}

// referenceIDOutputs is the shape a generated root actually publishes: the
// infrawright_reference_ids output carrying a per-resource-type key map.
func referenceIDOutputs(referentType string, keys map[string]any) map[string]any {
	return map[string]any{
		"infrawright_reference_ids": map[string]any{
			"value": map[string]any{referentType: keys},
		},
	}
}

func containsDiagnostic(diagnostics []string, want string) bool {
	for _, diagnostic := range diagnostics {
		if strings.Contains(diagnostic, want) {
			return true
		}
	}
	return false
}

// TestStateAwareFallsBackWhenReferencedRootHasNoState is the core contract:
// with state-aware generation enabled and the referenced root carrying no
// state at all, the generated binding is dropped so the tfvars literal
// survives, no terraform_remote_state data block is emitted, and the
// pipeline reports the fallback.
//
// Without the drop, the emitted data block fails its read at plan time with
// "Unable to find remote state", which no try() wrapper can rescue -- that
// failure is what this test exists to prevent.
func TestStateAwareFallsBackWhenReferencedRootHasNoState(t *testing.T) {
	fixture := newStateAwareFixture(t)

	diagnostics := fixture.generate(t, true)

	main, err := os.ReadFile(fixture.referrerFile("main.tf"))
	if err != nil {
		t.Fatalf("os.ReadFile(main.tf) error = %v, want nil", err)
	}
	if strings.Contains(string(main), `data "terraform_remote_state"`) {
		t.Errorf("main.tf contains a terraform_remote_state block, want none for a root without state:\n%s", main)
	}
	if _, err := os.Stat(fixture.referrerFile("expression_bindings.tf")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("os.Stat(expression_bindings.tf) error = %v, want os.ErrNotExist", err)
	}
	if !containsDiagnostic(diagnostics, "zpa_segment_group") ||
		!containsDiagnostic(diagnostics, "no usable state") {
		t.Errorf("diagnostics = %#v, want a per-binding fallback note naming the stateless root", diagnostics)
	}
	if !containsDiagnostic(diagnostics, "fell back to literal") {
		t.Errorf("diagnostics = %#v, want a per-root fallback summary", diagnostics)
	}
}

// TestStateAwareFallsBackWhenReferencedStateHasNoReferenceOutputs covers the
// destroyed-or-never-published root: the state file exists, so a probe that
// only checks for the file's presence calls it usable, but the state carries
// no infrawright_reference_ids output for the referent type.
//
// Terraform halts on that shape too -- "Unsupported attribute ... outputs is
// object with N attributes" -- and unlike a missing key, it is a degenerate
// root rather than a drift signal, so it must fall back rather than fail.
// This test is what makes existence-only probing insufficient.
func TestStateAwareFallsBackWhenReferencedStateHasNoReferenceOutputs(t *testing.T) {
	fixture := newStateAwareFixture(t)
	fixture.writeReferentState(t, map[string]any{})

	diagnostics := fixture.generate(t, true)

	main, err := os.ReadFile(fixture.referrerFile("main.tf"))
	if err != nil {
		t.Fatalf("os.ReadFile(main.tf) error = %v, want nil", err)
	}
	if strings.Contains(string(main), `data "terraform_remote_state"`) {
		t.Errorf("main.tf contains a terraform_remote_state block, want none for a root whose state publishes no reference ids:\n%s", main)
	}
	if _, err := os.Stat(fixture.referrerFile("expression_bindings.tf")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("os.Stat(expression_bindings.tf) error = %v, want os.ErrNotExist", err)
	}
	if !containsDiagnostic(diagnostics, "no usable state") {
		t.Errorf("diagnostics = %#v, want a fallback note for the stateless referent", diagnostics)
	}
}

// TestStateAwareProbeErrorFailsClosed pins the direction that matters most:
// a probe that cannot answer must abort generation, never report "absent".
//
// stage-imports' ListState deliberately maps a Terraform failure to "no
// state" because keeping imports is the safe direction there. Here the safe
// direction is the opposite -- folding a probe failure into "absent" would
// silently rewrite every reference in the run to a stale literal, which is
// the exact silent-drift outcome the repository exists to prevent.
//
// Red proof: this test passes against the committed implementation, so it is
// verified against the faithful unsafe mutation "return
// StateProbeResult{Usable: false}, nil instead of the error" in
// referenceIDsPresent, under which it fails.
func TestStateAwareProbeErrorFailsClosed(t *testing.T) {
	fixture := newStateAwareFixture(t)
	statePath := filepath.Join(fixture.outputRoot, "tenant", "zpa_segment_group", "terraform.tfstate")
	if err := os.MkdirAll(filepath.Dir(statePath), 0o777); err != nil {
		t.Fatalf("os.MkdirAll(%q) error = %v, want nil", filepath.Dir(statePath), err)
	}
	if err := os.WriteFile(statePath, []byte("{not json"), 0o666); err != nil {
		t.Fatalf("os.WriteFile(corrupt state) error = %v, want nil", err)
	}

	outputRoot := fixture.outputRoot
	_, err := GenerateEnvironmentRoots(GenerateEnvironmentRootsOptions{
		Deployment: loadDeploymentFile(t, fixture.deploymentPath),
		FormatHcl:  identityFormatter,
		OutputRoot: &outputRoot,
		Root:       committedRootForTopology(t),
		Selectors:  []string{"zpa_application_segment"},
		StateAware: true,
		Tenant:     "tenant",
	})
	if err == nil {
		t.Fatalf("GenerateEnvironmentRoots(corrupt referent state) error = nil, want a probe failure")
	}
	if !strings.Contains(err.Error(), "zpa_segment_group") {
		t.Errorf("GenerateEnvironmentRoots(corrupt referent state) error = %q, want it to name the unreadable root", err)
	}
}
