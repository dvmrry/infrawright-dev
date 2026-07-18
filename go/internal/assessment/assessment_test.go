package assessment

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/dvmrry/infrawright-dev/go/internal/artifacts"
	"github.com/dvmrry/infrawright-dev/go/internal/controlevidence"
	"github.com/dvmrry/infrawright-dev/go/internal/plan"
	"github.com/dvmrry/infrawright-dev/go/internal/procerr"
	"github.com/dvmrry/infrawright-dev/go/internal/terraformcmd"
)

type assessmentTransactionFixture struct {
	root            string
	envDir          string
	planPath        string
	fingerprintPath string
	rootInput       SavedPlanAssessmentRootInput
}

func newAssessmentTransactionFixture(t *testing.T) assessmentTransactionFixture {
	t.Helper()
	root := t.TempDir()
	envDir := filepath.Join(root, "envs", "tenant", "zpa_sample")
	moduleDir := filepath.Join(root, "modules", "zpa_sample")
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
		`module "zpa_sample" {`,
		fmt.Sprintf(`  source = %q`, filepath.ToSlash(relativeModule)),
		"  items = var.zpa_sample_items",
		"}",
		"",
	}, "\n")), 0o600)
	planPath := filepath.Join(envDir, "tfplan")
	fingerprintPath := filepath.Join(envDir, "tfplan.sources")
	writeAssessmentTransactionFile(t, planPath, []byte("opaque saved plan secret\n"), 0o600)
	input := plan.PlanFingerprintInput{
		EnvDir:      envDir,
		VarFiles:    []string{},
		MemberTypes: []string{"zpa_sample"},
	}
	fingerprint, err := plan.FingerprintPlanV2(input, nil)
	if err != nil {
		t.Fatalf("plan.FingerprintPlanV2(%+v, nil) error = %v, want nil", input, err)
	}
	writeAssessmentTransactionFile(
		t,
		fingerprintPath,
		[]byte(fmt.Sprintf("{\"version\":2,\"sha256\":%q}\n", fingerprint.SHA256)),
		0o600,
	)
	return assessmentTransactionFixture{
		root:            root,
		envDir:          envDir,
		planPath:        planPath,
		fingerprintPath: fingerprintPath,
		rootInput: SavedPlanAssessmentRootInput{
			Tenant:          "tenant",
			Label:           "zpa_sample",
			Members:         []string{"zpa_sample"},
			EnvDir:          envDir,
			SavedPlanPath:   planPath,
			FingerprintPath: fingerprintPath,
			VarFiles:        []string{},
		},
	}
}

func writeAssessmentTransactionFile(
	t *testing.T,
	filePath string,
	content []byte,
	mode os.FileMode,
) {
	t.Helper()
	if err := os.WriteFile(filePath, content, mode); err != nil {
		t.Fatalf("os.WriteFile(%q, %d bytes, %#o) error = %v, want nil", filePath, len(content), mode, err)
	}
}

func assessmentShellLiteral(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
}

func assessmentExecutable(t *testing.T, root, body string) string {
	t.Helper()
	executable := filepath.Join(root, "terraform-fake")
	writeAssessmentTransactionFile(t, executable, []byte("#!/bin/sh\n"+body+"\n"), 0o700)
	if err := os.Chmod(executable, 0o700); err != nil {
		t.Fatalf("os.Chmod(%q, 0700) error = %v, want nil", executable, err)
	}
	return executable
}

func assessmentPlanJSON(t *testing.T, complete bool, change map[string]any) string {
	t.Helper()
	value := map[string]any{
		"format_version":    "1.2",
		"terraform_version": "1.15.4",
		"complete":          complete,
		"errored":           false,
		"resource_changes": []any{map[string]any{
			"address": `zpa_sample.this["one"]`,
			"type":    "zpa_sample",
			"change":  change,
		}},
		"output_changes": map[string]any{},
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("json.Marshal(assessment plan) error = %v, want nil", err)
	}
	return string(encoded)
}

func cleanAssessmentPlanJSON(t *testing.T) string {
	t.Helper()
	return assessmentPlanJSON(t, true, map[string]any{
		"actions": []any{"no-op"},
		"before":  map[string]any{"credential": "raw-secret-24e1"},
		"after":   map[string]any{"credential": "raw-secret-24e1"},
	})
}

func assessmentOptions(
	fixture assessmentTransactionFixture,
	executable string,
	policyPath *string,
) SavedPlanAssessmentOptions {
	return SavedPlanAssessmentOptions{
		TerraformExecutable: executable,
		Roots:               []SavedPlanAssessmentRootInput{fixture.rootInput},
		PolicyPath:          policyPath,
	}
}

func requireSavedPlanAssessmentFailure(
	t *testing.T,
	err error,
	code string,
) *SavedPlanAssessmentFailure {
	t.Helper()
	var failure *SavedPlanAssessmentFailure
	if !errors.As(err, &failure) {
		t.Fatalf("error = %T(%v), want *SavedPlanAssessmentFailure code %q", err, err, code)
	}
	if failure.Code != code {
		t.Errorf("SavedPlanAssessmentFailure.Code = %q, want %q", failure.Code, code)
	}
	return failure
}

