package assessment

import (
	"errors"
	"math/big"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"time"

	"github.com/dvmrry/infrawright-dev/go/internal/artifacts"
	"github.com/dvmrry/infrawright-dev/go/internal/canonjson"
	"github.com/dvmrry/infrawright-dev/go/internal/controlevidence"
	"github.com/dvmrry/infrawright-dev/go/internal/metadata"
	"github.com/dvmrry/infrawright-dev/go/internal/plan"
	"github.com/dvmrry/infrawright-dev/go/internal/procerr"
	"github.com/dvmrry/infrawright-dev/go/internal/terraformcmd"
)

const (
	// MaxSavedPlanAssessmentRoots ports MAX_SAVED_PLAN_ASSESSMENT_ROOTS from
	// the original implementation.
	MaxSavedPlanAssessmentRoots = 1_000
	// MaxRetainedPlanSnapshotBytes ports MAX_RETAINED_PLAN_SNAPSHOT_BYTES.
	MaxRetainedPlanSnapshotBytes int64 = 2 * 1024 * 1024 * 1024
	// MaxSavedPlanAssessmentTimeoutMs ports MAX_SAVED_PLAN_ASSESSMENT_TIMEOUT_MS.
	MaxSavedPlanAssessmentTimeoutMs int64 = 60 * 60 * 1_000
	// MaxSavedPlanAssessmentFindings ports MAX_SAVED_PLAN_ASSESSMENT_FINDINGS.
	MaxSavedPlanAssessmentFindings = 100_000
	// MaxSavedPlanAssessmentPaths ports MAX_SAVED_PLAN_ASSESSMENT_PATHS.
	MaxSavedPlanAssessmentPaths = 250_000
	// MaxSavedPlanAssessmentMetadataBytes ports MAX_SAVED_PLAN_ASSESSMENT_METADATA_BYTES.
	MaxSavedPlanAssessmentMetadataBytes = 8 * 1024 * 1024

	defaultSavedPlanAssessmentTimeoutMs int64 = 10 * 60 * 1_000
)

var (
	assessmentTenantPattern       = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)
	assessmentRootLabelPattern    = regexp.MustCompile(`^[a-z0-9_]+$`)
	assessmentResourceTypePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
)

// SavedPlanAssessmentResultLimits bounds report-safe metadata retained by one
// assessment transaction.
type SavedPlanAssessmentResultLimits struct {
	MaxFindings      int
	MaxPaths         int
	MaxMetadataBytes int
}

// SavedPlanAssessmentTransactionOptions adds the transaction-only ceilings
// from the original implementation to the materialized input contract
// owned by plan-assessment-inputs.ts. Pointer fields preserve the source's
// omitted-versus-explicit-zero distinction.
type SavedPlanAssessmentTransactionOptions struct {
	Assessment SavedPlanAssessmentOptions

	ExpectedPolicySHA256    *string
	HasExpectedPolicySHA256 bool
	SourceLimits            *artifacts.BoundedReadLimits
	SavedPlanLimits         *artifacts.BoundedReadLimits
	PolicyLimits            *artifacts.BoundedReadLimits
	// OperationTimeoutMs is enforced at transaction barriers. An in-flight
	// bounded stable read is allowed to finish; expiry prevents the next pass.
	OperationTimeoutMs       *int64
	MaxRetainedSnapshotBytes *int64
	ResultLimits             *SavedPlanAssessmentResultLimits
}

// SavedPlanAssessmentFailure is a safe failure plus the reportable roots that
// completed before the transaction failed.
type SavedPlanAssessmentFailure struct {
	*procerr.ProcessFailure
	ReportKind AssessmentErrorKind
	Partial    SavedPlanAssessmentCore
	Guidance   []AssessmentGuidanceGroup
}

// Unwrap exposes the shared ProcessFailure spine to errors.As callers.
func (failure *SavedPlanAssessmentFailure) Unwrap() error {
	if failure == nil {
		return nil
	}
	return failure.ProcessFailure
}

// SavedPlanAssessmentReportOutcome pairs the report with the assessment
// failure represented by an error report. Failure is nil on success.
type SavedPlanAssessmentReportOutcome struct {
	Report  SavedPlanAssessmentReport
	Failure *SavedPlanAssessmentFailure
}

// AssessSavedPlansReportOptions supplies one versioned report transaction.
type AssessSavedPlansReportOptions struct {
	Assessment     SavedPlanAssessmentTransactionOptions
	Mode           AssessmentMode
	Request        AssessmentReportRequest
	GuidanceSource *AssessmentGuidanceSource
}

type capturedAssessmentOptions struct {
	assessment                SavedPlanAssessmentOptions
	expectedPolicySHA256      *string
	checkExpectedPolicySHA256 bool
	sourceLimits              artifacts.BoundedReadLimits
	savedPlanLimits           artifacts.BoundedReadLimits
	policyLimits              artifacts.BoundedReadLimits
	terraformShowLimits       *terraformcmd.TerraformShowLimits
	operationTimeoutMs        int64
	maxRetainedSnapshotBytes  int64
	resultLimits              SavedPlanAssessmentResultLimits
}

type assessmentHooks struct {
	now             func() time.Time
	showPlan        func(terraformcmd.TerraformShowOptions) (canonjson.Value, error)
	prepareEvidence func(plan.PrepareSavedPlanEvidenceOptions) (*plan.SavedPlanEvidence, error)
	recheckEvidence func(plan.RecheckSavedPlanEvidenceOptions) error
	cleanupEvidence func(*plan.SavedPlanEvidence) error
	recheckControls func([]controlevidence.BoundAssessmentControlFile) error
	recheckPolicy   func(BoundDriftPolicy, *artifacts.ReadBudget) error
	collectGuidance func(CollectAssessmentGuidanceOptions) AssessmentGuidanceGroup
	makeTemporary   func() (string, error)
	cleanupHooks    assessmentCleanupHooks
}

func productionAssessmentHooks() assessmentHooks {
	return assessmentHooks{
		now:             time.Now,
		showPlan:        terraformcmd.TerraformShowPlan,
		prepareEvidence: plan.PrepareSavedPlanEvidence,
		recheckEvidence: plan.RecheckSavedPlanEvidence,
		cleanupEvidence: plan.CleanupSavedPlanEvidence,
		recheckControls: controlevidence.RecheckAssessmentControlFiles,
		recheckPolicy:   RecheckBoundDriftPolicy,
		collectGuidance: CollectAssessmentGuidance,
		makeTemporary: func() (string, error) {
			return makeAssessmentTemporaryDirectory(os.TempDir())
		},
	}
}

