package transformrun

// This file pins Phase 3 (batch-runner level) and the Phase 7 self-describing
// token revision of the referent-alternate-id-spaces design (see
// docs/superpowers/specs/2026-08-04-referent-alternate-id-spaces.md): a
// referent with a declared alternate space, cited by two active generated
// referrers -- one through the canonical id, one through the alternate
// space -- tokenizes to the SAME key but a DIFFERENT token spelling: the id
// referrer mints the bare "sample_group.group_one" token, the val referrer
// mints the explicit "sample_group.group_one.val" token, and the referent's
// own sidecar publishes the "spaces" section the alternate referrer's
// derivation reads.

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dvmrry/infrawright-dev/go/internal/canonjson"
	"github.com/dvmrry/infrawright-dev/go/internal/deployment"
	"github.com/dvmrry/infrawright-dev/go/internal/metadata"
	"github.com/dvmrry/infrawright-dev/go/internal/tfrender"
)

// alternateSpacePackRoot builds the smallest pack universe with one
// generated referent (sample_group, carrying both an "id" string identity
// and a "val" numeric alternate identity) and two generated referrers:
// sample_rule_id cites sample_group.id (the default space, no
// referent_id_field); sample_rule_val cites sample_group.val via a declared
// referent_id_field.
func alternateSpacePackRoot(t *testing.T) metadata.LoadedPackRoot {
	t.Helper()
	packsRoot := t.TempDir()
	attribute := func(attributeType any, class string) metadata.JsonObject {
		return metadata.JsonObject{"type": attributeType, class: true}
	}
	schemas := metadata.JsonObject{
		"sample_group": metadata.JsonObject{"block": metadata.JsonObject{"attributes": metadata.JsonObject{
			"id":   attribute("string", "computed"),
			"val":  attribute("number", "computed"),
			"name": attribute("string", "optional"),
		}}},
		"sample_rule_id": metadata.JsonObject{"block": metadata.JsonObject{"attributes": metadata.JsonObject{
			"group_id": attribute("string", "optional"),
			"id":       attribute("string", "computed"),
			"name":     attribute("string", "optional"),
		}}},
		"sample_rule_val": metadata.JsonObject{"block": metadata.JsonObject{"attributes": metadata.JsonObject{
			"group_ref": attribute("number", "optional"),
			"id":        attribute("string", "computed"),
			"name":      attribute("string", "optional"),
		}}},
	}
	registry := metadata.JsonObject{}
	for resourceType := range schemas {
		registry[resourceType] = metadata.JsonObject{"generate": true, "product": "sample"}
	}
	writeJSON(t, filepath.Join(packsRoot, "sample", "pack.json"), metadata.JsonObject{
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
	})
	writeJSON(t, filepath.Join(packsRoot, "sample", "registry.json"), registry)
	writeJSON(t, filepath.Join(packsRoot, "sample", "schemas", "provider", "sample.json"), metadata.JsonObject{
		"resource_schemas": schemas,
	})
	profilePath := filepath.Join(packsRoot, "sample.packset.json")
	writeJSON(t, profilePath, metadata.JsonObject{
		"kind": metadata.PackSetKind, "version": 1,
		"packs": []any{"sample"}, "shared": []any{},
	})
	root, err := metadata.LoadPackRoot(metadata.LoadPackRootOptions{
		PacksRoot: packsRoot, ProfilePath: &profilePath,
	})
	if err != nil {
		t.Fatalf("LoadPackRoot(alternate space fixture): %v", err)
	}
	return root
}

