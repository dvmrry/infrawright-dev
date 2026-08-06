package metadata

import (
	"strings"
	"testing"
)

func mergePathsRegistry(fetch JsonObject) JsonObject {
	return JsonObject{
		"sample_settings": JsonObject{"product": "sample", "generate": true, "fetch": fetch},
	}
}

func TestValidateRegistryAcceptsMergePathsSingleton(t *testing.T) {
	if _, err := ValidateRegistry(mergePathsRegistry(JsonObject{
		"pagination": "single", "path": "security", "merge_paths": []any{"security/advanced"},
	}), "registry.json"); err != nil {
		t.Fatalf("ValidateRegistry(merge_paths) error = %v, want accepted", err)
	}
}

func TestValidateRegistryMergePathsRefusals(t *testing.T) {
	tests := []struct {
		name  string
		fetch JsonObject
		want  string
	}{
		{
			name:  "non_single_pagination",
			fetch: JsonObject{"pagination": "zia", "path": "security", "merge_paths": []any{"security/advanced"}},
			want:  `requires pagination "single"`,
		},
		{
			name: "combined_with_expand",
			fetch: JsonObject{
				"pagination": "single", "path": "security/{kind}",
				"expand": JsonObject{"kind": []any{"a"}}, "merge_paths": []any{"security/advanced"},
			},
			want: "cannot be combined with expand",
		},
		{
			name:  "empty_list",
			fetch: JsonObject{"pagination": "single", "path": "security", "merge_paths": []any{}},
			want:  "must be a non-empty list of paths",
		},
		{
			name:  "non_string_entry",
			fetch: JsonObject{"pagination": "single", "path": "security", "merge_paths": []any{7}},
			want:  "merge_paths[0] must be a non-empty string",
		},
		{
			name:  "duplicate_of_base_path",
			fetch: JsonObject{"pagination": "single", "path": "security", "merge_paths": []any{"security"}},
			want:  `merge_paths[0] duplicates path "security"`,
		},
		{
			name: "duplicate_merge_entry",
			fetch: JsonObject{
				"pagination": "single", "path": "security",
				"merge_paths": []any{"security/advanced", "security/advanced"},
			},
			want: `merge_paths[1] duplicates path "security/advanced"`,
		},
		{
			name:  "unsafe_path",
			fetch: JsonObject{"pagination": "single", "path": "security", "merge_paths": []any{"../secrets"}},
			want:  "must not contain raw or percent-encoded dot path segments",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := ValidateRegistry(mergePathsRegistry(test.fetch), "registry.json")
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("ValidateRegistry(%s) error = %v, want substring %q", test.name, err, test.want)
			}
		})
	}
}
