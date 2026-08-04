package envgen

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"testing"

	"github.com/dvmrry/infrawright-dev/go/internal/fixtureupdate"
	"github.com/dvmrry/infrawright-dev/go/internal/metadata"
	"github.com/dvmrry/infrawright-dev/go/internal/modulesgen"
	"github.com/dvmrry/infrawright-dev/go/internal/roots"
)

const environmentRootsCompatibilitySHA256 = "a64aff9370e360786347fe6c78ff402f9838820fe0ec04512ec54774c9903d5d"

type environmentRootsCompatibilityFixture struct {
	SchemaVersion       int                                  `json:"schema_version"`
	RepresentativeCases []environmentRootsRepresentativeCase `json:"representative_cases"`
	FullProfile         struct {
		FileCount int                                 `json:"file_count"`
		Manifest  []environmentRootsCompatibilityFile `json:"manifest"`
	} `json:"full_profile"`
}

type environmentRootsRepresentativeCase struct {
	Name string            `json:"name"`
	Tree map[string]string `json:"tree"`
}

type environmentRootsCompatibilityFile struct {
	Path   string `json:"path"`
	Length int    `json:"length"`
	SHA256 string `json:"sha256"`
}

func loadEnvironmentRootsCompatibility(t *testing.T) environmentRootsCompatibilityFixture {
	t.Helper()
	fixturePath := filepath.Join("testdata", "environment_roots_compatibility.json")
	fixtureBytes, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatalf("os.ReadFile(%q) error: %v", fixturePath, err)
	}
	digest := sha256.Sum256(fixtureBytes)
	if got := hex.EncodeToString(digest[:]); got != environmentRootsCompatibilitySHA256 {
		t.Fatalf("SHA256(%q) = %q, want %q", fixturePath, got, environmentRootsCompatibilitySHA256)
	}
	var fixture environmentRootsCompatibilityFixture
	if err := json.Unmarshal(fixtureBytes, &fixture); err != nil {
		t.Fatalf("json.Unmarshal(%q) error: %v", fixturePath, err)
	}
	if fixture.SchemaVersion != 1 {
		t.Fatalf("%s schema_version = %d, want 1", fixturePath, fixture.SchemaVersion)
	}
	if len(fixture.RepresentativeCases) != 1 || fixture.RepresentativeCases[0].Name != "ungrouped-json" {
		t.Fatalf("%s representative cases = %#v, want only ungrouped-json", fixturePath, fixture.RepresentativeCases)
	}
	// The pinned fixture digest above is the membership authority; the counts
	// only need to agree with each other and be non-empty.
	if fixture.FullProfile.FileCount == 0 || fixture.FullProfile.FileCount != len(fixture.FullProfile.Manifest) {
		t.Fatalf("%s full-profile file/manifest counts = %d/%d, want equal and non-zero", fixturePath, fixture.FullProfile.FileCount, len(fixture.FullProfile.Manifest))
	}
	return fixture
}

