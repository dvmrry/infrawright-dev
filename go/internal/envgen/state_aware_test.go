package envgen

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sort"
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
					"expression": `data.terraform_remote_state.zpa_segment_group.outputs.iw_reference_ids.zpa_segment_group["segment_one"]`,
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
// iw_reference_ids output carrying a per-resource-type key map.
// The type metadata is emitted alongside the value because Terraform writes
// both and refuses a state output carrying only one. A fixture without it is
// not state, so a probe that accepted it would prove nothing about the files
// this code actually meets.
func referenceIDOutputs(referentType string, keys map[string]any) map[string]any {
	keyTypes := map[string]any{}
	for key := range keys {
		keyTypes[key] = "string"
	}
	return map[string]any{
		"iw_reference_ids": map[string]any{
			"value": map[string]any{referentType: keys},
			"type": []any{
				"object",
				map[string]any{referentType: []any{"object", keyTypes}},
			},
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
// no iw_reference_ids output for the referent type.
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
// State-aware import staging follows the same boundary: only a successful
// empty state means no managed addresses; a failed Terraform state query
// aborts. Here, folding a probe failure into "absent" would silently rewrite
// every reference in the run to a stale literal, which is the exact
// silent-drift outcome the repository exists to prevent.
//
// Red proof: this test passes against the committed implementation, so it is
// verified against the faithful unsafe mutation "return
// StateProbeResult{Usable: false}, nil instead of the error" in
// ReferenceIDsPresent, under which it fails.
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
					"expression": `data.terraform_remote_state.zpa_segment_group.outputs.iw_reference_ids.zpa_segment_group["segment_one"]`,
				},
			},
			"zpa_application_segment.app_two": map[string]any{
				"segment_group_id": map[string]any{
					"expression": `data.terraform_remote_state.zpa_segment_group.outputs.iw_reference_ids.zpa_segment_group["segment_one"]`,
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

// TestReferenceIDsPresentRejectsNonObjectReferenceMap pins the shape check. A
// JSON null decodes to a present key holding nil, and a bare key check reports
// that usable -- Terraform then halts at plan time on the index. Strings and
// lists behave the same way.
//
// Every case carries a complete v4 envelope and a type its value genuinely
// satisfies, so the envelope and cty gates all pass and the referent-shape
// check is the only thing left that can reject it. Without that, removing the
// shape check leaves this test green because the cases die earlier.
func TestReferenceIDsPresentRejectsNonObjectReferenceMap(t *testing.T) {
	for _, testCase := range []struct{ name, value, valueType string }{
		{name: "null", value: "null", valueType: `"string"`},
		{name: "string", value: `"sg-1"`, valueType: `"string"`},
		{name: "list", value: `["sg-1"]`, valueType: `["list","string"]`},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			raw := []byte(`{"version":4,"outputs":{"iw_reference_ids":{` +
				`"value":{"zpa_segment_group":` + testCase.value + `},` +
				`"type":["object",{"zpa_segment_group":` + testCase.valueType + `}]}}}`)
			result, err := ReferenceIDsPresent(raw, "zpa_segment_group", "zpa_segment_group")
			if err == nil {
				t.Fatalf("ReferenceIDsPresent(%s) error = nil (usable=%v), want a malformed-state failure", testCase.value, result.Usable)
			}
			if !strings.Contains(err.Error(), "want an object of reference identifiers") {
				t.Errorf("ReferenceIDsPresent(%s) error = %q, want the referent-shape refusal rather than an earlier gate", testCase.value, err)
			}
		})
	}
}

// TestReferenceIDsPresentValidatesOutputTypeAgainstValue pins that the type is
// parsed and enforced, not merely present. Terraform refuses both of these with
// "Invalid output value type in state"; a probe that accepts them classifies a
// state Terraform cannot load, and then rewrites references on that basis.
func TestReferenceIDsPresentValidatesOutputTypeAgainstValue(t *testing.T) {
	for _, testCase := range []struct{ name, raw string }{
		{
			name: "type is not a type",
			raw:  `{"version":4,"outputs":{"iw_reference_ids":{"value":{},"type":"garbage"}}}`,
		},
		{
			name: "value disagrees with declared type",
			raw: `{"version":4,"outputs":{"iw_reference_ids":` +
				`{"value":{"zpa_segment_group":{"segment_one":"sg-1"}},` +
				`"type":["object",{"zpa_segment_group":"string"}]}}}`,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			result, err := ReferenceIDsPresent([]byte(testCase.raw), "zpa_segment_group", "zpa_segment_group")
			if err == nil {
				t.Fatalf("ReferenceIDsPresent(%s) error = nil (usable=%v), want a refusal", testCase.name, result.Usable)
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
// intent, so it survives even when the generated binding onto the same
// stateless root falls back, and it keeps failing loudly at plan time.
//
// The operator expression deliberately selects a different key than the
// generated one. Asserting only that "some remote-state block exists" cannot
// tell the layers apart, because the generated binding renders an identical
// block -- such a test passes even when the operator layer is never appended.
// The emitted bytes have to name the operator's own key to mean anything.
func TestStateAwareKeepsOperatorRemoteStateBinding(t *testing.T) {
	const operatorKey = "segment_two"
	fixture := newStateAwareFixture(t)
	writeJSONFile(t, filepath.Join(fixture.workspace, "config", "tenant", "zpa_application_segment.expressions.json"), map[string]any{
		"resources": map[string]any{
			"zpa_application_segment.app_one": map[string]any{
				"segment_group_id": map[string]any{
					"expression": `data.terraform_remote_state.zpa_segment_group.outputs.iw_reference_ids.zpa_segment_group["` + operatorKey + `"]`,
				},
			},
		},
	})

	fixture.generate(t, true)

	bindings, err := os.ReadFile(fixture.referrerFile("expression_bindings.tf"))
	if err != nil {
		t.Fatalf("os.ReadFile(expression_bindings.tf) error = %v, want the operator binding emitted", err)
	}
	if !strings.Contains(string(bindings), `"`+operatorKey+`"`) {
		t.Errorf("expression_bindings.tf does not carry the operator expression:\n%s", bindings)
	}
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

// equalTrees compares snapshots by key presence as well as content. A bare
// right[name] != content lookup returns the zero value for an absent key, so
// two trees with disjoint names but empty contents compared equal.
func equalTrees(left, right map[string]string) bool {
	return reflect.DeepEqual(left, right)
}

func mapKeysForTest(tree map[string]string) []string {
	names := make([]string, 0, len(tree))
	for name := range tree {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// generateStateBlind runs the fixture with state-awareness off and returns the
// error, so a test can pin that state-aware generation refuses exactly what
// state-blind generation already refuses.
func (f stateAwareFixture) generateStateBlind(t *testing.T) error {
	t.Helper()
	outputRoot := f.outputRoot
	_, err := GenerateEnvironmentRoots(GenerateEnvironmentRootsOptions{
		Deployment: loadDeploymentFile(t, f.deploymentPath),
		FormatHcl:  identityFormatter,
		OutputRoot: &outputRoot,
		Root:       syntheticRootForTopology(t),
		Selectors:  []string{"zpa_application_segment"},
		Tenant:     "tenant",
	})
	return err
}

// absentProbe reports every referent absent, which is what drives the fallback
// path. Each test below pairs it with generated evidence that is independently
// invalid, so the fallback cannot be used to launder the defect.
func absentProbe() StateProbe {
	return func(string, string) (StateProbeResult, error) {
		return StateProbeResult{Usable: false}, nil
	}
}

// TestStateAwareStillRefusesBindingWithNoTargetLeaf pins the boundary of the
// fallback. Dropping a binding is only a fallback because the tfvars literal
// underneath survives; when the target leaf does not exist there is no literal
// to fall back to, and reporting "fell back to the literal value" is a lie.
//
// The generated binding is the only one for this resource type, so dropping it
// also empties the type's binding set -- the case where generation skips the
// schema load and every validation gate outright.
func TestStateAwareStillRefusesBindingWithNoTargetLeaf(t *testing.T) {
	fixture := newStateAwareFixture(t)
	writeJSONFile(t, filepath.Join(fixture.workspace, "config", "tenant", "zpa_application_segment.auto.tfvars.json"), map[string]any{
		"items": map[string]any{"app_one": map[string]any{}},
	})

	blind := fixture.generateStateBlind(t)
	if blind == nil {
		t.Fatalf("state-blind generation error = nil, want a refusal for a binding with no target leaf")
	}
	if err := fixture.generateWithProbe(t, absentProbe()); err == nil {
		t.Errorf("state-aware generation error = nil, want the same refusal state-blind generation gives (%v)", blind)
	}
}

// TestStateAwareStillRefusesUnknownReferencedRoot pins that state-awareness
// cannot mask a malformed topology. A generated binding naming a root that does
// not exist is broken evidence, not a not-yet-applied root: probing it reports
// absent, and dropping it on that basis would silently discard the refusal.
func TestStateAwareStillRefusesUnknownReferencedRoot(t *testing.T) {
	fixture := newStateAwareFixture(t)
	writeJSONFile(t, filepath.Join(fixture.workspace, "config", "tenant", "zpa_application_segment.generated.expressions.json"), map[string]any{
		"resources": map[string]any{
			"zpa_application_segment.app_one": map[string]any{
				"segment_group_id": map[string]any{
					"expression": `data.terraform_remote_state.unknown_root.outputs.iw_reference_ids.zpa_segment_group["segment_one"]`,
				},
			},
		},
	})

	blind := fixture.generateStateBlind(t)
	if blind == nil {
		t.Fatalf("state-blind generation error = nil, want a refusal for a binding naming an unknown root")
	}
	if err := fixture.generateWithProbe(t, absentProbe()); err == nil {
		t.Errorf("state-aware generation error = nil, want the same refusal state-blind generation gives (%v)", blind)
	}
}

// TestStateAwareStillRefusesBindingOutsideProviderSchema pins the schema gate
// specifically, since it runs before the config and topology gates and would
// otherwise be the first one a dropped binding escapes.
func TestStateAwareStillRefusesBindingOutsideProviderSchema(t *testing.T) {
	fixture := newStateAwareFixture(t)
	writeJSONFile(t, filepath.Join(fixture.workspace, "config", "tenant", "zpa_application_segment.generated.expressions.json"), map[string]any{
		"resources": map[string]any{
			"zpa_application_segment.app_one": map[string]any{
				"not_a_provider_field": map[string]any{
					"expression": `data.terraform_remote_state.zpa_segment_group.outputs.iw_reference_ids.zpa_segment_group["segment_one"]`,
				},
			},
		},
	})

	blind := fixture.generateStateBlind(t)
	if blind == nil {
		t.Fatalf("state-blind generation error = nil, want a refusal for a binding outside the provider schema")
	}
	if err := fixture.generateWithProbe(t, absentProbe()); err == nil {
		t.Errorf("state-aware generation error = nil, want the same refusal state-blind generation gives (%v)", blind)
	}
}

// TestStateAwareStillRefusesOverlappingParentAndChildBindings pins the
// conflict gate against the state filter. An operator binding over a whole
// block value and a generated binding into a path inside that same value
// cannot both hold; the ported mutating walk caught the pair incidentally
// (the parent's sentinel broke the child's traversal), so the validation-only
// walk must refuse it explicitly -- BEFORE state filtering, or an absent-state
// run drops the generated child and launders the conflict into a successful
// render of the operator parent alone.
func TestStateAwareStillRefusesOverlappingParentAndChildBindings(t *testing.T) {
	fixture := newStateAwareFixture(t)
	config := filepath.Join(fixture.workspace, "config", "tenant")
	// A committed referent config so zpa_server_group's root materialises the
	// same way the fixture's zpa_segment_group root does.
	writeJSONFile(t, filepath.Join(config, "zpa_server_group.auto.tfvars.json"), map[string]any{
		"items": map[string]any{
			"group_one": map[string]any{"description": "Group", "enabled": true, "name": "Group One"},
		},
	})
	// Both target paths exist in the items, so each binding validates
	// independently; only the overlap between them is malformed.
	writeJSONFile(t, filepath.Join(config, "zpa_application_segment.auto.tfvars.json"), map[string]any{
		"items": map[string]any{"app_one": map[string]any{
			"segment_group_id": "sg-1",
			"server_groups":    []any{map[string]any{"id": []any{"srv-1"}}},
		}},
	})
	writeJSONFile(t, filepath.Join(config, "zpa_application_segment.expressions.json"), map[string]any{
		"resources": map[string]any{
			"zpa_application_segment.app_one": map[string]any{
				"server_groups": map[string]any{"expression": "var.operator_services"},
			},
		},
	})
	writeJSONFile(t, filepath.Join(config, "zpa_application_segment.generated.expressions.json"), map[string]any{
		"resources": map[string]any{
			"zpa_application_segment.app_one": map[string]any{
				"server_groups[0].id": map[string]any{
					"expression": `data.terraform_remote_state.zpa_server_group.outputs.iw_reference_ids.zpa_server_group["group_one"]`,
				},
			},
		},
	})

	blind := fixture.generateStateBlind(t)
	if blind == nil || !strings.Contains(blind.Error(), "conflicting expression binding") {
		t.Fatalf("state-blind generation error = %v, want a conflicting-expression-binding refusal", blind)
	}
	if _, err := os.Stat(fixture.referrerFile("main.tf")); !os.IsNotExist(err) {
		t.Errorf("state-blind refusal left output behind: main.tf stat = %v, want not-exist (conflict must refuse before writes)", err)
	}

	probeCalls := 0
	countingAbsentProbe := func(string, string) (StateProbeResult, error) {
		probeCalls++
		return StateProbeResult{Usable: false}, nil
	}
	aware := fixture.generateWithProbe(t, countingAbsentProbe)
	if aware == nil {
		t.Fatalf("state-aware generation error = nil, want the same refusal state-blind generation gives (%v)", blind)
	}
	if aware.Error() != blind.Error() {
		t.Errorf("state-aware refusal = %q, want the state-blind refusal %q", aware.Error(), blind.Error())
	}
	if probeCalls != 0 {
		t.Errorf("state probe consulted %d time(s), want 0: the conflict must refuse before state filtering runs", probeCalls)
	}
	if _, err := os.Stat(fixture.referrerFile("main.tf")); !os.IsNotExist(err) {
		t.Errorf("state-aware refusal left output behind: main.tf stat = %v, want not-exist", err)
	}
}

// TestFilterProbesEveryReferenceSoErrorsBeatAbsence pins error precedence in a
// binding that reaches more than one root. Stopping at the first absent
// reference means a later reference whose probe fails is never observed, and a
// probe failure -- the signal that state could not be read at all -- is
// downgraded to a silent fallback. Any error must beat absence regardless of
// reference order.
func TestFilterProbesEveryReferenceSoErrorsBeatAbsence(t *testing.T) {
	binding := ExpressionBinding{
		Address: "zpa_application_segment.app_one",
		Key:     "app_one",
		Path:    "segment_group_id",
		Expression: `[data.terraform_remote_state.absent_root.outputs.iw_reference_ids.zpa_segment_group["a"], ` +
			`data.terraform_remote_state.failing_root.outputs.iw_reference_ids.zpa_segment_group["b"]]`,
	}
	probeFailure := errors.New("probe state for root failing_root: boom")
	probe := func(rootLabel, referentType string) (StateProbeResult, error) {
		if rootLabel == "failing_root" {
			return StateProbeResult{}, probeFailure
		}
		return StateProbeResult{Usable: false}, nil
	}

	_, err := filterStatelessGeneratedBindings([]ExpressionBinding{binding}, "zpa_application_segment", probe, func(string) {})
	if !errors.Is(err, probeFailure) {
		t.Errorf("filterStatelessGeneratedBindings error = %v, want the probe failure from the reference after the absent one", err)
	}
}

// TestReferenceIDsPresentRequiresStateEnvelope pins that the probe only treats
// a document as state when it carries the envelope Terraform itself requires.
// Without the version check an arbitrary JSON object -- including {} -- reads
// as an applied root with no outputs and silently rewrites references to
// literals, and an output missing its type metadata reads as usable while
// Terraform refuses the same file.
func TestReferenceIDsPresentRequiresStateEnvelope(t *testing.T) {
	for _, testCase := range []struct {
		name string
		raw  string
	}{
		{name: "empty object", raw: `{}`},
		{name: "no version", raw: `{"outputs":{}}`},
		{name: "unsupported version", raw: `{"version":3,"outputs":{}}`},
		{name: "null outputs", raw: `{"version":4,"outputs":null}`},
		{name: "output without type", raw: `{"version":4,"outputs":{"iw_reference_ids":{"value":{"zpa_segment_group":{"segment_one":"sg-1"}}}}}`},
		{name: "output without value", raw: `{"version":4,"outputs":{"iw_reference_ids":{"type":["object",{}]}}}`},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if _, err := ReferenceIDsPresent([]byte(testCase.raw), "zpa_segment_group", "zpa_segment_group"); err == nil {
				t.Errorf("ReferenceIDsPresent(%s) error = nil, want a refusal for a document Terraform does not accept as state", testCase.raw)
			}
		})
	}
}

// TestReferenceIDsPresentAcceptsAppliedStateShape pins the positive side of the
// envelope check, so tightening it cannot degenerate into refusing everything.
// The shape is what Terraform writes: version 4, outputs carrying both value
// and type.
func TestReferenceIDsPresentAcceptsAppliedStateShape(t *testing.T) {
	applied := `{"version":4,"terraform_version":"1.15.4","serial":1,"lineage":"fixture",` +
		`"outputs":{"iw_reference_ids":{"value":{"zpa_segment_group":{"segment_one":"sg-1"}},` +
		`"type":["object",{"zpa_segment_group":["object",{"segment_one":"string"}]}]}},"resources":[]}`
	result, err := ReferenceIDsPresent([]byte(applied), "zpa_segment_group", "zpa_segment_group")
	if err != nil {
		t.Fatalf("ReferenceIDsPresent(applied state) error = %v, want nil", err)
	}
	if !result.Usable {
		t.Errorf("ReferenceIDsPresent(applied state) usable = false, want true")
	}

	noOutputs := `{"version":4,"terraform_version":"1.15.4","serial":1,"lineage":"fixture","outputs":{},"resources":[]}`
	result, err = ReferenceIDsPresent([]byte(noOutputs), "zpa_segment_group", "zpa_segment_group")
	if err != nil {
		t.Fatalf("ReferenceIDsPresent(applied state without outputs) error = %v, want nil", err)
	}
	if result.Usable {
		t.Errorf("ReferenceIDsPresent(applied state without outputs) usable = true, want false")
	}
}

// TestStateAwareMemoizationDistinguishesReferentTypes pins the memoization key.
// Keyed on the root alone, the first referent type's outcome is reused for
// every other type in that root, so a root publishing one type's identifiers
// but not another's classifies both the same way.
func TestStateAwareMemoizationDistinguishesReferentTypes(t *testing.T) {
	var calls []string
	probe := memoizedStateProbe(func(rootLabel, referentType string) (StateProbeResult, error) {
		calls = append(calls, rootLabel+"."+referentType)
		return StateProbeResult{Usable: referentType == "type_one"}, nil
	})

	first, err := probe("shared_root", "type_one")
	if err != nil {
		t.Fatalf("probe(type_one) error = %v, want nil", err)
	}
	second, err := probe("shared_root", "type_two")
	if err != nil {
		t.Fatalf("probe(type_two) error = %v, want nil", err)
	}
	if !first.Usable || second.Usable {
		t.Errorf("probe results = (%v, %v), want type_one usable and type_two not", first.Usable, second.Usable)
	}
	if len(calls) != 2 {
		t.Errorf("probe calls = %v, want one per referent type", calls)
	}
	if _, err := probe("shared_root", "type_one"); err != nil {
		t.Fatalf("repeat probe error = %v, want nil", err)
	}
	if len(calls) != 2 {
		t.Errorf("probe calls after repeat = %v, want the repeat served from the cache", calls)
	}
}

// TestStateAwareKeepsOperatorBindingWhenAnotherFallsBack pins that a fallback
// is scoped to the binding that triggered it. Operator bindings are never
// probed, so a generated binding falling back beside one must not take it with
// it -- an operator writing a remote-state reference by hand is asserting
// intent, and that assertion keeps failing loudly.
func TestStateAwareKeepsOperatorBindingWhenAnotherFallsBack(t *testing.T) {
	fixture := newStateAwareFixture(t)
	config := filepath.Join(fixture.workspace, "config", "tenant")
	writeJSONFile(t, filepath.Join(config, "zpa_application_segment.auto.tfvars.json"), map[string]any{
		"items": map[string]any{
			"app_one": map[string]any{"segment_group_id": "sg-1"},
			"app_two": map[string]any{"segment_group_id": "sg-2"},
		},
	})
	writeJSONFile(t, filepath.Join(config, "zpa_application_segment.expressions.json"), map[string]any{
		"resources": map[string]any{
			"zpa_application_segment.app_two": map[string]any{
				"segment_group_id": map[string]any{"expression": `local.operator_asserted_id`},
			},
		},
	})

	if err := fixture.generateWithProbe(t, absentProbe()); err != nil {
		t.Fatalf("GenerateEnvironmentRoots error = %v, want nil", err)
	}
	rendered := readFileString(t, fixture.referrerFile("expression_bindings.tf"))
	if !strings.Contains(rendered, "local.operator_asserted_id") {
		t.Errorf("expression_bindings.tf = %q, want the operator binding to survive a sibling's fallback", rendered)
	}
}

// TestReferenceIDsPresentAcceptsEmptyPerTypeMap pins the boundary between the
// two failure modes. An applied root that currently manages none of a type's
// resources publishes an empty key map; that root has usable state, and the
// individual key being missing is the case try() is deliberately left to catch
// at plan time. Reporting it unusable here would suppress that failure.
func TestReferenceIDsPresentAcceptsEmptyPerTypeMap(t *testing.T) {
	raw := `{"version":4,"outputs":{"iw_reference_ids":{"value":{"zpa_segment_group":{}},` +
		`"type":["object",{"zpa_segment_group":["object",{}]}]}}}`
	result, err := ReferenceIDsPresent([]byte(raw), "zpa_segment_group", "zpa_segment_group")
	if err != nil {
		t.Fatalf("ReferenceIDsPresent(empty per-type map) error = %v, want nil", err)
	}
	if !result.Usable {
		t.Errorf("ReferenceIDsPresent(empty per-type map) usable = false, want true")
	}
}

// TestEqualTreesComparesKeySets guards the comparator the byte-identity tests
// rely on. Comparing only values reachable by the left tree's keys made trees
// with different file names compare equal, which silently weakened every test
// built on it.
func TestEqualTreesComparesKeySets(t *testing.T) {
	if equalTrees(map[string]string{"left.tf": ""}, map[string]string{"right.tf": ""}) {
		t.Errorf("equalTrees(different key sets) = true, want false")
	}
	if !equalTrees(map[string]string{"main.tf": "body"}, map[string]string{"main.tf": "body"}) {
		t.Errorf("equalTrees(identical trees) = false, want true")
	}
}

// TestStateAwareStillRefusesBindingCycle pins that cycle detection runs before
// the state filter and is not conditioned on state-awareness. A cycle is
// malformed evidence: it cannot be resolved by falling back, and a state-aware
// run that dropped the participating bindings first would emit a root the
// state-blind run refuses.
//
// The absent probe would drop this binding if it were consulted, so the probe
// must never be reached at all.
func TestStateAwareStillRefusesBindingCycle(t *testing.T) {
	fixture := newStateAwareFixture(t)
	writeJSONFile(t, filepath.Join(fixture.workspace, "config", "tenant", "zpa_application_segment.generated.expressions.json"), map[string]any{
		"resources": map[string]any{
			"zpa_application_segment.app_one": map[string]any{
				"segment_group_id": map[string]any{
					// Both a module target and a remote-state reference. The
					// module target is what forms the cycle; the remote-state
					// reference is what gives the filter something to drop, so
					// filtering ahead of cycle detection is observable at all.
					// With the module target alone the filter finds no
					// reference, never probes, and the ordering mutation
					// survives.
					"expression": `[module.zpa_application_segment.iw_reference_ids["app_one"], ` +
						`data.terraform_remote_state.zpa_segment_group.outputs.iw_reference_ids.zpa_segment_group["segment_one"]]`,
				},
			},
		},
	})

	blind := fixture.generateStateBlind(t)
	if blind == nil {
		t.Fatalf("state-blind generation error = nil, want a cycle refusal")
	}

	var calls []string
	err := fixture.generateWithProbe(t, countingProbe(&calls, false))
	if err == nil {
		t.Fatalf("state-aware generation error = nil, want the same cycle refusal state-blind generation gives (%v)", blind)
	}
	// Identical text, not merely non-nil: a state-aware run that refused for
	// some other reason would satisfy a nil check while the cycle went
	// undetected.
	if err.Error() != blind.Error() {
		t.Errorf("state-aware error = %q, want the state-blind cycle refusal %q", err, blind)
	}
	if len(calls) != 0 {
		t.Errorf("probe calls = %v, want none: a cycle is refused before any state is read", calls)
	}
}

// writeBackendMarker writes the tenant's .backend marker, the file gen-env
// itself maintains, so a test can express a tenant whose backend was chosen
// on an earlier run without passing the flag on this one.
func (f stateAwareFixture) writeBackendMarker(t *testing.T, backend string) {
	t.Helper()
	directory := filepath.Join(f.outputRoot, "tenant")
	if err := os.MkdirAll(directory, 0o777); err != nil {
		t.Fatalf("create tenant directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(directory, ".backend"), []byte(backend+"\n"), 0o666); err != nil {
		t.Fatalf("write .backend marker: %v", err)
	}
}

// generateWith runs GenerateEnvironmentRoots with the fixture's baseline
// options after applying mutate, returning diagnostics and the error, so
// factory-seam tests can install StateProbeFor without duplicating the
// fixture wiring.
func (f stateAwareFixture) generateWith(
	t *testing.T,
	mutate func(*GenerateEnvironmentRootsOptions),
) ([]string, error) {
	t.Helper()
	diagnostics := make([]string, 0)
	outputRoot := f.outputRoot
	options := GenerateEnvironmentRootsOptions{
		Deployment:   loadDeploymentFile(t, f.deploymentPath),
		FormatHcl:    identityFormatter,
		OnDiagnostic: func(message string) { diagnostics = append(diagnostics, message) },
		OutputRoot:   &outputRoot,
		Root:         syntheticRootForTopology(t),
		Selectors:    []string{"zpa_application_segment"},
		StateAware:   true,
		Tenant:       "tenant",
	}
	mutate(&options)
	_, err := GenerateEnvironmentRoots(options)
	return diagnostics, err
}

// TestStateProbeForReceivesResolvedBackend pins that the factory sees the
// backend the run actually resolved -- here the .backend marker, with no
// backend option passed -- not the flag alone. Otherwise a marker-configured
// azurerm tenant would silently probe local state files that cannot exist.
func TestStateProbeForReceivesResolvedBackend(t *testing.T) {
	fixture := newStateAwareFixture(t)
	fixture.writeBackendMarker(t, "azurerm")

	seen := make([]string, 0)
	_, err := fixture.generateWith(t, func(options *GenerateEnvironmentRootsOptions) {
		options.StateProbeFor = func(backend *string) (StateProbe, error) {
			if backend == nil {
				seen = append(seen, "<nil>")
			} else {
				seen = append(seen, *backend)
			}
			return func(string, string) (StateProbeResult, error) {
				return StateProbeResult{Usable: true}, nil
			}, nil
		}
	})
	if err != nil {
		t.Fatalf("GenerateEnvironmentRoots error = %v, want nil", err)
	}
	if !reflect.DeepEqual(seen, []string{"azurerm"}) {
		t.Errorf("factory saw backends %v, want [azurerm] resolved from the marker", seen)
	}
}

// TestStateProbeForErrorAbortsGeneration pins fail-closed at the seam: a
// factory that cannot build a probe (azurerm without backend config being
// the motivating case) must abort the run before any root is written, not
// degrade references.
func TestStateProbeForErrorAbortsGeneration(t *testing.T) {
	fixture := newStateAwareFixture(t)

	_, err := fixture.generateWith(t, func(options *GenerateEnvironmentRootsOptions) {
		options.StateProbeFor = func(*string) (StateProbe, error) {
			return nil, errors.New("backend config required")
		}
	})
	if err == nil || !strings.Contains(err.Error(), "backend config required") {
		t.Fatalf("GenerateEnvironmentRoots error = %v, want the factory's refusal", err)
	}
	if _, statErr := os.Stat(fixture.referrerFile("main.tf")); !errors.Is(statErr, os.ErrNotExist) {
		t.Errorf("os.Stat(main.tf) error = %v, want os.ErrNotExist after an aborted run", statErr)
	}
}

// TestDirectStateProbeBeatsFactory pins precedence so existing library
// callers and every test above keep meaning what they said: an explicitly
// injected probe wins and the factory is never consulted.
func TestDirectStateProbeBeatsFactory(t *testing.T) {
	fixture := newStateAwareFixture(t)

	factoryCalled := false
	_, err := fixture.generateWith(t, func(options *GenerateEnvironmentRootsOptions) {
		options.StateProbe = func(string, string) (StateProbeResult, error) {
			return StateProbeResult{Usable: true}, nil
		}
		options.StateProbeFor = func(*string) (StateProbe, error) {
			factoryCalled = true
			return nil, errors.New("factory must not be consulted")
		}
	})
	if err != nil {
		t.Fatalf("GenerateEnvironmentRoots error = %v, want nil", err)
	}
	if factoryCalled {
		t.Errorf("StateProbeFor was consulted despite an explicit StateProbe")
	}
}

// TestNilFactoryResultFallsBackToLocalProbe pins the local path: a factory
// answering (nil, nil) declines to build a probe, and generation must use
// the local prober exactly as when no factory is installed -- proven here by
// the fallback note only the local prober's absent answer can produce.
func TestNilFactoryResultFallsBackToLocalProbe(t *testing.T) {
	fixture := newStateAwareFixture(t)

	diagnostics, err := fixture.generateWith(t, func(options *GenerateEnvironmentRootsOptions) {
		options.StateProbeFor = func(*string) (StateProbe, error) {
			return nil, nil
		}
	})
	if err != nil {
		t.Fatalf("GenerateEnvironmentRoots error = %v, want nil", err)
	}
	if !containsDiagnostic(diagnostics, "no usable state") {
		t.Errorf("diagnostics = %#v, want the local prober's fallback note", diagnostics)
	}
}

// TestReferenceIDsPresentReadsLegacyOutputName pins the iw_reference_ids
// rename bridge. A referent applied before the rename still publishes
// infrawright_reference_ids, and the probe must keep reading it until that
// root's next apply renames the output. When both names are present the
// current one is authoritative in both directions: a malformed legacy output
// beside a valid current one must not fail the probe, and a valid legacy one
// must not rescue a malformed current one.
func TestReferenceIDsPresentReadsLegacyOutputName(t *testing.T) {
	validOutput := `{"value":{"zpa_segment_group":{"segment_one":"sg-1"}},` +
		`"type":["object",{"zpa_segment_group":["object",{"segment_one":"string"}]}]}`
	malformedOutput := `{"value":{"zpa_segment_group":{"segment_one":"sg-1"}}}`

	legacyOnly := `{"version":4,"outputs":{"infrawright_reference_ids":` + validOutput + `}}`
	result, err := ReferenceIDsPresent([]byte(legacyOnly), "zpa_segment_group", "zpa_segment_group")
	if err != nil {
		t.Fatalf("ReferenceIDsPresent(legacy output name) error = %v, want nil", err)
	}
	if !result.Usable {
		t.Errorf("ReferenceIDsPresent(legacy output name) usable = false, want true")
	}

	currentBesideMalformedLegacy := `{"version":4,"outputs":{` +
		`"infrawright_reference_ids":` + malformedOutput + `,` +
		`"iw_reference_ids":` + validOutput + `}}`
	result, err = ReferenceIDsPresent([]byte(currentBesideMalformedLegacy), "zpa_segment_group", "zpa_segment_group")
	if err != nil {
		t.Fatalf("ReferenceIDsPresent(current beside malformed legacy) error = %v, want the current output to be authoritative", err)
	}
	if !result.Usable {
		t.Errorf("ReferenceIDsPresent(current beside malformed legacy) usable = false, want true")
	}

	malformedCurrentBesideValidLegacy := `{"version":4,"outputs":{` +
		`"infrawright_reference_ids":` + validOutput + `,` +
		`"iw_reference_ids":` + malformedOutput + `}}`
	if _, err := ReferenceIDsPresent([]byte(malformedCurrentBesideValidLegacy), "zpa_segment_group", "zpa_segment_group"); err == nil {
		t.Errorf("ReferenceIDsPresent(malformed current beside valid legacy) error = nil, want the current output to be authoritative")
	}
}
