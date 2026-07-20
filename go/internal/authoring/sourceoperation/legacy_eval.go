// Package sourceoperation contains isolated authoring source-operation
// compatibility helpers. The LegacyV1 names in this file intentionally mark
// the frozen OpenAPI-backed evaluator as ineligible for v2 readiness use.
package sourceoperation

import (
	"encoding/json"
	"math"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"github.com/dvmrry/infrawright-dev/go/internal/canonjson"
)

const LegacyV1MaxMarkdownChangeRows = 100

// LegacyV1Artifact is the decoded JSON artifact model used by the retained
// Node/Python differential vectors. It is intentionally not a v2 contract.
// It remains an alias (rather than a named map) so canonjson can render the
// resulting legacy artifact without a conversion step.
type LegacyV1Artifact = map[string]any

var legacyV1ShortcomingSeverity = map[string]string{
	"ambiguous_source_operation":           "review",
	"calls_without_openapi_match":          "gap",
	"graphql_source":                       "notice",
	"mapped_read_without_list":             "notice",
	"regression":                           "gap",
	"resource_file_not_found":              "gap",
	"source_files_without_operation_calls": "gap",
	"unmapped_without_reason":              "gap",
}

var legacyV1SeverityOrder = map[string]int{"gap": 0, "review": 1, "notice": 2}
var legacyV1ChangeClassOrder = map[string]int{"regression": 0, "review": 1, "acceptable": 2}

func legacyV1Object(value any) LegacyV1Artifact {
	switch object := value.(type) {
	case map[string]any:
		return object
	default:
		return LegacyV1Artifact{}
	}
}

func legacyV1Objects(value any) []LegacyV1Artifact {
	items, ok := value.([]any)
	if !ok {
		return nil
	}
	objects := make([]LegacyV1Artifact, 0, len(items))
	for _, item := range items {
		if object, ok := item.(map[string]any); ok {
			objects = append(objects, object)
		}
	}
	return objects
}

func legacyV1Strings(value any) []string {
	items, ok := value.([]any)
	if !ok {
		return nil
	}
	strings := make([]string, 0, len(items))
	for _, item := range items {
		if stringValue, ok := item.(string); ok {
			strings = append(strings, stringValue)
		}
	}
	return strings
}

func legacyV1Truthy(value any) bool {
	switch typed := value.(type) {
	case nil:
		return false
	case bool:
		return typed
	case string:
		return len(typed) > 0
	case json.Number:
		return legacyV1Number(typed) != 0
	case float64:
		return typed != 0
	case float32:
		return typed != 0
	case int:
		return typed != 0
	case int64:
		return typed != 0
	case int32:
		return typed != 0
	case uint:
		return typed != 0
	case uint64:
		return typed != 0
	case []any:
		return len(typed) > 0
	case []string:
		return len(typed) > 0
	case map[string]any:
		return len(typed) > 0
	default:
		return true
	}
}

func legacyV1Number(value any) float64 {
	switch typed := value.(type) {
	case nil:
		return 0
	case bool:
		if typed {
			return 1
		}
		return 0
	case json.Number:
		parsed, err := strconv.ParseFloat(string(typed), 64)
		if err != nil {
			return math.NaN()
		}
		return parsed
	case float64:
		return typed
	case float32:
		return float64(typed)
	case int:
		return float64(typed)
	case int64:
		return float64(typed)
	case int32:
		return float64(typed)
	case uint:
		return float64(typed)
	case uint64:
		return float64(typed)
	case string:
		trimmed := strings.TrimSpace(typed)
		if trimmed == "" {
			return 0
		}
		parsed, err := strconv.ParseFloat(trimmed, 64)
		if err != nil {
			return math.NaN()
		}
		return parsed
	default:
		return math.NaN()
	}
}

func legacyV1Display(value any) string {
	if value == nil {
		return "None"
	}
	if value == true {
		return "True"
	}
	if value == false {
		return "False"
	}
	return legacyV1JavaScriptString(value)
}

