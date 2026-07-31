package envgen

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/dvmrry/infrawright-dev/go/internal/metadata"
)

func terraformTestAttribute(attributeType any, class string) metadata.JsonObject {
	attribute := metadata.JsonObject{"type": attributeType}
	attribute[class] = true
	return attribute
}

func terraformTestBlock(attributes metadata.JsonObject) metadata.JsonObject {
	return metadata.JsonObject{"block": metadata.JsonObject{"attributes": attributes}}
}

func terraformTestListBlock(attributes metadata.JsonObject) metadata.JsonObject {
	return metadata.JsonObject{
		"nesting_mode": "list",
		"block":        metadata.JsonObject{"attributes": attributes},
	}
}

func writeSyntheticTopologyPack(
	t *testing.T,
	root string,
	name string,
	manifest metadata.JsonObject,
	registry metadata.JsonObject,
	resourceSchemas metadata.JsonObject,
) {
	t.Helper()
	writeJSONFile(t, filepath.Join(root, name, "pack.json"), manifest)
	writeJSONFile(t, filepath.Join(root, name, "registry.json"), registry)
	writeJSONFile(t, filepath.Join(root, name, "schemas", "provider", name+".json"), metadata.JsonObject{
		"resource_schemas": resourceSchemas,
	})
}

// defaultSyntheticZpaReferences is the zpa pack's reference metadata every
// existing envgen fixture assumes. It is a function rather than a package
// var so each call gets a fresh map: writeSyntheticTopologyPack serialises
// it, and a test that edits the result must not perturb the next test.
func defaultSyntheticZpaReferences() metadata.JsonObject {
	return metadata.JsonObject{
		"zpa_application_segment": metadata.JsonObject{
			"segment_group_id": metadata.JsonObject{"name_field": "name", "referent": "zpa_segment_group"},
			"server_groups.id": metadata.JsonObject{"name_field": "name", "referent": "zpa_server_group"},
		},
		"zpa_server_group": metadata.JsonObject{
			"app_connector_groups.id": metadata.JsonObject{"name_field": "name", "referent": "zpa_app_connector_group"},
			"servers.id":              metadata.JsonObject{"name_field": "name", "referent": "zpa_application_server"},
		},
	}
}

// syntheticRootForTopology supplies the smallest pack universe that retains
// the cross-provider and nested-reference shapes exercised by envgen. The
// resource labels intentionally match the legacy fixtures so those tests keep
// proving their original behavior without reading any committed pack.
func syntheticRootForTopology(t *testing.T) metadata.LoadedPackRoot {
	t.Helper()
	return syntheticRootWithZpaReferences(t, defaultSyntheticZpaReferences())
}

