package assessment

import (
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"regexp"
	"sort"
	"strconv"

	"github.com/dvmrry/infrawright-dev/go/internal/procerr"
)

// AssessmentSemanticsKeyword is the required custom vocabulary assertion in
// saved-plan-assessment.schema.json.
const AssessmentSemanticsKeyword = "x-infrawright-report-semantics"

var (
	tenantPattern       = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)
	rootLabelPattern    = regexp.MustCompile(`^[a-z0-9_]+$`)
	resourceTypePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	sha256Pattern       = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

type assessmentValidation struct {
	details []procerr.ErrorDetail
}

func (v *assessmentValidation) add(path, code, message string) {
	if path == "" {
		path = "/"
	}
	v.details = append(v.details, procerr.ErrorDetail{
		Path:    path,
		Code:    code,
		Message: message,
	})
}

// ValidateSavedPlanAssessment hand-ports the structural schema and its
// required cross-field semantic vocabulary. Details use the same first-64
// truncation contract as schemaErrorDetails in validators.ts.
func ValidateSavedPlanAssessment(value any) (bool, []procerr.ErrorDetail) {
	validation := &assessmentValidation{}
	report, ok := value.(map[string]any)
	if !ok {
		validation.add("/", "type", "must be object")
		return false, validation.details
	}
	validateAssessmentAllOf(report, validation)
	validateAssessmentStructure(report, validation)
	validation.details = append(validation.details, ValidateAssessmentSemantics(report, "")...)
	if len(validation.details) <= 64 {
		return len(validation.details) == 0, append([]procerr.ErrorDetail(nil), validation.details...)
	}
	details := append([]procerr.ErrorDetail(nil), validation.details[:64]...)
	details = append(details, procerr.ErrorDetail{
		Path:    "/",
		Code:    "schema_errors_truncated",
		Message: "additional schema errors were omitted",
	})
	return false, details
}

func validateAssessmentAllOf(report map[string]any, validation *assessmentValidation) {
	isError := nestedPropertyConstCondition(report, "summary", "status", "error")
	branch := &assessmentValidation{}
	if isError {
		if _, present := report["error"]; !present {
			branch.add("/", "required", "must have required property 'error'")
		} else {
			validateReportError(report["error"], "/error", branch)
		}
	} else {
		if _, present := report["error"]; present {
			branch.add("/error", "false schema", "boolean schema is false")
		}
		if roots, ok := report["roots"].([]any); ok && len(roots) < 1 {
			branch.add("/roots", "minItems", "must NOT have fewer than 1 items")
		}
		if summary, ok := report["summary"].(map[string]any); ok {
			if checked, present := summary["checked"]; present {
				if _, integer := jsonIntegerValue(checked); !integer {
					branch.add("/summary/checked", "type", "must be integer")
				}
				if numeric, number := jsonNumericValue(checked); number && numeric < 1 {
					branch.add("/summary/checked", "minimum", "must be >= 1")
				}
			}
		} else if _, present := report["summary"]; present {
			branch.add("/summary", "type", "must be object")
		}
	}
	if len(branch.details) > 0 {
		validation.details = append(validation.details, branch.details...)
		keyword := "then"
		if !isError {
			keyword = "else"
		}
		validation.add("/", "if", `must match "`+keyword+`" schema`)
	}

	validateSummaryStatusBranch(report, "clean", func(branch *assessmentValidation) {
		validateSummaryMinimum(report, "clean", 1, branch)
		validateSummaryConstant(report, "tolerated", 0, branch)
		validateSummaryConstant(report, "blocked", 0, branch)
		validateEveryRootStatus(report, "clean", branch)
	}, validation)
	validateSummaryStatusBranch(report, "clean_with_tolerated_drift", func(branch *assessmentValidation) {
		validateSummaryMinimum(report, "tolerated", 1, branch)
		validateSummaryConstant(report, "blocked", 0, branch)
		validateRootExcludesStatus(report, "blocked", branch)
		validateRootContainsStatus(report, "clean_with_tolerated_drift", branch)
	}, validation)
	validateSummaryStatusBranch(report, "blocked", func(branch *assessmentValidation) {
		validateSummaryMinimum(report, "blocked", 1, branch)
		validateRootContainsStatus(report, "blocked", branch)
	}, validation)

	if propertyConstCondition(report, "mode", "assert-clean") {
		branch = &assessmentValidation{}
		if request, ok := report["request"].(map[string]any); ok {
			if value, present := request["policy"]; present && value != nil {
				branch.add("/request/policy", "type", "must be null")
			}
			if value, present := request["policy_sha256"]; present && value != nil {
				branch.add("/request/policy_sha256", "type", "must be null")
			}
		}
		if len(branch.details) > 0 {
			validation.details = append(validation.details, branch.details...)
			validation.add("/", "if", `must match "then" schema`)
		}
	}
}

