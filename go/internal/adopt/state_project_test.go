package adopt

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/dvmrry/infrawright-dev/go/internal/metadata"
)

func stateProjectSchema() metadata.JsonObject {
	return metadata.JsonObject{"block": metadata.JsonObject{
		"attributes": metadata.JsonObject{
			"computed_only":     metadata.JsonObject{"computed": true, "type": "string"},
			"description":       metadata.JsonObject{"optional": true, "type": "string"},
			"enabled":           metadata.JsonObject{"optional": true, "type": "bool"},
			"filled":            metadata.JsonObject{"optional": true, "type": "string"},
			"id":                metadata.JsonObject{"computed": true, "optional": true, "type": "string"},
			"name":              metadata.JsonObject{"required": true, "type": "string"},
			"number_value":      metadata.JsonObject{"optional": true, "type": "number"},
			"secret":            metadata.JsonObject{"optional": true, "sensitive": true, "type": "string"},
			"source_categories": metadata.JsonObject{"optional": true, "type": []any{"set", "string"}},
			"target_categories": metadata.JsonObject{"optional": true, "type": []any{"set", "string"}},
		},
		"block_types": metadata.JsonObject{
			"required_settings": metadata.JsonObject{
				"min_items": json.Number("1"), "nesting_mode": "single",
				"block": metadata.JsonObject{"attributes": metadata.JsonObject{
					"mode":            metadata.JsonObject{"required": true, "type": "string"},
					"computed_nested": metadata.JsonObject{"computed": true, "type": "string"},
				}},
			},
			"rules": metadata.JsonObject{
				"nesting_mode": "list", "block": metadata.JsonObject{"attributes": metadata.JsonObject{
					"name":          metadata.JsonObject{"required": true, "type": "string"},
					"order":         metadata.JsonObject{"optional": true, "type": "number"},
					"computed_rule": metadata.JsonObject{"computed": true, "type": "string"},
				}},
			},
		},
	}}
}

func stateProjectRoot(t *testing.T, override metadata.JsonObject) *metadata.LoadedPackRoot {
	t.Helper()
	root := testOracleRoot(t)
	resource := root.Resources[testResourceType]
	resource.Registry = metadata.JsonObject{"generate": true, "product": "test"}
	resource.Override = override
	root.Resources[testResourceType] = resource
	providerSchema := metadata.JsonObject{"resource_schemas": metadata.JsonObject{testResourceType: stateProjectSchema()}}
	encoded, err := json.Marshal(providerSchema)
	if err != nil {
		t.Fatalf("json.Marshal schema: %v", err)
	}
	directory := filepath.Join(root.Packs.Manifests[0].Directory, "schemas", "provider")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatalf("os.MkdirAll schema: %v", err)
	}
	if err := os.WriteFile(filepath.Join(directory, testProvider+".json"), encoded, 0o600); err != nil {
		t.Fatalf("os.WriteFile schema: %v", err)
	}
	return root
}

func stateProjectPolicy(t *testing.T, resource metadata.JsonObject) *metadata.DriftPolicy {
	t.Helper()
	policy, err := metadata.NewDriftPolicy(metadata.JsonObject{
		"version": float64(1), "resource_types": metadata.JsonObject{testResourceType: resource},
	}, "state project test")
	if err != nil {
		t.Fatalf("metadata.NewDriftPolicy: %v", err)
	}
	return policy
}

func TestProjectProviderStatePreservesInputsAndDropsComputedRecursively(t *testing.T) {
	output, err := ProjectProviderState(ProjectProviderStateOptions{
		ResourceType: testResourceType, Root: stateProjectRoot(t, nil),
		StateValues: map[string]any{
			"computed_only": "drop", "description": "", "enabled": false, "id": "provider-id",
			"name": "Example", "number_value": json.Number("9007199254740993"),
			"required_settings": []any{map[string]any{"computed_nested": "drop", "mode": "strict"}},
			"rules":             []any{map[string]any{"computed_rule": "drop", "name": "first", "order": json.Number("0")}},
		},
	})
	if err != nil {
		t.Fatalf("ProjectProviderState: %v", err)
	}
	want := map[string]any{
		"description": "", "enabled": false, "name": "Example", "number_value": json.Number("9007199254740993"),
		"required_settings": map[string]any{"mode": "strict"},
		"rules":             []any{map[string]any{"name": "first", "order": json.Number("0")}},
	}
	if !reflect.DeepEqual(output, want) {
		t.Fatalf("projected state = %#v, want %#v", output, want)
	}
}

