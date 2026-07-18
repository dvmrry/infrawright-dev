package adopt

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/dvmrry/infrawright-dev/go/internal/canonjson"
	"github.com/dvmrry/infrawright-dev/go/internal/metadata"
)

func parsedAdoptionPolicy(t *testing.T, text string) any {
	t.Helper()
	value, err := canonjson.ParseDataJSONLosslessly(text)
	if err != nil {
		t.Fatalf("ParseDataJSONLosslessly: %v", err)
	}
	return value
}

func policyPaths(value any, resourceType, mode string) []string {
	resources, _ := value.(map[string]any)["resource_types"].(map[string]any)
	resource, _ := resources[resourceType].(map[string]any)
	entries, _ := resource[mode].([]any)
	paths := make([]string, 0, len(entries))
	for _, raw := range entries {
		entry, _ := raw.(map[string]any)
		path, _ := entry["path"].(string)
		paths = append(paths, path)
	}
	return paths
}

func TestMergeAdoptionPolicyDataExactVersionAndAppendOrder(t *testing.T) {
	base := parsedAdoptionPolicy(t, `{"version":10e-1,"resource_types":{"test_item":{"projection_omit":[{"path":"base","reason":"test","approved_by":"unit"}]}}}`)
	override := parsedAdoptionPolicy(t, `{"version":1.0,"resource_types":{"test_item":{"projection_omit":[{"path":"user","reason":"test","approved_by":"unit"}]}}}`)
	merged := MergeAdoptionPolicyData(base, override)
	if got, want := policyPaths(merged, "test_item", "projection_omit"), []string{"base", "user"}; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("merged paths = %v, want %v", got, want)
	}
	version := merged.(map[string]any)["version"]
	if !metadata.IsSupportedDriftPolicyVersion(version) {
		t.Fatalf("merged version = %v, want exact supported one", version)
	}

	nearOne := parsedAdoptionPolicy(t, `{"version":1.0000000000000000001,"resource_types":{}}`)
	replaced := MergeAdoptionPolicyData(base, nearOne)
	if got := replaced.(map[string]any)["version"]; got != json.Number("1.0000000000000000001") {
		t.Fatalf("unsupported-version merge = %v, want untouched override", got)
	}
}

func TestPackAdoptionPolicyDataUsesActiveManifestOrder(t *testing.T) {
	entry := func(path string) metadata.JsonObject {
		return metadata.JsonObject{"path": path, "reason": "test", "approved_by": "unit"}
	}
	manifest := func(name, path string) metadata.PackManifest {
		return metadata.PackManifest{Name: name, Data: metadata.JsonObject{"drift_policy": metadata.JsonObject{
			"version": float64(1), "resource_types": metadata.JsonObject{
				"test_item": metadata.JsonObject{"projection_omit": []any{entry(path)}},
			},
		}}}
	}
	root := metadata.LoadedPackRoot{
		Active: metadata.PackSelection{Packs: []string{"first", "second"}},
		Packs: metadata.PackMetadata{Manifests: []metadata.PackManifest{
			manifest("first", "first_path"), manifest("inactive", "ignored"), manifest("second", "second_path"),
		}},
	}
	if got, want := policyPaths(PackAdoptionPolicyData(root), "test_item", "projection_omit"), []string{"first_path", "second_path"}; len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("pack policy paths = %v, want %v", got, want)
	}
}

func TestLoadAdoptionPolicyValidatesUserBeforeMerge(t *testing.T) {
	path := filepath.Join(t.TempDir(), "policy.json")
	if err := os.WriteFile(path, []byte(`{"version":1,"resource_types":{"test_item":{"projection_omit":[{"path":"name","reason":"user","approved_by":"unit"}]}}}`), 0o600); err != nil {
		t.Fatalf("os.WriteFile: %v", err)
	}
	root := metadata.LoadedPackRoot{}
	policy, err := LoadAdoptionPolicy(root, &path)
	if err != nil {
		t.Fatalf("LoadAdoptionPolicy: %v", err)
	}
	if got := len(policy.Entries("test_item", metadata.PolicyProjectionOmit)); got != 1 {
		t.Fatalf("projection omit entries = %d, want 1", got)
	}

	if err := os.WriteFile(path, []byte(`{"version":2,"resource_types":{}}`), 0o600); err != nil {
		t.Fatalf("os.WriteFile invalid: %v", err)
	}
	if _, err := LoadAdoptionPolicy(root, &path); err == nil {
		t.Fatal("LoadAdoptionPolicy accepted unsupported user version")
	}
}
