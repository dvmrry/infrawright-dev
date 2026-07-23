package plan

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/dvmrry/infrawright-dev/go/internal/canonjson"
)

const infrawrightReferenceOutput = "infrawright_reference_ids"

// MaxAssessmentChangeRecords ports MAX_ASSESSMENT_CHANGE_RECORDS from
// node-src/domain/plan-contract.ts.
const MaxAssessmentChangeRecords = 100_000

var (
	assessmentFormatVersion = regexp.MustCompile(`^1\.[0-9]+$`)
	assessmentResourceType  = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
)

// AssessmentPlanError reports a plan-contract validation failure. It ports
// AssessmentPlanError from node-src/domain/plan-contract.ts.
type AssessmentPlanError struct {
	message string
}

// Error implements error.
func (e *AssessmentPlanError) Error() string { return e.message }

// AssessmentPlanContract ports AssessmentPlanContract from
// node-src/domain/plan-contract.ts. ReferenceOutputTypes is not retained or
// mutated by ValidateAssessmentPlan.
type AssessmentPlanContract struct {
	ReferenceOutputTypes []string
}

func assessmentFail(message string) {
	panic(&AssessmentPlanError{message: message})
}

func assessmentFailf(format string, args ...any) {
	assessmentFail(fmt.Sprintf(format, args...))
}

func recoverAssessmentPlanError(err *error) {
	if recovered := recover(); recovered != nil {
		failure, ok := recovered.(*AssessmentPlanError)
		if !ok {
			panic(recovered)
		}
		*err = failure
	}
}

func assessmentObject(value any) (map[string]any, bool) {
	object, ok := value.(map[string]any)
	return object, ok
}

// assessmentObjectKeys gives validation a deterministic order over Go's
// unordered map representation. Node's Object.entries uses source insertion
// order; that ordering is unavailable after canonjson decoding, and affects
// only which of multiple simultaneous nested violations is reported first.
func assessmentObjectKeys(value map[string]any) []string {
	keys := make([]string, 0, len(value))
	for key := range value {
		keys = append(keys, key)
	}
	return canonjson.SortedStrings(keys)
}

func validateImportMarker(value any, present bool, where string) {
	if !present || value == nil {
		return
	}
	if _, ok := assessmentObject(value); !ok {
		assessmentFailf("%s importing marker must be an object", where)
	}
}

func validateBooleanMask(value any, where string) {
	if _, ok := value.(bool); ok {
		return
	}
	if array, ok := value.([]any); ok {
		for index, child := range array {
			validateBooleanMask(child, fmt.Sprintf("%s[%d]", where, index))
		}
		return
	}
	if object, ok := assessmentObject(value); ok {
		for _, key := range assessmentObjectKeys(object) {
			validateBooleanMask(object[key], where+"."+key)
		}
		return
	}
	assessmentFailf("%s must be a recursive boolean mask", where)
}

func booleanMaskHasTrue(value any) bool {
	if boolean, ok := value.(bool); ok {
		return boolean
	}
	if array, ok := value.([]any); ok {
		for _, child := range array {
			if booleanMaskHasTrue(child) {
				return true
			}
		}
		return false
	}
	if object, ok := assessmentObject(value); ok {
		for _, child := range object {
			if booleanMaskHasTrue(child) {
				return true
			}
		}
	}
	return false
}

func supportedActionSequence(actions []any) bool {
	if len(actions) == 1 {
		action, ok := actions[0].(string)
		if !ok {
			return false
		}
		switch action {
		case "no-op", "create", "read", "update", "delete", "forget":
			return true
		default:
			return false
		}
	}
	if len(actions) != 2 {
		return false
	}
	first, firstOK := actions[0].(string)
	second, secondOK := actions[1].(string)
	if !firstOK || !secondOK {
		return false
	}
	return first == "delete" && second == "create" ||
		first == "create" && (second == "delete" || second == "forget")
}

