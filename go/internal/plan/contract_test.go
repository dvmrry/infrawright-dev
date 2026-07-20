package plan

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/dvmrry/infrawright-dev/go/internal/canonjson"
)

func assessmentArray(values ...any) []any {
	return values
}

func completeAssessmentPlan() map[string]any {
	return map[string]any{
		"format_version": "1.2",
		"complete":       true,
		"errored":        false,
	}
}

func cloneAssessmentValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		cloned := make(map[string]any, len(typed))
		for key, child := range typed {
			cloned[key] = cloneAssessmentValue(child)
		}
		return cloned
	case []any:
		cloned := make([]any, len(typed))
		for index, child := range typed {
			cloned[index] = cloneAssessmentValue(child)
		}
		return cloned
	default:
		return value
	}
}

func assessmentChange(actions ...string) map[string]any {
	rawActions := make([]any, len(actions))
	for index, action := range actions {
		rawActions[index] = action
	}
	return map[string]any{
		"address": "sample_resource.this",
		"type":    "sample_resource",
		"change": map[string]any{
			"actions": rawActions,
		},
	}
}

func planWithAssessmentChange(record any) map[string]any {
	plan := completeAssessmentPlan()
	plan["resource_changes"] = []any{record}
	return plan
}

func requireAssessmentPlanError(
	t *testing.T,
	operation string,
	planValue any,
	contract *AssessmentPlanContract,
	want string,
) *AssessmentPlanError {
	t.Helper()
	err := ValidateAssessmentPlan(planValue, contract)
	if err == nil {
		t.Fatalf("%s error = nil, want *AssessmentPlanError %q", operation, want)
	}
	var failure *AssessmentPlanError
	if !errors.As(err, &failure) {
		t.Fatalf("%s error = %v (%T), want *AssessmentPlanError", operation, err, err)
	}
	if failure.Error() != want {
		t.Errorf("%s error = %q, want %q", operation, failure.Error(), want)
	}
	return failure
}

func requireValidAssessmentPlan(
	t *testing.T,
	operation string,
	planValue any,
	contract *AssessmentPlanContract,
) {
	t.Helper()
	if err := ValidateAssessmentPlan(planValue, contract); err != nil {
		t.Errorf("%s error = %v, want nil", operation, err)
	}
}

func TestValidateAssessmentPlanTopLevelContractAndOrder(t *testing.T) {
	tests := []struct {
		name string
		plan any
		want string
	}{
		{
			name: "not_object",
			plan: []any{},
			want: "plan must be an object",
		},
		{
			name: "format_before_complete",
			plan: map[string]any{"complete": false, "errored": true},
			want: "plan format_version must be a supported 1.x version",
		},
		{
			name: "unsupported_format",
			plan: map[string]any{"format_version": "2.0", "complete": true, "errored": false},
			want: "plan format_version must be a supported 1.x version",
		},
		{
			name: "terraform_version_before_complete",
			plan: map[string]any{
				"format_version":    "1.2",
				"terraform_version": json.Number("1"),
				"complete":          false,
				"errored":           false,
			},
			want: "plan terraform_version must be a string when present",
		},
		{
			name: "missing_complete",
			plan: map[string]any{"format_version": "1.2", "errored": false},
			want: "plan must be complete before assessment",
		},
		{
			name: "false_complete",
			plan: map[string]any{"format_version": "1.2", "complete": false, "errored": false},
			want: "plan must be complete before assessment",
		},
		{
			name: "null_complete",
			plan: map[string]any{"format_version": "1.2", "complete": nil, "errored": false},
			want: "plan must be complete before assessment",
		},
		{
			name: "string_complete",
			plan: map[string]any{"format_version": "1.2", "complete": "true", "errored": false},
			want: "plan must be complete before assessment",
		},
		{
			name: "lossless_number_complete",
			plan: map[string]any{"format_version": "1.2", "complete": json.Number("1"), "errored": false},
			want: "plan must be complete before assessment",
		},
		{
			name: "float_complete",
			plan: map[string]any{"format_version": "1.2", "complete": float64(1), "errored": false},
			want: "plan must be complete before assessment",
		},
		{
			name: "missing_errored",
			plan: map[string]any{"format_version": "1.2", "complete": true},
			want: "errored plans cannot be assessed",
		},
		{
			name: "true_errored",
			plan: map[string]any{"format_version": "1.2", "complete": true, "errored": true},
			want: "errored plans cannot be assessed",
		},
		{
			name: "null_errored",
			plan: map[string]any{"format_version": "1.2", "complete": true, "errored": nil},
			want: "errored plans cannot be assessed",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			requireAssessmentPlanError(
				t,
				"ValidateAssessmentPlan(plan, nil)",
				test.plan,
				nil,
				test.want,
			)
		})
	}
}

