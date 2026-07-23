package roots

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"testing"

	"github.com/dvmrry/infrawright-dev/go/internal/deployment"
	"github.com/dvmrry/infrawright-dev/go/internal/metadata"
)

const qualificationBackendKeysSHA256 = "9895329b146e360acfe06b47bc410333a66b08e3f95d74e1b2ae79751eedc4dd"

// TestFullProfileSingletonTopologyAndBackendKeys qualifies the complete pack
// surface: every generated resource owns one state key named from its resource
// type, and the full key inventory remains stable.
func TestFullProfileSingletonTopologyAndBackendKeys(t *testing.T) {
	root := qualificationRepoRoot(t)
	profilePath := filepath.Join(root, "packs", "full.packset.json")
	loaded, err := metadata.LoadPackRoot(metadata.LoadPackRootOptions{
		PacksRoot:   filepath.Join(root, "packs"),
		ProfilePath: &profilePath,
	})
	if err != nil {
		t.Fatalf("LoadPackRoot(full profile) = %v, want nil", err)
	}
	if got := len(loaded.Resources); got != 151 {
		t.Fatalf("LoadPackRoot(full profile) generated resource count = %d, want 151", got)
	}

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
	if got := len(result.Topology.Roots); got != 151 {
		t.Fatalf("LoadedRootTopology(full profile) root count = %d, want 151", got)
	}

	for resourceType, resource := range loaded.Resources {
		generated, _ := resource.Registry["generate"].(bool)
		if !generated {
			t.Errorf("LoadPackRoot(full profile) resource %q generate = %v, want true", resourceType, resource.Registry["generate"])
			continue
		}
	}
	currentBackendKeys := make([]string, 0, len(result.Topology.Roots))
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

		currentBackendKeys = append(currentBackendKeys, "qualification/"+root.Label+".tfstate")
	}
	if !sort.StringsAreSorted(labels) {
		t.Errorf("LoadedRootTopology(full profile) root labels = %v, want sorted labels", labels)
	}
	if got := len(result.Topology.ResourceRoots); got != 151 {
		t.Errorf("LoadedRootTopology(full profile) resource_roots count = %d, want 151 identity mappings", got)
	}

	for resourceType, label := range result.Topology.ResourceRoots {
		if resourceType != label {
			t.Errorf("LoadedRootTopology(full profile) resource_roots[%q] = %q, want identity mapping", resourceType, label)
		}
	}

	sort.Strings(currentBackendKeys)
	if got := qualificationKeyDigest(currentBackendKeys); got != qualificationBackendKeysSHA256 {
		t.Errorf("full-profile qualification backend-key digest = %s, want %s", got, qualificationBackendKeysSHA256)
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

func qualificationKeyDigest(keys []string) string {
	hasher := sha256.New()
	for _, key := range keys {
		hasher.Write([]byte(key))
		hasher.Write([]byte{'\n'})
	}
	return hex.EncodeToString(hasher.Sum(nil))
}
