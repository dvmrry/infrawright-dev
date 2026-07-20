package assessment

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/dvmrry/infrawright-dev/go/internal/deployment"
	"github.com/dvmrry/infrawright-dev/go/internal/metadata"
	"github.com/dvmrry/infrawright-dev/go/internal/plan"
	"github.com/dvmrry/infrawright-dev/go/internal/procerr"
	"github.com/dvmrry/infrawright-dev/go/internal/roots"
)

func runnerTestString(value string) *string {
	return &value
}

type runnerPanickingError struct{}

func (runnerPanickingError) Error() string {
	panic("runner test Error method panic")
}

type runnerNilAwareError struct{}

func (*runnerNilAwareError) Error() string {
	return "typed-nil error text must not escape"
}

func runnerTestFailure(code string, category procerr.Category, message string) *procerr.ProcessFailure {
	return procerr.NewProcessFailure(procerr.NewProcessFailureOptions{
		Code: code, Category: category, Message: message,
	})
}

func runnerTestReport(
	t *testing.T,
	mode AssessmentMode,
	status PlanStatus,
) SavedPlanAssessmentReport {
	t.Helper()
	counts := map[PlanStatus][3]int{
		Clean:     {1, 0, 0},
		Tolerated: {0, 1, 0},
		Blocked:   {0, 0, 1},
	}[status]
	findings := []AssessmentFinding{}
	if status != Clean {
		findings = append(findings, AssessmentFinding{
			Status:       status,
			Source:       "resource_changes",
			Address:      `sample_resource.this["one"]`,
			ResourceType: runnerTestString("sample_resource"),
			Actions:      []string{"update"},
			Paths:        []PlanPath{{"status"}},
		})
	}
	core := SavedPlanAssessmentCore{
		Status:    status,
		Checked:   1,
		Clean:     counts[0],
		Tolerated: counts[1],
		Blocked:   counts[2],
		Roots: []AssessedSavedPlanRoot{{
			Tenant:  "tenant",
			Label:   "sample_resource",
			Members: []string{"sample_resource"},
			Status:  status,
			Plan: AssessedPlanEvidence{
				SHA256:           strings.Repeat("a", 64),
				FormatVersion:    runnerTestString("1.2"),
				TerraformVersion: runnerTestString("1.15.4"),
			},
			PlanFingerprint: plan.PlanFingerprintV2{
				Version: 2, SHA256: strings.Repeat("b", 64),
			},
			Findings: findings,
		}},
		StalePolicy: []metadata.StalePolicyEntry{},
	}
	report, err := BuildSavedPlanAssessmentReport(BuildSavedPlanAssessmentReportOptions{
		Mode: mode,
		Request: AssessmentReportRequest{
			Tenant: runnerTestString("tenant"), Selectors: []string{},
		},
		Core: core,
	})
	if err != nil {
		t.Fatalf("BuildSavedPlanAssessmentReport(mode=%q, status=%q) error = %v, want nil", mode, status, err)
	}
	return report
}

func runnerTestErrorOutcome(
	t *testing.T,
	code string,
	kind AssessmentErrorKind,
	message string,
) SavedPlanAssessmentReportOutcome {
	t.Helper()
	partial := emptyRunnerAssessment(nil)
	failure := &SavedPlanAssessmentFailure{
		ProcessFailure: runnerTestFailure(code, procerr.CategoryDomain, message),
		ReportKind:     kind,
		Partial:        partial,
		Guidance:       []AssessmentGuidanceGroup{},
	}
	report, err := BuildSavedPlanAssessmentErrorReport(BuildSavedPlanAssessmentErrorReportOptions{
		Mode: AssertClean,
		Request: AssessmentReportRequest{
			Tenant: runnerTestString("tenant"), Selectors: []string{},
		},
		Partial: partial,
		Error:   AssessmentReportError{Kind: kind, Message: message},
	})
	if err != nil {
		t.Fatalf("BuildSavedPlanAssessmentErrorReport(code=%q) error = %v, want nil", code, err)
	}
	return SavedPlanAssessmentReportOutcome{Report: report, Failure: failure}
}

func runnerTestHooks(report SavedPlanAssessmentReport) runSavedPlanAssertionHooks {
	return runSavedPlanAssertionHooks{
		preflightPolicy: func(*string) (BoundDriftPolicy, error) {
			return BoundDriftPolicy{}, nil
		},
		resolveInputs: func(options ResolveLoadedSavedPlanAssessmentOptions) (ResolvedSavedPlanAssessment, error) {
			return ResolvedSavedPlanAssessment{Assessment: SavedPlanAssessmentOptions{
				TerraformExecutable: options.TerraformExecutable,
				Roots: []SavedPlanAssessmentRootInput{{
					Tenant: "tenant", Label: "sample_resource", Members: []string{"sample_resource"},
				}},
			}}, nil
		},
		assessReport: func(AssessSavedPlansReportOptions) (SavedPlanAssessmentReportOutcome, error) {
			return SavedPlanAssessmentReportOutcome{Report: report}, nil
		},
		writeReport: func(WriteAssessmentReportOptions) error { return nil },
		guidanceSource: func(metadata.LoadedPackRoot) AssessmentGuidanceSource {
			return AssessmentGuidanceSource{}
		},
	}
}

func runnerTestOptions(t *testing.T, mode AssessmentMode) RunSavedPlanAssertionOptions {
	t.Helper()
	return RunSavedPlanAssertionOptions{
		Workspace:           t.TempDir(),
		Mode:                mode,
		Tenant:              runnerTestString("tenant"),
		Selectors:           []string{},
		TerraformExecutable: filepath.Join(string(filepath.Separator), "terraform"),
		Inputs:              &SavedPlanAssertionInputs{},
	}
}

func requireRunnerProcessFailure(
	t *testing.T,
	err error,
	code string,
) *procerr.ProcessFailure {
	t.Helper()
	var failure *procerr.ProcessFailure
	if !errors.As(err, &failure) {
		t.Fatalf("RunSavedPlanAssertion() error = %T(%v), want *procerr.ProcessFailure code %q", err, err, code)
	}
	if failure.Code != code {
		t.Errorf("RunSavedPlanAssertion() failure.Code = %q, want %q", failure.Code, code)
	}
	return failure
}

