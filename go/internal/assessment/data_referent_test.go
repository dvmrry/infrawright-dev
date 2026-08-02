package assessment

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/dvmrry/infrawright-dev/go/internal/plan"
)

func outputsOnlyDataReferentPlan() map[string]any {
	value := map[string]any{
		"sample_groups_data": map[string]any{
			"group_one": json.Number("101"),
		},
	}
	return map[string]any{
		"format_version": "1.2",
		"complete":       true,
		"errored":        false,
		"planned_values": map[string]any{
			"outputs": map[string]any{
				"iw_reference_ids": map[string]any{
					"sensitive": true,
					"value":     value,
				},
			},
			"root_module": map[string]any{},
		},
		"prior_state": map[string]any{
			"values": map[string]any{
				"root_module": map[string]any{
					"child_modules": []any{
						map[string]any{
							"address": "module.sample_groups_data",
							"resources": []any{
								map[string]any{
									"address": `module.sample_groups_data.data.sample_groups_data.items["group_one"]`,
									"index":   "group_one",
									"mode":    "data",
									"name":    "items",
									"type":    "sample_groups_data",
									"values":  map[string]any{"id": json.Number("101"), "name": "Group One"},
								},
							},
						},
					},
				},
			},
		},
		"configuration": map[string]any{
			"root_module": map[string]any{
				"module_calls": map[string]any{
					"sample_groups_data": map[string]any{
						"module": map[string]any{
							"resources": []any{
								map[string]any{
									"address": "data.sample_groups_data.items",
									"mode":    "data",
									"type":    "sample_groups_data",
									"name":    "items",
								},
							},
						},
					},
				},
			},
		},
		"resource_changes": []any{},
		"resource_drift":   []any{},
		"output_changes": map[string]any{
			"iw_reference_ids": map[string]any{
				"actions":          []any{"update"},
				"before":           map[string]any{"sample_groups_data": map[string]any{"group_one": json.Number("100")}},
				"after":            value,
				"before_sensitive": true,
				"after_sensitive":  true,
				"after_unknown":    false,
			},
		},
	}
}

// TestClassifyPlanAcceptsOutputsOnlyDataReferentPlan is the group (d)
// characterization. A data-root drift snapshot has no managed resource
// changes; only the engine output changes. Assessment should accept that plan
// as clean evidence rather than refusing it as an unsupported output change.
func TestClassifyPlanAcceptsOutputsOnlyDataReferentPlan(t *testing.T) {
	contract := &plan.AssessmentPlanContract{ReferenceOutputTypes: []plan.ReferenceOutputType{{
		Type: "sample_groups_data", Kind: plan.ReferenceOutputKindData,
	}}, PlanAttestation: &plan.PlanCreationAttestation{
		FormatVersion:    plan.PlanCreationAttestationVersion,
		TerraformVersion: "1.15.4",
		PlanArgv:         []string{"plan", "-input=false", "-refresh=true", "-out=tfplan"},
		Refresh:          true,
		PlanSHA256:       strings.Repeat("a", 64),
	}}
	got, err := ClassifyPlan(outputsOnlyDataReferentPlan(), nil, contract)
	if err != nil {
		t.Fatalf("ClassifyPlan(outputs-only data referent plan) error = %v, want nil", err)
	}
	want := PlanClassification{Status: Clean, Findings: []PlanFinding{}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ClassifyPlan(outputs-only data referent plan) = %#v, want %#v", got, want)
	}

	if err := plan.ValidateAssessmentPlan(outputsOnlyDataReferentPlan(), contract); err != nil {
		t.Errorf("ValidateAssessmentPlan(outputs-only data referent plan) error = %v, want nil", err)
	}
}
