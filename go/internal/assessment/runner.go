package assessment

// This file ports the original implementation: it coordinates
// policy preflight, lazy active-pack input resolution, saved-plan assessment,
// report publication, and operator diagnostics without adding CLI concerns.

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"

	"github.com/dvmrry/infrawright-dev/go/internal/canonjson"
	"github.com/dvmrry/infrawright-dev/go/internal/controlevidence"
	"github.com/dvmrry/infrawright-dev/go/internal/deployment"
	"github.com/dvmrry/infrawright-dev/go/internal/metadata"
	"github.com/dvmrry/infrawright-dev/go/internal/procerr"
)

// SavedPlanAssertionInputs ports the same-named interface from
// the original implementation. It contains the active pack and
// deployment loaded by an assert-clean or assert-adoptable command.
// ControlFiles bind source documents whose freshness must survive the
// assessment transaction.
type SavedPlanAssertionInputs struct {
	Deployment   deployment.Deployment
	Root         metadata.LoadedPackRoot
	ControlFiles []controlevidence.BoundAssessmentControlFile
}

// RunSavedPlanAssertionOptions ports RunSavedPlanAssertionOptions from
// the original implementation and supplies the operational inputs
// to RunSavedPlanAssertion. Exactly one of Inputs and LoadInputs should be set.
// A non-empty TerraformExecutable is an already-resolved executable; otherwise
// ResolveTerraformExecutable is called only when at least one root is selected.
type RunSavedPlanAssertionOptions struct {
	Workspace string
	Mode      AssessmentMode

	Tenant    *string
	Selectors []string

	BackendConfig *string
	PolicyPath    *string
	ReportPath    *string

	TerraformExecutable        string
	ResolveTerraformExecutable func() (string, error)

	Inputs     *SavedPlanAssertionInputs
	LoadInputs func() (SavedPlanAssertionInputs, error)

	OnDiagnostic func(string)
	Stdout       func(string) error
}

type runSavedPlanAssertionHooks struct {
	preflightPolicy func(*string) (BoundDriftPolicy, error)
	resolveInputs   func(ResolveLoadedSavedPlanAssessmentOptions) (ResolvedSavedPlanAssessment, error)
	assessReport    func(AssessSavedPlansReportOptions) (SavedPlanAssessmentReportOutcome, error)
	writeReport     func(WriteAssessmentReportOptions) error
	guidanceSource  func(metadata.LoadedPackRoot) AssessmentGuidanceSource
}

func productionSavedPlanAssertionHooks() runSavedPlanAssertionHooks {
	return runSavedPlanAssertionHooks{
		preflightPolicy: PreflightSavedPlanAssessmentPolicy,
		resolveInputs:   ResolveLoadedSavedPlanAssessment,
		assessReport:    AssessSavedPlansReport,
		writeReport:     WriteAssessmentReport,
		guidanceSource:  NewAssessmentGuidanceSource,
	}
}

func cloneSavedPlanAssertionInputs(input SavedPlanAssertionInputs) SavedPlanAssertionInputs {
	return SavedPlanAssertionInputs{
		Deployment:   copyDeploymentForAssessment(input.Deployment),
		Root:         input.Root,
		ControlFiles: copyControlFilesForAssessment(input.ControlFiles, true),
	}
}

func loadSavedPlanAssertionInputs(options RunSavedPlanAssertionOptions) (SavedPlanAssertionInputs, error) {
	if options.Inputs != nil && options.LoadInputs != nil {
		return SavedPlanAssertionInputs{}, procerr.NewProcessFailure(procerr.NewProcessFailureOptions{
			Code:     "INVALID_ASSESSMENT_INPUT",
			Category: procerr.CategoryRequest,
			Message:  "saved-plan assertion inputs are ambiguous",
		})
	}
	if options.LoadInputs != nil {
		inputs, err := options.LoadInputs()
		if err != nil {
			return SavedPlanAssertionInputs{}, err
		}
		return cloneSavedPlanAssertionInputs(inputs), nil
	}
	if options.Inputs != nil {
		return cloneSavedPlanAssertionInputs(*options.Inputs), nil
	}
	return SavedPlanAssertionInputs{}, procerr.NewProcessFailure(procerr.NewProcessFailureOptions{
		Code:     "INVALID_ASSESSMENT_INPUT",
		Category: procerr.CategoryRequest,
		Message:  "saved-plan assertion inputs are missing",
	})
}