func TestValidateAssessmentPlanOptionalTerraformVersion(t *testing.T) {
	for _, test := range []struct {
		name    string
		present bool
		value   any
	}{
		{name: "missing"},
		{name: "null", present: true, value: nil},
		{name: "string", present: true, value: "1.15.4"},
	} {
		t.Run(test.name, func(t *testing.T) {
			plan := completeAssessmentPlan()
			if test.present {
				plan["terraform_version"] = test.value
			}
			requireValidAssessmentPlan(t, "ValidateAssessmentPlan(plan, nil)", plan, nil)
		})
	}
}

func TestValidateAssessmentPlanResourceArraysAndLimit(t *testing.T) {
	for _, field := range []string{"resource_changes", "resource_drift"} {
		t.Run(field+"_null", func(t *testing.T) {
			plan := completeAssessmentPlan()
			plan[field] = nil
			requireAssessmentPlanError(
				t,
				"ValidateAssessmentPlan(plan, nil)",
				plan,
				nil,
				field+" must be an array",
			)
		})
	}

	validRecord := assessmentChange("create")
	atLimit := completeAssessmentPlan()
	atLimit["resource_changes"] = make([]any, MaxAssessmentChangeRecords)
	for index := range atLimit["resource_changes"].([]any) {
		atLimit["resource_changes"].([]any)[index] = validRecord
	}
	requireValidAssessmentPlan(t, "ValidateAssessmentPlan(100000 records, nil)", atLimit, nil)

	overLimit := completeAssessmentPlan()
	overLimit["resource_changes"] = make([]any, MaxAssessmentChangeRecords)
	overLimit["resource_drift"] = []any{validRecord}
	requireAssessmentPlanError(
		t,
		"ValidateAssessmentPlan(100001 records, nil)",
		overLimit,
		nil,
		"plan exceeds 100000 change records",
	)
}

func TestValidateAssessmentPlanSupportedActionSequences(t *testing.T) {
	sequences := [][]string{
		{"no-op"},
		{"create"},
		{"read"},
		{"update"},
		{"delete", "create"},
		{"create", "delete"},
		{"delete"},
		{"forget"},
		{"create", "forget"},
	}
	for _, sequence := range sequences {
		t.Run(sequence[0], func(t *testing.T) {
			record := assessmentChange(sequence...)
			if sequence[0] == "update" || sequence[0] == "no-op" {
				change := record["change"].(map[string]any)
				change["before"] = map[string]any{"id": "same"}
				change["after"] = map[string]any{"id": "same"}
			}
			requireValidAssessmentPlan(
				t,
				"ValidateAssessmentPlan(plan, nil)",
				planWithAssessmentChange(record),
				nil,
			)
		})
	}
}

