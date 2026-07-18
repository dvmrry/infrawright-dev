package adopt

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dvmrry/infrawright-dev/go/internal/metadata"
)

func generatedPolicyRoot(t *testing.T, override metadata.JsonObject) *metadata.LoadedPackRoot {
	t.Helper()
	root := testOracleRoot(t)
	manifest := root.Packs.Manifests[0]
	schemaDirectory := filepath.Join(manifest.Directory, "schemas", "provider")
	if err := os.MkdirAll(schemaDirectory, 0o700); err != nil {
		t.Fatalf("os.MkdirAll(schema): %v", err)
	}
	schema := map[string]any{"resource_schemas": map[string]any{
		testResourceType: map[string]any{"block": map[string]any{"attributes": map[string]any{
			"name":           map[string]any{"type": "string", "optional": true},
			"description":    map[string]any{"type": "string", "optional": true},
			"filled":         map[string]any{"type": "string", "optional": true},
			"required_value": map[string]any{"type": "string", "required": true},
		}}},
	}}
	data, err := json.Marshal(schema)
	if err != nil {
		t.Fatalf("json.Marshal(schema): %v", err)
	}
	if err := os.WriteFile(filepath.Join(schemaDirectory, testProvider+".json"), data, 0o600); err != nil {
		t.Fatalf("os.WriteFile(schema): %v", err)
	}
	resource := root.Resources[testResourceType]
	resource.Override = override
	root.Resources[testResourceType] = resource
	return root
}

func testPolicy(t *testing.T, mode string, entries ...metadata.JsonObject) *metadata.DriftPolicy {
	t.Helper()
	raw := make([]any, len(entries))
	for index := range entries {
		raw[index] = entries[index]
	}
	policy, err := metadata.NewDriftPolicy(metadata.JsonObject{
		"version":        float64(1),
		"resource_types": metadata.JsonObject{testResourceType: metadata.JsonObject{mode: raw}},
	}, "generated policy test")
	if err != nil {
		t.Fatalf("metadata.NewDriftPolicy: %v", err)
	}
	return policy
}

func policyEntry(path string, extra metadata.JsonObject) metadata.JsonObject {
	entry := metadata.JsonObject{"path": path, "reason": "test", "approved_by": "unit"}
	for key, value := range extra {
		entry[key] = value
	}
	return entry
}

func TestGeneratedConfigProjectionOmitIsExactAndMarksEntry(t *testing.T) {
	policy := testPolicy(t, "projection_omit", policyEntry("description", nil))
	input := "resource \"test_item\" \"example\" {\n  name = \"fixture\"\n  description = \"drop me\"\n  required_value = \"keep\"\n}\n"
	result, err := ApplyGeneratedConfigPolicy(input, GeneratedConfigPolicyResource{
		AddressToKey: map[string]string{"test_item.example": "key"}, Policy: policy, ResourceType: testResourceType,
	}, generatedPolicyRoot(t, nil))
	if err != nil {
		t.Fatalf("ApplyGeneratedConfigPolicy: %v", err)
	}
	want := "resource \"test_item\" \"example\" {\n  name = \"fixture\"\n  required_value = \"keep\"\n}\n"
	if result.Edits != 1 || result.Text != want {
		t.Fatalf("result = %#v, want edits=1 text=%q", result, want)
	}
	if stale := policy.StaleEntries(metadata.StaleEntriesOptions{}); len(stale) != 0 {
		t.Fatalf("matched policy remained stale: %#v", stale)
	}
}

func TestGeneratedConfigPackDefaultAndOmitIfOrder(t *testing.T) {
	override := metadata.JsonObject{"drop_if_default": metadata.JsonObject{"name": "pack-default"}}
	policy := testPolicy(t, "projection_omit_if", policyEntry("name", metadata.JsonObject{"values": []any{"pack-default"}}))
	input := "resource \"test_item\" \"example\" {\n  name = \"pack-default\"\n  required_value = \"keep\"\n}\n"
	result, err := ApplyGeneratedConfigPolicy(input, GeneratedConfigPolicyResource{
		AddressToKey: map[string]string{"test_item.example": "key"}, Policy: policy, ResourceType: testResourceType,
	}, generatedPolicyRoot(t, override))
	if err != nil {
		t.Fatalf("ApplyGeneratedConfigPolicy: %v", err)
	}
	if result.Edits != 1 || strings.Contains(result.Text, "name =") {
		t.Fatalf("result = %#v, want pack default removed first", result)
	}
	if stale := policy.StaleEntries(metadata.StaleEntriesOptions{}); len(stale) != 1 {
		t.Fatalf("overlapping policy stale count = %d, want 1", len(stale))
	}
}