func runnerResolvedPath(workspace, candidate string) (string, error) {
	if filepath.IsAbs(candidate) {
		return filepath.Abs(candidate)
	}
	return filepath.Abs(filepath.Join(workspace, candidate))
}

func runnerDiagnosticJSON(value any) (string, error) {
	switch typed := value.(type) {
	case nil, bool, string, json.Number, float64:
		rendered, err := canonjson.Render(typed)
		if err != nil {
			return "", errors.New("diagnostic value is not JSON")
		}
		return strings.TrimSuffix(rendered, "\n"), nil
	case int:
		return strconv.FormatInt(int64(typed), 10), nil
	case int8:
		return strconv.FormatInt(int64(typed), 10), nil
	case int16:
		return strconv.FormatInt(int64(typed), 10), nil
	case int32:
		return strconv.FormatInt(int64(typed), 10), nil
	case int64:
		return strconv.FormatInt(typed, 10), nil
	case uint:
		return strconv.FormatUint(uint64(typed), 10), nil
	case uint8:
		return strconv.FormatUint(uint64(typed), 10), nil
	case uint16:
		return strconv.FormatUint(uint64(typed), 10), nil
	case uint32:
		return strconv.FormatUint(uint64(typed), 10), nil
	case uint64:
		return strconv.FormatUint(typed, 10), nil
	case []any:
		parts := make([]string, len(typed))
		for index, child := range typed {
			part, err := runnerDiagnosticJSON(child)
			if err != nil {
				return "", err
			}
			parts[index] = part
		}
		return "[" + strings.Join(parts, ", ") + "]", nil
	case map[string]any:
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		keys = canonjson.SortedStrings(keys)
		parts := make([]string, len(keys))
		for index, key := range keys {
			encodedKey, err := runnerDiagnosticJSON(key)
			if err != nil {
				return "", err
			}
			encodedValue, err := runnerDiagnosticJSON(typed[key])
			if err != nil {
				return "", err
			}
			parts[index] = encodedKey + ": " + encodedValue
		}
		return "{" + strings.Join(parts, ", ") + "}", nil
	default:
		return "", errors.New("diagnostic value is not JSON")
	}
}

func runnerGuidanceText(entry map[string]any, field string) string {
	value, present := entry[field]
	if !present || value == nil {
		return "None"
	}
	switch typed := value.(type) {
	case string:
		return typed
	case bool:
		return strconv.FormatBool(typed)
	case json.Number:
		return string(typed)
	case float64:
		return javascriptNumberToken(typed)
	default:
		return fmt.Sprint(value)
	}
}