func TestValidateAssessmentPlanRejectsMalformedChangeRecords(t *testing.T) {
	validUpdate := func() map[string]any {
		record := assessmentChange("update")
		change := record["change"].(map[string]any)
		change["before"] = map[string]any{}
		change["after"] = map[string]any{}
		return record
	}
	tests := []struct {
		name   string
		record func() any
		want   string
	}{
		{
			name:   "record",
			record: func() any { return "not an object" },
			want:   "resource_changes[0] must be an object",
		},
		{
			name: "address",
			record: func() any {
				record := assessmentChange("create")
				record["address"] = ""
				return record
			},
			want: "resource_changes[0].address must be a non-empty string",
		},
		{
			name: "type",
			record: func() any {
				record := assessmentChange("create")
				record["type"] = "bad-type"
				return record
			},
			want: "resource_changes[0].type must be a Terraform resource type",
		},
		{
			name: "change",
			record: func() any {
				record := assessmentChange("create")
				record["change"] = nil
				return record
			},
			want: "resource_changes[0].change must be an object",
		},
		{
			name: "empty_actions",
			record: func() any {
				record := assessmentChange()
				return record
			},
			want: "resource_changes[0].change.actions must be a non-empty string array",
		},
		{
			name: "non_string_action",
			record: func() any {
				record := assessmentChange("create")
				record["change"].(map[string]any)["actions"] = []any{"create", true}
				return record
			},
			want: "resource_changes[0].change.actions must be a non-empty string array",
		},
		{
			name: "non_string_precedes_duplicate",
			record: func() any {
				record := assessmentChange("create")
				record["change"].(map[string]any)["actions"] = []any{"create", "create", true}
				return record
			},
			want: "resource_changes[0].change.actions must be a non-empty string array",
		},
		{
			name: "duplicate_action",
			record: func() any {
				record := assessmentChange("update", "update")
				return record
			},
			want: "resource_changes[0].change.actions must not contain duplicates",
		},
		{
			name: "unsupported_action_sequence",
			record: func() any {
				return assessmentChange("update", "read")
			},
			want: "resource_changes[0].change.actions is not a supported Terraform action sequence",
		},
		{
			name: "top_level_importing",
			record: func() any {
				record := assessmentChange("create")
				record["importing"] = nil
				return record
			},
			want: "resource_changes[0].importing is not part of the Terraform resource-change contract",
		},
		{
			name: "update_bindings",
			record: func() any {
				return assessmentChange("update")
			},
			want: "resource_changes[0].change must bind before and after values",
		},
		{
			name: "no_op_values",
			record: func() any {
				record := assessmentChange("no-op")
				change := record["change"].(map[string]any)
				change["before"] = map[string]any{"id": json.Number("1")}
				change["after"] = map[string]any{"id": json.Number("2")}
				return record
			},
			want: "resource_changes[0].change no-op values must be identical",
		},
		{
			name: "unknown_mask_shape",
			record: func() any {
				record := validUpdate()
				record["change"].(map[string]any)["after_unknown"] = map[string]any{"token": "true"}
				return record
			},
			want: "resource_changes[0].change.after_unknown.token must be a recursive boolean mask",
		},
		{
			name: "sensitivity_mask_shape",
			record: func() any {
				record := validUpdate()
				record["change"].(map[string]any)["before_sensitive"] = []any{false, "true"}
				return record
			},
			want: "resource_changes[0].change.before_sensitive[1] must be a recursive boolean mask",
		},
		{
			name: "no_op_unknown",
			record: func() any {
				record := assessmentChange("no-op")
				change := record["change"].(map[string]any)
				change["before"] = map[string]any{}
				change["after"] = map[string]any{}
				change["after_unknown"] = map[string]any{"token": true}
				return record
			},
			want: "resource_changes[0].change no-op must not contain unknown values",
		},
		{
			name: "no_op_metadata",
			record: func() any {
				record := assessmentChange("no-op")
				change := record["change"].(map[string]any)
				change["before"] = map[string]any{}
				change["after"] = map[string]any{}
				change["before_identity"] = map[string]any{"id": "old"}
				change["after_identity"] = map[string]any{"id": "new"}
				return record
			},
			want: "resource_changes[0].change no-op metadata must be identical",
		},
		{
			name: "nested_importing",
			record: func() any {
				record := assessmentChange("create")
				record["change"].(map[string]any)["importing"] = "secret-id"
				return record
			},
			want: "resource_changes[0].change importing marker must be an object",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			requireAssessmentPlanError(
				t,
				"ValidateAssessmentPlan(plan, nil)",
				planWithAssessmentChange(test.record()),
				nil,
				test.want,
			)
		})
	}
}

