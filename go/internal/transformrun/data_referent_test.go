package transformrun

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/dvmrry/infrawright-dev/go/internal/canonjson"
	"github.com/dvmrry/infrawright-dev/go/internal/deployment"
	"github.com/dvmrry/infrawright-dev/go/internal/metadata"
	"github.com/dvmrry/infrawright-dev/go/internal/tfrender"
	"github.com/google/go-cmp/cmp"
)

func dataReferentPackRoot(t *testing.T) metadata.LoadedPackRoot {
	return dataReferentPackRootWithGeneratedReferrer(t, false)
}

func dataReferentPackRootWithGeneratedReferrer(t *testing.T, generatedReferrer bool) metadata.LoadedPackRoot {
	t.Helper()
	packsRoot := t.TempDir()
	pack := metadata.JsonObject{
		"pin":               "1.0.0",
		"provider_prefixes": metadata.JsonObject{"sample_": "sample"},
		"provider_sources":  metadata.JsonObject{"sample": "example/sample"},
		"lookup_sources": metadata.JsonObject{
			"sample_groups_data": metadata.JsonObject{"name_field": "name"},
		},
	}
	registry := metadata.JsonObject{
		"sample_groups_data": metadata.JsonObject{
			"data_referent": true,
			"fetch": metadata.JsonObject{
				"pagination": "zia",
				"path":       "locations/groups",
			},
			"product": "sample",
		},
	}
	resourceSchemas := metadata.JsonObject{}
	if generatedReferrer {
		pack["references"] = metadata.JsonObject{
			"sample_rule": metadata.JsonObject{
				"group_id": metadata.JsonObject{
					"name_field": "name",
					"referent":   "sample_groups_data",
				},
			},
		}
		registry["sample_rule"] = metadata.JsonObject{"generate": true, "product": "sample"}
		resourceSchemas["sample_rule"] = metadata.JsonObject{"block": metadata.JsonObject{
			"attributes": metadata.JsonObject{
				"group_id": metadata.JsonObject{"optional": true, "type": "string"},
				"id":       metadata.JsonObject{"computed": true, "type": "string"},
				"name":     metadata.JsonObject{"optional": true, "type": "string"},
			},
		}}
	}
	writeJSON(t, filepath.Join(packsRoot, "sample", "pack.json"), pack)
	writeJSON(t, filepath.Join(packsRoot, "sample", "registry.json"), registry)
	writeJSON(t, filepath.Join(packsRoot, "sample", "schemas", "provider", "sample.json"), metadata.JsonObject{
		"resource_schemas": resourceSchemas,
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
	writeJSON(t, profilePath, metadata.JsonObject{
		"kind": metadata.PackSetKind, "version": 1,
		"packs": []any{"sample"}, "shared": []any{},
	})
	root, err := metadata.LoadPackRoot(metadata.LoadPackRootOptions{
		PacksRoot: packsRoot, ProfilePath: &profilePath,
	})
	if err != nil {
		t.Fatalf("LoadPackRoot(data referent fixture) = error %v, want nil", err)
	}
	return root
}

type dataReferentFixture struct {
	dep       deployment.Deployment
	paths     tfrender.TransformArtifactPaths
	pulls     string
	root      metadata.LoadedPackRoot
	workspace string
}

func newDataReferentFixture(t *testing.T, hcl, generatedReferrer bool) dataReferentFixture {
	t.Helper()
	workspace := t.TempDir()
	dep := deployment.Deployment{
		Overlay: workspace,
		Roots:   map[string]deployment.RootProviderConfig{"sample": {HasCrossStateReferences: true, CrossStateReferences: true}},
	}
	if hcl {
		dep.HasTfvarsFormat = true
		dep.TfvarsFormat = "hcl"
	}
	root := dataReferentPackRootWithGeneratedReferrer(t, generatedReferrer)
	pulls := filepath.Join(workspace, "pulls")
	paths, err := tfrender.ComputeTransformArtifactPaths(
		dep, "sample_groups_data", "tenant", tfrender.TransformArtifactModeDataReferent,
	)
	if err != nil {
		t.Fatalf("ComputeTransformArtifactPaths(data referent fixture) = error %v, want nil", err)
	}
	return dataReferentFixture{
		dep: dep, paths: paths, pulls: pulls, root: root, workspace: workspace,
	}
}

func runDataReferentTransform(t *testing.T) (metadata.LoadedPackRoot, deployment.Deployment, tfrender.TransformArtifactPaths) {
	t.Helper()
	fixture := newDataReferentFixture(t, false, false)
	// This JSON file is the fake collector output for two location groups. The
	// description and nested metadata prove the data lane drops everything but
	// the lookup_sources name_field before publication.
	writeJSON(t, filepath.Join(fixture.pulls, "sample_groups_data.json"), []any{
		map[string]any{
			"id":          json.Number("101"),
			"name":        "Head Office",
			"description": "must not reach config",
			"metadata":    map[string]any{"region": "east"},
		},
		map[string]any{
			"id":          json.Number("202"),
			"name":        "Branch Two",
			"description": "must not reach config",
			"metadata":    map[string]any{"region": "west"},
		},
	})
	result, err := RunTransformBatch(RunTransformBatchOptions{
		Deployment:     fixture.dep,
		InputDirectory: fixture.pulls,
		OnDiagnostic:   func(string) {},
		Root:           fixture.root,
		Selectors:      []string{"sample_groups_data"},
		Tenant:         "tenant",
	})
	if err != nil {
		t.Fatalf("RunTransformBatch(%v) = error %v, want nil", []string{"sample_groups_data"}, err)
	}
	if diff := cmp.Diff([]string{"sample_groups_data"}, result.Processed); diff != "" {
		t.Fatalf("RunTransformBatch(%v).Processed mismatch (-want +got):\n%s", []string{"sample_groups_data"}, diff)
	}
	if len(result.Failed) != 0 || len(result.Skipped) != 0 {
		t.Fatalf("RunTransformBatch(%v) result = %#v, want no failures or skips", []string{"sample_groups_data"}, result)
	}
	return fixture.root, fixture.dep, fixture.paths
}

func TestDataReferentTransformPublishesNameOnlyConfigAndLookup(t *testing.T) {
	_, _, paths := runDataReferentTransform(t)

	configText := readFile(t, paths.Config)
	wantConfig := "{\n" +
		"  \"items\": {\n" +
		"    \"branch_two\": {\n" +
		"      \"name\": \"Branch Two\"\n" +
		"    },\n" +
		"    \"head_office\": {\n" +
		"      \"name\": \"Head Office\"\n" +
		"    }\n" +
		"  }\n" +
		"}\n"
	if configText != wantConfig {
		t.Errorf("RunTransformBatch(%q) config bytes = %q, want canonical %q", "sample_groups_data", configText, wantConfig)
	}

	lookupText := readFile(t, paths.Lookup)
	wantLookup := "{\n" +
		"  \"by_id\": {\n" +
		"    \"101\": \"Head Office\",\n" +
		"    \"202\": \"Branch Two\"\n" +
		"  },\n" +
		"  \"id_by_key\": {\n" +
		"    \"branch_two\": \"202\",\n" +
		"    \"head_office\": \"101\"\n" +
		"  },\n" +
		"  \"key_by_id\": {\n" +
		"    \"101\": \"head_office\",\n" +
		"    \"202\": \"branch_two\"\n" +
		"  }\n" +
		"}\n"
	if lookupText != wantLookup {
		t.Errorf("RunTransformBatch(%q) lookup bytes = %q, want canonical %q", "sample_groups_data", lookupText, wantLookup)
	}
}

func TestDataReferentTransformNeverWritesImportsOrMoves(t *testing.T) {
	_, _, paths := runDataReferentTransform(t)

	if _, err := os.Stat(paths.Imports); err == nil {
		t.Errorf("data-referent transform never writes imports: %q exists", paths.Imports)
	} else if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("data-referent transform never writes imports: Stat(%q) = error %v, want os.ErrNotExist", paths.Imports, err)
	}
	if _, err := os.Stat(paths.Moves); err == nil {
		t.Errorf("data-referent transform never writes moves: %q exists", paths.Moves)
	} else if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("data-referent transform never writes moves: Stat(%q) = error %v, want os.ErrNotExist", paths.Moves, err)
	}
}