func validateChangeRecord(value any, where string) {
	record, ok := assessmentObject(value)
	if !ok {
		assessmentFailf("%s must be an object", where)
	}
	address, ok := record["address"].(string)
	if !ok || address == "" {
		assessmentFailf("%s.address must be a non-empty string", where)
	}
	resourceType, ok := record["type"].(string)
	if !ok || !assessmentResourceType.MatchString(resourceType) {
		assessmentFailf("%s.type must be a Terraform resource type", where)
	}
	change, ok := assessmentObject(record["change"])
	if !ok {
		assessmentFailf("%s.change must be an object", where)
	}
	actions, ok := change["actions"].([]any)
	if !ok || len(actions) == 0 {
		assessmentFailf("%s.change.actions must be a non-empty string array", where)
	}
	seen := make(map[string]struct{}, len(actions))
	for _, rawAction := range actions {
		if _, stringOK := rawAction.(string); !stringOK {
			assessmentFailf("%s.change.actions must be a non-empty string array", where)
		}
	}
	for _, rawAction := range actions {
		action := rawAction.(string)
		if _, duplicate := seen[action]; duplicate {
			assessmentFailf("%s.change.actions must not contain duplicates", where)
		}
		seen[action] = struct{}{}
	}
	if !supportedActionSequence(actions) {
		assessmentFailf("%s.change.actions is not a supported Terraform action sequence", where)
	}
	if _, present := record["importing"]; present {
		assessmentFailf("%s.importing is not part of the Terraform resource-change contract", where)
	}
	firstAction := actions[0].(string)
	if firstAction == "update" || firstAction == "no-op" {
		_, hasBefore := change["before"]
		_, hasAfter := change["after"]
		if !hasBefore || !hasAfter {
			assessmentFailf("%s.change must bind before and after values", where)
		}
	}
	if len(actions) == 1 && firstAction == "no-op" &&
		!canonjson.TerraformJSONEqual(change["before"], change["after"]) {
		assessmentFailf("%s.change no-op values must be identical", where)
	}
	if afterUnknown, present := change["after_unknown"]; present {
		validateBooleanMask(afterUnknown, where+".change.after_unknown")
		if len(actions) == 1 && firstAction == "no-op" && booleanMaskHasTrue(afterUnknown) {
			assessmentFailf("%s.change no-op must not contain unknown values", where)
		}
	}
	for _, field := range [...]string{"before_sensitive", "after_sensitive"} {
		if mask, present := change[field]; present {
			validateBooleanMask(mask, where+".change."+field)
		}
	}
	if len(actions) == 1 && firstAction == "no-op" {
		beforeIdentity := change["before_identity"]
		afterIdentity := change["after_identity"]
		beforeSensitive, beforePresent := change["before_sensitive"]
		if !beforePresent || beforeSensitive == nil {
			beforeSensitive = map[string]any{}
		}
		afterSensitive, afterPresent := change["after_sensitive"]
		if !afterPresent || afterSensitive == nil {
			afterSensitive = map[string]any{}
		}
		if !canonjson.TerraformJSONEqual(beforeIdentity, afterIdentity) ||
			!canonjson.TerraformJSONEqual(beforeSensitive, afterSensitive) {
			assessmentFailf("%s.change no-op metadata must be identical", where)
		}
	}
	importing, present := change["importing"]
	validateImportMarker(importing, present, where+".change")
}

func assessmentRecords(plan map[string]any, field string) []any {
	value, present := plan[field]
	if !present {
		return nil
	}
	records, ok := value.([]any)
	if !ok {
		assessmentFailf("%s must be an array", field)
	}
	return records
}

func validateEmptyArray(plan map[string]any, field string) {
	value, present := plan[field]
	if !present {
		return
	}
	array, ok := value.([]any)
	if !ok {
		assessmentFailf("%s must be an array", field)
	}
	if len(array) > 0 {
		assessmentFailf("%s is not supported by saved-plan assessment", field)
	}
}

