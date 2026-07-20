package assessment

import (
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"sort"
	"strconv"
	"strings"

	"github.com/dvmrry/infrawright-dev/go/internal/canonjson"
	"github.com/dvmrry/infrawright-dev/go/internal/metadata"
	"github.com/dvmrry/infrawright-dev/go/internal/plan"
	"github.com/dvmrry/infrawright-dev/go/internal/procerr"
)

// AssessmentMode selects the saved-plan assertion being reported.
type AssessmentMode string

const (
	// AssertClean reports whether saved plans are change-free without policy.
	AssertClean AssessmentMode = "assert-clean"
	// AssertAdoptable reports whether saved plans are safe under drift policy.
	AssertAdoptable AssessmentMode = "assert-adoptable"
)

// AssessmentErrorKind identifies the phase represented by an error report.
type AssessmentErrorKind string

const (
	// AssessmentError is a failure after assessment work could have started.
	AssessmentError AssessmentErrorKind = "assessment_error"
	// NoSavedPlans is the early failure produced when no plans were selected.
	NoSavedPlans AssessmentErrorKind = "no_saved_plans"
	// PolicyError is the early failure produced while loading policy.
	PolicyError AssessmentErrorKind = "policy_error"
)

// AssessmentReportRequest records the user-visible assessment selection.
type AssessmentReportRequest struct {
	Tenant    *string
	Selectors []string
	Policy    *string
}

// AssessmentFinding adds the resource type derived while assessing one plan.
type AssessmentFinding struct {
	Status       PlanStatus
	Source       string
	Address      string
	ResourceType *string
	Actions      []string
	Paths        []PlanPath
}

// AssessedPlanEvidence is the reportable identity of one saved plan.
type AssessedPlanEvidence struct {
	SHA256           string
	FormatVersion    *string
	TerraformVersion *string
}

// AssessedSavedPlanRoot is one completely classified selected root.
type AssessedSavedPlanRoot struct {
	Tenant          string
	Label           string
	Members         []string
	Status          PlanStatus
	Plan            AssessedPlanEvidence
	PlanFingerprint plan.PlanFingerprintV2
	Findings        []AssessmentFinding
}

// SavedPlanAssessmentCore is the domain result consumed by report builders.
type SavedPlanAssessmentCore struct {
	Status       PlanStatus
	Checked      int
	Clean        int
	Tolerated    int
	Blocked      int
	PolicySHA256 *string
	Roots        []AssessedSavedPlanRoot
	StalePolicy  []metadata.StalePolicyEntry
}

// NormalizedAssessmentFinding is the v1 report form of an assessment finding.
type NormalizedAssessmentFinding struct {
	Status       PlanStatus
	Source       string
	Address      string
	ResourceType *string
	Actions      []string
	Paths        []string
}

// AssessmentReportRoot is one root in a saved-plan assessment report.
type AssessmentReportRoot struct {
	Tenant          string
	Label           string
	Members         []string
	Status          PlanStatus
	Plan            AssessedPlanEvidence
	PlanFingerprint plan.PlanFingerprintV2
	Findings        []NormalizedAssessmentFinding
	Guidance        []map[string]any
}

// AssessmentReportSummary is the aggregate v1 report classification.
type AssessmentReportSummary struct {
	Status    string
	Checked   int
	Clean     int
	Tolerated int
	Blocked   int
}

// AssessmentReportError is the sanitized failure carried by an error report.
type AssessmentReportError struct {
	Kind    AssessmentErrorKind
	Message string
}

// SavedPlanAssessmentReport is the exact schema-version-1 report object.
type SavedPlanAssessmentReport struct {
	Kind          string
	SchemaVersion int
	Mode          AssessmentMode
	Request       struct {
		Tenant       *string
		Selectors    []string
		Policy       *string
		PolicySHA256 *string
	}
	Summary     AssessmentReportSummary
	Roots       []AssessmentReportRoot
	StalePolicy []metadata.StalePolicyEntry
	Error       *AssessmentReportError
}

// BuildSavedPlanAssessmentReportOptions supplies a successful report build.
type BuildSavedPlanAssessmentReportOptions struct {
	Mode     AssessmentMode
	Request  AssessmentReportRequest
	Core     SavedPlanAssessmentCore
	Guidance []AssessmentGuidanceGroup
}

