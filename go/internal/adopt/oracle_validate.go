package adopt

import (
	"fmt"
	"strings"

	"github.com/dvmrry/infrawright-dev/go/internal/canonjson"
)

func rawRecord(value any) (map[string]any, bool) {
	record, ok := value.(map[string]any)
	return record, ok
}

func optionalEmptyArray(value any, exists bool) bool {
	if !exists {
		return true
	}
	array, ok := value.([]any)
	return ok && len(array) == 0
}

func optionalEmptyRecord(value any, exists bool) bool {
	if !exists {
		return true
	}
	record, ok := rawRecord(value)
	return ok && len(record) == 0
}

func optionalEmptyCollection(value any, exists bool) bool {
	return optionalEmptyArray(value, exists) || optionalEmptyRecord(value, exists)
}

func expectedResourceContext(expected map[string]expectedOracleInstance) string {
	types := make(map[string]struct{})
	for _, instance := range expected {
		types[instance.ResourceType] = struct{}{}
	}
	if len(types) == 1 {
		return mapKeys(types)[0] + " "
	}
	return ""
}

func oraclePlanRefusal(message string) error {
	return oracleErrorf("%s; %s", message, oraclePlanDebugHint)
}

func assertImportOnlyBatchPlan(plan decodedPlan, expected map[string]expectedOracleInstance) error {
	context := expectedResourceContext(expected)
	raw := plan.Raw
	format, formatOK := raw["format_version"].(string)
	terraformVersion, terraformOK := raw["terraform_version"].(string)
	complete, completeRawOK := raw["complete"].(bool)
	errored, erroredOK := raw["errored"].(bool)
	applyable, applyableOK := raw["applyable"].(bool)
	_, errorsExists := raw["errors"]
	_, diagnosticsExists := raw["diagnostics"]
	_, checksExists := raw["checks"]
	_, deferredExists := raw["deferred_changes"]
	_, actionsExists := raw["action_invocations"]
	_, deferredActionsExists := raw["deferred_action_invocations"]
	_, driftExists := raw["resource_drift"]
	_, outputsExists := raw["output_changes"]
	if !formatOK || !formatVersionOne.MatchString(format) || !terraformOK || terraformVersion == "" ||
		plan.Typed == nil || plan.Typed.Complete == nil || !*plan.Typed.Complete || !completeRawOK || !complete ||
		!erroredOK || errored || !applyableOK || !applyable ||
		!optionalEmptyCollection(raw["errors"], errorsExists) ||
		!optionalEmptyCollection(raw["diagnostics"], diagnosticsExists) ||
		!optionalEmptyArray(raw["checks"], checksExists) ||
		!optionalEmptyArray(raw["deferred_changes"], deferredExists) ||
		!optionalEmptyArray(raw["action_invocations"], actionsExists) ||
		!optionalEmptyArray(raw["deferred_action_invocations"], deferredActionsExists) ||
		!optionalEmptyArray(raw["resource_drift"], driftExists) ||
		!optionalEmptyRecord(raw["output_changes"], outputsExists) {
		return oraclePlanRefusal(context + "oracle import plan was incomplete, errored, non-applyable, deferred, or contained non-import effects; refusing to apply the scratch plan")
	}
	changes, ok := raw["resource_changes"].([]any)
	if !ok || len(changes) != len(expected) {
		count := "malformed"
		if ok {
			count = fmt.Sprint(len(changes))
		}
		return oraclePlanRefusal(fmt.Sprintf("%soracle import plan reported %s resource change(s), expected %d import(s); refusing to apply the scratch plan", context, count, len(expected)))
	}
	seen := make(map[string]struct{}, len(changes))
	for _, item := range changes {
		change, ok := rawRecord(item)
		if !ok {
			return oracleErrorf("%soracle import plan contained a malformed change", context)
		}
		address, _ := change["address"].(string)
		instance, expectedAddress := expected[address]
		details, _ := rawRecord(change["change"])
		actions, _ := details["actions"].([]any)
		importing, importingOK := rawRecord(details["importing"])
		_, duplicate := seen[address]
		valid := expectedAddress && !duplicate && change["mode"] == "managed" &&
			change["type"] == instance.ResourceType && change["provider_name"] == instance.ProviderName &&
			len(actions) == 1 && actions[0] == "no-op" && importingOK && importing["id"] == instance.ImportID
		if !valid {
			safeAddress := address
			if !expectedAddress {
				safeAddress = "<unexpected address>"
			}
			return oraclePlanRefusal(fmt.Sprintf("%soracle import plan was not the exact requested import for %s; refusing to apply the scratch plan", context, safeAddress))
		}
		seen[address] = struct{}{}
	}
	missing := make([]string, 0)
	for address := range expected {
		if _, ok := seen[address]; !ok {
			missing = append(missing, address)
		}
	}
	if len(missing) > 0 {
		return oraclePlanRefusal(fmt.Sprintf("%soracle import plan addresses did not match expected scratch addresses (missing=%s unexpected=<none>); refusing to apply the scratch plan", context, strings.Join(canonjson.SortedStrings(missing), ", ")))
	}
	return nil
}

