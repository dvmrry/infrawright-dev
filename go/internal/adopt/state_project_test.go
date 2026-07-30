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
			"computed_categories": metadata.JsonObject{"computed": true, "type": []any{"set", "string"}},
			"computed_secret":     metadata.JsonObject{"computed": true, "sensitive": true, "type": []any{"set", "string"}},
			"computed_only":       metadata.JsonObject{"computed": true, "type": "string"},
			"description":         metadata.JsonObject{"optional": true, "type": "string"},
			"enabled":             metadata.JsonObject{"optional": true, "type": "bool"},
			"filled":              metadata.JsonObject{"optional": true, "type": "string"},
			"id":                  metadata.JsonObject{"computed": true, "optional": true, "type": "string"},
			"name":                metadata.JsonObject{"required": true, "type": "string"},
			"number_value":        metadata.JsonObject{"optional": true, "type": "number"},
			"secret":              metadata.JsonObject{"optional": true, "sensitive": true, "type": "string"},
			"source_categories":   metadata.JsonObject{"optional": true, "type": []any{"set", "string"}},
			"target_categories":   metadata.JsonObject{"optional": true, "type": []any{"set", "string"}},
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

func projectionSyncPolicy(t *testing.T, target, source string) *metadata.DriftPolicy {
	t.Helper()
	return stateProjectPolicy(t, metadata.JsonObject{"projection_sync": []any{metadata.JsonObject{
		"target_path": target, "source_path": source, "reason": "test", "approved_by": "unit",
	}}})
}

// TestProjectionSyncRecoversComputedOnlySource is the feature: a writable set
// input recovered from a computed-only set attribute the provider reported.
//
// This shape is zia_url_categories_predefined exactly -- db_categorized_urls
// is computed there, the retain list is the writable input -- and it needed
// two changes, not the one a type-level reading suggests. Resolving the
// computed attribute's type fixes the "schema types differ" refusal, but the
// projection drops computed values before the sync runs, so the source read
// against the projected output stayed a silent no-op. The test therefore
// drives ProjectProviderState, not the type seam: it fails unless the
// fallback actually reads the provider state.
func TestProjectionSyncRecoversComputedOnlySource(t *testing.T) {
	output, err := ProjectProviderState(ProjectProviderStateOptions{
		Policy:       projectionSyncPolicy(t, "target_categories", "computed_categories"),
		ResourceType: testResourceType, Root: stateProjectRoot(t, nil),
		StateValues: map[string]any{
			"name":              "Example",
			"required_settings": map[string]any{"mode": "strict"},
			"computed_categories": []any{
				".puter.com", ".mendeley.com", ".a.com", ".b.com", ".c.com", ".d.com",
			},
		},
	})
	if err != nil {
		t.Fatalf("ProjectProviderState(computed source sync) error = %v, want recovery", err)
	}
	want := []any{".puter.com", ".mendeley.com", ".a.com", ".b.com", ".c.com", ".d.com"}
	if got := output["target_categories"]; !reflect.DeepEqual(got, want) {
		t.Errorf("target_categories = %#v, want all six members recovered from the computed source", got)
	}
	if _, present := output["computed_categories"]; present {
		t.Error("computed_categories leaked into the projected output; the sync must read it, not project it")
	}
}

// TestProjectionSyncComputedSourceRespectsExistingTarget pins the
// absent-or-empty guard on the widened path: a real value in the input is
// never clobbered by the computed source.
func TestProjectionSyncComputedSourceRespectsExistingTarget(t *testing.T) {
	output, err := ProjectProviderState(ProjectProviderStateOptions{
		Policy:       projectionSyncPolicy(t, "target_categories", "computed_categories"),
		ResourceType: testResourceType, Root: stateProjectRoot(t, nil),
		StateValues: map[string]any{
			"name":                "Example",
			"required_settings":   map[string]any{"mode": "strict"},
			"target_categories":   []any{"already.example"},
			"computed_categories": []any{"replacement.example"},
		},
	})
	if err != nil {
		t.Fatalf("ProjectProviderState(populated target) error = %v, want nil", err)
	}
	if got, want := output["target_categories"], []any{"already.example"}; !reflect.DeepEqual(got, want) {
		t.Errorf("target_categories = %#v, want the existing value %#v untouched", got, want)
	}
}

// TestProjectionSyncComputedSourceAbsentIsANoOp pins the quiet case: no
// computed value in state, nothing to recover, no error.
func TestProjectionSyncComputedSourceAbsentIsANoOp(t *testing.T) {
	output, err := ProjectProviderState(ProjectProviderStateOptions{
		Policy:       projectionSyncPolicy(t, "target_categories", "computed_categories"),
		ResourceType: testResourceType, Root: stateProjectRoot(t, nil),
		StateValues: map[string]any{
			"name": "Example", "required_settings": map[string]any{"mode": "strict"},
		},
	})
	if err != nil {
		t.Fatalf("ProjectProviderState(absent computed source) error = %v, want nil", err)
	}
	if _, present := output["target_categories"]; present {
		t.Errorf("target_categories = %#v, want absent when the source has no value", output["target_categories"])
	}
}