func TestRunSavedPlanAssertionOrdersPolicyInputsTopologyTerraformAssessmentAndReport(t *testing.T) {
	events := []string{}
	diagnostics := []string{}
	selectors := []string{"sample_resource"}
	options := RunSavedPlanAssertionOptions{
		Workspace: t.TempDir(),
		Mode:      AssertClean,
		Tenant:    runnerTestString("tenant"),
		Selectors: selectors,
		LoadInputs: func() (SavedPlanAssertionInputs, error) {
			events = append(events, "inputs")
			selectors[0] = "mutated-after-capture"
			return SavedPlanAssertionInputs{}, nil
		},
		ResolveTerraformExecutable: func() (string, error) {
			events = append(events, "terraform")
			return filepath.Join(string(filepath.Separator), "resolved", "terraform"), nil
		},
		OnDiagnostic: func(message string) { diagnostics = append(diagnostics, message) },
	}
	hooks := runnerTestHooks(runnerTestReport(t, AssertClean, Clean))
	hooks.preflightPolicy = func(path *string) (BoundDriftPolicy, error) {
		events = append(events, "policy")
		if path != nil {
			t.Errorf("preflightPolicy(assert-clean) path = %q, want nil", *path)
		}
		return BoundDriftPolicy{}, nil
	}
	hooks.resolveInputs = func(got ResolveLoadedSavedPlanAssessmentOptions) (ResolvedSavedPlanAssessment, error) {
		events = append(events, "topology")
		wantSentinel := filepath.Join(options.Workspace, ".infrawright-unresolved-terraform")
		if got.TerraformExecutable != wantSentinel {
			t.Errorf("ResolveLoadedSavedPlanAssessment().TerraformExecutable = %q, want unresolved sentinel %q", got.TerraformExecutable, wantSentinel)
		}
		if !reflect.DeepEqual(got.Selectors, []string{"sample_resource"}) {
			t.Errorf("ResolveLoadedSavedPlanAssessment().Selectors = %#v, want captured selector", got.Selectors)
		}
		return ResolvedSavedPlanAssessment{
			Assessment: SavedPlanAssessmentOptions{
				TerraformExecutable: got.TerraformExecutable,
				Roots: []SavedPlanAssessmentRootInput{{
					Tenant: "tenant", Label: "sample_resource", Members: []string{"sample_resource"},
				}},
			},
			Diagnostics: []roots.WholeRootDiagnostic{{Message: "whole root selected"}},
		}, nil
	}
	hooks.assessReport = func(got AssessSavedPlansReportOptions) (SavedPlanAssessmentReportOutcome, error) {
		events = append(events, "assessment")
		wantTerraform := filepath.Join(string(filepath.Separator), "resolved", "terraform")
		if got.Assessment.Assessment.TerraformExecutable != wantTerraform {
			t.Errorf("AssessSavedPlansReport().TerraformExecutable = %q, want %q", got.Assessment.Assessment.TerraformExecutable, wantTerraform)
		}
		if !got.Assessment.HasExpectedPolicySHA256 || got.Assessment.ExpectedPolicySHA256 != nil {
			t.Errorf("AssessSavedPlansReport() expected policy = checked:%t sha:%v, want checked nil", got.Assessment.HasExpectedPolicySHA256, got.Assessment.ExpectedPolicySHA256)
		}
		return SavedPlanAssessmentReportOutcome{Report: runnerTestReport(t, AssertClean, Clean)}, nil
	}
	hooks.writeReport = func(WriteAssessmentReportOptions) error {
		events = append(events, "report")
		return nil
	}

	if err := runSavedPlanAssertion(options, hooks); err != nil {
		t.Fatalf("runSavedPlanAssertion(ordered success) error = %v, want nil", err)
	}
	wantEvents := []string{"policy", "inputs", "topology", "terraform", "assessment", "report"}
	if !reflect.DeepEqual(events, wantEvents) {
		t.Errorf("runSavedPlanAssertion() events = %#v, want %#v", events, wantEvents)
	}
	wantDiagnostics := []string{
		"NOTE: whole root selected",
		"all 1 saved plan(s) clean (no-op/imports only)",
	}
	if !reflect.DeepEqual(diagnostics, wantDiagnostics) {
		t.Errorf("runSavedPlanAssertion() diagnostics = %#v, want %#v", diagnostics, wantDiagnostics)
	}
}