func TestAssessSavedPlansReturnsOnlyReportSafeCleanMetadataAndCleansSnapshot(t *testing.T) {
	fixture := newAssessmentTransactionFixture(t)
	marker := filepath.Join(fixture.root, "snapshot-path")
	executable := assessmentExecutable(t, fixture.root, strings.Join([]string{
		"printf '%s' \"$4\" > " + assessmentShellLiteral(marker),
		"printf '%s' " + assessmentShellLiteral(cleanAssessmentPlanJSON(t)),
	}, "\n"))
	core, err := AssessSavedPlans(assessmentOptions(fixture, executable, nil))
	if err != nil {
		t.Fatalf("AssessSavedPlans(clean plan) error = %v, want nil", err)
	}
	if core.Status != Clean || core.Checked != 1 || core.Clean != 1 ||
		core.Tolerated != 0 || core.Blocked != 0 {
		t.Errorf("AssessSavedPlans(clean plan) counts = %+v, want clean 1/1/0/0", core)
	}
	if len(core.Roots) != 1 {
		t.Fatalf("AssessSavedPlans(clean plan).Roots length = %d, want 1", len(core.Roots))
	}
	root := core.Roots[0]
	if root.Plan.FormatVersion == nil || *root.Plan.FormatVersion != "1.2" ||
		root.Plan.TerraformVersion == nil || *root.Plan.TerraformVersion != "1.15.4" ||
		len(root.Plan.SHA256) != 64 || len(root.Findings) != 0 {
		t.Errorf("AssessSavedPlans(clean plan).Roots[0] = %+v, want safe clean metadata", root)
	}
	encoded := fmt.Sprintf("%+v", core)
	if strings.Contains(encoded, "raw-secret-24e1") || strings.Contains(encoded, "opaque saved plan secret") {
		t.Errorf("AssessSavedPlans(clean plan) = %q, want no raw plan secret", encoded)
	}
	snapshotPath, err := os.ReadFile(marker)
	if err != nil {
		t.Fatalf("os.ReadFile(%q) error = %v, want nil", marker, err)
	}
	directory := filepath.Dir(string(snapshotPath))
	if _, err := os.Lstat(string(snapshotPath)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("os.Lstat(removed snapshot %q) error = %v, want not-exist", snapshotPath, err)
	}
	if _, err := os.Lstat(directory); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("os.Lstat(removed assessment directory %q) error = %v, want not-exist", directory, err)
	}
}

func TestAssessSavedPlansRejectsIncompletePlanAtContractBoundary(t *testing.T) {
	fixture := newAssessmentTransactionFixture(t)
	incomplete := assessmentPlanJSON(t, false, map[string]any{
		"actions": []any{"no-op"}, "before": map[string]any{}, "after": map[string]any{},
	})
	executable := assessmentExecutable(t, fixture.root, "printf '%s' "+assessmentShellLiteral(incomplete))
	failure := requireSavedPlanAssessmentFailure(
		t,
		func() error {
			_, err := AssessSavedPlans(assessmentOptions(fixture, executable, nil))
			return err
		}(),
		"INVALID_ASSESSMENT_PLAN",
	)
	if failure.Category != procerr.CategoryDomain ||
		failure.Message != "saved plan is outside the supported assessment contract" {
		t.Errorf("AssessSavedPlans(complete=false) failure = %+v, want redacted contract failure", failure.ProcessFailure)
	}
}

func TestAssessSavedPlansInvalidatesOriginalControlAndPolicyMutations(t *testing.T) {
	tests := []struct {
		name     string
		wantCode string
		prepare  func(*testing.T, assessmentTransactionFixture) (SavedPlanAssessmentOptions, string)
	}{
		{
			name:     "original_plan",
			wantCode: "SAVED_PLAN_CHANGED",
			prepare: func(t *testing.T, fixture assessmentTransactionFixture) (SavedPlanAssessmentOptions, string) {
				body := strings.Join([]string{
					"printf '%s' changed > " + assessmentShellLiteral(fixture.planPath),
					"printf '%s' " + assessmentShellLiteral(cleanAssessmentPlanJSON(t)),
				}, "\n")
				executable := assessmentExecutable(t, fixture.root, body)
				return assessmentOptions(fixture, executable, nil), fixture.planPath
			},
		},
		{
			name:     "control_file",
			wantCode: "ASSESSMENT_CONTROL_CHANGED",
			prepare: func(t *testing.T, fixture assessmentTransactionFixture) (SavedPlanAssessmentOptions, string) {
				controlPath := filepath.Join(fixture.root, "control.json")
				writeAssessmentTransactionFile(t, controlPath, []byte("control-before\n"), 0o600)
				bound, err := controlevidence.BindRequiredAssessmentControlText(
					controlPath,
					controlevidence.BindOptions{},
				)
				if err != nil {
					t.Fatalf("BindRequiredAssessmentControlText(%q) error = %v, want nil", controlPath, err)
				}
				body := strings.Join([]string{
					"printf '%s' changed > " + assessmentShellLiteral(controlPath),
					"printf '%s' " + assessmentShellLiteral(cleanAssessmentPlanJSON(t)),
				}, "\n")
				executable := assessmentExecutable(t, fixture.root, body)
				options := assessmentOptions(fixture, executable, nil)
				options.ControlFiles = []controlevidence.BoundAssessmentControlFile{bound.File}
				return options, controlPath
			},
		},
		{
			name:     "policy_file",
			wantCode: "DRIFT_POLICY_CHANGED",
			prepare: func(t *testing.T, fixture assessmentTransactionFixture) (SavedPlanAssessmentOptions, string) {
				policyPath := filepath.Join(fixture.root, "policy.json")
				writeAssessmentTransactionFile(t, policyPath, []byte(`{"version":1,"resource_types":{}}`), 0o600)
				body := strings.Join([]string{
					"printf '%s' '{\"version\":1,\"resource_types\":{\"changed\":{}}}' > " + assessmentShellLiteral(policyPath),
					"printf '%s' " + assessmentShellLiteral(cleanAssessmentPlanJSON(t)),
				}, "\n")
				executable := assessmentExecutable(t, fixture.root, body)
				return assessmentOptions(fixture, executable, &policyPath), policyPath
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newAssessmentTransactionFixture(t)
			options, secretPath := test.prepare(t, fixture)
			_, err := AssessSavedPlans(options)
			failure := requireSavedPlanAssessmentFailure(t, err, test.wantCode)
			if strings.Contains(failure.Error(), secretPath) {
				t.Errorf("AssessSavedPlans(%s mutation) error = %q, want path redacted", test.name, failure.Error())
			}
		})
	}
}

