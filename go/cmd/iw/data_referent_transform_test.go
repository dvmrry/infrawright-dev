package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/dvmrry/infrawright-dev/go/internal/collectors"
	"github.com/dvmrry/infrawright-dev/go/internal/deployment"
	"github.com/dvmrry/infrawright-dev/go/internal/metadata"
	"github.com/dvmrry/infrawright-dev/go/internal/tfrender"
	"github.com/dvmrry/infrawright-dev/go/internal/transformrun"
)

type defaultTransformDataFixture struct {
	root      metadata.LoadedPackRoot
	dep       deployment.Deployment
	pulls     string
	workspace string
}

func newDefaultTransformDataFixture(t *testing.T) defaultTransformDataFixture {
	t.Helper()
	workspace := t.TempDir()
	packsRoot := filepath.Join(workspace, "packs")
	writeBlockC4JSON(t, filepath.Join(packsRoot, "sample", "pack.json"), map[string]any{
		"pin":               "1.0.0",
		"provider_prefixes": map[string]any{"sample_": "sample"},
		"provider_sources":  map[string]any{"sample": "example/sample"},
		"lookup_sources": map[string]any{
			"sample_groups_data": map[string]any{"name_field": "name"},
		},
		"references": map[string]any{
			"sample_rule": map[string]any{
				"group_id": map[string]any{"name_field": "name", "referent": "sample_groups_data"},
			},
		},
	})
	writeBlockC4JSON(t, filepath.Join(packsRoot, "sample", "registry.json"), map[string]any{
		"sample_groups_data": map[string]any{
			"data_referent": true,
			"fetch":         map[string]any{"pagination": "single", "path": "groups"},
			"product":       "sample",
		},
		"sample_rule": map[string]any{
			"fetch":    map[string]any{"pagination": "single", "path": "rules"},
			"generate": true,
			"product":  "sample",
		},
	})
	writeBlockC4JSON(t, filepath.Join(packsRoot, "sample", "schemas", "provider", "sample.json"), map[string]any{
		"resource_schemas": map[string]any{
			"sample_rule": map[string]any{"block": map[string]any{"attributes": map[string]any{
				"group_id": map[string]any{"optional": true, "type": "string"},
				"id":       map[string]any{"computed": true, "type": "string"},
				"name":     map[string]any{"optional": true, "type": "string"},
			}}},
		},
		"data_source_schemas": map[string]any{
			"sample_groups_data": map[string]any{"block": map[string]any{"attributes": map[string]any{
				"id":   map[string]any{"computed": true, "type": "string"},
				"name": map[string]any{"required": true, "type": "string"},
			}}},
		},
	})
	profile := filepath.Join(packsRoot, "sample.packset.json")
	writeBlockC4JSON(t, profile, map[string]any{
		"kind": metadata.PackSetKind, "version": 1, "packs": []any{"sample"}, "shared": []any{},
	})
	root, err := metadata.LoadPackRoot(metadata.LoadPackRootOptions{PacksRoot: packsRoot, ProfilePath: &profile})
	if err != nil {
		t.Fatalf("LoadPackRoot(default transform data fixture): %v", err)
	}
	return defaultTransformDataFixture{
		root: root,
		dep: deployment.Deployment{
			Overlay: workspace,
			Roots: map[string]deployment.RootProviderConfig{
				"sample": {HasCrossStateReferences: true, CrossStateReferences: true},
			},
		},
		pulls:     filepath.Join(workspace, "pulls"),
		workspace: workspace,
	}
}

func TestDefaultFetchAndTransformProcessesDataReferentBeforeReferrer(t *testing.T) {
	fixture := newDefaultTransformDataFixture(t)
	writeBlockC4JSON(t, filepath.Join(fixture.pulls, "sample_groups_data.json"), []any{
		map[string]any{"id": json.Number("101"), "name": "Head Office", "description": "drop me"},
	})
	writeBlockC4JSON(t, filepath.Join(fixture.pulls, "sample_rule.json"), []any{
		map[string]any{"id": "rule-1", "name": "Rule One", "group_id": "101"},
	})

	fetched, err := collectors.SelectFetchResources(collectors.SelectFetchResourcesOptions{Root: fixture.root})
	if err != nil {
		t.Fatalf("SelectFetchResources(no selectors): %v", err)
	}
	if !reflect.DeepEqual(fetched, []string{"sample_groups_data", "sample_rule"}) {
		t.Fatalf("SelectFetchResources(no selectors) = %v, want data referent and referrer", fetched)
	}

	var writes []string
	result, err := transformrun.RunTransformBatch(transformrun.RunTransformBatchOptions{
		BeforeArtifactWrite: func(resourceType string) error {
			writes = append(writes, resourceType)
			return nil
		},
		Deployment: fixture.dep, InputDirectory: fixture.pulls,
		OnDiagnostic: func(string) {}, Root: fixture.root, Tenant: "tenant",
	})
	if err != nil {
		t.Fatalf("RunTransformBatch(no selectors): %v", err)
	}
	if !reflect.DeepEqual(writes, []string{"sample_groups_data", "sample_rule"}) {
		t.Fatalf("RunTransformBatch publication order = %v, want data referent before referrer", writes)
	}
	if !reflect.DeepEqual(result.Processed, []string{"sample_groups_data", "sample_rule"}) || len(result.Failed) != 0 || len(result.Skipped) != 0 {
		t.Fatalf("RunTransformBatch result = %#v, want both processed with no failures or skips", result)
	}

	dataPaths, err := tfrender.ComputeTransformArtifactPaths(
		fixture.dep, "sample_groups_data", "tenant", tfrender.TransformArtifactModeDataReferent,
	)
	if err != nil {
		t.Fatalf("ComputeTransformArtifactPaths(data referent): %v", err)
	}
	rulePaths, err := tfrender.ComputeTransformArtifactPaths(
		fixture.dep, "sample_rule", "tenant", tfrender.TransformArtifactModeGenerated,
	)
	if err != nil {
		t.Fatalf("ComputeTransformArtifactPaths(referrer): %v", err)
	}
	for _, path := range []string{dataPaths.Config, dataPaths.Lookup, rulePaths.Config} {
		if _, err := os.Stat(path); err != nil {
			t.Errorf("RunTransformBatch output %q: %v, want published artifact", path, err)
		}
	}
	for _, path := range []string{dataPaths.Imports, dataPaths.Moves} {
		if _, err := os.Stat(path); err == nil {
			t.Errorf("data referent artifact %q exists, want no imports or moves", path)
		} else if !errors.Is(err, os.ErrNotExist) {
			t.Errorf("data referent artifact %q stat error = %v, want os.ErrNotExist", path, err)
		}
	}
	dataConfig, err := os.ReadFile(dataPaths.Config)
	if err != nil {
		t.Fatalf("read data referent config: %v", err)
	}
	if string(dataConfig) == "" || !containsBytes(dataConfig, []byte("Head Office")) {
		t.Fatalf("data referent config = %q, want published name-only config", dataConfig)
	}
}

func containsBytes(value, want []byte) bool {
	for index := 0; index+len(want) <= len(value); index++ {
		if string(value[index:index+len(want)]) == string(want) {
			return true
		}
	}
	return false
}