func TestRunSavedPlanAssertionSnapshotsGuidanceBeforeCallbacks(t *testing.T) {
	t.Run("assert adoptable snapshots once before diagnostics and Terraform", func(t *testing.T) {
		rule := map[string]any{
			"id": "dynamic-rule", "reason": "before callbacks",
		}
		root := metadata.LoadedPackRoot{Packs: metadata.PackMetadata{
			Manifests: []metadata.PackManifest{{
				Name: "sample",
				Data: map[string]any{
					"dynamic_schema": map[string]any{"rules": []any{rule}},
				},
			}},
		}}
		events := []string{}
		guidanceCalls := 0
		options := runnerTestOptions(t, AssertAdoptable)
		options.TerraformExecutable = ""
		options.Inputs = &SavedPlanAssertionInputs{Root: root}
		options.OnDiagnostic = func(message string) {
			if strings.HasPrefix(message, "NOTE: ") {
				events = append(events, "diagnostic")
				rule["reason"] = "mutated by diagnostic"
				return
			}
			events = append(events, "success diagnostic")
		}
		options.ResolveTerraformExecutable = func() (string, error) {
			events = append(events, "Terraform resolver")
			rule["reason"] = "mutated by Terraform resolver"
			return filepath.Join(string(filepath.Separator), "resolved", "terraform"), nil
		}
		hooks := runnerTestHooks(runnerTestReport(t, AssertAdoptable, Clean))
		hooks.resolveInputs = func(got ResolveLoadedSavedPlanAssessmentOptions) (ResolvedSavedPlanAssessment, error) {
			events = append(events, "topology")
			return ResolvedSavedPlanAssessment{
				Assessment: SavedPlanAssessmentOptions{
					TerraformExecutable: got.TerraformExecutable,
					Roots: []SavedPlanAssessmentRootInput{{
						Tenant: "tenant", Label: "sample_resource", Members: []string{"sample_resource"},
					}},
				},
				Diagnostics: []roots.WholeRootDiagnostic{{Message: "whole root selected"}},
			}, nil
		}
		hooks.guidanceSource = func(got metadata.LoadedPackRoot) AssessmentGuidanceSource {
			guidanceCalls++
			events = append(events, "guidance snapshot")
			return NewAssessmentGuidanceSource(got)
		}
		hooks.assessReport = func(got AssessSavedPlansReportOptions) (SavedPlanAssessmentReportOutcome, error) {
			events = append(events, "assessment")
			if got.GuidanceSource == nil || len(got.GuidanceSource.manifests) != 1 ||
				len(got.GuidanceSource.manifests[0].dynamicSchema) != 1 {
				t.Fatalf("AssessSavedPlansReport().GuidanceSource = %+v, want one snapshotted dynamic rule", got.GuidanceSource)
			}
			snapshotReason := got.GuidanceSource.manifests[0].dynamicSchema[0]["reason"]
			if snapshotReason != "before callbacks" {
				t.Errorf("AssessSavedPlansReport().GuidanceSource reason = %#v, want pre-callback snapshot", snapshotReason)
			}
			return SavedPlanAssessmentReportOutcome{
				Report: runnerTestReport(t, AssertAdoptable, Clean),
			}, nil
		}
		hooks.writeReport = func(WriteAssessmentReportOptions) error {
			events = append(events, "report")
			return nil
		}

		if err := runSavedPlanAssertion(options, hooks); err != nil {
			t.Fatalf("runSavedPlanAssertion(guidance callback mutation) error = %v, want nil", err)
		}
		if guidanceCalls != 1 {
			t.Errorf("guidanceSource call count = %d, want exactly 1", guidanceCalls)
		}
		wantEvents := []string{
			"topology",
			"guidance snapshot",
			"diagnostic",
			"Terraform resolver",
			"assessment",
			"report",
			"success diagnostic",
		}
		if !reflect.DeepEqual(events, wantEvents) {
			t.Errorf("runSavedPlanAssertion(guidance callback mutation) events = %#v, want %#v", events, wantEvents)
		}
	})

	t.Run("assert clean never snapshots guidance", func(t *testing.T) {
		options := runnerTestOptions(t, AssertClean)
		options.TerraformExecutable = ""
		options.ResolveTerraformExecutable = func() (string, error) {
			return filepath.Join(string(filepath.Separator), "resolved", "terraform"), nil
		}
		hooks := runnerTestHooks(runnerTestReport(t, AssertClean, Clean))
		hooks.resolveInputs = func(got ResolveLoadedSavedPlanAssessmentOptions) (ResolvedSavedPlanAssessment, error) {
			return ResolvedSavedPlanAssessment{
				Assessment: SavedPlanAssessmentOptions{
					TerraformExecutable: got.TerraformExecutable,
					Roots: []SavedPlanAssessmentRootInput{{
						Tenant: "tenant", Label: "sample_resource", Members: []string{"sample_resource"},
					}},
				},
				Diagnostics: []roots.WholeRootDiagnostic{{Message: "whole root selected"}},
			}, nil
		}
		guidanceCalls := 0
		hooks.guidanceSource = func(metadata.LoadedPackRoot) AssessmentGuidanceSource {
			guidanceCalls++
			return AssessmentGuidanceSource{}
		}

		if err := runSavedPlanAssertion(options, hooks); err != nil {
			t.Fatalf("runSavedPlanAssertion(assert-clean guidance calls) error = %v, want nil", err)
		}
		if guidanceCalls != 0 {
			t.Errorf("guidanceSource(assert-clean) call count = %d, want 0", guidanceCalls)
		}
	})
}

func TestRunSavedPlanAssertionZeroRootsSkipsTerraformLookupAndReportsFailureFirst(t *testing.T) {
	options := runnerTestOptions(t, AssertClean)
	options.TerraformExecutable = ""
	terraformLookup := false
	options.ResolveTerraformExecutable = func() (string, error) {
		terraformLookup = true
		return "", errors.New("must not run")
	}
	events := []string{}
	hooks := runnerTestHooks(runnerTestReport(t, AssertClean, Clean))
	hooks.resolveInputs = func(got ResolveLoadedSavedPlanAssessmentOptions) (ResolvedSavedPlanAssessment, error) {
		return ResolvedSavedPlanAssessment{Assessment: SavedPlanAssessmentOptions{
			TerraformExecutable: got.TerraformExecutable,
			Roots:               []SavedPlanAssessmentRootInput{},
		}}, nil
	}
	outcome := runnerTestErrorOutcome(
		t,
		"NO_SAVED_PLANS",
		NoSavedPlans,
		"no saved plans to check - run make plan SAVE=1 first",
	)
	hooks.assessReport = func(got AssessSavedPlansReportOptions) (SavedPlanAssessmentReportOutcome, error) {
		events = append(events, "assessment")
		wantSentinel := filepath.Join(options.Workspace, ".infrawright-unresolved-terraform")
		if got.Assessment.Assessment.TerraformExecutable != wantSentinel {
			t.Errorf("AssessSavedPlansReport(zero roots).TerraformExecutable = %q, want %q", got.Assessment.Assessment.TerraformExecutable, wantSentinel)
		}
		return outcome, nil
	}
	hooks.writeReport = func(got WriteAssessmentReportOptions) error {
		events = append(events, "report")
		if got.Report.Error == nil || got.Report.Error.Kind != NoSavedPlans {
			t.Errorf("WriteAssessmentReport(zero roots).Report.Error = %+v, want no_saved_plans", got.Report.Error)
		}
		return nil
	}

	err := runSavedPlanAssertion(options, hooks)
	requireRunnerProcessFailure(t, err, "NO_SAVED_PLANS")
	var assessmentFailure *SavedPlanAssessmentFailure
	if !errors.As(err, &assessmentFailure) {
		t.Fatalf("runSavedPlanAssertion(zero roots) error = %T(%v), want explicit SavedPlanAssessmentFailure", err, err)
	}
	if assessmentFailure.Partial.Checked != 0 || len(assessmentFailure.Partial.Roots) != 0 {
		t.Errorf("runSavedPlanAssertion(zero roots) partial = %+v, want zero completed roots", assessmentFailure.Partial)
	}
	if terraformLookup {
		t.Error("ResolveTerraformExecutable(zero roots) was called, want skipped")
	}
	if !reflect.DeepEqual(events, []string{"assessment", "report"}) {
		t.Errorf("runSavedPlanAssertion(zero roots) events = %#v, want assessment then report before failure", events)
	}
}