func TestAssessSavedPlansOrdersRootsAndRetainsOnlyCompletedRoots(t *testing.T) {
	fixture := newAssessmentTransactionFixture(t)
	marker := filepath.Join(fixture.root, "show-count")
	body := strings.Join([]string{
		"if [ -f " + assessmentShellLiteral(marker) + " ]; then",
		"  printf '%s' invalid-json",
		"else",
		"  : > " + assessmentShellLiteral(marker),
		"  printf '%s' " + assessmentShellLiteral(cleanAssessmentPlanJSON(t)),
		"fi",
	}, "\n")
	executable := assessmentExecutable(t, fixture.root, body)
	first := fixture.rootInput
	second := fixture.rootInput
	second.Label = "zpa_second"
	options := assessmentOptions(fixture, executable, nil)
	options.Roots = []SavedPlanAssessmentRootInput{second, first}
	_, err := AssessSavedPlans(options)
	failure := requireSavedPlanAssessmentFailure(t, err, "INVALID_TERRAFORM_SHOW_JSON")
	if failure.Partial.Checked != 1 || len(failure.Partial.Roots) != 1 ||
		failure.Partial.Roots[0].Label != "zpa_sample" {
		t.Errorf("AssessSavedPlans(later root failure).Partial = %+v, want completed zpa_sample only", failure.Partial)
	}
}

func TestAssessSavedPlansPolicyPrecedesContextAndZeroRootClassification(t *testing.T) {
	root := t.TempDir()
	invalidPolicy := filepath.Join(root, "invalid-policy.json")
	writeAssessmentTransactionFile(t, invalidPolicy, []byte(`{"version":`), 0o600)
	options := SavedPlanAssessmentOptions{
		TerraformExecutable: "/missing/terraform",
		PolicyPath:          &invalidPolicy,
		Context: &SavedPlanAssessmentContext{
			Workspace: "relative-workspace",
		},
	}
	_, err := AssessSavedPlans(options)
	requireSavedPlanAssessmentFailure(t, err, "INVALID_DRIFT_POLICY")

	options.PolicyPath = nil
	options.Context = nil
	_, err = AssessSavedPlans(options)
	failure := requireSavedPlanAssessmentFailure(t, err, "NO_SAVED_PLANS")
	if failure.ReportKind != NoSavedPlans || failure.Partial.Checked != 0 {
		t.Errorf("AssessSavedPlans(zero roots) failure = %+v, want no_saved_plans with empty partial", failure)
	}
}

func TestSavedPlanAssessmentOptionValidationPrecedence(t *testing.T) {
	validRoot := SavedPlanAssessmentRootInput{
		Tenant: "tenant", Label: "root", Members: []string{"zpa_sample"},
		EnvDir: "/missing/env", SavedPlanPath: "/missing/tfplan",
		FingerprintPath: "/missing/tfplan.sources", VarFiles: []string{},
	}
	tooMany := make([]SavedPlanAssessmentRootInput, MaxSavedPlanAssessmentRoots+1)
	for index := range tooMany {
		tooMany[index] = validRoot
		tooMany[index].Label = fmt.Sprintf("root_%d", index)
	}
	zero := int64(0)
	invalidLimits := artifacts.DefaultBoundedReadLimits()
	invalidLimits.MaxFiles = 0
	tests := []struct {
		name     string
		options  SavedPlanAssessmentTransactionOptions
		wantCode string
	}{
		{
			name: "root_count_before_control_copy",
			options: SavedPlanAssessmentTransactionOptions{Assessment: SavedPlanAssessmentOptions{
				TerraformExecutable: "/missing/terraform",
				Roots:               tooMany,
				ControlFiles: []controlevidence.BoundAssessmentControlFile{{
					Path: "relative-control",
				}},
			}},
			wantCode: "TOO_MANY_SAVED_PLANS",
		},
		{
			name: "root_before_numeric_limit",
			options: SavedPlanAssessmentTransactionOptions{
				Assessment: SavedPlanAssessmentOptions{
					TerraformExecutable: "/missing/terraform",
					Roots: []SavedPlanAssessmentRootInput{{
						Tenant: ".", Label: "root", Members: []string{"zpa_sample"},
						EnvDir: "/missing/env", SavedPlanPath: "/missing/tfplan",
						FingerprintPath: "/missing/tfplan.sources", VarFiles: []string{},
					}},
				},
				OperationTimeoutMs: &zero,
			},
			wantCode: "INVALID_ASSESSMENT_ROOT",
		},
		{
			name: "numeric_limit_before_path",
			options: SavedPlanAssessmentTransactionOptions{
				Assessment:         SavedPlanAssessmentOptions{TerraformExecutable: "relative-terraform"},
				OperationTimeoutMs: &zero,
			},
			wantCode: "INVALID_ASSESSMENT_LIMIT",
		},
		{
			name: "path_before_read_limit",
			options: SavedPlanAssessmentTransactionOptions{
				Assessment:   SavedPlanAssessmentOptions{TerraformExecutable: "relative-terraform"},
				SourceLimits: &invalidLimits,
			},
			wantCode: "UNRESOLVED_ASSESSMENT_PATH",
		},
		{
			name: "read_limit_before_policy",
			options: SavedPlanAssessmentTransactionOptions{
				Assessment: SavedPlanAssessmentOptions{
					TerraformExecutable: "/missing/terraform",
					PolicyPath:          func() *string { value := "/missing/policy"; return &value }(),
				},
				SourceLimits: &invalidLimits,
			},
			wantCode: "INVALID_ASSESSMENT_LIMIT",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := AssessSavedPlansWithOptions(test.options)
			requireSavedPlanAssessmentFailure(t, err, test.wantCode)
		})
	}
}