// BuildSavedPlanAssessmentErrorReportOptions supplies an error report build.
type BuildSavedPlanAssessmentErrorReportOptions struct {
	Mode     AssessmentMode
	Request  AssessmentReportRequest
	Partial  SavedPlanAssessmentCore
	Error    AssessmentReportError
	Guidance []AssessmentGuidanceGroup
}

var guidanceLanes = map[string]struct{}{
	"provider_config": {},
	"absent_default":  {},
	"dynamic_schema":  {},
}

var assessmentErrorKinds = map[AssessmentErrorKind]struct{}{
	AssessmentError: {},
	NoSavedPlans:    {},
	PolicyError:     {},
}

func reportDomainFailure(code, message string) *procerr.ProcessFailure {
	return procerr.NewProcessFailure(procerr.NewProcessFailureOptions{
		Code:     code,
		Category: procerr.CategoryDomain,
		Message:  message,
	})
}

func validatedAssessmentReport(report SavedPlanAssessmentReport) (SavedPlanAssessmentReport, error) {
	valid, details := ValidateSavedPlanAssessment(assessmentReportJSONValue(report))
	if valid {
		return report, nil
	}
	return SavedPlanAssessmentReport{}, procerr.NewProcessFailure(procerr.NewProcessFailureOptions{
		Code:     "INVALID_ASSESSMENT_REPORT",
		Category: procerr.CategoryInternal,
		Message:  "saved-plan assessment report is outside schema version 1",
		Details:  details,
	})
}

// FormatConcretePlanPath formats a plan-space path with concrete indexes.
func FormatConcretePlanPath(path PlanPath) string {
	if len(path) == 0 {
		return "<root>"
	}
	parts := make([]string, 0, len(path))
	for _, segment := range path {
		if text, ok := segment.(string); ok {
			if text == "[]" || text == "*" {
				if len(parts) == 0 {
					parts = append(parts, "[]")
				} else {
					parts[len(parts)-1] += "[]"
				}
				continue
			}
			parts = append(parts, text)
			continue
		}
		if number, ok := planPathIndex(segment); ok {
			formatted := "[" + number + "]"
			if len(parts) == 0 {
				parts = append(parts, formatted)
			} else {
				parts[len(parts)-1] += formatted
			}
			continue
		}
		parts = append(parts, fmt.Sprint(segment))
	}
	return strings.Join(parts, ".")
}

// FormatSchemaPlanPath normalizes numeric and wildcard indexes to [].
func FormatSchemaPlanPath(path PlanPath) string {
	normalized := make(PlanPath, len(path))
	for index, segment := range path {
		if segment == "*" {
			normalized[index] = "[]"
			continue
		}
		if _, ok := planPathIndex(segment); ok {
			normalized[index] = "[]"
			continue
		}
		normalized[index] = segment
	}
	return FormatConcretePlanPath(normalized)
}

func planPathIndex(value any) (string, bool) {
	switch typed := value.(type) {
	case int:
		return strconv.Itoa(typed), true
	case int8:
		return strconv.FormatInt(int64(typed), 10), true
	case int16:
		return strconv.FormatInt(int64(typed), 10), true
	case int32:
		return strconv.FormatInt(int64(typed), 10), true
	case int64:
		return strconv.FormatInt(typed, 10), true
	case uint:
		return strconv.FormatUint(uint64(typed), 10), true
	case uint8:
		return strconv.FormatUint(uint64(typed), 10), true
	case uint16:
		return strconv.FormatUint(uint64(typed), 10), true
	case uint32:
		return strconv.FormatUint(uint64(typed), 10), true
	case uint64:
		return strconv.FormatUint(typed, 10), true
	case float64:
		return javascriptNumberToken(typed), true
	case json.Number:
		parsed, err := strconv.ParseFloat(string(typed), 64)
		if err != nil {
			return string(typed), true
		}
		return javascriptNumberToken(parsed), true
	default:
		return "", false
	}
}

type guidanceCloneState struct {
	nodes int
}