// TestAlternateSpaceReferrersShareTheSameTokenAndSidecarPublishesSpaces runs
// the real batch runner over the fixture above end to end: pulls for the
// referent (two items, each with a distinct id and val) and the two
// referrers (one citing id, one citing val), then asserts the id referrer's
// committed config carries the bare "sample_group.group_one" token, the val
// referrer's carries the explicit "sample_group.group_one.val" token for the
// SAME key, and the referent's lookup sidecar publishes a "spaces.val"
// section.
func TestAlternateSpaceReferrersShareTheSameTokenAndSidecarPublishesSpaces(t *testing.T) {
	workspace := t.TempDir()
	root := alternateSpacePackRoot(t)
	dep := deployment.Deployment{
		Overlay: workspace,
		Roots:   map[string]deployment.RootProviderConfig{"sample": {HasCrossStateReferences: true, CrossStateReferences: true}},
	}
	pulls := filepath.Join(workspace, "pulls")
	writeJSON(t, filepath.Join(pulls, "sample_group.json"), []any{
		map[string]any{"id": "CUSTOM_1", "val": json.Number("501"), "name": "Group One"},
		map[string]any{"id": "CUSTOM_2", "val": json.Number("502"), "name": "Group Two"},
	})
	writeJSON(t, filepath.Join(pulls, "sample_rule_id.json"), []any{
		map[string]any{"id": "rule-a", "group_id": "CUSTOM_1", "name": "Rule A"},
	})
	writeJSON(t, filepath.Join(pulls, "sample_rule_val.json"), []any{
		map[string]any{"id": "rule-b", "group_ref": json.Number("501"), "name": "Rule B"},
	})

	result, err := RunTransformBatch(RunTransformBatchOptions{
		Deployment:     dep,
		InputDirectory: pulls,
		OnDiagnostic:   func(string) {},
		Root:           root,
		Selectors:      []string{"sample_group", "sample_rule_id", "sample_rule_val"},
		Tenant:         "tenant",
	})
	if err != nil {
		t.Fatalf("RunTransformBatch: %v", err)
	}
	if len(result.Failed) != 0 || len(result.Skipped) != 0 {
		t.Fatalf("RunTransformBatch result = %#v, want no failures or skips", result)
	}

	idPaths, err := tfrender.ComputeTransformArtifactPaths(dep, "sample_rule_id", "tenant", tfrender.TransformArtifactModeGenerated)
	if err != nil {
		t.Fatalf("ComputeTransformArtifactPaths(sample_rule_id): %v", err)
	}
	valPaths, err := tfrender.ComputeTransformArtifactPaths(dep, "sample_rule_val", "tenant", tfrender.TransformArtifactModeGenerated)
	if err != nil {
		t.Fatalf("ComputeTransformArtifactPaths(sample_rule_val): %v", err)
	}
	groupPaths, err := tfrender.ComputeTransformArtifactPaths(dep, "sample_group", "tenant", tfrender.TransformArtifactModeGenerated)
	if err != nil {
		t.Fatalf("ComputeTransformArtifactPaths(sample_group): %v", err)
	}

	wantIDToken := "sample_group.group_one"
	idConfig := readFile(t, idPaths.Config)
	if !strings.Contains(idConfig, wantIDToken) {
		t.Errorf("sample_rule_id config = %q, want it to carry token %q", idConfig, wantIDToken)
	}
	wantValToken := "sample_group.group_one.val"
	valConfig := readFile(t, valPaths.Config)
	if !strings.Contains(valConfig, wantValToken) {
		t.Errorf("sample_rule_val config = %q, want it to carry the explicit token %q", valConfig, wantValToken)
	}

	lookupText := readFile(t, groupPaths.Lookup)
	parsed, err := canonjson.ParseDataJSONLosslessly(lookupText)
	if err != nil {
		t.Fatalf("parsing sample_group lookup: %v", err)
	}
	root2, ok := parsed.(map[string]any)
	if !ok {
		t.Fatalf("sample_group lookup = %#v, want a JSON object", parsed)
	}
	spaces, ok := root2["spaces"].(map[string]any)
	if !ok {
		t.Fatalf("sample_group lookup = %q, want a top-level \"spaces\" section", lookupText)
	}
	valSpace, ok := spaces["val"].(map[string]any)
	if !ok {
		t.Fatalf("sample_group lookup spaces = %#v, want a \"val\" entry", spaces)
	}
	keyByID, ok := valSpace["key_by_id"].(map[string]any)
	if !ok {
		t.Fatalf("sample_group lookup spaces.val = %#v, want key_by_id", valSpace)
	}
	if keyByID["501"] != "group_one" {
		t.Errorf("sample_group lookup spaces.val.key_by_id[501] = %v, want %q", keyByID["501"], "group_one")
	}
}
