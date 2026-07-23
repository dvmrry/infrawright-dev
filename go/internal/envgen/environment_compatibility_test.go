package envgen

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/dvmrry/infrawright-dev/go/internal/modulesgen"
)

const environmentRootsCompatibilitySHA256 = "5672e8f8de2154c6022fd96a14939f1f18c5faa5c9cada63d5c6cfa2f9de0067"

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
	if fixture.FullProfile.FileCount != 453 || len(fixture.FullProfile.Manifest) != 453 {
		t.Fatalf("%s full-profile file/manifest counts = %d/%d, want 453/453", fixturePath, fixture.FullProfile.FileCount, len(fixture.FullProfile.Manifest))
	}
	return fixture
}

func TestUngroupedEnvironmentRootCompatibility(t *testing.T) {
	fixture := loadEnvironmentRootsCompatibility(t)
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
		Root: committedRootForTopology(t), Selectors: []string{"zia_url_categories"}, Tenant: "tenant",
	}); err != nil {
		t.Fatalf("GenerateEnvironmentRoots() error: %v", err)
	}
	got := snapshotTree(t, outputRoot)
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

func TestFullProfileEnvironmentRootCompatibility(t *testing.T) {
	fixture := loadEnvironmentRootsCompatibility(t)
	workspace := temporaryDirectory(t, "infrawright-gen-env-full-compatibility-")
	deploymentPath := filepath.Join(workspace, "deployment.json")
	writeJSONFile(t, deploymentPath, map[string]any{
		"overlay": workspace, "module_dir": filepath.Join(workspace, "modules"), "roots": map[string]any{},
	})
	deployment := loadDeploymentFile(t, deploymentPath)
	outputRoot := filepath.Join(workspace, "generated")
	formatter := modulesgen.NewHCLFormatter()
	result, err := GenerateEnvironmentRoots(GenerateEnvironmentRootsOptions{
		Deployment: deployment, FormatHcl: formatter.FormatHCL, OutputRoot: &outputRoot,
		Root: committedRootForTopology(t), Selectors: []string{}, Tenant: "full-profile-parity",
	})
	if err != nil {
		t.Fatalf("GenerateEnvironmentRoots() error: %v", err)
	}
	if got, want := len(result.Roots), 151; got != want {
		t.Fatalf("generated roots = %d, want %d", got, want)
	}
	tree := snapshotTree(t, outputRoot)
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
