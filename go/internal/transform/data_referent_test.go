package transform

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestTransformDataReferentUsesGeneratedKeyFallback(t *testing.T) {
	result, err := TransformDataReferentItems(DataReferentTransformOptions{
		NameField:    "name",
		RawItems:     []any{map[string]any{"id": json.Number("101"), "name": "東京"}},
		ResourceType: "sample_groups_data",
	})
	if err != nil {
		t.Fatalf("TransformDataReferentItems(%q) error = %v, want nil", "東京", err)
	}
	wantItems := map[string]TransformRecord{
		"id_101": {"name": "東京"},
	}
	wantOriginals := map[string]TransformRecord{
		"id_101": {"id": json.Number("101"), "name": "東京"},
	}
	if diff := cmp.Diff(wantItems, result.Items); diff != "" {
		t.Errorf("TransformDataReferentItems(%q).Items mismatch (-want +got):\n%s", "東京", diff)
	}
	if diff := cmp.Diff(wantOriginals, result.Originals); diff != "" {
		t.Errorf("TransformDataReferentItems(%q).Originals mismatch (-want +got):\n%s", "東京", diff)
	}
}

func TestTransformDataReferentRejectsInvalidNameAndID(t *testing.T) {
	tests := []struct {
		name string
		item map[string]any
	}{
		{name: "missing name", item: map[string]any{"id": json.Number("1")}},
		{name: "non-string name", item: map[string]any{"id": json.Number("1"), "name": 7}},
		{name: "whitespace name", item: map[string]any{"id": json.Number("1"), "name": " \t\n "}},
		{name: "missing id", item: map[string]any{"name": "Alpha"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := TransformDataReferentItems(DataReferentTransformOptions{
				NameField:    "name",
				RawItems:     []any{test.item},
				ResourceType: "sample_groups_data",
			})
			if err == nil {
				t.Errorf("TransformDataReferentItems(%s) error = nil, want a loud validation error", test.name)
			}
		})
	}
}

func TestTransformDataReferentRejectsDuplicateDerivedKeys(t *testing.T) {
	_, err := TransformDataReferentItems(DataReferentTransformOptions{
		NameField: "name",
		RawItems: []any{
			map[string]any{"id": json.Number("1"), "name": "A/B"},
			map[string]any{"id": json.Number("2"), "name": "A B"},
		},
		ResourceType: "sample_groups_data",
	})
	if err == nil {
		t.Fatal("TransformDataReferentItems(collision pair) error = nil, want duplicate-derived-key failure")
	}
	if !strings.Contains(err.Error(), "duplicate") || !strings.Contains(err.Error(), "a_b") {
		t.Errorf("TransformDataReferentItems(collision pair) error = %q, want a loud duplicate a_b failure", err)
	}
}

func TestTransformDataReferentRejectsDuplicateCanonicalIDs(t *testing.T) {
	tests := []struct {
		name     string
		firstID  any
		secondID any
		wantID   string
	}{
		{name: "same numeric ID", firstID: json.Number("101"), secondID: json.Number("101"), wantID: "101"},
		{name: "string and number canonicalize together", firstID: "101", secondID: json.Number("101"), wantID: "101"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := TransformDataReferentItems(DataReferentTransformOptions{
				NameField: "name",
				RawItems: []any{
					map[string]any{"id": test.firstID, "name": "Alpha"},
					map[string]any{"id": test.secondID, "name": "Beta"},
				},
				ResourceType: "sample_groups_data",
			})
			if err == nil {
				t.Fatalf("TransformDataReferentItems(%s) error = nil, want duplicate canonical-ID refusal", test.name)
			}
			for _, want := range []string{"alpha", "beta", test.wantID} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("TransformDataReferentItems(%s) error = %q, want it to name %s", test.name, err, want)
				}
			}
		})
	}
}
