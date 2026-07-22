package transform

// selection_test.go ports the original test corpus.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/dvmrry/infrawright-dev/go/internal/metadata"
)

type selectionTestPack struct {
	name     string
	manifest metadata.JsonObject
	registry metadata.JsonObject
}

func writeSelectionJSONFile(t *testing.T, path string, value any) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal %s: %v", path, err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// syntheticSelectionRoot ports the syntheticRoot helper from
// the original test corpus: writes pack.json/registry.json
// fixtures under a fresh temp directory (auto-cleaned via t.TempDir, the Go
// analogue of the Node test's mkdtemp + context.after(rm)) and loads them
// with no profile/catalog (matching loadPackRoot({ packsRoot: directory })).
func syntheticSelectionRoot(t *testing.T, packs []selectionTestPack) metadata.LoadedPackRoot {
	t.Helper()
	directory := t.TempDir()
	for _, pack := range packs {
		writeSelectionJSONFile(t, filepath.Join(directory, pack.name, "pack.json"), pack.manifest)
		writeSelectionJSONFile(t, filepath.Join(directory, pack.name, "registry.json"), pack.registry)
	}
	root, err := metadata.LoadPackRoot(metadata.LoadPackRootOptions{PacksRoot: directory})
	if err != nil {
		t.Fatalf("LoadPackRoot: %v", err)
	}
	return root
}

func TestSelectorExpansionIsReferentFirstAndOtherwiseAlphabetic(t *testing.T) {
	root := syntheticSelectionRoot(t, []selectionTestPack{{
		name: "sample",
		manifest: metadata.JsonObject{
			"provider_prefixes": metadata.JsonObject{"sample_": "sample"},
			"references": metadata.JsonObject{
				"sample_a_referrer": metadata.JsonObject{
					"referent_id": metadata.JsonObject{"name_field": "name", "referent": "sample_b_referent"},
				},
			},
		},
		registry: metadata.JsonObject{
			"sample_a_referrer":   metadata.JsonObject{"generate": true, "product": "sample"},
			"sample_aa_unrelated": metadata.JsonObject{"generate": true, "product": "sample"},
			"sample_b_referent":   metadata.JsonObject{"generate": true, "product": "sample"},
			"sample_data_only":    metadata.JsonObject{"product": "sample"},
		},
	}})

	result, err := SelectTransformResources(root, []string{"sample"})
	if err != nil {
		t.Fatalf("SelectTransformResources: %v", err)
	}
	want := TransformSelection{
		ResourceTypes: []string{"sample_aa_unrelated", "sample_b_referent", "sample_a_referrer"},
		Notes:         []string{},
	}
	if !reflect.DeepEqual(result, want) {
		t.Fatalf("SelectTransformResources = %+v, want %+v", result, want)
	}
}

func TestDuplicateInputsCollapseAndReferencesOutsideSelectionAreIgnored(t *testing.T) {
	root := syntheticSelectionRoot(t, []selectionTestPack{{
		name: "sample",
		manifest: metadata.JsonObject{
			"provider_prefixes": metadata.JsonObject{"sample_": "sample"},
			"references": metadata.JsonObject{
				"sample_a": metadata.JsonObject{"target": metadata.JsonObject{"name_field": "name", "referent": "sample_b"}},
			},
		},
		registry: metadata.JsonObject{
			"sample_a": metadata.JsonObject{"generate": true, "product": "sample"},
			"sample_b": metadata.JsonObject{"generate": true, "product": "sample"},
		},
	}})

	result, err := ReferenceOrder(root, []string{"sample_a", "sample_a"})
	if err != nil {
		t.Fatalf("ReferenceOrder: %v", err)
	}
	want := TransformSelection{ResourceTypes: []string{"sample_a"}, Notes: []string{}}
	if !reflect.DeepEqual(result, want) {
		t.Fatalf("ReferenceOrder = %+v, want %+v", result, want)
	}
}

func unvalidatedSelectionRoot(references metadata.JsonObject) metadata.LoadedPackRoot {
	return metadata.LoadedPackRoot{
		Active: metadata.PackSelection{Packs: []string{"sample"}},
		Packs: metadata.PackMetadata{Manifests: []metadata.PackManifest{{
			Name: "sample",
			Data: metadata.JsonObject{"references": references},
		}}},
	}
}

func TestReferenceOrderFailsClosedForUnvalidatedSelfReference(t *testing.T) {
	root := unvalidatedSelectionRoot(metadata.JsonObject{
		"sample_self": metadata.JsonObject{
			"parent_id": metadata.JsonObject{"name_field": "name", "referent": "sample_self"},
		},
	})

	result, err := ReferenceOrder(root, []string{"sample_self"})
	wantError := "reference order cycle detected; resolve one direction via a literal ID or operator expression"
	if err == nil || err.Error() != wantError {
		t.Errorf("ReferenceOrder(unvalidated self-reference) error = %v, want %q", err, wantError)
	}
	if !reflect.DeepEqual(result, TransformSelection{}) {
		t.Errorf("ReferenceOrder(unvalidated self-reference) result = %+v, want no partial result", result)
	}
}

func TestReferenceOrderFailsClosedForUnvalidatedMutualReferenceAfterReadyNode(t *testing.T) {
	root := unvalidatedSelectionRoot(metadata.JsonObject{
		"sample_cycle_a": metadata.JsonObject{
			"other_id": metadata.JsonObject{"name_field": "name", "referent": "sample_cycle_b"},
		},
		"sample_cycle_b": metadata.JsonObject{
			"other_id": metadata.JsonObject{"name_field": "name", "referent": "sample_cycle_a"},
		},
	})

	result, err := ReferenceOrder(root, []string{"sample_ready", "sample_cycle_b", "sample_cycle_a"})
	wantError := "reference order cycle detected; resolve one direction via a literal ID or operator expression"
	if err == nil || err.Error() != wantError {
		t.Errorf("ReferenceOrder(unvalidated mutual reference) error = %v, want %q", err, wantError)
	}
	if !reflect.DeepEqual(result, TransformSelection{}) {
		t.Errorf("ReferenceOrder(unvalidated mutual reference) result = %+v, want no partial result", result)
	}
}

