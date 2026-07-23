package assessment

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"strings"
	"testing"

	"github.com/dvmrry/infrawright-dev/go/internal/metadata"
	"github.com/dvmrry/infrawright-dev/go/internal/plan"
	"github.com/dvmrry/infrawright-dev/go/internal/procerr"
)

func reportStringPointer(value string) *string {
	return &value
}

func reportCore(status PlanStatus) SavedPlanAssessmentCore {
	counts := map[PlanStatus][3]int{
		Clean:     {1, 0, 0},
		Tolerated: {0, 1, 0},
		Blocked:   {0, 0, 1},
	}[status]
	var findings []AssessmentFinding
	if status != Clean {
		findings = []AssessmentFinding{{
			Status:       status,
			Source:       "resource_changes",
			Address:      `zpa_sample.this["one"]`,
			ResourceType: reportStringPointer("zpa_sample"),
			Actions:      []string{"update"},
			Paths:        []PlanPath{{"rules", 0, "map.key", `quote"slash\`}},
		}}
	}
	return SavedPlanAssessmentCore{
		Status:       status,
		Checked:      1,
		Clean:        counts[0],
		Tolerated:    counts[1],
		Blocked:      counts[2],
		PolicySHA256: reportStringPointer(strings.Repeat("a", 64)),
		Roots: []AssessedSavedPlanRoot{{
			Tenant:  "tenant",
			Label:   "zpa_custom",
			Members: []string{"zpa_sample"},
			Status:  status,
			Plan: AssessedPlanEvidence{
				SHA256:           strings.Repeat("b", 64),
				FormatVersion:    reportStringPointer("1.2"),
				TerraformVersion: reportStringPointer("1.15.4"),
			},
			PlanFingerprint: plan.PlanFingerprintV2{
				Version: 2,
				SHA256:  strings.Repeat("c", 64),
			},
			Findings: findings,
		}},
		StalePolicy: []metadata.StalePolicyEntry{{
			ResourceType: "zpa_sample",
			Mode:         metadata.PolicyPlanTolerate,
			Path:         "unused",
		}},
	}
}

func reportGuidance(status PlanStatus, observed any) []AssessmentGuidanceGroup {
	var entries []map[string]any
	if status == Blocked {
		entry := map[string]any{
			"lane":              "absent_default",
			"source":            "resource_changes",
			"address":           `zpa_sample.this["one"]`,
			"finding_path":      `rules[0].map.key.quote"slash\`,
			"matched_plan_path": `rules[].map.key.quote"slash\`,
			"status_effect":     "informational only; plan remains blocked",
		}
		if observed == nil {
			entry["reason"] = "fixture"
		} else {
			entry["rule"] = "float-provenance"
			entry["observed_value"] = observed
		}
		entries = []map[string]any{entry}
	}
	return []AssessmentGuidanceGroup{{
		Tenant:  "tenant",
		Label:   "zpa_custom",
		Entries: entries,
	}}
}

func buildReportForTest(t *testing.T, status PlanStatus) SavedPlanAssessmentReport {
	t.Helper()
	report, err := BuildSavedPlanAssessmentReport(BuildSavedPlanAssessmentReportOptions{
		Mode: AssertAdoptable,
		Request: AssessmentReportRequest{
			Tenant:    reportStringPointer("tenant"),
			Selectors: []string{"zpa/sample"},
			Policy:    reportStringPointer("policy.json"),
		},
		Core:     reportCore(status),
		Guidance: reportGuidance(status, nil),
	})
	if err != nil {
		t.Fatalf("BuildSavedPlanAssessmentReport(%q) error = %v, want nil", status, err)
	}
	return report
}

func TestReportGuidanceJoinSortCloneAndExactDedup(t *testing.T) {
	core := reportCore(Blocked)
	base := map[string]any{
		"source":            "resource_changes",
		"address":           `zpa_sample.this["one"]`,
		"finding_path":      `rules[0].map.key.quote"slash\`,
		"matched_plan_path": `rules[].map.key.quote"slash\`,
		"status_effect":     "informational only; plan remains blocked",
		"provider":          "sample",
	}
	absentLossless := cloneGuidanceRecord(base)
	absentLossless["lane"] = "absent_default"
	absentLossless["rule"] = "numeric"
	absentLossless["observed_value"] = json.Number("1e-7")
	absentFloat := cloneGuidanceRecord(absentLossless)
	absentFloat["observed_value"] = 1e-7
	provider := cloneGuidanceRecord(base)
	provider["lane"] = "provider_config"
	provider["setting"] = "tenant"
	provider["nested"] = map[string]any{"values": []any{json.Number("1.0"), "original"}}

	report, err := BuildSavedPlanAssessmentReport(BuildSavedPlanAssessmentReportOptions{
		Mode: AssertAdoptable,
		Request: AssessmentReportRequest{
			Tenant: reportStringPointer("tenant"), Policy: reportStringPointer("policy.json"),
		},
		Core: core,
		Guidance: []AssessmentGuidanceGroup{{
			Tenant: "tenant", Label: "zpa_custom",
			Entries: []map[string]any{absentLossless, provider, absentFloat, provider},
		}},
	})
	if err != nil {
		t.Fatalf("BuildSavedPlanAssessmentReport(guidance sort/dedup) error = %v, want nil", err)
	}
	got := report.Roots[0].Guidance
	if len(got) != 2 || got[0]["lane"] != "provider_config" || got[1]["lane"] != "absent_default" {
		t.Fatalf("BuildSavedPlanAssessmentReport(guidance sort/dedup).Guidance = %#v, want provider_config then one absent_default", got)
	}
	provider["setting"] = "mutated"
	provider["nested"].(map[string]any)["values"].([]any)[1] = "mutated"
	if got[0]["setting"] != "tenant" || got[0]["nested"].(map[string]any)["values"].([]any)[1] != "original" {
		t.Errorf("BuildSavedPlanAssessmentReport(guidance clone).Guidance = %#v, want detached original", got)
	}
}

func TestReportGuidanceAcceptsOnlyExactlyRepresentableGoIntegers(t *testing.T) {
	tests := []struct {
		name      string
		value     any
		wantToken string
		wantError bool
	}{
		{name: "native int", value: int(2), wantToken: `"observed_value": 2`},
		{name: "native uint", value: uint(2), wantToken: `"observed_value": 2`},
		{
			name:  "largest exact power of two",
			value: int64(9_007_199_254_740_992), wantToken: `"observed_value": 9007199254740992`,
		},
		{name: "inexact integer", value: int64(9_007_199_254_740_993), wantError: true},
		{name: "inexact unsigned integer", value: uint64(9_007_199_254_740_993), wantError: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			guidance := reportGuidance(Blocked, test.value)
			report, err := BuildSavedPlanAssessmentReport(BuildSavedPlanAssessmentReportOptions{
				Mode: AssertAdoptable,
				Request: AssessmentReportRequest{
					Tenant: reportStringPointer("tenant"), Policy: reportStringPointer("policy.json"),
				},
				Core: reportCore(Blocked), Guidance: guidance,
			})
			if test.wantError {
				assertReportFailureCode(t, err, "INVALID_ASSESSMENT_GUIDANCE")
				return
			}
			if err != nil {
				t.Fatalf("BuildSavedPlanAssessmentReport(%s) error = %v, want nil", test.name, err)
			}
			rendered, err := RenderAssessmentReport(report)
			if err != nil {
				t.Fatalf("RenderAssessmentReport(%s) error = %v, want nil", test.name, err)
			}
			if !strings.Contains(rendered, test.wantToken) {
				t.Errorf("RenderAssessmentReport(%s) = %q, want token %q", test.name, rendered, test.wantToken)
			}
		})
	}
}

func TestInvalidReportGuidanceFailsClosed(t *testing.T) {
	base := reportGuidance(Blocked, nil)[0].Entries[0]
	tests := []struct {
		name    string
		entries []map[string]any
	}{
		{name: "unjoined", entries: []map[string]any{func() map[string]any {
			entry := cloneGuidanceRecord(base)
			entry["finding_path"] = "wrong.path"
			entry["matched_plan_path"] = "wrong.path"
			return entry
		}()}},
		{name: "leaked sort key", entries: []map[string]any{func() map[string]any {
			entry := cloneGuidanceRecord(base)
			entry["sort_key"] = []any{"internal"}
			return entry
		}()}},
		{name: "non JSON", entries: []map[string]any{func() map[string]any {
			entry := cloneGuidanceRecord(base)
			entry["value"] = mathInf()
			return entry
		}()}},
		{name: "invalid sort field", entries: []map[string]any{
			func() map[string]any {
				entry := cloneGuidanceRecord(base)
				entry["provider"] = json.Number("1")
				entry["lane"] = "absent_default"
				return entry
			}(),
			func() map[string]any {
				entry := cloneGuidanceRecord(base)
				entry["lane"] = "provider_config"
				return entry
			}(),
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := BuildSavedPlanAssessmentReport(BuildSavedPlanAssessmentReportOptions{
				Mode: AssertAdoptable,
				Request: AssessmentReportRequest{
					Tenant: reportStringPointer("tenant"), Policy: reportStringPointer("policy.json"),
				},
				Core: reportCore(Blocked),
				Guidance: []AssessmentGuidanceGroup{{
					Tenant: "tenant", Label: "zpa_custom", Entries: test.entries,
				}},
			})
			var failure *procerr.ProcessFailure
			if !errors.As(err, &failure) || failure.Code != "INVALID_ASSESSMENT_GUIDANCE" {
				t.Errorf("BuildSavedPlanAssessmentReport(%s guidance) error = %v, want INVALID_ASSESSMENT_GUIDANCE", test.name, err)
			}
		})
	}
	_, err := BuildSavedPlanAssessmentReport(BuildSavedPlanAssessmentReportOptions{
		Mode: AssertAdoptable,
		Request: AssessmentReportRequest{
			Tenant: reportStringPointer("tenant"), Policy: reportStringPointer("policy.json"),
		},
		Core:     reportCore(Blocked),
		Guidance: []AssessmentGuidanceGroup{{Tenant: "other", Label: "unknown"}},
	})
	var failure *procerr.ProcessFailure
	if !errors.As(err, &failure) || failure.Code != "INVALID_ASSESSMENT_GUIDANCE" {
		t.Errorf("BuildSavedPlanAssessmentReport(unknown guidance root) error = %v, want INVALID_ASSESSMENT_GUIDANCE", err)
	}
}

func mathInf() float64 {
	return math.Inf(1)
}

func TestReportStatusCountsAndErrorInputsFailClosed(t *testing.T) {
	core := reportCore(Clean)
	core.Status = Blocked
	_, err := BuildSavedPlanAssessmentReport(BuildSavedPlanAssessmentReportOptions{
		Mode: AssertAdoptable,
		Request: AssessmentReportRequest{
			Tenant: reportStringPointer("tenant"), Policy: reportStringPointer("policy.json"),
		},
		Core: core,
	})
	assertReportFailureCode(t, err, "INVALID_ASSESSMENT_REPORT")

	cleanCore := reportCore(Clean)
	cleanCore.PolicySHA256 = nil
	cleanCore.StalePolicy = nil
	_, err = BuildSavedPlanAssessmentReport(BuildSavedPlanAssessmentReportOptions{
		Mode:    AssertAdoptable,
		Request: AssessmentReportRequest{Tenant: reportStringPointer("tenant")},
		Core:    cleanCore,
	})
	if err != nil {
		t.Fatalf("BuildSavedPlanAssessmentReport(clean import finding setup) error = %v, want nil", err)
	}

	_, err = BuildSavedPlanAssessmentErrorReport(BuildSavedPlanAssessmentErrorReportOptions{
		Mode: AssertAdoptable,
		Request: AssessmentReportRequest{
			Policy: reportStringPointer("policy.json"),
		},
		Partial: reportCore(Clean),
		Error:   AssessmentReportError{Kind: "invented", Message: "fixture"},
	})
	assertReportFailureCode(t, err, "INVALID_ASSESSMENT_REPORT")
}

func TestErrorReportRecomputesCountsAndDoesNotMutateCore(t *testing.T) {
	partial := reportCore(Blocked)
	report, err := BuildSavedPlanAssessmentErrorReport(BuildSavedPlanAssessmentErrorReportOptions{
		Mode: AssertAdoptable,
		Request: AssessmentReportRequest{
			Policy: reportStringPointer("policy.json"),
		},
		Partial: partial,
		Error: AssessmentReportError{
			Kind: AssessmentError, Message: "sanitized assessment failure",
		},
	})
	if err != nil {
		t.Fatalf("BuildSavedPlanAssessmentErrorReport() error = %v, want nil", err)
	}
	wantSummary := AssessmentReportSummary{
		Status: "error", Checked: 1, Clean: 0, Tolerated: 0, Blocked: 1,
	}
	if report.Summary != wantSummary {
		t.Errorf("BuildSavedPlanAssessmentErrorReport().Summary = %+v, want %+v", report.Summary, wantSummary)
	}
	if partial.Status != Blocked || partial.Blocked != 1 {
		t.Errorf("BuildSavedPlanAssessmentErrorReport() mutated partial = %+v, want blocked core unchanged", partial)
	}
}

func TestErrorReportExactBytes(t *testing.T) {
	report, err := BuildSavedPlanAssessmentErrorReport(BuildSavedPlanAssessmentErrorReportOptions{
		Mode: AssertAdoptable,
		Request: AssessmentReportRequest{
			Policy: reportStringPointer("policy.json"),
		},
		Partial: reportCore(Clean),
		Error: AssessmentReportError{
			Kind: AssessmentError, Message: "sanitized assessment failure",
		},
	})
	if err != nil {
		t.Fatalf("BuildSavedPlanAssessmentErrorReport(exact bytes) error = %v, want nil", err)
	}
	rendered, err := RenderAssessmentReport(report)
	if err != nil {
		t.Fatalf("RenderAssessmentReport(error report) error = %v, want nil", err)
	}
	want := `{
  "error": {
    "kind": "assessment_error",
    "message": "sanitized assessment failure"
  },
  "kind": "infrawright.saved_plan_assessment",
  "mode": "assert-adoptable",
  "request": {
    "policy": "policy.json",
    "policy_sha256": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
    "selectors": [],
    "tenant": null
  },
  "roots": [
    {
      "findings": [],
      "guidance": [],
      "label": "zpa_custom",
      "members": [
        "zpa_sample"
      ],
      "plan": {
        "format_version": "1.2",
        "sha256": "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
        "terraform_version": "1.15.4"
      },
      "plan_fingerprint": {
        "sha256": "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
        "version": 2
      },
      "status": "clean",
      "tenant": "tenant"
    }
  ],
  "schema_version": 1,
  "stale_policy": [
    {
      "mode": "plan_tolerate",
      "path": "unused",
      "resource_type": "zpa_sample"
    }
  ],
  "summary": {
    "blocked": 0,
    "checked": 1,
    "clean": 1,
    "status": "error",
    "tolerated": 0
  }
}
`
	if rendered != want {
		t.Errorf("RenderAssessmentReport(error report) bytes mismatch:\n got: %q\nwant: %q", rendered, want)
	}
}

func TestSchemaDiagnosticsRemainProcessFailureBytesNotReportBytes(t *testing.T) {
	core := reportCore(Clean)
	core.Roots[0].Tenant = "invalid tenant"
	_, err := BuildSavedPlanAssessmentReport(BuildSavedPlanAssessmentReportOptions{
		Mode: AssertAdoptable,
		Request: AssessmentReportRequest{
			Tenant: reportStringPointer("invalid tenant"), Policy: reportStringPointer("policy.json"),
		},
		Core: core,
	})
	var failure *procerr.ProcessFailure
	if !errors.As(err, &failure) {
		t.Fatalf("BuildSavedPlanAssessmentReport(invalid schema) error = %v, want ProcessFailure", err)
	}
	if failure.Code != "INVALID_ASSESSMENT_REPORT" || failure.Category != procerr.CategoryInternal ||
		failure.Message != "saved-plan assessment report is outside schema version 1" {
		t.Fatalf("BuildSavedPlanAssessmentReport(invalid schema) failure = %+v, want internal INVALID_ASSESSMENT_REPORT", failure)
	}
	wantDetails := []procerr.ErrorDetail{
		{Path: "/request/tenant", Code: "type", Message: "must be null"},
		{Path: "/request/tenant", Code: "pattern", Message: `must match pattern "^(?!\.{1,2}$)[A-Za-z0-9_.-]+$"`},
		{Path: "/request/tenant", Code: "oneOf", Message: "must match exactly one schema in oneOf"},
		{Path: "/roots/0/tenant", Code: "pattern", Message: `must match pattern "^(?!\.{1,2}$)[A-Za-z0-9_.-]+$"`},
	}
	if !reflect.DeepEqual(failure.Details, wantDetails) {
		t.Errorf("BuildSavedPlanAssessmentReport(invalid schema) details = %#v, want %#v", failure.Details, wantDetails)
	}
	cli := procerr.RenderCLIProcessFailure(failure)
	if !strings.Contains(cli, "  detail: /request/tenant [oneOf]") ||
		!strings.Contains(cli, "  detail: /roots/0/tenant [pattern]") {
		t.Errorf("RenderCLIProcessFailure(invalid report) = %q, want schema diagnostic detail bytes", cli)
	}
}

func TestStalePolicyOrderIsPreserved(t *testing.T) {
	core := reportCore(Clean)
	core.StalePolicy = []metadata.StalePolicyEntry{
		{ResourceType: "zpa_sample", Mode: metadata.PolicyPlanTolerate, Path: "z-last"},
		{ResourceType: "zpa_sample", Mode: metadata.PolicyPlanTolerate, Path: "a-first"},
	}
	report, err := BuildSavedPlanAssessmentReport(BuildSavedPlanAssessmentReportOptions{
		Mode: AssertAdoptable,
		Request: AssessmentReportRequest{
			Tenant: reportStringPointer("tenant"), Policy: reportStringPointer("policy.json"),
		},
		Core: core,
	})
	if err != nil {
		t.Fatalf("BuildSavedPlanAssessmentReport(stale order) error = %v, want nil", err)
	}
	if !reflect.DeepEqual(report.StalePolicy, core.StalePolicy) {
		t.Errorf("BuildSavedPlanAssessmentReport(stale order).StalePolicy = %#v, want %#v", report.StalePolicy, core.StalePolicy)
	}
}

func TestProjectionScopeMarkersNeverEnterReportBytes(t *testing.T) {
	entries := reportGuidance(Blocked, 1e-7)
	entries[0].Entries[0]["rule"] = "exponent-observation"
	report, err := BuildSavedPlanAssessmentReport(BuildSavedPlanAssessmentReportOptions{
		Mode: AssertAdoptable,
		Request: AssessmentReportRequest{
			Tenant: reportStringPointer("tenant"), Policy: reportStringPointer("policy.json"),
		},
		Core: reportCore(Blocked), Guidance: entries,
	})
	if err != nil {
		t.Fatalf("BuildSavedPlanAssessmentReport(exponent guidance) error = %v, want nil", err)
	}
	rendered, err := RenderAssessmentReport(report)
	if err != nil {
		t.Fatalf("RenderAssessmentReport(exponent guidance) error = %v, want nil", err)
	}
	if !strings.Contains(rendered, `"observed_value": 1e-07`) {
		t.Errorf("RenderAssessmentReport(exponent guidance) = %q, want Python exponent spelling", rendered)
	}
	for _, internalMarker := range []string{"projection_omit_if", "float:1e-07", "float:1e-7", "integer:"} {
		if strings.Contains(rendered, internalMarker) {
			t.Errorf("RenderAssessmentReport(exponent guidance) contains internal scope marker %q; bytes = %q", internalMarker, rendered)
		}
	}
}

func TestLargeGuidanceRemainsReportable(t *testing.T) {
	base := reportGuidance(Blocked, nil)[0].Entries[0]
	entries := make([]map[string]any, 10_001)
	for index := range entries {
		entry := cloneGuidanceRecord(base)
		entry["rule"] = fmt.Sprintf("rule-%05d", index)
		entry["observed_value"] = 0.5
		entries[index] = entry
	}
	report, err := BuildSavedPlanAssessmentReport(BuildSavedPlanAssessmentReportOptions{
		Mode: AssertAdoptable,
		Request: AssessmentReportRequest{
			Tenant: reportStringPointer("tenant"), Policy: reportStringPointer("policy.json"),
		},
		Core: reportCore(Blocked),
		Guidance: []AssessmentGuidanceGroup{{
			Tenant: "tenant", Label: "zpa_custom", Entries: entries,
		}},
	})
	if err != nil {
		t.Fatalf("BuildSavedPlanAssessmentReport(10,001 guidance entries) error = %v, want nil", err)
	}
	if got := len(report.Roots[0].Guidance); got != len(entries) {
		t.Errorf("BuildSavedPlanAssessmentReport(10,001 guidance entries) count = %d, want %d", got, len(entries))
	}
	rendered, err := RenderAssessmentReport(report)
	if err != nil {
		t.Fatalf("RenderAssessmentReport(10,001 guidance entries) error = %v, want nil", err)
	}
	if !strings.Contains(rendered, `"observed_value": 0.5`) {
		t.Errorf("RenderAssessmentReport(10,001 guidance entries) does not contain Python float spelling; bytes length = %d", len(rendered))
	}
}

func TestGuidanceDepthLimitFailsClosed(t *testing.T) {
	entry := reportGuidance(Blocked, nil)[0].Entries[0]
	var nested any = "leaf"
	for range 66 {
		nested = []any{nested}
	}
	entry["nested"] = nested
	_, err := BuildSavedPlanAssessmentReport(BuildSavedPlanAssessmentReportOptions{
		Mode: AssertAdoptable,
		Request: AssessmentReportRequest{
			Tenant: reportStringPointer("tenant"), Policy: reportStringPointer("policy.json"),
		},
		Core: reportCore(Blocked),
		Guidance: []AssessmentGuidanceGroup{{
			Tenant: "tenant", Label: "zpa_custom", Entries: []map[string]any{entry},
		}},
	})
	assertReportFailureCode(t, err, "INVALID_ASSESSMENT_GUIDANCE")
}

func assertReportFailureCode(t *testing.T, err error, code string) {
	t.Helper()
	var failure *procerr.ProcessFailure
	if !errors.As(err, &failure) || failure.Code != code {
		t.Fatalf("report operation error = %v, want ProcessFailure code %q", err, code)
	}
}
