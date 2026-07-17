package roots

// roots_test.go ports node-tests/roots.test.ts's six test cases verbatim,
// against the same fixture RootCatalog literal that Node test builds
// in-line, exercised through RootTopologyFromCatalog (the Go analogue of
// the Node test's rootTopology import).

import (
	"reflect"
	"strings"
	"testing"

	"github.com/dvmrry/infrawright-dev/go/internal/deployment"
	"github.com/dvmrry/infrawright-dev/go/internal/metadata"
)

func strPtr(s string) *string { return &s }
func boolPtr(b bool) *bool    { return &b }

func fixtureCatalog() metadata.RootCatalog {
	return metadata.RootCatalog{
		Kind:              "infrawright.root_catalog",
		SchemaVersion:     1,
		DeclaredProviders: []string{"zpa"},
		Resources: []metadata.RootCatalogResource{
			{
				Type: "zpa_alpha_one", Product: "zpa", Provider: "zpa",
				BareName: "alpha_one", SlugLabel: strPtr("zpa_alpha"),
				Generated: true, Derived: false,
			},
			{
				Type: "zpa_alpha_two", Product: "zpa", Provider: "zpa",
				BareName: "alpha_two", SlugLabel: strPtr("zpa_alpha"),
				Generated: true, Derived: false,
			},
			{
				Type: "zpa_derived_reorder", Product: "zpa", Provider: "zpa",
				BareName: "derived_reorder", SlugLabel: strPtr("zpa_derived"),
				Generated: true, Derived: true,
			},
			{
				Type: "zpa_known_only", Product: "zpa", Provider: "zpa",
				BareName: "known_only", SlugLabel: strPtr("zpa_known"),
				Generated: false, Derived: false,
			},
			{
				Type: "zpa_alpha_reference", Product: "zpa", Provider: "zpa",
				BareName: "alpha_reference", SlugLabel: strPtr("zpa_alpha"),
				Generated: true, Derived: false, SlugGroup: boolPtr(false),
			},
		},
		SourceFiles:   []string{"zpa/pack.json", "zpa/registry.json"},
		SourcesSHA256: strings.Repeat("0", 64),
	}
}

func TestSlugSelectionReturnsEntireRootAndStructuredDiagnostic(t *testing.T) {
	dep := deployment.Deployment{
		Overlay: "tenant-data//../stable",
		Roots: map[string]deployment.RootProviderConfig{
			"zpa": {HasStrategy: true, Strategy: "slug"},
		},
	}
	result, err := RootTopologyFromCatalog(RootTopologyOptions{
		Catalog:    fixtureCatalog(),
		Deployment: dep,
		Tenant:     strPtr("prod"),
		Selectors:  []string{"zpa_alpha_one"},
	})
	if err != nil {
		t.Fatalf("RootTopologyFromCatalog: %v", err)
	}

	wantRoots := []RootTopologyRoot{
		{
			Label: "zpa_alpha", Provider: strPtr("zpa"),
			Members: []string{"zpa_alpha_one", "zpa_alpha_two"},
			EnvDir:  strPtr("tenant-data//../stable/envs/prod/zpa_alpha"),
		},
	}
	if !reflect.DeepEqual(result.Topology.Roots, wantRoots) {
		t.Errorf("Roots = %+v, want %+v", derefRoots(result.Topology.Roots), derefRoots(wantRoots))
	}

	wantResourceRoots := map[string]string{
		"zpa_alpha_one": "zpa_alpha",
		"zpa_alpha_two": "zpa_alpha",
	}
	if !reflect.DeepEqual(result.Topology.ResourceRoots, wantResourceRoots) {
		t.Errorf("ResourceRoots = %v, want %v", result.Topology.ResourceRoots, wantResourceRoots)
	}

	wantDiagnostics := []WholeRootDiagnostic{
		{
			Level:             "note",
			Code:              "WHOLE_ROOT_SELECTION",
			Message:           "selecting zpa_alpha_one selects whole root zpa_alpha; also operating on zpa_alpha_two",
			SelectedMembers:   []string{"zpa_alpha_one"},
			Root:              "zpa_alpha",
			AdditionalMembers: []string{"zpa_alpha_two"},
		},
	}
	if !reflect.DeepEqual(result.Diagnostics, wantDiagnostics) {
		t.Errorf("Diagnostics = %+v, want %+v", result.Diagnostics, wantDiagnostics)
	}
}

