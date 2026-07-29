package assessment

import (
	"sort"
	"strconv"

	"github.com/dvmrry/infrawright-dev/go/internal/canonjson"
	"github.com/dvmrry/infrawright-dev/go/internal/metadata"
	"github.com/dvmrry/infrawright-dev/go/internal/plan"
)

// PlanStatus is the saved-plan classification status emitted by plan-eval.ts.
type PlanStatus string

const (
	// Clean means the plan contains no actionable changes.
	Clean PlanStatus = "clean"
	// Tolerated means every actionable update path is allowed by drift policy.
	Tolerated PlanStatus = "clean_with_tolerated_drift"
	// Blocked means at least one actionable change is not allowed.
	Blocked PlanStatus = "blocked"

	// OpaqueUpdate is the synthetic path used when an update has no visible
	// value, unknown-value, identity, or sensitivity path.
	OpaqueUpdate = "<opaque_update>"
	// IdentityChange is the synthetic path for resource identity metadata drift.
	IdentityChange = "<identity_change>"
	// SensitivityChange is the synthetic path for sensitivity-mask drift.
	SensitivityChange = "<sensitivity_change>"
)

// PlanPath is one Terraform value path. Segments are strings or zero-based
// integer indexes.
type PlanPath []any

// PlanChangeKind classifies how a differing value should be read. The shape a
// reviewer needs differs by attribute kind, so the classification travels with
// the change instead of being inferred downstream.
type PlanChangeKind string

const (
	// ScalarChange is one value that moved; Before and After carry it. Leaves
	// inside ordered lists and block collections are scalar changes too, and
	// keep their positional path, because there position is identity.
	// ScalarChange is the only kind emitted today. Kind stays a field rather
	// than being dropped so that reporting Terraform set membership as its own
	// kind, once provider schema types reach this function, is an additive
	// change a consumer can branch on without a schema version bump.
	ScalarChange PlanChangeKind = "scalar"
)

// PlanChange is one differing value, carried with enough content that a
// reviewer can name the change without opening the plan.
//
// Sensitive marks a value the plan flagged through its before_sensitive or
// after_sensitive masks. Those are reported with the content withheld rather
// than dropped, so a reviewer still sees that a secret moved without the
// secret reaching a build log.
type PlanChange struct {
	Path      PlanPath       `json:"path"`
	Kind      PlanChangeKind `json:"kind"`
	Sensitive bool           `json:"sensitive"`
	Before    any            `json:"before,omitempty"`
	After     any            `json:"after,omitempty"`
	Added     []any          `json:"added,omitempty"`
	Removed   []any          `json:"removed,omitempty"`
}

// PlanFinding is one classified Terraform resource change.
//
// Paths remains the complete list of what differs, including the synthetic
// markers for identity, sensitivity, opaque updates, and values that are
// unknown until apply. Changes covers the subset that has content to show, so
// every Change path appears in Paths but not the reverse.
type PlanFinding struct {
	Status  PlanStatus   `json:"status"`
	Source  string       `json:"source"`
	Address string       `json:"address"`
	Actions []string     `json:"actions"`
	Paths   []PlanPath   `json:"paths"`
	Changes []PlanChange `json:"changes"`
}

// PlanClassification is the ordered classification of a validated plan.
type PlanClassification struct {
	Status   PlanStatus    `json:"status"`
	Findings []PlanFinding `json:"findings"`
}

// PythonJSONEqual reports Python-compatible equality over canonjson values.
// In particular, JSON booleans participate in Python's numeric tower.
func PythonJSONEqual(left, right any) bool {
	return canonjson.JSONEqual(left, right)
}

// DiffPaths returns Python-ordered leaf paths whose values differ. Missing
// object keys and array elements compare as JSON null, matching plan-eval.ts.
func DiffPaths(before, after any) []PlanPath {
	return diffPathsAt(before, after, nil)
}

