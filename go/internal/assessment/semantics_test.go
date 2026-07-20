package assessment

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strconv"
	"testing"

	"github.com/dvmrry/infrawright-dev/go/internal/procerr"
)

func cloneAssessmentValue(t *testing.T, value map[string]any) map[string]any {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("json.Marshal(assessment value) error = %v, want nil", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var cloned map[string]any
	if err := decoder.Decode(&cloned); err != nil {
		t.Fatalf("json.Decoder.Decode(assessment value) error = %v, want nil", err)
	}
	return cloned
}

func cleanAssessmentValue(t *testing.T) map[string]any {
	t.Helper()
	return assessmentReportJSONValue(buildReportForTest(t, Clean))
}

func earlyErrorAssessmentValue(t *testing.T, kind string) map[string]any {
	t.Helper()
	value := cleanAssessmentValue(t)
	value["summary"] = map[string]any{
		"status": "error", "checked": json.Number("0"), "clean": json.Number("0"),
		"tolerated": json.Number("0"), "blocked": json.Number("0"),
	}
	value["roots"] = []any{}
	value["stale_policy"] = []any{}
	value["error"] = map[string]any{"kind": kind, "message": "sanitized fixture"}
	return value
}

func assertAssessmentDetails(
	t *testing.T,
	name string,
	value map[string]any,
	want []procerr.ErrorDetail,
) {
	t.Helper()
	valid, got := ValidateSavedPlanAssessment(value)
	if valid {
		t.Errorf("ValidateSavedPlanAssessment(%s) = true, want false", name)
		return
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ValidateSavedPlanAssessment(%s) details = %#v, want %#v", name, got, want)
	}
}

func TestAssessmentValidatorErrorDetailParity(t *testing.T) {
	t.Run("forged counts", func(t *testing.T) {
		value := cleanAssessmentValue(t)
		summary := value["summary"].(map[string]any)
		summary["checked"] = json.Number("999")
		summary["clean"] = json.Number("999")
		assertAssessmentDetails(t, "forged counts", value, []procerr.ErrorDetail{
			{Path: "/summary/checked", Code: AssessmentSemanticsKeyword, Message: "checked must equal the count derived from roots"},
			{Path: "/summary/clean", Code: AssessmentSemanticsKeyword, Message: "clean must equal the count derived from roots"},
		})
	})

	t.Run("tenant mismatch", func(t *testing.T) {
		value := cleanAssessmentValue(t)
		value["roots"].([]any)[0].(map[string]any)["tenant"] = "other"
		assertAssessmentDetails(t, "tenant mismatch", value, []procerr.ErrorDetail{{
			Path: "/roots/0/tenant", Code: AssessmentSemanticsKeyword,
			Message: "root tenant must match the requested tenant",
		}})
	})

	t.Run("normal report without roots", func(t *testing.T) {
		value := cleanAssessmentValue(t)
		value["roots"] = []any{}
		value["summary"] = map[string]any{
			"status": "clean", "checked": json.Number("999"), "clean": json.Number("999"),
			"tolerated": json.Number("0"), "blocked": json.Number("0"),
		}
		assertAssessmentDetails(t, "normal report without roots", value, []procerr.ErrorDetail{
			{Path: "/roots", Code: "minItems", Message: "must NOT have fewer than 1 items"},
			{Path: "/", Code: "if", Message: `must match "else" schema`},
			{Path: "/summary/checked", Code: AssessmentSemanticsKeyword, Message: "checked must equal the count derived from roots"},
			{Path: "/summary/clean", Code: AssessmentSemanticsKeyword, Message: "clean must equal the count derived from roots"},
			{Path: "/stale_policy/0/resource_type", Code: AssessmentSemanticsKeyword, Message: "stale policy resource type must be present in an assessed root"},
		})
	})

	t.Run("error on normal report", func(t *testing.T) {
		value := cleanAssessmentValue(t)
		value["error"] = map[string]any{"kind": "assessment_error", "message": "contradiction"}
		assertAssessmentDetails(t, "error on normal report", value, []procerr.ErrorDetail{
			{Path: "/error", Code: "false schema", Message: "boolean schema is false"},
			{Path: "/", Code: "if", Message: `must match "else" schema`},
		})
	})

	t.Run("invalid root structure", func(t *testing.T) {
		value := cleanAssessmentValue(t)
		root := value["roots"].([]any)[0].(map[string]any)
		root["label"] = "BAD"
		root["members"] = []any{}
		assertAssessmentDetails(t, "invalid root structure", value, []procerr.ErrorDetail{
			{Path: "/roots/0/label", Code: "pattern", Message: `must match pattern "^[a-z0-9_]+$"`},
			{Path: "/roots/0/members", Code: "minItems", Message: "must NOT have fewer than 1 items"},
			{Path: "/stale_policy/0/resource_type", Code: AssessmentSemanticsKeyword, Message: "stale policy resource type must be present in an assessed root"},
		})
	})

	t.Run("invalid request tenant oneOf", func(t *testing.T) {
		value := cleanAssessmentValue(t)
		value["request"].(map[string]any)["tenant"] = "."
		assertAssessmentDetails(t, "invalid request tenant oneOf", value, []procerr.ErrorDetail{
			{Path: "/request/tenant", Code: "type", Message: "must be null"},
			{Path: "/request/tenant", Code: "pattern", Message: `must match pattern "^(?!\.{1,2}$)[A-Za-z0-9_.-]+$"`},
			{Path: "/request/tenant", Code: "oneOf", Message: "must match exactly one schema in oneOf"},
			{Path: "/roots/0/tenant", Code: AssessmentSemanticsKeyword, Message: "root tenant must match the requested tenant"},
		})
	})

	t.Run("fractional summary count", func(t *testing.T) {
		value := cleanAssessmentValue(t)
		value["summary"].(map[string]any)["checked"] = json.Number("0.5")
		assertAssessmentDetails(t, "fractional summary count", value, []procerr.ErrorDetail{
			{Path: "/summary/checked", Code: "type", Message: "must be integer"},
			{Path: "/summary/checked", Code: "minimum", Message: "must be >= 1"},
			{Path: "/", Code: "if", Message: `must match "else" schema`},
			{Path: "/summary/checked", Code: "type", Message: "must be integer"},
			{Path: "/summary/checked", Code: AssessmentSemanticsKeyword, Message: "checked must equal the count derived from roots"},
		})
	})
}

func TestAssessmentValidatorJSONSchemaBranchTruthParity(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(map[string]any)
		want   []procerr.ErrorDetail
	}{
		{
			name: "summary null",
			mutate: func(value map[string]any) {
				value["summary"] = nil
			},
			want: []procerr.ErrorDetail{
				{Path: "/summary", Code: "type", Message: "must be object"},
				{Path: "/", Code: "if", Message: `must match "else" schema`},
				{Path: "/summary", Code: "type", Message: "must be object"},
			},
		},
		{
			name: "summary string",
			mutate: func(value map[string]any) {
				value["summary"] = "invalid"
			},
			want: []procerr.ErrorDetail{
				{Path: "/summary", Code: "type", Message: "must be object"},
				{Path: "/", Code: "if", Message: `must match "else" schema`},
				{Path: "/summary", Code: "type", Message: "must be object"},
			},
		},
		{
			name: "summary array",
			mutate: func(value map[string]any) {
				value["summary"] = []any{}
			},
			want: []procerr.ErrorDetail{
				{Path: "/summary", Code: "type", Message: "must be object"},
				{Path: "/", Code: "if", Message: `must match "else" schema`},
				{Path: "/summary", Code: "type", Message: "must be object"},
			},
		},
		{
			name: "summary number",
			mutate: func(value map[string]any) {
				value["summary"] = json.Number("3")
			},
			want: []procerr.ErrorDetail{
				{Path: "/summary", Code: "type", Message: "must be object"},
				{Path: "/", Code: "if", Message: `must match "else" schema`},
				{Path: "/summary", Code: "type", Message: "must be object"},
			},
		},
		{
			name: "status array",
			mutate: func(value map[string]any) {
				value["summary"].(map[string]any)["status"] = []any{}
			},
			want: []procerr.ErrorDetail{
				{Path: "/summary/status", Code: "enum", Message: "must be equal to one of the allowed values"},
				{Path: "/summary/status", Code: AssessmentSemanticsKeyword, Message: "summary status must be derived from root statuses"},
			},
		},
		{
			name: "status number",
			mutate: func(value map[string]any) {
				value["summary"].(map[string]any)["status"] = json.Number("3")
			},
			want: []procerr.ErrorDetail{
				{Path: "/summary/status", Code: "enum", Message: "must be equal to one of the allowed values"},
				{Path: "/summary/status", Code: AssessmentSemanticsKeyword, Message: "summary status must be derived from root statuses"},
			},
		},
		{
			name: "status missing",
			mutate: func(value map[string]any) {
				delete(value["summary"].(map[string]any), "status")
			},
			want: []procerr.ErrorDetail{
				{Path: "/", Code: "required", Message: "must have required property 'error'"},
				{Path: "/", Code: "if", Message: `must match "then" schema`},
				{Path: "/summary/tolerated", Code: "minimum", Message: "must be >= 1"},
				{Path: "/roots/0/status", Code: "const", Message: "must be equal to constant"},
				{Path: "/roots", Code: "contains", Message: "must contain at least 1 valid item(s)"},
				{Path: "/", Code: "if", Message: `must match "then" schema`},
				{Path: "/summary/blocked", Code: "minimum", Message: "must be >= 1"},
				{Path: "/roots/0/status", Code: "const", Message: "must be equal to constant"},
				{Path: "/roots", Code: "contains", Message: "must contain at least 1 valid item(s)"},
				{Path: "/", Code: "if", Message: `must match "then" schema`},
				{Path: "/summary", Code: "required", Message: "must have required property 'status'"},
				{Path: "/summary/status", Code: AssessmentSemanticsKeyword, Message: "summary status must be derived from root statuses"},
			},
		},
		{
			name: "mode array",
			mutate: func(value map[string]any) {
				value["mode"] = []any{}
			},
			want: []procerr.ErrorDetail{{
				Path: "/mode", Code: "enum", Message: "must be equal to one of the allowed values",
			}},
		},
		{
			name: "mode number",
			mutate: func(value map[string]any) {
				value["mode"] = json.Number("3")
			},
			want: []procerr.ErrorDetail{{
				Path: "/mode", Code: "enum", Message: "must be equal to one of the allowed values",
			}},
		},
		{
			name: "mode missing",
			mutate: func(value map[string]any) {
				delete(value, "mode")
			},
			want: []procerr.ErrorDetail{
				{Path: "/request/policy", Code: "type", Message: "must be null"},
				{Path: "/request/policy_sha256", Code: "type", Message: "must be null"},
				{Path: "/", Code: "if", Message: `must match "then" schema`},
				{Path: "/", Code: "required", Message: "must have required property 'mode'"},
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value := cleanAssessmentValue(t)
			test.mutate(value)
			assertAssessmentDetails(t, test.name, value, test.want)
		})
	}
}