func TestRunSavedPlanAssertionPolicyPreflightPrecedesLazyInputsAndRetainsDigest(t *testing.T) {
	workspace := t.TempDir()
	invalidPolicy := []byte(`{"version":1,"resource_types":`)
	policyPath := filepath.Join(workspace, "policy.json")
	if err := os.WriteFile(policyPath, invalidPolicy, 0o600); err != nil {
		t.Fatalf("os.WriteFile(%q) error = %v, want nil", policyPath, err)
	}
	reportPath := filepath.Join(workspace, "assessment.json")
	loaded := false
	resolved := false
	var written SavedPlanAssessmentReport
	options := RunSavedPlanAssertionOptions{
		Workspace:  workspace,
		Mode:       AssertAdoptable,
		Tenant:     runnerTestString("tenant"),
		Selectors:  []string{"missing"},
		PolicyPath: runnerTestString("policy.json"),
		ReportPath: &reportPath,
		LoadInputs: func() (SavedPlanAssertionInputs, error) {
			loaded = true
			return SavedPlanAssertionInputs{}, errors.New("must not load")
		},
		ResolveTerraformExecutable: func() (string, error) {
			return "", errors.New("must not resolve Terraform")
		},
	}
	hooks := runnerTestHooks(runnerTestReport(t, AssertAdoptable, Clean))
	hooks.preflightPolicy = PreflightSavedPlanAssessmentPolicy
	hooks.resolveInputs = func(ResolveLoadedSavedPlanAssessmentOptions) (ResolvedSavedPlanAssessment, error) {
		resolved = true
		return ResolvedSavedPlanAssessment{}, errors.New("must not resolve inputs")
	}
	hooks.writeReport = func(got WriteAssessmentReportOptions) error {
		written = got.Report
		return nil
	}

	err := runSavedPlanAssertion(options, hooks)
	failure := requireRunnerProcessFailure(t, err, "INVALID_DRIFT_POLICY")
	if loaded || resolved {
		t.Errorf("policy preflight ordering loaded=%t resolved=%t, want both false", loaded, resolved)
	}
	if failure.Category != procerr.CategoryDomain {
		t.Errorf("policy failure category = %q, want %q", failure.Category, procerr.CategoryDomain)
	}
	if written.Error == nil || written.Error.Kind != PolicyError {
		t.Fatalf("policy error report = %+v, want policy_error", written.Error)
	}
	wantDigest := sha256.Sum256(invalidPolicy)
	wantSHA := hex.EncodeToString(wantDigest[:])
	if written.Request.PolicySHA256 == nil || *written.Request.PolicySHA256 != wantSHA {
		t.Errorf("policy error report SHA = %v, want %q", written.Request.PolicySHA256, wantSHA)
	}
	if written.Request.Policy == nil || *written.Request.Policy != "policy.json" {
		t.Errorf("policy error report request policy = %v, want original relative spelling", written.Request.Policy)
	}
}

func TestRunSavedPlanAssertionLazyFailureRetainsSuccessfulPolicyEvidence(t *testing.T) {
	workspace := t.TempDir()
	policyBytes := []byte(`{"version":1,"resource_types":{}}`)
	policyPath := filepath.Join(workspace, "policy.json")
	if err := os.WriteFile(policyPath, policyBytes, 0o600); err != nil {
		t.Fatalf("os.WriteFile(%q) error = %v, want nil", policyPath, err)
	}
	reportPath := filepath.Join(workspace, "assessment.json")
	original := runnerTestFailure("INVALID_DEPLOYMENT", procerr.CategoryDomain, "deployment is not valid JSON")
	options := RunSavedPlanAssertionOptions{
		Workspace:  workspace,
		Mode:       AssertAdoptable,
		Tenant:     runnerTestString("tenant"),
		Selectors:  []string{},
		PolicyPath: runnerTestString("policy.json"),
		ReportPath: &reportPath,
		LoadInputs: func() (SavedPlanAssertionInputs, error) {
			return SavedPlanAssertionInputs{}, original
		},
	}
	hooks := runnerTestHooks(runnerTestReport(t, AssertAdoptable, Clean))
	hooks.preflightPolicy = PreflightSavedPlanAssessmentPolicy
	var written SavedPlanAssessmentReport
	hooks.writeReport = func(got WriteAssessmentReportOptions) error {
		written = got.Report
		return nil
	}

	requireRunnerProcessFailure(t, runSavedPlanAssertion(options, hooks), "INVALID_DEPLOYMENT")
	wantDigest := sha256.Sum256(policyBytes)
	wantSHA := hex.EncodeToString(wantDigest[:])
	if written.Request.Policy == nil || *written.Request.Policy != "policy.json" ||
		written.Request.PolicySHA256 == nil || *written.Request.PolicySHA256 != wantSHA {
		t.Errorf("lazy failure policy evidence = path:%v sha:%v, want original path and SHA %q", written.Request.Policy, written.Request.PolicySHA256, wantSHA)
	}
	if written.Error == nil || written.Error.Kind != AssessmentError ||
		written.Error.Message != original.Message {
		t.Errorf("lazy failure report error = %+v, want assessment_error preserving original", written.Error)
	}
}

