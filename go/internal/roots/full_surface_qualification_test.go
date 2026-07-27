package roots

import (
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	"github.com/dvmrry/infrawright-dev/go/internal/deployment"
	"github.com/dvmrry/infrawright-dev/go/internal/metadata"
)

// TestSelectedPackSingletonTopologyMatchesSelectedResources qualifies a
// complete selected pack surface without depending on any committed pack.
func TestSelectedPackSingletonTopologyMatchesSelectedResources(t *testing.T) {
	directory := t.TempDir()
	pack := filepath.Join(directory, "sample")
	mustMkdirAll(t, pack)
	mustWriteFile(t, filepath.Join(pack, "pack.json"), `{"provider_prefixes":{"sample_":"sample"}}`)
	mustWriteFile(t, filepath.Join(pack, "registry.json"), `{
		"sample_alpha":{"generate":true,"product":"sample"},
		"sample_beta":{"generate":true,"product":"sample"}
	}`)
	loaded, err := metadata.LoadPackRoot(metadata.LoadPackRootOptions{PacksRoot: directory})
	if err != nil {
		t.Fatalf("LoadPackRoot(synthetic pack) = %v, want nil", err)
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
		t.Fatalf("LoadedRootTopology(synthetic pack) = %v, want nil", err)
	}
	if got := len(result.Diagnostics); got != 0 {
		t.Errorf("LoadedRootTopology(synthetic pack) diagnostics = %#v, want none", result.Diagnostics)
	}
	if got := len(result.Topology.Roots); got != len(wantResourceTypes) {
		t.Fatalf("LoadedRootTopology(synthetic pack) root count = %d, want selected generated resource count %d", got, len(wantResourceTypes))
	}

	labels := make([]string, 0, len(result.Topology.Roots))
	for _, root := range result.Topology.Roots {
		labels = append(labels, root.Label)
		if len(root.Members) != 1 {
			t.Errorf("LoadedRootTopology(synthetic pack) root %q members = %v, want exactly one member", root.Label, root.Members)
			continue
		}

		resourceType := root.Members[0]
		resource, ok := loaded.Resources[resourceType]
		if !ok {
			t.Errorf("LoadedRootTopology(synthetic pack) root %q member = %q, want a loaded resource", root.Label, resourceType)
			continue
		}
		generated, _ := resource.Registry["generate"].(bool)
		if root.Label != resourceType || !generated {
			t.Errorf("LoadedRootTopology(synthetic pack) root = {label:%q members:%v}, want label == member == generated resource type %q", root.Label, root.Members, resourceType)
		}
		if root.Provider == nil || *root.Provider != resource.Provider {
			t.Errorf("LoadedRootTopology(synthetic pack) root %q provider = %v, want loaded resource provider %q", root.Label, root.Provider, resource.Provider)
		}
		if got := result.Topology.ResourceRoots[resourceType]; got != root.Label {
			t.Errorf("LoadedRootTopology(synthetic pack) resource_roots[%q] = %q, want identity label %q", resourceType, got, root.Label)
		}
	}
	if !sort.StringsAreSorted(labels) {
		t.Errorf("LoadedRootTopology(synthetic pack) root labels = %v, want sorted labels", labels)
	}
	if !reflect.DeepEqual(labels, wantResourceTypes) {
		t.Errorf("LoadedRootTopology(synthetic pack) root labels = %v, want selected generated resources %v", labels, wantResourceTypes)
	}
	if got := len(result.Topology.ResourceRoots); got != len(wantResourceTypes) {
		t.Errorf("LoadedRootTopology(synthetic pack) resource_roots count = %d, want %d identity mappings", got, len(wantResourceTypes))
	}

	for resourceType, label := range result.Topology.ResourceRoots {
		if resourceType != label {
			t.Errorf("LoadedRootTopology(synthetic pack) resource_roots[%q] = %q, want identity mapping", resourceType, label)
		}
	}
}