func TestValidateAssessmentPlanNoOpNumericAndBooleanEquality(t *testing.T) {
	compatible, err := canonjson.ParseDataJSONLosslessly(
		`{"format_version":"1.2","complete":true,"errored":false,` +
			`"resource_changes":[{"address":"sample_resource.this",` +
			`"type":"sample_resource","change":{"actions":["no-op"],` +
			`"before":{"flag":true,"value":1},` +
			`"after":{"flag":true,"value":1.0}}}]}`,
	)
	if err != nil {
		t.Fatalf("ParseDataJSONLosslessly(compatible) error = %v, want nil", err)
	}
	requireValidAssessmentPlan(t, "ValidateAssessmentPlan(compatible, nil)", compatible, nil)

	unsafeInteger, err := canonjson.ParseDataJSONLosslessly(
		`{"format_version":"1.2","complete":true,"errored":false,` +
			`"resource_changes":[{"address":"sample_resource.this",` +
			`"type":"sample_resource","change":{"actions":["no-op"],` +
			`"before":{"value":9007199254740993},` +
			`"after":{"value":9007199254740993.0}}}]}`,
	)
	if err != nil {
		t.Fatalf("ParseDataJSONLosslessly(unsafeInteger) error = %v, want nil", err)
	}
	requireAssessmentPlanError(
		t,
		"ValidateAssessmentPlan(unsafeInteger, nil)",
		unsafeInteger,
		nil,
		"resource_changes[0].change no-op values must be identical",
	)

	booleanNumber, err := canonjson.ParseDataJSONLosslessly(
		`{"format_version":"1.2","complete":true,"errored":false,` +
			`"resource_changes":[{"address":"sample_resource.this",` +
			`"type":"sample_resource","change":{"actions":["no-op"],` +
			`"before":{"value":true},"after":{"value":1}}}]}`,
	)
	if err != nil {
		t.Fatalf("ParseDataJSONLosslessly(booleanNumber) error = %v, want nil", err)
	}
	requireAssessmentPlanError(
		t,
		"ValidateAssessmentPlan(booleanNumber, nil)",
		booleanNumber,
		nil,
		"resource_changes[0].change no-op values must be identical",
	)
}

// TestValidateAssessmentPlanSortsDiagnosticOnlyObjectViolations pins the
// explicit map-order parity exception in contract.go. Node reports the first
// invalid Object.entries member in insertion order, but canonjson's Go maps no
// longer retain that order. Sorting therefore makes the underlying
// AssessmentPlanError deterministic when one object contains multiple invalid
// members. Assessment orchestration redacts this diagnostic, so the exception
// cannot affect report or CLI bytes.
func TestValidateAssessmentPlanSortsDiagnosticOnlyObjectViolations(t *testing.T) {
	record := assessmentChange("update")
	change := record["change"].(map[string]any)
	change["before"] = map[string]any{}
	change["after"] = map[string]any{}
	change["after_unknown"] = map[string]any{
		"z_invalid": "not a mask",
		"a_invalid": "not a mask",
	}
	requireAssessmentPlanError(
		t,
		"ValidateAssessmentPlan(multi-invalid mask, nil)",
		planWithAssessmentChange(record),
		nil,
		"resource_changes[0].change.after_unknown.a_invalid must be a recursive boolean mask",
	)
}

func TestValidateAssessmentPlanImportMarkers(t *testing.T) {
	for _, marker := range []any{nil, map[string]any{}, map[string]any{
		"identity": map[string]any{"account_id": "example"},
	}} {
		record := assessmentChange("create")
		record["change"].(map[string]any)["importing"] = marker
		requireValidAssessmentPlan(
			t,
			"ValidateAssessmentPlan(plan with importing marker, nil)",
			planWithAssessmentChange(record),
			nil,
		)
	}

	noOp := assessmentChange("no-op")
	change := noOp["change"].(map[string]any)
	change["before"] = map[string]any{"secret": "same"}
	change["after"] = map[string]any{"secret": "same"}
	change["before_sensitive"] = map[string]any{"secret": true}
	change["after_sensitive"] = map[string]any{}
	change["importing"] = map[string]any{"id": "x"}
	requireAssessmentPlanError(
		t,
		"ValidateAssessmentPlan(no-op sensitivity change, nil)",
		planWithAssessmentChange(noOp),
		nil,
		"resource_changes[0].change no-op metadata must be identical",
	)
}