func TestAssessSavedPlansPolicyClassificationAndReportErrorPhase(t *testing.T) {
	fixture := newAssessmentTransactionFixture(t)
	policyPath := filepath.Join(fixture.root, "policy.json")
	policy := []byte(`{"version":1,"resource_types":{"zpa_sample":{"plan_tolerate":[` +
		`{"path":"status","reason":"known","approved_by":"owner"},` +
		`{"path":"unused","reason":"stale","approved_by":"owner"}]}}}`)
	writeAssessmentTransactionFile(t, policyPath, policy, 0o600)
	updated := assessmentPlanJSON(t, true, map[string]any{
		"actions": []any{"update"},
		"before":  map[string]any{"status": "old"},
		"after":   map[string]any{"status": "new"},
	})
	executable := assessmentExecutable(t, fixture.root, "printf '%s' "+assessmentShellLiteral(updated))
	core, err := AssessSavedPlans(assessmentOptions(fixture, executable, &policyPath))
	if err != nil {
		t.Fatalf("AssessSavedPlans(tolerated policy) error = %v, want nil", err)
	}
	if core.Status != Tolerated || core.Tolerated != 1 || len(core.Roots) != 1 ||
		len(core.Roots[0].Findings) != 1 || core.Roots[0].Findings[0].ResourceType == nil ||
		*core.Roots[0].Findings[0].ResourceType != "zpa_sample" {
		t.Errorf("AssessSavedPlans(tolerated policy) core = %+v, want one typed tolerated finding", core)
	}
	if len(core.StalePolicy) != 1 || core.StalePolicy[0].Path != "unused" {
		t.Errorf("AssessSavedPlans(tolerated policy).StalePolicy = %+v, want unused entry", core.StalePolicy)
	}

	invalidPolicyPath := filepath.Join(fixture.root, "invalid-policy.json")
	invalidBytes := []byte(`{"version":1,"resource_types":`)
	writeAssessmentTransactionFile(t, invalidPolicyPath, invalidBytes, 0o600)
	policyRequest := "invalid-policy.json"
	outcome, err := AssessSavedPlansReport(AssessSavedPlansReportOptions{
		Assessment: SavedPlanAssessmentTransactionOptions{Assessment: SavedPlanAssessmentOptions{
			TerraformExecutable: "/missing/terraform",
			PolicyPath:          &invalidPolicyPath,
		}},
		Mode: AssertAdoptable,
		Request: AssessmentReportRequest{
			Tenant: &fixture.rootInput.Tenant, Policy: &policyRequest, Selectors: []string{},
		},
	})
	if err != nil {
		t.Fatalf("AssessSavedPlansReport(invalid policy) error = %v, want outcome", err)
	}
	if outcome.Failure == nil || outcome.Failure.Code != "INVALID_DRIFT_POLICY" ||
		outcome.Failure.ReportKind != PolicyError || outcome.Report.Error == nil ||
		outcome.Report.Error.Kind != PolicyError {
		t.Errorf("AssessSavedPlansReport(invalid policy) outcome = %+v, want policy_error failure", outcome)
	}
	wantSHA := sha256.Sum256(invalidBytes)
	if outcome.Report.Request.PolicySHA256 == nil ||
		*outcome.Report.Request.PolicySHA256 != hex.EncodeToString(wantSHA[:]) {
		t.Errorf("AssessSavedPlansReport(invalid policy).PolicySHA256 = %v, want %x", outcome.Report.Request.PolicySHA256, wantSHA)
	}
}

func TestAssessSavedPlansResultCeilingRejectsBeforeRootRetention(t *testing.T) {
	fixture := newAssessmentTransactionFixture(t)
	updated := assessmentPlanJSON(t, true, map[string]any{
		"actions": []any{"update"},
		"before":  map[string]any{"status": "old"},
		"after":   map[string]any{"status": "new"},
	})
	executable := assessmentExecutable(t, fixture.root, "printf '%s' "+assessmentShellLiteral(updated))
	limits := SavedPlanAssessmentResultLimits{MaxFindings: 1, MaxPaths: 1, MaxMetadataBytes: 1}
	_, err := AssessSavedPlansWithOptions(SavedPlanAssessmentTransactionOptions{
		Assessment:   assessmentOptions(fixture, executable, nil),
		ResultLimits: &limits,
	})
	failure := requireSavedPlanAssessmentFailure(t, err, "ASSESSMENT_RESULT_LIMIT_EXCEEDED")
	if failure.Partial.Checked != 0 || len(failure.Partial.Roots) != 0 {
		t.Errorf("AssessSavedPlans(result ceiling).Partial = %+v, want no retained root", failure.Partial)
	}
}

