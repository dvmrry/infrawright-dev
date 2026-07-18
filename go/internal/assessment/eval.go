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

// PlanFinding is one classified Terraform resource change.
type PlanFinding struct {
	Status  PlanStatus `json:"status"`
	Source  string     `json:"source"`
	Address string     `json:"address"`
	Actions []string   `json:"actions"`
	Paths   []PlanPath `json:"paths"`
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
	return PlanFinding{
		Status:  Blocked,
		Source:  source,
		Address: address,
		Actions: sortedActions(actions),
		Paths:   clonePaths(paths),
	}
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

func updatePaths(change map[string]any) []PlanPath {
	unique := make(map[string]PlanPath)
	opaque := false
	candidates := append(
		DiffPaths(change["before"], change["after"]),
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
		paths := updatePaths(change)
		unmatched := make([]PlanPath, 0, len(paths))
		for _, candidate := range paths {
			if policy == nil || !policy.ToleratesPlanPath(resourceType, []any(candidate), "update") {
				unmatched = append(unmatched, clonePath(candidate))
			}
		}
		if len(unmatched) > 0 {
			return []PlanFinding{blockedFinding(source, address, actions, unmatched)}
		}
		return []PlanFinding{{
			Status:  Tolerated,
			Source:  source,
			Address: address,
			Actions: sortedActions(actions),
			Paths:   clonePaths(paths),
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

// ClassifyPlan is the fail-closed assessment entry point. It validates the
// complete plan contract before applying any drift-policy matches.
func ClassifyPlan(
	planValue any,
	policy *metadata.DriftPolicy,
	contract *plan.AssessmentPlanContract,
) (PlanClassification, error) {
	if err := plan.ValidateAssessmentPlan(planValue, contract); err != nil {
		return PlanClassification{}, err
	}
	planObject := planValue.(map[string]any)
	findings := make([]PlanFinding, 0)
	for _, source := range []string{"resource_changes", "resource_drift"} {
		records, _ := planObject[source].([]any)
		for _, rawRecord := range records {
			findings = append(findings, classifyChange(rawRecord.(map[string]any), source, policy)...)
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