// updateEnvironmentRootsCompatibility is the IW_UPDATE_FIXTURES=1 refresh
// path: it re-runs both compatibility generations and rewrites the snapshot
// plus its pinned constant. The manifest here IS the generated tree, so
// unlike the curated snapshots there is no hand-maintained membership;
// review the diff before committing.
func updateEnvironmentRootsCompatibility(t *testing.T, fixturePath string) {
	t.Helper()
	fixture := environmentRootsCompatibilityFixture{SchemaVersion: 1}
	fixture.RepresentativeCases = []environmentRootsRepresentativeCase{
		{Name: "ungrouped-json", Tree: generateUngroupedCompatibilityTree(t)},
	}
	tree := generateFullProfileCompatibilityTree(t)
	paths := make([]string, 0, len(tree))
	for path := range tree {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	fixture.FullProfile.FileCount = len(paths)
	fixture.FullProfile.Manifest = make([]environmentRootsCompatibilityFile, 0, len(paths))
	for _, path := range paths {
		digest := sha256.Sum256([]byte(tree[path]))
		fixture.FullProfile.Manifest = append(fixture.FullProfile.Manifest, environmentRootsCompatibilityFile{
			Path: path, Length: len(tree[path]), SHA256: hex.EncodeToString(digest[:]),
		})
	}
	encoded, err := json.MarshalIndent(fixture, "", "  ")
	if err != nil {
		t.Fatalf("json.MarshalIndent(environment roots compatibility) error: %v", err)
	}
	encoded = append(encoded, '\n')
	if err := os.WriteFile(fixturePath, encoded, 0o644); err != nil {
		t.Fatalf("os.WriteFile(%q) error: %v", fixturePath, err)
	}
	digest := sha256.Sum256(encoded)
	_, sourcePath, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	if err := fixtureupdate.ReplaceConst(sourcePath, "environmentRootsCompatibilitySHA256", hex.EncodeToString(digest[:])); err != nil {
		t.Fatalf("fixtureupdate.ReplaceConst error: %v", err)
	}
	t.Skipf("environment roots compatibility snapshot regenerated; review the diff before committing")
}

func TestEnvironmentRootsCompatibilityUpdateMode(t *testing.T) {
	if !fixtureupdate.Requested() {
		t.Skip("set IW_UPDATE_FIXTURES=1 to regenerate the environment roots compatibility snapshot")
	}
	updateEnvironmentRootsCompatibility(t, filepath.Join("testdata", "environment_roots_compatibility.json"))
}

func generateUngroupedCompatibilityTree(t *testing.T) map[string]string {
	t.Helper()
	workspace := temporaryDirectory(t, "infrawright-gen-env-compatibility-")
	deploymentPath := filepath.Join(workspace, "deployment.json")
	writeJSONFile(t, deploymentPath, map[string]any{
		"overlay": workspace, "module_dir": filepath.Join(workspace, "modules"), "roots": map[string]any{},
	})
	writeJSONFile(t, filepath.Join(workspace, "config", "tenant", "zia_url_categories.auto.tfvars.json"), map[string]any{
		"items": map[string]any{"example": map[string]any{"configured_name": "Example", "custom_category": true, "urls": []any{}}},
	})
	deployment := loadDeploymentFile(t, deploymentPath)
	outputRoot := filepath.Join(workspace, "generated")
	formatter := modulesgen.NewHCLFormatter()
	if _, err := GenerateEnvironmentRoots(GenerateEnvironmentRootsOptions{
		Deployment: deployment, FormatHcl: formatter.FormatHCL, OutputRoot: &outputRoot,
		Root:      committedTopologyRoot(t, "zia", metadata.PackSelection{Packs: []string{"zia"}, Shared: []string{"zscaler"}}),
		Selectors: []string{"zia_url_categories"}, Tenant: "tenant",
	}); err != nil {
		t.Fatalf("GenerateEnvironmentRoots() error: %v", err)
	}
	return snapshotTree(t, outputRoot)
}

func TestUngroupedEnvironmentRootCompatibility(t *testing.T) {
	fixture := loadEnvironmentRootsCompatibility(t)
	got := generateUngroupedCompatibilityTree(t)
	want := fixture.RepresentativeCases[0].Tree
	if !reflect.DeepEqual(got, want) {
		for path, expected := range want {
			if actual, ok := got[path]; !ok || actual != expected {
				t.Errorf("generated environment root differs at %s", path)
			}
		}
		for path := range got {
			if _, ok := want[path]; !ok {
				t.Errorf("generated environment root has unexpected path %s", path)
			}
		}
	}
}

// generateFullProfileCompatibilityTree runs the full-profile generation and
// asserts the topology-label invariants shared by the comparing test and the
// update mode, returning the generated tree.
func generateFullProfileCompatibilityTree(t *testing.T) map[string]string {
	t.Helper()
	workspace := temporaryDirectory(t, "infrawright-gen-env-full-compatibility-")
	deploymentPath := filepath.Join(workspace, "deployment.json")
	writeJSONFile(t, deploymentPath, map[string]any{
		"overlay": workspace, "module_dir": filepath.Join(workspace, "modules"), "roots": map[string]any{},
	})
	deployment := loadDeploymentFile(t, deploymentPath)
	outputRoot := filepath.Join(workspace, "generated")
	formatter := modulesgen.NewHCLFormatter()
	root := committedTopologyRoot(t, "full", metadata.PackSelection{
		Packs:  []string{"aws", "cloudflare", "google", "netbox", "zcc", "zia", "zpa", "ztc"},
		Shared: []string{"zscaler"},
	})
	result, err := GenerateEnvironmentRoots(GenerateEnvironmentRootsOptions{
		Deployment: deployment, FormatHcl: formatter.FormatHCL, OutputRoot: &outputRoot,
		Root: root, Selectors: []string{}, Tenant: "full-profile-parity",
	})
	if err != nil {
		t.Fatalf("GenerateEnvironmentRoots() error: %v", err)
	}
	topologyResult, err := roots.LoadedRootTopology(roots.LoadedRootTopologyOptions{
		Deployment: deployment, Root: root, Selectors: []string{}, Tenant: strPtr("full-profile-parity"),
	})
	if err != nil {
		t.Fatalf("LoadedRootTopology() error: %v", err)
	}
	wantLabels := make([]string, len(topologyResult.Topology.Roots))
	for index, topologyRoot := range topologyResult.Topology.Roots {
		wantLabels[index] = topologyRoot.Label
	}
	sort.Strings(wantLabels)
	gotLabels := make([]string, len(result.Roots))
	for index, generatedRoot := range result.Roots {
		gotLabels[index] = generatedRoot.Label
	}
	sort.Strings(gotLabels)
	if !reflect.DeepEqual(gotLabels, wantLabels) {
		t.Fatalf("generated root labels = %v, want loaded topology root labels %v", gotLabels, wantLabels)
	}
	return snapshotTree(t, outputRoot)
}

func TestFullProfileEnvironmentRootCompatibility(t *testing.T) {
	fixture := loadEnvironmentRootsCompatibility(t)
	tree := generateFullProfileCompatibilityTree(t)
	if got := len(tree); got != fixture.FullProfile.FileCount {
		t.Fatalf("generated files = %d, want %d", got, fixture.FullProfile.FileCount)
	}
	seen := map[string]bool{}
	for _, expected := range fixture.FullProfile.Manifest {
		if seen[expected.Path] {
			t.Fatalf("duplicate compatibility path %q", expected.Path)
		}
		seen[expected.Path] = true
		actual, ok := tree[expected.Path]
		if !ok {
			t.Errorf("generated environment root omitted %s", expected.Path)
			continue
		}
		digest := sha256.Sum256([]byte(actual))
		actualSHA256 := hex.EncodeToString(digest[:])
		if len(actual) != expected.Length || actualSHA256 != expected.SHA256 {
			t.Errorf("generated %s length/SHA256 = %d/%s, want %d/%s", expected.Path, len(actual), actualSHA256, expected.Length, expected.SHA256)
		}
	}
	for path := range tree {
		if !seen[path] {
			t.Errorf("generated environment root has unexpected path %s", path)
		}
	}
}