func TestRunSavedPlanAssertionLazyInputFailureWritesErrorAndPreservesOriginal(t *testing.T) {
	workspace := t.TempDir()
	reportPath := filepath.Join(workspace, "report-é.json")
	diagnostics := []string{}
	original := runnerTestFailure("INVALID_DEPLOYMENT", procerr.CategoryDomain, "deployment is not valid JSON")
	options := RunSavedPlanAssertionOptions{
		Workspace:  workspace,
		Mode:       AssertClean,
		Tenant:     runnerTestString("tenant"),
		Selectors:  []string{},
		ReportPath: &reportPath,
		LoadInputs: func() (SavedPlanAssertionInputs, error) {
			return SavedPlanAssertionInputs{}, original
		},
		OnDiagnostic: func(message string) { diagnostics = append(diagnostics, message) },
	}
	hooks := runnerTestHooks(runnerTestReport(t, AssertClean, Clean))
	var written SavedPlanAssessmentReport
	hooks.writeReport = func(got WriteAssessmentReportOptions) error {
		written = got.Report
		return assessmentReportWriteFailure()
	}

	err := runSavedPlanAssertion(options, hooks)
	failure := requireRunnerProcessFailure(t, err, "INVALID_DEPLOYMENT")
	if failure.Message != original.Message {
		t.Errorf("lazy failure message = %q, want original %q", failure.Message, original.Message)
	}
	if written.Error == nil || written.Error.Kind != AssessmentError ||
		written.Error.Message != original.Message {
		t.Errorf("lazy error report error = %+v, want assessment_error preserving original", written.Error)
	}
	encodedPath, encodeErr := runnerDiagnosticJSON(reportPath)
	if encodeErr != nil {
		t.Fatalf("runnerDiagnosticJSON(%q) error = %v, want nil", reportPath, encodeErr)
	}
	wantWarning := "WARNING: could not write assessment error report " + encodedPath +
		": unable to write saved-plan assessment report; preserving original assessment error"
	if !reflect.DeepEqual(diagnostics, []string{wantWarning}) {
		t.Errorf("runSavedPlanAssertion(lazy write failure) diagnostics = %#v, want %#v", diagnostics, []string{wantWarning})
	}
}

func TestRunSavedPlanAssertionSafeFailureRejectsTypedNilAndPanickingErrors(t *testing.T) {
	errorCases := []struct {
		name string
		make func() error
	}{
		{
			name: "typed nil ProcessFailure",
			make: func() error {
				var failure *procerr.ProcessFailure
				return failure
			},
		},
		{
			name: "typed nil MetadataError",
			make: func() error {
				var failure *metadata.MetadataError
				return failure
			},
		},
		{
			name: "typed nil custom error",
			make: func() error {
				var failure *runnerNilAwareError
				return failure
			},
		},
		{
			name: "panicking Error method",
			make: func() error {
				return runnerPanickingError{}
			},
		},
	}
	for _, stage := range []string{"LoadInputs", "Terraform resolver"} {
		for _, errorCase := range errorCases {
			t.Run(stage+"/"+errorCase.name, func(t *testing.T) {
				options := runnerTestOptions(t, AssertClean)
				reportPath := filepath.Join(options.Workspace, "assessment-error.json")
				options.ReportPath = &reportPath
				diagnostics := []string{}
				options.OnDiagnostic = func(message string) {
					diagnostics = append(diagnostics, message)
				}
				if stage == "LoadInputs" {
					options.Inputs = nil
					options.LoadInputs = func() (SavedPlanAssertionInputs, error) {
						return SavedPlanAssertionInputs{}, errorCase.make()
					}
				} else {
					options.TerraformExecutable = ""
					options.ResolveTerraformExecutable = func() (string, error) {
						return "", errorCase.make()
					}
				}

				hooks := runnerTestHooks(runnerTestReport(t, AssertClean, Clean))
				assessmentCalls := 0
				hooks.assessReport = func(AssessSavedPlansReportOptions) (SavedPlanAssessmentReportOutcome, error) {
					assessmentCalls++
					return SavedPlanAssessmentReportOutcome{}, errors.New("assessment must not run")
				}
				writeCalls := 0
				var written SavedPlanAssessmentReport
				hooks.writeReport = func(got WriteAssessmentReportOptions) error {
					writeCalls++
					written = got.Report
					return nil
				}

				err := runSavedPlanAssertion(options, hooks)
				failure := requireRunnerProcessFailure(t, err, "ASSESSMENT_FAILED")
				if failure.Category != procerr.CategoryInternal ||
					failure.Message != "saved-plan assessment failed" ||
					failure.Retryable || len(failure.Details) != 0 {
					t.Errorf("runSavedPlanAssertion(%s %s) failure = %+v, want fixed generic internal failure", stage, errorCase.name, failure)
				}
				if writeCalls != 1 || written.Error == nil ||
					written.Error.Kind != AssessmentError ||
					written.Error.Message != "saved-plan assessment failed" {
					t.Errorf("runSavedPlanAssertion(%s %s) report writes = %d error=%+v, want one generic assessment error report", stage, errorCase.name, writeCalls, written.Error)
				}
				if assessmentCalls != 0 {
					t.Errorf("runSavedPlanAssertion(%s %s) assessment calls = %d, want 0", stage, errorCase.name, assessmentCalls)
				}
				if len(diagnostics) != 0 {
					t.Errorf("runSavedPlanAssertion(%s %s) diagnostics = %#v, want no success diagnostic", stage, errorCase.name, diagnostics)
				}
			})
		}
	}
}

func TestRunnerSafeFailurePreservesNonNilProcessFailureFields(t *testing.T) {
	original := procerr.NewProcessFailure(procerr.NewProcessFailureOptions{
		Code:      "ORIGINAL_FAILURE",
		Category:  procerr.CategoryIO,
		Message:   "original failure text",
		Retryable: true,
		Details: []procerr.ErrorDetail{{
			Path: "/input", Code: "original_detail", Message: "original detail text",
		}},
	})
	mapped := runnerSafeFailure(original)
	if mapped == original {
		t.Error("runnerSafeFailure(non-nil ProcessFailure) returned original pointer, want detached copy")
	}
	if mapped.Code != original.Code || mapped.Category != original.Category ||
		mapped.Message != original.Message || mapped.Retryable != original.Retryable ||
		!reflect.DeepEqual(mapped.Details, original.Details) {
		t.Errorf("runnerSafeFailure(non-nil ProcessFailure) = %+v, want preserved fields %+v", mapped, original)
	}
	if len(mapped.Details) != 1 {
		t.Fatalf("runnerSafeFailure(non-nil ProcessFailure).Details length = %d, want 1", len(mapped.Details))
	}
	mapped.Details[0].Message = "mutated copy"
	if original.Details[0].Message != "original detail text" {
		t.Errorf("runnerSafeFailure(non-nil ProcessFailure) mutated original details = %+v, want detached", original.Details)
	}
}

