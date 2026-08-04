package envgen

// referent_alternate_spaces_e2e_test.go is the phase-6 synthetic acceptance
// test for the referent-alternate-id-spaces design (see
// docs/superpowers/specs/2026-08-04-referent-alternate-id-spaces.md,
// "Acceptance criteria (engine)", first bullet). Unlike the Phase 3 test in
// internal/transformrun/referent_alternate_spaces_test.go (transform lane
// only) and the Phase 4 test in this package's
// referent_alternate_spaces_test.go (envgen lane only, fed by hand-written
// fixture config/lookup/bindings files), this test drives BOTH lanes back
// to back over the SAME workspace: RunTransformBatch produces the real
// committed config, lookup sidecar, and generated-bindings cache from raw
// pulls, and GenerateEnvironmentRoots then reads exactly those artifacts
// off disk -- proving the two phases agree by construction rather than by
// two independently maintained fixtures.
//
// This lands as an internal (non-_test-suffixed) package under
// internal/envgen rather than an external test package: envgen's own
// non-test code already imports internal/transformrun
// (environment_generator.go), so a test in package envgen importing
// transformrun creates no import cycle and needed no isolation.

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/dvmrry/infrawright-dev/go/internal/canonjson"
	"github.com/dvmrry/infrawright-dev/go/internal/deployment"
	"github.com/dvmrry/infrawright-dev/go/internal/metadata"
	"github.com/dvmrry/infrawright-dev/go/internal/tfrender"
	"github.com/dvmrry/infrawright-dev/go/internal/transformrun"
)

// e2eAlternateSpaceFixture is the smallest pack universe exercising both
// citing forms of the same referent: one generated referent (sample_group,
// carrying a string "id" in the CUSTOM_nn shape the design doc's motivating
// case uses, plus a numeric "val" alternate identity) and two generated
// referrers -- sample_rule_id cites sample_group.id (the default space, no
// referent_id_field declared), sample_rule_val cites sample_group.val via a
// declared referent_id_field.
type e2eAlternateSpaceFixture struct {
	dep       deployment.Deployment
	pulls     string
	root      metadata.LoadedPackRoot
	workspace string
}

func newE2EAlternateSpaceFixture(t *testing.T) e2eAlternateSpaceFixture {
	t.Helper()
	workspace := t.TempDir()
	packsRoot := filepath.Join(workspace, "packs")

	optionalString := func() metadata.JsonObject { return terraformTestAttribute("string", "optional") }
	computedString := func() metadata.JsonObject { return terraformTestAttribute("string", "computed") }
	computedNumber := func() metadata.JsonObject { return terraformTestAttribute("number", "computed") }

	resourceSchemas := metadata.JsonObject{
		"sample_group": terraformTestBlock(metadata.JsonObject{
			"id":   computedString(),
			"val":  computedNumber(),
			"name": optionalString(),
		}),
		"sample_rule_id": terraformTestBlock(metadata.JsonObject{
			"id":       computedString(),
			"group_id": optionalString(),
			"name":     optionalString(),
		}),
		"sample_rule_val": terraformTestBlock(metadata.JsonObject{
			"id":        computedString(),
			"group_ref": optionalString(),
			"name":      optionalString(),
		}),
	}
	registry := metadata.JsonObject{
		"sample_group":    metadata.JsonObject{"generate": true, "product": "sample"},
		"sample_rule_id":  metadata.JsonObject{"generate": true, "product": "sample"},
		"sample_rule_val": metadata.JsonObject{"generate": true, "product": "sample"},
	}
	manifest := metadata.JsonObject{
		"pin":               "1.0.0",
		"provider_prefixes": metadata.JsonObject{"sample_": "sample"},
		"provider_sources":  metadata.JsonObject{"sample": "example/sample"},
		"references": metadata.JsonObject{
			"sample_rule_id": metadata.JsonObject{
				"group_id": metadata.JsonObject{"name_field": "name", "referent": "sample_group"},
			},
			"sample_rule_val": metadata.JsonObject{
				"group_ref": metadata.JsonObject{
					"name_field": "name", "referent": "sample_group", "referent_id_field": "val",
				},
			},
		},
	}
	writeSyntheticTopologyPack(t, packsRoot, "sample", manifest, registry, resourceSchemas)
	profilePath := filepath.Join(packsRoot, "sample.packset.json")
	writeJSONFile(t, profilePath, metadata.JsonObject{
		"kind": metadata.PackSetKind, "version": 1,
		"packs": []any{"sample"}, "shared": []any{},
	})
	root, err := metadata.LoadPackRoot(metadata.LoadPackRootOptions{
		PacksRoot: packsRoot, ProfilePath: &profilePath,
	})
	if err != nil {
		t.Fatalf("LoadPackRoot(e2e alternate-space fixture) error = %v", err)
	}

	deploymentPath := filepath.Join(workspace, "deployment.json")
	writeJSONFile(t, deploymentPath, metadata.JsonObject{
		"module_dir": filepath.Join(workspace, "modules"),
		"overlay":    workspace,
		"roots": metadata.JsonObject{
			"sample": metadata.JsonObject{"cross_state_references": true},
		},
	})
	dep, err := deployment.LoadDeployment(deploymentPath)
	if err != nil {
		t.Fatalf("LoadDeployment(e2e alternate-space fixture) error = %v", err)
	}

	pulls := filepath.Join(workspace, "pulls")
	writeJSONFile(t, filepath.Join(pulls, "sample_group.json"), []any{
		map[string]any{"id": "CUSTOM_1", "val": json.Number("501"), "name": "Group One"},
		map[string]any{"id": "CUSTOM_2", "val": json.Number("502"), "name": "Group Two"},
	})
	writeJSONFile(t, filepath.Join(pulls, "sample_rule_id.json"), []any{
		map[string]any{"id": "rule-a", "group_id": "CUSTOM_1", "name": "Rule A"},
	})
	writeJSONFile(t, filepath.Join(pulls, "sample_rule_val.json"), []any{
		map[string]any{"id": "rule-b", "group_ref": json.Number("501"), "name": "Rule B"},
	})

	return e2eAlternateSpaceFixture{dep: dep, pulls: pulls, root: root, workspace: workspace}
}

