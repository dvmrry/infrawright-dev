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
	"unicode/utf8"

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
	schemaTypes     func(metadata.LoadedPackRoot) (PlanSchemaTypes, error)
}

func productionSavedPlanAssertionHooks() runSavedPlanAssertionHooks {
	return runSavedPlanAssertionHooks{
		preflightPolicy: PreflightSavedPlanAssessmentPolicy,
		resolveInputs:   ResolveLoadedSavedPlanAssessment,
		assessReport:    AssessSavedPlansReport,
		writeReport:     WriteAssessmentReport,
		guidanceSource:  NewAssessmentGuidanceSource,
		schemaTypes:     NewPlanSchemaTypes,
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
		for _, line := range runnerFindingLines(finding) {
			emit("    - " + line)
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
		SchemaVersion: 2,
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
	var schemaTypes PlanSchemaTypes
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
		// Built for every mode, not only the one that collects guidance: the
		// classifier reads it too, and assert-clean and assert-adoptable
		// disagreeing about what a set is would make the same plan classify two
		// ways depending on which gate ran.
		schemaTypes, err = hooks.schemaTypes(inputs.Root)
		if err == nil && options.Mode == AssertAdoptable {
			guidance := hooks.guidanceSource(inputs.Root)
			guidanceSource = &guidance
		}
	}
	if err == nil {
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
	assessmentOptions.SchemaTypes = schemaTypes
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

// runnerChangeSummary renders one change so a reviewer can name it without
// opening the plan. A redacted change says that it moved and nothing more.
func runnerChangeSummary(change NormalizedPlanChange) string {
	if change.Sensitive {
		return "(sensitive value changed)"
	}
	if change.Kind == string(SetChange) {
		// A set change carries its membership delta instead of a value pair,
		// so there is no before and after to print: it is not one value that
		// moved. An empty delta never reaches here, because a set whose members
		// match produces no change at all.
		return runnerMembershipDelta(change.Added, change.Removed)
	}
	return runnerValueText(change.Before) + " -> " + runnerValueText(change.After)
}

// runnerMembershipDelta renders what entered and left a collection. Shared by
// the schema-typed set change, which is handed its delta, and the positional
// summary below, which reconstructs one, so the two read alike whether or not
// provider schema types were available.
func runnerMembershipDelta(added, removed []any) string {
	parts := make([]string, 0, 2)
	if len(added) > 0 {
		parts = append(parts, fmt.Sprintf("+%d (%s)", len(added), runnerValueList(added)))
	}
	if len(removed) > 0 {
		parts = append(parts, fmt.Sprintf("-%d (%s)", len(removed), runnerValueList(removed)))
	}
	return strings.Join(parts, ", ")
}

// runnerArrayAttribute splits a plan path such as db_categorized_urls[100]
// into its attribute and reports that it addressed an element. Paths that
// name no index -- scalars, block-collection leaves, synthetic markers --
// come back unchanged and are rendered as they always were.
func runnerArrayAttribute(planPath string) (attribute string, indexed bool) {
	if !strings.HasSuffix(planPath, "]") {
		return planPath, false
	}
	open := strings.LastIndex(planPath, "[")
	if open <= 0 {
		return planPath, false
	}
	index := planPath[open+1 : len(planPath)-1]
	if index == "" {
		return planPath, false
	}
	for _, digit := range index {
		if digit < '0' || digit > '9' {
			return planPath, false
		}
	}
	return planPath[:open], true
}

// runnerFindingLines renders a finding's paths, collapsing each array
// attribute to one line that says what entered and left.
//
// Rendering array elements positionally is not merely verbose, it is wrong.
// Inserting one member of a Terraform set shifts every position after it, and a
// positional line then reads
//
//	db_categorized_urls[100]: .904-aladdin.com -> blackrocksinc.com
//
// which asserts that one domain was retargeted to the other. Nothing of the
// sort happened: they are unrelated entries whose positions moved. A real
// edit of two removals and three additions produced 7,643 such lines, each
// making a claim that is not true.
//
// Where the classifier had provider schema types it has already collapsed such
// an attribute to a single SetChange (see PlanSchemaTypes), and the summary
// here is not reached. What remains for this function is the case where it did
// not: an ordered list, or a run with no schema. The multiset delta is true for
// both kinds of attribute, which is why it can be computed without knowing
// which one this is. It is presentation only: Paths keeps every index, so
// gating, policy matching, and the report are untouched -- nothing is
// suppressed, only summarised.
func runnerFindingLines(finding NormalizedAssessmentFinding) []string {
	changes := make(map[string]NormalizedPlanChange, len(finding.Changes))
	for _, change := range finding.Changes {
		changes[change.Path] = change
	}
	// An attribute is only summarised when the finding actually carries values
	// for it. A path with no change -- an older report, an unknown-until-apply
	// entry, a synthetic marker -- has nothing to summarise, and inventing a
	// delta for it would report "same members reordered" about a path we know
	// nothing about.
	summarisable := make(map[string]bool)
	for _, planPath := range finding.Paths {
		attribute, indexed := runnerArrayAttribute(planPath)
		if !indexed {
			continue
		}
		if _, ok := changes[planPath]; ok {
			summarisable[attribute] = true
		}
	}
	lines := make([]string, 0, len(finding.Paths))
	grouped := make(map[string]bool)
	for _, planPath := range finding.Paths {
		attribute, indexed := runnerArrayAttribute(planPath)
		if indexed && summarisable[attribute] {
			if grouped[attribute] {
				continue
			}
			grouped[attribute] = true
			lines = append(lines, runnerArraySummary(attribute, finding.Paths, changes))
			continue
		}
		if change, ok := changes[planPath]; ok {
			lines = append(lines, planPath+": "+runnerChangeSummary(change))
			continue
		}
		lines = append(lines, planPath)
	}
	return lines
}

// runnerArraySummary states what entered and left one array attribute.
//
// The delta is computed from the differing positions alone, which is exact
// rather than approximate: positions that did not differ hold the same value
// on both sides, so they contribute equally to both multisets and cancel.
func runnerArraySummary(
	attribute string,
	paths []string,
	changes map[string]NormalizedPlanChange,
) string {
	before := make([]any, 0)
	after := make([]any, 0)
	positions := 0
	sensitive := false
	for _, planPath := range paths {
		owner, indexed := runnerArrayAttribute(planPath)
		if !indexed || owner != attribute {
			continue
		}
		positions++
		change, ok := changes[planPath]
		if !ok {
			continue
		}
		if change.Sensitive {
			sensitive = true
			continue
		}
		// A padded side carries no member. Counting nil would report null as
		// having been removed when the array simply got shorter.
		if change.Before != nil {
			before = append(before, change.Before)
		}
		if change.After != nil {
			after = append(after, change.After)
		}
	}
	if sensitive {
		return fmt.Sprintf("%s: (sensitive values changed at %d position(s))", attribute, positions)
	}
	added, removed := runnerMultisetDelta(before, after)
	delta := runnerMembershipDelta(added, removed)
	if delta == "" {
		// Same members, different order. Reported rather than suppressed:
		// for an ordered list this is a real change, and nothing here knows
		// whether it is one.
		return fmt.Sprintf("%s: same members reordered (%d position(s) differ)", attribute, positions)
	}
	return fmt.Sprintf("%s: %s across %d differing position(s)", attribute, delta, positions)
}

// runnerMultisetDelta returns the members present only in after and only in
// before, respecting multiplicity.
func runnerMultisetDelta(before, after []any) (added, removed []any) {
	matched := make([]bool, len(before))
	added = make([]any, 0)
	for _, afterValue := range after {
		found := false
		for index, beforeValue := range before {
			if !matched[index] && canonjson.TerraformJSONEqual(beforeValue, afterValue) {
				matched[index] = true
				found = true
				break
			}
		}
		if !found {
			added = append(added, afterValue)
		}
	}
	removed = make([]any, 0)
	for index, beforeValue := range before {
		if !matched[index] {
			removed = append(removed, beforeValue)
		}
	}
	return added, removed
}

// runnerValueList names as many members as fit a readable line and counts the
// rest. A mass edit changes hundreds of members at once, and a line nobody
// reads to the end carries no more than a line that says how many there were.
func runnerValueList(values []any) string {
	shown := len(values)
	if shown > maxRenderedListMembers {
		shown = maxRenderedListMembers
	}
	rendered := make([]string, 0, shown+1)
	for _, value := range values[:shown] {
		rendered = append(rendered, runnerValueText(value))
	}
	if elided := len(values) - shown; elided > 0 {
		rendered = append(rendered, fmt.Sprintf("and %d more", elided))
	}
	return strings.Join(rendered, ", ")
}

// maxRenderedValueRunes bounds one value in the emitted text so a single long
// value cannot blow out the line. It applies only to the console: the report
// carries every value in full, so nothing observed is lost, only shortened in
// one place.
const (
	maxRenderedValueRunes  = 120
	maxRenderedListMembers = 10
)

func runnerValueText(value any) string {
	return runnerTruncate(runnerRenderValue(value))
}

func runnerRenderValue(value any) string {
	if value == nil {
		return "null"
	}
	if text, ok := value.(string); ok {
		return text
	}
	rendered, err := canonjson.Render(value)
	if err != nil {
		return "?"
	}
	return strings.TrimSuffix(rendered, "\n")
}

// runnerTruncate bounds one value so a single pathological member cannot blow
// out the line on its own. It counts runes rather than bytes: cutting a UTF-8
// value mid-rune emits replacement characters, which reads as corrupted data
// in a report whose purpose is letting a reviewer trust what they see.
func runnerTruncate(text string) string {
	if utf8.RuneCountInString(text) <= maxRenderedValueRunes {
		return text
	}
	runes := []rune(text)
	return string(runes[:maxRenderedValueRunes]) + "... (truncated)"
}