func cloneReportJSON(value any, state *guidanceCloneState, depth int) (any, error) {
	state.nodes++
	if depth > 64 {
		return nil, reportDomainFailure(
			"INVALID_ASSESSMENT_GUIDANCE",
			"assessment guidance is too complex",
		)
	}
	switch typed := value.(type) {
	case nil, string, bool:
		return typed, nil
	case float64:
		if math.IsNaN(typed) || math.IsInf(typed, 0) {
			return nil, reportDomainFailure(
				"INVALID_ASSESSMENT_GUIDANCE",
				"assessment guidance is not JSON",
			)
		}
		return typed, nil
	case json.Number:
		if _, err := canonjson.CanonicalNumberToken(string(typed)); err != nil {
			return nil, reportDomainFailure(
				"INVALID_ASSESSMENT_GUIDANCE",
				"assessment guidance is not JSON",
			)
		}
		return json.Number(string(typed)), nil
	case int:
		return cloneGuidanceInteger(big.NewInt(int64(typed)))
	case int8:
		return cloneGuidanceInteger(big.NewInt(int64(typed)))
	case int16:
		return cloneGuidanceInteger(big.NewInt(int64(typed)))
	case int32:
		return cloneGuidanceInteger(big.NewInt(int64(typed)))
	case int64:
		return cloneGuidanceInteger(big.NewInt(typed))
	case uint:
		integer := new(big.Int).SetUint64(uint64(typed))
		return cloneGuidanceInteger(integer)
	case uint8:
		integer := new(big.Int).SetUint64(uint64(typed))
		return cloneGuidanceInteger(integer)
	case uint16:
		integer := new(big.Int).SetUint64(uint64(typed))
		return cloneGuidanceInteger(integer)
	case uint32:
		integer := new(big.Int).SetUint64(uint64(typed))
		return cloneGuidanceInteger(integer)
	case uint64:
		integer := new(big.Int).SetUint64(typed)
		return cloneGuidanceInteger(integer)
	case []any:
		output := make([]any, len(typed))
		for index, child := range typed {
			cloned, err := cloneReportJSON(child, state, depth+1)
			if err != nil {
				return nil, err
			}
			output[index] = cloned
		}
		return output, nil
	case map[string]any:
		output := make(map[string]any, len(typed))
		for key, child := range typed {
			cloned, err := cloneReportJSON(child, state, depth+1)
			if err != nil {
				return nil, err
			}
			output[key] = cloned
		}
		return output, nil
	default:
		return nil, reportDomainFailure(
			"INVALID_ASSESSMENT_GUIDANCE",
			"assessment guidance is not JSON",
		)
	}
}

func cloneGuidanceInteger(integer *big.Int) (any, error) {
	value, accuracy := new(big.Float).SetInt(integer).Float64()
	if accuracy != big.Exact || math.IsInf(value, 0) {
		return nil, reportDomainFailure(
			"INVALID_ASSESSMENT_GUIDANCE",
			"assessment guidance is not JSON",
		)
	}
	return value, nil
}

func normalizedAssessmentFinding(finding AssessmentFinding) NormalizedAssessmentFinding {
	paths := make([]string, len(finding.Paths))
	for index, path := range finding.Paths {
		paths[index] = FormatConcretePlanPath(path)
	}
	return NormalizedAssessmentFinding{
		Status:       finding.Status,
		Source:       finding.Source,
		Address:      finding.Address,
		ResourceType: cloneStringPointer(finding.ResourceType),
		Actions:      append([]string(nil), finding.Actions...),
		Paths:        paths,
	}
}

func rootIdentity(tenant, label string) string {
	return lengthPrefixedIdentity(tenant, label)
}

func blockedFindingIdentity(source, address, path string) string {
	return lengthPrefixedIdentity(source, address, path)
}

func lengthPrefixedIdentity(parts ...string) string {
	var marker strings.Builder
	for _, part := range parts {
		marker.WriteString(strconv.Itoa(len(part)))
		marker.WriteByte(':')
		marker.WriteString(part)
	}
	return marker.String()
}