func TestValidateAssessmentPlanOutputsEmptyFieldsAndChecks(t *testing.T) {
	valid := completeAssessmentPlan()
	valid["output_changes"] = map[string]any{
		"summary": map[string]any{
			"actions":          []any{"no-op"},
			"before":           "same",
			"after":            "same",
			"after_unknown":    map[string]any{"nested": []any{false}},
			"before_sensitive": false,
			"after_sensitive":  false,
		},
	}
	valid["action_invocations"] = []any{}
	valid["deferred_changes"] = []any{}
	valid["deferred_action_invocations"] = []any{}
	valid["checks"] = []any{
		map[string]any{
			"status": "unknown",
			"instances": []any{
				map[string]any{"status": "pass"},
				map[string]any{"status": "unknown"},
			},
		},
	}
	requireValidAssessmentPlan(t, "ValidateAssessmentPlan(valid, nil)", valid, nil)

	tests := []struct {
		name   string
		mutate func(map[string]any)
		want   string
	}{
		{
			name:   "output_array",
			mutate: func(plan map[string]any) { plan["output_changes"] = []any{} },
			want:   "output_changes must be an object",
		},
		{
			name: "output_missing_actions",
			mutate: func(plan map[string]any) {
				plan["output_changes"] = map[string]any{"summary": map[string]any{}}
			},
			want: "output_changes entries must contain actions",
		},
		{
			name: "output_update",
			mutate: func(plan map[string]any) {
				plan["output_changes"] = map[string]any{
					"summary": map[string]any{"actions": []any{"update"}},
				}
			},
			want: "non-no-op output changes are not supported by saved-plan assessment",
		},
		{
			name: "output_no_op_values",
			mutate: func(plan map[string]any) {
				plan["output_changes"] = map[string]any{
					"summary": map[string]any{
						"actions": []any{"no-op"}, "before": "before", "after": "after",
					},
				}
			},
			want: "output no-op values must be identical",
		},
		{
			name: "output_unknown",
			mutate: func(plan map[string]any) {
				plan["output_changes"] = map[string]any{
					"summary": map[string]any{
						"actions": []any{"no-op"}, "before": "same", "after": "same",
						"after_unknown": true,
					},
				}
			},
			want: "output no-op must not contain unknown values",
		},
		{
			name: "output_sensitivity",
			mutate: func(plan map[string]any) {
				plan["output_changes"] = map[string]any{
					"summary": map[string]any{
						"actions": []any{"no-op"}, "before": "same", "after": "same",
						"before_sensitive": false, "after_sensitive": true,
					},
				}
			},
			want: "output no-op sensitivity metadata must be identical",
		},
		{
			name:   "action_invocations_type",
			mutate: func(plan map[string]any) { plan["action_invocations"] = map[string]any{} },
			want:   "action_invocations must be an array",
		},
		{
			name:   "deferred_changes_nonempty",
			mutate: func(plan map[string]any) { plan["deferred_changes"] = []any{map[string]any{}} },
			want:   "deferred_changes is not supported by saved-plan assessment",
		},
		{
			name:   "deferred_action_invocations_type",
			mutate: func(plan map[string]any) { plan["deferred_action_invocations"] = nil },
			want:   "deferred_action_invocations must be an array",
		},
		{
			name:   "checks_type",
			mutate: func(plan map[string]any) { plan["checks"] = nil },
			want:   "checks must be an array",
		},
		{
			name:   "check_status",
			mutate: func(plan map[string]any) { plan["checks"] = []any{map[string]any{"status": "future"}} },
			want:   "checks[0].status is invalid",
		},
		{
			name:   "failed_check",
			mutate: func(plan map[string]any) { plan["checks"] = []any{map[string]any{"status": "fail"}} },
			want:   "failed Terraform checks are not supported by saved-plan assessment",
		},
		{
			name: "check_instances_type",
			mutate: func(plan map[string]any) {
				plan["checks"] = []any{map[string]any{"status": "pass", "instances": nil}}
			},
			want: "checks[0].instances must be an array",
		},
		{
			name: "failed_instance",
			mutate: func(plan map[string]any) {
				plan["checks"] = []any{map[string]any{
					"status": "pass", "instances": []any{map[string]any{"status": "error"}},
				}}
			},
			want: "failed Terraform checks are not supported by saved-plan assessment",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			plan := completeAssessmentPlan()
			test.mutate(plan)
			requireAssessmentPlanError(
				t,
				"ValidateAssessmentPlan(plan, nil)",
				plan,
				nil,
				test.want,
			)
		})
	}
}

