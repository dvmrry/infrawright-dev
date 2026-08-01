package plan

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dvmrry/infrawright-dev/go/internal/canonjson"
)

const dataReferenceType = "sample_groups_data"

func dataReferenceAssessmentPlan(resources []any, configurationMode string, ids map[string]any) map[string]any {
	configurationAddress := "data." + dataReferenceType + ".items"
	configurationResource := map[string]any{
		"address": configurationAddress,
		"mode":    "data",
		"type":    dataReferenceType,
		"name":    "items",
	}
	if configurationMode == "managed" {
		configurationResource = map[string]any{
			"address": dataReferenceType + ".this",
			"mode":    "managed",
			"type":    dataReferenceType,
			"name":    "this",
		}
	}
	value := map[string]any{dataReferenceType: ids}
	return map[string]any{
		"format_version": "1.2",
		"complete":       true,
		"errored":        false,
		"planned_values": map[string]any{
			"outputs": map[string]any{
				infrawrightReferenceOutput: map[string]any{
					"sensitive": true,
					"value":     value,
				},
			},
			"root_module": map[string]any{
				"child_modules": []any{
					map[string]any{
						"address":   "module." + dataReferenceType,
						"resources": resources,
					},
				},
			},
		},
		"configuration": map[string]any{
			"root_module": map[string]any{
				"module_calls": map[string]any{
					dataReferenceType: map[string]any{
						"module": map[string]any{
							"resources": []any{configurationResource},
						},
					},
				},
			},
		},
		"resource_changes": []any{},
		"output_changes": map[string]any{
			infrawrightReferenceOutput: map[string]any{
				"actions":          []any{"create"},
				"before":           nil,
				"after":            value,
				"before_sensitive": false,
				"after_sensitive":  true,
				"after_unknown":    false,
			},
		},
	}
}

func dataReferenceResource(mode, address, index string, id any, resourceType string) map[string]any {
	name := "this"
	if mode == "data" {
		name = "items"
	}
	return map[string]any{
		"address": address,
		"index":   index,
		"mode":    mode,
		"name":    name,
		"type":    resourceType,
		"values":  map[string]any{"id": id, "name": "Group One"},
	}
}

func validDataReferenceResource(index string, id any) map[string]any {
	return dataReferenceResource(
		"data",
		`module.sample_groups_data.data.sample_groups_data.items["`+index+`"]`,
		index,
		id,
		dataReferenceType,
	)
}

func validDataReferencePlan(id any) map[string]any {
	return dataReferenceAssessmentPlan(
		[]any{validDataReferenceResource("group_one", id)},
		"data",
		map[string]any{"group_one": id},
	)
}

func dataReferenceContract() *AssessmentPlanContract {
	return &AssessmentPlanContract{ReferenceOutputTypes: []ReferenceOutputType{{
		Type: dataReferenceType, Kind: ReferenceOutputKindData,
	}}}
}

func requireAssessmentPlanErrorContaining(t *testing.T, planValue any, contract *AssessmentPlanContract, want string) {
	t.Helper()
	err := ValidateAssessmentPlan(planValue, contract)
	if err == nil {
		t.Fatalf("ValidateAssessmentPlan(%v) error = nil, want an error containing %q", planValue, want)
	}
	var failure *AssessmentPlanError
	if !errors.As(err, &failure) {
		t.Fatalf("ValidateAssessmentPlan(%v) error = %v (%T), want *AssessmentPlanError", planValue, err, err)
	}
	if !strings.Contains(failure.Error(), want) {
		t.Errorf("ValidateAssessmentPlan(%v) error = %q, want substring %q", planValue, failure.Error(), want)
	}
}

func offlineTerraformShowDataReferencePlan(t *testing.T) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "offline_remote_state_capture", "show.json"))
	if err != nil {
		t.Fatalf("ReadFile(offline terraform show capture) error = %v, want nil", err)
	}
	showValue, err := canonjson.ParseDataJSONLosslessly(string(raw))
	if err != nil {
		t.Fatalf("ParseDataJSONLosslessly(offline terraform show capture) error = %v, want nil", err)
	}
	show := showValue.(map[string]any)
	priorState := show["prior_state"].(map[string]any)
	priorValues := priorState["values"].(map[string]any)
	priorRoot := priorValues["root_module"].(map[string]any)
	priorChild := priorRoot["child_modules"].([]any)[0].(map[string]any)
	resource := cloneAssessmentValue(priorChild["resources"].([]any)[0]).(map[string]any)
	resource["address"] = `module.sample_groups_data.data.sample_groups_data.items["group_one"]`
	resource["type"] = dataReferenceType
	remoteValues := resource["values"].(map[string]any)
	resource["values"] = map[string]any{
		"id": remoteValues["outputs"].(map[string]any)["id"],
	}

	plannedValues := cloneAssessmentValue(show["planned_values"]).(map[string]any)
	plannedValues["root_module"].(map[string]any)["child_modules"] = []any{
		map[string]any{
			"address":   "module.sample_groups_data",
			"resources": []any{resource},
		},
	}
	configuration := cloneAssessmentValue(show["configuration"]).(map[string]any)
	moduleCall := configuration["root_module"].(map[string]any)["module_calls"].(map[string]any)[dataReferenceType].(map[string]any)
	module := moduleCall["module"].(map[string]any)
	configurationResource := module["resources"].([]any)[0].(map[string]any)
	configurationResource["address"] = "data." + dataReferenceType + ".items"
	configurationResource["type"] = dataReferenceType

	outputChanges := cloneAssessmentValue(show["output_changes"])
	return map[string]any{
		"format_version":    show["format_version"],
		"terraform_version": show["terraform_version"],
		"planned_values":    plannedValues,
		"configuration":     configuration,
		"resource_changes":  []any{},
		"output_changes":    outputChanges,
		"complete":          show["complete"],
		"errored":           show["errored"],
	}
}