// TestProjectionSyncComputedSourceStillTypeChecked pins that widening type
// resolution did not widen type agreement: a computed string source against a
// set target is still refused as a type mismatch.
func TestProjectionSyncComputedSourceStillTypeChecked(t *testing.T) {
	_, err := ProjectProviderState(ProjectProviderStateOptions{
		Policy:       projectionSyncPolicy(t, "target_categories", "computed_only"),
		ResourceType: testResourceType, Root: stateProjectRoot(t, nil),
		StateValues: map[string]any{
			"name": "Example", "required_settings": map[string]any{"mode": "strict"},
			"computed_only": "not-a-set",
		},
	})
	if err == nil || !strings.Contains(err.Error(), "schema types differ") {
		t.Fatalf("ProjectProviderState(string source, set target) error = %v, want a type refusal", err)
	}
}

// TestProjectionSyncComputedTargetStillRefused pins the writability gate the
// widening must not touch: computed may be a source, never a target.
func TestProjectionSyncComputedTargetStillRefused(t *testing.T) {
	_, err := ProjectProviderState(ProjectProviderStateOptions{
		Policy:       projectionSyncPolicy(t, "computed_categories", "target_categories"),
		ResourceType: testResourceType, Root: stateProjectRoot(t, nil),
		StateValues: map[string]any{
			"name": "Example", "required_settings": map[string]any{"mode": "strict"},
			"target_categories": []any{"x"},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "not a writable input attribute") {
		t.Fatalf("ProjectProviderState(computed target) error = %v, want the writability refusal", err)
	}
}

// TestProjectionSyncComputedSourceSensitivityRefusals pins both sensitivity
// gates on the fallback. The projection refuses to write sensitive inputs to
// generated tfvars; a sync from a sensitive computed source is the same write
// through an alias, and it must refuse whether the sensitivity arrives in the
// runtime mask or only in the schema declaration.
func TestProjectionSyncComputedSourceSensitivityRefusals(t *testing.T) {
	t.Run("runtime_mask", func(t *testing.T) {
		_, err := ProjectProviderState(ProjectProviderStateOptions{
			Policy:       projectionSyncPolicy(t, "target_categories", "computed_categories"),
			ResourceType: testResourceType, Root: stateProjectRoot(t, nil),
			SensitiveValues: map[string]any{"computed_categories": []any{true}},
			StateValues: map[string]any{
				"name": "Example", "required_settings": map[string]any{"mode": "strict"},
				"computed_categories": []any{"secret.example"},
			},
		})
		if err == nil || !strings.Contains(err.Error(), "sensitive source") {
			t.Fatalf("ProjectProviderState(masked computed source) error = %v, want a sensitivity refusal", err)
		}
	})
	t.Run("schema_declared", func(t *testing.T) {
		_, err := ProjectProviderState(ProjectProviderStateOptions{
			Policy:       projectionSyncPolicy(t, "target_categories", "computed_secret"),
			ResourceType: testResourceType, Root: stateProjectRoot(t, nil),
			StateValues: map[string]any{
				"name": "Example", "required_settings": map[string]any{"mode": "strict"},
				"computed_secret": []any{"secret.example"},
			},
		})
		if err == nil || !strings.Contains(err.Error(), "sensitive source") {
			t.Fatalf("ProjectProviderState(schema-sensitive computed source) error = %v, want a sensitivity refusal", err)
		}
	})
}

// TestProjectionSyncOmittedInputSourceIsNotResurrected pins the exclusion
// that keeps the fallback from widening past its reason. An input source
// absent from the projected output is absent because the state lacked it or
// because policy omitted it; reading the state behind the projection's back
// would restore exactly what projection_omit removed.
func TestProjectionSyncOmittedInputSourceIsNotResurrected(t *testing.T) {
	policy := stateProjectPolicy(t, metadata.JsonObject{
		"projection_sync": []any{metadata.JsonObject{
			"target_path": "target_categories", "source_path": "source_categories",
			"reason": "test", "approved_by": "unit",
		}},
		"projection_omit": []any{metadata.JsonObject{
			"path": "source_categories", "reason": "test", "approved_by": "unit",
		}},
	})
	output, err := ProjectProviderState(ProjectProviderStateOptions{
		Policy: policy, ResourceType: testResourceType, Root: stateProjectRoot(t, nil),
		StateValues: map[string]any{
			"name": "Example", "required_settings": map[string]any{"mode": "strict"},
			"source_categories": []any{"omitted.example"},
		},
	})
	if err != nil {
		t.Fatalf("ProjectProviderState(omitted input source) error = %v, want nil", err)
	}
	if _, present := output["target_categories"]; present {
		t.Errorf("target_categories = %#v, want absent: an omitted input must stay omitted", output["target_categories"])
	}
}

// TestProjectionSyncComputedSourceListTraversalRefusedHonestly pins the guard
// widening: a path that traverses a set-typed computed attribute is refused
// with the message that names the actual problem, not a type mismatch.
func TestProjectionSyncComputedSourceListTraversalRefusedHonestly(t *testing.T) {
	_, err := ProjectProviderState(ProjectProviderStateOptions{
		Policy:       projectionSyncPolicy(t, "target_categories", "computed_categories.member"),
		ResourceType: testResourceType, Root: stateProjectRoot(t, nil),
		StateValues: map[string]any{
			"name": "Example", "required_settings": map[string]any{"mode": "strict"},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "not an object-shaped container") {
		t.Fatalf("ProjectProviderState(traversal through computed set) error = %v, want the container refusal", err)
	}
}