func defaultSavedPlanLimits() artifacts.BoundedReadLimits {
	return artifacts.BoundedReadLimits{
		MaxFiles:            16,
		MaxDirectories:      1,
		MaxDirectoryEntries: 1,
		MaxDepth:            0,
		MaxTotalBytes:       big.NewInt(MaxRetainedPlanSnapshotBytes),
		MaxFileBytes:        big.NewInt(512 * 1024 * 1024),
	}
}

func defaultPolicyLimits() artifacts.BoundedReadLimits {
	return artifacts.BoundedReadLimits{
		MaxFiles:            2,
		MaxDirectories:      1,
		MaxDirectoryEntries: 1,
		MaxDepth:            0,
		MaxTotalBytes:       big.NewInt(32 * 1024 * 1024),
		MaxFileBytes:        big.NewInt(16 * 1024 * 1024),
	}
}

func defaultAssessmentResultLimits() SavedPlanAssessmentResultLimits {
	return SavedPlanAssessmentResultLimits{
		MaxFindings:      MaxSavedPlanAssessmentFindings,
		MaxPaths:         MaxSavedPlanAssessmentPaths,
		MaxMetadataBytes: MaxSavedPlanAssessmentMetadataBytes,
	}
}

func cloneBoundedReadLimits(limits artifacts.BoundedReadLimits) artifacts.BoundedReadLimits {
	result := limits
	if limits.MaxTotalBytes != nil {
		result.MaxTotalBytes = new(big.Int).Set(limits.MaxTotalBytes)
	}
	if limits.MaxFileBytes != nil {
		result.MaxFileBytes = new(big.Int).Set(limits.MaxFileBytes)
	}
	return result
}

func boundedLimitsWithin(limits, maximum artifacts.BoundedReadLimits) bool {
	return limits.MaxFiles > 0 && limits.MaxDirectories > 0 &&
		limits.MaxDirectoryEntries > 0 && limits.MaxDepth >= 0 &&
		limits.MaxTotalBytes != nil && limits.MaxFileBytes != nil &&
		limits.MaxTotalBytes.Sign() > 0 && limits.MaxFileBytes.Sign() > 0 &&
		limits.MaxFiles <= maximum.MaxFiles &&
		limits.MaxDirectories <= maximum.MaxDirectories &&
		limits.MaxDirectoryEntries <= maximum.MaxDirectoryEntries &&
		limits.MaxDepth <= maximum.MaxDepth &&
		limits.MaxTotalBytes.Cmp(maximum.MaxTotalBytes) <= 0 &&
		limits.MaxFileBytes.Cmp(maximum.MaxFileBytes) <= 0
}

func cloneOptionalStrings(values []string) []string {
	if values == nil {
		return nil
	}
	return append([]string{}, values...)
}

func cloneAssessmentRoots(values []SavedPlanAssessmentRootInput) []SavedPlanAssessmentRootInput {
	result := make([]SavedPlanAssessmentRootInput, len(values))
	for index, root := range values {
		result[index] = SavedPlanAssessmentRootInput{
			Tenant:               root.Tenant,
			Label:                root.Label,
			Members:              append([]string{}, root.Members...),
			EnvDir:               root.EnvDir,
			SavedPlanPath:        root.SavedPlanPath,
			FingerprintPath:      root.FingerprintPath,
			VarFiles:             append([]string{}, root.VarFiles...),
			ReferenceOutputTypes: cloneOptionalStrings(root.ReferenceOutputTypes),
		}
	}
	sort.SliceStable(result, func(left, right int) bool {
		return result[left].Tenant+"\x00"+result[left].Label <
			result[right].Tenant+"\x00"+result[right].Label
	})
	return result
}

func captureAssessmentOptions(
	options SavedPlanAssessmentTransactionOptions,
) (capturedAssessmentOptions, error) {
	base := options.Assessment
	controls, err := controlevidence.CopyAssessmentControlFiles(base.ControlFiles)
	if err != nil {
		return capturedAssessmentOptions{}, err
	}
	capturedBase := SavedPlanAssessmentOptions{
		TerraformExecutable: base.TerraformExecutable,
		Roots:               cloneAssessmentRoots(base.Roots),
		BackendConfig:       cloneString(base.BackendConfig),
		PolicyPath:          cloneString(base.PolicyPath),
		ControlFiles:        controls,
	}
	if base.Context != nil {
		context := CopySavedPlanAssessmentContext(*base.Context)
		capturedBase.Context = &context
	}
	if base.LoadedContext != nil {
		context := CopyLoadedSavedPlanAssessmentContext(*base.LoadedContext)
		capturedBase.LoadedContext = &context
	}
	if base.TerraformShowLimits != nil {
		limits := *base.TerraformShowLimits
		capturedBase.TerraformShowLimits = &limits
	}

	result := capturedAssessmentOptions{
		assessment:                capturedBase,
		expectedPolicySHA256:      cloneString(options.ExpectedPolicySHA256),
		checkExpectedPolicySHA256: options.HasExpectedPolicySHA256 || options.ExpectedPolicySHA256 != nil,
		sourceLimits:              artifacts.DefaultBoundedReadLimits(),
		savedPlanLimits:           defaultSavedPlanLimits(),
		policyLimits:              defaultPolicyLimits(),
		operationTimeoutMs:        defaultSavedPlanAssessmentTimeoutMs,
		maxRetainedSnapshotBytes:  MaxRetainedPlanSnapshotBytes,
		resultLimits:              defaultAssessmentResultLimits(),
	}
	if options.SourceLimits != nil {
		result.sourceLimits = cloneBoundedReadLimits(*options.SourceLimits)
	}
	if options.SavedPlanLimits != nil {
		result.savedPlanLimits = cloneBoundedReadLimits(*options.SavedPlanLimits)
	}
	if options.PolicyLimits != nil {
		result.policyLimits = cloneBoundedReadLimits(*options.PolicyLimits)
	}
	if options.OperationTimeoutMs != nil {
		result.operationTimeoutMs = *options.OperationTimeoutMs
	}
	if options.MaxRetainedSnapshotBytes != nil {
		result.maxRetainedSnapshotBytes = *options.MaxRetainedSnapshotBytes
	}
	if options.ResultLimits != nil {
		result.resultLimits = *options.ResultLimits
	}
	result.terraformShowLimits = capturedBase.TerraformShowLimits
	return result, nil
}