func referenceAssessmentPlan(action string) map[string]any {
	value := map[string]any{
		"zpa_segment_group": map[string]any{"segment_one": "72059380790653545"},
	}
	before := any(nil)
	beforeSensitive := any(false)
	if action == "update" {
		before = map[string]any{"zpa_segment_group": map[string]any{}}
		beforeSensitive = true
	}
	if action == "no-op" {
		before = value
		beforeSensitive = true
	}
	return map[string]any{
		"format_version": "1.2",
		"complete":       true,
		"errored":        false,
		"planned_values": map[string]any{
			"outputs": map[string]any{
				infrawrightReferenceOutput: map[string]any{"sensitive": true, "value": value},
			},
			"root_module": map[string]any{
				"child_modules": []any{
					map[string]any{
						"address": "module.zpa_segment_group",
						"resources": []any{
							map[string]any{
								"address": `module.zpa_segment_group.zpa_segment_group.this["segment_one"]`,
								"index":   "segment_one",
								"mode":    "managed",
								"type":    "zpa_segment_group",
								"values":  map[string]any{"id": "72059380790653545", "name": "Segment One"},
							},
						},
					},
				},
			},
		},
		"resource_changes": []any{},
		"output_changes": map[string]any{
			infrawrightReferenceOutput: map[string]any{
				"actions":          []any{action},
				"before":           before,
				"after":            value,
				"before_sensitive": beforeSensitive,
				"after_sensitive":  true,
				"after_unknown":    false,
			},
		},
	}
}

func referenceContract() *AssessmentPlanContract {
	return &AssessmentPlanContract{ReferenceOutputTypes: []string{"zpa_segment_group"}}
}

func TestValidateAssessmentPlanAuthorizesBoundReferenceOutput(t *testing.T) {
	for _, action := range []string{"create", "update", "no-op"} {
		t.Run(action, func(t *testing.T) {
			plan := referenceAssessmentPlan(action)
			requireValidAssessmentPlan(
				t,
				"ValidateAssessmentPlan(reference plan, contract)",
				plan,
				referenceContract(),
			)
			if action == "create" {
				requireAssessmentPlanError(
					t,
					"ValidateAssessmentPlan(reference plan, nil)",
					plan,
					nil,
					"non-no-op output changes are not supported by saved-plan assessment",
				)
			}
		})
	}
}

func TestValidateAssessmentPlanRejectsInvalidReferenceContractAndEvidence(t *testing.T) {
	for _, test := range []struct {
		name     string
		contract *AssessmentPlanContract
		want     string
	}{
		{
			name:     "duplicate_types",
			contract: &AssessmentPlanContract{ReferenceOutputTypes: []string{"zpa_segment_group", "zpa_segment_group"}},
			want:     "reference output contract must contain unique Terraform resource types",
		},
		{
			name:     "malformed_type",
			contract: &AssessmentPlanContract{ReferenceOutputTypes: []string{"bad-type"}},
			want:     "reference output contract must contain unique Terraform resource types",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			requireAssessmentPlanError(
				t,
				"ValidateAssessmentPlan(reference plan, contract)",
				referenceAssessmentPlan("create"),
				test.contract,
				test.want,
			)
		})
	}

	tests := []struct {
		name   string
		mutate func(map[string]any)
		want   string
	}{
		{
			name: "missing_output_changes",
			mutate: func(plan map[string]any) {
				delete(plan, "output_changes")
			},
			want: "reference output contract requires output_changes evidence",
		},
		{
			name: "missing_engine_change",
			mutate: func(plan map[string]any) {
				plan["output_changes"] = map[string]any{}
			},
			want: "reference output contract requires the engine output change",
		},
		{
			name: "wrong_after",
			mutate: func(plan map[string]any) {
				change := plan["output_changes"].(map[string]any)[infrawrightReferenceOutput].(map[string]any)
				change["after"] = map[string]any{"zpa_segment_group": map[string]any{"segment_one": "wrong"}}
			},
			want: "engine reference output does not match provider-observed resource IDs",
		},
		{
			name: "unknown_after",
			mutate: func(plan map[string]any) {
				change := plan["output_changes"].(map[string]any)[infrawrightReferenceOutput].(map[string]any)
				change["after_unknown"] = true
			},
			want: "engine reference output must be fully known",
		},
		{
			name: "not_sensitive",
			mutate: func(plan map[string]any) {
				change := plan["output_changes"].(map[string]any)[infrawrightReferenceOutput].(map[string]any)
				change["after_sensitive"] = false
			},
			want: "engine reference output must remain sensitive",
		},
		{
			name: "planned_output_not_sensitive",
			mutate: func(plan map[string]any) {
				plannedValues := plan["planned_values"].(map[string]any)
				output := plannedValues["outputs"].(map[string]any)[infrawrightReferenceOutput].(map[string]any)
				output["sensitive"] = false
			},
			want: "planned engine reference output does not match provider-observed resource IDs",
		},
		{
			name: "duplicate_child",
			mutate: func(plan map[string]any) {
				plannedValues := plan["planned_values"].(map[string]any)
				root := plannedValues["root_module"].(map[string]any)
				child := root["child_modules"].([]any)[0]
				root["child_modules"] = []any{child, child}
			},
			want: "reference output authorization permits at most one module.zpa_segment_group child module",
		},
		{
			name: "unsupported_action",
			mutate: func(plan map[string]any) {
				change := plan["output_changes"].(map[string]any)[infrawrightReferenceOutput].(map[string]any)
				change["actions"] = []any{"delete"}
				change["after"] = nil
			},
			want: "engine reference output permits only create, update, or no-op actions",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			plan := referenceAssessmentPlan("create")
			test.mutate(plan)
			requireAssessmentPlanError(
				t,
				"ValidateAssessmentPlan(reference plan, contract)",
				plan,
				referenceContract(),
				test.want,
			)
		})
	}
}