func TestRunSavedPlanAssertionMapsMetadataErrorsAndRecordsInvalidTenantInvocation(t *testing.T) {
	_, metadataErr := metadata.NewDriftPolicy(map[string]any{
		"version": json.Number("2"),
	}, "<runner-test>")
	if metadataErr == nil {
		t.Fatal("metadata.NewDriftPolicy(version=2) error = nil, want MetadataError fixture")
	}

	t.Run("metadata error", func(t *testing.T) {
		options := runnerTestOptions(t, AssertClean)
		options.Inputs = nil
		options.LoadInputs = func() (SavedPlanAssertionInputs, error) {
			return SavedPlanAssertionInputs{}, metadataErr
		}
		hooks := runnerTestHooks(runnerTestReport(t, AssertClean, Clean))
		failure := requireRunnerProcessFailure(t, runSavedPlanAssertion(options, hooks), "INVALID_ASSESSMENT_INPUT")
		if failure.Category != procerr.CategoryRequest || failure.Message != metadataErr.Error() {
			t.Errorf("metadata failure = %+v, want request failure with message %q", failure, metadataErr.Error())
		}
	})

	t.Run("invalid tenant error record", func(t *testing.T) {
		options := runnerTestOptions(t, AssertClean)
		options.Tenant = runnerTestString("")
		reportPath := filepath.Join(options.Workspace, "invalid-tenant.json")
		options.ReportPath = &reportPath
		hooks := runnerTestHooks(runnerTestReport(t, AssertClean, Clean))
		hooks.resolveInputs = func(ResolveLoadedSavedPlanAssessmentOptions) (ResolvedSavedPlanAssessment, error) {
			return ResolvedSavedPlanAssessment{}, errors.New("topology failed")
		}
		var written SavedPlanAssessmentReport
		hooks.writeReport = func(got WriteAssessmentReportOptions) error {
			written = got.Report
			return nil
		}
		failure := requireRunnerProcessFailure(t, runSavedPlanAssertion(options, hooks), "ASSESSMENT_FAILED")
		if failure.Message != "topology failed" {
			t.Errorf("invalid tenant original failure message = %q, want %q", failure.Message, "topology failed")
		}
		if written.Request.Tenant == nil || *written.Request.Tenant != "" ||
			written.Summary.Status != "error" || written.Error == nil {
			t.Errorf("invalid tenant error report = %+v, want raw empty tenant error record", written)
		}
	})
}

func TestRunSavedPlanAssertionWritesExactReportToFileAndStdout(t *testing.T) {
	for _, destination := range []string{"file", "stdout"} {
		t.Run(destination, func(t *testing.T) {
			report := runnerTestReport(t, AssertClean, Clean)
			want, err := RenderAssessmentReport(report)
			if err != nil {
				t.Fatalf("RenderAssessmentReport(clean) error = %v, want nil", err)
			}
			options := runnerTestOptions(t, AssertClean)
			diagnostics := []string{}
			options.OnDiagnostic = func(message string) { diagnostics = append(diagnostics, message) }
			var stdout strings.Builder
			if destination == "file" {
				path := filepath.Join(options.Workspace, "reports", "assessment.json")
				options.ReportPath = &path
			} else {
				options.ReportPath = runnerTestString("-")
				options.Stdout = func(text string) error {
					stdout.WriteString(text)
					return nil
				}
			}
			hooks := runnerTestHooks(report)
			hooks.writeReport = WriteAssessmentReport
			if err := runSavedPlanAssertion(options, hooks); err != nil {
				t.Fatalf("runSavedPlanAssertion(report %s) error = %v, want nil", destination, err)
			}
			var got string
			if destination == "file" {
				data, err := os.ReadFile(*options.ReportPath)
				if err != nil {
					t.Fatalf("os.ReadFile(%q) error = %v, want nil", *options.ReportPath, err)
				}
				got = string(data)
			} else {
				got = stdout.String()
			}
			if got != want {
				t.Errorf("runSavedPlanAssertion(report %s) bytes = %q, want %q", destination, got, want)
			}
			if !reflect.DeepEqual(diagnostics, []string{"all 1 saved plan(s) clean (no-op/imports only)"}) {
				t.Errorf("runSavedPlanAssertion(report %s) diagnostics = %#v, want exact success line", destination, diagnostics)
			}
		})
	}
}

