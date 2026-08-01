package envgen

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dvmrry/infrawright-dev/go/internal/metadata"
)

// dataReferentEnvironmentFixture is deliberately provider-neutral. The
// fixture exercises only the engine contract: a generated referrer points at
// one singleton referent root, whose registry entry is either data-only or
// generated. The two variants must travel through the same envgen machinery.
type dataReferentEnvironmentFixture struct {
	root           metadata.LoadedPackRoot
	deploymentPath string
	outputRoot     string
	workspace      string
	referentType   string
}

func newDataReferentEnvironmentFixture(t *testing.T, dataOnly bool) dataReferentEnvironmentFixture {
	t.Helper()
	workspace := t.TempDir()
	packsRoot := filepath.Join(workspace, "packs")
	referentType := "sample_groups_generated"
	if dataOnly {
		referentType = "sample_groups_data"
	}

	optionalString := func() metadata.JsonObject {
		return terraformTestAttribute("string", "optional")
	}
	computedNumber := func() metadata.JsonObject {
		return terraformTestAttribute("number", "computed")
	}
	ruleSchema := metadata.JsonObject{
		"block": metadata.JsonObject{
			"attributes": metadata.JsonObject{
				"id": computedNumber(),
			},
			"block_types": metadata.JsonObject{
				"groups": terraformTestListBlock(metadata.JsonObject{
					"id": terraformTestAttribute([]any{"set", "number"}, "required"),
				}),
			},
		},
	}
	resourceSchemas := metadata.JsonObject{
		"sample_rule": ruleSchema,
	}
	registry := metadata.JsonObject{
		"sample_rule": metadata.JsonObject{"generate": true, "product": "sample"},
	}
	dataSourceSchemas := metadata.JsonObject{}
	if dataOnly {
		registry[referentType] = metadata.JsonObject{
			"data_referent": true,
			"fetch": metadata.JsonObject{
				"pagination": "zia",
				"path":       "sample/groups",
			},
			"product": "sample",
		}
		dataSourceSchemas[referentType] = terraformTestBlock(metadata.JsonObject{
			"id":   computedNumber(),
			"name": optionalString(),
		})
	} else {
		registry[referentType] = metadata.JsonObject{"generate": true, "product": "sample"}
		resourceSchemas[referentType] = terraformTestBlock(metadata.JsonObject{
			"id":   computedNumber(),
			"name": optionalString(),
		})
	}

	references := metadata.JsonObject{
		"sample_rule": metadata.JsonObject{
			"groups.id": metadata.JsonObject{
				"referent":   referentType,
				"name_field": "name",
			},
		},
	}
	manifest := metadata.JsonObject{
		"pin":               "1.0.0",
		"provider_prefixes": metadata.JsonObject{"sample_": "sample"},
		"provider_sources":  metadata.JsonObject{"sample": "example/sample"},
		"references":        references,
	}
	if dataOnly {
		manifest["lookup_sources"] = metadata.JsonObject{
			referentType: metadata.JsonObject{"name_field": "name"},
		}
	}
	writeSyntheticTopologyPack(t, packsRoot, "sample", manifest, registry, resourceSchemas, dataSourceSchemas)
	profilePath := filepath.Join(packsRoot, "sample.packset.json")
	writeJSONFile(t, profilePath, metadata.JsonObject{
		"kind":    metadata.PackSetKind,
		"version": 1,
		"packs":   []any{"sample"},
		"shared":  []any{},
	})
	root, err := metadata.LoadPackRoot(metadata.LoadPackRootOptions{
		PacksRoot: packsRoot, ProfilePath: &profilePath,
	})
	if err != nil {
		t.Fatalf("LoadPackRoot(sample data-referent fixture, dataOnly=%t) error = %v", dataOnly, err)
	}

	deploymentPath := filepath.Join(workspace, "deployment.json")
	writeJSONFile(t, deploymentPath, metadata.JsonObject{
		"module_dir": filepath.Join(workspace, "modules"),
		"overlay":    workspace,
		"roots": metadata.JsonObject{
			"sample": metadata.JsonObject{"cross_state_references": true},
		},
	})
	configDirectory := filepath.Join(workspace, "config", "tenant")
	writeJSONFile(t, filepath.Join(configDirectory, "sample_rule.auto.tfvars.json"), metadata.JsonObject{
		"items": metadata.JsonObject{
			"rule_one": metadata.JsonObject{
				"groups": []any{metadata.JsonObject{
					"id": referentType + ".group_one",
				}},
			},
		},
	})
	writeJSONFile(t, filepath.Join(configDirectory, "lookups", referentType+".lookup.json"), metadata.JsonObject{
		"by_id":     metadata.JsonObject{"group-id": "Group One"},
		"id_by_key": metadata.JsonObject{"group_one": "group-id"},
		"key_by_id": metadata.JsonObject{"group-id": "group_one"},
	})
	writeJSONFile(t, filepath.Join(configDirectory, "sample_rule.generated.expressions.json"), metadata.JsonObject{
		"resources": metadata.JsonObject{
			"sample_rule.rule_one": metadata.JsonObject{
				"groups[0].id": metadata.JsonObject{
					"expression": fmt.Sprintf(
						`data.terraform_remote_state.%s.outputs.iw_reference_ids.%s["group_one"]`,
						referentType, referentType,
					),
				},
			},
		},
	})

	return dataReferentEnvironmentFixture{
		root:           root,
		deploymentPath: deploymentPath,
		outputRoot:     filepath.Join(workspace, "generated"),
		workspace:      workspace,
		referentType:   referentType,
	}
}

