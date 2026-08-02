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
		{name: "empty id", item: map[string]any{"id": "", "name": "Alpha"}},
		{name: "whitespace id", item: map[string]any{"id": " \t\n ", "name": "Alpha"}},
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

func TestTransformDataReferentRejectsCaseInsensitiveNames(t *testing.T) {
	_, err := TransformDataReferentItems(DataReferentTransformOptions{
		NameField: "name",
		RawItems: []any{
			map[string]any{"id": json.Number("1"), "name": "Location Group"},
			map[string]any{"id": json.Number("2"), "name": "location group"},
		},
		ResourceType: "sample_groups_data",
	})
	if err == nil {
		t.Fatal("TransformDataReferentItems(case-insensitive name collision) error = nil, want case-fold ambiguity refusal")
	}
	for _, want := range []string{"ambiguous", "location group", "sample_groups_data"} {
		if !strings.Contains(strings.ToLower(err.Error()), want) {
			t.Errorf("TransformDataReferentItems(case-insensitive name collision) error = %q, want it to contain %q", err, want)
		}
	}
}

func TestTransformDataReferentRejectsUnicodeCaseFoldAmbiguity(t *testing.T) {
	tests := []struct {
		name   string
		first  string
		second string
	}{
		{name: "same non-ASCII name with distinct IDs", first: "東京", second: "東京"},
		{name: "Latin case fold", first: "Å", second: "å"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := TransformDataReferentItems(DataReferentTransformOptions{
				NameField: "name",
				RawItems: []any{
					map[string]any{"id": json.Number("101"), "name": test.first},
					map[string]any{"id": json.Number("102"), "name": test.second},
				},
				ResourceType: "sample_groups_data",
			})
			if err == nil {
				t.Fatalf("TransformDataReferentItems(%s) error = nil, want Unicode case-fold ambiguity refusal", test.name)
			}
			if !strings.Contains(strings.ToLower(err.Error()), "unicode case folding") {
				t.Errorf("TransformDataReferentItems(%s) error = %q, want Unicode case-fold diagnostic", test.name, err)
			}
		})
	}
}

func TestTransformDataReferentAllowsDistinctEmptySlugNames(t *testing.T) {
	result, err := TransformDataReferentItems(DataReferentTransformOptions{
		NameField: "name",
		RawItems: []any{
			map[string]any{"id": json.Number("101"), "name": "東京"},
			map[string]any{"id": json.Number("102"), "name": "大阪"},
		},
		ResourceType: "sample_groups_data",
	})
	if err != nil {
		t.Fatalf("TransformDataReferentItems(distinct empty-slug names) error = %v, want nil", err)
	}
	for _, key := range []string{"id_101", "id_102"} {
		if _, ok := result.Items[key]; !ok {
			t.Errorf("TransformDataReferentItems(distinct empty-slug names) missing item key %q", key)
		}
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
