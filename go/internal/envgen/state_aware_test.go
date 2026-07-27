package envgen

import (
	"errors"
	"github.com/dvmrry/infrawright-dev/go/internal/canonjson"
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
		Root:         syntheticRootForTopology(t),
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
//
// Scope: this pins the refusal only. Generation is not transactional, so the
// referrer directory may already exist when the probe fails; nothing here
// claims the output tree is left untouched.
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
		Root:       syntheticRootForTopology(t),
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

// countingProbe records every (root, referentType) pair it is asked about and
// answers usable, so tests can assert both which references were probed and
// how often.
func countingProbe(calls *[]string, usable bool) StateProbe {
	return func(rootLabel, referentType string) (StateProbeResult, error) {
		*calls = append(*calls, rootLabel+"."+referentType)
		return StateProbeResult{Usable: usable}, nil
	}
}

func (f stateAwareFixture) generateWithProbe(t *testing.T, probe StateProbe) error {
	t.Helper()
	outputRoot := f.outputRoot
	_, err := GenerateEnvironmentRoots(GenerateEnvironmentRootsOptions{
		Deployment: loadDeploymentFile(t, f.deploymentPath),
		FormatHcl:  identityFormatter,
		OutputRoot: &outputRoot,
		Root:       syntheticRootForTopology(t),
		Selectors:  []string{"zpa_application_segment"},
		StateAware: true,
		StateProbe: probe,
		Tenant:     "tenant",
	})
	return err
}

// TestStateAwareSkipsProbeForOperatorOverriddenBinding pins binding
// precedence. An operator binding at the same identity replaces the generated
// one during the merge, so the generated binding is never emitted and must
// never be probed: probing it would abort generation on a referent error, or
// report a fallback that did not happen.
//
// Red proof: with the probe applied before operator resolution, the generated
// binding is probed and this test's call list is non-empty.
func TestStateAwareSkipsProbeForOperatorOverriddenBinding(t *testing.T) {
	fixture := newStateAwareFixture(t)
	writeJSONFile(t, filepath.Join(fixture.workspace, "config", "tenant", "zpa_application_segment.expressions.json"), map[string]any{
		"resources": map[string]any{
			"zpa_application_segment.app_one": map[string]any{
				"segment_group_id": map[string]any{"expression": `local.operator_literal`},
			},
		},
	})

	var calls []string
	if err := fixture.generateWithProbe(t, countingProbe(&calls, false)); err != nil {
		t.Fatalf("GenerateEnvironmentRoots(operator override) error = %v, want nil", err)
	}
	if len(calls) != 0 {
		t.Errorf("probe calls = %v, want none for a generated binding an operator overrides", calls)
	}
}

// TestStateAwareProbesEachReferenceOnce pins memoization. Without it a root
// referenced by several bindings is read repeatedly, and state changing
// mid-run can classify identical references differently within one
// invocation.
func TestStateAwareProbesEachReferenceOnce(t *testing.T) {
	fixture := newStateAwareFixture(t)
	writeJSONFile(t, filepath.Join(fixture.workspace, "config", "tenant", "zpa_application_segment.auto.tfvars.json"), map[string]any{
		"items": map[string]any{
			"app_one": map[string]any{"segment_group_id": "sg-1"},
			"app_two": map[string]any{"segment_group_id": "sg-1"},
		},
	})
	writeJSONFile(t, filepath.Join(fixture.workspace, "config", "tenant", "zpa_application_segment.generated.expressions.json"), map[string]any{
		"resources": map[string]any{
			"zpa_application_segment.app_one": map[string]any{
				"segment_group_id": map[string]any{
					"expression": `data.terraform_remote_state.zpa_segment_group.outputs.infrawright_reference_ids.zpa_segment_group["segment_one"]`,
				},
			},
			"zpa_application_segment.app_two": map[string]any{
				"segment_group_id": map[string]any{
					"expression": `data.terraform_remote_state.zpa_segment_group.outputs.infrawright_reference_ids.zpa_segment_group["segment_one"]`,
				},
			},
		},
	})

	var calls []string
	if err := fixture.generateWithProbe(t, countingProbe(&calls, true)); err != nil {
		t.Fatalf("GenerateEnvironmentRoots(repeated reference) error = %v, want nil", err)
	}
	if len(calls) != 1 {
		t.Errorf("probe calls = %v, want exactly one per (root, referent type)", calls)
	}
}

// TestStateAwareRejectsRemoteBackendWithoutProber pins the azurerm hole. The
// local prober reads tenantDirectory/<label>/terraform.tfstate, which a remote
// backend never populates, so probing it would report every root absent and
// rewrite every reference to a literal in one silent sweep.
func TestStateAwareRejectsRemoteBackendWithoutProber(t *testing.T) {
	fixture := newStateAwareFixture(t)
	backend := "azurerm"
	outputRoot := fixture.outputRoot
	_, err := GenerateEnvironmentRoots(GenerateEnvironmentRootsOptions{
		Backend:    &backend,
		Deployment: loadDeploymentFile(t, fixture.deploymentPath),
		FormatHcl:  identityFormatter,
		OutputRoot: &outputRoot,
		Root:       syntheticRootForTopology(t),
		Selectors:  []string{"zpa_application_segment"},
		StateAware: true,
		Tenant:     "tenant",
	})
	if err == nil {
		t.Fatalf("GenerateEnvironmentRoots(azurerm + StateAware) error = nil, want a refusal")
	}
	if !strings.Contains(err.Error(), "azurerm") {
		t.Errorf("GenerateEnvironmentRoots(azurerm + StateAware) error = %q, want it to name the backend", err)
	}
}

// TestReferenceIDsPresentRejectsNonObjectReferenceMap pins the shape check. A
// JSON null decodes to a present key holding nil, and a bare key check reports
// that usable -- Terraform then halts at plan time on the index. Strings and
// lists behave the same way.
func TestReferenceIDsPresentRejectsNonObjectReferenceMap(t *testing.T) {
	for _, testCase := range []struct{ name, value string }{
		{name: "null", value: "null"},
		{name: "string", value: `"sg-1"`},
		{name: "list", value: `["sg-1"]`},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			raw := []byte(`{"outputs":{"infrawright_reference_ids":{"value":{"zpa_segment_group":` + testCase.value + `}}}}`)
			result, err := referenceIDsPresent(raw, "zpa_segment_group", "zpa_segment_group")
			if err == nil {
				t.Fatalf("referenceIDsPresent(%s) error = nil (usable=%v), want a malformed-state failure", testCase.value, result.Usable)
			}
		})
	}
}