// AssertImportOnlyPlan ports assertImportOnlyPlan from
// node-src/domain/import-oracle.ts.
func AssertImportOnlyPlan(planBytes []byte, expectedImports map[string]string, provider, resourceType string) error {
	typed, raw, err := DecodeOraclePlan(planBytes)
	if err != nil {
		return fmt.Errorf("%w", err)
	}
	expected := make(map[string]expectedOracleInstance, len(expectedImports))
	for address, importID := range expectedImports {
		expected[address] = expectedOracleInstance{ImportID: importID, Key: address, ProviderName: provider, ResourceType: resourceType}
	}
	return assertImportOnlyBatchPlan(decodedPlan{Typed: typed, Raw: raw}, expected)
}

func exactBatchStateObjects(state map[string]any, expected map[string]expectedOracleInstance) (OracleBatchState, error) {
	context := expectedResourceContext(expected)
	format, formatOK := state["format_version"].(string)
	terraformVersion, terraformOK := state["terraform_version"].(string)
	values, valuesOK := rawRecord(state["values"])
	outputs, outputsExists := values["outputs"]
	checks, checksExists := state["checks"]
	root, rootOK := rawRecord(values["root_module"])
	if !formatOK || !formatVersionOne.MatchString(format) || !terraformOK || terraformVersion == "" || !valuesOK ||
		!optionalEmptyRecord(outputs, outputsExists) || !optionalEmptyArray(checks, checksExists) || !rootOK {
		return nil, oracleErrorf("%simport oracle returned malformed Terraform state", context)
	}
	resources, resourcesOK := root["resources"].([]any)
	children, childrenExists := root["child_modules"]
	if !resourcesOK || len(resources) != len(expected) || !optionalEmptyArray(children, childrenExists) {
		return nil, oracleErrorf("%simport oracle returned non-exact root state", context)
	}
	output := make(OracleBatchState)
	seen := make(map[string]struct{}, len(resources))
	for _, item := range resources {
		resource, ok := rawRecord(item)
		address, addressOK := resource["address"].(string)
		if !ok || !addressOK {
			return nil, oracleErrorf("%simport oracle returned a malformed root resource", context)
		}
		instance, expectedAddress := expected[address]
		_, duplicate := seen[address]
		_, deposed := resource["deposed_key"]
		valuesRecord, resourceValuesOK := rawRecord(resource["values"])
		sensitive, sensitiveExists := resource["sensitive_values"]
		_, sensitiveRecord := rawRecord(sensitive)
		sensitiveTrue, _ := sensitive.(bool)
		tainted, taintedExists := resource["tainted"]
		if !expectedAddress || duplicate || resource["mode"] != "managed" || resource["type"] != instance.ResourceType ||
			resource["provider_name"] != instance.ProviderName || deposed || (taintedExists && tainted != false) || !resourceValuesOK ||
			!sensitiveExists || (!sensitiveRecord && !sensitiveTrue) {
			return nil, oracleErrorf("%simport oracle returned non-exact managed state for %s", context, address)
		}
		seen[address] = struct{}{}
		if output[instance.ResourceType] == nil {
			output[instance.ResourceType] = make(map[string]OracleStateObject)
		}
		output[instance.ResourceType][instance.Key] = OracleStateObject{Address: address, SensitiveValues: sensitive, Values: valuesRecord}
	}
	if len(seen) != len(expected) {
		return nil, oracleErrorf("%simport oracle did not return exact expected-address coverage", context)
	}
	return output, nil
}

func assertNoUnknownValues(value any, resourceType string) error {
	if boolean, ok := value.(bool); ok {
		if boolean {
			return oracleErrorf("%s accepted import plan left provider-observed values unknown", resourceType)
		}
		return nil
	}
	switch typed := value.(type) {
	case []any:
		for _, child := range typed {
			if err := assertNoUnknownValues(child, resourceType); err != nil {
				return fmt.Errorf("%w", err)
			}
		}
		return nil
	case map[string]any:
		for _, key := range canonjson.SortedStrings(mapKeys(typed)) {
			if err := assertNoUnknownValues(typed[key], resourceType); err != nil {
				return fmt.Errorf("%w", err)
			}
		}
		return nil
	default:
		return oracleErrorf("%s accepted import plan returned a malformed unknown-value mask", resourceType)
	}
}