func TestDataReferentTransformHCLDeploymentPinsConfigBytes(t *testing.T) {
	fixture := newDataReferentFixture(t, true, false)
	writeJSON(t, filepath.Join(fixture.pulls, "sample_groups_data.json"), []any{
		map[string]any{"id": json.Number("101"), "name": "Head Office"},
		map[string]any{"id": json.Number("202"), "name": "Branch Two"},
	})
	result, err := RunTransformBatch(RunTransformBatchOptions{
		Deployment: fixture.dep, InputDirectory: fixture.pulls, OnDiagnostic: func(string) {},
		Root: fixture.root, Selectors: []string{"sample_groups_data"}, Tenant: "tenant",
	})
	if err != nil || len(result.Failed) != 0 {
		t.Fatalf("RunTransformBatch(HCL data referent) = result %#v, error %v, want success", result, err)
	}
	want := "# Generated by infrawright. Do not edit; regenerate via make transform/adopt.\n" +
		"items = {\n" +
		"  \"branch_two\" = {\n" +
		"    name = \"Branch Two\"\n" +
		"  }\n" +
		"  \"head_office\" = {\n" +
		"    name = \"Head Office\"\n" +
		"  }\n" +
		"}\n"
	if got := readFile(t, fixture.paths.Config); got != want {
		t.Errorf("RunTransformBatch(HCL data referent) config bytes = %q, want %q", got, want)
	}
}

