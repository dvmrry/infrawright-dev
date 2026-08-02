package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/dvmrry/infrawright-dev/go/internal/canonjson"
	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"
)

func preparePlanRootsSchemaFixture(t *testing.T, workspace string, withDataReferent bool) blockC4Fixture {
	t.Helper()
	fixture := prepareBlockC4Fixture(t, workspace)
	if !withDataReferent {
		return fixture
	}

	writeBlockC4JSON(t, filepath.Join(fixture.packs, "sample", "pack.json"), map[string]any{
		"pin":               "1.0.0",
		"provider_prefixes": map[string]any{"sample_": "sample"},
		"provider_sources":  map[string]any{"sample": "example/sample"},
		"lookup_sources": map[string]any{
			"sample_groups_data": map[string]any{"name_field": "name"},
		},
		"references": map[string]any{
			"sample_resource": map[string]any{
				"group_id": map[string]any{"name_field": "name", "referent": "sample_groups_data"},
			},
		},
		"vendor": "sample",
	})
	writeBlockC4JSON(t, filepath.Join(fixture.packs, "sample", "registry.json"), map[string]any{
		"sample_groups_data": map[string]any{
			"data_referent": true,
			"fetch":         map[string]any{"pagination": "single", "path": "groups"},
			"product":       "sample",
		},
		"sample_resource": map[string]any{"generate": true, "product": "sample"},
	})
	writeBlockC4JSON(t, filepath.Join(fixture.packs, "sample", "schemas", "provider", "sample.json"), map[string]any{
		"resource_schemas": map[string]any{},
		"data_source_schemas": map[string]any{
			"sample_groups_data": map[string]any{},
		},
	})
	writeBlockC4JSON(t, fixture.deployment, map[string]any{
		"overlay": workspace, "module_dir": filepath.Join(workspace, "modules"),
		"roots": map[string]any{"sample": map[string]any{"cross_state_references": true}},
	})
	writeBlockC4File(t, filepath.Join(workspace, "envs", "tenant", "sample_groups_data", "main.tf"), []byte("# data referent fixture\n"), 0o600)
	return fixture
}

func validatePlanRootsOutputAgainstCommittedSchema(t *testing.T, repositoryRoot string, output []byte) any {
	t.Helper()
	schemaDirectory := filepath.Join(repositoryRoot, "do"+"cs", "schemas")
	schemaBytes, err := os.ReadFile(filepath.Join(schemaDirectory, "plan-roots.schema.json"))
	if err != nil {
		t.Fatalf("read committed plan-roots schema: %v", err)
	}
	schemaValue, err := canonjson.ParseControlJSON(string(schemaBytes))
	if err != nil {
		t.Fatalf("parse committed plan-roots schema: %v", err)
	}
	const schemaID = "https://infrawright.local/schemas/plan-roots.schema.json"
	compiler := jsonschema.NewCompiler()
	compiler.DefaultDraft(jsonschema.Draft2020)
	if err := compiler.AddResource(schemaID, schemaValue); err != nil {
		t.Fatalf("register committed plan-roots schema: %v", err)
	}
	schema, err := compiler.Compile(schemaID)
	if err != nil {
		t.Fatalf("compile committed plan-roots schema: %v", err)
	}
	value, err := canonjson.ParseControlJSON(string(output))
	if err != nil {
		t.Fatalf("parse plan-roots CLI output: %v", err)
	}
	if err := schema.Validate(value); err != nil {
		t.Fatalf("plan-roots CLI output does not validate against committed schema: %v\noutput:\n%s", err, output)
	}
	return value
}

func TestPlanRootsCLIOutputValidatesAgainstPublishedSchema(t *testing.T) {
	repositoryRoot := repoRoot(t)
	binary := buildGoV2AuthorityCLI(t, repositoryRoot, "iw-go-plan-roots-schema")

	cases := []struct {
		name             string
		withDataReferent bool
		wantData         []string
	}{
		{name: "empty", wantData: []string{}},
		{name: "non-empty", withDataReferent: true, wantData: []string{"sample_groups_data"}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			fixture := preparePlanRootsSchemaFixture(t, filepath.Join(t.TempDir(), "workspace"), testCase.withDataReferent)
			result := runV2TopologyCommand(t, binary, fixture, append([]string{"plan-roots", "--tenant", "tenant"}, topologyFixtureArguments(fixture)...))
			if result.exit != 0 {
				t.Fatalf("plan-roots exit = %d, want 0; stdout=%q stderr=%q", result.exit, result.stdout, result.stderr)
			}
			value := validatePlanRootsOutputAgainstCommittedSchema(t, repositoryRoot, result.stdout)
			root, ok := value.(map[string]any)
			if !ok {
				t.Fatalf("plan-roots output root = %T, want object", value)
			}
			roots, ok := root["roots"].([]any)
			if !ok || len(roots) == 0 {
				t.Fatalf("plan-roots output roots = %#v, want at least one root", root["roots"])
			}
			found := false
			for _, rawRoot := range roots {
				materialized, ok := rawRoot.(map[string]any)
				if !ok {
					t.Fatalf("plan-roots root = %T, want object", rawRoot)
				}
				data, ok := materialized["data_referents"].([]any)
				if !ok {
					t.Fatalf("plan-roots data_referents = %T, want array", materialized["data_referents"])
				}
				if len(testCase.wantData) == 0 && len(data) == 0 {
					found = true
				}
				if len(data) == len(testCase.wantData) {
					matches := true
					for index, want := range testCase.wantData {
						if data[index] != want {
							matches = false
						}
					}
					if matches {
						found = true
					}
				}
			}
			if !found {
				t.Fatalf("plan-roots data_referents did not include expected %#v: %#v", testCase.wantData, root["roots"])
			}
		})
	}
}