func emitRunnerGuidance(
	guidance []map[string]any,
	emit func(string),
) error {
	lanes := []struct {
		name    string
		heading string
	}{
		{name: "provider_config", heading: "Provider configuration guidance:"},
		{name: "absent_default", heading: "Absent/default guidance:"},
		{name: "dynamic_schema", heading: "Dynamic-schema guidance:"},
	}
	for _, lane := range lanes {
		entries := make([]map[string]any, 0)
		for _, entry := range guidance {
			if entryLane, _ := entry["lane"].(string); entryLane == lane.name {
				entries = append(entries, entry)
			}
		}
		if len(entries) == 0 {
			continue
		}
		emit("  " + lane.heading)
		for _, entry := range entries {
			switch lane.name {
			case "provider_config":
				emit("    - provider: " + runnerGuidanceText(entry, "provider"))
				emit("      setting: " + runnerGuidanceText(entry, "setting"))
				if expected, present := entry["expected_value"]; present && expected != nil {
					encoded, err := runnerDiagnosticJSON(expected)
					if err != nil {
						return err
					}
					emit("      expected value: " + encoded)
				}
				emit("      mode: " + runnerGuidanceText(entry, "mode"))
			case "absent_default":
				emit("    - rule: " + runnerGuidanceText(entry, "rule"))
				emit("      provider: " + runnerGuidanceText(entry, "provider"))
				emit("      resource type: " + runnerGuidanceText(entry, "resource_type"))
				emit("      kind: " + runnerGuidanceText(entry, "kind"))
				emit("      action: " + runnerGuidanceText(entry, "action"))
				if observed, present := entry["observed_value"]; present {
					encoded, err := runnerDiagnosticJSON(observed)
					if err != nil {
						return err
					}
					emit("      observed value: " + encoded)
				}
			case "dynamic_schema":
				emit("    - rule: " + runnerGuidanceText(entry, "rule"))
				emit("      provider: " + runnerGuidanceText(entry, "provider"))
				emit("      resource type: " + runnerGuidanceText(entry, "resource_type"))
				emit("      kind: " + runnerGuidanceText(entry, "kind"))
				emit("      ownership: " + runnerGuidanceText(entry, "ownership"))
				emit("      action: " + runnerGuidanceText(entry, "action"))
				if constraint, _ := entry["provider_version_constraint"].(string); constraint != "" {
					emit("      provider version constraint: " + constraint)
				}
			}
			emit("      matched plan path: " + runnerGuidanceText(entry, "matched_plan_path"))
			emit("      reason: " + runnerGuidanceText(entry, "reason"))
			if evidence, _ := entry["evidence"].(string); evidence != "" {
				emit("      evidence: " + evidence)
			}
			emit("      status: " + runnerGuidanceText(entry, "status_effect"))
		}
	}
	return nil
}

func emitRunnerFindings(
	root AssessmentReportRoot,
	includeGuidance bool,
	emit func(string),
) error {
	for _, finding := range root.Findings {
		address := finding.Address
		if address == "" {
			address = "None"
		}
		emit("  " + address + " " + strings.Join(finding.Actions, ",") + " " + string(finding.Status))
		for _, planPath := range finding.Paths {
			emit("    - " + planPath)
		}
	}
	if includeGuidance {
		return emitRunnerGuidance(root.Guidance, emit)
	}
	return nil
}

func emitRunnerAssessment(report SavedPlanAssessmentReport, emit func(string)) error {
	if report.Mode == AssertClean {
		for _, root := range report.Roots {
			if root.Status == Clean {
				continue
			}
			emit(fmt.Sprintf(
				"NOT CLEAN: %s/%s plan contains %d change(s) beyond imports",
				root.Tenant,
				root.Label,
				len(root.Findings),
			))
		}
		return nil
	}
	for _, root := range report.Roots {
		switch root.Status {
		case Blocked:
			emit("BLOCKED: " + root.Tenant + "/" + root.Label)
			if err := emitRunnerFindings(root, true, emit); err != nil {
				return err
			}
		case Tolerated:
			emit("TOLERATED: " + root.Tenant + "/" + root.Label)
			if err := emitRunnerFindings(root, false, emit); err != nil {
				return err
			}
		}
	}
	for _, stale := range report.StalePolicy {
		emit(
			"STALE DRIFT POLICY: " + stale.ResourceType + " " + string(stale.Mode) +
				" " + stale.Path + " matched no path",
		)
	}
	return nil
}

func runnerBlockedFailure(report SavedPlanAssessmentReport) *procerr.ProcessFailure {
	if report.Mode == AssertClean {
		return procerr.NewProcessFailure(procerr.NewProcessFailureOptions{
			Code:     "PLAN_NOT_CLEAN",
			Category: procerr.CategoryDomain,
			Message:  "tenant moved since fetch (or transform disagrees) - do not auto-merge",
		})
	}
	return procerr.NewProcessFailure(procerr.NewProcessFailureOptions{
		Code:     "PLAN_NOT_ADOPTABLE",
		Category: procerr.CategoryDomain,
		Message:  fmt.Sprintf("%d saved plan(s) blocked by untolerated changes", report.Summary.Blocked),
	})
}