// TestReferentAlternateSpaceEndToEndTransformThenEnvgen is the design doc's
// first acceptance criterion, run for real across both engine lanes: after
// RunTransformBatch, both referrers' committed configs carry the SAME
// minted token and the referent's sidecar publishes the canonical maps plus
// spaces.val; after GenerateEnvironmentRoots reads that same workspace, the
// id-referrer resolves through outputs.iw_reference_ids with its lookup
// local, the val-referrer resolves through outputs.iw_reference_ids_val
// with the _val local, and the referent root's own outputs file carries
// both output blocks.
func TestReferentAlternateSpaceEndToEndTransformThenEnvgen(t *testing.T) {
	fixture := newE2EAlternateSpaceFixture(t)

	// Step 1: transform all three selectors for real -- no hand-written
	// config, lookup, or generated-bindings fixture files.
	result, err := transformrun.RunTransformBatch(transformrun.RunTransformBatchOptions{
		Deployment:     fixture.dep,
		InputDirectory: fixture.pulls,
		OnDiagnostic:   func(string) {},
		Root:           fixture.root,
		Selectors:      []string{"sample_group", "sample_rule_id", "sample_rule_val"},
		Tenant:         "tenant",
	})
	if err != nil {
		t.Fatalf("RunTransformBatch(e2e alternate-space fixture) error = %v", err)
	}
	if len(result.Failed) != 0 || len(result.Skipped) != 0 {
		t.Fatalf("RunTransformBatch(e2e alternate-space fixture) result = %#v, want no failures or skips", result)
	}

	idPaths, err := tfrender.ComputeTransformArtifactPaths(fixture.dep, "sample_rule_id", "tenant", tfrender.TransformArtifactModeGenerated)
	if err != nil {
		t.Fatalf("ComputeTransformArtifactPaths(sample_rule_id) error = %v", err)
	}
	valPaths, err := tfrender.ComputeTransformArtifactPaths(fixture.dep, "sample_rule_val", "tenant", tfrender.TransformArtifactModeGenerated)
	if err != nil {
		t.Fatalf("ComputeTransformArtifactPaths(sample_rule_val) error = %v", err)
	}
	groupPaths, err := tfrender.ComputeTransformArtifactPaths(fixture.dep, "sample_group", "tenant", tfrender.TransformArtifactModeGenerated)
	if err != nil {
		t.Fatalf("ComputeTransformArtifactPaths(sample_group) error = %v", err)
	}

	wantToken := "sample_group.group_one"
	idConfig := readFileString(t, idPaths.Config)
	if !contains(idConfig, wantToken) {
		t.Errorf("sample_rule_id committed config = %q, want minted token %q", idConfig, wantToken)
	}
	valConfig := readFileString(t, valPaths.Config)
	if !contains(valConfig, wantToken) {
		t.Errorf("sample_rule_val committed config = %q, want the SAME minted token %q", valConfig, wantToken)
	}

	lookupText := readFileString(t, groupPaths.Lookup)
	parsedLookup, err := canonjson.ParseDataJSONLosslessly(lookupText)
	if err != nil {
		t.Fatalf("parsing sample_group lookup sidecar: %v", err)
	}
	lookupObject, ok := parsedLookup.(map[string]any)
	if !ok {
		t.Fatalf("sample_group lookup sidecar = %#v, want a JSON object", parsedLookup)
	}
	for _, canonicalKey := range []string{"by_id", "id_by_key", "key_by_id"} {
		if _, ok := lookupObject[canonicalKey].(map[string]any); !ok {
			t.Errorf("sample_group lookup sidecar = %q, want canonical map %q", lookupText, canonicalKey)
		}
	}
	spaces, ok := lookupObject["spaces"].(map[string]any)
	if !ok {
		t.Fatalf("sample_group lookup sidecar = %q, want a top-level \"spaces\" section", lookupText)
	}
	valSpace, ok := spaces["val"].(map[string]any)
	if !ok {
		t.Fatalf("sample_group lookup sidecar spaces = %#v, want a \"val\" entry", spaces)
	}
	keyByID, ok := valSpace["key_by_id"].(map[string]any)
	if !ok {
		t.Fatalf("sample_group lookup sidecar spaces.val = %#v, want key_by_id", valSpace)
	}
	if keyByID["501"] != "group_one" {
		t.Errorf("sample_group lookup sidecar spaces.val.key_by_id[501] = %v, want %q", keyByID["501"], "group_one")
	}

	// Step 2: generate environment roots off exactly the artifacts step 1
	// just wrote -- no fixture config/lookup/bindings files of its own.
	outputRoot := filepath.Join(fixture.workspace, "generated")
	if _, err := GenerateEnvironmentRoots(GenerateEnvironmentRootsOptions{
		Deployment: fixture.dep,
		FormatHcl:  identityFormatter,
		OutputRoot: &outputRoot,
		Root:       fixture.root,
		Selectors:  []string{"sample_rule_id", "sample_rule_val"},
		Tenant:     "tenant",
	}); err != nil {
		t.Fatalf("GenerateEnvironmentRoots(e2e alternate-space fixture) error = %v", err)
	}

	groupMain := readFileString(t, filepath.Join(outputRoot, "tenant", "sample_group", "main.tf"))
	wantCanonicalOutput := `output "iw_reference_ids" {`
	if !contains(groupMain, wantCanonicalOutput) {
		t.Errorf("sample_group main.tf = %q, want canonical output block %q", groupMain, wantCanonicalOutput)
	}
	wantSiblingOutput := `output "iw_reference_ids_val" {`
	if !contains(groupMain, wantSiblingOutput) {
		t.Errorf("sample_group main.tf = %q, want sibling output block %q", groupMain, wantSiblingOutput)
	}
	wantSiblingValue := `value = {
    sample_group = { for key, item in module.sample_group.items : key => item.val }
  }`
	if !contains(groupMain, wantSiblingValue) {
		t.Errorf("sample_group main.tf = %q, want sibling output value keyed on item.val %q", groupMain, wantSiblingValue)
	}

	idBindings := readFileString(t, filepath.Join(outputRoot, "tenant", "sample_rule_id", "expression_bindings.tf"))
	wantIDResolver := `data.terraform_remote_state.sample_group.outputs.iw_reference_ids.sample_group["group_one"]`
	if !contains(idBindings, wantIDResolver) {
		t.Errorf("sample_rule_id expression_bindings.tf = %q, want canonical resolver %q", idBindings, wantIDResolver)
	}
	wantIDLocal := `iw_reference_lookup_sample_group = fileexists`
	if !contains(idBindings, wantIDLocal) {
		t.Errorf("sample_rule_id expression_bindings.tf = %q, want canonical lookup local", idBindings)
	}

	valBindings := readFileString(t, filepath.Join(outputRoot, "tenant", "sample_rule_val", "expression_bindings.tf"))
	wantValResolver := `data.terraform_remote_state.sample_group.outputs.iw_reference_ids_val.sample_group["group_one"]`
	if !contains(valBindings, wantValResolver) {
		t.Errorf("sample_rule_val expression_bindings.tf = %q, want alternate-space resolver %q", valBindings, wantValResolver)
	}
	wantValLocal := `iw_reference_lookup_sample_group_val = fileexists`
	if !contains(valBindings, wantValLocal) {
		t.Errorf("sample_rule_val expression_bindings.tf = %q, want _val lookup local", valBindings)
	}
	wantIDTryFallback := `try(data.terraform_remote_state.sample_group.outputs.iw_reference_ids.sample_group["group_one"], local.iw_reference_lookup_sample_group["group_one"])`
	if !contains(idBindings, wantIDTryFallback) {
		t.Errorf("sample_rule_id expression_bindings.tf = %q, want committed-lookup fallback %q", idBindings, wantIDTryFallback)
	}
	wantValTryFallback := `try(data.terraform_remote_state.sample_group.outputs.iw_reference_ids_val.sample_group["group_one"], local.iw_reference_lookup_sample_group_val["group_one"])`
	if !contains(valBindings, wantValTryFallback) {
		t.Errorf("sample_rule_val expression_bindings.tf = %q, want committed-lookup fallback %q", valBindings, wantValTryFallback)
	}
}