func TestActivePackReferenceTablesMergeWithPythonsLaterFieldOverwrite(t *testing.T) {
	root := syntheticSelectionRoot(t, []selectionTestPack{
		{
			name: "alpha",
			manifest: metadata.JsonObject{
				"provider_prefixes": metadata.JsonObject{"sample_": "sample"},
				"references": metadata.JsonObject{
					"sample_a_referrer": metadata.JsonObject{"target": metadata.JsonObject{"name_field": "name", "referent": "sample_z_referent"}},
				},
			},
			registry: metadata.JsonObject{
				"sample_a_referrer": metadata.JsonObject{"generate": true, "product": "sample"},
				"sample_z_referent": metadata.JsonObject{"generate": true, "product": "sample"},
			},
		},
		{
			name: "beta",
			manifest: metadata.JsonObject{
				"provider_prefixes": metadata.JsonObject{"other_": "other"},
				"references": metadata.JsonObject{
					"sample_a_referrer": metadata.JsonObject{"target": metadata.JsonObject{"name_field": "name", "referent": "other_z_referent"}},
				},
			},
			registry: metadata.JsonObject{
				"other_z_referent": metadata.JsonObject{"generate": true, "product": "other"},
			},
		},
	})

	result, err := ReferenceOrder(root, []string{"sample_z_referent", "sample_a_referrer", "other_z_referent"})
	if err != nil {
		t.Fatalf("ReferenceOrder: %v", err)
	}
	want := TransformSelection{
		ResourceTypes: []string{"other_z_referent", "sample_a_referrer", "sample_z_referent"},
		Notes:         []string{},
	}
	if !reflect.DeepEqual(result, want) {
		t.Fatalf("ReferenceOrder = %+v, want %+v", result, want)
	}
}

func TestEffectiveReferenceMergeShadowsEarlierFieldBeforeCycleValidation(t *testing.T) {
	root := syntheticSelectionRoot(t, []selectionTestPack{
		{
			name: "alpha",
			manifest: metadata.JsonObject{
				"provider_prefixes": metadata.JsonObject{"sample_": "sample"},
				"references": metadata.JsonObject{
					"sample_a": metadata.JsonObject{
						"target": metadata.JsonObject{"name_field": "name", "referent": "sample_b"},
					},
				},
			},
			registry: metadata.JsonObject{
				"sample_a": metadata.JsonObject{"generate": true, "product": "sample"},
				"sample_b": metadata.JsonObject{"generate": true, "product": "sample"},
				"sample_c": metadata.JsonObject{"generate": true, "product": "sample"},
			},
		},
		{
			name: "beta",
			manifest: metadata.JsonObject{
				"references": metadata.JsonObject{
					"sample_a": metadata.JsonObject{
						"target": metadata.JsonObject{"name_field": "name", "referent": "sample_c"},
					},
				},
			},
			registry: metadata.JsonObject{},
		},
		{
			name: "gamma",
			manifest: metadata.JsonObject{
				"references": metadata.JsonObject{
					"sample_b": metadata.JsonObject{
						"back": metadata.JsonObject{"name_field": "name", "referent": "sample_a"},
					},
				},
			},
			registry: metadata.JsonObject{},
		},
	})

	result, err := ReferenceOrder(root, []string{"sample_a", "sample_b", "sample_c"})
	if err != nil {
		t.Fatalf("ReferenceOrder([sample_a sample_b sample_c]): %v", err)
	}
	want := TransformSelection{
		ResourceTypes: []string{"sample_c", "sample_a", "sample_b"},
		Notes:         []string{},
	}
	if !reflect.DeepEqual(result, want) {
		t.Errorf("ReferenceOrder([sample_a sample_b sample_c]) = %+v, want %+v", result, want)
	}
}

func TestDerivedResourcesResolveSourcePullWhileNormalResourcesResolveThemselves(t *testing.T) {
	root := syntheticSelectionRoot(t, []selectionTestPack{{
		name:     "sample",
		manifest: metadata.JsonObject{"provider_prefixes": metadata.JsonObject{"sample_": "sample"}},
		registry: metadata.JsonObject{
			"sample_source": metadata.JsonObject{"generate": true, "product": "sample"},
			"sample_derived": metadata.JsonObject{
				"derive":   metadata.JsonObject{"from": "sample_source", "policy_type": "ACCESS_POLICY"},
				"generate": true,
				"product":  "sample",
			},
			"sample_data_only": metadata.JsonObject{"product": "sample"},
		},
	}})

	if source, err := TransformSourceType(root, "sample_source"); err != nil || source != "sample_source" {
		t.Fatalf("TransformSourceType(sample_source) = (%q, %v), want (sample_source, nil)", source, err)
	}
	if source, err := TransformSourceType(root, "sample_derived"); err != nil || source != "sample_source" {
		t.Fatalf("TransformSourceType(sample_derived) = (%q, %v), want (sample_source, nil)", source, err)
	}
	if _, err := TransformSourceType(root, "sample_data_only"); err == nil {
		t.Fatalf("TransformSourceType(sample_data_only) = nil error, want an 'unknown or non-generated' error")
	}
	if _, err := TransformSourceType(root, "sample_missing"); err == nil {
		t.Fatalf("TransformSourceType(sample_missing) = nil error, want an 'unknown or non-generated' error")
	}
}