// syntheticRootWithZpaReferences is syntheticRootForTopology with the zpa
// pack's `references` block supplied by the caller, so a test can express a
// pack VERSION that no longer declares an edge an older transform already
// committed tokens for -- valid pack evolution, and the shape the
// dropped-edge orphan gate exists to catch.
func syntheticRootWithZpaReferences(t *testing.T, zpaReferences metadata.JsonObject) metadata.LoadedPackRoot {
	t.Helper()
	packsRoot := t.TempDir()

	optionalString := func() metadata.JsonObject { return terraformTestAttribute("string", "optional") }
	optionalBool := func() metadata.JsonObject { return terraformTestAttribute("bool", "optional") }
	computedString := func() metadata.JsonObject { return terraformTestAttribute("string", "computed") }
	optionalStrings := func() metadata.JsonObject {
		return terraformTestAttribute([]any{"set", "string"}, "optional")
	}
	nestedIDs := func() metadata.JsonObject {
		return terraformTestListBlock(metadata.JsonObject{
			"id": terraformTestAttribute([]any{"set", "string"}, "required"),
		})
	}
	baseAttributes := func() metadata.JsonObject {
		return metadata.JsonObject{
			"description": optionalString(),
			"enabled":     optionalBool(),
			"id":          computedString(),
			"name":        optionalString(),
		}
	}

	zpaSchemas := metadata.JsonObject{
		"zpa_app_connector_group": terraformTestBlock(baseAttributes()),
		"zpa_application_server":  terraformTestBlock(baseAttributes()),
		"zpa_segment_group":       terraformTestBlock(baseAttributes()),
		"zpa_application_segment": metadata.JsonObject{"block": metadata.JsonObject{
			"attributes": metadata.JsonObject{
				"description":      optionalString(),
				"id":               computedString(),
				"segment_group_id": optionalString(),
			},
			"block_types": metadata.JsonObject{"server_groups": nestedIDs()},
		}},
		"zpa_server_group": metadata.JsonObject{"block": metadata.JsonObject{
			"attributes": baseAttributes(),
			"block_types": metadata.JsonObject{
				"app_connector_groups": nestedIDs(),
				"servers":              nestedIDs(),
			},
		}},
	}
	zpaRegistry := metadata.JsonObject{}
	for resourceType := range zpaSchemas {
		zpaRegistry[resourceType] = metadata.JsonObject{"generate": true, "product": "zpa"}
	}
	writeSyntheticTopologyPack(t, packsRoot, "zpa", metadata.JsonObject{
		"pin":               "1.0.0",
		"provider_prefixes": metadata.JsonObject{"zpa_": "zpa"},
		"provider_sources":  metadata.JsonObject{"zpa": "zscaler/zpa"},
		"references":        zpaReferences,
	}, zpaRegistry, zpaSchemas)

	ziaSchemas := metadata.JsonObject{
		"zia_url_categories": metadata.JsonObject{"block": metadata.JsonObject{"attributes": metadata.JsonObject{
			"configured_name": optionalString(),
			"custom_category": optionalBool(),
			"id":              computedString(),
			"urls":            optionalStrings(),
		}}},
		"zia_url_filtering_rules": metadata.JsonObject{"block": metadata.JsonObject{
			"attributes": metadata.JsonObject{
				"id":             computedString(),
				"url_categories": optionalStrings(),
			},
			"block_types": metadata.JsonObject{
				"cbi_profile": terraformTestListBlock(metadata.JsonObject{"id": optionalString()}),
			},
		}},
	}
	ziaRegistry := metadata.JsonObject{}
	for resourceType := range ziaSchemas {
		ziaRegistry[resourceType] = metadata.JsonObject{"generate": true, "product": "zia"}
	}
	writeSyntheticTopologyPack(t, packsRoot, "zia", metadata.JsonObject{
		"pin":               "1.0.0",
		"provider_prefixes": metadata.JsonObject{"zia_": "zia"},
		"provider_sources":  metadata.JsonObject{"zia": "zscaler/zia"},
		"references": metadata.JsonObject{
			"zia_url_filtering_rules": metadata.JsonObject{
				"url_categories": metadata.JsonObject{"name_field": "configured_name", "referent": "zia_url_categories"},
			},
		},
	}, ziaRegistry, ziaSchemas)

	zccSchemas := metadata.JsonObject{
		"zcc_failopen_policy": terraformTestBlock(metadata.JsonObject{"id": computedString(), "name": optionalString()}),
		"zcc_forwarding_profile": terraformTestBlock(metadata.JsonObject{
			"id":                           computedString(),
			"name":                         optionalString(),
			"trusted_network_ids":          optionalStrings(),
			"trusted_network_ids_selected": optionalStrings(),
		}),
		"zcc_trusted_network": terraformTestBlock(metadata.JsonObject{
			"id":           computedString(),
			"network_name": optionalString(),
		}),
	}
	zccRegistry := metadata.JsonObject{}
	for resourceType := range zccSchemas {
		zccRegistry[resourceType] = metadata.JsonObject{"generate": true, "product": "zcc"}
	}
	writeSyntheticTopologyPack(t, packsRoot, "zcc", metadata.JsonObject{
		"pin":               "1.0.0",
		"provider_prefixes": metadata.JsonObject{"zcc_": "zcc"},
		"provider_sources":  metadata.JsonObject{"zcc": "zscaler/zcc"},
		"references": metadata.JsonObject{
			"zcc_forwarding_profile": metadata.JsonObject{
				"trusted_network_ids":          metadata.JsonObject{"name_field": "network_name", "referent": "zcc_trusted_network"},
				"trusted_network_ids_selected": metadata.JsonObject{"name_field": "network_name", "referent": "zcc_trusted_network"},
			},
		},
	}, zccRegistry, zccSchemas)

	profilePath := filepath.Join(packsRoot, "synthetic.packset.json")
	writeJSONFile(t, profilePath, metadata.JsonObject{
		"kind": metadata.PackSetKind, "version": 1,
		"packs": []any{"zcc", "zia", "zpa"}, "shared": []any{},
	})
	loaded, err := metadata.LoadPackRoot(metadata.LoadPackRootOptions{
		PacksRoot: packsRoot, ProfilePath: &profilePath,
	})
	if err != nil {
		t.Fatalf("LoadPackRoot(synthetic envgen fixture) error: %v", err)
	}
	return loaded
}

