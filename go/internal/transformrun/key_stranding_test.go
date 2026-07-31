package transformrun

// This file pins the runner-level half of the lookup-key-stranding guard, the
// evidence gap the round-3 adversarial re-review named: every other regression
// for that guard exercises tfrender's compile in isolation, and the defect the
// re-review found lives in what the RUNNER does around it -- it publishes each
// selected type immediately and independently, and continues past a later
// member that skips or fails.
//
// See docs/superpowers/specs/2026-07-31-sidecar-minimization-design.md.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/dvmrry/infrawright-dev/go/internal/deployment"
	"github.com/dvmrry/infrawright-dev/go/internal/metadata"
)

func writeJSON(t *testing.T, file string, value any) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(file), 0o777); err != nil {
		t.Fatalf("os.MkdirAll(%s): %v", filepath.Dir(file), err)
	}
	encoded, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatalf("json.Marshal(%s): %v", file, err)
	}
	if err := os.WriteFile(file, append(encoded, '\n'), 0o666); err != nil {
		t.Fatalf("os.WriteFile(%s): %v", file, err)
	}
}

func readFile(t *testing.T, file string) string {
	t.Helper()
	raw, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("os.ReadFile(%s): %v", file, err)
	}
	return string(raw)
}

// samplePackRoot is the smallest pack universe carrying one reference edge:
// sample_referrer.group_id -> sample_group. The edge is what gives sample_group
// an inferred lookup (transformLookupNameField), which is the artifact the
// stranding guard protects.
func samplePackRoot(t *testing.T) metadata.LoadedPackRoot {
	t.Helper()
	packsRoot := t.TempDir()

	attribute := func(attributeType any, class string) metadata.JsonObject {
		return metadata.JsonObject{"type": attributeType, class: true}
	}
	schemas := metadata.JsonObject{
		"sample_group": metadata.JsonObject{"block": metadata.JsonObject{"attributes": metadata.JsonObject{
			"id":   attribute("string", "computed"),
			"name": attribute("string", "optional"),
		}}},
		"sample_referrer": metadata.JsonObject{"block": metadata.JsonObject{"attributes": metadata.JsonObject{
			"group_id": attribute("string", "optional"),
			"id":       attribute("string", "computed"),
			"name":     attribute("string", "optional"),
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
			"sample_referrer": metadata.JsonObject{
				"group_id": metadata.JsonObject{"name_field": "name", "referent": "sample_group"},
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
	loaded, err := metadata.LoadPackRoot(metadata.LoadPackRootOptions{
		PacksRoot: packsRoot, ProfilePath: &profilePath,
	})
	if err != nil {
		t.Fatalf("LoadPackRoot(sample fixture): %v", err)
	}
	return loaded
}

// TestRunTransformRefusesRatherThanStrandingWhenALaterMemberSkips is the
// re-review's deterministic repro, run end to end through the real batch
// runner.
//
// The committed tree holds a lookup decoding "segment_one" and a referrer config
// naming "sample_group.segment_one". The referent's fresh input renames that
// item, so its rebuilt lookup would drop the key. Both types are selected, but
// the referrer's pull input is absent, so the runner skips it AFTER the
// referent has already been published -- exactly the partial run the
// type-membership exemption could not see.
//
// The run must refuse before publishing, leaving the old lookup and the old
// referrer config intact as a matched pair. An exemption based on selection
// membership publishes the new lookup and then skips the repair, leaving a token
// nothing decodes.
func TestRunTransformRefusesRatherThanStrandingWhenALaterMemberSkips(t *testing.T) {
	workspace := t.TempDir()
	root := samplePackRoot(t)
	dep := deployment.Deployment{
		Overlay: workspace,
		Roots:   map[string]deployment.RootProviderConfig{"sample": {HasCrossStateReferences: true, CrossStateReferences: true}},
	}

	configDirectory := filepath.Join(workspace, "config", "tenant")
	lookup := filepath.Join(configDirectory, "lookups", "sample_group.lookup.json")
	referrerConfig := filepath.Join(configDirectory, "sample_referrer.auto.tfvars.json")
	writeJSON(t, lookup, map[string]any{
		"by_id":     map[string]any{"sg-1": "Segment One"},
		"id_by_key": map[string]any{"segment_one": "sg-1"},
		"key_by_id": map[string]any{"sg-1": "segment_one"},
	})
	writeJSON(t, referrerConfig, map[string]any{
		"items": map[string]any{"one": map[string]any{"group_id": "sample_group.segment_one"}},
	})
	lookupBefore := readFile(t, lookup)
	referrerBefore := readFile(t, referrerConfig)

	// The referent's item is renamed; the referrer has NO pull input, so the
	// runner will skip it after the referent has been processed.
	pulls := filepath.Join(workspace, "pulls")
	writeJSON(t, filepath.Join(pulls, "sample_group.json"), []any{
		map[string]any{"id": "sg-1", "name": "Segment Uno"},
	})

	var diagnostics []string
	result, err := RunTransformBatch(RunTransformBatchOptions{
		Deployment:     dep,
		InputDirectory: pulls,
		OnDiagnostic:   func(message string) { diagnostics = append(diagnostics, message) },
		Root:           root,
		Selectors:      []string{},
		Tenant:         "tenant",
	})
	if err != nil {
		t.Fatalf("RunTransformBatch error = %v, want nil (per-resource failures are reported in the result)", err)
	}
	if !containsValue(result.Failed, "sample_group") {
		t.Fatalf("result.Failed = %v (skipped %v, processed %v), want the referent refused rather than silently stranding its dependent",
			result.Failed, result.Skipped, result.Processed)
	}
	if !containsValue(result.Skipped, "sample_referrer") {
		t.Fatalf("result.Skipped = %v, want the dependent skipped -- the fixture does not reproduce a partial run otherwise", result.Skipped)
	}
	if got := readFile(t, lookup); got != lookupBefore {
		t.Errorf("lookup changed despite the refusal:\ngot  %s\nwant %s", got, lookupBefore)
	}
	if got := readFile(t, referrerConfig); got != referrerBefore {
		t.Errorf("referrer config changed:\ngot  %s\nwant %s", got, referrerBefore)
	}
	if !anyContains(diagnostics, "sample_group.segment_one") {
		t.Errorf("diagnostics = %v, want the refusal to name the stranded token", diagnostics)
	}
}

// TestRunTransformPublishesWhenNoCommittedTokenNamesTheDroppedKey is the
// honest half: the same rename, with no committed config naming the departing
// key, must publish exactly as before. Without it, a guard that refused every
// rename would satisfy the test above.
func TestRunTransformPublishesWhenNoCommittedTokenNamesTheDroppedKey(t *testing.T) {
	workspace := t.TempDir()
	root := samplePackRoot(t)
	dep := deployment.Deployment{
		Overlay: workspace,
		Roots:   map[string]deployment.RootProviderConfig{"sample": {HasCrossStateReferences: true, CrossStateReferences: true}},
	}

	configDirectory := filepath.Join(workspace, "config", "tenant")
	lookup := filepath.Join(configDirectory, "lookups", "sample_group.lookup.json")
	writeJSON(t, lookup, map[string]any{
		"by_id":     map[string]any{"sg-1": "Segment One"},
		"id_by_key": map[string]any{"segment_one": "sg-1"},
		"key_by_id": map[string]any{"sg-1": "segment_one"},
	})
	writeJSON(t, filepath.Join(configDirectory, "sample_referrer.auto.tfvars.json"), map[string]any{
		"items": map[string]any{"one": map[string]any{"group_id": "some-unrelated-id"}},
	})

	pulls := filepath.Join(workspace, "pulls")
	writeJSON(t, filepath.Join(pulls, "sample_group.json"), []any{
		map[string]any{"id": "sg-1", "name": "Segment Uno"},
	})

	result, err := RunTransformBatch(RunTransformBatchOptions{
		Deployment: dep, InputDirectory: pulls, OnDiagnostic: func(string) {},
		Root: root, Selectors: []string{}, Tenant: "tenant",
	})
	if err != nil {
		t.Fatalf("RunTransformBatch error = %v, want nil", err)
	}
	if len(result.Failed) != 0 {
		t.Fatalf("result.Failed = %v, want the rename published when nothing references the departing key", result.Failed)
	}
	if got := readFile(t, lookup); got == "" || !anyContains([]string{got}, "segment_uno") {
		t.Errorf("lookup = %s, want the renamed key published", got)
	}
}

func containsValue(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func anyContains(values []string, want string) bool {
	for _, value := range values {
		if len(value) >= len(want) && indexOf(value, want) >= 0 {
			return true
		}
	}
	return false
}

func indexOf(haystack, needle string) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}
