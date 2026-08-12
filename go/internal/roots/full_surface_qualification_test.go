package roots

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"testing"

	"github.com/dvmrry/infrawright-dev/go/internal/deployment"
	"github.com/dvmrry/infrawright-dev/go/internal/fixtureupdate"
	"github.com/dvmrry/infrawright-dev/go/internal/metadata"
)

// qualificationBackendKeysSHA256 pins the committed full distribution's
// backend-state-key inventory and is this gate's membership authority: a
// root label rename or removal silently changes state keys, which is state
// loss, so membership changes must be deliberate. A legitimate pack change
// (a new generated type or data referent) regenerates it with
// IW_UPDATE_FIXTURES=1 (make regen-compatibility-fixtures); the counts that
// used to be pinned alongside it are derived instead -- all registry
// entries, topology roots, and resource-root mappings are the same
// root-bearing set by construction, which the structural assertions below
// prove per entry, so a hardcoded count only ever blocked legitimate pack
// evolution with a misleading message.
const qualificationBackendKeysSHA256 = "5e070d3da4320c0f4a1023fd438e0d62765f6ab3d0c896fda526bd227c0e78cc"

// TestFullProfileSingletonTopologyAndBackendKeys preserves the committed full
// distribution's backend-state-key inventory. It is a full-profile contract,
// so reduced distributions skip it when any selected pack is unavailable.
func TestFullProfileSingletonTopologyAndBackendKeys(t *testing.T) {
	root := qualificationRepoRoot(t)
	packsRoot := filepath.Join(root, "packs")
	profilePath := filepath.Join(packsRoot, "full.packset.json")
	if _, err := os.Stat(profilePath); err != nil {
		if os.IsNotExist(err) {
			t.Skip("committed full profile is not installed")
		}
		t.Fatalf("os.Stat(%q) = %v", profilePath, err)
	}
	profile, err := metadata.LoadPackSetDocument(profilePath, metadata.PackSetKind)
	if err != nil {
		t.Fatalf("LoadPackSetDocument(%q) = %v, want nil", profilePath, err)
	}
	requireQualificationPackSelectionAvailable(t, packsRoot, profile.PackSelection)

	loaded, err := metadata.LoadPackRoot(metadata.LoadPackRootOptions{
		PacksRoot:   packsRoot,
		ProfilePath: &profilePath,
	})
	if err != nil {
		t.Fatalf("LoadPackRoot(full profile) = %v, want nil", err)
	}
	if len(loaded.Resources) == 0 {
		t.Fatal("LoadPackRoot(full profile) loaded no resources")
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
	// Every loaded entry is asserted root-bearing below, so the registry,
	// the topology roots, and the resource-root mappings must all be the
	// same set; deriving the comparison keeps this true for any pack root
	// rather than one frozen size.
	if got := len(result.Topology.Roots); got != len(loaded.Resources) {
		t.Fatalf("LoadedRootTopology(full profile) roots = %d, want one per loaded root-bearing resource (%d)", got, len(loaded.Resources))
	}

	currentBackendKeys := make([]string, 0, len(result.Topology.Roots))
	labels := make([]string, 0, len(result.Topology.Roots))
	for resourceType, resource := range loaded.Resources {
		generated, _ := resource.Registry["generate"].(bool)
		dataReferent, _ := resource.Registry["data_referent"].(bool)
		if !generated && !dataReferent {
			t.Errorf("LoadPackRoot(full profile) resource %q is neither generated nor a data referent (generate = %v)", resourceType, resource.Registry["generate"])
		}
	}
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
		dataReferent, _ := resource.Registry["data_referent"].(bool)
		if root.Label != resourceType || (!generated && !dataReferent) {
			t.Errorf("LoadedRootTopology(full profile) root = {label:%q members:%v}, want label == member == a generated or data-referent resource type %q", root.Label, root.Members, resourceType)
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
	if got := len(result.Topology.ResourceRoots); got != len(loaded.Resources) {
		t.Errorf("LoadedRootTopology(full profile) resource_roots = %d, want one identity mapping per loaded resource (%d)", got, len(loaded.Resources))
	}
	for resourceType, label := range result.Topology.ResourceRoots {
		if resourceType != label {
			t.Errorf("LoadedRootTopology(full profile) resource_roots[%q] = %q, want identity mapping", resourceType, label)
		}
	}

	sort.Strings(currentBackendKeys)
	if fixtureupdate.Requested() {
		_, sourcePath, _, ok := runtime.Caller(0)
		if !ok {
			t.Fatal("runtime.Caller failed")
		}
		if err := fixtureupdate.ReplaceConst(sourcePath, "qualificationBackendKeysSHA256", qualificationKeyDigest(currentBackendKeys)); err != nil {
			t.Fatalf("fixtureupdate.ReplaceConst error: %v", err)
		}
		t.Skipf("backend-key inventory digest regenerated; review the diff before committing")
	}
	if got := qualificationKeyDigest(currentBackendKeys); got != qualificationBackendKeysSHA256 {
		t.Errorf("full-profile qualification backend-key digest = %s, want %s; a deliberate pack membership change regenerates this with IW_UPDATE_FIXTURES=1 (make regen-compatibility-fixtures)", got, qualificationBackendKeysSHA256)
	}
}

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
		t.Fatalf("LoadedRootTopology(synthetic pack) root count = %d, want selected root-bearing resource count %d", got, len(wantResourceTypes))
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

func qualificationRepoRoot(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller(0) = false, want true")
	}
	for directory := filepath.Dir(thisFile); ; directory = filepath.Dir(directory) {
		if _, err := os.Stat(filepath.Join(directory, "go.mod")); err == nil {
			return filepath.Dir(directory)
		}
		parent := filepath.Dir(directory)
		if parent == directory {
			t.Fatalf("qualificationRepoRoot(%q): reached filesystem root without go.mod", thisFile)
		}
	}
}

func requireQualificationPackSelectionAvailable(t *testing.T, packsRoot string, selection metadata.PackSelection) {
	t.Helper()
	missing := make([]string, 0)
	for _, pack := range selection.Packs {
		path := filepath.Join(packsRoot, pack)
		if _, err := os.Stat(path); err != nil {
			if os.IsNotExist(err) {
				missing = append(missing, pack)
				continue
			}
			t.Fatalf("os.Stat(%q) = %v", path, err)
		}
	}
	for _, shared := range selection.Shared {
		path := filepath.Join(packsRoot, "_shared", shared)
		if _, err := os.Stat(path); err != nil {
			if os.IsNotExist(err) {
				missing = append(missing, "_shared/"+shared)
				continue
			}
			t.Fatalf("os.Stat(%q) = %v", path, err)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		t.Skipf("full-profile contract requires unavailable pack paths: %v", missing)
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