func validAssessmentTenant(value string) bool {
	return value != "." && value != ".." && assessmentTenantPattern.MatchString(value)
}

func uniqueStrings(values []string) bool {
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if _, exists := seen[value]; exists {
			return false
		}
		seen[value] = struct{}{}
	}
	return true
}

func validateAssessmentRoots(roots []SavedPlanAssessmentRootInput) error {
	if len(roots) > MaxSavedPlanAssessmentRoots {
		return assessmentDomainFailure(
			"TOO_MANY_SAVED_PLANS",
			"saved-plan assessment exceeds the root-count limit",
		)
	}
	seen := make(map[string]struct{}, len(roots))
	for _, root := range roots {
		if len(root.Members) != 1 || root.Members[0] != root.Label {
			return assessmentDomainFailure(
				"INVALID_ASSESSMENT_ROOT",
				"saved-plan root must contain exactly one member matching its label",
			)
		}
		valid := validAssessmentTenant(root.Tenant) &&
			assessmentRootLabelPattern.MatchString(root.Label) &&
			len(root.Members) > 0 && uniqueStrings(root.Members) &&
			filepath.IsAbs(root.EnvDir) && filepath.IsAbs(root.SavedPlanPath) &&
			filepath.IsAbs(root.FingerprintPath)
		for _, member := range root.Members {
			valid = valid && assessmentResourceTypePattern.MatchString(member)
		}
		for _, file := range root.VarFiles {
			valid = valid && filepath.IsAbs(file)
		}
		if root.ReferenceOutputTypes != nil {
			valid = valid && len(root.ReferenceOutputTypes) > 0 &&
				uniqueStrings(root.ReferenceOutputTypes)
			memberSet := make(map[string]struct{}, len(root.Members))
			for _, member := range root.Members {
				memberSet[member] = struct{}{}
			}
			for index, resourceType := range root.ReferenceOutputTypes {
				_, member := memberSet[resourceType]
				valid = valid && member && assessmentResourceTypePattern.MatchString(resourceType)
				if index > 0 && canonjson.ComparePythonStrings(root.ReferenceOutputTypes[index-1], resourceType) >= 0 {
					valid = false
				}
			}
		}
		if !valid {
			return assessmentDomainFailure("INVALID_ASSESSMENT_ROOT", "saved-plan root input is invalid")
		}
		key := root.Tenant + "\x00" + root.Label
		if _, duplicate := seen[key]; duplicate {
			return assessmentDomainFailure(
				"DUPLICATE_ASSESSMENT_ROOT",
				"saved-plan root was selected more than once",
			)
		}
		seen[key] = struct{}{}
	}
	return nil
}

func validateCapturedAssessmentLimits(options capturedAssessmentOptions) error {
	if err := validateAssessmentRoots(options.assessment.Roots); err != nil {
		return err
	}
	limits := options.resultLimits
	if options.operationTimeoutMs <= 0 ||
		options.operationTimeoutMs > MaxSavedPlanAssessmentTimeoutMs ||
		options.maxRetainedSnapshotBytes <= 0 ||
		options.maxRetainedSnapshotBytes > MaxRetainedPlanSnapshotBytes ||
		limits.MaxFindings <= 0 || limits.MaxFindings > MaxSavedPlanAssessmentFindings ||
		limits.MaxPaths <= 0 || limits.MaxPaths > MaxSavedPlanAssessmentPaths ||
		limits.MaxMetadataBytes <= 0 || limits.MaxMetadataBytes > MaxSavedPlanAssessmentMetadataBytes {
		return assessmentDomainFailure("INVALID_ASSESSMENT_LIMIT", "saved-plan assessment limits are invalid")
	}
	return nil
}

func validateCapturedAssessmentPathsAndReadLimits(options capturedAssessmentOptions) error {
	base := options.assessment
	if !filepath.IsAbs(base.TerraformExecutable) ||
		(base.BackendConfig != nil && !filepath.IsAbs(*base.BackendConfig)) ||
		(base.PolicyPath != nil && !filepath.IsAbs(*base.PolicyPath)) {
		return assessmentDomainFailure(
			"UNRESOLVED_ASSESSMENT_PATH",
			"saved-plan assessment paths must be absolute",
		)
	}
	if !boundedLimitsWithin(options.sourceLimits, artifacts.DefaultBoundedReadLimits()) ||
		!boundedLimitsWithin(options.savedPlanLimits, defaultSavedPlanLimits()) ||
		!boundedLimitsWithin(options.policyLimits, defaultPolicyLimits()) {
		return assessmentDomainFailure(
			"INVALID_ASSESSMENT_LIMIT",
			"saved-plan read limits cannot exceed the hard transaction ceilings",
		)
	}
	return nil
}

func newAssessmentBudget(limits artifacts.BoundedReadLimits) (*artifacts.ReadBudget, error) {
	return artifacts.NewReadBudget(cloneBoundedReadLimits(limits))
}

func assessmentRemainingTime(deadline time.Time, now func() time.Time) (time.Duration, error) {
	remaining := deadline.Sub(now())
	if remaining <= 0 {
		return 0, assessmentDomainFailure(
			"ASSESSMENT_TIMEOUT",
			"saved-plan assessment exceeded its execution deadline",
		)
	}
	return remaining, nil
}