func TestAssessmentValidatorNumericKeywordParity(t *testing.T) {
	tests := []struct {
		name  string
		field string
		value json.Number
		want  []procerr.ErrorDetail
	}{
		{
			name: "checked huge finite integer", field: "checked", value: json.Number("1e100"),
			want: []procerr.ErrorDetail{{
				Path: "/summary/checked", Code: AssessmentSemanticsKeyword,
				Message: "checked must equal the count derived from roots",
			}},
		},
		{
			name: "checked at int64 maximum", field: "checked", value: json.Number("9223372036854775807"),
			want: []procerr.ErrorDetail{{
				Path: "/summary/checked", Code: AssessmentSemanticsKeyword,
				Message: "checked must equal the count derived from roots",
			}},
		},
		{
			name: "checked above int64", field: "checked", value: json.Number("9223372036854775808"),
			want: []procerr.ErrorDetail{{
				Path: "/summary/checked", Code: AssessmentSemanticsKeyword,
				Message: "checked must equal the count derived from roots",
			}},
		},
		{
			name: "checked fractional positive", field: "checked", value: json.Number("0.5"),
			want: []procerr.ErrorDetail{
				{Path: "/summary/checked", Code: "type", Message: "must be integer"},
				{Path: "/summary/checked", Code: "minimum", Message: "must be >= 1"},
				{Path: "/", Code: "if", Message: `must match "else" schema`},
				{Path: "/summary/checked", Code: "type", Message: "must be integer"},
				{Path: "/summary/checked", Code: AssessmentSemanticsKeyword, Message: "checked must equal the count derived from roots"},
			},
		},
		{
			name: "checked fractional negative", field: "checked", value: json.Number("-0.5"),
			want: []procerr.ErrorDetail{
				{Path: "/summary/checked", Code: "type", Message: "must be integer"},
				{Path: "/summary/checked", Code: "minimum", Message: "must be >= 1"},
				{Path: "/", Code: "if", Message: `must match "else" schema`},
				{Path: "/summary/checked", Code: "type", Message: "must be integer"},
				{Path: "/summary/checked", Code: "minimum", Message: "must be >= 0"},
				{Path: "/summary/checked", Code: AssessmentSemanticsKeyword, Message: "checked must equal the count derived from roots"},
			},
		},
		{
			name: "clean fractional", field: "clean", value: json.Number("0.5"),
			want: []procerr.ErrorDetail{
				{Path: "/summary/clean", Code: "type", Message: "must be integer"},
				{Path: "/summary/clean", Code: "minimum", Message: "must be >= 1"},
				{Path: "/", Code: "if", Message: `must match "then" schema`},
				{Path: "/summary/clean", Code: "type", Message: "must be integer"},
				{Path: "/summary/clean", Code: AssessmentSemanticsKeyword, Message: "clean must equal the count derived from roots"},
			},
		},
		{
			name: "tolerated fractional", field: "tolerated", value: json.Number("0.5"),
			want: []procerr.ErrorDetail{
				{Path: "/summary/tolerated", Code: "type", Message: "must be integer"},
				{Path: "/summary/tolerated", Code: "const", Message: "must be equal to constant"},
				{Path: "/", Code: "if", Message: `must match "then" schema`},
				{Path: "/summary/tolerated", Code: "type", Message: "must be integer"},
				{Path: "/summary/tolerated", Code: AssessmentSemanticsKeyword, Message: "tolerated must equal the count derived from roots"},
			},
		},
		{
			name: "blocked fractional", field: "blocked", value: json.Number("0.5"),
			want: []procerr.ErrorDetail{
				{Path: "/summary/blocked", Code: "type", Message: "must be integer"},
				{Path: "/summary/blocked", Code: "const", Message: "must be equal to constant"},
				{Path: "/", Code: "if", Message: `must match "then" schema`},
				{Path: "/summary/blocked", Code: "type", Message: "must be integer"},
				{Path: "/summary/blocked", Code: AssessmentSemanticsKeyword, Message: "blocked must equal the count derived from roots"},
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value := cleanAssessmentValue(t)
			value["summary"].(map[string]any)[test.field] = test.value
			assertAssessmentDetails(t, test.name, value, test.want)
		})
	}

	for _, field := range []string{"tolerated", "blocked"} {
		t.Run(field+" string", func(t *testing.T) {
			value := cleanAssessmentValue(t)
			value["summary"].(map[string]any)[field] = "0"
			assertAssessmentDetails(t, field+" string", value, []procerr.ErrorDetail{
				{Path: "/summary/" + field, Code: "type", Message: "must be integer"},
				{Path: "/summary/" + field, Code: "const", Message: "must be equal to constant"},
				{Path: "/", Code: "if", Message: `must match "then" schema`},
				{Path: "/summary/" + field, Code: "type", Message: "must be integer"},
				{Path: "/summary/" + field, Code: AssessmentSemanticsKeyword, Message: field + " must equal the count derived from roots"},
			})
		})
	}

	t.Run("clean string", func(t *testing.T) {
		value := cleanAssessmentValue(t)
		value["summary"].(map[string]any)["clean"] = "1"
		assertAssessmentDetails(t, "clean string", value, []procerr.ErrorDetail{
			{Path: "/summary/clean", Code: "type", Message: "must be integer"},
			{Path: "/", Code: "if", Message: `must match "then" schema`},
			{Path: "/summary/clean", Code: "type", Message: "must be integer"},
			{Path: "/summary/clean", Code: AssessmentSemanticsKeyword, Message: "clean must equal the count derived from roots"},
		})
	})
}