// referrerTree snapshots the generated root so two runs can be compared
// byte-for-byte. The referent's own state file lives outside this subtree, so
// writing state never perturbs the comparison.
func (f stateAwareFixture) referrerTree(t *testing.T) map[string]string {
	t.Helper()
	return snapshotTree(t, filepath.Join(f.outputRoot, "tenant", "zpa_application_segment"))
}

// TestStateAwareWithUsableStateMatchesUnprobedOutput is the guard that keeps
// the filter honest. Every other state-aware test asserts something is
// dropped, so all of them still pass if the filter drops everything; this one
// fails in that case.
//
// With the referent publishing reference identifiers, a state-aware run must
// produce exactly the bytes an unprobed run produces, and a second
// state-aware run over unchanged state must reproduce the first.
func TestStateAwareWithUsableStateMatchesUnprobedOutput(t *testing.T) {
	fixture := newStateAwareFixture(t)

	fixture.generate(t, false)
	unprobed := fixture.referrerTree(t)
	if _, present := unprobed["expression_bindings.tf"]; !present {
		t.Fatalf("unprobed tree = %v, want a bindings file to compare against", mapKeysForTest(unprobed))
	}

	fixture.writeReferentState(t, referenceIDOutputs("zpa_segment_group", map[string]any{"segment_one": "sg-1"}))
	fixture.generate(t, true)
	probed := fixture.referrerTree(t)
	if !equalTrees(probed, unprobed) {
		t.Errorf("state-aware tree over usable state differs from unprobed tree:\ngot  %v\nwant %v", mapKeysForTest(probed), mapKeysForTest(unprobed))
	}

	fixture.generate(t, true)
	if repeat := fixture.referrerTree(t); !equalTrees(repeat, probed) {
		t.Errorf("repeat state-aware run over unchanged state is not byte-identical")
	}
}

// TestStateAwareKeepsOperatorRemoteStateBinding pins the operator escape
// hatch. An operator who writes a remote-state reference by hand is asserting
// intent, so it keeps its data block and keeps failing loudly at plan time
// even when the generated binding onto the same stateless root falls back.
func TestStateAwareKeepsOperatorRemoteStateBinding(t *testing.T) {
	fixture := newStateAwareFixture(t)
	writeJSONFile(t, filepath.Join(fixture.workspace, "config", "tenant", "zpa_application_segment.expressions.json"), map[string]any{
		"resources": map[string]any{
			"zpa_application_segment.app_one": map[string]any{
				"segment_group_id": map[string]any{
					"expression": `data.terraform_remote_state.zpa_segment_group.outputs.infrawright_reference_ids.zpa_segment_group["segment_one"]`,
				},
			},
		},
	})

	fixture.generate(t, true)

	main, err := os.ReadFile(fixture.referrerFile("main.tf"))
	if err != nil {
		t.Fatalf("os.ReadFile(main.tf) error = %v, want nil", err)
	}
	if !strings.Contains(string(main), `data "terraform_remote_state"`) {
		t.Errorf("main.tf has no terraform_remote_state block, want the operator reference preserved:\n%s", main)
	}
}

// TestStateBlindGenerationIgnoresPresentState is the determinism invariant the
// byte goldens depend on: with StateAware false, output cannot vary with any
// tfstate under the generated tree, so no existing gate's committed bytes can
// drift because a root happened to be applied.
func TestStateBlindGenerationIgnoresPresentState(t *testing.T) {
	fixture := newStateAwareFixture(t)

	fixture.generate(t, false)
	withoutState := fixture.referrerTree(t)

	fixture.writeReferentState(t, referenceIDOutputs("zpa_segment_group", map[string]any{"segment_one": "sg-1"}))
	fixture.generate(t, false)
	if withState := fixture.referrerTree(t); !equalTrees(withState, withoutState) {
		t.Errorf("state-blind generation varied with tfstate present, want identical output")
	}
}

func equalTrees(left, right map[string]string) bool {
	if len(left) != len(right) {
		return false
	}
	for name, content := range left {
		if right[name] != content {
			return false
		}
	}
	return true
}

func mapKeysForTest(tree map[string]string) []string {
	names := make([]string, 0, len(tree))
	for name := range tree {
		names = append(names, name)
	}
	return canonjson.SortedStrings(names)
}