func referenceOutputValue(
	plan map[string]any,
	resourceTypes []string,
) map[string]any {
	seenTypes := make(map[string]struct{}, len(resourceTypes))
	for _, resourceType := range resourceTypes {
		if _, duplicate := seenTypes[resourceType]; duplicate ||
			!assessmentResourceType.MatchString(resourceType) {
			assessmentFail("reference output contract must contain unique Terraform resource types")
		}
		seenTypes[resourceType] = struct{}{}
	}
	if len(resourceTypes) == 0 {
		assessmentFail("reference output contract must contain unique Terraform resource types")
	}

	plannedValues, ok := assessmentObject(plan["planned_values"])
	if !ok {
		assessmentFail("reference output authorization requires planned root-module values")
	}
	rootModule, ok := assessmentObject(plannedValues["root_module"])
	if !ok {
		assessmentFail("reference output authorization requires planned root-module values")
	}
	var childModules []any
	if rawChildren, present := rootModule["child_modules"]; present {
		childModules, ok = rawChildren.([]any)
		if !ok {
			assessmentFail("planned child modules must be an array")
		}
	}

	expected := make(map[string]any, len(resourceTypes))
	for _, resourceType := range resourceTypes {
		address := "module." + resourceType
		ids := make(map[string]any)
		matches := make([]map[string]any, 0, 1)
		for _, rawChild := range childModules {
			child, childOK := assessmentObject(rawChild)
			if childOK && child["address"] == address {
				matches = append(matches, child)
			}
		}
		if len(matches) > 1 {
			assessmentFailf("reference output authorization permits at most one %s child module", address)
		}
		if len(matches) == 1 {
			child := matches[0]
			resourcesValue, present := child["resources"]
			if !present || resourcesValue == nil {
				resourcesValue = []any{}
			}
			resources, resourcesOK := resourcesValue.([]any)
			if !resourcesOK {
				assessmentFailf("%s.resources must be an array", address)
			}
			for _, rawResource := range resources {
				resource, resourceOK := assessmentObject(rawResource)
				if !resourceOK {
					assessmentFailf("%s.resources entries must be objects", address)
				}
				if resource["mode"] != "managed" || resource["type"] != resourceType {
					continue
				}
				resourceAddress, addressOK := resource["address"].(string)
				index, indexOK := resource["index"].(string)
				values, valuesOK := assessmentObject(resource["values"])
				id, idOK := values["id"].(string)
				if !addressOK || !strings.HasPrefix(resourceAddress, address+"."+resourceType+".this[") ||
					!indexOK || !valuesOK || !idOK {
					assessmentFailf("%s contains an invalid reference-output resource instance", address)
				}
				if _, duplicate := ids[index]; duplicate {
					assessmentFailf("%s contains a duplicate reference-output key", address)
				}
				ids[index] = id
			}
		}
		if len(ids) == 0 {
			validateEmptyReferenceModule(plan, resourceType)
		}
		expected[resourceType] = ids
	}

	outputs, ok := assessmentObject(plannedValues["outputs"])
	if !ok {
		assessmentFail("reference output authorization requires the planned engine output")
	}
	plannedOutput, ok := assessmentObject(outputs[infrawrightReferenceOutput])
	if !ok {
		assessmentFail("reference output authorization requires the planned engine output")
	}
	outputValue, hasValue := plannedOutput["value"]
	if plannedOutput["sensitive"] != true || !hasValue ||
		!canonjson.TerraformJSONEqual(outputValue, expected) {
		assessmentFail("planned engine reference output does not match provider-observed resource IDs")
	}
	return expected
}

func validateEmptyReferenceModule(plan map[string]any, resourceType string) {
	configuration, ok := assessmentObject(plan["configuration"])
	if !ok {
		assessmentFail("empty reference output authorization requires root-module configuration")
	}
	rootModule, ok := assessmentObject(configuration["root_module"])
	if !ok {
		assessmentFail("empty reference output authorization requires root-module configuration")
	}
	moduleCalls, ok := assessmentObject(rootModule["module_calls"])
	if !ok {
		assessmentFailf("empty reference output authorization requires module.%s", resourceType)
	}
	moduleCall, ok := assessmentObject(moduleCalls[resourceType])
	if !ok {
		assessmentFailf("empty reference output authorization requires module.%s", resourceType)
	}
	module, ok := assessmentObject(moduleCall["module"])
	if !ok {
		assessmentFailf("empty reference output authorization requires module.%s resources", resourceType)
	}
	resources, ok := module["resources"].([]any)
	if !ok {
		assessmentFailf("empty reference output authorization requires module.%s resources", resourceType)
	}
	matches := 0
	for _, rawResource := range resources {
		resource, resourceOK := assessmentObject(rawResource)
		if resourceOK &&
			resource["address"] == resourceType+".this" &&
			resource["mode"] == "managed" &&
			resource["type"] == resourceType &&
			resource["name"] == "this" {
			matches++
		}
	}
	if matches != 1 {
		assessmentFailf("empty reference output authorization requires %s.this configuration", resourceType)
	}
}