func TestAssessmentValidatorContainsNotUniqueAndTruncationParity(t *testing.T) {
	t.Run("tolerated summary with clean root", func(t *testing.T) {
		value := cleanAssessmentValue(t)
		value["summary"].(map[string]any)["status"] = "clean_with_tolerated_drift"
		assertAssessmentDetails(t, "tolerated summary with clean root", value, []procerr.ErrorDetail{
			{Path: "/summary/tolerated", Code: "minimum", Message: "must be >= 1"},
			{Path: "/roots/0/status", Code: "const", Message: "must be equal to constant"},
			{Path: "/roots", Code: "contains", Message: "must contain at least 1 valid item(s)"},
			{Path: "/", Code: "if", Message: `must match "then" schema`},
			{Path: "/summary/status", Code: AssessmentSemanticsKeyword, Message: "summary status must be derived from root statuses"},
		})
	})

	t.Run("blocked summary with clean root", func(t *testing.T) {
		value := cleanAssessmentValue(t)
		value["summary"].(map[string]any)["status"] = "blocked"
		assertAssessmentDetails(t, "blocked summary with clean root", value, []procerr.ErrorDetail{
			{Path: "/summary/blocked", Code: "minimum", Message: "must be >= 1"},
			{Path: "/roots/0/status", Code: "const", Message: "must be equal to constant"},
			{Path: "/roots", Code: "contains", Message: "must contain at least 1 valid item(s)"},
			{Path: "/", Code: "if", Message: `must match "then" schema`},
			{Path: "/summary/status", Code: AssessmentSemanticsKeyword, Message: "summary status must be derived from root statuses"},
		})
	})

	t.Run("tolerated branch sees blocked root before contains", func(t *testing.T) {
		value := assessmentReportJSONValue(buildReportForTest(t, Blocked))
		summary := value["summary"].(map[string]any)
		summary["status"] = "clean_with_tolerated_drift"
		summary["tolerated"] = json.Number("1")
		summary["blocked"] = json.Number("0")
		assertAssessmentDetails(t, "tolerated branch with blocked root", value, []procerr.ErrorDetail{
			{Path: "/roots", Code: "not", Message: "must NOT be valid"},
			{Path: "/roots/0/status", Code: "const", Message: "must be equal to constant"},
			{Path: "/roots", Code: "contains", Message: "must contain at least 1 valid item(s)"},
			{Path: "/", Code: "if", Message: `must match "then" schema`},
			{Path: "/summary/tolerated", Code: AssessmentSemanticsKeyword, Message: "tolerated must equal the count derived from roots"},
			{Path: "/summary/blocked", Code: AssessmentSemanticsKeyword, Message: "blocked must equal the count derived from roots"},
			{Path: "/summary/status", Code: AssessmentSemanticsKeyword, Message: "summary status must be derived from root statuses"},
		})
	})

	t.Run("properties are vacuously true for missing root status", func(t *testing.T) {
		value := cleanAssessmentValue(t)
		value["summary"].(map[string]any)["status"] = "clean_with_tolerated_drift"
		delete(value["roots"].([]any)[0].(map[string]any), "status")
		assertAssessmentDetails(t, "missing root status", value, []procerr.ErrorDetail{
			{Path: "/summary/tolerated", Code: "minimum", Message: "must be >= 1"},
			{Path: "/roots", Code: "not", Message: "must NOT be valid"},
			{Path: "/", Code: "if", Message: `must match "then" schema`},
			{Path: "/roots/0", Code: "required", Message: "must have required property 'status'"},
		})
	})

	t.Run("unique triple reports reverse duplicate pair once", func(t *testing.T) {
		value := cleanAssessmentValue(t)
		value["roots"].([]any)[0].(map[string]any)["members"] = []any{
			"zpa_sample", "zpa_sample", "zpa_sample",
		}
		assertAssessmentDetails(t, "unique triple", value, []procerr.ErrorDetail{
			{
				Path: "/roots/0/members", Code: "uniqueItems",
				Message: "must NOT have duplicate items (items ## 2 and 1 are identical)",
			},
			{
				Path: "/roots/0/members", Code: AssessmentSemanticsKeyword,
				Message: "a resource type can belong to only one selected root per tenant",
			},
			{
				Path: "/roots/0/members", Code: AssessmentSemanticsKeyword,
				Message: "a resource type can belong to only one selected root per tenant",
			},
		})
	})

	uniqueItemTests := []struct {
		name    string
		members []any
		want    []procerr.ErrorDetail
	}{
		{
			name: "duplicate numbers are only item type errors",
			members: []any{
				json.Number("1"), json.Number("1"),
			},
			want: []procerr.ErrorDetail{
				{Path: "/roots/0/members/0", Code: "type", Message: "must be string"},
				{Path: "/roots/0/members/1", Code: "type", Message: "must be string"},
			},
		},
		{
			name:    "duplicate nulls are only item type errors",
			members: []any{nil, nil},
			want: []procerr.ErrorDetail{
				{Path: "/roots/0/members/0", Code: "type", Message: "must be string"},
				{Path: "/roots/0/members/1", Code: "type", Message: "must be string"},
			},
		},
		{
			name:    "valid duplicate strings before malformed item",
			members: []any{"x", "x", json.Number("1")},
			want: []procerr.ErrorDetail{
				{Path: "/roots/0/members/2", Code: "type", Message: "must be string"},
				{
					Path: "/roots/0/members", Code: "uniqueItems",
					Message: "must NOT have duplicate items (items ## 1 and 0 are identical)",
				},
				{
					Path: "/roots/0/members", Code: AssessmentSemanticsKeyword,
					Message: "a resource type can belong to only one selected root per tenant",
				},
			},
		},
		{
			name:    "valid duplicate strings after malformed item",
			members: []any{json.Number("1"), "x", "x"},
			want: []procerr.ErrorDetail{
				{Path: "/roots/0/members/0", Code: "type", Message: "must be string"},
				{
					Path: "/roots/0/members", Code: "uniqueItems",
					Message: "must NOT have duplicate items (items ## 2 and 1 are identical)",
				},
				{
					Path: "/roots/0/members", Code: AssessmentSemanticsKeyword,
					Message: "a resource type can belong to only one selected root per tenant",
				},
			},
		},
	}
	for _, test := range uniqueItemTests {
		t.Run(test.name, func(t *testing.T) {
			value := cleanAssessmentValue(t)
			value["roots"].([]any)[0].(map[string]any)["members"] = test.members
			value["stale_policy"] = []any{}
			assertAssessmentDetails(t, test.name, value, test.want)
		})
	}

	t.Run("first 64 contains item failures then truncation", func(t *testing.T) {
		value := cleanAssessmentValue(t)
		template := value["roots"].([]any)[0]
		roots := make([]any, 70)
		for index := range roots {
			cloned := cloneAssessmentValue(t, map[string]any{"root": template})["root"].(map[string]any)
			cloned["label"] = "root_" + strconv.Itoa(index)
			if index != 0 {
				cloned["members"] = []any{"zpa_sample_" + strconv.Itoa(index)}
			}
			roots[index] = cloned
		}
		value["roots"] = roots
		value["summary"] = map[string]any{
			"status": "blocked", "checked": json.Number("70"), "clean": json.Number("0"),
			"tolerated": json.Number("0"), "blocked": json.Number("70"),
		}
		want := make([]procerr.ErrorDetail, 0, 65)
		for index := range 64 {
			want = append(want, procerr.ErrorDetail{
				Path: "/roots/" + strconv.Itoa(index) + "/status",
				Code: "const", Message: "must be equal to constant",
			})
		}
		want = append(want, procerr.ErrorDetail{
			Path: "/", Code: "schema_errors_truncated", Message: "additional schema errors were omitted",
		})
		assertAssessmentDetails(t, "contains truncation", value, want)
	})
}

