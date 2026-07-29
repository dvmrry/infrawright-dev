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
	ScalarChange PlanChangeKind = "scalar"
	// SetChange is one Terraform set attribute whose membership moved; Added
	// and Removed carry what entered and left, and Before and After are unset
	// because no single pair of values describes the change. It is emitted only
	// where the provider schema declares the attribute a set (see
	// PlanSchemaTypes); without that evidence the array is compared by
	// position and reported as scalar leaves.
	SetChange PlanChangeKind = "set"
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
	return DiffPathsWithSets(before, after, nil)
}

// DiffPathsWithSets is DiffPaths with the top-level attributes named in
// setAttributes compared by membership instead of by position. A set that
// differs contributes its own path once; a set whose members match contributes
// nothing, because for a Terraform set that is not a difference at all.
//
// setAttributes applies at the resource's own attributes only, so it is
// consulted in the top-level object walk and never handed to the recursion.
func DiffPathsWithSets(before, after any, setAttributes map[string]struct{}) []PlanPath {
	return diffPathsAt(before, after, nil, setAttributes)
}

func diffPathsAt(before, after any, path PlanPath, setAttributes map[string]struct{}) []PlanPath {
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
			if _, isSet := setAttributes[key]; isSet {
				paths = append(paths, setAttributePaths(
					beforeValue,
					afterValue,
					appendPath(path, key),
				)...)
				continue
			}
			paths = append(paths, diffPathsAt(
				beforeValue,
				afterValue,
				appendPath(path, key),
				nil,
			)...)
		}
		return paths
	}
	beforeArray, beforeIsArray := before.([]any)
	afterArray, afterIsArray := after.([]any)
	if beforeIsArray && afterIsArray {
		// An array reached here is positional: either the provider schema
		// declares it a list, or no schema was supplied. Distinguishing a set
		// from an ordered list needs provider schema types (see
		// PlanSchemaTypes); guessing from the values instead -- treating every
		// all-scalar array as a set -- silently drops real reorders of
		// list(string) attributes. Over-reporting is recoverable by a reader;
		// under-reporting is not.
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
				nil,
			)...)
		}
		return paths
	}
	return []PlanPath{clonePath(path)}
}

// setAttributePaths reports a schema-declared set attribute as one path when
// its membership moved and as nothing when it did not.
//
// A side that is not an array -- null on a resource being created, or a value
// the provider did not render as a collection -- falls back to the ordinary
// walk, which reports the attribute itself as a leaf. Nothing is skipped for
// want of the expected shape.
func setAttributePaths(before, after any, path PlanPath) []PlanPath {
	beforeArray, beforeIsArray := before.([]any)
	afterArray, afterIsArray := after.([]any)
	if !beforeIsArray || !afterIsArray {
		return diffPathsAt(before, after, path, nil)
	}
	added, removed := multisetDelta(beforeArray, afterArray)
	if len(added) == 0 && len(removed) == 0 {
		return []PlanPath{}
	}
	return []PlanPath{clonePath(path)}
}