func recheckAssessmentContext(
	options capturedAssessmentOptions,
	deadline time.Time,
	hooks assessmentHooks,
) error {
	checkDeadline := func() error {
		_, err := assessmentRemainingTime(deadline, hooks.now)
		return err
	}
	if err := checkDeadline(); err != nil {
		return err
	}
	if err := hooks.recheckControls(options.assessment.ControlFiles); err != nil {
		return err
	}
	// The control-evidence primitive may perform up to 128 MiB of physical I/O
	// for a 64-MiB logical set. Recheck the transaction deadline before another
	// potentially maximum-sized pass so an expired transaction stays bounded.
	if err := checkDeadline(); err != nil {
		return err
	}
	if options.assessment.Context != nil {
		if err := RecheckSavedPlanAssessmentContext(
			*options.assessment.Context,
			options.assessment.Roots,
		); err != nil {
			return err
		}
	}
	if err := checkDeadline(); err != nil {
		return err
	}
	if options.assessment.LoadedContext != nil {
		if err := RecheckLoadedSavedPlanAssessmentContext(
			*options.assessment.LoadedContext,
			options.assessment.Roots,
		); err != nil {
			return err
		}
	}
	if err := checkDeadline(); err != nil {
		return err
	}
	if err := hooks.recheckControls(options.assessment.ControlFiles); err != nil {
		return err
	}
	if err := checkDeadline(); err != nil {
		return err
	}
	if options.assessment.Context != nil {
		if err := RecheckSavedPlanAssessmentContext(
			*options.assessment.Context,
			options.assessment.Roots,
		); err != nil {
			return err
		}
	}
	if err := checkDeadline(); err != nil {
		return err
	}
	if options.assessment.LoadedContext != nil {
		if err := RecheckLoadedSavedPlanAssessmentContext(
			*options.assessment.LoadedContext,
			options.assessment.Roots,
		); err != nil {
			return err
		}
	}
	return nil
}

// PreflightSavedPlanAssessmentPolicy binds policy before topology or
// Terraform lookup, using the source-default policy ceiling.
func PreflightSavedPlanAssessmentPolicy(policyPath *string) (BoundDriftPolicy, error) {
	budget, err := newAssessmentBudget(defaultPolicyLimits())
	if err != nil {
		return BoundDriftPolicy{}, err
	}
	return LoadBoundDriftPolicy(policyPath, budget)
}

func assessmentShowLimits(
	configured *terraformcmd.TerraformShowLimits,
	deadline time.Time,
	now func() time.Time,
) (*terraformcmd.TerraformShowLimits, error) {
	limits := terraformcmd.DefaultTerraformShowLimits()
	if configured != nil {
		limits = *configured
	}
	remaining, err := assessmentRemainingTime(deadline, now)
	if err != nil {
		return nil, err
	}
	remainingMs := remaining.Milliseconds()
	if remainingMs < 1 {
		remainingMs = 1
	}
	if limits.TimeoutMs > remainingMs {
		limits.TimeoutMs = remainingMs
	}
	if limits.TimeoutMs < 1 {
		limits.TimeoutMs = 1
	}
	return &limits, nil
}

func assessmentMetadata(planObject map[string]any, field string) *string {
	value, ok := planObject[field].(string)
	if !ok {
		return nil
	}
	return &value
}

func assessmentResourceTypes(planObject map[string]any) map[string]string {
	result := make(map[string]string)
	for _, source := range []string{"resource_changes", "resource_drift"} {
		records, _ := planObject[source].([]any)
		for _, rawRecord := range records {
			record, ok := rawRecord.(map[string]any)
			if !ok {
				continue
			}
			address, addressOK := record["address"].(string)
			resourceType, typeOK := record["type"].(string)
			if addressOK && typeOK {
				result[source+"\x00"+address] = resourceType
			}
		}
	}
	return result
}

func attachAssessmentResourceTypes(
	planObject map[string]any,
	findings []PlanFinding,
) []AssessmentFinding {
	types := assessmentResourceTypes(planObject)
	result := make([]AssessmentFinding, len(findings))
	for index, finding := range findings {
		var resourceType *string
		if value, ok := types[finding.Source+"\x00"+finding.Address]; ok {
			copy := value
			resourceType = &copy
		}
		result[index] = AssessmentFinding{
			Status:       finding.Status,
			Source:       finding.Source,
			Address:      finding.Address,
			ResourceType: resourceType,
			Actions:      append([]string{}, finding.Actions...),
			Paths:        clonePaths(finding.Paths),
		}
	}
	return result
}

func assessmentFindingMetadataBytes(finding AssessmentFinding) int {
	bytes := len([]byte(finding.Source)) + len([]byte(finding.Address))
	if finding.ResourceType != nil {
		bytes += len([]byte(*finding.ResourceType))
	}
	for _, action := range finding.Actions {
		bytes += len([]byte(action))
	}
	for _, path := range finding.Paths {
		for _, segment := range path {
			switch value := segment.(type) {
			case string:
				bytes += len([]byte(value))
			case int:
				bytes += len(strconv.Itoa(value))
			}
		}
	}
	return bytes
}

func assessmentTotalStatus(clean, tolerated, blocked int) PlanStatus {
	if blocked > 0 {
		return Blocked
	}
	if tolerated > 0 {
		return Tolerated
	}
	return Clean
}

func buildAssessmentCore(
	roots []AssessedSavedPlanRoot,
	policySHA256 *string,
	stalePolicy []metadata.StalePolicyEntry,
) SavedPlanAssessmentCore {
	result := SavedPlanAssessmentCore{
		PolicySHA256: cloneString(policySHA256),
		Roots:        cloneAssessedRoots(roots),
		StalePolicy:  append([]metadata.StalePolicyEntry{}, stalePolicy...),
	}
	result.Checked = len(result.Roots)
	for _, root := range result.Roots {
		switch root.Status {
		case Clean:
			result.Clean++
		case Tolerated:
			result.Tolerated++
		case Blocked:
			result.Blocked++
		}
	}
	result.Status = assessmentTotalStatus(result.Clean, result.Tolerated, result.Blocked)
	return result
}

func cloneAssessedRoots(values []AssessedSavedPlanRoot) []AssessedSavedPlanRoot {
	result := make([]AssessedSavedPlanRoot, len(values))
	for index, root := range values {
		result[index] = AssessedSavedPlanRoot{
			Tenant:  root.Tenant,
			Label:   root.Label,
			Members: append([]string{}, root.Members...),
			Status:  root.Status,
			Plan: AssessedPlanEvidence{
				SHA256:           root.Plan.SHA256,
				FormatVersion:    cloneString(root.Plan.FormatVersion),
				TerraformVersion: cloneString(root.Plan.TerraformVersion),
			},
			PlanFingerprint: root.PlanFingerprint,
			Findings:        cloneAssessmentFindings(root.Findings),
		}
	}
	return result
}

func cloneAssessmentFindings(values []AssessmentFinding) []AssessmentFinding {
	result := make([]AssessmentFinding, len(values))
	for index, finding := range values {
		result[index] = AssessmentFinding{
			Status:       finding.Status,
			Source:       finding.Source,
			Address:      finding.Address,
			ResourceType: cloneString(finding.ResourceType),
			Actions:      append([]string{}, finding.Actions...),
			Paths:        clonePaths(finding.Paths),
		}
	}
	return result
}

