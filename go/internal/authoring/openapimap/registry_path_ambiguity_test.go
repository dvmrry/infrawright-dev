package openapimap

import (
	"encoding/json"
	"testing"

	"github.com/dvmrry/infrawright-dev/go/internal/authoring/openapiadapter"
	"github.com/google/go-cmp/cmp"
)

func TestMatchRegistryPathAmbiguityUsesEqualBestDistinctEndpoints(t *testing.T) {
	t.Parallel()
	view := openapiadapter.LegacyMap{Paths: []openapiadapter.LegacyPath{
		{Template: "/api/v1/b/widgets/{id}", Methods: []string{"get"}},
		{Template: "/api/v1/a/widgets/{id}", Methods: []string{"get"}},
	}}
	want := Object{
		"candidates": []any{
			Object{"match": "suffix", "openapi_path": "/api/v1/a/widgets/{id}", "variant": "exact"},
			Object{"match": "suffix", "openapi_path": "/api/v1/b/widgets/{id}", "variant": "exact"},
		},
		"reason": "multiple_equal_rank_matches",
		"status": "ambiguous",
	}
	got := matchRegistryPath(view, "/api/", "/widgets", "example")
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("matchRegistryPath(%q, %q, %q) mismatch (-want +got):\n%s", "/api/", "/widgets", "example", diff)
	}
}

func TestMatchRegistryPathPrefersExactOverSuffix(t *testing.T) {
	t.Parallel()
	view := openapiadapter.LegacyMap{Paths: []openapiadapter.LegacyPath{
		{Template: "/api/v1/widgets/{id}", Methods: []string{"get"}},
		{Template: "/api/widgets/{id}", Methods: []string{"get"}},
	}}
	want := Object{"match": "exact", "openapi_path": "/api/widgets/{id}", "variant": "exact"}
	got := matchRegistryPath(view, "/api/", "/widgets/{id}", "example")
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("matchRegistryPath(%q, %q, %q) mismatch (-want +got):\n%s", "/api/", "/widgets/{id}", "example", diff)
	}
}

func TestMatchRegistryPathDeduplicatesVariantsForOneEndpoint(t *testing.T) {
	t.Parallel()
	view := openapiadapter.LegacyMap{Paths: []openapiadapter.LegacyPath{
		{Template: "/example/widgets/{id}", Methods: []string{"get"}},
	}}
	want := Object{"match": "exact", "openapi_path": "/example/widgets/{id}", "variant": "exact"}
	got := matchRegistryPath(view, "", "/example/widgets/{id}", "example")
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("matchRegistryPath(%q, %q, %q) mismatch (-want +got):\n%s", "", "/example/widgets/{id}", "example", diff)
	}
}

func TestMatchRegistryPathAmbiguityIsOrderingIndependent(t *testing.T) {
	t.Parallel()
	paths := []openapiadapter.LegacyPath{
		{Template: "/api/v1/a/widgets/{id}", Methods: []string{"get"}},
		{Template: "/api/v1/b/widgets/{id}", Methods: []string{"get"}},
	}
	left := matchRegistryPath(openapiadapter.LegacyMap{Paths: paths}, "/api/", "/widgets", "example")
	right := matchRegistryPath(openapiadapter.LegacyMap{Paths: []openapiadapter.LegacyPath{paths[1], paths[0]}}, "/api/", "/widgets", "example")
	if diff := cmp.Diff(left, right); diff != "" {
		t.Errorf("matchRegistryPath ordering mismatch (-left +right):\n%s", diff)
	}
}

func TestRegistryCoverageAmbiguousPathKeepsCountsAndSurfaceMapInSync(t *testing.T) {
	t.Parallel()
	view := openapiadapter.LegacyMap{Paths: []openapiadapter.LegacyPath{
		{Template: "/api/v1/b/widgets/{id}", Methods: []string{"get"}},
		{Template: "/api/v1/a/widgets/{id}", Methods: []string{"get"}},
	}}
	registry := Object{
		"example_widget": Object{
			"product": "example",
			"status":  "mapped",
			"read":    Object{"path": "/widgets"},
		},
	}
	coverage := registryCoverage(view, "/api/", "example", registry, "read")
	wantCoverage := Object{
		"resources": []any{
			Object{
				"candidates": []any{
					Object{"match": "suffix", "openapi_path": "/api/v1/a/widgets/{id}", "variant": "exact"},
					Object{"match": "suffix", "openapi_path": "/api/v1/b/widgets/{id}", "variant": "exact"},
				},
				"read_path": "/widgets",
				"reason":    "multiple_equal_rank_matches",
				"resource":  "example_widget",
				"status":    "ambiguous",
			},
		},
		"summary": Object{
			"ambiguous":      1,
			"coverage_ratio": json.Number("0.0"),
			"matched":        0,
			"read_resources": 1,
			"unmatched":      0,
		},
		"warnings": []any{Object{
			"code":      "registry_read_paths_ambiguous",
			"message":   "At least one registry read path matched multiple equal-ranked OpenAPI GET endpoints; inspect candidate evidence before treating it as covered.",
			"resources": []any{"example_widget"},
		}},
	}
	if diff := cmp.Diff(wantCoverage, coverage); diff != "" {
		t.Errorf("registryCoverage(%q, %q) mismatch (-want +got):\n%s", "example", "read", diff)
	}

	surface := surfaceMap(nil, "example", nil, Object{"resources": []any{}}, coverage, nil)
	wantSurface := Object{
		"diagnostics": []any{Object{
			"code":    "registry_read_paths_ambiguous",
			"message": "At least one registry read path matched multiple equal-ranked OpenAPI GET endpoints; inspect candidate evidence before treating it as covered.",
			"source":  "source_read_registry",
		}},
		"records": []any{
			Object{
				"adapter_required": false,
				"ambiguity_reason": "multiple_equal_rank_matches",
				"api_surface":      "example",
				"confidence":       nil,
				"evidence": []any{Object{
					"candidates": []any{
						Object{"match": "suffix", "openapi_path": "/api/v1/a/widgets/{id}", "variant": "exact"},
						Object{"match": "suffix", "openapi_path": "/api/v1/b/widgets/{id}", "variant": "exact"},
					},
					"kind":         "source_read_registry",
					"match":        nil,
					"openapi_path": nil,
					"operation_id": nil,
					"path_kind":    nil,
					"read_path":    "/widgets",
					"reason":       "multiple_equal_rank_matches",
					"variant":      nil,
				}},
				"match_status":   "ambiguous",
				"provider":       nil,
				"read_operation": nil,
				"read_path":      nil,
				"resource_type":  "example_widget",
				"source":         "source_read_registry",
			},
		},
		"schema_version": 1,
		"summary": Object{
			"by_source": Object{"source_read_registry": Object{"ambiguous": 1}},
			"by_status": Object{"ambiguous": 1},
			"records":   1,
		},
	}
	if diff := cmp.Diff(wantSurface, surface); diff != "" {
		t.Errorf("surfaceMap ambiguous registry record mismatch (-want +got):\n%s", diff)
	}
}