func validateSummaryStatusBranch(
	report map[string]any,
	status string,
	validate func(*assessmentValidation),
	validation *assessmentValidation,
) {
	if !nestedPropertyConstCondition(report, "summary", "status", status) {
		return
	}
	branch := &assessmentValidation{}
	validate(branch)
	if len(branch.details) == 0 {
		return
	}
	validation.details = append(validation.details, branch.details...)
	validation.add("/", "if", `must match "then" schema`)
}

func validateSummaryMinimum(
	report map[string]any,
	field string,
	minimum float64,
	validation *assessmentValidation,
) {
	summary, ok := report["summary"].(map[string]any)
	if !ok {
		return
	}
	value, present := summary[field]
	if !present {
		return
	}
	if _, integer := jsonIntegerValue(value); !integer {
		validation.add("/summary/"+field, "type", "must be integer")
	}
	if numeric, number := jsonNumericValue(value); number && numeric < minimum {
		validation.add(
			"/summary/"+field,
			"minimum",
			"must be >= "+strconv.FormatFloat(minimum, 'f', -1, 64),
		)
	}
}

func validateSummaryConstant(
	report map[string]any,
	field string,
	want float64,
	validation *assessmentValidation,
) {
	summary, ok := report["summary"].(map[string]any)
	if !ok {
		return
	}
	value, present := summary[field]
	if !present {
		return
	}
	if _, integer := jsonIntegerValue(value); !integer {
		validation.add("/summary/"+field, "type", "must be integer")
	}
	if !jsonSchemaEqual(value, want) {
		validation.add("/summary/"+field, "const", "must be equal to constant")
	}
}

func validateEveryRootStatus(
	report map[string]any,
	want string,
	validation *assessmentValidation,
) {
	roots, ok := report["roots"].([]any)
	if !ok {
		return
	}
	for index, candidate := range roots {
		root, record := candidate.(map[string]any)
		if !record {
			validation.add(fmt.Sprintf("/roots/%d", index), "type", "must be object")
			continue
		}
		status, present := root["status"]
		if present && !jsonSchemaEqual(status, want) {
			validation.add(
				fmt.Sprintf("/roots/%d/status", index),
				"const",
				"must be equal to constant",
			)
		}
	}
}

func validateRootContainsStatus(
	report map[string]any,
	want string,
	validation *assessmentValidation,
) {
	roots, ok := report["roots"].([]any)
	if !ok {
		return
	}
	items := &assessmentValidation{}
	for index, candidate := range roots {
		root, record := candidate.(map[string]any)
		if !record {
			items.add(fmt.Sprintf("/roots/%d", index), "type", "must be object")
			continue
		}
		status, present := root["status"]
		if !present || jsonSchemaEqual(status, want) {
			return
		}
		items.add(fmt.Sprintf("/roots/%d/status", index), "const", "must be equal to constant")
	}
	validation.details = append(validation.details, items.details...)
	validation.add("/roots", "contains", "must contain at least 1 valid item(s)")
}

func validateRootExcludesStatus(
	report map[string]any,
	forbidden string,
	validation *assessmentValidation,
) {
	roots, ok := report["roots"].([]any)
	if !ok {
		return
	}
	for _, candidate := range roots {
		root, record := candidate.(map[string]any)
		if !record {
			continue
		}
		status, present := root["status"]
		if !present || jsonSchemaEqual(status, forbidden) {
			validation.add("/roots", "not", "must NOT be valid")
			return
		}
	}
}