func diffPathsAt(before, after any, path PlanPath) []PlanPath {
	if PythonJSONEqual(before, after) {
		return []PlanPath{}
	}
	beforeObject, beforeIsObject := before.(map[string]any)
	afterObject, afterIsObject := after.(map[string]any)
	if beforeIsObject && afterIsObject {
		keySet := make(map[string]struct{}, len(beforeObject)+len(afterObject))
		for key := range beforeObject {
			keySet[key] = struct{}{}
		}
		for key := range afterObject {
			keySet[key] = struct{}{}
		}
		keys := make([]string, 0, len(keySet))
		for key := range keySet {
			keys = append(keys, key)
		}
		keys = canonjson.SortedStrings(keys)
		paths := make([]PlanPath, 0)
		for _, key := range keys {
			beforeValue := beforeObject[key]
			afterValue := afterObject[key]
			paths = append(paths, diffPathsAt(
				beforeValue,
				afterValue,
				appendPath(path, key),
			)...)
		}
		return paths
	}
	beforeArray, beforeIsArray := before.([]any)
	afterArray, afterIsArray := after.([]any)
	if beforeIsArray && afterIsArray {
		// Every array is compared by position, including one holding a
		// Terraform set. Terraform compares sets by element hash and
		// serializes them in provider-chosen order, so a pure reorder is
		// reported here as a change the plan does not contain. That is a
		// known false positive, and it is deliberately kept: distinguishing a
		// set from an ordered list needs provider schema types, which this
		// function does not have, and suppressing every all-scalar array
		// instead silently drops real reorders of list(string) attributes.
		// Over-reporting is recoverable by a reader; under-reporting is not.
		length := max(len(beforeArray), len(afterArray))
		paths := make([]PlanPath, 0)
		for index := range length {
			var beforeValue any
			if index < len(beforeArray) {
				beforeValue = beforeArray[index]
			}
			var afterValue any
			if index < len(afterArray) {
				afterValue = afterArray[index]
			}
			paths = append(paths, diffPathsAt(
				beforeValue,
				afterValue,
				appendPath(path, index),
			)...)
		}
		return paths
	}
	return []PlanPath{clonePath(path)}
}

// DiffChanges returns the differing values in the same Python order DiffPaths
// uses, carrying the content of each difference. The sensitivity arguments are
// the plan's before_sensitive and after_sensitive masks; a nil mask means
// nothing under it is sensitive.
func DiffChanges(before, after, beforeSensitive, afterSensitive any) []PlanChange {
	return diffChangesAt(before, after, beforeSensitive, afterSensitive, nil)
}

func diffChangesAt(before, after, beforeSensitive, afterSensitive any, path PlanPath) []PlanChange {
	if PythonJSONEqual(before, after) {
		return []PlanChange{}
	}
	beforeObject, beforeIsObject := before.(map[string]any)
	afterObject, afterIsObject := after.(map[string]any)
	if beforeIsObject && afterIsObject {
		keySet := make(map[string]struct{}, len(beforeObject)+len(afterObject))
		for key := range beforeObject {
			keySet[key] = struct{}{}
		}
		for key := range afterObject {
			keySet[key] = struct{}{}
		}
		keys := make([]string, 0, len(keySet))
		for key := range keySet {
			keys = append(keys, key)
		}
		keys = canonjson.SortedStrings(keys)
		changes := make([]PlanChange, 0)
		for _, key := range keys {
			changes = append(changes, diffChangesAt(
				beforeObject[key],
				afterObject[key],
				sensitiveMaskChild(beforeSensitive, key),
				sensitiveMaskChild(afterSensitive, key),
				appendPath(path, key),
			)...)
		}
		return changes
	}
	beforeArray, beforeIsArray := before.([]any)
	afterArray, afterIsArray := after.([]any)
	if beforeIsArray && afterIsArray {
		// Positional, matching DiffPaths. Collapsing an all-scalar array to a
		// membership delta reads far better for a genuine set, but the same
		// resource can carry a set and an ordered list side by side
		// (zia_url_categories has db_categorized_urls as set(string) and urls
		// as list(string)), and nothing here can tell them apart.
		length := max(len(beforeArray), len(afterArray))
		changes := make([]PlanChange, 0)
		for index := range length {
			var beforeValue any
			if index < len(beforeArray) {
				beforeValue = beforeArray[index]
			}
			var afterValue any
			if index < len(afterArray) {
				afterValue = afterArray[index]
			}
			changes = append(changes, diffChangesAt(
				beforeValue,
				afterValue,
				sensitiveMaskChild(beforeSensitive, index),
				sensitiveMaskChild(afterSensitive, index),
				appendPath(path, index),
			)...)
		}
		return changes
	}
	return []PlanChange{redactedChange(
		PlanChange{Path: clonePath(path), Kind: ScalarChange, Before: before, After: after},
		beforeSensitive,
		afterSensitive,
	)}
}