func cloneAssessmentGuidance(values []AssessmentGuidanceGroup) []AssessmentGuidanceGroup {
	result := make([]AssessmentGuidanceGroup, len(values))
	for index, group := range values {
		result[index] = AssessmentGuidanceGroup{
			Tenant:  group.Tenant,
			Label:   group.Label,
			Entries: make([]map[string]any, len(group.Entries)),
		}
		for entryIndex, entry := range group.Entries {
			result[index].Entries[entryIndex] = cloneGuidanceRecord(entry)
		}
	}
	return result
}

func safeAssessmentFailure(err error) *procerr.ProcessFailure {
	var failure *procerr.ProcessFailure
	if errors.As(err, &failure) {
		return procerr.NewProcessFailure(procerr.NewProcessFailureOptions{
			Code:      failure.Code,
			Category:  failure.Category,
			Message:   failure.Message,
			Retryable: failure.Retryable,
			Details:   append([]procerr.ErrorDetail{}, failure.Details...),
		})
	}
	var planFailure *plan.AssessmentPlanError
	if errors.As(err, &planFailure) {
		return procerr.NewProcessFailure(procerr.NewProcessFailureOptions{
			Code:     "INVALID_ASSESSMENT_PLAN",
			Category: procerr.CategoryDomain,
			Message:  "saved plan is outside the supported assessment contract",
		})
	}
	return procerr.NewProcessFailure(procerr.NewProcessFailureOptions{
		Code:     "ASSESSMENT_FAILED",
		Category: procerr.CategoryInternal,
		Message:  "saved-plan assessment failed",
	})
}

func assessmentWithCleanupDetail(
	failure, cleanup *procerr.ProcessFailure,
) *procerr.ProcessFailure {
	details := append([]procerr.ErrorDetail{}, failure.Details...)
	details = append(details, procerr.ErrorDetail{
		Path:    "/",
		Code:    cleanup.Code,
		Message: "private assessment cleanup also failed",
	})
	return procerr.NewProcessFailure(procerr.NewProcessFailureOptions{
		Code:      failure.Code,
		Category:  failure.Category,
		Message:   failure.Message,
		Retryable: failure.Retryable,
		Details:   details,
	})
}

func safeCollectAssessmentGuidance(
	hook func(CollectAssessmentGuidanceOptions) AssessmentGuidanceGroup,
	options CollectAssessmentGuidanceOptions,
) (group AssessmentGuidanceGroup) {
	group = AssessmentGuidanceGroup{Tenant: options.Tenant, Label: options.Label, Entries: []map[string]any{}}
	defer func() {
		if recover() != nil {
			group = AssessmentGuidanceGroup{
				Tenant:  options.Tenant,
				Label:   options.Label,
				Entries: []map[string]any{},
			}
		}
	}()
	return hook(options)
}

func asyncAssessmentFinalizerValue(value any) bool {
	if value == nil {
		return false
	}
	reflected := reflect.ValueOf(value)
	for reflected.IsValid() && (reflected.Kind() == reflect.Interface || reflected.Kind() == reflect.Pointer) {
		if reflected.IsNil() {
			return false
		}
		reflected = reflected.Elem()
	}
	if !reflected.IsValid() {
		return false
	}
	switch reflected.Kind() {
	case reflect.Chan:
		return true
	case reflect.Map:
		if reflected.Type().Key().Kind() == reflect.String {
			return reflected.MapIndex(reflect.ValueOf("then").Convert(reflected.Type().Key())).IsValid()
		}
	case reflect.Struct:
		return reflected.FieldByName("Then").IsValid()
	}
	return false
}