func validateAssessmentStructure(report map[string]any, validation *assessmentValidation) {
	requiredProperties(report, "/", []string{
		"kind", "schema_version", "mode", "request", "summary", "roots", "stale_policy",
	}, validation)
	additionalProperties(report, "/", stringSet(
		"kind", "schema_version", "mode", "request", "summary", "roots", "stale_policy", "error",
	), validation)
	if value, present := report["kind"]; present && value != "infrawright.saved_plan_assessment" {
		validation.add("/kind", "const", "must be equal to constant")
	}
	if value, present := report["schema_version"]; present {
		if integer, ok := jsonIntegerValue(value); !ok || integer != 1 {
			validation.add("/schema_version", "const", "must be equal to constant")
		}
	}
	if value, present := report["mode"]; present && value != "assert-clean" && value != "assert-adoptable" {
		validation.add("/mode", "enum", "must be equal to one of the allowed values")
	}
	if value, present := report["request"]; present {
		validateReportRequest(value, "/request", validation)
	}
	if value, present := report["summary"]; present {
		validateReportSummary(value, "/summary", validation)
	}
	if value, present := report["roots"]; present {
		roots, ok := value.([]any)
		if !ok {
			validation.add("/roots", "type", "must be array")
		} else {
			for index, root := range roots {
				validateReportRoot(root, fmt.Sprintf("/roots/%d", index), validation)
			}
		}
	}
	if value, present := report["stale_policy"]; present {
		entries, ok := value.([]any)
		if !ok {
			validation.add("/stale_policy", "type", "must be array")
		} else {
			for index, entry := range entries {
				validateStalePolicy(entry, fmt.Sprintf("/stale_policy/%d", index), validation)
			}
		}
	}
	if value, present := report["error"]; present {
		validateReportError(value, "/error", validation)
	}
}

func validateReportRequest(value any, path string, validation *assessmentValidation) {
	request, ok := value.(map[string]any)
	if !ok {
		validation.add(path, "type", "must be object")
		return
	}
	requiredProperties(request, path, []string{"tenant", "selectors", "policy", "policy_sha256"}, validation)
	additionalProperties(request, path, stringSet("tenant", "selectors", "policy", "policy_sha256"), validation)
	if tenant, present := request["tenant"]; present && tenant != nil {
		text, ok := tenant.(string)
		if !ok || !validTenant(text) {
			validation.add(path+"/tenant", "type", "must be null")
			if !ok {
				validation.add(path+"/tenant", "type", "must be string")
			} else {
				validation.add(
					path+"/tenant",
					"pattern",
					`must match pattern "^(?!\.{1,2}$)[A-Za-z0-9_.-]+$"`,
				)
			}
			validation.add(
				path+"/tenant",
				"oneOf",
				"must match exactly one schema in oneOf",
			)
		}
	}
	if selectors, present := request["selectors"]; present {
		validateStringArray(selectors, path+"/selectors", false, false, "", validation)
	}
	if policy, present := request["policy"]; present && policy != nil {
		if _, ok := policy.(string); !ok {
			validation.add(path+"/policy", "type", "must be string,null")
		}
	}
	if digest, present := request["policy_sha256"]; present && digest != nil {
		text, ok := digest.(string)
		if !ok {
			validation.add(path+"/policy_sha256", "type", "must be string,null")
		} else if !sha256Pattern.MatchString(text) {
			validation.add(path+"/policy_sha256", "pattern", `must match pattern "^[0-9a-f]{64}$"`)
		}
	}
}

func validateReportSummary(value any, path string, validation *assessmentValidation) {
	summary, ok := value.(map[string]any)
	if !ok {
		validation.add(path, "type", "must be object")
		return
	}
	requiredProperties(summary, path, []string{"status", "checked", "clean", "tolerated", "blocked"}, validation)
	additionalProperties(summary, path, stringSet("status", "checked", "clean", "tolerated", "blocked"), validation)
	if status, present := summary["status"]; present && status != "clean" &&
		status != "clean_with_tolerated_drift" && status != "blocked" && status != "error" {
		validation.add(path+"/status", "enum", "must be equal to one of the allowed values")
	}
	for _, field := range []string{"checked", "clean", "tolerated", "blocked"} {
		if number, present := summary[field]; present {
			if _, integer := jsonIntegerValue(number); !integer {
				validation.add(path+"/"+field, "type", "must be integer")
			}
			if numeric, isNumber := jsonNumericValue(number); isNumber && numeric < 0 {
				validation.add(path+"/"+field, "minimum", "must be >= 0")
			}
		}
	}
}

