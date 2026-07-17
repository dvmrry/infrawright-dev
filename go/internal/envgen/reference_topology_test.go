package envgen

// reference_topology_test.go ports both tests in
// node-tests/reference-topology.test.ts verbatim, against the real
// committed pack root (packs/ + packsets/full.json) -- exactly as the Node
// test does -- through this repository's own metadata/roots Go packages.
// No Node or Python oracle is needed: the expected edges/dependency sets
// are literal fixtures hardcoded in the Node test itself, not derived from
// a live run of either runtime.

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"

	"github.com/dvmrry/infrawright-dev/go/internal/deployment"
	"github.com/dvmrry/infrawright-dev/go/internal/metadata"
	"github.com/dvmrry/infrawright-dev/go/internal/roots"
)

// repoRoot walks up from this test file's directory until it finds a
// directory containing both "catalogs" and "packs", the same convention
// go/internal/metadata/gate_test.go and go/internal/transform/kernel_test.go
// already establish for locating the committed pack corpus from a Go test.
func repoRoot(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller(0) failed")
	}
	dir := filepath.Dir(thisFile)
	for {
		_, catalogsErr := os.Stat(filepath.Join(dir, "catalogs"))
		_, packsErr := os.Stat(filepath.Join(dir, "packs"))
		if catalogsErr == nil && packsErr == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("walked up to filesystem root from %s without finding a directory containing both catalogs/ and packs/", filepath.Dir(thisFile))
		}
		dir = parent
	}
}

func committedRootForTopology(t *testing.T) metadata.LoadedPackRoot {
	t.Helper()
	root := repoRoot(t)
	packsRoot := filepath.Join(root, "packs")
	profilePath := filepath.Join(root, "packsets", "full.json")
	catalogPath := filepath.Join(root, "packsets", "full.json")
	loaded, err := metadata.LoadPackRoot(metadata.LoadPackRootOptions{
		PacksRoot:   packsRoot,
		ProfilePath: &profilePath,
		CatalogPath: &catalogPath,
	})
	if err != nil {
		t.Fatalf("LoadPackRoot: %v", err)
	}
	return loaded
}

func TestCrossStateTopologyKeepsSingletonDependenciesAndCollapsesGroups(t *testing.T) {
	root := committedRootForTopology(t)
	tenant := "tenant"

	singletonDeployment := deployment.Deployment{
		Overlay: ".",
		Roots: map[string]deployment.RootProviderConfig{
			"zpa": {HasCrossStateReferences: true, CrossStateReferences: true},
		},
	}
	singletonResult, err := roots.LoadedRootTopology(roots.LoadedRootTopologyOptions{
		Root: root, Deployment: singletonDeployment, Tenant: &tenant, Selectors: []string{},
	})
	if err != nil {
		t.Fatalf("LoadedRootTopology: %v", err)
	}
	singleton, err := ResolveCrossStateReferenceTopology(CrossStateReferenceTopologyOptions{
		Deployment: singletonDeployment, Root: root, Topology: singletonResult.Topology,
	})
	if err != nil {
		t.Fatalf("ResolveCrossStateReferenceTopology: %v", err)
	}
	wantEdges := []CrossStateReferenceEdge{
		{Field: "segment_group_id", Referent: "zpa_segment_group", ReferentRoot: "zpa_segment_group", Referrer: "zpa_application_segment", ReferrerRoot: "zpa_application_segment"},
		{Field: "server_groups.id", Referent: "zpa_server_group", ReferentRoot: "zpa_server_group", Referrer: "zpa_application_segment", ReferrerRoot: "zpa_application_segment"},
		{Field: "app_connector_groups.id", Referent: "zpa_app_connector_group", ReferentRoot: "zpa_app_connector_group", Referrer: "zpa_server_group", ReferrerRoot: "zpa_server_group"},
		{Field: "servers.id", Referent: "zpa_application_server", ReferentRoot: "zpa_application_server", Referrer: "zpa_server_group", ReferrerRoot: "zpa_server_group"},
	}
	if !reflect.DeepEqual(singleton.Edges, wantEdges) {
		t.Fatalf("Edges = %+v, want %+v", singleton.Edges, wantEdges)
	}

	gotDeps := setKeysSorted(singleton.DependenciesByRoot["zpa_application_segment"])
	wantDeps := []string{"zpa_segment_group", "zpa_server_group"}
	if !reflect.DeepEqual(gotDeps, wantDeps) {
		t.Fatalf("dependenciesByRoot[zpa_application_segment] = %v, want %v", gotDeps, wantDeps)
	}

	closure := CrossStateDependencyClosure([]string{"zpa_application_segment"}, singleton.DependenciesByRoot)
	wantClosure := []string{
		"zpa_app_connector_group",
		"zpa_application_segment",
		"zpa_application_server",
		"zpa_segment_group",
		"zpa_server_group",
	}
	if !reflect.DeepEqual(closure, wantClosure) {
		t.Fatalf("closure = %v, want %v", closure, wantClosure)
	}

	groupedDeployment := deployment.Deployment{
		Overlay: ".",
		Roots: map[string]deployment.RootProviderConfig{
			"zpa": {
				HasCrossStateReferences: true, CrossStateReferences: true,
				HasGroups: true,
				Groups: map[string][]string{
					"zpa_app": {
						"zpa_app_connector_group",
						"zpa_application_segment",
						"zpa_application_server",
						"zpa_segment_group",
						"zpa_server_group",
					},
				},
			},
		},
	}
	groupedTopologyResult, err := roots.LoadedRootTopology(roots.LoadedRootTopologyOptions{
		Root: root, Deployment: groupedDeployment, Tenant: &tenant, Selectors: []string{},
	})
	if err != nil {
		t.Fatalf("LoadedRootTopology: %v", err)
	}
	grouped, err := ResolveCrossStateReferenceTopology(CrossStateReferenceTopologyOptions{
		Deployment: groupedDeployment, Root: root, Topology: groupedTopologyResult.Topology,
	})
	if err != nil {
		t.Fatalf("ResolveCrossStateReferenceTopology: %v", err)
	}
	if len(grouped.Edges) != 0 {
		t.Fatalf("grouped.Edges = %+v, want empty", grouped.Edges)
	}
	if len(grouped.DependenciesByRoot) != 0 {
		t.Fatalf("grouped.DependenciesByRoot = %v, want empty", grouped.DependenciesByRoot)
	}
}