// multisetDelta returns the members present only in after and only in before,
// respecting multiplicity. A Terraform set cannot hold a duplicate, but this
// walk is fed whatever the plan actually contains rather than what the schema
// promises, so a repeated member is counted rather than collapsed.
//
// Members compare under PythonJSONEqual, the same equality every other
// comparison in this walk uses. Two members that the enclosing walk would call
// equal must not be called distinct here, or an array the walk short-circuits
// as unchanged could still report a membership delta.
func multisetDelta(before, after []any) (added, removed []any) {
	matched := make([]bool, len(before))
	added = make([]any, 0)
	for _, afterValue := range after {
		found := false
		for index, beforeValue := range before {
			if !matched[index] && PythonJSONEqual(beforeValue, afterValue) {
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

// DiffChanges returns the differing values in the same Python order DiffPaths
// uses, carrying the content of each difference. The sensitivity arguments are
// the plan's before_sensitive and after_sensitive masks; a nil mask means
// nothing under it is sensitive.
func DiffChanges(before, after, beforeSensitive, afterSensitive any) []PlanChange {
	return DiffChangesWithSets(before, after, beforeSensitive, afterSensitive, nil)
}

// DiffChangesWithSets is DiffChanges under the same set-attribute treatment
// DiffPathsWithSets applies, so the two walks keep agreeing on paths. A set
// whose membership moved yields one SetChange carrying what entered and left.
func DiffChangesWithSets(
	before, after, beforeSensitive, afterSensitive any,
	setAttributes map[string]struct{},
) []PlanChange {
	return diffChangesAt(before, after, beforeSensitive, afterSensitive, nil, setAttributes)
}

func diffChangesAt(
	before, after, beforeSensitive, afterSensitive any,
	path PlanPath,
	setAttributes map[string]struct{},
) []PlanChange {
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
			if _, isSet := setAttributes[key]; isSet {
				changes = append(changes, setAttributeChanges(
					beforeObject[key],
					afterObject[key],
					sensitiveMaskChild(beforeSensitive, key),
					sensitiveMaskChild(afterSensitive, key),
					appendPath(path, key),
				)...)
				continue
			}
			changes = append(changes, diffChangesAt(
				beforeObject[key],
				afterObject[key],
				sensitiveMaskChild(beforeSensitive, key),
				sensitiveMaskChild(afterSensitive, key),
				appendPath(path, key),
				nil,
			)...)
		}
		return changes
	}
	beforeArray, beforeIsArray := before.([]any)
	afterArray, afterIsArray := after.([]any)
	if beforeIsArray && afterIsArray {
		// Positional, matching DiffPaths. The same resource can carry a set and
		// an ordered list side by side -- zia_url_categories has
		// db_categorized_urls as set(string) and urls as list(string) -- so
		// only the schema decides, never the shape of the values.
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
				nil,
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

// setAttributeChanges mirrors setAttributePaths, carrying the membership delta
// behind the single path that walk emits. Redaction is applied to the whole
// attribute: a sensitivity mask anywhere under a set withholds the delta rather
// than the members it happens to cover, because naming what entered a set is
// naming its contents.
//
// A set attribute moving between null and a populated array is reported as a
// scalar change rather than as membership, and that asymmetry with [] to
// populated is deliberate. Terraform distinguishes an unset set from an empty
// one, and the scalar form is the only one that carries the distinction: it
// shows null on one side. Rendering it as membership would spell "unset became
// {a, b}" and "empty became {a, b}" identically, which loses information the
// plan actually contained. The rule is that the delta form is used where both
// sides are collections and both readings agree; otherwise the fuller form
// wins.
func setAttributeChanges(
	before, after, beforeSensitive, afterSensitive any,
	path PlanPath,
) []PlanChange {
	beforeArray, beforeIsArray := before.([]any)
	afterArray, afterIsArray := after.([]any)
	if !beforeIsArray || !afterIsArray {
		return diffChangesAt(before, after, beforeSensitive, afterSensitive, path, nil)
	}
	added, removed := multisetDelta(beforeArray, afterArray)
	if len(added) == 0 && len(removed) == 0 {
		return []PlanChange{}
	}
	return []PlanChange{redactedChange(
		PlanChange{Path: clonePath(path), Kind: SetChange, Added: added, Removed: removed},
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
func updateContent(change map[string]any, setAttributes map[string]struct{}) ([]PlanPath, []PlanChange) {
	changes := DiffChangesWithSets(
		change["before"],
		change["after"],
		change["before_sensitive"],
		change["after_sensitive"],
		setAttributes,
	)
	valuePaths := make([]PlanPath, 0, len(changes))
	for _, valueChange := range changes {
		valuePaths = append(valuePaths, valueChange.Path)
	}
	return updatePathsFrom(change, valuePaths, setAttributes), sortedPlanChanges(changes)
}

func sortedPlanChanges(changes []PlanChange) []PlanChange {
	sorted := append([]PlanChange(nil), changes...)
	sort.SliceStable(sorted, func(left, right int) bool {
		return comparePaths(sorted[left].Path, sorted[right].Path) < 0
	})
	return sorted
}

func updatePathsFrom(
	change map[string]any,
	valuePaths []PlanPath,
	setAttributes map[string]struct{},
) []PlanPath {
	unique := make(map[string]PlanPath)
	opaque := false
	candidates := append(
		append([]PlanPath(nil), valuePaths...),
		collapseSetPaths(TruthyPaths(change["after_unknown"]), setAttributes)...,
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

// collapseSetPaths truncates any path that descends into a schema-declared set
// attribute to the attribute itself.
//
// after_unknown is walked separately from the values, so without this an
// unknown member of a set would arrive as db_categorized_urls[3] while the
// value walk emits db_categorized_urls -- two spellings of one attribute, only
// one of which a drift-policy entry can match. Truncation coarsens a path and
// never removes one, so nothing stops being reported.
func collapseSetPaths(paths []PlanPath, setAttributes map[string]struct{}) []PlanPath {
	if len(setAttributes) == 0 {
		return paths
	}
	collapsed := make([]PlanPath, 0, len(paths))
	for _, path := range paths {
		attribute, isString := "", false
		if len(path) > 1 {
			attribute, isString = path[0].(string)
		}
		if _, isSet := setAttributes[attribute]; isString && isSet {
			collapsed = append(collapsed, PlanPath{attribute})
			continue
		}
		collapsed = append(collapsed, path)
	}
	return collapsed
}

// explainedBySetEquality reports whether every serialized difference between
// this record's before and after is confined to schema-declared set attributes
// whose members match.
//
// It answers a question <opaque_update> otherwise gets wrong. An update whose
// values diff to nothing normally means "something moved that this walk cannot
// see", and blocking on it is the fail-closed default. But a set attribute
// serialized two different ways with the same members has not moved at all --
// that is what "set" means to Terraform -- and calling that opaque asserts
// ignorance where there is none. Only a record whose entire difference is
// accounted for this way qualifies; anything else keeps the marker.
func explainedBySetEquality(change map[string]any, setAttributes map[string]struct{}) bool {
	if len(setAttributes) == 0 {
		return false
	}
	beforeObject, beforeIsObject := change["before"].(map[string]any)
	afterObject, afterIsObject := change["after"].(map[string]any)
	if !beforeIsObject || !afterIsObject {
		return false
	}
	keys := make(map[string]struct{}, len(beforeObject)+len(afterObject))
	for key := range beforeObject {
		keys[key] = struct{}{}
	}
	for key := range afterObject {
		keys[key] = struct{}{}
	}
	explained := false
	for key := range keys {
		if PythonJSONEqual(beforeObject[key], afterObject[key]) {
			continue
		}
		if _, isSet := setAttributes[key]; !isSet {
			return false
		}
		beforeArray, beforeIsArray := beforeObject[key].([]any)
		afterArray, afterIsArray := afterObject[key].([]any)
		if !beforeIsArray || !afterIsArray {
			return false
		}
		added, removed := multisetDelta(beforeArray, afterArray)
		if len(added) > 0 || len(removed) > 0 {
			return false
		}
		explained = true
	}
	return explained
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
	schemaTypes PlanSchemaTypes,
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
		setAttributes := schemaTypes.SetAttributes(resourceType)
		paths, changes := updateContent(change, setAttributes)
		if onlyOpaqueUpdate(paths) &&
			len(TruthyPaths(change["after_unknown"])) == 0 &&
			explainedBySetEquality(change, setAttributes) {
			// The record's whole difference is set attributes whose members
			// match, so there is nothing here to tolerate or to block. Reported
			// as an examined-and-clear finding rather than dropped, so the
			// resource still appears in the report that cleared it.
			return []PlanFinding{{
				Status:  Clean,
				Source:  source,
				Address: address,
				Actions: sortedActions(actions),
				Paths:   []PlanPath{},
			}}
		}
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

// onlyOpaqueUpdate reports whether the update produced no path but the
// synthetic marker, which is the only shape set equality can explain away.
func onlyOpaqueUpdate(paths []PlanPath) bool {
	return len(paths) == 1 && len(paths[0]) == 1 && paths[0][0] == OpaqueUpdate
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

	// SchemaTypes supplies the provider's set-typed attributes, letting the
	// walk compare those by membership instead of by position. The zero value
	// declares nothing a set, which is the positional stance every caller had
	// before this field existed.
	//
	// This is not a relaxation. A set whose membership moved is still one
	// reported path, still matched against drift policy, and still blocking;
	// what changes is that it is one path naming the attribute rather than one
	// path per shifted index, and that it carries what entered and left instead
	// of pairing unrelated members by position.
	SchemaTypes PlanSchemaTypes
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
			for _, finding := range classifyChange(rawRecord.(map[string]any), source, policy, options.SchemaTypes) {
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