func validateReportRoot(value any, path string, validation *assessmentValidation) {
	root, ok := value.(map[string]any)
	if !ok {
		validation.add(path, "type", "must be object")
		return
	}
	requiredProperties(root, path, []string{
		"tenant", "label", "members", "status", "plan", "plan_fingerprint", "findings", "guidance",
	}, validation)
	additionalProperties(root, path, stringSet(
		"tenant", "label", "members", "status", "plan", "plan_fingerprint", "findings", "guidance",
	), validation)
	if tenant, present := root["tenant"]; present {
		text, ok := tenant.(string)
		if !ok {
			validation.add(path+"/tenant", "type", "must be string")
		} else if !validTenant(text) {
			validation.add(path+"/tenant", "pattern", `must match pattern "^(?!\.{1,2}$)[A-Za-z0-9_.-]+$"`)
		}
	}
	if label, present := root["label"]; present {
		text, ok := label.(string)
		if !ok {
			validation.add(path+"/label", "type", "must be string")
		} else if !rootLabelPattern.MatchString(text) {
			validation.add(path+"/label", "pattern", `must match pattern "^[a-z0-9_]+$"`)
		}
	}
	if members, present := root["members"]; present {
		validateStringArray(members, path+"/members", true, true, `^[A-Za-z_][A-Za-z0-9_]*$`, validation)
	}
	if status, present := root["status"]; present && status != "clean" &&
		status != "clean_with_tolerated_drift" && status != "blocked" {
		validation.add(path+"/status", "enum", "must be equal to one of the allowed values")
	}
	if planValue, present := root["plan"]; present {
		validatePlanEvidence(planValue, path+"/plan", validation)
	}
	if fingerprint, present := root["plan_fingerprint"]; present {
		validatePlanFingerprint(fingerprint, path+"/plan_fingerprint", validation)
	}
	if findings, present := root["findings"]; present {
		items, ok := findings.([]any)
		if !ok {
			validation.add(path+"/findings", "type", "must be array")
		} else {
			for index, finding := range items {
				validateReportFinding(finding, fmt.Sprintf("%s/findings/%d", path, index), validation)
			}
		}
	}
	if guidance, present := root["guidance"]; present {
		items, ok := guidance.([]any)
		if !ok {
			validation.add(path+"/guidance", "type", "must be array")
		} else {
			for index, entry := range items {
				validateReportGuidance(entry, fmt.Sprintf("%s/guidance/%d", path, index), validation)
			}
		}
	}
}

func validatePlanEvidence(value any, path string, validation *assessmentValidation) {
	planValue, ok := value.(map[string]any)
	if !ok {
		validation.add(path, "type", "must be object")
		return
	}
	requiredProperties(planValue, path, []string{"sha256", "format_version", "terraform_version"}, validation)
	additionalProperties(planValue, path, stringSet("sha256", "format_version", "terraform_version"), validation)
	if digest, present := planValue["sha256"]; present {
		validateSHA256(digest, path+"/sha256", validation)
	}
	for _, field := range []string{"format_version", "terraform_version"} {
		if value, present := planValue[field]; present && value != nil {
			if _, ok := value.(string); !ok {
				validation.add(path+"/"+field, "type", "must be string,null")
			}
		}
	}
}

func validatePlanFingerprint(value any, path string, validation *assessmentValidation) {
	fingerprint, ok := value.(map[string]any)
	if !ok {
		validation.add(path, "type", "must be object")
		return
	}
	requiredProperties(fingerprint, path, []string{"version", "sha256"}, validation)
	additionalProperties(fingerprint, path, stringSet("version", "sha256"), validation)
	if version, present := fingerprint["version"]; present {
		if !jsonSchemaEqual(version, float64(2)) {
			validation.add(path+"/version", "const", "must be equal to constant")
		}
	}
	if digest, present := fingerprint["sha256"]; present {
		validateSHA256(digest, path+"/sha256", validation)
	}
}

func validateReportFinding(value any, path string, validation *assessmentValidation) {
	finding, ok := value.(map[string]any)
	if !ok {
		validation.add(path, "type", "must be object")
		return
	}
	requiredProperties(finding, path, []string{
		"status", "source", "address", "resource_type", "actions", "paths",
	}, validation)
	additionalProperties(finding, path, stringSet(
		"status", "source", "address", "resource_type", "actions", "paths",
	), validation)
	if status, present := finding["status"]; present && status != "clean" &&
		status != "clean_with_tolerated_drift" && status != "blocked" {
		validation.add(path+"/status", "enum", "must be equal to one of the allowed values")
	}
	if source, present := finding["source"]; present && source != "resource_changes" && source != "resource_drift" {
		validation.add(path+"/source", "enum", "must be equal to one of the allowed values")
	}
	for _, field := range []string{"address", "resource_type"} {
		if value, present := finding[field]; present && value != nil {
			if _, ok := value.(string); !ok {
				validation.add(path+"/"+field, "type", "must be string,null")
			}
		}
	}
	if actions, present := finding["actions"]; present {
		validateStringArray(actions, path+"/actions", false, false, "", validation)
	}
	if paths, present := finding["paths"]; present {
		validateStringArray(paths, path+"/paths", false, false, "", validation)
	}
}