func guidanceForReportRoot(
	root AssessedSavedPlanRoot,
	entries []map[string]any,
	cloneState *guidanceCloneState,
) ([]map[string]any, error) {
	blocked := make(map[string][]string)
	for _, finding := range root.Findings {
		if finding.Status != Blocked {
			continue
		}
		for _, path := range finding.Paths {
			key := blockedFindingIdentity(
				finding.Source,
				finding.Address,
				FormatConcretePlanPath(path),
			)
			blocked[key] = append(blocked[key], FormatSchemaPlanPath(path))
		}
	}

	normalized := make([]map[string]any, len(entries))
	for index, entry := range entries {
		lane, laneOK := entry["lane"].(string)
		source, sourceOK := entry["source"].(string)
		address, addressOK := entry["address"].(string)
		findingPath, findingPathOK := entry["finding_path"].(string)
		matchedPath, matchedPathOK := entry["matched_plan_path"].(string)
		_, statusEffectOK := entry["status_effect"].(string)
		_, leakedSortKey := entry["sort_key"]
		matching := blocked[blockedFindingIdentity(source, address, findingPath)]
		_, laneAllowed := guidanceLanes[lane]
		if !laneOK || !laneAllowed || !sourceOK || !addressOK || !findingPathOK ||
			!matchedPathOK || !statusEffectOK || leakedSortKey || len(matching) != 1 ||
			matching[0] != matchedPath {
			return nil, reportDomainFailure(
				"INVALID_ASSESSMENT_GUIDANCE",
				"assessment guidance is not joined to a blocked finding",
			)
		}
		cloned, err := cloneReportJSON(entry, cloneState, 0)
		if err != nil {
			return nil, err
		}
		normalized[index] = cloned.(map[string]any)
	}

	var sortFailure error
	sort.SliceStable(normalized, func(left, right int) bool {
		compared, err := compareReportGuidance(normalized[left], normalized[right])
		if err != nil && sortFailure == nil {
			sortFailure = err
		}
		return compared < 0
	})
	if sortFailure != nil {
		return nil, sortFailure
	}
	seen := make(map[string]struct{}, len(normalized))
	deduplicated := make([]map[string]any, 0, len(normalized))
	for _, entry := range normalized {
		marker, err := reportGuidanceMarker(entry)
		if err != nil {
			return nil, err
		}
		if _, duplicate := seen[marker]; duplicate {
			continue
		}
		seen[marker] = struct{}{}
		deduplicated = append(deduplicated, entry)
	}
	return deduplicated, nil
}

func reportGuidanceText(entry map[string]any, key string) (string, error) {
	value, present := entry[key]
	if !present || value == nil {
		return "", nil
	}
	text, ok := value.(string)
	if !ok {
		return "", reportDomainFailure(
			"INVALID_ASSESSMENT_GUIDANCE",
			"assessment guidance sort fields are invalid",
		)
	}
	return text, nil
}

func reportGuidanceSortKey(entry map[string]any) ([]any, error) {
	lane, err := reportGuidanceText(entry, "lane")
	if err != nil {
		return nil, err
	}
	laneOrder := map[string]int{
		"provider_config": 0,
		"absent_default":  1,
		"dynamic_schema":  2,
	}[lane]
	provider, err := reportGuidanceText(entry, "provider")
	if err != nil {
		return nil, err
	}
	matchedPath, err := reportGuidanceText(entry, "matched_plan_path")
	if err != nil {
		return nil, err
	}
	if lane == "provider_config" {
		setting, settingErr := reportGuidanceText(entry, "setting")
		if settingErr != nil {
			return nil, settingErr
		}
		return []any{laneOrder, provider, setting, matchedPath}, nil
	}
	resourceType, err := reportGuidanceText(entry, "resource_type")
	if err != nil {
		return nil, err
	}
	rule, err := reportGuidanceText(entry, "rule")
	if err != nil {
		return nil, err
	}
	return []any{laneOrder, provider, resourceType, matchedPath, rule}, nil
}

func compareReportGuidance(left, right map[string]any) (int, error) {
	leftKey, err := reportGuidanceSortKey(left)
	if err != nil {
		return 0, err
	}
	rightKey, err := reportGuidanceSortKey(right)
	if err != nil {
		return 0, err
	}
	for index := range max(len(leftKey), len(rightKey)) {
		var leftPart, rightPart any = "", ""
		if index < len(leftKey) {
			leftPart = leftKey[index]
		}
		if index < len(rightKey) {
			rightPart = rightKey[index]
		}
		if leftPart == rightPart {
			continue
		}
		leftNumber, leftIsNumber := leftPart.(int)
		rightNumber, rightIsNumber := rightPart.(int)
		if leftIsNumber && rightIsNumber {
			return leftNumber - rightNumber, nil
		}
		return canonjson.ComparePythonStrings(fmt.Sprint(leftPart), fmt.Sprint(rightPart)), nil
	}
	return 0, nil
}

