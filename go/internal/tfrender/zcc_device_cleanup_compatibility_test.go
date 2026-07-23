package tfrender

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/dvmrry/infrawright-dev/go/internal/canonjson"
	"github.com/dvmrry/infrawright-dev/go/internal/metadata"
	transformkernel "github.com/dvmrry/infrawright-dev/go/internal/transform"
)

func TestZCCDeviceCleanupTransformCompatibility(t *testing.T) {
	repositoryRoot, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatalf("filepath.Abs(repository root) error: %v", err)
	}
	profilePath := filepath.Join(repositoryRoot, "packs", "full.packset.json")
	loaded, err := metadata.LoadPackRoot(metadata.LoadPackRootOptions{
		PacksRoot: filepath.Join(repositoryRoot, "packs"), ProfilePath: &profilePath,
	})
	if err != nil {
		t.Fatalf("metadata.LoadPackRoot() error: %v", err)
	}
	const resourceType = "zcc_device_cleanup"
	resource, ok := loaded.Resources[resourceType]
	if !ok {
		t.Fatalf("active pack root has no %s resource", resourceType)
	}
	schema, err := loaded.LoadResourceSchema(resourceType)
	if err != nil {
		t.Fatalf("LoadResourceSchema(%q) error: %v", resourceType, err)
	}

	fixtureRoot := filepath.Join("testdata", resourceType)
	apiBytes := readZCCDeviceCleanupFixture(t, fixtureRoot, "api.json")
	decoded, err := canonjson.Decode(apiBytes)
	if err != nil {
		t.Fatalf("canonjson.Decode(api.json) error: %v", err)
	}
	rawItems, ok := decoded.([]any)
	if !ok {
		t.Fatalf("api.json = %T, want array", decoded)
	}
	result, err := transformkernel.TransformLoadedItems(transformkernel.TransformLoadedItemsOptions{
		Resource: resource, Schema: schema, RawItems: rawItems,
	})
	if err != nil {
		t.Fatalf("TransformLoadedItems(%q) error: %v", resourceType, err)
	}
	if len(result.Drops) != 0 {
		t.Fatalf("TransformLoadedItems(%q) drops = %#v, want none", resourceType, result.Drops)
	}

	workspace := t.TempDir()
	options := newArtifactOptions(workspace, resourceType)
	options.LookupNameField = nil
	options.Override = resource.Override
	options.Result = PullTransformResult{Items: result.Items, Originals: result.Originals, Drops: result.Drops}
	written, err := WriteTransformArtifacts(options)
	if err != nil {
		t.Fatalf("WriteTransformArtifacts(%q) error: %v", resourceType, err)
	}
	configBytes, err := os.ReadFile(written.Paths.Config)
	if err != nil {
		t.Fatalf("os.ReadFile(config) error: %v", err)
	}
	if want := readZCCDeviceCleanupFixture(t, fixtureRoot, "expected.auto.tfvars.json"); string(configBytes) != string(want) {
		t.Errorf("%s config bytes = %q, want fixed %q", resourceType, configBytes, want)
	}
	importsBytes, err := os.ReadFile(written.Paths.Imports)
	if err != nil {
		t.Fatalf("os.ReadFile(imports) error: %v", err)
	}
	if want := readZCCDeviceCleanupFixture(t, fixtureRoot, "expected_imports.tf"); string(importsBytes) != string(want) {
		t.Errorf("%s import bytes = %q, want fixed %q", resourceType, importsBytes, want)
	}
}

func TestDetailedTransformCorpusImportCompatibility(t *testing.T) {
	repositoryRoot, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatalf("filepath.Abs(repository root) error: %v", err)
	}
	profilePath := filepath.Join(repositoryRoot, "packs", "full.packset.json")
	loaded, err := metadata.LoadPackRoot(metadata.LoadPackRootOptions{
		PacksRoot: filepath.Join(repositoryRoot, "packs"), ProfilePath: &profilePath,
	})
	if err != nil {
		t.Fatalf("metadata.LoadPackRoot() error: %v", err)
	}
	fixtureRoot := filepath.Join(repositoryRoot, "tests", "fixtures", "transform")
	entries, err := os.ReadDir(fixtureRoot)
	if err != nil {
		t.Fatalf("os.ReadDir(%q) error: %v", fixtureRoot, err)
	}
	covered := 0
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		resourceType := entry.Name()
		apiPath := filepath.Join(fixtureRoot, resourceType, "api.json")
		expectedImportsPath := filepath.Join(fixtureRoot, resourceType, "expected_imports.tf")
		if _, err := os.Stat(expectedImportsPath); err != nil {
			continue
		}
		covered++
		t.Run(resourceType, func(t *testing.T) {
			resource, ok := loaded.Resources[resourceType]
			if !ok {
				t.Fatalf("active pack root has no %s resource", resourceType)
			}
			schema, err := loaded.LoadResourceSchema(resourceType)
			if err != nil {
				t.Fatalf("LoadResourceSchema(%q) error: %v", resourceType, err)
			}
			apiBytes, err := os.ReadFile(apiPath)
			if err != nil {
				t.Fatalf("os.ReadFile(%q) error: %v", apiPath, err)
			}
			decoded, err := canonjson.Decode(apiBytes)
			if err != nil {
				t.Fatalf("canonjson.Decode(%q) error: %v", apiPath, err)
			}
			rawItems, ok := decoded.([]any)
			if !ok {
				t.Fatalf("%s = %T, want array", apiPath, decoded)
			}
			result, err := transformkernel.TransformLoadedItems(transformkernel.TransformLoadedItemsOptions{
				Resource: resource, Schema: schema, RawItems: rawItems,
			})
			if err != nil {
				t.Fatalf("TransformLoadedItems(%q) error: %v", resourceType, err)
			}
			options := newArtifactOptions(t.TempDir(), resourceType)
			options.LookupNameField = nil
			options.Override = resource.Override
			options.Result = PullTransformResult{Items: result.Items, Originals: result.Originals, Drops: result.Drops}
			written, err := WriteTransformArtifacts(options)
			if err != nil {
				t.Fatalf("WriteTransformArtifacts(%q) error: %v", resourceType, err)
			}
			got, err := os.ReadFile(written.Paths.Imports)
			if err != nil {
				t.Fatalf("os.ReadFile(imports) error: %v", err)
			}
			want, err := os.ReadFile(expectedImportsPath)
			if err != nil {
				t.Fatalf("os.ReadFile(%q) error: %v", expectedImportsPath, err)
			}
			if string(got) != string(want) {
				t.Errorf("%s imports = %q, want fixed %q", resourceType, got, want)
			}
		})
	}
	if covered != 7 {
		t.Fatalf("detailed transform import fixtures covered = %d, want 7", covered)
	}
}

func readZCCDeviceCleanupFixture(t *testing.T, root, name string) []byte {
	t.Helper()
	path := filepath.Join(root, name)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("os.ReadFile(%q) error: %v", path, err)
	}
	return data
}