func TestDataReferentEmptyFetchPinsNestedLookupAndRoundTrips(t *testing.T) {
	fixture := newDataReferentFixture(t, false, false)
	writeJSON(t, filepath.Join(fixture.pulls, "sample_groups_data.json"), []any{})
	result, err := RunTransformBatch(RunTransformBatchOptions{
		Deployment: fixture.dep, InputDirectory: fixture.pulls, OnDiagnostic: func(string) {},
		Root: fixture.root, Selectors: []string{"sample_groups_data"}, Tenant: "tenant",
	})
	if err != nil || len(result.Failed) != 0 {
		t.Fatalf("RunTransformBatch(empty data referent) = result %#v, error %v, want success", result, err)
	}
	wantLookup := "{\n" +
		"  \"by_id\": {},\n" +
		"  \"id_by_key\": {},\n" +
		"  \"key_by_id\": {}\n" +
		"}\n"
	lookupText := readFile(t, fixture.paths.Lookup)
	if lookupText != wantLookup {
		t.Errorf("RunTransformBatch(empty data referent) lookup bytes = %q, want canonical nested %q; pre-fix flat {} is not an acceptable sidecar", lookupText, wantLookup)
	}
	parsed, err := canonjson.ParseDataJSONLosslessly(lookupText)
	if err != nil {
		t.Fatalf("ParseDataJSONLosslessly(empty lookup) = error %v, want nil", err)
	}
	lookup, err := tfrender.ParseLookupSidecar(parsed)
	if err != nil {
		t.Fatalf("ParseLookupSidecar(empty lookup) = error %v, want nil", err)
	}
	if diff := cmp.Diff(tfrender.TransformLookupData{
		ByID: map[string]string{}, IDByKey: map[string]string{}, KeyByID: map[string]string{},
	}, lookup); diff != "" {
		t.Errorf("ParseLookupSidecar(empty lookup) mismatch (-want +got):\n%s", diff)
	}
}

