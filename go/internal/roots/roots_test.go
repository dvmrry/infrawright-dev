package roots

// roots_test.go exercises singleton topology over a compact ResourceSet.

import (
	"path/filepath"
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
				Generated: true,
			},
			{
				Type: "zpa_alpha_two", Product: "zpa", Provider: "zpa",
				BareName:  "alpha_two",
				Generated: true,
			},
			{
				Type: "zpa_derived_reorder", Product: "zpa", Provider: "zpa",
				BareName:  "derived_reorder",
				Generated: true,
			},
			{
				Type: "zpa_known_only", Product: "zpa", Provider: "zpa",
				BareName:  "known_only",
				Generated: false,
			},
			{
				Type: "zpa_alpha_reference", Product: "zpa", Provider: "zpa",
				BareName:  "alpha_reference",
				Generated: true,
			},
		},
	}
}

func fixtureResourceSetWithDataReferent() metadata.ResourceSet {
	resourceSet := fixtureResourceSet()
	resourceSet.Resources = append(resourceSet.Resources, metadata.ResourceDescriptor{
		Type:         "zpa_data_only",
		Product:      "zpa",
		Provider:     "zpa",
		BareName:     "data_only",
		DataReferent: true,
	})
	return resourceSet
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

func TestLoadedResourceShapeSurfacesDataReferent(t *testing.T) {
	root := metadata.LoadedPackRoot{
		Packs: metadata.PackMetadata{
			ProviderPrefixes: map[string]string{"sample_": "sample"},
		},
		Resources: map[string]metadata.LoadedResourceMetadata{
			"sample_groups": {
				Type:     "sample_groups",
				Product:  "sample",
				Provider: "sample",
				Registry: metadata.JsonObject{"data_referent": true},
			},
		},
	}

	got := loadedResourceShape(root, "sample_groups")
	if !got.DataReferent {
		t.Errorf("loadedResourceShape(%q).DataReferent = %t, want true", "sample_groups", got.DataReferent)
	}
	if got.Generated {
		t.Errorf("loadedResourceShape(%q).Generated = %t, want false", "sample_groups", got.Generated)
	}
}

func TestLoadedRootTopologyIncludesDataReferentAndExactExpansionSelectsIt(t *testing.T) {
	root := metadata.LoadedPackRoot{
		Packs: metadata.PackMetadata{
			ProviderPrefixes: map[string]string{"sample_": "sample"},
		},
		Resources: map[string]metadata.LoadedResourceMetadata{
			"sample_groups_data": {
				Type:     "sample_groups_data",
				Product:  "sample",
				Provider: "sample",
				Registry: metadata.JsonObject{
					"data_referent": true,
					"fetch":         metadata.JsonObject{"pagination": "zia", "path": "locations/groups"},
				},
			},
			"sample_rule": {
				Type:     "sample_rule",
				Product:  "sample",
				Provider: "sample",
				Registry: metadata.JsonObject{"generate": true},
			},
		},
	}

	result, err := LoadedRootTopology(LoadedRootTopologyOptions{
		Root: root, Deployment: deployment.Deployment{Overlay: "."}, Selectors: []string{},
	})
	if err != nil {
		t.Fatalf("LoadedRootTopology(data referent fixture) error = %v, want nil", err)
	}
	wantRoots := []RootTopologyRoot{
		{Label: "sample_groups_data", Provider: strPtr("sample"), Members: []string{"sample_groups_data"}},
		{Label: "sample_rule", Provider: strPtr("sample"), Members: []string{"sample_rule"}},
	}
	if !reflect.DeepEqual(result.Topology.Roots, wantRoots) {
		t.Errorf("LoadedRootTopology(data referent fixture) roots mismatch:\n got: %#v\nwant: %#v", result.Topology.Roots, wantRoots)
	}
	wantResourceRoots := map[string]string{
		"sample_groups_data": "sample_groups_data",
		"sample_rule":        "sample_rule",
	}
	if !reflect.DeepEqual(result.Topology.ResourceRoots, wantResourceRoots) {
		t.Errorf("LoadedRootTopology(data referent fixture) resource roots mismatch:\n got: %#v\nwant: %#v", result.Topology.ResourceRoots, wantResourceRoots)
	}

	generated, err := ExpandLoadedResources(root, []string{})
	if err != nil {
		t.Fatalf("ExpandLoadedResources(data referent fixture) error = %v, want nil", err)
	}
	if !reflect.DeepEqual(generated, []string{"sample_rule"}) {
		t.Errorf("ExpandLoadedResources(data referent fixture) mismatch:\n got: %#v\nwant: %#v", generated, []string{"sample_rule"})
	}
	selected, err := ExpandLoadedRootTargets(root, []string{"sample_groups_data"})
	if err != nil {
		t.Fatalf("ExpandLoadedRootTargets(%q) error = %v, want nil", "sample_groups_data", err)
	}
	if !reflect.DeepEqual(selected, []string{"sample_groups_data"}) {
		t.Errorf("ExpandLoadedRootTargets(%q) = %#v, want exactly the selected data root", "sample_groups_data", selected)
	}
	if _, err := ExpandLoadedResources(root, []string{"sample_groups_data"}); err == nil {
		t.Error("ExpandLoadedResources(exact data selector) error = nil, want generated-only boundary refusal")
	}
	if _, err := ExpandLoadedResources(root, []string{"sample_rule", "sample_groups_data"}); err == nil {
		t.Error("ExpandLoadedResources(mixed generated/data selectors) error = nil, want generated-only boundary refusal")
	}
}

func TestResourceSetRootTopologyIncludesDataReferent(t *testing.T) {
	result, err := RootTopologyFromResourceSet(RootTopologyOptions{
		ResourceSet: fixtureResourceSetWithDataReferent(),
		Deployment:  deployment.Deployment{Overlay: "."},
		Selectors:   []string{},
	})
	if err != nil {
		t.Fatalf("RootTopologyFromResourceSet(data referent fixture) error = %v, want nil", err)
	}
	wantRoot := RootTopologyRoot{
		Label: "zpa_data_only", Provider: strPtr("zpa"), Members: []string{"zpa_data_only"},
	}
	var gotRoot *RootTopologyRoot
	for i := range result.Topology.Roots {
		if result.Topology.Roots[i].Label == wantRoot.Label {
			gotRoot = &result.Topology.Roots[i]
			break
		}
	}
	if gotRoot == nil {
		t.Fatalf("RootTopologyFromResourceSet(data referent fixture) roots = %#v, want root %#v", result.Topology.Roots, wantRoot)
	}
	if !reflect.DeepEqual(*gotRoot, wantRoot) {
		t.Errorf("RootTopologyFromResourceSet(data referent fixture) data root = %#v, want %#v", *gotRoot, wantRoot)
	}
	if got := result.Topology.ResourceRoots["zpa_data_only"]; got != "zpa_data_only" {
		t.Errorf("RootTopologyFromResourceSet(data referent fixture) ResourceRoots[%q] = %q, want %q", "zpa_data_only", got, "zpa_data_only")
	}
}

func TestChangedPathScopeMapsDataReferentConfigToRoot(t *testing.T) {
	const changedPath = "config/acme/zpa_data_only.auto.tfvars.json"
	scope, err := ChangedPathScopeFromResourceSet(ChangedPathScopeOptions{
		Paths:          []string{changedPath},
		Workspace:      "/workspace",
		DeploymentPath: "/workspace/deployment.json",
		Deployment:     deployment.Deployment{Overlay: "."},
		ResourceSet:    fixtureResourceSetWithDataReferent(),
	})
	if err != nil {
		t.Fatalf("ChangedPathScopeFromResourceSet(data referent config) error = %v, want nil", err)
	}
	wantMatch := ChangedPathMatch{
		Path: changedPath, Kinds: []ChangedPathKind{ChangedPathKindConfig},
		Tenants: []string{"acme"}, Resources: []string{"zpa_data_only"}, Roots: []string{"zpa_data_only"},
	}
	if !reflect.DeepEqual(scope.PathMatches, []ChangedPathMatch{wantMatch}) {
		t.Errorf("ChangedPathScopeFromResourceSet(data referent config) PathMatches = %#v, want %#v", scope.PathMatches, []ChangedPathMatch{wantMatch})
	}
	wantRoot := AffectedRoot{
		Label: "zpa_data_only", Provider: strPtr("zpa"), Members: []string{"zpa_data_only"},
		MatchedResources: []string{"zpa_data_only"}, Paths: []string{changedPath},
	}
	if !reflect.DeepEqual(scope.AffectedRoots, []AffectedRoot{wantRoot}) {
		t.Errorf("ChangedPathScopeFromResourceSet(data referent config) AffectedRoots = %#v, want %#v", scope.AffectedRoots, []AffectedRoot{wantRoot})
	}
}

func TestPlanRootsDiscoversDataReferentRoot(t *testing.T) {
	workspace := t.TempDir()
	dataRootPath := filepath.Join(workspace, "envs/acme/zpa_data_only")
	mustMkdirAll(t, dataRootPath)
	mustWriteFile(t, filepath.Join(dataRootPath, "tfplan"), "data-only-plan")

	result, err := PlanRootsFromResourceSet(PlanRootsOptions{
		Workspace:   workspace,
		Deployment:  deployment.Deployment{Overlay: "."},
		ResourceSet: fixtureResourceSetWithDataReferent(),
		Selectors:   []string{},
	})
	if err != nil {
		t.Fatalf("PlanRootsFromResourceSet(data referent fixture) error = %v, want nil", err)
	}
	if len(result.Result.Roots) != 1 {
		t.Fatalf("PlanRootsFromResourceSet(data referent fixture) roots = %#v, want one discovered data root", result.Result.Roots)
	}
	got := result.Result.Roots[0]
	if got.Tenant != "acme" || got.Label != "zpa_data_only" {
		t.Errorf("PlanRootsFromResourceSet(data referent fixture) root identity = %#v, want tenant acme / label zpa_data_only", got)
	}
	if !reflect.DeepEqual(got.Members, []string{"zpa_data_only"}) {
		t.Errorf("PlanRootsFromResourceSet(data referent fixture) Members = %#v, want %#v", got.Members, []string{"zpa_data_only"})
	}
	if got.ArtifactState != ArtifactStateIncomplete {
		t.Errorf("PlanRootsFromResourceSet(data referent fixture) ArtifactState = %q, want %q for tfplan without sources", got.ArtifactState, ArtifactStateIncomplete)
	}
	if !got.Artifacts.Tfplan.Exists || got.Artifacts.TfplanSources.Exists {
		t.Errorf("PlanRootsFromResourceSet(data referent fixture) Artifacts = %#v, want tfplan present and sources absent", got.Artifacts)
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