func runSavedPlanAssessment[T any](
	options SavedPlanAssessmentTransactionOptions,
	finalize func(SavedPlanAssessmentCore, []AssessmentGuidanceGroup) (T, error),
	guidanceSource *AssessmentGuidanceSource,
	hooks assessmentHooks,
) (completed T, returnedErr error) {
	assessed := make([]AssessedSavedPlanRoot, 0)
	guidance := make([]AssessmentGuidanceGroup, 0)
	stalePolicy := make([]metadata.StalePolicyEntry, 0)
	var policySHA256 *string
	reportKind := AssessmentError
	var captured capturedAssessmentOptions
	var temporary string
	var temporaryIdentity assessmentCleanupIdentity
	temporarySnapshots := make([]assessmentCleanupSnapshot, 0)
	evidence := make([]*plan.SavedPlanEvidence, 0)
	hasCompleted := false
	var primaryFailure *procerr.ProcessFailure

	defer func() {
		if recovered := recover(); recovered != nil {
			if recoveredErr, ok := recovered.(error); ok {
				primaryFailure = safeAssessmentFailure(recoveredErr)
			} else {
				primaryFailure = safeAssessmentFailure(errors.New("assessment panicked"))
			}
		}
		var cleanupFailure *procerr.ProcessFailure
		for _, capturedEvidence := range evidence {
			if err := hooks.cleanupEvidence(capturedEvidence); err != nil && cleanupFailure == nil {
				cleanupFailure = safeAssessmentFailure(err)
			}
		}
		if temporary != "" && cleanupFailure == nil {
			cleanupFailure = cleanupAssessmentTemporaryDirectory(
				temporary,
				temporaryIdentity,
				temporarySnapshots,
				hooks.cleanupHooks,
			)
		}
		if cleanupFailure != nil {
			if primaryFailure == nil {
				primaryFailure = cleanupFailure
			} else {
				primaryFailure = assessmentWithCleanupDetail(primaryFailure, cleanupFailure)
			}
		}
		if primaryFailure != nil {
			var zero T
			completed = zero
			if primaryFailure.Code == "NO_SAVED_PLANS" {
				reportKind = NoSavedPlans
			}
			returnedErr = &SavedPlanAssessmentFailure{
				ProcessFailure: primaryFailure,
				ReportKind:     reportKind,
				Partial:        buildAssessmentCore(assessed, policySHA256, stalePolicy),
				Guidance:       cloneAssessmentGuidance(guidance),
			}
			return
		}
		if !hasCompleted {
			var zero T
			completed = zero
			returnedErr = &SavedPlanAssessmentFailure{
				ProcessFailure: safeAssessmentFailure(errors.New("assessment did not complete")),
				ReportKind:     AssessmentError,
				Partial:        buildAssessmentCore(assessed, policySHA256, stalePolicy),
				Guidance:       cloneAssessmentGuidance(guidance),
			}
		}
	}()

	if len(options.Assessment.Roots) > MaxSavedPlanAssessmentRoots {
		primaryFailure = assessmentDomainFailure(
			"TOO_MANY_SAVED_PLANS",
			"saved-plan assessment exceeds the root-count limit",
		)
		return completed, nil
	}
	var err error
	captured, err = captureAssessmentOptions(options)
	if err != nil {
		primaryFailure = safeAssessmentFailure(err)
		return completed, nil
	}
	if err := validateCapturedAssessmentLimits(captured); err != nil {
		primaryFailure = safeAssessmentFailure(err)
		return completed, nil
	}
	deadline := hooks.now().Add(time.Duration(captured.operationTimeoutMs) * time.Millisecond)
	if err := validateCapturedAssessmentPathsAndReadLimits(captured); err != nil {
		primaryFailure = safeAssessmentFailure(err)
		return completed, nil
	}

	reportKind = PolicyError
	policyBudget, err := newAssessmentBudget(captured.policyLimits)
	if err != nil {
		primaryFailure = safeAssessmentFailure(err)
		return completed, nil
	}
	boundPolicy, err := LoadBoundDriftPolicy(captured.assessment.PolicyPath, policyBudget)
	if err != nil {
		var policyFailure *DriftPolicyLoadFailure
		if errors.As(err, &policyFailure) {
			sha := policyFailure.File.SHA256
			policySHA256 = &sha
		}
		primaryFailure = safeAssessmentFailure(err)
		return completed, nil
	}
	if boundPolicy.File != nil {
		sha := boundPolicy.File.SHA256
		policySHA256 = &sha
	}
	reportKind = AssessmentError
	if captured.checkExpectedPolicySHA256 &&
		((captured.expectedPolicySHA256 == nil) != (policySHA256 == nil) ||
			(captured.expectedPolicySHA256 != nil && policySHA256 != nil &&
				*captured.expectedPolicySHA256 != *policySHA256)) {
		primaryFailure = assessmentDomainFailure(
			"DRIFT_POLICY_CHANGED",
			"saved-plan drift policy changed during assessment",
		)
		return completed, nil
	}
	if _, err := assessmentRemainingTime(deadline, hooks.now); err != nil {
		primaryFailure = safeAssessmentFailure(err)
		return completed, nil
	}
	if err := recheckAssessmentContext(captured, deadline, hooks); err != nil {
		primaryFailure = safeAssessmentFailure(err)
		return completed, nil
	}
	if _, err := assessmentRemainingTime(deadline, hooks.now); err != nil {
		primaryFailure = safeAssessmentFailure(err)
		return completed, nil
	}
	if len(captured.assessment.Roots) == 0 {
		primaryFailure = assessmentDomainFailure(
			"NO_SAVED_PLANS",
			"no saved plans to check - run make plan SAVE=1 first",
		)
		reportKind = NoSavedPlans
		return completed, nil
	}
	temporary, err = hooks.makeTemporary()
	if err != nil {
		primaryFailure = safeAssessmentFailure(err)
		return completed, nil
	}
	temporaryIdentity, err = directorySafeIdentity(temporary)
	if err != nil {
		primaryFailure = safeAssessmentFailure(err)
		return completed, nil
	}

	retainedSnapshotBytes := int64(0)
	findingCount := 0
	findingPathCount := 0
	findingMetadataBytes := 0
	for _, root := range captured.assessment.Roots {
		if _, err := assessmentRemainingTime(deadline, hooks.now); err != nil {
			primaryFailure = safeAssessmentFailure(err)
			return completed, nil
		}
		remainingSnapshotBytes := captured.maxRetainedSnapshotBytes - retainedSnapshotBytes
		if remainingSnapshotBytes <= 0 {
			primaryFailure = assessmentDomainFailure(
				"PLAN_SNAPSHOT_BUDGET_EXCEEDED",
				"saved-plan snapshots exceed the transaction byte limit",
			)
			return completed, nil
		}
		captureLimits := cloneBoundedReadLimits(captured.savedPlanLimits)
		if captureLimits.MaxFileBytes.Cmp(big.NewInt(remainingSnapshotBytes)) > 0 {
			captureLimits.MaxFileBytes = big.NewInt(remainingSnapshotBytes)
		}
		fingerprintBudget, budgetErr := newAssessmentBudget(captured.sourceLimits)
		if budgetErr != nil {
			primaryFailure = safeAssessmentFailure(budgetErr)
			return completed, nil
		}
		savedPlanBudget, budgetErr := newAssessmentBudget(captureLimits)
		if budgetErr != nil {
			primaryFailure = safeAssessmentFailure(budgetErr)
			return completed, nil
		}
		backendKey := (*string)(nil)
		if captured.assessment.BackendConfig != nil {
			value := root.Tenant + "/" + root.Label + ".tfstate"
			backendKey = &value
		}
		capturedEvidence, err := hooks.prepareEvidence(plan.PrepareSavedPlanEvidenceOptions{
			SavedPlanPath:   root.SavedPlanPath,
			FingerprintPath: root.FingerprintPath,
			FingerprintInput: plan.PlanFingerprintInput{
				EnvDir:        root.EnvDir,
				VarFiles:      append([]string{}, root.VarFiles...),
				MemberTypes:   append([]string{}, root.Members...),
				BackendConfig: cloneString(captured.assessment.BackendConfig),
				BackendKey:    backendKey,
			},
			SnapshotDirectory: temporary,
			FingerprintBudget: fingerprintBudget,
			SavedPlanBudget:   savedPlanBudget,
		})
		if err != nil {
			primaryFailure = safeAssessmentFailure(err)
			return completed, nil
		}
		evidence = append(evidence, capturedEvidence)
		cleanupSnapshot, bindingErr := assessmentSnapshotCleanupBinding(
			temporary,
			capturedEvidence.Snapshot,
		)
		if bindingErr != nil {
			primaryFailure = safeAssessmentFailure(bindingErr)
			return completed, nil
		}
		temporarySnapshots = append(temporarySnapshots, cleanupSnapshot)
		retainedSnapshotBytes += capturedEvidence.Snapshot.Size
		if retainedSnapshotBytes > captured.maxRetainedSnapshotBytes {
			primaryFailure = assessmentDomainFailure(
				"PLAN_SNAPSHOT_BUDGET_EXCEEDED",
				"saved-plan snapshots exceed the transaction byte limit",
			)
			return completed, nil
		}
		showLimits, err := assessmentShowLimits(captured.terraformShowLimits, deadline, hooks.now)
		if err != nil {
			primaryFailure = safeAssessmentFailure(err)
			return completed, nil
		}
		planValue, err := hooks.showPlan(terraformcmd.TerraformShowOptions{
			TerraformExecutable: captured.assessment.TerraformExecutable,
			EnvDir:              root.EnvDir,
			SnapshotPath:        capturedEvidence.Snapshot.Path,
			Limits:              showLimits,
		})
		if err != nil {
			primaryFailure = safeAssessmentFailure(err)
			return completed, nil
		}
		if _, err := assessmentRemainingTime(deadline, hooks.now); err != nil {
			primaryFailure = safeAssessmentFailure(err)
			return completed, nil
		}
		if err := hooks.recheckControls(captured.assessment.ControlFiles); err != nil {
			primaryFailure = safeAssessmentFailure(err)
			return completed, nil
		}
		if _, err := assessmentRemainingTime(deadline, hooks.now); err != nil {
			primaryFailure = safeAssessmentFailure(err)
			return completed, nil
		}
		fingerprintBudget, budgetErr = newAssessmentBudget(captured.sourceLimits)
		if budgetErr != nil {
			primaryFailure = safeAssessmentFailure(budgetErr)
			return completed, nil
		}
		savedPlanBudget, budgetErr = newAssessmentBudget(captured.savedPlanLimits)
		if budgetErr != nil {
			primaryFailure = safeAssessmentFailure(budgetErr)
			return completed, nil
		}
		if err := hooks.recheckEvidence(plan.RecheckSavedPlanEvidenceOptions{
			Evidence:          capturedEvidence,
			FingerprintBudget: fingerprintBudget,
			SavedPlanBudget:   savedPlanBudget,
		}); err != nil {
			primaryFailure = safeAssessmentFailure(err)
			return completed, nil
		}
		contract := (*plan.AssessmentPlanContract)(nil)
		if root.ReferenceOutputTypes != nil {
			contract = &plan.AssessmentPlanContract{
				ReferenceOutputTypes: append([]string{}, root.ReferenceOutputTypes...),
			}
		}
		classification, err := ClassifyPlan(planValue, boundPolicy.Policy, contract)
		if err != nil {
			primaryFailure = safeAssessmentFailure(err)
			return completed, nil
		}
		if _, err := assessmentRemainingTime(deadline, hooks.now); err != nil {
			primaryFailure = safeAssessmentFailure(err)
			return completed, nil
		}
		planObject, ok := planValue.(map[string]any)
		if !ok {
			primaryFailure = assessmentDomainFailure(
				"INVALID_ASSESSMENT_PLAN",
				"Terraform show did not emit a plan object",
			)
			return completed, nil
		}
		fingerprintBudget, budgetErr = newAssessmentBudget(captured.sourceLimits)
		if budgetErr != nil {
			primaryFailure = safeAssessmentFailure(budgetErr)
			return completed, nil
		}
		savedPlanBudget, budgetErr = newAssessmentBudget(captured.savedPlanLimits)
		if budgetErr != nil {
			primaryFailure = safeAssessmentFailure(budgetErr)
			return completed, nil
		}
		if err := hooks.recheckEvidence(plan.RecheckSavedPlanEvidenceOptions{
			Evidence:          capturedEvidence,
			FingerprintBudget: fingerprintBudget,
			SavedPlanBudget:   savedPlanBudget,
		}); err != nil {
			primaryFailure = safeAssessmentFailure(err)
			return completed, nil
		}
		findings := attachAssessmentResourceTypes(planObject, classification.Findings)
		findingCount += len(findings)
		for _, finding := range findings {
			findingPathCount += len(finding.Paths)
			findingMetadataBytes += assessmentFindingMetadataBytes(finding)
		}
		if findingCount > captured.resultLimits.MaxFindings ||
			findingPathCount > captured.resultLimits.MaxPaths ||
			findingMetadataBytes > captured.resultLimits.MaxMetadataBytes {
			primaryFailure = assessmentDomainFailure(
				"ASSESSMENT_RESULT_LIMIT_EXCEEDED",
				"saved-plan assessment exceeds the report metadata limit",
			)
			return completed, nil
		}
		members := append([]string{}, root.Members...)
		sort.SliceStable(members, func(left, right int) bool {
			return canonjson.ComparePythonStrings(members[left], members[right]) < 0
		})
		assessed = append(assessed, AssessedSavedPlanRoot{
			Tenant:  root.Tenant,
			Label:   root.Label,
			Members: members,
			Status:  classification.Status,
			Plan: AssessedPlanEvidence{
				SHA256:           capturedEvidence.OriginalPlan.SHA256,
				FormatVersion:    assessmentMetadata(planObject, "format_version"),
				TerraformVersion: assessmentMetadata(planObject, "terraform_version"),
			},
			PlanFingerprint: capturedEvidence.FingerprintFile.Fingerprint,
			Findings:        findings,
		})
		if classification.Status == Blocked && guidanceSource != nil {
			guidance = append(guidance, safeCollectAssessmentGuidance(
				hooks.collectGuidance,
				CollectAssessmentGuidanceOptions{
					Source:   *guidanceSource,
					Tenant:   root.Tenant,
					Label:    root.Label,
					Members:  append([]string{}, root.Members...),
					Plan:     planObject,
					Findings: append([]PlanFinding{}, classification.Findings...),
				},
			))
		}
	}

	checkedTypes := make(map[string]struct{})
	for _, root := range captured.assessment.Roots {
		for _, member := range root.Members {
			checkedTypes[member] = struct{}{}
		}
	}
	stalePolicy = boundPolicy.Policy.StaleEntries(metadata.StaleEntriesOptions{
		ResourceTypes: checkedTypes,
		Modes:         []metadata.PolicyMode{metadata.PolicyPlanTolerate},
	})
	for pass := 0; pass < 2; pass++ {
		for _, capturedEvidence := range evidence {
			if _, err := assessmentRemainingTime(deadline, hooks.now); err != nil {
				primaryFailure = safeAssessmentFailure(err)
				return completed, nil
			}
			fingerprintBudget, budgetErr := newAssessmentBudget(captured.sourceLimits)
			if budgetErr != nil {
				primaryFailure = safeAssessmentFailure(budgetErr)
				return completed, nil
			}
			savedPlanBudget, budgetErr := newAssessmentBudget(captured.savedPlanLimits)
			if budgetErr != nil {
				primaryFailure = safeAssessmentFailure(budgetErr)
				return completed, nil
			}
			if err := hooks.recheckEvidence(plan.RecheckSavedPlanEvidenceOptions{
				Evidence:          capturedEvidence,
				FingerprintBudget: fingerprintBudget,
				SavedPlanBudget:   savedPlanBudget,
			}); err != nil {
				primaryFailure = safeAssessmentFailure(err)
				return completed, nil
			}
		}
		policyBudget, budgetErr := newAssessmentBudget(captured.policyLimits)
		if budgetErr != nil {
			primaryFailure = safeAssessmentFailure(budgetErr)
			return completed, nil
		}
		if err := hooks.recheckPolicy(boundPolicy, policyBudget); err != nil {
			primaryFailure = safeAssessmentFailure(err)
			return completed, nil
		}
		if err := recheckAssessmentContext(captured, deadline, hooks); err != nil {
			primaryFailure = safeAssessmentFailure(err)
			return completed, nil
		}
	}
	if _, err := assessmentRemainingTime(deadline, hooks.now); err != nil {
		primaryFailure = safeAssessmentFailure(err)
		return completed, nil
	}
	core := buildAssessmentCore(assessed, policySHA256, stalePolicy)
	completed, err = finalize(core, cloneAssessmentGuidance(guidance))
	if err != nil {
		primaryFailure = safeAssessmentFailure(err)
		return completed, nil
	}
	if _, err := assessmentRemainingTime(deadline, hooks.now); err != nil {
		primaryFailure = safeAssessmentFailure(err)
		return completed, nil
	}
	if asyncAssessmentFinalizerValue(completed) {
		primaryFailure = assessmentDomainFailure(
			"INVALID_ASSESSMENT_FINALIZER",
			"saved-plan assessment finalization must be synchronous",
		)
		return completed, nil
	}
	hasCompleted = true
	return completed, nil
}