func TestSavedPlanAssessmentTransactionOrderingAndFinalDoubleWindow(t *testing.T) {
	fixture := newAssessmentTransactionFixture(t)
	executable := assessmentExecutable(t, fixture.root, "printf '%s' "+assessmentShellLiteral(cleanAssessmentPlanJSON(t)))
	hooks := productionAssessmentHooks()
	sequence := make([]string, 0)
	realPrepare := hooks.prepareEvidence
	hooks.prepareEvidence = func(options plan.PrepareSavedPlanEvidenceOptions) (*plan.SavedPlanEvidence, error) {
		sequence = append(sequence, "prepare")
		return realPrepare(options)
	}
	realShow := hooks.showPlan
	hooks.showPlan = func(options terraformcmd.TerraformShowOptions) (any, error) {
		sequence = append(sequence, "show")
		return realShow(options)
	}
	realControls := hooks.recheckControls
	hooks.recheckControls = func(files []controlevidence.BoundAssessmentControlFile) error {
		sequence = append(sequence, "controls")
		return realControls(files)
	}
	realEvidence := hooks.recheckEvidence
	hooks.recheckEvidence = func(options plan.RecheckSavedPlanEvidenceOptions) error {
		sequence = append(sequence, "evidence")
		return realEvidence(options)
	}
	realPolicy := hooks.recheckPolicy
	hooks.recheckPolicy = func(bound BoundDriftPolicy, budget *artifacts.ReadBudget) error {
		sequence = append(sequence, "policy")
		return realPolicy(bound, budget)
	}
	_, err := runSavedPlanAssessment(
		SavedPlanAssessmentTransactionOptions{Assessment: assessmentOptions(fixture, executable, nil)},
		func(core SavedPlanAssessmentCore, _ []AssessmentGuidanceGroup) (SavedPlanAssessmentCore, error) {
			sequence = append(sequence, "finalize")
			return core, nil
		},
		nil,
		hooks,
	)
	if err != nil {
		t.Fatalf("runSavedPlanAssessment(ordering) error = %v, want nil", err)
	}
	want := []string{
		"controls", "controls",
		"prepare", "show", "controls", "evidence", "evidence",
		"evidence", "policy", "controls", "controls",
		"evidence", "policy", "controls", "controls",
		"finalize",
	}
	if !reflect.DeepEqual(sequence, want) {
		t.Errorf("runSavedPlanAssessment(ordering) sequence = %#v, want %#v", sequence, want)
	}
}

func TestSavedPlanAssessmentRejectsAsynchronousFinalizer(t *testing.T) {
	fixture := newAssessmentTransactionFixture(t)
	executable := assessmentExecutable(t, fixture.root, "printf '%s' "+assessmentShellLiteral(cleanAssessmentPlanJSON(t)))
	result, err := runSavedPlanAssessment(
		SavedPlanAssessmentTransactionOptions{Assessment: assessmentOptions(fixture, executable, nil)},
		func(SavedPlanAssessmentCore, []AssessmentGuidanceGroup) (map[string]any, error) {
			return map[string]any{"then": func() {}}, nil
		},
		nil,
		productionAssessmentHooks(),
	)
	failure := requireSavedPlanAssessmentFailure(t, err, "INVALID_ASSESSMENT_FINALIZER")
	if result != nil {
		t.Errorf("runSavedPlanAssessment(async finalizer) result = %#v, want nil on error", result)
	}
	if failure.Partial.Checked != 1 {
		t.Errorf("runSavedPlanAssessment(async finalizer).Partial.Checked = %d, want 1", failure.Partial.Checked)
	}
}