// redactedChange withholds content the plan marked sensitive while keeping the
// change itself visible. Dropping the entry would hide that a secret moved;
// emitting the value would put it in whatever log reads the report.
func redactedChange(change PlanChange, beforeSensitive, afterSensitive any) PlanChange {
	if !maskIsSensitive(beforeSensitive) && !maskIsSensitive(afterSensitive) {
		return change
	}
	return PlanChange{Path: change.Path, Kind: change.Kind, Sensitive: true}
}

// maskIsSensitive reports whether a sensitivity mask marks this value or
// anything beneath it. Terraform may collapse a whole subtree to a single
// true, so a container mask is as decisive as a leaf.
func maskIsSensitive(mask any) bool {
	return len(TruthyPaths(mask)) > 0
}

// sensitiveMaskChild descends a sensitivity mask alongside the value walk. A
// subtree collapsed to true stays true for every child.
func sensitiveMaskChild(mask any, segment any) any {
	if boolean, ok := mask.(bool); ok {
		if boolean {
			return true
		}
		return nil
	}
	switch key := segment.(type) {
	case string:
		if object, ok := mask.(map[string]any); ok {
			return object[key]
		}
	case int:
		if array, ok := mask.([]any); ok && key >= 0 && key < len(array) {
			return array[key]
		}
	}
	return nil
}

// TruthyPaths returns Python-ordered paths whose recursive boolean mask leaf
// is exactly true.
func TruthyPaths(value any) []PlanPath {
	return truthyPathsAt(value, nil)
}

func truthyPathsAt(value any, path PlanPath) []PlanPath {
	if boolean, ok := value.(bool); ok && boolean {
		return []PlanPath{clonePath(path)}
	}
	if object, ok := value.(map[string]any); ok {
		keys := make([]string, 0, len(object))
		for key := range object {
			keys = append(keys, key)
		}
		keys = canonjson.SortedStrings(keys)
		paths := make([]PlanPath, 0)
		for _, key := range keys {
			paths = append(paths, truthyPathsAt(object[key], appendPath(path, key))...)
		}
		return paths
	}
	if array, ok := value.([]any); ok {
		paths := make([]PlanPath, 0)
		for index, child := range array {
			paths = append(paths, truthyPathsAt(child, appendPath(path, index))...)
		}
		return paths
	}
	return []PlanPath{}
}

func clonePath(path PlanPath) PlanPath {
	return append(PlanPath(nil), path...)
}

func appendPath(path PlanPath, segment any) PlanPath {
	result := make(PlanPath, len(path), len(path)+1)
	copy(result, path)
	return append(result, segment)
}

func comparePaths(left, right PlanPath) int {
	for index := 0; index < min(len(left), len(right)); index++ {
		compared := canonjson.ComparePythonStrings(pathSegmentText(left[index]), pathSegmentText(right[index]))
		if compared != 0 {
			return compared
		}
	}
	return len(left) - len(right)
}

func pathSegmentText(segment any) string {
	switch value := segment.(type) {
	case string:
		return value
	case int:
		return strconv.Itoa(value)
	default:
		return ""
	}
}

func pathMarker(path PlanPath) string {
	marker := ""
	for _, segment := range path {
		switch value := segment.(type) {
		case string:
			marker += "s" + strconv.Itoa(len(value)) + ":" + value
		case int:
			marker += "i" + strconv.Itoa(value) + ":"
		}
	}
	return marker
}

func samePaths(left, right []PlanPath) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if len(left[index]) != len(right[index]) {
			return false
		}
		for segment := range left[index] {
			if left[index][segment] != right[index][segment] {
				return false
			}
		}
	}
	return true
}

func blockedFinding(source, address string, actions map[string]struct{}, paths []PlanPath) PlanFinding {
	return blockedFindingWithChanges(source, address, actions, paths, nil)
}

func blockedFindingWithChanges(
	source, address string,
	actions map[string]struct{},
	paths []PlanPath,
	changes []PlanChange,
) PlanFinding {
	return PlanFinding{
		Status:  Blocked,
		Source:  source,
		Address: address,
		Actions: sortedActions(actions),
		Paths:   clonePaths(paths),
		Changes: clonePlanChanges(changes),
	}
}

