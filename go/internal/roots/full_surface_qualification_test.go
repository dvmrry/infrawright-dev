package roots

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"testing"

	"github.com/dvmrry/infrawright-dev/go/internal/deployment"
	"github.com/dvmrry/infrawright-dev/go/internal/metadata"
)

// TestFullProfileSingletonTopologyMatchesSelectedResources qualifies the
// complete selected pack surface. The profile, rather than a compiled-in
// inventory, defines the exact resource set.
func TestFullProfileSingletonTopologyMatchesSelectedResources(t *testing.T) {
	root := qualificationRepoRoot(t)
	profilePath := filepath.Join(root, "packs", "full.packset.json")
	loaded, err := metadata.LoadPackRoot(metadata.LoadPackRootOptions{
		PacksRoot:   filepath.Join(root, "packs"),
		ProfilePath: &profilePath,
	})
	if err != nil {
		t.Fatalf("LoadPackRoot(full profile) = %v, want nil", err)
	}
	wantResourceTypes := make([]string, 0, len(loaded.Resources))
	for resourceType, resource := range loaded.Resources {
		if generated, _ := resource.Registry["generate"].(bool); generated {
			wantResourceTypes = append(wantResourceTypes, resourceType)
		}
	}
	sort.Strings(wantResourceTypes)

	result, err := LoadedRootTopology(LoadedRootTopologyOptions{
		Root:       loaded,
		Deployment: deployment.Deployment{Overlay: ".", Roots: map[string]deployment.RootProviderConfig{}},
	})
	if err != nil {
		t.Fatalf("LoadedRootTopology(full profile) = %v, want nil", err)
	}
	if got := len(result.Diagnostics); got != 0 {
		t.Errorf("LoadedRootTopology(full profile) diagnostics = %#v, want none", result.Diagnostics)
	}
	if got := len(result.Topology.Roots); got != len(wantResourceTypes) {
		t.Fatalf("LoadedRootTopology(full profile) root count = %d, want selected generated resource count %d", got, len(wantResourceTypes))
	}

	labels := make([]string, 0, len(result.Topology.Roots))
	for _, root := range result.Topology.Roots {
		labels = append(labels, root.Label)
		if len(root.Members) != 1 {
			t.Errorf("LoadedRootTopology(full profile) root %q members = %v, want exactly one member", root.Label, root.Members)
			continue
		}

		resourceType := root.Members[0]
		resource, ok := loaded.Resources[resourceType]
		if !ok {
			t.Errorf("LoadedRootTopology(full profile) root %q member = %q, want a loaded resource", root.Label, resourceType)
			continue
		}
		generated, _ := resource.Registry["generate"].(bool)
		if root.Label != resourceType || !generated {
			t.Errorf("LoadedRootTopology(full profile) root = {label:%q members:%v}, want label == member == generated resource type %q", root.Label, root.Members, resourceType)
		}
		if root.Provider == nil || *root.Provider != resource.Provider {
			t.Errorf("LoadedRootTopology(full profile) root %q provider = %v, want loaded resource provider %q", root.Label, root.Provider, resource.Provider)
		}
		if got := result.Topology.ResourceRoots[resourceType]; got != root.Label {
			t.Errorf("LoadedRootTopology(full profile) resource_roots[%q] = %q, want identity label %q", resourceType, got, root.Label)
		}
	}
	if !sort.StringsAreSorted(labels) {
		t.Errorf("LoadedRootTopology(full profile) root labels = %v, want sorted labels", labels)
	}
	if !reflect.DeepEqual(labels, wantResourceTypes) {
		t.Errorf("LoadedRootTopology(full profile) root labels = %v, want selected generated resources %v", labels, wantResourceTypes)
	}
	if got := len(result.Topology.ResourceRoots); got != len(wantResourceTypes) {
		t.Errorf("LoadedRootTopology(full profile) resource_roots count = %d, want %d identity mappings", got, len(wantResourceTypes))
	}

	for resourceType, label := range result.Topology.ResourceRoots {
		if resourceType != label {
			t.Errorf("LoadedRootTopology(full profile) resource_roots[%q] = %q, want identity mapping", resourceType, label)
		}
	}
}

func qualificationRepoRoot(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller(0) = false, want true")
	}
	for directory := filepath.Dir(thisFile); ; directory = filepath.Dir(directory) {
		if _, packsErr := os.Stat(filepath.Join(directory, "packs", "full.packset.json")); packsErr == nil {
			return directory
		}
		parent := filepath.Dir(directory)
		if parent == directory {
			t.Fatalf("qualificationRepoRoot(%q): reached filesystem root without packs/full.packset.json", thisFile)
		}
	}
}