func TestSavedPlanAssessmentCleanupCompositeAndDirectoryIdentity(t *testing.T) {
	t.Run("composite", func(t *testing.T) {
		fixture := newAssessmentTransactionFixture(t)
		executable := assessmentExecutable(t, fixture.root, "exit 99")
		hooks := productionAssessmentHooks()
		hooks.showPlan = func(terraformcmd.TerraformShowOptions) (any, error) {
			return nil, procerr.NewProcessFailure(procerr.NewProcessFailureOptions{
				Code: "SHOW_FAILED", Category: procerr.CategoryDomain, Message: "show failed safely",
			})
		}
		realCleanup := hooks.cleanupEvidence
		hooks.cleanupEvidence = func(evidence *plan.SavedPlanEvidence) error {
			if err := realCleanup(evidence); err != nil {
				return err
			}
			return procerr.NewProcessFailure(procerr.NewProcessFailureOptions{
				Code: "SNAPSHOT_CLEANUP_FAILED", Category: procerr.CategoryIO, Message: "scrub failed safely",
			})
		}
		result, err := runSavedPlanAssessment(
			SavedPlanAssessmentTransactionOptions{Assessment: assessmentOptions(fixture, executable, nil)},
			func(core SavedPlanAssessmentCore, _ []AssessmentGuidanceGroup) (SavedPlanAssessmentCore, error) {
				return core, nil
			},
			nil,
			hooks,
		)
		failure := requireSavedPlanAssessmentFailure(t, err, "SHOW_FAILED")
		wantDetail := procerr.ErrorDetail{
			Path: "/", Code: "SNAPSHOT_CLEANUP_FAILED", Message: "private assessment cleanup also failed",
		}
		if len(failure.Details) != 1 || failure.Details[0] != wantDetail {
			t.Errorf("runSavedPlanAssessment(composite cleanup).Details = %+v, want %+v", failure.Details, []procerr.ErrorDetail{wantDetail})
		}
		if result.Checked != 0 || len(result.Roots) != 0 {
			t.Errorf("runSavedPlanAssessment(composite cleanup) result = %+v, want zero core on error", result)
		}
	})

	t.Run("cleanup_only_zeroes_result", func(t *testing.T) {
		fixture := newAssessmentTransactionFixture(t)
		executable := assessmentExecutable(t, fixture.root, "printf '%s' "+assessmentShellLiteral(cleanAssessmentPlanJSON(t)))
		hooks := productionAssessmentHooks()
		realCleanup := hooks.cleanupEvidence
		hooks.cleanupEvidence = func(evidence *plan.SavedPlanEvidence) error {
			if err := realCleanup(evidence); err != nil {
				return err
			}
			return procerr.NewProcessFailure(procerr.NewProcessFailureOptions{
				Code: "SNAPSHOT_CLEANUP_FAILED", Category: procerr.CategoryIO, Message: "scrub failed safely",
			})
		}
		result, err := runSavedPlanAssessment(
			SavedPlanAssessmentTransactionOptions{Assessment: assessmentOptions(fixture, executable, nil)},
			func(core SavedPlanAssessmentCore, _ []AssessmentGuidanceGroup) (SavedPlanAssessmentCore, error) {
				return core, nil
			},
			nil,
			hooks,
		)
		failure := requireSavedPlanAssessmentFailure(t, err, "SNAPSHOT_CLEANUP_FAILED")
		if result.Checked != 0 || len(result.Roots) != 0 {
			t.Errorf("runSavedPlanAssessment(cleanup-only failure) result = %+v, want zero core", result)
		}
		if failure.Partial.Checked != 1 || len(failure.Partial.Roots) != 1 {
			t.Errorf("runSavedPlanAssessment(cleanup-only failure).Partial = %+v, want completed root", failure.Partial)
		}
	})

	t.Run("directory_identity_after_bind", func(t *testing.T) {
		fixture := newAssessmentTransactionFixture(t)
		marker := filepath.Join(fixture.root, "snapshot-path")
		executable := assessmentExecutable(t, fixture.root, strings.Join([]string{
			"printf '%s' \"$4\" > " + assessmentShellLiteral(marker),
			"printf '%s' " + assessmentShellLiteral(cleanAssessmentPlanJSON(t)),
		}, "\n"))
		hooks := productionAssessmentHooks()
		var temporary string
		var boundBefore os.FileInfo
		var oldDirectory string
		hooks.makeTemporary = func() (string, error) {
			temporary = filepath.Join(fixture.root, "private-assessment")
			return temporary, os.Mkdir(temporary, 0o700)
		}
		hooks.cleanupHooks.afterDirectoryIdentity = func() error {
			snapshotPath, err := os.ReadFile(marker)
			if err != nil {
				return err
			}
			boundBefore, err = os.Lstat(string(snapshotPath))
			if err != nil {
				return err
			}
			oldDirectory = temporary + ".old"
			if err := os.Rename(temporary, oldDirectory); err != nil {
				return err
			}
			if err := os.Mkdir(temporary, 0o700); err != nil {
				return err
			}
			return os.WriteFile(filepath.Join(temporary, "replacement-secret"), []byte("keep me\n"), 0o600)
		}
		result, err := runSavedPlanAssessment(
			SavedPlanAssessmentTransactionOptions{Assessment: assessmentOptions(fixture, executable, nil)},
			func(core SavedPlanAssessmentCore, _ []AssessmentGuidanceGroup) (SavedPlanAssessmentCore, error) {
				return core, nil
			},
			nil,
			hooks,
		)
		failure := requireSavedPlanAssessmentFailure(t, err, "ASSESSMENT_CLEANUP_REFUSED")
		if result.Checked != 0 || failure.Partial.Checked != 1 {
			t.Errorf("runSavedPlanAssessment(directory swap) result/partial = %+v/%+v, want zero result and one completed partial root", result, failure.Partial)
		}
		replacement, readErr := os.ReadFile(filepath.Join(temporary, "replacement-secret"))
		if readErr != nil || string(replacement) != "keep me\n" {
			t.Errorf("replacement contents after refused cleanup = %q, %v, want untouched", replacement, readErr)
		}
		snapshotPath, readErr := os.ReadFile(marker)
		if readErr != nil {
			t.Fatalf("os.ReadFile(%q) error = %v, want nil", marker, readErr)
		}
		boundAfter, statErr := os.Lstat(filepath.Join(oldDirectory, filepath.Base(string(snapshotPath))))
		if statErr != nil {
			t.Fatalf("os.Lstat(bound snapshot after refused cleanup) error = %v, want nil", statErr)
		}
		if boundAfter.Size() != 0 || !os.SameFile(boundBefore, boundAfter) {
			t.Errorf("bound snapshot after refused cleanup = {size:%d same:%t}, want same scrubbed inode", boundAfter.Size(), os.SameFile(boundBefore, boundAfter))
		}
	})

	t.Run("directory_swap_composes_with_primary", func(t *testing.T) {
		fixture := newAssessmentTransactionFixture(t)
		executable := assessmentExecutable(t, fixture.root, "exit 99")
		hooks := productionAssessmentHooks()
		var temporary string
		hooks.makeTemporary = func() (string, error) {
			temporary = filepath.Join(fixture.root, "private-composite")
			return temporary, os.Mkdir(temporary, 0o700)
		}
		hooks.showPlan = func(terraformcmd.TerraformShowOptions) (any, error) {
			return nil, procerr.NewProcessFailure(procerr.NewProcessFailureOptions{
				Code: "SHOW_FAILED", Category: procerr.CategoryDomain, Message: "show failed safely",
			})
		}
		hooks.cleanupHooks.afterDirectoryIdentity = func() error {
			if err := os.Rename(temporary, temporary+".old"); err != nil {
				return err
			}
			if err := os.Mkdir(temporary, 0o700); err != nil {
				return err
			}
			return os.WriteFile(filepath.Join(temporary, "replacement-secret"), []byte("keep me\n"), 0o600)
		}
		result, err := runSavedPlanAssessment(
			SavedPlanAssessmentTransactionOptions{Assessment: assessmentOptions(fixture, executable, nil)},
			func(core SavedPlanAssessmentCore, _ []AssessmentGuidanceGroup) (SavedPlanAssessmentCore, error) {
				return core, nil
			},
			nil,
			hooks,
		)
		failure := requireSavedPlanAssessmentFailure(t, err, "SHOW_FAILED")
		if result.Checked != 0 || len(failure.Details) != 1 ||
			failure.Details[0].Code != "ASSESSMENT_CLEANUP_REFUSED" {
			t.Errorf("runSavedPlanAssessment(primary + directory swap) = result %+v details %+v, want zero plus cleanup-refused detail", result, failure.Details)
		}
		contents, readErr := os.ReadFile(filepath.Join(temporary, "replacement-secret"))
		if readErr != nil || string(contents) != "keep me\n" {
			t.Errorf("replacement after composite cleanup = %q, %v, want untouched", contents, readErr)
		}
	})
}