func validateReportGuidance(value any, path string, validation *assessmentValidation) {
	guidance, ok := value.(map[string]any)
	if !ok {
		validation.add(path, "type", "must be object")
		return
	}
	requiredProperties(guidance, path, []string{
		"lane", "source", "address", "finding_path", "matched_plan_path", "status_effect",
	}, validation)
	if lane, present := guidance["lane"]; present && lane != "provider_config" &&
		lane != "absent_default" && lane != "dynamic_schema" {
		validation.add(path+"/lane", "enum", "must be equal to one of the allowed values")
	}
	for _, field := range []string{"source", "address", "finding_path", "matched_plan_path", "status_effect"} {
		if value, present := guidance[field]; present {
			if _, ok := value.(string); !ok {
				validation.add(path+"/"+field, "type", "must be string")
			}
		}
	}
}

func validateStalePolicy(value any, path string, validation *assessmentValidation) {
	entry, ok := value.(map[string]any)
	if !ok {
		validation.add(path, "type", "must be object")
		return
	}
	requiredProperties(entry, path, []string{"resource_type", "mode", "path"}, validation)
	additionalProperties(entry, path, stringSet("resource_type", "mode", "path"), validation)
	if resourceType, present := entry["resource_type"]; present {
		text, ok := resourceType.(string)
		if !ok {
			validation.add(path+"/resource_type", "type", "must be string")
		} else if !resourceTypePattern.MatchString(text) {
			validation.add(path+"/resource_type", "pattern", `must match pattern "^[A-Za-z_][A-Za-z0-9_]*$"`)
		}
	}
	if mode, present := entry["mode"]; present && mode != "plan_tolerate" {
		validation.add(path+"/mode", "const", "must be equal to constant")
	}
	if value, present := entry["path"]; present {
		text, ok := value.(string)
		if !ok {
			validation.add(path+"/path", "type", "must be string")
		} else if len([]rune(text)) < 1 {
			validation.add(path+"/path", "minLength", "must NOT have fewer than 1 characters")
		}
	}
}

func validateReportError(value any, path string, validation *assessmentValidation) {
	reportError, ok := value.(map[string]any)
	if !ok {
		validation.add(path, "type", "must be object")
		return
	}
	requiredProperties(reportError, path, []string{"kind", "message"}, validation)
	additionalProperties(reportError, path, stringSet("kind", "message"), validation)
	if kind, present := reportError["kind"]; present && kind != "assessment_error" &&
		kind != "no_saved_plans" && kind != "policy_error" {
		validation.add(path+"/kind", "enum", "must be equal to one of the allowed values")
	}
	if message, present := reportError["message"]; present {
		if _, ok := message.(string); !ok {
			validation.add(path+"/message", "type", "must be string")
		}
	}
}

func requiredProperties(
	record map[string]any,
	path string,
	required []string,
	validation *assessmentValidation,
) {
	for _, property := range required {
		if _, present := record[property]; !present {
			validation.add(path, "required", "must have required property '"+property+"'")
		}
	}
}

func additionalProperties(
	record map[string]any,
	path string,
	allowed map[string]struct{},
	validation *assessmentValidation,
) {
	extras := make([]string, 0)
	for property := range record {
		if _, ok := allowed[property]; !ok {
			extras = append(extras, property)
		}
	}
	sort.Strings(extras)
	for range extras {
		validation.add(path, "additionalProperties", "additional property is not allowed")
	}
}