func TestAssessmentValidatorPolicyMissingAndNullParity(t *testing.T) {
	tests := []struct {
		name   string
		value  func(*testing.T) map[string]any
		mutate func(map[string]any)
		want   []procerr.ErrorDetail
	}{
		{
			name: "normal missing policy", value: cleanAssessmentValue,
			mutate: func(value map[string]any) { delete(value["request"].(map[string]any), "policy") },
			want:   []procerr.ErrorDetail{{Path: "/request", Code: "required", Message: "must have required property 'policy'"}},
		},
		{
			name: "normal missing policy digest", value: cleanAssessmentValue,
			mutate: func(value map[string]any) { delete(value["request"].(map[string]any), "policy_sha256") },
			want:   []procerr.ErrorDetail{{Path: "/request", Code: "required", Message: "must have required property 'policy_sha256'"}},
		},
		{
			name: "normal without stale policy missing policy",
			value: func(t *testing.T) map[string]any {
				value := cleanAssessmentValue(t)
				value["stale_policy"] = []any{}
				return value
			},
			mutate: func(value map[string]any) { delete(value["request"].(map[string]any), "policy") },
			want:   []procerr.ErrorDetail{{Path: "/request", Code: "required", Message: "must have required property 'policy'"}},
		},
		{
			name: "normal without stale policy missing policy digest",
			value: func(t *testing.T) map[string]any {
				value := cleanAssessmentValue(t)
				value["stale_policy"] = []any{}
				return value
			},
			mutate: func(value map[string]any) { delete(value["request"].(map[string]any), "policy_sha256") },
			want:   []procerr.ErrorDetail{{Path: "/request", Code: "required", Message: "must have required property 'policy_sha256'"}},
		},
		{
			name:   "no saved plans missing policy",
			value:  func(t *testing.T) map[string]any { return earlyErrorAssessmentValue(t, "no_saved_plans") },
			mutate: func(value map[string]any) { delete(value["request"].(map[string]any), "policy") },
			want:   []procerr.ErrorDetail{{Path: "/request", Code: "required", Message: "must have required property 'policy'"}},
		},
		{
			name:   "no saved plans missing policy digest",
			value:  func(t *testing.T) map[string]any { return earlyErrorAssessmentValue(t, "no_saved_plans") },
			mutate: func(value map[string]any) { delete(value["request"].(map[string]any), "policy_sha256") },
			want:   []procerr.ErrorDetail{{Path: "/request", Code: "required", Message: "must have required property 'policy_sha256'"}},
		},
		{
			name:   "policy error missing policy",
			value:  func(t *testing.T) map[string]any { return earlyErrorAssessmentValue(t, "policy_error") },
			mutate: func(value map[string]any) { delete(value["request"].(map[string]any), "policy") },
			want: []procerr.ErrorDetail{
				{Path: "/request", Code: "required", Message: "must have required property 'policy'"},
				{Path: "/request/policy", Code: AssessmentSemanticsKeyword, Message: "policy_error requires a requested policy"},
			},
		},
		{
			name:   "policy error missing policy digest",
			value:  func(t *testing.T) map[string]any { return earlyErrorAssessmentValue(t, "policy_error") },
			mutate: func(value map[string]any) { delete(value["request"].(map[string]any), "policy_sha256") },
			want:   []procerr.ErrorDetail{{Path: "/request", Code: "required", Message: "must have required property 'policy_sha256'"}},
		},
		{
			name:   "no saved plans null policy",
			value:  func(t *testing.T) map[string]any { return earlyErrorAssessmentValue(t, "no_saved_plans") },
			mutate: func(value map[string]any) { value["request"].(map[string]any)["policy"] = nil },
			want: []procerr.ErrorDetail{
				{Path: "/request/policy_sha256", Code: AssessmentSemanticsKeyword, Message: "policy evidence requires a requested policy"},
				{Path: "/request/policy_sha256", Code: AssessmentSemanticsKeyword, Message: "no_saved_plans requires completed policy evidence"},
			},
		},
		{
			name:   "no saved plans null policy digest",
			value:  func(t *testing.T) map[string]any { return earlyErrorAssessmentValue(t, "no_saved_plans") },
			mutate: func(value map[string]any) { value["request"].(map[string]any)["policy_sha256"] = nil },
			want: []procerr.ErrorDetail{{
				Path: "/request/policy_sha256", Code: AssessmentSemanticsKeyword,
				Message: "no_saved_plans requires completed policy evidence",
			}},
		},
		{
			name:   "policy error null policy",
			value:  func(t *testing.T) map[string]any { return earlyErrorAssessmentValue(t, "policy_error") },
			mutate: func(value map[string]any) { value["request"].(map[string]any)["policy"] = nil },
			want: []procerr.ErrorDetail{
				{Path: "/request/policy_sha256", Code: AssessmentSemanticsKeyword, Message: "policy evidence requires a requested policy"},
				{Path: "/request/policy", Code: AssessmentSemanticsKeyword, Message: "policy_error requires a requested policy"},
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value := test.value(t)
			test.mutate(value)
			assertAssessmentDetails(t, test.name, value, test.want)
		})
	}

	t.Run("policy error null digest remains valid", func(t *testing.T) {
		value := earlyErrorAssessmentValue(t, "policy_error")
		value["request"].(map[string]any)["policy_sha256"] = nil
		valid, details := ValidateSavedPlanAssessment(value)
		if !valid || len(details) != 0 {
			t.Errorf("ValidateSavedPlanAssessment(policy_error null digest) = (%v, %#v), want (true, nil)", valid, details)
		}
	})
}