// AssessSavedPlans inspects the report-safe kernel using the source-default
// transaction ceilings.
func AssessSavedPlans(options SavedPlanAssessmentOptions) (SavedPlanAssessmentCore, error) {
	return AssessSavedPlansWithOptions(SavedPlanAssessmentTransactionOptions{Assessment: options})
}

// AssessSavedPlansWithOptions inspects the report-safe kernel with explicit
// transaction-only ceilings.
func AssessSavedPlansWithOptions(
	options SavedPlanAssessmentTransactionOptions,
) (SavedPlanAssessmentCore, error) {
	return runSavedPlanAssessment(
		options,
		func(core SavedPlanAssessmentCore, _ []AssessmentGuidanceGroup) (SavedPlanAssessmentCore, error) {
			return core, nil
		},
		nil,
		productionAssessmentHooks(),
	)
}

func buildReportWithIsolatedGuidance(
	build func([]AssessmentGuidanceGroup) (SavedPlanAssessmentReport, error),
	guidance []AssessmentGuidanceGroup,
) (SavedPlanAssessmentReport, error) {
	report, err := build(cloneAssessmentGuidance(guidance))
	if err == nil {
		return report, nil
	}
	var failure *procerr.ProcessFailure
	if !errors.As(err, &failure) || failure.Code != "INVALID_ASSESSMENT_GUIDANCE" {
		return SavedPlanAssessmentReport{}, err
	}
	return build([]AssessmentGuidanceGroup{})
}

