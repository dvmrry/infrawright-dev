package plan

// Phase 5 (alternate ID spaces): saved-plan authorization for the
// iw_reference_ids_<field> sibling outputs, per
// docs/superpowers/specs/2026-08-04-referent-alternate-id-spaces.md, surface
// 5. These tests extend the referenceAssessmentPlan/referenceContract
// fixtures used by TestValidateAssessmentPlanAuthorizesBoundReferenceOutput
// to cover the sibling's own reference-output authorization path.

import "testing"

// siblingReferenceAssessmentPlan builds a plan carrying both the canonical
// iw_reference_ids output (proven, no-op) and an
// iw_reference_ids_<field> sibling exercising action for the same
// zpa_segment_group instance, whose provider-observed values.<field> is
// siblingValue. This is the shape of a referent root's FIRST plan after
// declaring the space: the canonical output is already steady-state
// no-op, and the sibling carries the new create/update claim.
func siblingReferenceAssessmentPlan(field string, action string, siblingValue any, afterValue map[string]any) map[string]any {
	plan := referenceAssessmentPlan("no-op")

	root := plan["planned_values"].(map[string]any)["root_module"].(map[string]any)
	child := root["child_modules"].([]any)[0].(map[string]any)
	resource := child["resources"].([]any)[0].(map[string]any)
	values := resource["values"].(map[string]any)
	values[field] = siblingValue

	outputName := infrawrightReferenceOutput + "_" + field
	plan["planned_values"].(map[string]any)["outputs"].(map[string]any)[outputName] =
		map[string]any{"sensitive": true, "value": afterValue}

	before := any(nil)
	beforeSensitive := any(false)
	if action == "update" {
		before = map[string]any{"zpa_segment_group": map[string]any{}}
		beforeSensitive = true
	}
	if action == "no-op" {
		before = afterValue
		beforeSensitive = true
	}
	plan["output_changes"].(map[string]any)[outputName] = map[string]any{
		"actions":          []any{action},
		"before":           before,
		"after":            afterValue,
		"before_sensitive": beforeSensitive,
		"after_sensitive":  true,
		"after_unknown":    false,
	}
	return plan
}

// TestValidateAssessmentPlanAuthorizesSiblingReferenceOutputCreate is THE
// acceptance case: a referent root's first plan after declaring an
// alternate space is authorized when the sibling's after value matches the
// provider-observed values.<field> at the contracted resource instance.
func TestValidateAssessmentPlanAuthorizesSiblingReferenceOutputCreate(t *testing.T) {
	afterValue := map[string]any{"zpa_segment_group": map[string]any{"segment_one": "56"}}
	plan := siblingReferenceAssessmentPlan("val", "create", "56", afterValue)
	requireValidAssessmentPlan(
		t,
		"ValidateAssessmentPlan(sibling create, contract)",
		plan,
		referenceContract(),
	)
}

// TestValidateAssessmentPlanAuthorizesSiblingReferenceOutputNoOp confirms a
// recognized sibling with a lone no-op action goes through the same
// no-op gate as the canonical output, not the reference-output proof.
func TestValidateAssessmentPlanAuthorizesSiblingReferenceOutputNoOp(t *testing.T) {
	afterValue := map[string]any{"zpa_segment_group": map[string]any{"segment_one": "56"}}
	plan := siblingReferenceAssessmentPlan("val", "no-op", "56", afterValue)
	requireValidAssessmentPlan(
		t,
		"ValidateAssessmentPlan(sibling no-op, contract)",
		plan,
		referenceContract(),
	)
}

// TestValidateAssessmentPlanRefusesSiblingReferenceOutputMismatch confirms a
// sibling create whose after-value does not match the provider-observed
// values.<field> is refused, exactly like the canonical output's
// wrong_after case.
func TestValidateAssessmentPlanRefusesSiblingReferenceOutputMismatch(t *testing.T) {
	afterValue := map[string]any{"zpa_segment_group": map[string]any{"segment_one": "wrong"}}
	plan := siblingReferenceAssessmentPlan("val", "create", "56", afterValue)
	requireAssessmentPlanError(
		t,
		"ValidateAssessmentPlan(sibling create mismatch, contract)",
		plan,
		referenceContract(),
		"engine reference output does not match provider-observed resource IDs",
	)
}

// TestValidateAssessmentPlanRefusesSiblingReferenceOutputUndeclaredType
// confirms a sibling create claiming a resourceType the active reference
// contract does not admit is refused, rather than silently reconstructed
// against nothing.
func TestValidateAssessmentPlanRefusesSiblingReferenceOutputUndeclaredType(t *testing.T) {
	afterValue := map[string]any{"zpa_app_connector_group": map[string]any{"segment_one": "56"}}
	plan := siblingReferenceAssessmentPlan("val", "create", "56", afterValue)
	requireAssessmentPlanError(
		t,
		"ValidateAssessmentPlan(sibling create undeclared type, contract)",
		plan,
		referenceContract(),
		"engine reference output does not match provider-observed resource IDs",
	)
}

// TestValidateAssessmentPlanFallsThroughOnMalformedSiblingName confirms a
// malformed suffix -- no separator, or an empty field -- is NOT recognized
// as a sibling and falls to the generic no-op-only gate, refusing a create
// exactly like any other unexpected output.
func TestValidateAssessmentPlanFallsThroughOnMalformedSiblingName(t *testing.T) {
	for _, name := range []string{
		"iw_reference_idsfoo",
		"iw_reference_ids_",
	} {
		t.Run(name, func(t *testing.T) {
			plan := referenceAssessmentPlan("no-op")
			plan["output_changes"].(map[string]any)[name] = map[string]any{
				"actions":          []any{"create"},
				"before":           nil,
				"after":            map[string]any{"zpa_segment_group": map[string]any{"segment_one": "56"}},
				"before_sensitive": false,
				"after_sensitive":  true,
				"after_unknown":    false,
			}
			requireAssessmentPlanError(
				t,
				"ValidateAssessmentPlan(malformed sibling name, contract)",
				plan,
				referenceContract(),
				"non-no-op output changes are not supported by saved-plan assessment",
			)
		})
	}
}

// TestValidateAssessmentPlanRefusesSiblingOnDataReferentRoot confirms that,
// even though the data-kind evidence path is out of scope for siblings, an
// unexpected sibling output name on a data-referent root's plan still fails
// closed through the generic no-op-only gate -- v1 packs cannot declare a
// sibling for a data referent, so there is no separate sibling-recognition
// branch to reach for a data-kind contract; recognition itself refuses a
// non-no-op sibling claiming the data-kind resourceType.
func TestValidateAssessmentPlanRefusesSiblingOnDataReferentRoot(t *testing.T) {
	dataContract := dataReferenceContractFor("zpa_segment_group")
	afterValue := map[string]any{"zpa_segment_group": map[string]any{"segment_one": "56"}}
	plan := siblingReferenceAssessmentPlan("val", "create", "56", afterValue)
	requireAssessmentPlanError(
		t,
		"ValidateAssessmentPlan(sibling on data-referent root, contract)",
		plan,
		dataContract,
		"engine reference output does not match provider-observed resource IDs",
	)
}