func reportGuidanceMarker(value any) (string, error) {
	switch typed := value.(type) {
	case nil:
		return "null", nil
	case string:
		encoded, _ := json.Marshal(typed)
		return string(encoded), nil
	case bool:
		if typed {
			return "true", nil
		}
		return "false", nil
	case float64:
		if math.IsNaN(typed) || math.IsInf(typed, 0) {
			return "", reportDomainFailure(
				"INVALID_ASSESSMENT_GUIDANCE",
				"assessment guidance is not JSON",
			)
		}
		if typed == 0 && math.Signbit(typed) {
			return "-0", nil
		}
		return javascriptNumberToken(typed), nil
	case json.Number:
		return string(typed), nil
	case []any:
		parts := make([]string, len(typed))
		for index, child := range typed {
			marker, err := reportGuidanceMarker(child)
			if err != nil {
				return "", err
			}
			parts[index] = marker
		}
		return "[" + strings.Join(parts, ",") + "]", nil
	case map[string]any:
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		keys = canonjson.SortedStrings(keys)
		parts := make([]string, len(keys))
		for index, key := range keys {
			keyBytes, _ := json.Marshal(key)
			marker, err := reportGuidanceMarker(typed[key])
			if err != nil {
				return "", err
			}
			parts[index] = string(keyBytes) + ":" + marker
		}
		return "{" + strings.Join(parts, ",") + "}", nil
	default:
		return "", reportDomainFailure(
			"INVALID_ASSESSMENT_GUIDANCE",
			"assessment guidance is not JSON",
		)
	}
}

func javascriptNumberToken(value float64) string {
	if math.IsNaN(value) {
		return "NaN"
	}
	if math.IsInf(value, 1) {
		return "Infinity"
	}
	if math.IsInf(value, -1) {
		return "-Infinity"
	}
	if value == 0 {
		return "0"
	}
	sign := ""
	if value < 0 {
		sign = "-"
		value = -value
	}
	formatted := strconv.FormatFloat(value, 'e', -1, 64)
	exponentAt := strings.IndexByte(formatted, 'e')
	mantissa := formatted[:exponentAt]
	exponent, _ := strconv.Atoi(formatted[exponentAt+1:])
	digits := strings.Replace(mantissa, ".", "", 1)
	if exponent >= -6 && exponent < 21 {
		point := exponent + 1
		switch {
		case point <= 0:
			return sign + "0." + strings.Repeat("0", -point) + digits
		case point >= len(digits):
			return sign + digits + strings.Repeat("0", point-len(digits))
		default:
			return sign + digits[:point] + "." + digits[point:]
		}
	}
	coefficient := digits[:1]
	if len(digits) > 1 {
		coefficient += "." + digits[1:]
	}
	exponentSign := "+"
	if exponent < 0 {
		exponentSign = "-"
		exponent = -exponent
	}
	return sign + coefficient + "e" + exponentSign + strconv.Itoa(exponent)
}

func buildAssessmentReportRoots(
	core SavedPlanAssessmentCore,
	guidance []AssessmentGuidanceGroup,
) ([]AssessmentReportRoot, error) {
	rootSet := make(map[string]struct{}, len(core.Roots))
	for _, root := range core.Roots {
		rootSet[rootIdentity(root.Tenant, root.Label)] = struct{}{}
	}
	byRoot := make(map[string][]map[string]any, len(guidance))
	for _, group := range guidance {
		key := rootIdentity(group.Tenant, group.Label)
		if _, known := rootSet[key]; !known {
			return nil, reportDomainFailure(
				"INVALID_ASSESSMENT_GUIDANCE",
				"assessment guidance references an unknown or duplicate root",
			)
		}
		if _, duplicate := byRoot[key]; duplicate {
			return nil, reportDomainFailure(
				"INVALID_ASSESSMENT_GUIDANCE",
				"assessment guidance references an unknown or duplicate root",
			)
		}
		byRoot[key] = group.Entries
	}

	cloneState := &guidanceCloneState{}
	roots := make([]AssessmentReportRoot, len(core.Roots))
	for index, root := range core.Roots {
		reportable := make([]AssessmentFinding, 0, len(root.Findings))
		for _, finding := range root.Findings {
			if finding.Status != Clean {
				reportable = append(reportable, finding)
			}
		}
		derived := Clean
		for _, finding := range reportable {
			if finding.Status == Blocked {
				derived = Blocked
				break
			}
			if finding.Status == Tolerated {
				derived = Tolerated
			}
		}
		if root.Status != derived {
			return nil, reportDomainFailure(
				"INVALID_ASSESSMENT_REPORT",
				"assessment root status and findings are inconsistent",
			)
		}
		findings := make([]NormalizedAssessmentFinding, len(reportable))
		for findingIndex, finding := range reportable {
			findings[findingIndex] = normalizedAssessmentFinding(finding)
		}
		rootGuidance, err := guidanceForReportRoot(
			root,
			byRoot[rootIdentity(root.Tenant, root.Label)],
			cloneState,
		)
		if err != nil {
			return nil, err
		}
		roots[index] = AssessmentReportRoot{
			Tenant:          root.Tenant,
			Label:           root.Label,
			Members:         append([]string(nil), root.Members...),
			Status:          derived,
			Plan:            cloneAssessedPlanEvidence(root.Plan),
			PlanFingerprint: root.PlanFingerprint,
			Findings:        findings,
			Guidance:        rootGuidance,
		}
	}
	return roots, nil
}