func TestAssessmentValidatorAdditionalPropertiesAreDeterministicAndSanitized(t *testing.T) {
	value := cleanAssessmentValue(t)
	value["z-extra"] = true
	value["a-extra"] = true
	request := value["request"].(map[string]any)
	request["y-extra"] = true
	request["b-extra"] = true
	want := []procerr.ErrorDetail{
		{Path: "/", Code: "additionalProperties", Message: "additional property is not allowed"},
		{Path: "/", Code: "additionalProperties", Message: "additional property is not allowed"},
		{Path: "/request", Code: "additionalProperties", Message: "additional property is not allowed"},
		{Path: "/request", Code: "additionalProperties", Message: "additional property is not allowed"},
	}
	for iteration := range 20 {
		assertAssessmentDetails(t, "deterministic extras "+strconv.Itoa(iteration), value, want)
	}

	many := cleanAssessmentValue(t)
	for index := range 70 {
		many["extra_"+strconv.Itoa(index)] = index
	}
	wantMany := make([]procerr.ErrorDetail, 64, 65)
	for index := range wantMany {
		wantMany[index] = procerr.ErrorDetail{
			Path: "/", Code: "additionalProperties", Message: "additional property is not allowed",
		}
	}
	wantMany = append(wantMany, procerr.ErrorDetail{
		Path: "/", Code: "schema_errors_truncated", Message: "additional schema errors were omitted",
	})
	assertAssessmentDetails(t, "many deterministic extras", many, wantMany)
}