func genericRunnerFailure() *procerr.ProcessFailure {
	return procerr.NewProcessFailure(procerr.NewProcessFailureOptions{
		Code:     "ASSESSMENT_FAILED",
		Category: procerr.CategoryInternal,
		Message:  "saved-plan assessment failed",
	})
}

func runnerErrorIsTypedNil(err error) bool {
	if err == nil {
		return false
	}
	value := reflect.ValueOf(err)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}

func runnerSafeFailure(err error) (result *procerr.ProcessFailure) {
	result = genericRunnerFailure()
	defer func() {
		if recover() != nil {
			result = genericRunnerFailure()
		}
	}()
	if runnerErrorIsTypedNil(err) {
		return result
	}
	var failure *procerr.ProcessFailure
	if errors.As(err, &failure) {
		if failure == nil {
			return result
		}
		return procerr.NewProcessFailure(procerr.NewProcessFailureOptions{
			Code:      failure.Code,
			Category:  failure.Category,
			Message:   failure.Message,
			Retryable: failure.Retryable,
			Details:   append([]procerr.ErrorDetail{}, failure.Details...),
		})
	}
	var metadataFailure *metadata.MetadataError
	if errors.As(err, &metadataFailure) {
		if metadataFailure == nil {
			return result
		}
		return procerr.NewProcessFailure(procerr.NewProcessFailureOptions{
			Code:     "INVALID_ASSESSMENT_INPUT",
			Category: procerr.CategoryRequest,
			Message:  metadataFailure.Error(),
		})
	}
	if err != nil {
		candidate := err.Error()
		if candidate != "" {
			return procerr.NewProcessFailure(procerr.NewProcessFailureOptions{
				Code:     "ASSESSMENT_FAILED",
				Category: procerr.CategoryInternal,
				Message:  candidate,
			})
		}
	}
	return result
}

func emptyRunnerAssessment(policySHA256 *string) SavedPlanAssessmentCore {
	return SavedPlanAssessmentCore{
		Status:       Clean,
		PolicySHA256: cloneStringPointer(policySHA256),
		Roots:        []AssessedSavedPlanRoot{},
		StalePolicy:  []metadata.StalePolicyEntry{},
	}
}

func runnerTenantIsValid(tenant string) bool {
	if tenant == "." || tenant == ".." || tenant == "" {
		return false
	}
	for _, character := range tenant {
		if (character >= 'A' && character <= 'Z') ||
			(character >= 'a' && character <= 'z') ||
			(character >= '0' && character <= '9') ||
			character == '_' || character == '.' || character == '-' {
			continue
		}
		return false
	}
	return true
}

func buildRunnerPreflightErrorReport(
	mode AssessmentMode,
	request AssessmentReportRequest,
	partial SavedPlanAssessmentCore,
	reportError AssessmentReportError,
) (SavedPlanAssessmentReport, error) {
	report, err := BuildSavedPlanAssessmentErrorReport(BuildSavedPlanAssessmentErrorReportOptions{
		Mode: mode, Request: request, Partial: partial, Error: reportError,
	})
	if err == nil {
		return report, nil
	}
	var failure *procerr.ProcessFailure
	if !errors.As(err, &failure) || failure.Code != "INVALID_ASSESSMENT_REPORT" ||
		request.Tenant == nil || runnerTenantIsValid(*request.Tenant) || len(partial.Roots) != 0 {
		return SavedPlanAssessmentReport{}, err
	}
	// CPython records an invalid raw invocation before tenant validation. Keep
	// this narrow error-only fallback outside the published schema validation.
	report = SavedPlanAssessmentReport{
		Kind:          "infrawright.saved_plan_assessment",
		SchemaVersion: 1,
		Mode:          mode,
		Summary: AssessmentReportSummary{
			Status: "error",
		},
		Roots:       []AssessmentReportRoot{},
		StalePolicy: []metadata.StalePolicyEntry{},
		Error: &AssessmentReportError{
			Kind: reportError.Kind, Message: reportError.Message,
		},
	}
	report.Request.Tenant = cloneStringPointer(request.Tenant)
	report.Request.Selectors = append([]string{}, request.Selectors...)
	if mode != AssertClean {
		report.Request.Policy = cloneStringPointer(request.Policy)
		report.Request.PolicySHA256 = cloneStringPointer(partial.PolicySHA256)
	}
	return report, nil
}