func legacyV1JavaScriptString(value any) string {
	switch typed := value.(type) {
	case nil:
		return "null"
	case string:
		return typed
	case json.Number:
		return string(typed)
	case float64:
		if math.IsNaN(typed) {
			return "NaN"
		}
		if math.IsInf(typed, 1) {
			return "Infinity"
		}
		if math.IsInf(typed, -1) {
			return "-Infinity"
		}
		return strconv.FormatFloat(typed, 'g', -1, 64)
	case float32:
		return legacyV1JavaScriptString(float64(typed))
	case int:
		return strconv.Itoa(typed)
	case int64:
		return strconv.FormatInt(typed, 10)
	case int32:
		return strconv.FormatInt(int64(typed), 10)
	case uint:
		return strconv.FormatUint(uint64(typed), 10)
	case uint64:
		return strconv.FormatUint(typed, 10)
	case []any:
		parts := make([]string, len(typed))
		for index, item := range typed {
			if item != nil {
				parts[index] = legacyV1JavaScriptString(item)
			}
		}
		return strings.Join(parts, ",")
	case []string:
		return strings.Join(typed, ",")
	case map[string]any:
		return "[object Object]"
	case bool:
		return strconv.FormatBool(typed)
	default:
		return "[object Object]"
	}
}

func legacyV1Default(object LegacyV1Artifact, key string, fallback any) any {
	if value, ok := object[key]; ok {
		return value
	}
	return fallback
}

func legacyV1FirstTruthy(values ...any) any {
	for _, value := range values {
		if legacyV1Truthy(value) {
			return value
		}
	}
	if len(values) == 0 {
		return nil
	}
	return values[len(values)-1]
}

func legacyV1ChangedFilesOnly(before, after LegacyV1Artifact) bool {
	beforeFiles := legacyV1Default(before, "files", []any{})
	afterFiles := legacyV1Default(after, "files", []any{})
	beforeRest := make(LegacyV1Artifact, len(before))
	for key, value := range before {
		if key != "files" {
			beforeRest[key] = value
		}
	}
	afterRest := make(LegacyV1Artifact, len(after))
	for key, value := range after {
		if key != "files" {
			afterRest[key] = value
		}
	}
	return reflect.DeepEqual(beforeRest, afterRest) && !reflect.DeepEqual(beforeFiles, afterFiles)
}

// ClassifyLegacyV1SourceEvidenceChange preserves the frozen v1 classification
// precedence. It must not be used for source-first readiness decisions.
func ClassifyLegacyV1SourceEvidenceChange(change LegacyV1Artifact) LegacyV1Artifact {
	before := legacyV1Object(change["before"])
	after := legacyV1Object(change["after"])
	beforeStatus, afterStatus := before["status"], after["status"]
	beforeRead, afterRead := before["read_path"], after["read_path"]
	beforeFiles, afterFiles := legacyV1Strings(before["files"]), legacyV1Strings(after["files"])

	if beforeStatus == "mapped" && afterStatus == "unmapped" {
		return LegacyV1Artifact{"classification": "regression", "reason": "mapped_to_unmapped"}
	}
	if beforeStatus == "mapped" && afterStatus == "mapped" && legacyV1Truthy(beforeRead) && legacyV1Truthy(afterRead) && !reflect.DeepEqual(beforeRead, afterRead) {
		return LegacyV1Artifact{"classification": "regression", "reason": "mapped_read_path_changed"}
	}
	if len(beforeFiles) > 0 && len(afterFiles) == 0 {
		return LegacyV1Artifact{"classification": "regression", "reason": "source_files_dropped_to_zero"}
	}
	if legacyV1ChangedFilesOnly(before, after) {
		reason := "source_files_changed"
		if len(afterFiles) < len(beforeFiles) {
			reason = "source_files_narrowed"
		}
		return LegacyV1Artifact{"classification": "acceptable", "reason": reason}
	}
	if beforeStatus != "mapped" && afterStatus == "mapped" {
		return LegacyV1Artifact{"classification": "review", "reason": "new_mapping"}
	}
	if beforeStatus == "mapped" && afterStatus == "ambiguous_source_operation" {
		return LegacyV1Artifact{"classification": "review", "reason": "mapped_to_ambiguous"}
	}
	if !reflect.DeepEqual(before["list_path"], after["list_path"]) {
		return LegacyV1Artifact{"classification": "review", "reason": "list_path_changed"}
	}
	if !reflect.DeepEqual(beforeRead, afterRead) {
		return LegacyV1Artifact{"classification": "review", "reason": "read_path_changed"}
	}
	if !reflect.DeepEqual(beforeStatus, afterStatus) {
		return LegacyV1Artifact{"classification": "review", "reason": "status_changed"}
	}
	return LegacyV1Artifact{"classification": "review", "reason": "diagnostic_changed"}
}