func TestValidateAssessmentPlanReferenceOutputModeAuthorization(t *testing.T) {
	managed := referenceAssessmentPlan("create")
	managedChild := managed["planned_values"].(map[string]any)["root_module"].(map[string]any)["child_modules"].([]any)[0].(map[string]any)
	managedChild["resources"].([]any)[0] = dataReferenceResource(
		"data",
		`module.zpa_segment_group.data.zpa_segment_group.items["segment_one"]`,
		"segment_one",
		"72059380790653545",
		"zpa_segment_group",
	)
	requireAssessmentPlanErrorContaining(t, managed, referenceContract(), "unauthorized mode")

	managedForData := validDataReferencePlan("101")
	dataChild := managedForData["planned_values"].(map[string]any)["root_module"].(map[string]any)["child_modules"].([]any)[0].(map[string]any)
	dataChild["resources"].([]any)[0] = dataReferenceResource(
		"managed",
		`module.sample_groups_data.sample_groups_data.this["group_one"]`,
		"group_one",
		"101",
		dataReferenceType,
	)
	requireAssessmentPlanErrorContaining(t, managedForData, dataReferenceContract(), "unauthorized mode")

	mixed := validDataReferencePlan(json.Number("101"))
	mixedChild := mixed["planned_values"].(map[string]any)["root_module"].(map[string]any)["child_modules"].([]any)[0].(map[string]any)
	mixedChild["resources"] = []any{
		validDataReferenceResource("group_one", json.Number("101")),
		dataReferenceResource(
			"managed",
			`module.sample_groups_data.sample_groups_data.this["group_two"]`,
			"group_two",
			"102",
			dataReferenceType,
		),
	}
	mixed["planned_values"].(map[string]any)["outputs"].(map[string]any)[infrawrightReferenceOutput].(map[string]any)["value"] = map[string]any{
		dataReferenceType: map[string]any{
			"group_one": json.Number("101"),
			"group_two": "102",
		},
	}
	mixed["output_changes"].(map[string]any)[infrawrightReferenceOutput].(map[string]any)["after"] = map[string]any{
		dataReferenceType: map[string]any{
			"group_one": json.Number("101"),
			"group_two": "102",
		},
	}
	requireAssessmentPlanErrorContaining(t, mixed, dataReferenceContract(), "unauthorized mode")

	managedEmpty := emptyReferenceAssessmentPlan()
	managedEmptyConfig := managedEmpty["configuration"].(map[string]any)["root_module"].(map[string]any)["module_calls"].(map[string]any)["zpa_segment_group"].(map[string]any)["module"].(map[string]any)["resources"].([]any)
	managedEmptyConfig[0] = map[string]any{
		"address": "data.zpa_segment_group.items",
		"mode":    "data",
		"type":    "zpa_segment_group",
		"name":    "items",
	}
	requireAssessmentPlanErrorContaining(t, managedEmpty, referenceContract(), "empty reference output authorization requires")

	dataEmpty := dataReferenceAssessmentPlan([]any{}, "managed", map[string]any{})
	requireAssessmentPlanErrorContaining(t, dataEmpty, dataReferenceContract(), "empty reference output authorization requires")
	dataEmptyValid := dataReferenceAssessmentPlan([]any{}, "data", map[string]any{})
	requireValidAssessmentPlan(t, "ValidateAssessmentPlan(valid empty data reference)", dataEmptyValid, dataReferenceContract())

	requireValidAssessmentPlan(t, "ValidateAssessmentPlan(valid managed reference)", referenceAssessmentPlan("create"), referenceContract())
	requireValidAssessmentPlan(t, "ValidateAssessmentPlan(valid data reference)", validDataReferencePlan(json.Number("101")), dataReferenceContract())
}