func writeRunnerErrorReportBestEffort(
	hooks runSavedPlanAssertionHooks,
	emit func(string),
	path *string,
	report SavedPlanAssessmentReport,
	stdout func(string) error,
) {
	err := hooks.writeReport(WriteAssessmentReportOptions{
		Path: path, Report: report, Stdout: stdout,
	})
	if err == nil {
		return
	}
	pathValue := any(nil)
	if path != nil {
		pathValue = *path
	}
	formattedPath, formatErr := runnerDiagnosticJSON(pathValue)
	if formatErr != nil {
		formattedPath = "null"
	}
	emit(
		"WARNING: could not write assessment error report " + formattedPath + ": " +
			err.Error() + "; preserving original assessment error",
	)
}

func runSavedPlanAssertion(
	supplied RunSavedPlanAssertionOptions,
	hooks runSavedPlanAssertionHooks,
) error {
	options := supplied
	options.Tenant = cloneStringPointer(supplied.Tenant)
	options.Selectors = append([]string{}, supplied.Selectors...)
	options.BackendConfig = cloneStringPointer(supplied.BackendConfig)
	options.PolicyPath = cloneStringPointer(supplied.PolicyPath)
	options.ReportPath = cloneStringPointer(supplied.ReportPath)
	emit := options.OnDiagnostic
	if emit == nil {
		emit = func(string) {}
	}
	request := AssessmentReportRequest{
		Tenant: cloneStringPointer(options.Tenant), Selectors: append([]string{}, options.Selectors...),
	}
	if options.Mode != AssertClean {
		request.Policy = cloneStringPointer(options.PolicyPath)
	}

	var policyPath *string
	if options.Mode != AssertClean && options.PolicyPath != nil {
		resolved, err := runnerResolvedPath(options.Workspace, *options.PolicyPath)
		if err != nil {
			return runnerSafeFailure(err)
		}
		policyPath = &resolved
	}
	unresolvedTerraform, err := runnerResolvedPath(
		options.Workspace,
		".infrawright-unresolved-terraform",
	)
	if err != nil {
		return runnerSafeFailure(err)
	}

	var policySHA256 *string
	boundPolicy, err := hooks.preflightPolicy(policyPath)
	if err != nil {
		failure := runnerSafeFailure(err)
		var policyFailure *DriftPolicyLoadFailure
		if errors.As(err, &policyFailure) {
			sha := policyFailure.File.SHA256
			policySHA256 = &sha
		}
		if options.ReportPath != nil {
			report, reportErr := buildRunnerPreflightErrorReport(
				options.Mode,
				request,
				emptyRunnerAssessment(policySHA256),
				AssessmentReportError{Kind: PolicyError, Message: failure.Message},
			)
			if reportErr != nil {
				return reportErr
			}
			writeRunnerErrorReportBestEffort(
				hooks, emit, options.ReportPath, report, options.Stdout,
			)
		}
		return failure
	}
	if boundPolicy.File != nil {
		sha := boundPolicy.File.SHA256
		policySHA256 = &sha
	}

	var inputs SavedPlanAssertionInputs
	var resolved ResolvedSavedPlanAssessment
	var guidanceSource *AssessmentGuidanceSource
	terraformExecutable := options.TerraformExecutable
	inputs, err = loadSavedPlanAssertionInputs(options)
	if err == nil {
		resolverTerraformExecutable := unresolvedTerraform
		if terraformExecutable != "" {
			resolverTerraformExecutable = terraformExecutable
		}
		resolved, err = hooks.resolveInputs(ResolveLoadedSavedPlanAssessmentOptions{
			Workspace:           options.Workspace,
			Deployment:          inputs.Deployment,
			Root:                inputs.Root,
			Tenant:              options.Tenant,
			Selectors:           append([]string{}, options.Selectors...),
			TerraformExecutable: resolverTerraformExecutable,
			BackendConfig:       options.BackendConfig,
			PolicyPath:          policyPath,
			ControlFiles:        inputs.ControlFiles,
		})
	}
	if err == nil {
		if options.Mode == AssertAdoptable {
			guidance := hooks.guidanceSource(inputs.Root)
			guidanceSource = &guidance
		}
		for _, diagnostic := range resolved.Diagnostics {
			emit("NOTE: " + diagnostic.Message)
		}
		if terraformExecutable == "" && len(resolved.Assessment.Roots) > 0 {
			if options.ResolveTerraformExecutable == nil {
				err = errors.New("Terraform executable resolver is missing")
			} else {
				terraformExecutable, err = options.ResolveTerraformExecutable()
			}
		}
		if terraformExecutable == "" {
			terraformExecutable = unresolvedTerraform
		}
	}
	if err != nil {
		failure := runnerSafeFailure(err)
		if options.ReportPath != nil {
			report, reportErr := buildRunnerPreflightErrorReport(
				options.Mode,
				request,
				emptyRunnerAssessment(policySHA256),
				AssessmentReportError{Kind: AssessmentError, Message: failure.Message},
			)
			if reportErr != nil {
				pathValue := any(nil)
				if options.ReportPath != nil {
					pathValue = *options.ReportPath
				}
				formattedPath, formatErr := runnerDiagnosticJSON(pathValue)
				if formatErr != nil {
					formattedPath = "null"
				}
				emit(
					"WARNING: could not write assessment error report " + formattedPath + ": " +
						reportErr.Error() + "; preserving original assessment error",
				)
			} else {
				writeRunnerErrorReportBestEffort(
					hooks, emit, options.ReportPath, report, options.Stdout,
				)
			}
		}
		return failure
	}

	resolved.Assessment.TerraformExecutable = terraformExecutable
	transaction := SavedPlanAssessmentTransactionOptions{
		Assessment:              resolved.Assessment,
		ExpectedPolicySHA256:    cloneStringPointer(policySHA256),
		HasExpectedPolicySHA256: true,
	}
	assessmentOptions := AssessSavedPlansReportOptions{
		Assessment: transaction,
		Mode:       options.Mode,
		Request:    request,
	}
	assessmentOptions.GuidanceSource = guidanceSource
	outcome, err := hooks.assessReport(assessmentOptions)
	if err != nil {
		return runnerSafeFailure(err)
	}
	if err := emitRunnerAssessment(outcome.Report, emit); err != nil {
		return runnerSafeFailure(err)
	}
	if outcome.Failure != nil {
		writeRunnerErrorReportBestEffort(
			hooks, emit, options.ReportPath, outcome.Report, options.Stdout,
		)
		return outcome.Failure
	}
	if err := hooks.writeReport(WriteAssessmentReportOptions{
		Path: options.ReportPath, Report: outcome.Report, Stdout: options.Stdout,
	}); err != nil {
		return err
	}
	if outcome.Report.Summary.Blocked > 0 {
		return runnerBlockedFailure(outcome.Report)
	}
	if options.Mode == AssertClean {
		emit(fmt.Sprintf(
			"all %d saved plan(s) clean (no-op/imports only)",
			outcome.Report.Summary.Checked,
		))
	} else if outcome.Report.Summary.Tolerated > 0 {
		emit(fmt.Sprintf(
			"%d saved plan(s) adoptable with consumer-tolerated drift",
			outcome.Report.Summary.Tolerated,
		))
	} else {
		emit(fmt.Sprintf("all %d saved plan(s) clean", outcome.Report.Summary.Checked))
	}
	return nil
}

// RunSavedPlanAssertion ports runSavedPlanAssertion from
// the original implementation over the already-ported Go
// assessment primitives.
func RunSavedPlanAssertion(options RunSavedPlanAssertionOptions) error {
	return runSavedPlanAssertion(options, productionSavedPlanAssertionHooks())
}