func TestProjectProviderStatePolicyOrderAndPackDefaults(t *testing.T) {
	entry := func(fields metadata.JsonObject) metadata.JsonObject {
		output := metadata.JsonObject{"reason": "test", "approved_by": "unit"}
		for key, value := range fields {
			output[key] = value
		}
		return output
	}
	policy := stateProjectPolicy(t, metadata.JsonObject{
		"projection_sync": []any{entry(metadata.JsonObject{"target_path": "target_categories", "source_path": "source_categories"})},
		"projection_fill": []any{entry(metadata.JsonObject{"path": "filled", "source": "rawFilled"})},
		"projection_omit_if": []any{
			entry(metadata.JsonObject{"path": "filled", "values": []any{"DROP"}}),
			entry(metadata.JsonObject{"path": "source_categories[]", "values": []any{"DROP"}}),
			entry(metadata.JsonObject{"path": "number_value", "values": []any{false}}),
		},
	})
	override := metadata.JsonObject{"drop_if_default": metadata.JsonObject{"rules.order": json.Number("0")}}
	output, err := ProjectProviderState(ProjectProviderStateOptions{
		Policy: policy, RawItem: map[string]any{"rawFilled": "DROP"}, ResourceType: testResourceType,
		Root: stateProjectRoot(t, override), StateValues: map[string]any{
			"name": "Example", "number_value": json.Number("0"),
			"required_settings": map[string]any{"mode": "strict"},
			"rules":             []any{map[string]any{"name": "first", "order": json.Number("0")}, map[string]any{"name": "second", "order": json.Number("2")}},
			"source_categories": []any{"ONE", "DROP"}, "target_categories": []any{},
		},
	})
	if err != nil {
		t.Fatalf("ProjectProviderState policy order: %v", err)
	}
	want := map[string]any{
		"name": "Example", "number_value": json.Number("0"),
		"required_settings": map[string]any{"mode": "strict"},
		"rules":             []any{map[string]any{"name": "first"}, map[string]any{"name": "second", "order": json.Number("2")}},
		"source_categories": []any{"ONE"}, "target_categories": []any{"ONE", "DROP"},
	}
	if !reflect.DeepEqual(output, want) {
		t.Fatalf("projected policy state = %#v, want %#v", output, want)
	}
	stale := policy.StaleEntries(metadata.StaleEntriesOptions{})
	if len(stale) != 1 || stale[0].Path != "number_value" {
		t.Fatalf("stale entries = %#v, want only strict false-vs-zero omit", stale)
	}
}

func TestProjectProviderStateSensitiveAndRequiredFailures(t *testing.T) {
	if err := ValidateSensitiveMaskShape([]any{map[string]any{"secret": true}}, []any{map[string]any{"secret": "value"}}); err == nil {
		t.Fatal("ValidateSensitiveMaskShape accepted a root array")
	}
	_, err := ProjectProviderState(ProjectProviderStateOptions{
		ResourceType: testResourceType, Root: stateProjectRoot(t, nil),
		SensitiveValues: map[string]any{"secret": true}, StateValues: map[string]any{
			"name": "Example", "required_settings": map[string]any{"mode": "strict"}, "secret": "do-not-write",
		},
	})
	if err == nil || !strings.Contains(err.Error(), "sensitive input path secret") {
		t.Fatalf("sensitive projection error = %v", err)
	}
	_, err = ProjectProviderState(ProjectProviderStateOptions{
		ResourceType: testResourceType, Root: stateProjectRoot(t, nil), StateValues: map[string]any{"required_settings": map[string]any{"mode": "strict"}},
	})
	if err == nil || !strings.Contains(err.Error(), "required state path missing: name") {
		t.Fatalf("required projection error = %v", err)
	}
}

func TestProjectionSyncRejectsRepeatedBlockTraversal(t *testing.T) {
	policy := stateProjectPolicy(t, metadata.JsonObject{"projection_sync": []any{metadata.JsonObject{
		"target_path": "rules.name", "source_path": "required_settings.mode", "reason": "invalid", "approved_by": "unit",
	}}})
	_, err := ProjectProviderState(ProjectProviderStateOptions{
		Policy: policy, ResourceType: testResourceType, Root: stateProjectRoot(t, nil),
		StateValues: map[string]any{
			"name": "Example", "required_settings": map[string]any{"mode": "strict"}, "rules": []any{map[string]any{"name": "one"}},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "is a repeated block") {
		t.Fatalf("projection sync error = %v", err)
	}
}