func TestAssessmentValidatorRejectsSemanticContradictions(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{
			name: "duplicate root identity and membership",
			mutate: func(value map[string]any) {
				root := cloneAssessmentValue(t, value)["roots"].([]any)[0]
				value["roots"] = append(value["roots"].([]any), root)
				summary := value["summary"].(map[string]any)
				summary["checked"] = json.Number("2")
				summary["clean"] = json.Number("2")
			},
		},
		{
			name: "duplicate stale policy",
			mutate: func(value map[string]any) {
				entry := cloneAssessmentValue(t, value)["stale_policy"].([]any)[0]
				value["stale_policy"] = append(value["stale_policy"].([]any), entry)
			},
		},
		{
			name: "blocked finding under clean root",
			mutate: func(value map[string]any) {
				value["roots"].([]any)[0].(map[string]any)["findings"] = []any{map[string]any{
					"status": "blocked", "source": "resource_changes", "address": "zpa_sample.this",
					"resource_type": "zpa_sample", "actions": []any{"update"}, "paths": []any{"name"},
				}}
			},
		},
		{
			name: "guidance without blocked finding",
			mutate: func(value map[string]any) {
				value["roots"].([]any)[0].(map[string]any)["guidance"] = []any{map[string]any{
					"lane": "absent_default", "source": "resource_changes", "address": "zpa_sample.this",
					"finding_path": "name", "matched_plan_path": "name",
					"status_effect": "informational only; plan remains blocked",
				}}
			},
		},
		{
			name: "policy evidence without request",
			mutate: func(value map[string]any) {
				value["request"].(map[string]any)["policy"] = nil
			},
		},
		{
			name: "stale policy outside checked roots",
			mutate: func(value map[string]any) {
				value["stale_policy"].([]any)[0].(map[string]any)["resource_type"] = "zpa_other"
			},
		},
		{
			name: "internal guidance sort key",
			mutate: func(value map[string]any) {
				blocked := assessmentReportJSONValue(buildReportForTest(t, Blocked))
				for key, child := range blocked {
					value[key] = child
				}
				value["roots"].([]any)[0].(map[string]any)["guidance"].([]any)[0].(map[string]any)["sort_key"] = []any{"internal"}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value := cleanAssessmentValue(t)
			test.mutate(value)
			valid, details := ValidateSavedPlanAssessment(value)
			if valid || len(details) == 0 {
				t.Errorf("ValidateSavedPlanAssessment(%s contradiction) = (%v, %#v), want false with details", test.name, valid, details)
			}
		})
	}
}