func TestValidateAssessmentPlanDataReferenceEvidenceIsExactAndScalar(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(map[string]any)
		want   string
	}{
		{
			name: "address_index_mismatch",
			mutate: func(plan map[string]any) {
				resource := plan["planned_values"].(map[string]any)["root_module"].(map[string]any)["child_modules"].([]any)[0].(map[string]any)["resources"].([]any)[0].(map[string]any)
				resource["address"] = `module.sample_groups_data.data.sample_groups_data.items["real_key"]`
			},
			want: "invalid reference-output resource instance",
		},
		{
			name: "trailing_address_material",
			mutate: func(plan map[string]any) {
				resource := plan["planned_values"].(map[string]any)["root_module"].(map[string]any)["child_modules"].([]any)[0].(map[string]any)["resources"].([]any)[0].(map[string]any)
				resource["address"] = `module.sample_groups_data.data.sample_groups_data.items["group_one"].trailing`
			},
			want: "invalid reference-output resource instance",
		},
		{
			name: "boolean_id",
			mutate: func(plan map[string]any) {
				resource := plan["planned_values"].(map[string]any)["root_module"].(map[string]any)["child_modules"].([]any)[0].(map[string]any)["resources"].([]any)[0].(map[string]any)
				resource["values"].(map[string]any)["id"] = false
			},
			want: "invalid reference-output resource instance",
		},
		{
			name: "object_id",
			mutate: func(plan map[string]any) {
				resource := plan["planned_values"].(map[string]any)["root_module"].(map[string]any)["child_modules"].([]any)[0].(map[string]any)["resources"].([]any)[0].(map[string]any)
				resource["values"].(map[string]any)["id"] = map[string]any{"nested": true}
			},
			want: "invalid reference-output resource instance",
		},
		{
			name: "array_id",
			mutate: func(plan map[string]any) {
				resource := plan["planned_values"].(map[string]any)["root_module"].(map[string]any)["child_modules"].([]any)[0].(map[string]any)["resources"].([]any)[0].(map[string]any)
				resource["values"].(map[string]any)["id"] = []any{"nested"}
			},
			want: "invalid reference-output resource instance",
		},
		{
			name: "null_id",
			mutate: func(plan map[string]any) {
				resource := plan["planned_values"].(map[string]any)["root_module"].(map[string]any)["child_modules"].([]any)[0].(map[string]any)["resources"].([]any)[0].(map[string]any)
				resource["values"].(map[string]any)["id"] = nil
			},
			want: "invalid reference-output resource instance",
		},
		{
			name: "wrong_type",
			mutate: func(plan map[string]any) {
				resource := plan["planned_values"].(map[string]any)["root_module"].(map[string]any)["child_modules"].([]any)[0].(map[string]any)["resources"].([]any)[0].(map[string]any)
				resource["type"] = "other_data_source"
			},
			want: "planned engine reference output does not match provider-observed resource IDs",
		},
		{
			name: "wrong_name",
			mutate: func(plan map[string]any) {
				resource := plan["planned_values"].(map[string]any)["root_module"].(map[string]any)["child_modules"].([]any)[0].(map[string]any)["resources"].([]any)[0].(map[string]any)
				resource["name"] = "other"
			},
			want: "invalid reference-output resource instance",
		},
		{
			name: "wrong_module_address",
			mutate: func(plan map[string]any) {
				resource := plan["planned_values"].(map[string]any)["root_module"].(map[string]any)["child_modules"].([]any)[0].(map[string]any)["resources"].([]any)[0].(map[string]any)
				resource["address"] = `module.other.data.sample_groups_data.items["group_one"]`
			},
			want: "invalid reference-output resource instance",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			plan := validDataReferencePlan(json.Number("101"))
			test.mutate(plan)
			requireAssessmentPlanErrorContaining(t, plan, dataReferenceContract(), test.want)
		})
	}

	for _, test := range []struct {
		name string
		id   any
	}{
		{name: "string_id", id: "101"},
		{name: "numeric_id", id: json.Number("101")},
	} {
		t.Run("valid_"+test.name, func(t *testing.T) {
			requireValidAssessmentPlan(t, "ValidateAssessmentPlan(valid data ID)", validDataReferencePlan(test.id), dataReferenceContract())
		})
	}

	duplicate := validDataReferencePlan(json.Number("101"))
	duplicateChild := duplicate["planned_values"].(map[string]any)["root_module"].(map[string]any)["child_modules"].([]any)[0].(map[string]any)
	duplicateChild["resources"] = []any{
		validDataReferenceResource("group_one", json.Number("101")),
		validDataReferenceResource("group_one", json.Number("102")),
	}
	requireAssessmentPlanErrorContaining(t, duplicate, dataReferenceContract(), "duplicate reference-output key")
}

// TestValidateAssessmentPlanAcceptsOfflineTerraformShowDataShape pins the
// positive data contract to a real Terraform 1.15.4 `show -json` capture. The
// capture uses only the builtin terraform_remote_state data source and a local
// state file; the test adapts its provider-specific resource type and
// outputs.id field to the engine's provider-neutral referent shape while
// preserving Terraform-emitted address, mode, name, index, and sensitivity
// fields.
func TestValidateAssessmentPlanAcceptsOfflineTerraformShowDataShape(t *testing.T) {
	requireValidAssessmentPlan(
		t,
		"ValidateAssessmentPlan(real offline terraform show data shape)",
		offlineTerraformShowDataReferencePlan(t),
		dataReferenceContract(),
	)
}