func assessmentCounts(roots []AssessmentReportRoot) AssessmentReportSummary {
	summary := AssessmentReportSummary{Checked: len(roots)}
	for _, root := range roots {
		switch root.Status {
		case Clean:
			summary.Clean++
		case Tolerated:
			summary.Tolerated++
		case Blocked:
			summary.Blocked++
		}
	}
	return summary
}

func assessmentStatusFromCounts(summary AssessmentReportSummary) PlanStatus {
	if summary.Blocked > 0 {
		return Blocked
	}
	if summary.Tolerated > 0 {
		return Tolerated
	}
	return Clean
}

// BuildSavedPlanAssessmentReport builds and validates a successful v1 report.
func BuildSavedPlanAssessmentReport(
	options BuildSavedPlanAssessmentReportOptions,
) (SavedPlanAssessmentReport, error) {
	if (options.Mode == AssertClean && options.Request.Policy != nil) ||
		(options.Request.Policy == nil && options.Core.PolicySHA256 != nil) ||
		(options.Request.Policy != nil && options.Core.PolicySHA256 == nil) {
		return SavedPlanAssessmentReport{}, reportDomainFailure(
			"INVALID_ASSESSMENT_REPORT",
			"assessment request and policy evidence disagree",
		)
	}
	roots, err := buildAssessmentReportRoots(options.Core, options.Guidance)
	if err != nil {
		return SavedPlanAssessmentReport{}, err
	}
	summary := assessmentCounts(roots)
	if len(roots) == 0 || summary.Checked != options.Core.Checked ||
		summary.Clean != options.Core.Clean || summary.Tolerated != options.Core.Tolerated ||
		summary.Blocked != options.Core.Blocked ||
		assessmentStatusFromCounts(summary) != options.Core.Status {
		return SavedPlanAssessmentReport{}, reportDomainFailure(
			"INVALID_ASSESSMENT_REPORT",
			"assessment summary counts are inconsistent",
		)
	}
	summary.Status = string(assessmentStatusFromCounts(summary))
	report := newAssessmentReport(options.Mode, options.Request, options.Core, roots, summary)
	return validatedAssessmentReport(report)
}

// BuildSavedPlanAssessmentErrorReport builds and validates an error v1 report.
func BuildSavedPlanAssessmentErrorReport(
	options BuildSavedPlanAssessmentErrorReportOptions,
) (SavedPlanAssessmentReport, error) {
	_, knownKind := assessmentErrorKinds[options.Error.Kind]
	if len(options.Error.Kind) == 0 || len(options.Error.Message) == 0 || !knownKind ||
		(options.Mode == AssertClean && options.Request.Policy != nil) {
		return SavedPlanAssessmentReport{}, reportDomainFailure(
			"INVALID_ASSESSMENT_REPORT",
			"assessment error input is invalid",
		)
	}
	roots, err := buildAssessmentReportRoots(options.Partial, options.Guidance)
	if err != nil {
		return SavedPlanAssessmentReport{}, err
	}
	summary := assessmentCounts(roots)
	summary.Status = "error"
	report := newAssessmentReport(options.Mode, options.Request, options.Partial, roots, summary)
	report.Error = &AssessmentReportError{Kind: options.Error.Kind, Message: options.Error.Message}
	return validatedAssessmentReport(report)
}