func validateReferenceOutputChange(
	change map[string]any,
	plan map[string]any,
	resourceTypes []string,
) {
	expected := referenceOutputValue(plan, resourceTypes)
	actions, ok := change["actions"].([]any)
	if !ok || len(actions) != 1 {
		assessmentFail("engine reference output permits only create, update, or no-op actions")
	}
	action, ok := actions[0].(string)
	if !ok || action != "create" && action != "update" && action != "no-op" {
		assessmentFail("engine reference output permits only create, update, or no-op actions")
	}
	after, hasAfter := change["after"]
	if !hasAfter || !canonjson.TerraformJSONEqual(after, expected) {
		assessmentFail("engine reference output does not match provider-observed resource IDs")
	}
	before, hasBefore := change["before"]
	if action == "create" && (!hasBefore || before != nil) {
		assessmentFail("engine reference output create must start from null")
	}
	if action == "update" && !hasBefore {
		assessmentFail("engine reference output update must bind its prior value")
	}
	if action == "no-op" && (!hasBefore || !canonjson.TerraformJSONEqual(before, expected)) {
		assessmentFail("engine reference output no-op must bind the provider-observed IDs")
	}
	if afterUnknown, present := change["after_unknown"]; present {
		validateBooleanMask(afterUnknown, "output_changes after_unknown")
		if booleanMaskHasTrue(afterUnknown) {
			assessmentFail("engine reference output must be fully known")
		}
	}
	for _, field := range [...]string{"before_sensitive", "after_sensitive"} {
		if mask, present := change[field]; present {
			validateBooleanMask(mask, "output_changes "+field)
		}
	}
	if change["after_sensitive"] != true {
		assessmentFail("engine reference output must remain sensitive")
	}
	if (action == "update" || action == "no-op") && change["before_sensitive"] != true {
		assessmentFail("engine reference output existing value must preserve sensitivity")
	}
}

func validateOutputChanges(plan map[string]any, contract *AssessmentPlanContract) {
	var resourceTypes []string
	if contract != nil {
		resourceTypes = contract.ReferenceOutputTypes
	}
	if len(resourceTypes) > 0 {
		referenceOutputValue(plan, resourceTypes)
	}
	value, present := plan["output_changes"]
	if !present {
		if len(resourceTypes) > 0 {
			assessmentFail("reference output contract requires output_changes evidence")
		}
		return
	}
	changes, ok := assessmentObject(value)
	if !ok {
		assessmentFail("output_changes must be an object")
	}
	if _, hasReferenceOutput := changes[infrawrightReferenceOutput]; len(resourceTypes) > 0 && !hasReferenceOutput {
		assessmentFail("reference output contract requires the engine output change")
	}
	for _, name := range assessmentObjectKeys(changes) {
		change, changeOK := assessmentObject(changes[name])
		actions, actionsOK := change["actions"].([]any)
		if !changeOK || !actionsOK {
			assessmentFail("output_changes entries must contain actions")
		}
		if name == infrawrightReferenceOutput && len(resourceTypes) > 0 {
			validateReferenceOutputChange(change, plan, resourceTypes)
			continue
		}
		if len(actions) != 1 || actions[0] != "no-op" {
			assessmentFail("non-no-op output changes are not supported by saved-plan assessment")
		}
		before, hasBefore := change["before"]
		after, hasAfter := change["after"]
		if !hasBefore || !hasAfter || !canonjson.TerraformJSONEqual(before, after) {
			assessmentFail("output no-op values must be identical")
		}
		if afterUnknown, hasAfterUnknown := change["after_unknown"]; hasAfterUnknown {
			validateBooleanMask(afterUnknown, "output_changes after_unknown")
			if booleanMaskHasTrue(afterUnknown) {
				assessmentFail("output no-op must not contain unknown values")
			}
		}
		for _, field := range [...]string{"before_sensitive", "after_sensitive"} {
			if mask, hasMask := change[field]; hasMask {
				validateBooleanMask(mask, "output_changes "+field)
			}
		}
		beforeSensitive, beforePresent := change["before_sensitive"]
		if !beforePresent || beforeSensitive == nil {
			beforeSensitive = map[string]any{}
		}
		afterSensitive, afterPresent := change["after_sensitive"]
		if !afterPresent || afterSensitive == nil {
			afterSensitive = map[string]any{}
		}
		if !canonjson.TerraformJSONEqual(beforeSensitive, afterSensitive) {
			assessmentFail("output no-op sensitivity metadata must be identical")
		}
	}
}

