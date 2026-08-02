package transformrun_test

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/dvmrry/infrawright-dev/go/internal/adopt"
	"github.com/dvmrry/infrawright-dev/go/internal/deployment"
	"github.com/dvmrry/infrawright-dev/go/internal/metadata"
	"github.com/dvmrry/infrawright-dev/go/internal/procerr"
	"github.com/dvmrry/infrawright-dev/go/internal/tfrender"
	"github.com/dvmrry/infrawright-dev/go/internal/transformrun"
)

func writeExternalJSON(t *testing.T, file string, value any) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(file), 0o777); err != nil {
		t.Fatalf("os.MkdirAll(%q) = error %v", filepath.Dir(file), err)
	}
	encoded, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatalf("json.Marshal(%q) = error %v", file, err)
	}
	if err := os.WriteFile(file, append(encoded, '\n'), 0o666); err != nil {
		t.Fatalf("os.WriteFile(%q) = error %v", file, err)
	}
}

func externalDataReferentRoot(t *testing.T) metadata.LoadedPackRoot {
	t.Helper()
	packsRoot := t.TempDir()
	writeExternalJSON(t, filepath.Join(packsRoot, "sample", "pack.json"), metadata.JsonObject{
		"pin":               "1.0.0",
		"provider_prefixes": metadata.JsonObject{"sample_": "sample"},
		"provider_sources":  metadata.JsonObject{"sample": "example/sample"},
		"lookup_sources": metadata.JsonObject{
			"sample_groups_data": metadata.JsonObject{"name_field": "name"},
		},
	})
	writeExternalJSON(t, filepath.Join(packsRoot, "sample", "registry.json"), metadata.JsonObject{
		"sample_groups_data": metadata.JsonObject{
			"data_referent": true,
			"fetch": metadata.JsonObject{
				"pagination": "zia",
				"path":       "locations/groups",
			},
			"product": "sample",
		},
	})
	writeExternalJSON(t, filepath.Join(packsRoot, "sample", "schemas", "provider", "sample.json"), metadata.JsonObject{
		"resource_schemas": metadata.JsonObject{},
		"data_source_schemas": metadata.JsonObject{
			"sample_groups_data": metadata.JsonObject{"block": metadata.JsonObject{
				"attributes": metadata.JsonObject{
					"id":   metadata.JsonObject{"computed": true, "type": "string"},
					"name": metadata.JsonObject{"required": true, "type": "string"},
				},
			}},
		},
	})
	profilePath := filepath.Join(packsRoot, "sample.packset.json")
	writeExternalJSON(t, profilePath, metadata.JsonObject{
		"kind": metadata.PackSetKind, "version": 1,
		"packs": []any{"sample"}, "shared": []any{},
	})
	root, err := metadata.LoadPackRoot(metadata.LoadPackRootOptions{
		PacksRoot: packsRoot, ProfilePath: &profilePath,
	})
	if err != nil {
		t.Fatalf("LoadPackRoot(external data referent fixture) = error %v, want nil", err)
	}
	return root
}

func TestDataReferentTransformRetiresArtifactsBeforeImportStaging(t *testing.T) {
	workspace := t.TempDir()
	root := externalDataReferentRoot(t)
	dep := deployment.Deployment{
		Overlay: workspace,
		Roots:   map[string]deployment.RootProviderConfig{"sample": {HasCrossStateReferences: true, CrossStateReferences: true}},
	}
	pulls := filepath.Join(workspace, "pulls")
	writeExternalJSON(t, filepath.Join(pulls, "sample_groups_data.json"), []any{
		map[string]any{"id": json.Number("101"), "name": "Head Office"},
	})
	paths, err := tfrender.ComputeTransformArtifactPaths(dep, "sample_groups_data", "tenant")
	if err != nil {
		t.Fatalf("ComputeTransformArtifactPaths(data referent) = error %v, want nil", err)
	}
	allManaged := []string{
		paths.Config, paths.Lookup, paths.Imports, paths.Moves,
		paths.StaleConfig, paths.GeneratedBindings, paths.LegacyLookup,
	}
	for _, file := range allManaged {
		if err := os.MkdirAll(filepath.Dir(file), 0o777); err != nil {
			t.Fatalf("os.MkdirAll(%q) = error %v", filepath.Dir(file), err)
		}
		contents := "STALE-" + filepath.Base(file)
		if file == paths.Lookup {
			contents = `{"by_id":{"stale-id":"Stale"},"id_by_key":{"stale_key":"stale-id"},"key_by_id":{"stale-id":"stale_key"}}`
		}
		if err := os.WriteFile(file, []byte(contents), 0o666); err != nil {
			t.Fatalf("os.WriteFile(%q) = error %v", file, err)
		}
	}

	result, err := transformrun.RunTransformBatch(transformrun.RunTransformBatchOptions{
		Deployment: dep, InputDirectory: pulls, OnDiagnostic: func(string) {},
		Root: root, Selectors: []string{"sample_groups_data"}, Tenant: "tenant",
	})
	if err != nil || len(result.Failed) != 0 {
		t.Fatalf("RunTransformBatch(stale data artifacts) = result %#v, error %v, want success", result, err)
	}
	wantConfig := "{\n" +
		"  \"items\": {\n" +
		"    \"head_office\": {\n" +
		"      \"name\": \"Head Office\"\n" +
		"    }\n" +
		"  }\n" +
		"}\n"
	wantLookup := "{\n" +
		"  \"by_id\": {\n" +
		"    \"101\": \"Head Office\"\n" +
		"  },\n" +
		"  \"id_by_key\": {\n" +
		"    \"head_office\": \"101\"\n" +
		"  },\n" +
		"  \"key_by_id\": {\n" +
		"    \"101\": \"head_office\"\n" +
		"  }\n" +
		"}\n"
	config, err := os.ReadFile(paths.Config)
	if err != nil {
		t.Fatalf("os.ReadFile(%q config) = error %v, want published config", paths.Config, err)
	}
	if string(config) != wantConfig {
		t.Errorf("RunTransformBatch(stale data artifacts) config bytes = %q, want %q", config, wantConfig)
	}
	lookup, err := os.ReadFile(paths.Lookup)
	if err != nil {
		t.Fatalf("os.ReadFile(%q lookup) = error %v, want published lookup", paths.Lookup, err)
	}
	if string(lookup) != wantLookup {
		t.Errorf("RunTransformBatch(stale data artifacts) lookup bytes = %q, want %q", lookup, wantLookup)
	}
	for _, file := range []string{
		paths.Imports, paths.Moves, paths.StaleConfig,
		paths.GeneratedBindings, paths.LegacyLookup,
	} {
		if _, err := os.Stat(file); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("RunTransformBatch(stale data artifacts) Stat(%q) = %v, want absent", file, err)
		}
	}

	staged, err := adopt.StageImports(adopt.StageImportsOptions{
		Deployment: dep, Root: root,
		Selectors: []string{}, Tenant: "tenant",
		Workspace: workspace,
	})
	var failure *procerr.ProcessFailure
	if !errors.As(err, &failure) || failure.Code != "NO_IMPORT_ARTIFACTS" {
		t.Fatalf("StageImports(data root after transform) error = %v, want NO_IMPORT_ARTIFACTS", err)
	}
	if staged != (adopt.StageImportsResult{}) {
		t.Errorf("StageImports(data root after transform) = %#v, want no sources or staged artifacts", staged)
	}
}