func TestDerivedAndPackExcludedResourcesRemainSeparateUnderSlugGrouping(t *testing.T) {
	dep := deployment.Deployment{
		Overlay: ".",
		Roots: map[string]deployment.RootProviderConfig{
			"zpa": {HasStrategy: true, Strategy: "slug"},
		},
	}
	result, err := RootTopologyFromCatalog(RootTopologyOptions{
		Catalog:    fixtureCatalog(),
		Deployment: dep,
		Tenant:     nil,
		Selectors:  []string{"zpa"},
	})
	if err != nil {
		t.Fatalf("RootTopologyFromCatalog: %v", err)
	}

	var labels []string
	for _, root := range result.Topology.Roots {
		labels = append(labels, root.Label)
	}
	wantLabels := []string{"zpa_alpha", "zpa_alpha_reference", "zpa_derived_reorder"}
	if !reflect.DeepEqual(labels, wantLabels) {
		t.Errorf("labels = %v, want %v", labels, wantLabels)
	}
	if result.Topology.Directories != nil {
		t.Errorf("Directories = %+v, want nil", result.Topology.Directories)
	}
	for _, root := range result.Topology.Roots {
		if root.EnvDir != nil {
			t.Errorf("root %s EnvDir = %v, want nil", root.Label, *root.EnvDir)
		}
	}
	wantResourceRoots := map[string]string{
		"zpa_alpha_one":       "zpa_alpha",
		"zpa_alpha_two":       "zpa_alpha",
		"zpa_alpha_reference": "zpa_alpha_reference",
		"zpa_derived_reorder": "zpa_derived_reorder",
	}
	if !reflect.DeepEqual(result.Topology.ResourceRoots, wantResourceRoots) {
		t.Errorf("ResourceRoots = %v, want %v", result.Topology.ResourceRoots, wantResourceRoots)
	}
}

func TestKnownNonGeneratedAndUnknownSelectorsFailClosed(t *testing.T) {
	for _, selector := range []string{"zpa_known_only", "zpa_missing"} {
		_, err := RootTopologyFromCatalog(RootTopologyOptions{
			Catalog:    fixtureCatalog(),
			Deployment: deployment.Deployment{Overlay: ".", Roots: map[string]deployment.RootProviderConfig{}},
			Tenant:     nil,
			Selectors:  []string{selector},
		})
		if err == nil || !strings.Contains(err.Error(), "unknown or non-generated resource selector") {
			t.Errorf("selector %q: err = %v, want message containing %q", selector, err, "unknown or non-generated resource selector")
		}
	}
}

func TestLibraryBoundaryRejectsInvalidTenantsWithoutRelyingOnTheHost(t *testing.T) {
	for _, tenant := range []string{"", ".", "..", "bad/tenant", "é"} {
		_, err := RootTopologyFromCatalog(RootTopologyOptions{
			Catalog:    fixtureCatalog(),
			Deployment: deployment.Deployment{Overlay: ".", Roots: map[string]deployment.RootProviderConfig{}},
			Tenant:     strPtr(tenant),
			Selectors:  []string{},
		})
		if err == nil || !strings.Contains(err.Error(), "TENANT must match") {
			t.Errorf("tenant %q: err = %v, want message containing %q", tenant, err, "TENANT must match")
		}
	}
}

func TestExplicitGroupsRejectDerivedAndCrossProviderMembers(t *testing.T) {
	_, err := RootTopologyFromCatalog(RootTopologyOptions{
		Catalog: fixtureCatalog(),
		Deployment: deployment.Deployment{
			Overlay: ".",
			Roots: map[string]deployment.RootProviderConfig{
				"zpa": {HasGroups: true, Groups: map[string][]string{"combined": {"zpa_derived_reorder"}}},
			},
		},
		Tenant:    nil,
		Selectors: []string{},
	})
	if err == nil || !strings.Contains(err.Error(), "derived type") {
		t.Errorf("derived member: err = %v, want message containing %q", err, "derived type")
	}

	_, err = RootTopologyFromCatalog(RootTopologyOptions{
		Catalog: fixtureCatalog(),
		Deployment: deployment.Deployment{
			Overlay: ".",
			Roots: map[string]deployment.RootProviderConfig{
				"other": {HasStrategy: true, Strategy: "explicit"},
			},
		},
		Tenant:    nil,
		Selectors: []string{},
	})
	if err == nil || !strings.Contains(err.Error(), "not a declared provider") {
		t.Errorf("undeclared provider: err = %v, want message containing %q", err, "not a declared provider")
	}
}

func TestExplicitGroupsMayIncludeGenerateOnlyType(t *testing.T) {
	result, err := RootTopologyFromCatalog(RootTopologyOptions{
		Catalog: fixtureCatalog(),
		Deployment: deployment.Deployment{
			Overlay: ".",
			Roots: map[string]deployment.RootProviderConfig{
				"zpa": {
					HasGroups: true,
					Groups: map[string][]string{
						"zpa_explicit": {"zpa_alpha_one", "zpa_alpha_reference"},
					},
				},
			},
		},
		Tenant:    nil,
		Selectors: []string{"zpa_alpha_one"},
	})
	if err != nil {
		t.Fatalf("RootTopologyFromCatalog: %v", err)
	}
	if len(result.Topology.Roots) == 0 {
		t.Fatalf("Roots is empty")
	}
	want := []string{"zpa_alpha_one", "zpa_alpha_reference"}
	if !reflect.DeepEqual(result.Topology.Roots[0].Members, want) {
		t.Errorf("Roots[0].Members = %v, want %v", result.Topology.Roots[0].Members, want)
	}
}

// derefRoots renders a []RootTopologyRoot with its pointer fields
// dereferenced, purely to make a failing reflect.DeepEqual diff readable
// in test output.
func derefRoots(roots []RootTopologyRoot) []map[string]any {
	out := make([]map[string]any, len(roots))
	for i, root := range roots {
		entry := map[string]any{"label": root.Label, "members": root.Members}
		if root.Provider != nil {
			entry["provider"] = *root.Provider
		}
		if root.EnvDir != nil {
			entry["env_dir"] = *root.EnvDir
		}
		out[i] = entry
	}
	return out
}
