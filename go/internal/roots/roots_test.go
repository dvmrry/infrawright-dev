package roots

// roots_test.go ports the original test corpus's six test cases verbatim,
// against the same fixture ResourceSet literal that Node test builds
// in-line, exercised through RootTopologyFromResourceSet (the Go analogue of
// the Node test's rootTopology import).

import (
	"reflect"
	"strings"
	"testing"

	"github.com/dvmrry/infrawright-dev/go/internal/deployment"
	"github.com/dvmrry/infrawright-dev/go/internal/metadata"
)

func strPtr(s string) *string { return &s }

func fixtureResourceSet() metadata.ResourceSet {
	return metadata.ResourceSet{
		DeclaredProviders: []string{"zpa"},
		Resources: []metadata.ResourceDescriptor{
			{
				Type: "zpa_alpha_one", Product: "zpa", Provider: "zpa",
				BareName:  "alpha_one",
				Generated: true, Derived: false,
			},
			{
				Type: "zpa_alpha_two", Product: "zpa", Provider: "zpa",
				BareName:  "alpha_two",
				Generated: true, Derived: false,
			},
			{
				Type: "zpa_derived_reorder", Product: "zpa", Provider: "zpa",
				BareName:  "derived_reorder",
				Generated: true, Derived: true,
			},
			{
				Type: "zpa_known_only", Product: "zpa", Provider: "zpa",
				BareName:  "known_only",
				Generated: false, Derived: false,
			},
			{
				Type: "zpa_alpha_reference", Product: "zpa", Provider: "zpa",
				BareName:  "alpha_reference",
				Generated: true, Derived: false,
			},
		},
	}
}

func TestSelectionReturnsOnlySingletonRoot(t *testing.T) {
	dep := deployment.Deployment{
		Overlay: "tenant-data//../stable",
		Roots:   map[string]deployment.RootProviderConfig{},
	}
	result, err := RootTopologyFromResourceSet(RootTopologyOptions{
		ResourceSet: fixtureResourceSet(),
		Deployment:  dep,
		Tenant:      strPtr("prod"),
		Selectors:   []string{"zpa_alpha_one"},
	})
	if err != nil {
		t.Fatalf("RootTopologyFromResourceSet: %v", err)
	}

	wantRoots := []RootTopologyRoot{
		{
			Label: "zpa_alpha_one", Provider: strPtr("zpa"),
			Members: []string{"zpa_alpha_one"},
			EnvDir:  strPtr("tenant-data//../stable/envs/prod/zpa_alpha_one"),
		},
	}
	if !reflect.DeepEqual(result.Topology.Roots, wantRoots) {
		t.Errorf("Roots = %+v, want %+v", derefRoots(result.Topology.Roots), derefRoots(wantRoots))
	}

	wantResourceRoots := map[string]string{
		"zpa_alpha_one": "zpa_alpha_one",
	}
	if !reflect.DeepEqual(result.Topology.ResourceRoots, wantResourceRoots) {
		t.Errorf("ResourceRoots = %v, want %v", result.Topology.ResourceRoots, wantResourceRoots)
	}

	wantDiagnostics := []WholeRootDiagnostic(nil)
	if !reflect.DeepEqual(result.Diagnostics, wantDiagnostics) {
		t.Errorf("Diagnostics = %+v, want %+v", result.Diagnostics, wantDiagnostics)
	}
}

func TestEveryGeneratedResourceHasItsOwnRoot(t *testing.T) {
	dep := deployment.Deployment{
		Overlay: ".",
		Roots:   map[string]deployment.RootProviderConfig{},
	}
	result, err := RootTopologyFromResourceSet(RootTopologyOptions{
		ResourceSet: fixtureResourceSet(),
		Deployment:  dep,
		Tenant:      nil,
		Selectors:   []string{"zpa"},
	})
	if err != nil {
		t.Fatalf("RootTopologyFromResourceSet: %v", err)
	}

	var labels []string
	for _, root := range result.Topology.Roots {
		labels = append(labels, root.Label)
	}
	wantLabels := []string{"zpa_alpha_one", "zpa_alpha_reference", "zpa_alpha_two", "zpa_derived_reorder"}
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
		"zpa_alpha_one":       "zpa_alpha_one",
		"zpa_alpha_two":       "zpa_alpha_two",
		"zpa_alpha_reference": "zpa_alpha_reference",
		"zpa_derived_reorder": "zpa_derived_reorder",
	}
	if !reflect.DeepEqual(result.Topology.ResourceRoots, wantResourceRoots) {
		t.Errorf("ResourceRoots = %v, want %v", result.Topology.ResourceRoots, wantResourceRoots)
	}
}

func TestKnownNonGeneratedAndUnknownSelectorsFailClosed(t *testing.T) {
	for _, selector := range []string{"zpa_known_only", "zpa_missing"} {
		_, err := RootTopologyFromResourceSet(RootTopologyOptions{
			ResourceSet: fixtureResourceSet(),
			Deployment:  deployment.Deployment{Overlay: ".", Roots: map[string]deployment.RootProviderConfig{}},
			Tenant:      nil,
			Selectors:   []string{selector},
		})
		if err == nil || !strings.Contains(err.Error(), "unknown or non-generated resource selector") {
			t.Errorf("selector %q: err = %v, want message containing %q", selector, err, "unknown or non-generated resource selector")
		}
	}
}

func TestLibraryBoundaryRejectsInvalidTenantsWithoutRelyingOnTheHost(t *testing.T) {
	for _, tenant := range []string{"", ".", "..", "bad/tenant", "é"} {
		_, err := RootTopologyFromResourceSet(RootTopologyOptions{
			ResourceSet: fixtureResourceSet(),
			Deployment:  deployment.Deployment{Overlay: ".", Roots: map[string]deployment.RootProviderConfig{}},
			Tenant:      strPtr(tenant),
			Selectors:   []string{},
		})
		if err == nil || !strings.Contains(err.Error(), "TENANT must match") {
			t.Errorf("tenant %q: err = %v, want message containing %q", tenant, err, "TENANT must match")
		}
	}
}

func TestProviderOptionsDoNotChangeSingletonTopology(t *testing.T) {
	result, err := RootTopologyFromResourceSet(RootTopologyOptions{
		ResourceSet: fixtureResourceSet(),
		Deployment: deployment.Deployment{
			Overlay: ".",
			Roots: map[string]deployment.RootProviderConfig{
				"zpa": {HasCrossStateReferences: true, CrossStateReferences: true},
			},
		},
		Tenant:    nil,
		Selectors: []string{"zpa_alpha_one"},
	})
	if err != nil {
		t.Fatalf("RootTopologyFromResourceSet: %v", err)
	}
	if len(result.Topology.Roots) == 0 {
		t.Fatalf("Roots is empty")
	}
	want := []string{"zpa_alpha_one"}
	if !reflect.DeepEqual(result.Topology.Roots[0].Members, want) {
		t.Errorf("Roots[0].Members = %v, want %v", result.Topology.Roots[0].Members, want)
	}
}

func TestUnknownDeploymentRootProviderStillFailsClosed(t *testing.T) {
	_, err := RootTopologyFromResourceSet(RootTopologyOptions{
		ResourceSet: fixtureResourceSet(),
		Deployment: deployment.Deployment{
			Overlay: ".",
			Roots: map[string]deployment.RootProviderConfig{
				"unknown": {},
			},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "roots.unknown is not a declared provider prefix value") {
		t.Fatalf("RootTopologyFromResourceSet error = %v, want undeclared-provider failure", err)
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