func TestAssessmentTemporaryCleanupRefusesUnexpectedOrReplacedEntries(t *testing.T) {
	newBinding := func(t *testing.T) (string, assessmentCleanupIdentity, assessmentCleanupSnapshot) {
		t.Helper()
		directory := t.TempDir()
		if err := os.Chmod(directory, 0o700); err != nil {
			t.Fatalf("os.Chmod(%q, 0700) error = %v, want nil", directory, err)
		}
		identity, err := directorySafeIdentity(directory)
		if err != nil {
			t.Fatalf("directorySafeIdentity(%q) error = %v, want nil", directory, err)
		}
		filePath := filepath.Join(directory, "snapshot")
		if err := os.WriteFile(filePath, []byte{}, 0o600); err != nil {
			t.Fatalf("os.WriteFile(%q, empty) error = %v, want nil", filePath, err)
		}
		info, err := os.Lstat(filePath)
		if err != nil {
			t.Fatalf("os.Lstat(%q) error = %v, want nil", filePath, err)
		}
		fileIdentity, ok := assessmentCleanupFileIdentity(info)
		if !ok {
			t.Fatalf("assessmentCleanupFileIdentity(%q) unavailable, want identity", filePath)
		}
		return directory, identity, assessmentCleanupSnapshot{name: "snapshot", identity: fileIdentity}
	}

	t.Run("successful_validation_removes_bound_directory", func(t *testing.T) {
		directory, identity, snapshot := newBinding(t)
		if failure := cleanupAssessmentTemporaryDirectory(
			directory,
			identity,
			[]assessmentCleanupSnapshot{snapshot},
			assessmentCleanupHooks{},
		); failure != nil {
			t.Fatalf("cleanupAssessmentTemporaryDirectory(bound zero inode) failure = %+v, want nil", failure)
		}
		if _, err := os.Lstat(directory); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("os.Lstat(removed bound directory %q) error = %v, want not-exist", directory, err)
		}
	})

	t.Run("unexpected_entry", func(t *testing.T) {
		directory, identity, snapshot := newBinding(t)
		unexpected := filepath.Join(directory, "unexpected-secret")
		if err := os.WriteFile(unexpected, []byte("keep me\n"), 0o600); err != nil {
			t.Fatalf("os.WriteFile(%q) error = %v, want nil", unexpected, err)
		}
		failure := cleanupAssessmentTemporaryDirectory(
			directory,
			identity,
			[]assessmentCleanupSnapshot{snapshot},
			assessmentCleanupHooks{},
		)
		if failure == nil || failure.Code != "ASSESSMENT_CLEANUP_REFUSED" {
			t.Errorf("cleanupAssessmentTemporaryDirectory(unexpected entry) = %+v, want ASSESSMENT_CLEANUP_REFUSED", failure)
		}
		contents, err := os.ReadFile(unexpected)
		if err != nil || string(contents) != "keep me\n" {
			t.Errorf("unexpected entry after refused cleanup = %q, %v, want untouched", contents, err)
		}
	})

	t.Run("replaced_snapshot", func(t *testing.T) {
		directory, identity, snapshot := newBinding(t)
		filePath := filepath.Join(directory, snapshot.name)
		if err := os.Remove(filePath); err != nil {
			t.Fatalf("os.Remove(%q) error = %v, want nil", filePath, err)
		}
		if err := os.WriteFile(filePath, []byte{}, 0o600); err != nil {
			t.Fatalf("os.WriteFile(replacement %q) error = %v, want nil", filePath, err)
		}
		failure := cleanupAssessmentTemporaryDirectory(
			directory,
			identity,
			[]assessmentCleanupSnapshot{snapshot},
			assessmentCleanupHooks{},
		)
		if failure == nil || failure.Code != "ASSESSMENT_CLEANUP_REFUSED" {
			t.Errorf("cleanupAssessmentTemporaryDirectory(replaced snapshot) = %+v, want ASSESSMENT_CLEANUP_REFUSED", failure)
		}
	})

	t.Run("directory_rebound_before_rmdir", func(t *testing.T) {
		directory, identity, snapshot := newBinding(t)
		original := directory + ".original"
		target := t.TempDir()
		sentinel := filepath.Join(target, "sentinel")
		if err := os.WriteFile(sentinel, []byte("keep me\n"), 0o600); err != nil {
			t.Fatalf("os.WriteFile(rebound target sentinel) error = %v, want nil", err)
		}
		failure := cleanupAssessmentTemporaryDirectory(
			directory,
			identity,
			[]assessmentCleanupSnapshot{snapshot},
			assessmentCleanupHooks{beforeDirectoryRemoval: func() error {
				if err := os.Rename(directory, original); err != nil {
					return err
				}
				return os.Symlink(target, directory)
			}},
		)
		if failure == nil || failure.Code != "ASSESSMENT_CLEANUP_REFUSED" {
			t.Errorf("cleanupAssessmentTemporaryDirectory(rebound before rmdir) = %+v, want ASSESSMENT_CLEANUP_REFUSED", failure)
		}
		linkTarget, err := os.Readlink(directory)
		if err != nil || linkTarget != target {
			t.Errorf("rebound directory symlink after refused rmdir = %q, %v, want untouched target %q", linkTarget, err, target)
		}
		contents, err := os.ReadFile(sentinel)
		if err != nil || string(contents) != "keep me\n" {
			t.Errorf("rebound target sentinel after refused rmdir = %q, %v, want untouched", contents, err)
		}
		entries, err := os.ReadDir(original)
		if err != nil || len(entries) != 0 {
			t.Errorf("original directory after refused rmdir = %d entries, %v, want empty scrubbed remnant", len(entries), err)
		}
	})
}