func validateCheckStatus(value any, where string) {
	check, ok := assessmentObject(value)
	if !ok {
		assessmentFailf("%s must be an object", where)
	}
	status, ok := check["status"].(string)
	if !ok || status != "pass" && status != "unknown" && status != "fail" && status != "error" {
		assessmentFailf("%s.status is invalid", where)
	}
	if status == "fail" || status == "error" {
		assessmentFail("failed Terraform checks are not supported by saved-plan assessment")
	}
}

func validateChecks(value any, present bool) {
	if !present {
		return
	}
	checks, ok := value.([]any)
	if !ok {
		assessmentFail("checks must be an array")
	}
	for checkIndex, rawCheck := range checks {
		where := fmt.Sprintf("checks[%d]", checkIndex)
		validateCheckStatus(rawCheck, where)
		check, _ := assessmentObject(rawCheck)
		instancesValue, hasInstances := check["instances"]
		if !hasInstances {
			continue
		}
		instances, instancesOK := instancesValue.([]any)
		if !instancesOK {
			assessmentFailf("%s.instances must be an array", where)
		}
		for instanceIndex, instance := range instances {
			validateCheckStatus(
				instance,
				fmt.Sprintf("%s.instances[%d]", where, instanceIndex),
			)
		}
	}
}

// ValidateAssessmentPlan validates the narrow Terraform plan surface consumed
// by saved-plan assessment. Unknown object properties remain allowed for
// forward-compatible 1.x additions. A nil contract ports the source default
// AssessmentPlanContract value of {}.
func ValidateAssessmentPlan(planValue any, contract *AssessmentPlanContract) (err error) {
	defer recoverAssessmentPlanError(&err)

	plan, ok := assessmentObject(planValue)
	if !ok {
		assessmentFail("plan must be an object")
	}
	formatVersion, ok := plan["format_version"].(string)
	if !ok || !assessmentFormatVersion.MatchString(formatVersion) {
		assessmentFail("plan format_version must be a supported 1.x version")
	}
	if terraformVersion, present := plan["terraform_version"]; present && terraformVersion != nil {
		if _, stringOK := terraformVersion.(string); !stringOK {
			assessmentFail("plan terraform_version must be a string when present")
		}
	}
	if plan["complete"] != true {
		assessmentFail("plan must be complete before assessment")
	}
	if plan["errored"] != false {
		assessmentFail("errored plans cannot be assessed")
	}
	changes := assessmentRecords(plan, "resource_changes")
	drift := assessmentRecords(plan, "resource_drift")
	if len(changes)+len(drift) > MaxAssessmentChangeRecords {
		assessmentFailf("plan exceeds %d change records", MaxAssessmentChangeRecords)
	}
	for index, record := range changes {
		validateChangeRecord(record, fmt.Sprintf("resource_changes[%d]", index))
	}
	for index, record := range drift {
		validateChangeRecord(record, fmt.Sprintf("resource_drift[%d]", index))
	}
	validateOutputChanges(plan, contract)
	validateEmptyArray(plan, "action_invocations")
	validateEmptyArray(plan, "deferred_changes")
	validateEmptyArray(plan, "deferred_action_invocations")
	checks, hasChecks := plan["checks"]
	validateChecks(checks, hasChecks)
	return nil
}