func (f dataReferentEnvironmentFixture) generate(t *testing.T, stateAware bool, probe StateProbe) string {
	t.Helper()
	outputRoot := f.outputRoot
	if _, err := GenerateEnvironmentRoots(GenerateEnvironmentRootsOptions{
		Deployment: loadDeploymentFile(t, f.deploymentPath),
		FormatHcl:  identityFormatter,
		OutputRoot: &outputRoot,
		Root:       f.root,
		Selectors:  []string{"sample_rule"},
		StateAware: stateAware,
		StateProbe: probe,
		Tenant:     "tenant",
	}); err != nil {
		t.Fatalf("GenerateEnvironmentRoots(sample data-referent fixture, dataOnly=%t, stateAware=%t) error = %v", strings.HasSuffix(f.referentType, "_data"), stateAware, err)
	}
	path := filepath.Join(f.outputRoot, "tenant", "sample_rule", "expression_bindings.tf")
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%s) error = %v", path, err)
	}
	return string(content)
}

func normalizeDataReferentName(content, referentType string) string {
	return strings.ReplaceAll(content, referentType, "sample_groups_referent")
}

// TestDataReferentEnvironmentBindingsMatchGeneratedReferent is the primary
// characterization for group (a). The data-only registry bit changes root and
// module production, but the referrer's binding bytes must remain the same
// reference machinery, modulo the referent type name.
func TestDataReferentEnvironmentBindingsMatchGeneratedReferent(t *testing.T) {
	dataFixture := newDataReferentEnvironmentFixture(t, true)
	generatedFixture := newDataReferentEnvironmentFixture(t, false)

	dataBindings := dataFixture.generate(t, false, nil)
	generatedBindings := generatedFixture.generate(t, false, nil)
	wantResolver := `try(data.terraform_remote_state.sample_groups_data.outputs.iw_reference_ids.sample_groups_data["group_one"], local.iw_reference_lookup_sample_groups_data["group_one"])`
	if !strings.Contains(dataBindings, wantResolver) {
		t.Errorf("sample_groups_data expression_bindings.tf = %q, want resolver %q", dataBindings, wantResolver)
	}
	if got, want := normalizeDataReferentName(dataBindings, dataFixture.referentType), normalizeDataReferentName(generatedBindings, generatedFixture.referentType); got != want {
		t.Errorf("data-only/generated expression_bindings.tf normalized bytes differ (-data +generated):\n-data:\n%s\n+generated:\n%s", got, want)
	}
}

// TestDataReferentStateAwareAbsentProbeKeepsLookupFallback is the group (b)
// characterization. It uses the state_aware_test.go absentProbe pattern: an
// unavailable referent state must leave the committed lookup arm available in
// the generated resolver for a tokenised data-only reference.
func TestDataReferentStateAwareAbsentProbeKeepsLookupFallback(t *testing.T) {
	fixture := newDataReferentEnvironmentFixture(t, true)
	var probeCalls int
	bindings := fixture.generate(t, true, func(rootLabel, referentType string) (StateProbeResult, error) {
		probeCalls++
		if rootLabel != fixture.referentType || referentType != fixture.referentType {
			t.Errorf("StateProbe(rootLabel=%q, referentType=%q) want (%q, %q)", rootLabel, referentType, fixture.referentType, fixture.referentType)
		}
		return StateProbeResult{Usable: false}, nil
	})
	if probeCalls != 1 {
		t.Errorf("StateProbe calls = %d, want 1 for the tokenised data referent", probeCalls)
	}
	wantResolver := `local.iw_reference_lookup_sample_groups_data["group_one"]`
	if !strings.Contains(bindings, wantResolver) || strings.Contains(bindings, "try(data.terraform_remote_state.") {
		t.Errorf("GenerateEnvironmentRoots(sample_groups_data, absentProbe) expression_bindings.tf = %q, want lookup-only resolver %q and no try arm", bindings, wantResolver)
	}
	mainPath := filepath.Join(fixture.outputRoot, "tenant", "sample_rule", "main.tf")
	main, err := os.ReadFile(mainPath)
	if err != nil {
		t.Fatalf("ReadFile(%s) error = %v, want nil", mainPath, err)
	}
	if strings.Contains(string(main), `data "terraform_remote_state"`) {
		t.Errorf("sample_rule main.tf = %q, want no unusable terraform_remote_state block", main)
	}
}