func TestRunSavedPlanAssertionProductionCleanVerticalSlice(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("saved-plan snapshot cleanup is deliberately fail-closed on this platform")
	}
	workspace := t.TempDir()
	resourceType := "zpa_application_segment"
	envDir := filepath.Join(workspace, "envs", "tenant", resourceType)
	moduleDir := filepath.Join(workspace, "modules", resourceType)
	for _, directory := range []string{envDir, moduleDir} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatalf("os.MkdirAll(%q) error = %v, want nil", directory, err)
		}
	}
	relativeModule, err := filepath.Rel(envDir, moduleDir)
	if err != nil {
		t.Fatalf("filepath.Rel(%q, %q) error = %v, want nil", envDir, moduleDir, err)
	}
	writeAssessmentTransactionFile(t, filepath.Join(moduleDir, "main.tf"), []byte("# module\n"), 0o600)
	writeAssessmentTransactionFile(t, filepath.Join(envDir, "main.tf"), []byte(strings.Join([]string{
		`module "` + resourceType + `" {`,
		`  source = "` + filepath.ToSlash(relativeModule) + `"`,
		"  items = var." + resourceType + "_items",
		"}",
		"",
	}, "\n")), 0o600)
	writeAssessmentTransactionFile(t, filepath.Join(envDir, "tfplan"), []byte("opaque saved plan\n"), 0o600)
	varFile := filepath.Join(
		workspace,
		"config",
		"tenant",
		resourceType+".auto.tfvars.json",
	)
	if err := os.MkdirAll(filepath.Dir(varFile), 0o755); err != nil {
		t.Fatalf("os.MkdirAll(%q) error = %v, want nil", filepath.Dir(varFile), err)
	}
	writeAssessmentTransactionFile(t, varFile, []byte("{}\n"), 0o600)
	fingerprint, err := plan.FingerprintPlanV2(plan.PlanFingerprintInput{
		EnvDir:      envDir,
		VarFiles:    []string{varFile},
		MemberTypes: []string{resourceType},
	}, nil)
	if err != nil {
		t.Fatalf("plan.FingerprintPlanV2(runner vertical slice) error = %v, want nil", err)
	}
	writeAssessmentTransactionFile(
		t,
		filepath.Join(envDir, "tfplan.sources"),
		[]byte(`{"version":2,"sha256":"`+fingerprint.SHA256+`"}`+"\n"),
		0o600,
	)
	executable := assessmentExecutable(
		t,
		workspace,
		"printf '%s' "+assessmentShellLiteral(strings.ReplaceAll(
			cleanAssessmentPlanJSON(t),
			"zpa_sample",
			resourceType,
		)),
	)
	reportPath := "-"
	diagnostics := []string{}
	var stdout strings.Builder
	err = RunSavedPlanAssertion(RunSavedPlanAssertionOptions{
		Workspace: workspace,
		Mode:      AssertClean,
		Tenant:    runnerTestString("tenant"),
		Selectors: []string{resourceType},
		Inputs: &SavedPlanAssertionInputs{
			Deployment: deployment.Deployment{Overlay: ".", Roots: map[string]deployment.RootProviderConfig{}},
			Root:       loadedAssessmentPack(t),
		},
		TerraformExecutable: executable,
		ReportPath:          &reportPath,
		OnDiagnostic: func(message string) {
			diagnostics = append(diagnostics, message)
		},
		Stdout: func(text string) error {
			stdout.WriteString(text)
			return nil
		},
	})
	if err != nil {
		t.Fatalf("RunSavedPlanAssertion(production clean vertical slice) error = %v, want nil", err)
	}
	if !reflect.DeepEqual(diagnostics, []string{
		"all 1 saved plan(s) clean (no-op/imports only)",
	}) {
		t.Errorf("RunSavedPlanAssertion(production clean vertical slice) diagnostics = %#v, want exact clean line", diagnostics)
	}
	var reportValue map[string]any
	if err := json.Unmarshal([]byte(stdout.String()), &reportValue); err != nil {
		t.Fatalf("json.Unmarshal(production runner stdout) error = %v, want valid report bytes", err)
	}
	summary, _ := reportValue["summary"].(map[string]any)
	if reportValue["mode"] != "assert-clean" || summary["status"] != "clean" ||
		summary["checked"] != float64(1) {
		t.Errorf("RunSavedPlanAssertion(production clean vertical slice) report = %#v, want checked clean assert-clean", reportValue)
	}
}

func TestRunSavedPlanAssertionSuccessReportWriteFailureStopsBeforeSuccessDiagnostic(t *testing.T) {
	report := runnerTestReport(t, AssertClean, Clean)
	options := runnerTestOptions(t, AssertClean)
	diagnostics := []string{}
	options.OnDiagnostic = func(message string) { diagnostics = append(diagnostics, message) }
	hooks := runnerTestHooks(report)
	hooks.writeReport = func(WriteAssessmentReportOptions) error {
		return assessmentReportWriteFailure()
	}

	failure := requireRunnerProcessFailure(
		t,
		runSavedPlanAssertion(options, hooks),
		"ASSESSMENT_REPORT_WRITE_FAILED",
	)
	if failure.Category != procerr.CategoryIO {
		t.Errorf("success report write failure category = %q, want %q", failure.Category, procerr.CategoryIO)
	}
	if len(diagnostics) != 0 {
		t.Errorf("success report write failure diagnostics = %#v, want no success diagnostic", diagnostics)
	}
}

func TestRunSavedPlanAssertionBlockedClassificationsWriteBeforeReturning(t *testing.T) {
	tests := []struct {
		name        string
		mode        AssessmentMode
		status      PlanStatus
		wantCode    string
		wantMessage string
	}{
		{
			name: "assert clean", mode: AssertClean, status: Blocked,
			wantCode: "PLAN_NOT_CLEAN",
			wantMessage: "tenant moved since fetch (or transform disagrees) - " +
				"do not auto-merge",
		},
		{
			name: "assert adoptable", mode: AssertAdoptable, status: Blocked,
			wantCode:    "PLAN_NOT_ADOPTABLE",
			wantMessage: "1 saved plan(s) blocked by untolerated changes",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			report := runnerTestReport(t, test.mode, test.status)
			options := runnerTestOptions(t, test.mode)
			events := []string{}
			hooks := runnerTestHooks(report)
			hooks.writeReport = func(WriteAssessmentReportOptions) error {
				events = append(events, "report")
				return nil
			}
			failure := requireRunnerProcessFailure(t, runSavedPlanAssertion(options, hooks), test.wantCode)
			events = append(events, "failure")
			if failure.Message != test.wantMessage {
				t.Errorf("runSavedPlanAssertion(%s) failure.Message = %q, want %q", test.name, failure.Message, test.wantMessage)
			}
			if !reflect.DeepEqual(events, []string{"report", "failure"}) {
				t.Errorf("runSavedPlanAssertion(%s) events = %#v, want report before failure", test.name, events)
			}
		})
	}
}

