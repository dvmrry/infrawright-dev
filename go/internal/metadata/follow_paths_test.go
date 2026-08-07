package metadata

import (
	"strings"
	"testing"
)

func followPathsRegistry(fetch JsonObject) JsonObject {
	return JsonObject{
		"sample_locations": JsonObject{"product": "sample", "generate": true, "fetch": fetch},
	}
}

func TestValidateRegistryAcceptsFollowPaths(t *testing.T) {
	if _, err := ValidateRegistry(followPathsRegistry(JsonObject{
		"pagination": "zia", "path": "locations",
		"follow_paths": []any{JsonObject{"path": "locations/{id}/sublocations", "from_field": "id"}},
	}), "registry.json"); err != nil {
		t.Fatalf("ValidateRegistry(follow_paths) error = %v, want accepted", err)
	}
}

func TestValidateRegistryFollowPathsRefusals(t *testing.T) {
	tests := []struct {
		name  string
		fetch JsonObject
		want  string
	}{
		{
			name: "combined_with_expand",
			fetch: JsonObject{
				"pagination": "zia", "path": "locations/{kind}",
				"expand":       JsonObject{"kind": []any{"a"}},
				"follow_paths": []any{JsonObject{"path": "l/{id}/s", "from_field": "id"}},
			},
			want: "cannot be combined with expand",
		},
		{
			name: "combined_with_merge_paths",
			fetch: JsonObject{
				"pagination": "single", "path": "locations",
				"merge_paths":  []any{"locations/advanced"},
				"follow_paths": []any{JsonObject{"path": "l/{id}/s", "from_field": "id"}},
			},
			want: "cannot be combined with merge_paths",
		},
		{
			name:  "empty_list",
			fetch: JsonObject{"pagination": "zia", "path": "locations", "follow_paths": []any{}},
			want:  "must be a non-empty list of follow entries",
		},
		{
			name: "unknown_key",
			fetch: JsonObject{
				"pagination": "zia", "path": "locations",
				"follow_paths": []any{JsonObject{"path": "l/{id}/s", "from_field": "id", "extra": true}},
			},
			want: "extra",
		},
		{
			name: "missing_placeholder",
			fetch: JsonObject{
				"pagination": "zia", "path": "locations",
				"follow_paths": []any{JsonObject{"path": "locations/sublocations", "from_field": "id"}},
			},
			want: `must contain the placeholder "{id}" exactly once`,
		},
		{
			name: "undeclared_braces",
			fetch: JsonObject{
				"pagination": "zia", "path": "locations",
				"follow_paths": []any{JsonObject{"path": "l/{id}/{other}", "from_field": "id"}},
			},
			want: "must not contain undeclared expansion braces",
		},
		{
			name: "unsafe_path",
			fetch: JsonObject{
				"pagination": "zia", "path": "locations",
				"follow_paths": []any{JsonObject{"path": "../{id}/s", "from_field": "id"}},
			},
			want: "must not contain raw or percent-encoded dot path segments",
		},
		{
			name: "duplicate_path",
			fetch: JsonObject{
				"pagination": "zia", "path": "locations",
				"follow_paths": []any{
					JsonObject{"path": "l/{id}/s", "from_field": "id"},
					JsonObject{"path": "l/{id}/s", "from_field": "id"},
				},
			},
			want: `duplicates path "l/{id}/s"`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := ValidateRegistry(followPathsRegistry(test.fetch), "registry.json")
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("ValidateRegistry(%s) error = %v, want substring %q", test.name, err, test.want)
			}
		})
	}
}
