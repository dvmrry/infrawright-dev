package tfrender

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dvmrry/infrawright-dev/go/internal/canonjson"
	"github.com/dvmrry/infrawright-dev/go/internal/metadata"
	transformkernel "github.com/dvmrry/infrawright-dev/go/internal/transform"
)

func tfrenderRepositoryRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatalf("filepath.Abs(repository root) error: %v", err)
	}
	return root
}

func requireTfrenderPack(t *testing.T, pack string) {
	t.Helper()
	path := filepath.Join(tfrenderRepositoryRoot(t), "packs", pack)
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			t.Skipf("%s pack is not installed", pack)
		}
		t.Fatalf("os.Stat(%q) error: %v", path, err)
	}
}

func installedTfrenderRoot(t *testing.T) metadata.LoadedPackRoot {
	t.Helper()
	packsRoot := filepath.Join(tfrenderRepositoryRoot(t), "packs")
	entries, err := os.ReadDir(packsRoot)
	if err != nil {
		t.Fatalf("os.ReadDir(%q) error: %v", packsRoot, err)
	}
	packs := make([]any, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() && entry.Name() != "_shared" {
			packs = append(packs, entry.Name())
		}
	}
	shared := []any{}
	sharedRoot := filepath.Join(packsRoot, "_shared")
	sharedEntries, err := os.ReadDir(sharedRoot)
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("os.ReadDir(%q) error: %v", sharedRoot, err)
	}
	for _, entry := range sharedEntries {
		if entry.IsDir() {
			shared = append(shared, entry.Name())
		}
	}
	profile := filepath.Join(t.TempDir(), "installed.packset.json")
	data, err := json.Marshal(metadata.JsonObject{
		"kind": metadata.PackSetKind, "version": 1, "packs": packs, "shared": shared,
	})
	if err != nil {
		t.Fatalf("json.Marshal(installed pack set) error: %v", err)
	}
	if err := os.WriteFile(profile, append(data, '\n'), 0o600); err != nil {
		t.Fatalf("os.WriteFile(%q) error: %v", profile, err)
	}
	loaded, err := metadata.LoadPackRoot(metadata.LoadPackRootOptions{PacksRoot: packsRoot, ProfilePath: &profile})
	if err != nil {
		t.Fatalf("metadata.LoadPackRoot(installed packs) error: %v", err)
	}
	return loaded
}

func TestZCCDeviceCleanupTransformCompatibility(t *testing.T) {
	requireTfrenderPack(t, "zcc")
	loaded := installedTfrenderRoot(t)
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
	repositoryRoot := tfrenderRepositoryRoot(t)
	loaded := installedTfrenderRoot(t)
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
			pack, _, _ := strings.Cut(resourceType, "_")
			requireTfrenderPack(t, pack)
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