func clonePlanChanges(changes []PlanChange) []PlanChange {
	result := make([]PlanChange, 0, len(changes))
	for _, change := range changes {
		cloned := change
		cloned.Path = clonePath(change.Path)
		cloned.Added = append([]any(nil), change.Added...)
		cloned.Removed = append([]any(nil), change.Removed...)
		result = append(result, cloned)
	}
	return result
}

// changesForPaths keeps the changes whose paths survived policy matching, so a
// finding never shows content for a difference it did not report.
func changesForPaths(changes []PlanChange, paths []PlanPath) []PlanChange {
	kept := make(map[string]struct{}, len(paths))
	for _, path := range paths {
		kept[pathMarker(path)] = struct{}{}
	}
	result := make([]PlanChange, 0, len(changes))
	for _, change := range changes {
		if _, ok := kept[pathMarker(change.Path)]; ok {
			result = append(result, change)
		}
	}
	return result
}

func clonePaths(paths []PlanPath) []PlanPath {
	result := make([]PlanPath, len(paths))
	for index, path := range paths {
		result[index] = clonePath(path)
	}
	return result
}

func sortedActions(actions map[string]struct{}) []string {
	values := make([]string, 0, len(actions))
	for action := range actions {
		values = append(values, action)
	}
	return canonjson.SortedStrings(values)
}

// updateContent returns every differing path and, for those differences that
// have content to show, the values behind them. Synthetic markers and
// unknown-until-apply paths appear in the path list alone: there is no value
// to carry for them.
func updateContent(change map[string]any) ([]PlanPath, []PlanChange) {
	changes := DiffChanges(
		change["before"],
		change["after"],
		change["before_sensitive"],
		change["after_sensitive"],
	)
	valuePaths := make([]PlanPath, 0, len(changes))
	for _, valueChange := range changes {
		valuePaths = append(valuePaths, valueChange.Path)
	}
	return updatePathsFrom(change, valuePaths), sortedPlanChanges(changes)
}

func sortedPlanChanges(changes []PlanChange) []PlanChange {
	sorted := append([]PlanChange(nil), changes...)
	sort.SliceStable(sorted, func(left, right int) bool {
		return comparePaths(sorted[left].Path, sorted[right].Path) < 0
	})
	return sorted
}

func updatePathsFrom(change map[string]any, valuePaths []PlanPath) []PlanPath {
	unique := make(map[string]PlanPath)
	opaque := false
	candidates := append(
		append([]PlanPath(nil), valuePaths...),
		TruthyPaths(change["after_unknown"])...,
	)
	for _, path := range candidates {
		if len(path) == 0 {
			opaque = true
			continue
		}
		unique[pathMarker(path)] = clonePath(path)
	}
	if !PythonJSONEqual(change["before_identity"], change["after_identity"]) {
		path := PlanPath{IdentityChange}
		unique[pathMarker(path)] = path
	}
	if !samePaths(TruthyPaths(change["before_sensitive"]), TruthyPaths(change["after_sensitive"])) {
		path := PlanPath{SensitivityChange}
		unique[pathMarker(path)] = path
	}
	if opaque || len(unique) == 0 {
		path := PlanPath{OpaqueUpdate}
		unique[pathMarker(path)] = path
	}
	paths := make([]PlanPath, 0, len(unique))
	for _, path := range unique {
		paths = append(paths, path)
	}
	sortPlanPaths(paths)
	return paths
}

func sortPlanPaths(paths []PlanPath) {
	sort.SliceStable(paths, func(left, right int) bool {
		return comparePaths(paths[left], paths[right]) < 0
	})
}

