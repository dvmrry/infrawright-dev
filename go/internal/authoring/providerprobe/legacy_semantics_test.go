package providerprobe

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestLegacyRecipeFirstFailureOrder mirrors the phased validation order in
// node-src/authoring/provider-probe.ts: root scalars, section shapes, then
// each nested section and its semantic requirements. Each case is deliberately
// cross-invalid so an accidental reordering changes the observed error.
func TestLegacyRecipeFirstFailureOrder(t *testing.T) {
	valid := func() map[string]any {
		return map[string]any{
			"name":             "example",
			"provider_source":  "registry.test/example",
			"provider_version": "1.2.3",
			"resource_prefix":  "example",
			"api_prefix":       "/api/",
			"openapi":          map[string]any{"path": "openapi.json"},
			"source":           map[string]any{"path": "provider"},
		}
	}
	cases := []struct {
		name  string
		value map[string]any
		want  string
	}{
		{
			name: "root scalars precede section shapes",
			value: func() map[string]any {
				v := valid()
				v["name"] = []any{}
				v["openapi"] = []any{}
				return v
			}(),
			want: "recipe name must be a string",
		},
		{
			name: "openapi shape precedes source shape",
			value: func() map[string]any {
				v := valid()
				v["openapi"] = []any{}
				v["source"] = false
				return v
			}(),
			want: "recipe openapi must be an object",
		},
		{
			name: "source shape precedes later section shapes",
			value: func() map[string]any {
				v := valid()
				v["source"] = false
				v["terraform_schema"] = []any{}
				return v
			}(),
			want: "recipe source must be an object",
		},
		{
			name: "all section shapes precede openapi fields",
			value: func() map[string]any {
				v := valid()
				v["openapi"] = map[string]any{"path": []any{}}
				v["tools"] = false
				return v
			}(),
			want: "recipe tools must be an object",
		},
		{
			name: "openapi fields precede openapi required check",
			value: func() map[string]any {
				v := valid()
				v["openapi"] = map[string]any{"path": []any{}}
				v["source"] = map[string]any{}
				return v
			}(),
			want: "recipe openapi.path must be a string",
		},
		{
			name: "openapi required check precedes source fields",
			value: func() map[string]any {
				v := valid()
				v["openapi"] = map[string]any{}
				v["source"] = map[string]any{"path": []any{}}
				return v
			}(),
			want: "recipe openapi must include path or url",
		},
		{
			name: "source fields precede source required check",
			value: func() map[string]any {
				v := valid()
				v["source"] = map[string]any{"path": []any{}}
				return v
			}(),
			want: "recipe source.path must be a string",
		},
		{
			name: "source required check precedes source ref check",
			value: func() map[string]any {
				v := valid()
				v["source"] = map[string]any{}
				return v
			}(),
			want: "recipe source must include path or git",
		},
		{
			name: "source ref check precedes schema field",
			value: func() map[string]any {
				v := valid()
				v["source"] = map[string]any{"git": "repository"}
				v["terraform_schema"] = map[string]any{"path": []any{}}
				return v
			}(),
			want: "recipe source.ref is required when source.git is used",
		},
		{
			name: "schema field precedes terraform provider field",
			value: func() map[string]any {
				v := valid()
				v["terraform_schema"] = map[string]any{"path": []any{}}
				v["terraform_provider"] = map[string]any{"source": []any{}}
				return v
			}(),
			want: "recipe terraform_schema.path must be a string",
		},
		{
			name: "terraform provider fields precede tools field",
			value: func() map[string]any {
				v := valid()
				v["terraform_provider"] = map[string]any{"source": []any{}}
				v["tools"] = map[string]any{"terraform": []any{}}
				return v
			}(),
			want: "recipe terraform_provider.source must be a string",
		},
		{
			name: "tools field precedes schema fallback provider requirement",
			value: func() map[string]any {
				v := valid()
				delete(v, "provider_source")
				v["tools"] = map[string]any{"terraform": []any{}}
				return v
			}(),
			want: "recipe tools.terraform must be a string",
		},
		{
			name: "provider requirement precedes provider version requirement",
			value: func() map[string]any {
				v := valid()
				delete(v, "provider_source")
				delete(v, "provider_version")
				return v
			}(),
			want: "recipe provider_source is required when terraform_schema.path is omitted",
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			err := loadLegacyRecipeValue(t, test.value)
			if err == nil || err.Error() != test.want {
				t.Fatalf("loadRecipe(%s) error = %v, want %q", test.name, err, test.want)
			}
		})
	}
}