// ClassifyLegacyV1SourceEvidenceComparison classifies a materialized legacy
// source-operation comparison artifact; it does not invoke a mapper.
func ClassifyLegacyV1SourceEvidenceComparison(comparison LegacyV1Artifact) LegacyV1Artifact {
	changes := make([]any, 0)
	counts := map[string]float64{"acceptable": 0, "regression": 0, "review": 0}
	reasons := map[string]float64{}
	for _, change := range legacyV1Objects(comparison["changes"]) {
		verdict := ClassifyLegacyV1SourceEvidenceChange(change)
		classification := legacyV1JavaScriptString(verdict["classification"])
		reason := legacyV1JavaScriptString(verdict["reason"])
		counts[classification]++
		reasons[reason]++
		annotated := make(LegacyV1Artifact, len(change)+2)
		for key, value := range change {
			annotated[key] = value
		}
		annotated["classification"] = classification
		annotated["classification_reason"] = reason
		changes = append(changes, annotated)
	}
	summary := legacyV1Object(comparison["summary"])
	sortedReasons := make(LegacyV1Artifact, len(reasons))
	for _, reason := range canonjson.SortedStrings(mapKeys(reasons)) {
		sortedReasons[reason] = reasons[reason]
	}
	return LegacyV1Artifact{
		"changes": changes,
		"summary": LegacyV1Artifact{
			"acceptable":      counts["acceptable"],
			"candidate":       legacyV1Object(summary["candidate"]),
			"changed":         float64(len(changes)),
			"control":         legacyV1Object(summary["control"]),
			"reasons":         sortedReasons,
			"regressions":     counts["regression"],
			"resources":       legacyV1Default(summary, "resources", float64(0)),
			"review_required": counts["review"],
			"unchanged":       legacyV1Default(summary, "unchanged", float64(0)),
		},
	}
}

func legacyV1OperationCallCount(detail LegacyV1Artifact) float64 {
	var count float64
	for _, key := range []string{"client_call_count", "package_call_count", "raw_rest_call_count"} {
		if legacyV1Truthy(detail[key]) {
			count += legacyV1Number(detail[key])
		}
	}
	return count
}

func legacyV1UnmappedBucket(reason any, detail LegacyV1Artifact) string {
	if reason == "no_source_operation_match" {
		if legacyV1OperationCallCount(detail) > 0 || legacyV1Truthy(detail["candidate_count"]) {
			return "calls_without_openapi_match"
		}
		return "source_files_without_operation_calls"
	}
	if legacyV1Truthy(reason) {
		return legacyV1JavaScriptString(reason)
	}
	return "unmapped_without_reason"
}

func legacyV1CandidateSamples(candidates any, limit int) []any {
	keys := []string{"client_symbol", "operation_id", "method", "path", "path_kind", "source_role", "read_score", "list_score"}
	objects := legacyV1Objects(candidates)
	if len(objects) > limit {
		objects = objects[:limit]
	}
	samples := make([]any, 0, len(objects))
	for _, candidate := range objects {
		sample := LegacyV1Artifact{}
		for _, key := range keys {
			if value, ok := candidate[key]; ok {
				sample[key] = value
			}
		}
		samples = append(samples, sample)
	}
	return samples
}