func classifyChange(
	record map[string]any,
	source string,
	policy *metadata.DriftPolicy,
) []PlanFinding {
	change, _ := record["change"].(map[string]any)
	rawActions, _ := change["actions"].([]any)
	actions := make(map[string]struct{}, len(rawActions))
	for _, rawAction := range rawActions {
		actions[rawAction.(string)] = struct{}{}
	}
	if len(actions) == 0 || onlyNoOp(actions) {
		return []PlanFinding{}
	}
	address := record["address"].(string)
	resourceType := record["type"].(string)
	if importing, ok := change["importing"].(map[string]any); ok && len(importing) > 0 &&
		len(actions) == 1 && hasAction(actions, "create") {
		return []PlanFinding{{
			Status:  Clean,
			Source:  source,
			Address: address,
			Actions: sortedActions(actions),
			Paths:   []PlanPath{},
		}}
	}
	if hasAction(actions, "delete") {
		return []PlanFinding{blockedFinding(source, address, actions, []PlanPath{{"<delete>"}})}
	}
	if hasAction(actions, "create") {
		return []PlanFinding{blockedFinding(source, address, actions, []PlanPath{{"<create>"}})}
	}
	if hasAction(actions, "update") {
		paths, changes := updateContent(change)
		unmatched := make([]PlanPath, 0, len(paths))
		for _, candidate := range paths {
			if policy == nil || !policy.ToleratesPlanPath(resourceType, []any(candidate), "update") {
				unmatched = append(unmatched, clonePath(candidate))
			}
		}
		if len(unmatched) > 0 {
			return []PlanFinding{blockedFindingWithChanges(
				source, address, actions, unmatched, changesForPaths(changes, unmatched),
			)}
		}
		return []PlanFinding{{
			Status:  Tolerated,
			Source:  source,
			Address: address,
			Actions: sortedActions(actions),
			Paths:   clonePaths(paths),
			Changes: clonePlanChanges(changesForPaths(changes, paths)),
		}}
	}
	return []PlanFinding{blockedFinding(source, address, actions, []PlanPath{{"<unsupported_action>"}})}
}

func onlyNoOp(actions map[string]struct{}) bool {
	for action := range actions {
		if action != "no-op" {
			return false
		}
	}
	return true
}

func hasAction(actions map[string]struct{}, action string) bool {
	_, ok := actions[action]
	return ok
}

// ClassifyPlanOptions selects the classification stance for one plan. The
// zero value is the strict stance: every actionable change blocks, whichever
// plan section reported it. New call sites therefore fail closed by default.
type ClassifyPlanOptions struct {
	// TolerateRefreshDrift demotes resource_drift findings from Blocked to
	// Tolerated, leaving resource_changes strict.
	//
	// Refresh records a resource whose remote values have moved since the
	// last apply in resource_drift, not in resource_changes. Adoption cannot
	// clear such a record before the gate runs, because persisting the
	// refreshed values is the apply the gate is guarding: an import-only
	// plan is refused for drift that only the import can settle. Adoption
	// therefore reports the drift and proceeds.
	//
	// Steady-state assertion leaves this false, where the same records are
	// the out-of-band change the gate exists to catch.
	TolerateRefreshDrift bool
}

// ClassifyPlan is the fail-closed assessment entry point. It validates the
// complete plan contract before applying any drift-policy matches, and blocks
// on every actionable change in either plan section.
func ClassifyPlan(
	planValue any,
	policy *metadata.DriftPolicy,
	contract *plan.AssessmentPlanContract,
) (PlanClassification, error) {
	return ClassifyPlanWithOptions(planValue, policy, contract, ClassifyPlanOptions{})
}

// ClassifyPlanWithOptions is ClassifyPlan under an explicit stance.
func ClassifyPlanWithOptions(
	planValue any,
	policy *metadata.DriftPolicy,
	contract *plan.AssessmentPlanContract,
	options ClassifyPlanOptions,
) (PlanClassification, error) {
	if err := plan.ValidateAssessmentPlan(planValue, contract); err != nil {
		return PlanClassification{}, err
	}
	planObject := planValue.(map[string]any)
	findings := make([]PlanFinding, 0)
	for _, source := range []string{"resource_changes", "resource_drift"} {
		records, _ := planObject[source].([]any)
		demote := options.TolerateRefreshDrift && source == "resource_drift"
		for _, rawRecord := range records {
			for _, finding := range classifyChange(rawRecord.(map[string]any), source, policy) {
				if demote && finding.Status == Blocked {
					finding.Status = Tolerated
				}
				findings = append(findings, finding)
			}
		}
	}
	status := Clean
	for _, finding := range findings {
		if finding.Status == Blocked {
			status = Blocked
			break
		}
		if finding.Status == Tolerated {
			status = Tolerated
		}
	}
	return PlanClassification{Status: status, Findings: findings}, nil
}