func newAssessmentReport(
	mode AssessmentMode,
	request AssessmentReportRequest,
	core SavedPlanAssessmentCore,
	roots []AssessmentReportRoot,
	summary AssessmentReportSummary,
) SavedPlanAssessmentReport {
	report := SavedPlanAssessmentReport{
		Kind:          "infrawright.saved_plan_assessment",
		SchemaVersion: 1,
		Mode:          mode,
		Summary:       summary,
		Roots:         roots,
		StalePolicy:   append([]metadata.StalePolicyEntry(nil), core.StalePolicy...),
	}
	report.Request.Tenant = cloneStringPointer(request.Tenant)
	report.Request.Selectors = append([]string(nil), request.Selectors...)
	if mode != AssertClean {
		report.Request.Policy = cloneStringPointer(request.Policy)
		report.Request.PolicySHA256 = cloneStringPointer(core.PolicySHA256)
	}
	return report
}

func cloneStringPointer(value *string) *string {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func cloneAssessedPlanEvidence(value AssessedPlanEvidence) AssessedPlanEvidence {
	return AssessedPlanEvidence{
		SHA256:           value.SHA256,
		FormatVersion:    cloneStringPointer(value.FormatVersion),
		TerraformVersion: cloneStringPointer(value.TerraformVersion),
	}
}

func assessmentReportJSONValue(report SavedPlanAssessmentReport) map[string]any {
	roots := make([]any, len(report.Roots))
	for index, root := range report.Roots {
		members := make([]any, len(root.Members))
		for memberIndex, member := range root.Members {
			members[memberIndex] = member
		}
		findings := make([]any, len(root.Findings))
		for findingIndex, finding := range root.Findings {
			actions := make([]any, len(finding.Actions))
			for actionIndex, action := range finding.Actions {
				actions[actionIndex] = action
			}
			paths := make([]any, len(finding.Paths))
			for pathIndex, path := range finding.Paths {
				paths[pathIndex] = path
			}
			findings[findingIndex] = map[string]any{
				"status":        string(finding.Status),
				"source":        finding.Source,
				"address":       finding.Address,
				"resource_type": stringPointerJSONValue(finding.ResourceType),
				"actions":       actions,
				"paths":         paths,
			}
		}
		guidance := make([]any, len(root.Guidance))
		for guidanceIndex, entry := range root.Guidance {
			guidance[guidanceIndex] = entry
		}
		roots[index] = map[string]any{
			"tenant":  root.Tenant,
			"label":   root.Label,
			"members": members,
			"status":  string(root.Status),
			"plan": map[string]any{
				"sha256":            root.Plan.SHA256,
				"format_version":    stringPointerJSONValue(root.Plan.FormatVersion),
				"terraform_version": stringPointerJSONValue(root.Plan.TerraformVersion),
			},
			"plan_fingerprint": map[string]any{
				"version": json.Number(strconv.Itoa(root.PlanFingerprint.Version)),
				"sha256":  root.PlanFingerprint.SHA256,
			},
			"findings": findings,
			"guidance": guidance,
		}
	}
	stalePolicy := make([]any, len(report.StalePolicy))
	for index, entry := range report.StalePolicy {
		stalePolicy[index] = map[string]any{
			"resource_type": entry.ResourceType,
			"mode":          string(entry.Mode),
			"path":          entry.Path,
		}
	}
	value := map[string]any{
		"kind":           report.Kind,
		"schema_version": json.Number(strconv.Itoa(report.SchemaVersion)),
		"mode":           string(report.Mode),
		"request": map[string]any{
			"tenant":        stringPointerJSONValue(report.Request.Tenant),
			"selectors":     stringsJSONValue(report.Request.Selectors),
			"policy":        stringPointerJSONValue(report.Request.Policy),
			"policy_sha256": stringPointerJSONValue(report.Request.PolicySHA256),
		},
		"summary": map[string]any{
			"status":    report.Summary.Status,
			"checked":   json.Number(strconv.Itoa(report.Summary.Checked)),
			"clean":     json.Number(strconv.Itoa(report.Summary.Clean)),
			"tolerated": json.Number(strconv.Itoa(report.Summary.Tolerated)),
			"blocked":   json.Number(strconv.Itoa(report.Summary.Blocked)),
		},
		"roots":        roots,
		"stale_policy": stalePolicy,
	}
	if report.Error != nil {
		value["error"] = map[string]any{
			"kind":    string(report.Error.Kind),
			"message": report.Error.Message,
		}
	}
	return value
}

func stringPointerJSONValue(value *string) any {
	if value == nil {
		return nil
	}
	return *value
}

func stringsJSONValue(values []string) []any {
	result := make([]any, len(values))
	for index, value := range values {
		result[index] = value
	}
	return result
}