func TestAssessmentSemanticErrorOrderIsSourceOrder(t *testing.T) {
	value := cleanAssessmentValue(t)
	root := value["roots"].([]any)[0].(map[string]any)
	duplicate := cloneAssessmentValue(t, value)["roots"].([]any)[0].(map[string]any)
	duplicate["label"] = "other_root"
	value["roots"] = append(value["roots"].([]any), duplicate)
	summary := value["summary"].(map[string]any)
	summary["checked"] = json.Number("2")
	summary["clean"] = json.Number("2")
	root["members"] = []any{"zpa_sample", "zpa_sample"}

	_, details := ValidateSavedPlanAssessment(value)
	var semantic []procerr.ErrorDetail
	for _, detail := range details {
		if detail.Code == AssessmentSemanticsKeyword {
			semantic = append(semantic, detail)
		}
	}
	want := []procerr.ErrorDetail{
		{Path: "/roots/0/members", Code: AssessmentSemanticsKeyword, Message: "a resource type can belong to only one selected root per tenant"},
		{Path: "/roots/1/members", Code: AssessmentSemanticsKeyword, Message: "a resource type can belong to only one selected root per tenant"},
	}
	if !reflect.DeepEqual(semantic, want) {
		t.Errorf("ValidateSavedPlanAssessment(source-order vector) semantic details = %#v, want %#v", semantic, want)
	}
}