func validateStringArray(
	value any,
	path string,
	minimumOne bool,
	unique bool,
	pattern string,
	validation *assessmentValidation,
) {
	items, ok := value.([]any)
	if !ok {
		validation.add(path, "type", "must be array")
		return
	}
	if minimumOne && len(items) == 0 {
		validation.add(path, "minItems", "must NOT have fewer than 1 items")
	}
	for index, item := range items {
		text, ok := item.(string)
		if !ok {
			validation.add(fmt.Sprintf("%s/%d", path, index), "type", "must be string")
			continue
		}
		if pattern != "" && !resourceTypePattern.MatchString(text) {
			validation.add(
				fmt.Sprintf("%s/%d", path, index),
				"pattern",
				`must match pattern "`+pattern+`"`,
			)
		}
	}
	if unique {
		for duplicate := len(items) - 1; duplicate > 0; duplicate-- {
			duplicateText, duplicateIsString := items[duplicate].(string)
			if !duplicateIsString {
				continue
			}
			for prior := duplicate - 1; prior >= 0; prior-- {
				priorText, priorIsString := items[prior].(string)
				if priorIsString && duplicateText == priorText {
					validation.add(
						path,
						"uniqueItems",
						fmt.Sprintf(
							"must NOT have duplicate items (items ## %d and %d are identical)",
							duplicate,
							prior,
						),
					)
					return
				}
			}
		}
	}
}

func validateSHA256(value any, path string, validation *assessmentValidation) {
	text, ok := value.(string)
	if !ok {
		validation.add(path, "type", "must be string")
		return
	}
	if !sha256Pattern.MatchString(text) {
		validation.add(path, "pattern", `must match pattern "^[0-9a-f]{64}$"`)
	}
}

func validTenant(value string) bool {
	return value != "." && value != ".." && tenantPattern.MatchString(value)
}

func stringSet(values ...string) map[string]struct{} {
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		result[value] = struct{}{}
	}
	return result
}

func propertyConstCondition(record map[string]any, property string, want any) bool {
	value, present := record[property]
	return !present || jsonSchemaEqual(value, want)
}

func nestedPropertyConstCondition(
	record map[string]any,
	parent string,
	child string,
	want any,
) bool {
	value, present := record[parent]
	if !present {
		return true
	}
	nested, ok := value.(map[string]any)
	if !ok {
		return false
	}
	return propertyConstCondition(nested, child, want)
}

func jsonSchemaEqual(value, want any) bool {
	left, leftNumber := jsonNumericValue(value)
	right, rightNumber := jsonNumericValue(want)
	if leftNumber || rightNumber {
		return leftNumber && rightNumber && left == right
	}
	return reflect.DeepEqual(value, want)
}

func jsonIntegerValue(value any) (float64, bool) {
	numeric, number := jsonNumericValue(value)
	return numeric, number && math.Trunc(numeric) == numeric
}

func jsonNumericValue(value any) (float64, bool) {
	switch typed := value.(type) {
	case json.Number:
		parsed, err := strconv.ParseFloat(string(typed), 64)
		if err != nil && !math.IsInf(parsed, 0) {
			return 0, false
		}
		if math.IsNaN(parsed) || math.IsInf(parsed, 0) {
			// schemaValidationValue maps non-finite LosslessNumber values to 0.
			return 0, true
		}
		return parsed, true
	case float64:
		return typed, !math.IsNaN(typed) && !math.IsInf(typed, 0)
	case float32:
		converted := float64(typed)
		return converted, !math.IsNaN(converted) && !math.IsInf(converted, 0)
	case int:
		return float64(typed), true
	case int8:
		return float64(typed), true
	case int16:
		return float64(typed), true
	case int32:
		return float64(typed), true
	case int64:
		return float64(typed), true
	case uint:
		return float64(typed), true
	case uint8:
		return float64(typed), true
	case uint16:
		return float64(typed), true
	case uint32:
		return float64(typed), true
	case uint64:
		return float64(typed), true
	default:
		return 0, false
	}
}