func TestDataReferentTransformRetiresAllManagedArtifactsAndLeavesNothingStageable(t *testing.T) {
	fixture := newDataReferentFixture(t, false, false)
	writeJSON(t, filepath.Join(fixture.pulls, "sample_groups_data.json"), []any{
		map[string]any{"id": json.Number("101"), "name": "Head Office"},
	})
	stale := []string{
		fixture.paths.Config, fixture.paths.Lookup, fixture.paths.Imports,
		fixture.paths.Moves, fixture.paths.StaleConfig,
		fixture.paths.GeneratedBindings, fixture.paths.LegacyLookup,
		fixture.paths.LegacyConfig, fixture.paths.LegacyStaleConfig,
	}
	for _, file := range stale {
		if err := os.MkdirAll(filepath.Dir(file), 0o777); err != nil {
			t.Fatalf("os.MkdirAll(%q) = error %v", filepath.Dir(file), err)
		}
		contents := "STALE-" + filepath.Base(file)
		if file == fixture.paths.Lookup {
			contents = `{"by_id":{"stale-id":"Stale"},"id_by_key":{"stale_key":"stale-id"},"key_by_id":{"stale-id":"stale_key"}}`
		}
		if err := os.WriteFile(file, []byte(contents), 0o666); err != nil {
			t.Fatalf("os.WriteFile(%q) = error %v", file, err)
		}
	}
	result, err := RunTransformBatch(RunTransformBatchOptions{
		Deployment: fixture.dep, InputDirectory: fixture.pulls, OnDiagnostic: func(string) {},
		Root: fixture.root, Selectors: []string{"sample_groups_data"}, Tenant: "tenant",
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
	if got := readFile(t, fixture.paths.Config); got != wantConfig {
		t.Errorf("RunTransformBatch(stale data artifacts) config bytes = %q, want %q", got, wantConfig)
	}
	if got := readFile(t, fixture.paths.Lookup); got != wantLookup {
		t.Errorf("RunTransformBatch(stale data artifacts) lookup bytes = %q, want %q", got, wantLookup)
	}
	for _, file := range []string{
		fixture.paths.Imports, fixture.paths.Moves, fixture.paths.StaleConfig,
		fixture.paths.GeneratedBindings, fixture.paths.LegacyLookup,
		fixture.paths.LegacyConfig, fixture.paths.LegacyStaleConfig,
	} {
		if _, err := os.Stat(file); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("RunTransformBatch(stale data artifacts) Stat(%q) = %v, want absent", file, err)
		}
	}
}

func TestMixedDataReferentAndGeneratedReferrerPublishesReferentFirst(t *testing.T) {
	fixture := newDataReferentFixture(t, false, true)
	writeJSON(t, filepath.Join(fixture.pulls, "sample_groups_data.json"), []any{
		map[string]any{"id": json.Number("101"), "name": "Head Office"},
	})
	writeJSON(t, filepath.Join(fixture.pulls, "sample_rule.json"), []any{
		map[string]any{"id": "rule-1", "name": "Rule One", "group_id": "101"},
	})
	var writes []string
	result, err := RunTransformBatch(RunTransformBatchOptions{
		BeforeArtifactWrite: func(resourceType string) error {
			writes = append(writes, resourceType)
			return nil
		},
		Deployment: fixture.dep, InputDirectory: fixture.pulls, OnDiagnostic: func(string) {},
		Root:      fixture.root,
		Selectors: []string{"sample_rule", "sample_groups_data"}, Tenant: "tenant",
	})
	if err != nil || len(result.Failed) != 0 {
		t.Fatalf("RunTransformBatch(mixed data referent/referrer) = result %#v, error %v, want success", result, err)
	}
	if diff := cmp.Diff([]string{"sample_groups_data", "sample_rule"}, writes); diff != "" {
		t.Errorf("RunTransformBatch(mixed selection) publication order mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]string{"sample_groups_data", "sample_rule"}, result.Processed); diff != "" {
		t.Errorf("RunTransformBatch(mixed selection).Processed mismatch (-want +got):\n%s", diff)
	}
	rulePaths, err := tfrender.ComputeTransformArtifactPaths(
		fixture.dep, "sample_rule", "tenant", tfrender.TransformArtifactModeGenerated,
	)
	if err != nil {
		t.Fatalf("ComputeTransformArtifactPaths(%q) = error %v, want nil", "sample_rule", err)
	}
	for _, file := range []string{fixture.paths.Config, fixture.paths.Lookup, rulePaths.Config} {
		if _, err := os.Stat(file); err != nil {
			t.Errorf("RunTransformBatch(mixed selection) Stat(%q) = %v, want published artifact", file, err)
		}
	}
}