func requireTopologyPackSelection(t *testing.T, selection metadata.PackSelection) {
	t.Helper()
	packsRoot := filepath.Join(repoRoot(t), "packs")
	missing := make([]string, 0)
	for _, pack := range selection.Packs {
		if _, err := os.Stat(filepath.Join(packsRoot, pack)); err != nil {
			if os.IsNotExist(err) {
				missing = append(missing, pack)
				continue
			}
			t.Fatalf("os.Stat(pack %q) error: %v", pack, err)
		}
	}
	for _, shared := range selection.Shared {
		if _, err := os.Stat(filepath.Join(packsRoot, "_shared", shared)); err != nil {
			if os.IsNotExist(err) {
				missing = append(missing, "_shared/"+shared)
				continue
			}
			t.Fatalf("os.Stat(shared %q) error: %v", shared, err)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		t.Skipf("required pack paths are not installed: %v", missing)
	}
}

func topologyPackSelectionFromProfile(t *testing.T, profilePath string) metadata.PackSelection {
	t.Helper()
	data, err := os.ReadFile(profilePath)
	if err != nil {
		t.Fatalf("os.ReadFile(%q) error: %v", profilePath, err)
	}
	var document metadata.PackSetDocument
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatalf("json.Unmarshal(%q) error: %v", profilePath, err)
	}
	return document.PackSelection
}

func committedTopologyRoot(t *testing.T, profile string, selection metadata.PackSelection) metadata.LoadedPackRoot {
	t.Helper()
	requireTopologyPackSelection(t, selection)
	repo := repoRoot(t)
	selectedRoot := t.TempDir()
	for _, pack := range selection.Packs {
		copyDirRecursive(t, filepath.Join(repo, "packs", pack), filepath.Join(selectedRoot, pack))
	}
	for _, shared := range selection.Shared {
		copyDirRecursive(
			t,
			filepath.Join(repo, "packs", "_shared", shared),
			filepath.Join(selectedRoot, "_shared", shared),
		)
	}
	profilePath := filepath.Join(repo, "packs", profile+".packset.json")
	loaded, err := metadata.LoadPackRoot(metadata.LoadPackRootOptions{
		PacksRoot: selectedRoot, ProfilePath: &profilePath,
	})
	if err != nil {
		t.Fatalf("LoadPackRoot(%s) error: %v", profile, err)
	}
	return loaded
}

func installedTopologyRoot(t *testing.T) metadata.LoadedPackRoot {
	t.Helper()
	repo := repoRoot(t)
	packsRoot := filepath.Join(repo, "packs")
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
	sharedEntries, err := os.ReadDir(filepath.Join(packsRoot, "_shared"))
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("os.ReadDir(_shared) error: %v", err)
	}
	for _, entry := range sharedEntries {
		if entry.IsDir() {
			shared = append(shared, entry.Name())
		}
	}
	profilePath := filepath.Join(t.TempDir(), "installed.packset.json")
	writeJSONFile(t, profilePath, metadata.JsonObject{
		"kind": metadata.PackSetKind, "version": 1, "packs": packs, "shared": shared,
	})
	loaded, err := metadata.LoadPackRoot(metadata.LoadPackRootOptions{
		PacksRoot: packsRoot, ProfilePath: &profilePath,
	})
	if err != nil {
		t.Fatalf("LoadPackRoot(installed envgen packs) error: %v", err)
	}
	return loaded
}