func TestGeneratedConfigProjectionFillThenOmitIf(t *testing.T) {
	policy, err := metadata.NewDriftPolicy(metadata.JsonObject{
		"version": float64(1),
		"resource_types": metadata.JsonObject{testResourceType: metadata.JsonObject{
			"projection_fill":    []any{policyEntry("filled", metadata.JsonObject{"source": "rawFilled"})},
			"projection_omit_if": []any{policyEntry("filled", metadata.JsonObject{"values": []any{"sentinel"}})},
		}},
	}, "fill then omit test")
	if err != nil {
		t.Fatalf("metadata.NewDriftPolicy: %v", err)
	}
	input := "resource \"test_item\" \"example\" {\n  required_value = \"keep\"\n}\n"
	result, err := ApplyGeneratedConfigPolicy(input, GeneratedConfigPolicyResource{
		AddressToKey: map[string]string{"test_item.example": "key"},
		Policy:       policy,
		RawItems:     map[string]map[string]any{"key": {"rawFilled": "sentinel"}},
		ResourceType: testResourceType,
	}, generatedPolicyRoot(t, nil))
	if err != nil {
		t.Fatalf("ApplyGeneratedConfigPolicy fill then omit: %v", err)
	}
	if result.Edits != 2 || result.Text != input {
		t.Fatalf("fill-then-omit result = %#v, want edits=2 and original text", result)
	}
	if stale := policy.StaleEntries(metadata.StaleEntriesOptions{}); len(stale) != 0 {
		t.Fatalf("fill-then-omit stale entries = %#v, want none", stale)
	}
}

func TestGeneratedConfigRejectsRequiredOmitAndUnknownSibling(t *testing.T) {
	policy := testPolicy(t, "projection_omit", policyEntry("required_value", nil))
	_, err := ApplyGeneratedConfigPolicy("resource \"test_item\" \"example\" {\n}\n", GeneratedConfigPolicyResource{
		AddressToKey: map[string]string{"test_item.example": "key"}, Policy: policy, ResourceType: testResourceType,
	}, generatedPolicyRoot(t, nil))
	if err == nil || !strings.Contains(err.Error(), "required path") {
		t.Fatalf("required omit error = %v", err)
	}

	_, err = ApplyGeneratedConfigPolicies(ApplyGeneratedConfigPoliciesOptions{
		GeneratedConfig: "resource \"test_item\" \"example\" {\n}\n\nresource \"unknown\" \"sibling\" {\n}\n",
		Resources: []GeneratedConfigPolicyResource{
			{AddressToKey: map[string]string{"test_item.example": "key"}, ResourceType: testResourceType},
			{AddressToKey: map[string]string{"test_other.sibling": "key"}, ResourceType: "test_other"},
		},
		Root: generatedPolicyRoot(t, nil),
	})
	if err == nil || !strings.Contains(err.Error(), "unexpected resource block unknown.sibling") {
		t.Fatalf("unknown sibling error = %v", err)
	}
}

func TestGeneratedConfigCompoundExpressionIsPreserved(t *testing.T) {
	policy := testPolicy(t, "projection_omit_if", policyEntry("description", metadata.JsonObject{"values": []any{"x"}}))
	input := "resource \"test_item\" \"example\" {\n  description = coalesce(\n    \"x\",\n    \"y\",\n  )\n  required_value = \"keep\"\n}\n"
	result, err := ApplyGeneratedConfigPolicy(input, GeneratedConfigPolicyResource{
		AddressToKey: map[string]string{"test_item.example": "key"}, Policy: policy, ResourceType: testResourceType,
	}, generatedPolicyRoot(t, nil))
	if err != nil {
		t.Fatalf("ApplyGeneratedConfigPolicy compound: %v", err)
	}
	if result.Edits != 0 || result.Text != input {
		t.Fatalf("compound expression changed: %#v", result)
	}
}