func legacyV1AddShortcoming(shortcomings map[string]LegacyV1Artifact, bucket string, resource any, detail LegacyV1Artifact) {
	entry, ok := shortcomings[bucket]
	if !ok {
		severity, known := legacyV1ShortcomingSeverity[bucket]
		if !known {
			severity = "review"
		}
		entry = LegacyV1Artifact{"count": float64(0), "resources": []any{}, "severity": severity}
	}
	entry["count"] = legacyV1Number(entry["count"]) + 1
	resources, _ := entry["resources"].([]any)
	resourceDetail := make(LegacyV1Artifact, len(detail)+1)
	resourceDetail["resource"] = resource
	for key, value := range detail {
		resourceDetail[key] = value
	}
	entry["resources"] = append(resources, resourceDetail)
	shortcomings[bucket] = entry
}

// SummarizeLegacyV1SourceEvidenceShortcomings preserves the frozen v1
// shortcomings artifact. Its result is diagnostic-only and not a v2 report.
func SummarizeLegacyV1SourceEvidenceShortcomings(candidateReport, evaluation LegacyV1Artifact) LegacyV1Artifact {
	shortcomings := map[string]LegacyV1Artifact{}
	registry := legacyV1Object(candidateReport["registry"])
	diagnostics := map[string]LegacyV1Artifact{}
	for _, item := range legacyV1Objects(candidateReport["diagnostics"]) {
		diagnostics[legacyV1JavaScriptString(item["resource"])] = item
	}
	for _, change := range legacyV1Objects(evaluation["changes"]) {
		if change["classification"] == "regression" {
			legacyV1AddShortcoming(shortcomings, "regression", change["resource"], LegacyV1Artifact{
				"after":  legacyV1Object(change["after"]),
				"before": legacyV1Object(change["before"]),
				"reason": change["classification_reason"],
			})
		}
	}
	for _, resource := range canonjson.SortedStrings(mapKeys(registry)) {
		entry := legacyV1Object(registry[resource])
		diagnostic := diagnostics[resource]
		source := legacyV1Object(entry["source"])
		status, reason := entry["status"], entry["reason"]
		detail := LegacyV1Artifact{
			"candidate_count":     legacyV1Default(source, "candidate_count", float64(0)),
			"client_call_count":   legacyV1Default(source, "client_call_count", float64(0)),
			"files":               legacyV1FirstTruthy(source["files"], diagnostic["files"], []any{}),
			"package_call_count":  legacyV1Default(source, "package_call_count", float64(0)),
			"raw_rest_call_count": legacyV1Default(source, "raw_rest_call_count", float64(0)),
			"reason":              reason,
			"status":              status,
		}
		for _, key := range []string{"client_calls", "package_calls", "raw_rest_calls"} {
			if legacyV1Truthy(source[key]) {
				if calls, ok := source[key].([]any); ok {
					if len(calls) > 10 {
						calls = calls[:10]
					}
					detail[key] = calls
				}
			}
		}
		candidates := legacyV1FirstTruthy(entry["candidates"], diagnostic["ambiguous"], diagnostic["hits"], []any{})
		if legacyV1Truthy(candidates) {
			detail["candidate_samples"] = legacyV1CandidateSamples(candidates, 5)
		}
		switch status {
		case "ambiguous_source_operation":
			legacyV1AddShortcoming(shortcomings, "ambiguous_source_operation", resource, detail)
		case "graphql_source":
			legacyV1AddShortcoming(shortcomings, "graphql_source", resource, detail)
		case "mapped":
			read := legacyV1Object(entry["read"])
			if legacyV1Truthy(read) && !legacyV1Truthy(legacyV1Object(entry["list"])) {
				mappedDetail := make(LegacyV1Artifact, len(detail)+2)
				for key, value := range detail {
					mappedDetail[key] = value
				}
				mappedDetail["read_operation_id"] = read["operation_id"]
				mappedDetail["read_path"] = read["path"]
				legacyV1AddShortcoming(shortcomings, "mapped_read_without_list", resource, mappedDetail)
			}
		default:
			legacyV1AddShortcoming(shortcomings, legacyV1UnmappedBucket(reason, detail), resource, detail)
		}
	}

	buckets := LegacyV1Artifact{}
	for _, bucket := range canonjson.SortedStrings(mapKeys(shortcomings)) {
		detail := shortcomings[bucket]
		resources, _ := detail["resources"].([]any)
		sort.SliceStable(resources, func(left, right int) bool {
			return canonjson.ComparePythonStrings(legacyV1JavaScriptString(legacyV1Object(resources[left])["resource"]), legacyV1JavaScriptString(legacyV1Object(resources[right])["resource"])) < 0
		})
		buckets[bucket] = LegacyV1Artifact{"count": detail["count"], "resources": resources, "severity": detail["severity"]}
	}
	severity := map[string]float64{}
	for _, bucket := range buckets {
		detail := legacyV1Object(bucket)
		name := legacyV1JavaScriptString(legacyV1Default(detail, "severity", "review"))
		severity[name] += legacyV1Number(legacyV1Default(detail, "count", 0))
	}
	severitySummary := LegacyV1Artifact{}
	for _, name := range canonjson.SortedStrings(mapKeys(severity)) {
		severitySummary[name] = severity[name]
	}
	summary := LegacyV1Artifact{}
	for _, bucket := range canonjson.SortedStrings(mapKeys(buckets)) {
		summary[bucket] = legacyV1Object(buckets[bucket])["count"]
	}
	return LegacyV1Artifact{"buckets": buckets, "severity_summary": severitySummary, "summary": summary}
}