// AssessSavedPlansReport builds the versioned report synchronously inside the
// final evidence window.
func AssessSavedPlansReport(
	options AssessSavedPlansReportOptions,
) (SavedPlanAssessmentReportOutcome, error) {
	mode := options.Mode
	request := AssessmentReportRequest{
		Tenant:    cloneString(options.Request.Tenant),
		Selectors: append([]string{}, options.Request.Selectors...),
		Policy:    cloneString(options.Request.Policy),
	}
	assessmentPolicy := options.Assessment.Assessment.PolicyPath
	if (mode == AssertClean && (request.Policy != nil || assessmentPolicy != nil)) ||
		(mode == AssertAdoptable && ((request.Policy == nil) != (assessmentPolicy == nil))) {
		return SavedPlanAssessmentReportOutcome{}, assessmentDomainFailure(
			"INVALID_ASSESSMENT_REQUEST",
			"assessment mode and policy input disagree",
		)
	}
	report, err := runSavedPlanAssessment(
		options.Assessment,
		func(core SavedPlanAssessmentCore, guidance []AssessmentGuidanceGroup) (SavedPlanAssessmentReport, error) {
			return buildReportWithIsolatedGuidance(
				func(isolated []AssessmentGuidanceGroup) (SavedPlanAssessmentReport, error) {
					return BuildSavedPlanAssessmentReport(BuildSavedPlanAssessmentReportOptions{
						Mode: mode, Request: request, Core: core, Guidance: isolated,
					})
				},
				guidance,
			)
		},
		options.GuidanceSource,
		productionAssessmentHooks(),
	)
	if err == nil {
		return SavedPlanAssessmentReportOutcome{Report: report}, nil
	}
	var failure *SavedPlanAssessmentFailure
	if !errors.As(err, &failure) {
		return SavedPlanAssessmentReportOutcome{}, err
	}
	errorReport, reportErr := buildReportWithIsolatedGuidance(
		func(isolated []AssessmentGuidanceGroup) (SavedPlanAssessmentReport, error) {
			return BuildSavedPlanAssessmentErrorReport(BuildSavedPlanAssessmentErrorReportOptions{
				Mode:    mode,
				Request: request,
				Partial: failure.Partial,
				Error: AssessmentReportError{
					Kind: failure.ReportKind, Message: failure.Message,
				},
				Guidance: isolated,
			})
		},
		failure.Guidance,
	)
	if reportErr != nil {
		return SavedPlanAssessmentReportOutcome{}, reportErr
	}
	return SavedPlanAssessmentReportOutcome{Report: errorReport, Failure: failure}, nil
}