// ValidateAssessmentSemantics enforces the cross-field invariants that the
// structural schema cannot express. Error order and text follow
// saved-plan-assessment-semantics.ts.
func ValidateAssessmentSemantics(value any, instancePath string) []procerr.ErrorDetail {
	report, reportOK := value.(map[string]any)
	if !reportOK {
		return nil
	}
	summary, summaryOK := report["summary"].(map[string]any)
	request, requestOK := report["request"].(map[string]any)
	roots, rootsOK := report["roots"].([]any)
	if !summaryOK || !requestOK || !rootsOK {
		return nil
	}
	validation := &assessmentValidation{}
	add := func(path, rule, message string) {
		validation.add(instancePath+path, AssessmentSemanticsKeyword, message)
	}

	rootStatuses := make([]string, 0, len(roots))
	rootKeys := make(map[string]struct{})
	tenantMemberKeys := make(map[string]struct{})
	checkedTypes := make(map[string]struct{})
	for index, candidate := range roots {
		root, rootOK := candidate.(map[string]any)
		if rootOK {
			tenant, tenantOK := root["tenant"].(string)
			if tenantOK {
				if requestedTenant, ok := request["tenant"].(string); ok && tenant != requestedTenant {
					add(
						fmt.Sprintf("/roots/%d/tenant", index),
						"request_tenant",
						"root tenant must match the requested tenant",
					)
				}
				if label, ok := root["label"].(string); ok {
					key := rootIdentity(tenant, label)
					if _, duplicate := rootKeys[key]; duplicate {
						add(
							fmt.Sprintf("/roots/%d", index),
							"root_identity",
							"tenant and root label must be unique within an assessment",
						)
					}
					rootKeys[key] = struct{}{}
				}
				if members, ok := root["members"].([]any); ok {
					for _, memberCandidate := range members {
						member, ok := memberCandidate.(string)
						if !ok {
							continue
						}
						checkedTypes[member] = struct{}{}
						key := rootIdentity(tenant, member)
						if _, duplicate := tenantMemberKeys[key]; duplicate {
							add(
								fmt.Sprintf("/roots/%d/members", index),
								"root_membership",
								"a resource type can belong to only one selected root per tenant",
							)
						}
						tenantMemberKeys[key] = struct{}{}
					}
				}
			}
		}
		if !rootOK {
			continue
		}
		status, statusOK := root["status"].(string)
		if !statusOK || !isAssessmentStatus(status) {
			continue
		}
		findings, findingsOK := root["findings"].([]any)
		if !findingsOK {
			continue
		}
		findingRecords := make([]map[string]any, len(findings))
		validFindings := true
		for findingIndex, finding := range findings {
			record, ok := finding.(map[string]any)
			if !ok {
				validFindings = false
				break
			}
			findingRecords[findingIndex] = record
		}
		if !validFindings {
			continue
		}
		findingStatuses := make([]string, 0, len(findingRecords))
		for _, finding := range findingRecords {
			findingStatus, ok := finding["status"].(string)
			if !ok || !isAssessmentStatus(findingStatus) {
				validFindings = false
				break
			}
			findingStatuses = append(findingStatuses, findingStatus)
		}
		if !validFindings {
			continue
		}

		blockedFindings := make(map[string]int)
		for _, finding := range findingRecords {
			if finding["status"] != "blocked" {
				continue
			}
			source, sourceOK := finding["source"].(string)
			address, addressOK := finding["address"].(string)
			paths, pathsOK := finding["paths"].([]any)
			if !sourceOK || !addressOK || !pathsOK {
				continue
			}
			for _, pathCandidate := range paths {
				path, ok := pathCandidate.(string)
				if !ok {
					continue
				}
				key := blockedFindingIdentity(source, address, path)
				blockedFindings[key]++
			}
		}
		if guidance, ok := root["guidance"].([]any); ok {
			for guidanceIndex, candidateGuidance := range guidance {
				entry, ok := candidateGuidance.(map[string]any)
				if !ok {
					continue
				}
				if _, leaked := entry["sort_key"]; leaked {
					add(
						fmt.Sprintf("/roots/%d/guidance/%d/sort_key", index, guidanceIndex),
						"guidance_join",
						"internal guidance sort keys cannot be emitted",
					)
				}
				source, sourceOK := entry["source"].(string)
				address, addressOK := entry["address"].(string)
				findingPath, findingPathOK := entry["finding_path"].(string)
				if !sourceOK || !addressOK || !findingPathOK {
					continue
				}
				if blockedFindings[blockedFindingIdentity(source, address, findingPath)] != 1 {
					add(
						fmt.Sprintf("/roots/%d/guidance/%d", index, guidanceIndex),
						"guidance_join",
						"guidance must join exactly one blocked finding path",
					)
				}
			}
		}
		rootStatuses = append(rootStatuses, status)
		if containsStatus(findingStatuses, "clean") {
			add(
				fmt.Sprintf("/roots/%d/findings", index),
				"finding_status",
				"classified findings must not use the aggregate clean status",
			)
		}
		if status != derivedAssessmentStatus(findingStatuses) {
			add(
				fmt.Sprintf("/roots/%d/status", index),
				"root_status",
				"root status must be derived from its findings",
			)
		}
	}

	if len(rootStatuses) == len(roots) {
		expected := map[string]float64{
			"checked":   float64(len(roots)),
			"clean":     float64(countStatus(rootStatuses, "clean")),
			"tolerated": float64(countStatus(rootStatuses, "clean_with_tolerated_drift")),
			"blocked":   float64(countStatus(rootStatuses, "blocked")),
		}
		for _, field := range []string{"checked", "clean", "tolerated", "blocked"} {
			actual, ok := jsonIntegerValue(summary[field])
			if !ok || actual != expected[field] {
				add(
					"/summary/"+field,
					"summary_count",
					field+" must equal the count derived from roots",
				)
			}
		}
		if summary["status"] != "error" && summary["status"] != derivedAssessmentStatus(rootStatuses) {
			add(
				"/summary/status",
				"summary_status",
				"summary status must be derived from root statuses",
			)
		}
	}

	policy, policyPresent := request["policy"]
	policySHA256, policySHA256Present := request["policy_sha256"]
	policyIsNull := policyPresent && policy == nil
	policySHA256IsNull := policySHA256Present && policySHA256 == nil
	if policyIsNull && !policySHA256IsNull {
		add(
			"/request/policy_sha256",
			"policy_evidence",
			"policy evidence requires a requested policy",
		)
	} else if summary["status"] != "error" && policyIsNull != policySHA256IsNull {
		add(
			"/request/policy_sha256",
			"policy_evidence",
			"normal assessment reports require policy bytes and evidence together",
		)
	}

	reportError, _ := report["error"].(map[string]any)
	stalePolicy, stalePolicyOK := report["stale_policy"].([]any)
	errorKind, _ := reportError["kind"].(string)
	earlyError := summary["status"] == "error" && (errorKind == "no_saved_plans" || errorKind == "policy_error")
	if earlyError && len(roots) != 0 {
		add(
			"/roots",
			"error_phase",
			errorKind+" reports cannot contain assessed roots",
		)
	}
	if earlyError && stalePolicyOK && len(stalePolicy) != 0 {
		add(
			"/stale_policy",
			"error_phase",
			errorKind+" reports cannot contain stale policy entries",
		)
	}
	if errorKind == "policy_error" {
		if _, ok := policy.(string); !ok {
			add(
				"/request/policy",
				"error_phase",
				"policy_error requires a requested policy",
			)
		}
	}
	if errorKind == "no_saved_plans" && policyIsNull != policySHA256IsNull {
		add(
			"/request/policy_sha256",
			"error_phase",
			"no_saved_plans requires completed policy evidence",
		)
	}
	if stalePolicyOK && len(stalePolicy) != 0 && (policyIsNull || policySHA256IsNull) {
		add(
			"/stale_policy",
			"policy_evidence",
			"stale policy entries require bound policy evidence",
		)
	}
	if stalePolicyOK {
		staleKeys := make(map[string]struct{})
		for index, candidate := range stalePolicy {
			entry, ok := candidate.(map[string]any)
			if !ok {
				continue
			}
			resourceType, resourceOK := entry["resource_type"].(string)
			mode, modeOK := entry["mode"].(string)
			path, pathOK := entry["path"].(string)
			if !resourceOK || !modeOK || !pathOK {
				continue
			}
			if _, checked := checkedTypes[resourceType]; !checked {
				add(
					fmt.Sprintf("/stale_policy/%d/resource_type", index),
					"stale_policy_scope",
					"stale policy resource type must be present in an assessed root",
				)
			}
			key := lengthPrefixedIdentity(resourceType, mode, path)
			if _, duplicate := staleKeys[key]; duplicate {
				add(
					fmt.Sprintf("/stale_policy/%d", index),
					"stale_policy_identity",
					"stale policy entries must be unique",
				)
			}
			staleKeys[key] = struct{}{}
		}
	}
	return validation.details
}

func isAssessmentStatus(status string) bool {
	return status == "clean" || status == "clean_with_tolerated_drift" || status == "blocked"
}

func derivedAssessmentStatus(statuses []string) string {
	if containsStatus(statuses, "blocked") {
		return "blocked"
	}
	if containsStatus(statuses, "clean_with_tolerated_drift") {
		return "clean_with_tolerated_drift"
	}
	return "clean"
}

func containsStatus(statuses []string, status string) bool {
	for _, candidate := range statuses {
		if candidate == status {
			return true
		}
	}
	return false
}

func countStatus(statuses []string, status string) int64 {
	var count int64
	for _, candidate := range statuses {
		if candidate == status {
			count++
		}
	}
	return count
}
