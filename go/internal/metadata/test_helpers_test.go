package metadata

import (
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"testing"
)

func repoRoot(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller(0) failed")
	}
	for directory := filepath.Dir(thisFile); ; directory = filepath.Dir(directory) {
		marker := filepath.Join(directory, "packs", "full.packset.json")
		if info, err := os.Stat(marker); err == nil && info.Mode().IsRegular() {
			return directory
		}
		parent := filepath.Dir(directory)
		if parent == directory {
			t.Fatalf("unable to find packs/full.packset.json above %s", thisFile)
		}
	}
}

func requirePackSelectionAvailable(t *testing.T, packsRoot string, selection PackSelection) {
	t.Helper()
	missing := make([]string, 0)
	for _, pack := range selection.Packs {
		path := filepath.Join(packsRoot, pack)
		if _, err := os.Stat(path); err != nil {
			if os.IsNotExist(err) {
				missing = append(missing, pack)
				continue
			}
			t.Fatalf("os.Stat(%q) error: %v", path, err)
		}
	}
	for _, shared := range selection.Shared {
		path := filepath.Join(packsRoot, "_shared", shared)
		if _, err := os.Stat(path); err != nil {
			if os.IsNotExist(err) {
				missing = append(missing, "_shared/"+shared)
				continue
			}
			t.Fatalf("os.Stat(%q) error: %v", path, err)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		t.Skipf("required pack paths are not installed: %v", missing)
	}
}

func syntheticLoadedPackRoot(t *testing.T, providers ...string) (string, LoadedPackRoot) {
	t.Helper()
	providers = append([]string(nil), providers...)
	sort.Strings(providers)
	directory := t.TempDir()
	for _, provider := range providers {
		resourceType := provider + "_resource"
		writeJSONFile(t, filepath.Join(directory, provider, "pack.json"), JsonObject{
			"pin":               "1.2.3",
			"provider_prefixes": JsonObject{provider + "_": provider},
			"provider_sources":  JsonObject{provider: "example/" + provider},
		})
		writeJSONFile(t, filepath.Join(directory, provider, "registry.json"), JsonObject{
			resourceType: JsonObject{"generate": true, "product": provider},
		})
		writeJSONFile(t, filepath.Join(directory, provider, "schemas", "provider", provider+".json"), JsonObject{
			"resource_schemas": JsonObject{
				resourceType: JsonObject{"block": JsonObject{"attributes": JsonObject{
					"id":   JsonObject{"computed": true, "optional": true, "type": "string"},
					"name": JsonObject{"required": true, "type": "string"},
				}}},
			},
		})
	}
	profile := filepath.Join(directory, "profile.json")
	writeJSONFile(t, profile, JsonObject{
		"kind": PackSetKind, "version": 1, "packs": providers, "shared": []string{},
	})
	loaded, err := LoadPackRoot(LoadPackRootOptions{PacksRoot: directory, ProfilePath: &profile})
	if err != nil {
		t.Fatalf("LoadPackRoot(synthetic providers %v) error: %v", providers, err)
	}
	return directory, loaded
}