func extractAcceptedBatchPlanState(plan decodedPlan, expected map[string]expectedOracleInstance) (OracleBatchState, error) {
	if err := assertImportOnlyBatchPlan(plan, expected); err != nil {
		return nil, err
	}
	context := expectedResourceContext(expected)
	planned, plannedOK := rawRecord(plan.Raw["planned_values"])
	prior, priorOK := rawRecord(plan.Raw["prior_state"])
	if !plannedOK || !priorOK {
		return nil, oracleErrorf("%saccepted import plan did not contain complete planned and prior state", context)
	}
	stateHeader := map[string]any{"format_version": plan.Raw["format_version"], "terraform_version": plan.Raw["terraform_version"]}
	plannedState := cloneRecord(stateHeader)
	plannedState["values"] = planned
	plannedObjects, err := exactBatchStateObjects(plannedState, expected)
	if err != nil {
		return nil, err
	}
	priorObjects, err := exactBatchStateObjects(prior, expected)
	if err != nil {
		return nil, err
	}
	changes, _ := plan.Raw["resource_changes"].([]any)
	byAddress := make(map[string]map[string]any, len(changes))
	for _, item := range changes {
		change, ok := rawRecord(item)
		address, addressOK := change["address"].(string)
		if !ok || !addressOK || byAddress[address] != nil {
			return nil, oracleErrorf("%saccepted import plan contained duplicate or malformed change addresses", context)
		}
		byAddress[address] = change
	}
	for address, instance := range expected {
		plannedObject, plannedOK := plannedObjects[instance.ResourceType][instance.Key]
		priorObject, priorOK := priorObjects[instance.ResourceType][instance.Key]
		rawChange := byAddress[address]
		change, changeOK := rawRecord(rawChange["change"])
		_, deposed := rawChange["deposed"]
		before, beforeOK := rawRecord(change["before"])
		after, afterOK := rawRecord(change["after"])
		unknown, unknownExists := change["after_unknown"]
		beforeSensitive, beforeSensitiveExists := change["before_sensitive"]
		afterSensitive, afterSensitiveExists := change["after_sensitive"]
		if !plannedOK || !priorOK || rawChange == nil || !changeOK || deposed || !beforeOK || !afterOK || !unknownExists ||
			!beforeSensitiveExists || !afterSensitiveExists || !sensitivityMask(beforeSensitive) || !sensitivityMask(afterSensitive) {
			return nil, oracleErrorf("%s accepted import plan did not contain exact provider-observed evidence", instance.ResourceType)
		}
		if err := assertNoUnknownValues(unknown, instance.ResourceType); err != nil {
			return nil, err
		}
		if !canonjson.TerraformJSONExactlyEqual(before, after) ||
			!canonjson.TerraformJSONExactlyEqual(after, plannedObject.Values) ||
			!canonjson.TerraformJSONExactlyEqual(plannedObject.Values, priorObject.Values) ||
			!canonjson.TerraformJSONExactlyEqual(beforeSensitive, afterSensitive) ||
			!canonjson.TerraformJSONExactlyEqual(afterSensitive, plannedObject.SensitiveValues) ||
			!canonjson.TerraformJSONExactlyEqual(plannedObject.SensitiveValues, priorObject.SensitiveValues) {
			return nil, oracleErrorf("%s accepted import plan provider observations were inconsistent", instance.ResourceType)
		}
	}
	return plannedObjects, nil
}

// ExtractAcceptedPlanState ports extractAcceptedPlanState from
// node-src/domain/import-oracle.ts.
func ExtractAcceptedPlanState(planBytes []byte, addressToKey, expectedImports map[string]string, provider, resourceType string) (map[string]OracleStateObject, error) {
	missing := make([]string, 0)
	unexpected := make([]string, 0)
	for address := range addressToKey {
		if _, ok := expectedImports[address]; !ok {
			missing = append(missing, address)
		}
	}
	for address := range expectedImports {
		if _, ok := addressToKey[address]; !ok {
			unexpected = append(unexpected, address)
		}
	}
	if len(missing) > 0 || len(unexpected) > 0 {
		missingText, unexpectedText := "<none>", "<none>"
		if len(missing) > 0 {
			missingText = strings.Join(canonjson.SortedStrings(missing), ", ")
		}
		if len(unexpected) > 0 {
			unexpectedText = strings.Join(canonjson.SortedStrings(unexpected), ", ")
		}
		return nil, oracleErrorf("%s accepted import plan address maps did not match (missing=%s unexpected=%s)", resourceType, missingText, unexpectedText)
	}
	expected := make(map[string]expectedOracleInstance, len(addressToKey))
	for address, key := range addressToKey {
		importID, ok := expectedImports[address]
		if !ok {
			return nil, oracleErrorf("%s accepted import plan missing expected import %s", resourceType, address)
		}
		expected[address] = expectedOracleInstance{ImportID: importID, Key: key, ProviderName: provider, ResourceType: resourceType}
	}
	typed, raw, err := DecodeOraclePlan(planBytes)
	if err != nil {
		return nil, fmt.Errorf("%w", err)
	}
	output, err := extractAcceptedBatchPlanState(decodedPlan{Typed: typed, Raw: raw}, expected)
	if err != nil {
		return nil, fmt.Errorf("%w", err)
	}
	if output[resourceType] == nil {
		return map[string]OracleStateObject{}, nil
	}
	return output[resourceType], nil
}

func sensitivityMask(value any) bool {
	if boolean, ok := value.(bool); ok {
		return boolean
	}
	_, ok := rawRecord(value)
	return ok
}

func cloneRecord(input map[string]any) map[string]any {
	output := make(map[string]any, len(input))
	for key, value := range input {
		output[key] = value
	}
	return output
}