func legacyV1StatusTable(summary LegacyV1Artifact) string {
	control, candidate := legacyV1Object(summary["control"]), legacyV1Object(summary["candidate"])
	names := []string{"resources", "mapped", "ambiguous", "graphql_source", "unmapped", "resources_with_source_files"}
	lines := []string{"| Metric | Text Scanner | AST Facts |", "|---|---:|---:|"}
	for _, name := range names {
		lines = append(lines, "| `"+name+"` | `"+legacyV1Display(legacyV1Default(control, name, 0))+"` | `"+legacyV1Display(legacyV1Default(candidate, name, 0))+"` |")
	}
	return strings.Join(lines, "\n")
}

// RenderLegacyV1SourceEvidenceMarkdown renders the retained v1 Markdown
// bytes, including the 100-row cap and LF-only line endings. Omit title to
// use the Node default; an explicitly empty title remains explicitly empty.
func RenderLegacyV1SourceEvidenceMarkdown(evaluation LegacyV1Artifact, titles ...string) string {
	title := "Source Evidence A/B Evaluation"
	if len(titles) > 0 {
		title = titles[0]
	}
	summary := legacyV1Object(evaluation["summary"])
	lines := []string{
		"# " + title, "", legacyV1StatusTable(summary), "", "## Delta Summary", "",
		"| Classification | Count |", "|---|---:|",
		"| `regression` | `" + legacyV1Display(legacyV1Default(summary, "regressions", 0)) + "` |",
		"| `review` | `" + legacyV1Display(legacyV1Default(summary, "review_required", 0)) + "` |",
		"| `acceptable` | `" + legacyV1Display(legacyV1Default(summary, "acceptable", 0)) + "` |",
		"| `unchanged` | `" + legacyV1Display(legacyV1Default(summary, "unchanged", 0)) + "` |", "",
	}
	reasons := legacyV1Object(summary["reasons"])
	if len(reasons) > 0 {
		lines = append(lines, "## Reasons", "", "| Reason | Count |", "|---|---:|")
		for _, reason := range canonjson.SortedStrings(mapKeys(reasons)) {
			lines = append(lines, "| `"+reason+"` | `"+legacyV1Display(reasons[reason])+"` |")
		}
		lines = append(lines, "")
	}
	changes := append([]LegacyV1Artifact(nil), legacyV1Objects(evaluation["changes"])...)
	sort.SliceStable(changes, func(left, right int) bool {
		leftOrder, leftKnown := legacyV1ChangeClassOrder[legacyV1JavaScriptString(changes[left]["classification"])]
		rightOrder, rightKnown := legacyV1ChangeClassOrder[legacyV1JavaScriptString(changes[right]["classification"])]
		if !leftKnown {
			leftOrder = 99
		}
		if !rightKnown {
			rightOrder = 99
		}
		if leftOrder != rightOrder {
			return leftOrder < rightOrder
		}
		return canonjson.ComparePythonStrings(legacyV1JavaScriptString(changes[left]["resource"]), legacyV1JavaScriptString(changes[right]["resource"])) < 0
	})
	if len(changes) > 0 {
		shown := changes
		if len(shown) > LegacyV1MaxMarkdownChangeRows {
			shown = shown[:LegacyV1MaxMarkdownChangeRows]
		}
		lines = append(lines, "## Changes", "", "| Resource | Class | Reason | Before | After |", "|---|---|---|---|---|")
		for _, change := range shown {
			before, after := legacyV1Object(change["before"]), legacyV1Object(change["after"])
			lines = append(lines, "| `"+legacyV1Display(change["resource"])+"` | `"+legacyV1Display(change["classification"])+"` | `"+legacyV1Display(change["classification_reason"])+"` | "+legacyV1Display(before["status"])+" `"+legacyV1Display(before["read_path"])+"` | "+legacyV1Display(after["status"])+" `"+legacyV1Display(after["read_path"])+"` |")
		}
		if len(changes) > len(shown) {
			lines = append(lines, "", "Showing `"+strconv.Itoa(len(shown))+"` of `"+strconv.Itoa(len(changes))+"` changes; full detail is in JSON.")
		}
		lines = append(lines, "")
	}
	buckets := legacyV1Object(legacyV1Object(evaluation["shortcomings"])["buckets"])
	if len(buckets) > 0 {
		names := mapKeys(buckets)
		sort.SliceStable(names, func(left, right int) bool {
			leftSeverity := legacyV1JavaScriptString(legacyV1Object(buckets[names[left]])["severity"])
			rightSeverity := legacyV1JavaScriptString(legacyV1Object(buckets[names[right]])["severity"])
			leftOrder, leftKnown := legacyV1SeverityOrder[leftSeverity]
			rightOrder, rightKnown := legacyV1SeverityOrder[rightSeverity]
			if !leftKnown {
				leftOrder = 99
			}
			if !rightKnown {
				rightOrder = 99
			}
			if leftOrder != rightOrder {
				return leftOrder < rightOrder
			}
			return canonjson.ComparePythonStrings(names[left], names[right]) < 0
		})
		lines = append(lines, "## Shortcomings", "", "| Bucket | Severity | Count | Sample Resources |", "|---|---|---:|---|")
		for _, bucket := range names {
			detail := legacyV1Object(buckets[bucket])
			resources := legacyV1Objects(detail["resources"])
			if len(resources) > 8 {
				resources = resources[:8]
			}
			samples := make([]string, 0, len(resources)+1)
			for _, resource := range resources {
				samples = append(samples, "`"+legacyV1Display(resource["resource"])+"`")
			}
			if legacyV1Number(legacyV1Default(detail, "count", 0)) > float64(len(resources)) {
				samples = append(samples, "...")
			}
			lines = append(lines, "| `"+bucket+"` | `"+legacyV1Display(legacyV1Default(detail, "severity", "review"))+"` | `"+legacyV1Display(legacyV1Default(detail, "count", 0))+"` | "+strings.Join(samples, ", ")+" |")
		}
		lines = append(lines, "")
	}
	return strings.Join(lines, "\n")
}

// EvaluateLegacyV1SourceEvidence evaluates a candidate report against an
// already-materialized comparison artifact. Keeping the comparator outside
// this package prevents this compatibility layer from becoming a v2 mapper.
func EvaluateLegacyV1SourceEvidence(candidateReport, comparison LegacyV1Artifact) LegacyV1Artifact {
	evaluation := ClassifyLegacyV1SourceEvidenceComparison(comparison)
	evaluation["shortcomings"] = SummarizeLegacyV1SourceEvidenceShortcomings(candidateReport, evaluation)
	return evaluation
}

// LegacyV1FailOnRegressionAfterArtifacts is the legacy CLI decision helper.
// Call it only after every diagnostic artifact has been published.
func LegacyV1FailOnRegressionAfterArtifacts(evaluation LegacyV1Artifact) bool {
	summary := legacyV1Object(evaluation["summary"])
	return legacyV1Number(legacyV1Default(summary, "regressions", 0)) > 0
}

func mapKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	return keys
}