func emptyReferenceAssessmentPlan() map[string]any {
	value := map[string]any{"zpa_segment_group": map[string]any{}}
	return map[string]any{
		"format_version": "1.2",
		"complete":       true,
		"errored":        false,
		"planned_values": map[string]any{
			"outputs": map[string]any{
				infrawrightReferenceOutput: map[string]any{"sensitive": true, "value": value},
			},
			"root_module": map[string]any{},
		},
		"configuration": map[string]any{
			"root_module": map[string]any{
				"module_calls": map[string]any{
					"zpa_segment_group": map[string]any{
						"module": map[string]any{
							"resources": []any{
								map[string]any{
									"address": "zpa_segment_group.this",
									"mode":    "managed",
									"type":    "zpa_segment_group",
									"name":    "this",
								},
							},
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
				"before_sensitive": true,
				"after_sensitive":  true,
				"after_unknown":    false,
			},
		},
	}
}

func TestValidateAssessmentPlanEmptyReferenceRequiresConfigurationAuthority(t *testing.T) {
	plan := emptyReferenceAssessmentPlan()
	requireValidAssessmentPlan(
		t,
		"ValidateAssessmentPlan(empty reference, contract)",
		plan,
		referenceContract(),
	)

	configuration := plan["configuration"].(map[string]any)
	root := configuration["root_module"].(map[string]any)
	moduleCalls := root["module_calls"].(map[string]any)
	delete(moduleCalls, "zpa_segment_group")
	requireAssessmentPlanError(
		t,
		"ValidateAssessmentPlan(empty reference without module, contract)",
		plan,
		referenceContract(),
		"empty reference output authorization requires module.zpa_segment_group",
	)
}

func TestValidateAssessmentPlanDoesNotMutateContractOrPlan(t *testing.T) {
	plan := referenceAssessmentPlan("create")
	contract := referenceContract()
	beforeTypes := append([]string(nil), contract.ReferenceOutputTypes...)
	beforePlan := cloneAssessmentValue(plan)
	requireValidAssessmentPlan(
		t,
		"ValidateAssessmentPlan(reference plan, contract)",
		plan,
		contract,
	)
	if !reflect.DeepEqual(contract.ReferenceOutputTypes, beforeTypes) {
		t.Errorf(
			"ValidateAssessmentPlan contract.ReferenceOutputTypes = %v, want unchanged %v",
			contract.ReferenceOutputTypes,
			beforeTypes,
		)
	}
	if !reflect.DeepEqual(plan, beforePlan) {
		t.Errorf(
			"ValidateAssessmentPlan plan = %#v, want unchanged %#v",
			plan,
			beforePlan,
		)
	}
}