func TestSavedPlanAssessmentPostFinalizerTimeoutZeroesResult(t *testing.T) {
	fixture := newAssessmentTransactionFixture(t)
	executable := assessmentExecutable(t, fixture.root, "printf '%s' "+assessmentShellLiteral(cleanAssessmentPlanJSON(t)))
	hooks := productionAssessmentHooks()
	start := time.Unix(1_700_000_000, 0)
	finalized := false
	hooks.now = func() time.Time {
		if finalized {
			return start.Add(2 * time.Second)
		}
		return start
	}
	timeout := int64(1_000)
	result, err := runSavedPlanAssessment(
		SavedPlanAssessmentTransactionOptions{
			Assessment:         assessmentOptions(fixture, executable, nil),
			OperationTimeoutMs: &timeout,
		},
		func(core SavedPlanAssessmentCore, _ []AssessmentGuidanceGroup) (SavedPlanAssessmentCore, error) {
			finalized = true
			return core, nil
		},
		nil,
		hooks,
	)
	failure := requireSavedPlanAssessmentFailure(t, err, "ASSESSMENT_TIMEOUT")
	if result.Checked != 0 || len(result.Roots) != 0 {
		t.Errorf("runSavedPlanAssessment(post-finalizer timeout) result = %+v, want zero core", result)
	}
	if failure.Partial.Checked != 1 || len(failure.Partial.Roots) != 1 {
		t.Errorf("runSavedPlanAssessment(post-finalizer timeout).Partial = %+v, want completed root", failure.Partial)
	}
}

func TestAssessmentContextDeadlineBoundsExactMaximumControlRecheck(t *testing.T) {
	const fileBytes = 16 * 1024 * 1024
	zeroes := make([]byte, fileBytes)
	digest := sha256.Sum256(zeroes)
	controls := make([]controlevidence.BoundAssessmentControlFile, 4)
	for index := range controls {
		filePath := filepath.Join(t.TempDir(), fmt.Sprintf("control-%d", index))
		file, err := os.OpenFile(filePath, os.O_CREATE|os.O_WRONLY, 0o600)
		if err != nil {
			t.Fatalf("os.OpenFile(%q) error = %v, want nil", filePath, err)
		}
		if err := file.Truncate(fileBytes); err != nil {
			_ = file.Close()
			t.Fatalf("file.Truncate(%q, %d) error = %v, want nil", filePath, fileBytes, err)
		}
		if err := file.Close(); err != nil {
			t.Fatalf("file.Close(%q) error = %v, want nil", filePath, err)
		}
		boundDigest := artifacts.StableFileDigest{
			SHA256: hex.EncodeToString(digest[:]),
			Size:   fileBytes,
		}
		controls[index] = controlevidence.BoundAssessmentControlFile{
			Path: filePath, Digest: &boundDigest,
		}
	}
	start := time.Unix(1_700_000_000, 0)
	nowCalls := 0
	hooks := productionAssessmentHooks()
	hooks.now = func() time.Time {
		nowCalls++
		if nowCalls == 1 {
			return start
		}
		return start.Add(2 * time.Second)
	}
	started := time.Now()
	err := recheckAssessmentContext(
		capturedAssessmentOptions{assessment: SavedPlanAssessmentOptions{ControlFiles: controls}},
		start.Add(time.Second),
		hooks,
	)
	elapsed := time.Since(started)
	requireAssessmentFailure(t, err, "ASSESSMENT_TIMEOUT")
	if nowCalls != 2 {
		t.Errorf("recheckAssessmentContext(exact 64-MiB set) deadline checks = %d, want 2 around one bounded physical recheck", nowCalls)
	}
	if elapsed > 30*time.Second {
		t.Errorf("recheckAssessmentContext(exact 64-MiB set) elapsed = %v, want bounded completion within 30s", elapsed)
	}
}