func setKeysSorted(set map[string]bool) []string {
	var out []string
	for key := range set {
		out = append(out, key)
	}
	// Match Node's `[...set]` insertion-order iteration for a set built by
	// this port's own addToSet in ascending-sorted addition order: the Go
	// side re-sorts explicitly here since map iteration order is undefined,
	// but the two roots this fixture ever adds ("zpa_segment_group" then
	// "zpa_server_group" by referrer/field-sorted edge order) already sort
	// lexicographically, so a plain sort reproduces the Node assertion's
	// literal expected order.
	sortStringsInPlace(out)
	return out
}

func sortStringsInPlace(values []string) {
	for i := 1; i < len(values); i++ {
		for j := i; j > 0 && values[j-1] > values[j]; j-- {
			values[j-1], values[j] = values[j], values[j-1]
		}
	}
}

func TestCrossStateTopologyRejectsDeclaredRootCycles(t *testing.T) {
	root := committedRootForTopology(t)
	tenant := "tenant"

	var manifests []metadata.PackManifest
	for _, manifest := range root.Packs.Manifests {
		if manifest.Name != "zpa" {
			manifests = append(manifests, manifest)
			continue
		}
		references, _ := manifest.Data["references"].(map[string]any)
		newReferences := map[string]any{}
		for k, v := range references {
			newReferences[k] = v
		}
		newReferences["zpa_segment_group"] = map[string]any{
			"application_id": map[string]any{"name_field": "name", "referent": "zpa_application_segment"},
		}
		newData := map[string]any{}
		for k, v := range manifest.Data {
			newData[k] = v
		}
		newData["references"] = newReferences
		manifests = append(manifests, metadata.PackManifest{
			Name: manifest.Name, Directory: manifest.Directory, Path: manifest.Path,
			Data: newData, ProviderPrefixes: manifest.ProviderPrefixes,
			ProviderSources: manifest.ProviderSources, RequiresShared: manifest.RequiresShared,
		})
	}
	cyclicRoot := root
	cyclicRoot.Packs.Manifests = manifests

	dep := deployment.Deployment{
		Overlay: ".",
		Roots: map[string]deployment.RootProviderConfig{
			"zpa": {HasCrossStateReferences: true, CrossStateReferences: true},
		},
	}
	topologyResult, err := roots.LoadedRootTopology(roots.LoadedRootTopologyOptions{
		Root: cyclicRoot, Deployment: dep, Tenant: &tenant, Selectors: []string{},
	})
	if err != nil {
		t.Fatalf("LoadedRootTopology: %v", err)
	}
	_, err = ResolveCrossStateReferenceTopology(CrossStateReferenceTopologyOptions{
		Deployment: dep, Root: cyclicRoot, Topology: topologyResult.Topology,
	})
	if err == nil {
		t.Fatal("expected a cross-state reference cycle error")
	}
	mustMatch(t, err.Error(), `cross-state reference cycle detected.*explicitly group every member`)
}