func TestRunSavedPlanAssertionAssertAdoptableSuccessDiagnostics(t *testing.T) {
	tests := []struct {
		name            string
		status          PlanStatus
		wantDiagnostics []string
	}{
		{
			name:   "clean",
			status: Clean,
			wantDiagnostics: []string{
				"all 1 saved plan(s) clean",
			},
		},
		{
			name:   "tolerated",
			status: Tolerated,
			wantDiagnostics: []string{
				"TOLERATED: tenant/sample_resource",
				`  sample_resource.this["one"] update clean_with_tolerated_drift`,
				"    - status",
				"1 saved plan(s) adoptable with consumer-tolerated drift",
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			report := runnerTestReport(t, AssertAdoptable, test.status)
			options := runnerTestOptions(t, AssertAdoptable)
			diagnostics := []string{}
			resolverCalled := false
			options.ResolveTerraformExecutable = func() (string, error) {
				resolverCalled = true
				return "", errors.New("explicit executable should win")
			}
			options.OnDiagnostic = func(message string) {
				diagnostics = append(diagnostics, message)
			}
			hooks := runnerTestHooks(report)
			hooks.resolveInputs = func(got ResolveLoadedSavedPlanAssessmentOptions) (ResolvedSavedPlanAssessment, error) {
				if got.TerraformExecutable != options.TerraformExecutable {
					t.Errorf("ResolveLoadedSavedPlanAssessment(explicit Terraform).TerraformExecutable = %q, want %q", got.TerraformExecutable, options.TerraformExecutable)
				}
				return ResolvedSavedPlanAssessment{Assessment: SavedPlanAssessmentOptions{
					TerraformExecutable: got.TerraformExecutable,
					Roots: []SavedPlanAssessmentRootInput{{
						Tenant: "tenant", Label: "sample_resource", Members: []string{"sample_resource"},
					}},
				}}, nil
			}
			if err := runSavedPlanAssertion(options, hooks); err != nil {
				t.Fatalf("runSavedPlanAssertion(assert-adoptable %s) error = %v, want nil", test.name, err)
			}
			if resolverCalled {
				t.Error("ResolveTerraformExecutable(explicit Terraform) was called, want skipped")
			}
			if !reflect.DeepEqual(diagnostics, test.wantDiagnostics) {
				t.Errorf("runSavedPlanAssertion(assert-adoptable %s) diagnostics = %#v, want %#v", test.name, diagnostics, test.wantDiagnostics)
			}
		})
	}
}

func TestEmitRunnerAssessmentExactDiagnostics(t *testing.T) {
	report := SavedPlanAssessmentReport{Mode: AssertAdoptable}
	report.Roots = []AssessmentReportRoot{
		{
			Tenant: "tenant", Label: "blocked_root", Status: Blocked,
			Findings: []NormalizedAssessmentFinding{{
				Status: Blocked, Address: `sample_resource.this["one"]`,
				Actions: []string{"update", "delete"}, Paths: []string{"status", "rules[0]"},
			}},
			Guidance: []map[string]any{
				{
					"lane": "dynamic_schema", "rule": "dynamic", "provider": "sample",
					"resource_type": "sample_resource", "kind": "freeform_object",
					"ownership": "unknown", "action": "manual_review_required",
					"provider_version_constraint": "1.0.0", "matched_plan_path": "body",
					"reason": "review", "evidence": "dynamic.md", "status_effect": "blocked",
				},
				{
					"lane": "absent_default", "rule": "absent", "provider": "sample",
					"resource_type": "sample_resource", "kind": "api_absent",
					"action": "diagnostic_only", "observed_value": nil,
					"matched_plan_path": "optional", "reason": "absent", "status_effect": "blocked",
				},
				{
					"lane": "provider_config", "provider": "sample", "setting": "tenant",
					"expected_value": map[string]any{"z": "é", "a": json.Number("1.0")},
					"mode":           "required_external", "matched_plan_path": "provider",
					"reason": "configure", "status_effect": "blocked",
				},
			},
		},
		{
			Tenant: "tenant", Label: "tolerated_root", Status: Tolerated,
			Findings: []NormalizedAssessmentFinding{{
				Status: Tolerated, Address: "", Actions: []string{"update"}, Paths: []string{"name"},
			}},
		},
	}
	report.StalePolicy = []metadata.StalePolicyEntry{{
		ResourceType: "sample_resource", Mode: metadata.PolicyPlanTolerate, Path: "unused",
	}}
	diagnostics := []string{}
	if err := emitRunnerAssessment(report, func(message string) {
		diagnostics = append(diagnostics, message)
	}); err != nil {
		t.Fatalf("emitRunnerAssessment(exact fixture) error = %v, want nil", err)
	}
	want := []string{
		"BLOCKED: tenant/blocked_root",
		`  sample_resource.this["one"] update,delete blocked`,
		"    - status",
		"    - rules[0]",
		"  Provider configuration guidance:",
		"    - provider: sample",
		"      setting: tenant",
		`      expected value: {"a": 1.0, "z": "\u00e9"}`,
		"      mode: required_external",
		"      matched plan path: provider",
		"      reason: configure",
		"      status: blocked",
		"  Absent/default guidance:",
		"    - rule: absent",
		"      provider: sample",
		"      resource type: sample_resource",
		"      kind: api_absent",
		"      action: diagnostic_only",
		"      observed value: null",
		"      matched plan path: optional",
		"      reason: absent",
		"      status: blocked",
		"  Dynamic-schema guidance:",
		"    - rule: dynamic",
		"      provider: sample",
		"      resource type: sample_resource",
		"      kind: freeform_object",
		"      ownership: unknown",
		"      action: manual_review_required",
		"      provider version constraint: 1.0.0",
		"      matched plan path: body",
		"      reason: review",
		"      evidence: dynamic.md",
		"      status: blocked",
		"TOLERATED: tenant/tolerated_root",
		"  None update clean_with_tolerated_drift",
		"    - name",
		"STALE DRIFT POLICY: sample_resource plan_tolerate unused matched no path",
	}
	if !reflect.DeepEqual(diagnostics, want) {
		t.Errorf("emitRunnerAssessment(exact fixture) diagnostics mismatch:\n got: %#v\nwant: %#v", diagnostics, want)
	}

	cleanReport := report
	cleanReport.Mode = AssertClean
	cleanReport.Roots = cleanReport.Roots[:1]
	diagnostics = nil
	if err := emitRunnerAssessment(cleanReport, func(message string) {
		diagnostics = append(diagnostics, message)
	}); err != nil {
		t.Fatalf("emitRunnerAssessment(assert-clean) error = %v, want nil", err)
	}
	if !reflect.DeepEqual(diagnostics, []string{
		"NOT CLEAN: tenant/blocked_root plan contains 1 change(s) beyond imports",
	}) {
		t.Errorf("emitRunnerAssessment(assert-clean) diagnostics = %#v, want exact NOT CLEAN line", diagnostics)
	}
}