func TestRecipeModeSelectionKeepsQualifiedCategorical(t *testing.T) {
	qualified := map[string]any{
		"source_provenance": map[string]any{
			"manifest": "manifest.json", "provider_root": "provider", "schema_root": "schema",
		},
	}
	path := filepath.Join(t.TempDir(), "qualified.json")
	writeJSONTestFile(t, path, qualified)
	loaded, err := loadRecipe(path)
	if err != nil {
		t.Fatalf("loadRecipe(qualified categorical recipe) error = %v", err)
	}
	if loaded.mode != QualifiedV2 {
		t.Errorf("loadRecipe(qualified categorical recipe).mode = %q, want %q", loaded.mode, QualifiedV2)
	}

	qualified["source_provenance"] = nil
	path = filepath.Join(t.TempDir(), "null-provenance.json")
	writeJSONTestFile(t, path, qualified)
	if _, err := loadRecipe(path); err == nil || err.Error() != "recipe openapi must include path or url" {
		t.Fatalf("loadRecipe(null source_provenance) error = %v, want legacy openapi requirement", err)
	}
}

func TestLegacySummaryAPIPrefixUsesNullishFallback(t *testing.T) {
	openAPI := map[string]any{
		"summary":                 map[string]any{},
		"registry_read_coverage":  map[string]any{"summary": map[string]any{}},
		"registry_fetch_coverage": map[string]any{"summary": map[string]any{}},
	}
	source := map[string]any{"summary": map[string]any{}}
	for _, test := range []struct {
		name string
		api  *string
		want string
	}{
		{name: "missing or null", api: nil, want: "/api/"},
		{name: "explicit empty", api: stringPointer(""), want: ""},
		{name: "explicit value", api: stringPointer("/v2/"), want: "/v2/"},
	} {
		t.Run(test.name, func(t *testing.T) {
			summary, err := buildLegacySummary(loadedRecipe{api: test.api}, source, openAPI, map[string]any{})
			if err != nil {
				t.Fatalf("buildLegacySummary(%q) error = %v", test.name, err)
			}
			provider, ok := summary["provider"].(map[string]any)
			if !ok {
				t.Fatalf("buildLegacySummary(%q) provider = %#v, want object", test.name, summary["provider"])
			}
			if got := provider["api_prefix"]; got != test.want {
				t.Errorf("buildLegacySummary(%q) api_prefix = %#v, want %q", test.name, got, test.want)
			}
		})
	}
}

func TestLegacyEmptyAPIPrefixMatchesNodeArtifactBytes(t *testing.T) {
	root := t.TempDir()
	writeLegacyFixture(t, root)
	recipePath := filepath.Join(root, "empty-api-prefix.json")
	writeJSONTestFile(t, recipePath, map[string]any{
		"api_prefix":       "",
		"name":             "example",
		"openapi":          map[string]any{"format": "json", "path": "openapi.json"},
		"provider_source":  "registry.terraform.io/example/example",
		"provider_version": "1.2.3",
		"resource_prefix":  "example",
		"source":           map[string]any{"path": "provider"},
		"terraform_schema": map[string]any{"path": "schema.json"},
	})
	recipe, err := loadRecipe(recipePath)
	if err != nil {
		t.Fatal(err)
	}
	result, err := runLegacy(context.Background(), recipe, RunOptions{WorkDirectory: filepath.Join(root, "empty-api-work")})
	if err != nil {
		t.Fatal(err)
	}
	// These are SHA-256 digests of the five byte strings emitted by the current
	// frozen Node v1 implementation for this local fixture after replacing its
	// ephemeral fixture root with <fixture-root>. They are intentionally not a
	// new authority fixture: the frozen CPython authority remains unchanged.
	want := map[string]string{
		"openapi-map.json":        "0aa278ea53992d9df98e7692191cd000b8e09b7c0b762d3de5c248e9dd75aa2b",
		"source-diagnostics.json": "a29c5eb777bcfa4a557bd4a6e0cd45add5bd3b09b1fef396b54931b024c47788",
		"source-registry.json":    "eedf8bd013be74e18dd4483ad1fc29bc127c506148601114cec1bf14798d8281",
		"summary.json":            "fb376c89b0a22bf793c3e753a3baab7ad8725bb968a8b2464ab5c300b01c751a",
		"summary.md":              "8d97825706ab8e5aacdde16d5d956dd4847e470e5622b8c6ddb097cbd8d5094c",
	}
	if got := result.Artifacts(); len(got) != len(want) {
		t.Fatalf("legacy empty-api artifact count = %d, want %d", len(got), len(want))
	} else {
		for _, artifact := range got {
			normalized := strings.ReplaceAll(string(artifact.Bytes), root, "<fixture-root>")
			sum := sha256.Sum256([]byte(normalized))
			if actual := hex.EncodeToString(sum[:]); actual != want[artifact.Name] {
				t.Errorf("empty api_prefix %s SHA-256 = %s, want %s", artifact.Name, actual, want[artifact.Name])
			}
		}
	}
	for _, artifact := range result.Artifacts() {
		if artifact.Name != "summary.json" {
			continue
		}
		if !strings.Contains(string(artifact.Bytes), `"api_prefix": ""`) {
			t.Fatalf("summary.json did not preserve empty api_prefix: %s", artifact.Bytes)
		}
	}
	if _, err := os.Stat(filepath.Join(result.WorkDirectory(), "artifacts")); !os.IsNotExist(err) {
		t.Fatalf("legacy runner created public artifact directory: %v", err)
	}
}
