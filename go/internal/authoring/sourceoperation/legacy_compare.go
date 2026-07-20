package sourceoperation

import (
	"encoding/json"
	"reflect"
)

// CompareLegacySourceOperationReports ports the frozen v1 comparison helper.
// It compares only the historical v1 report signature and is not part of v2
// readiness or source-first accounting.
func CompareLegacySourceOperationReports(control, candidate map[string]any) map[string]any {
	beforeRegistry, afterRegistry := legacyObject(control["registry"]), legacyObject(candidate["registry"])
	resources := map[string]bool{}
	for resource := range beforeRegistry {
		resources[resource] = true
	}
	for resource := range afterRegistry {
		resources[resource] = true
	}
	names := make([]string, 0, len(resources))
	for resource := range resources {
		names = append(names, resource)
	}
	names = legacySorted(names)
	changes := []any{}
	unchanged, statuses, reads, files := 0, 0, 0, 0
	for _, resource := range names {
		before, after := legacyRegistrySignature(legacyObject(beforeRegistry[resource])), legacyRegistrySignature(legacyObject(afterRegistry[resource]))
		if legacyMapJSONEqual(before, after) {
			unchanged++
			continue
		}
		if before["status"] != after["status"] {
			statuses++
		}
		if before["read_path"] != after["read_path"] {
			reads++
		}
		if !legacyJSONValueEqual(before["files"], after["files"]) {
			files++
		}
		changes = append(changes, map[string]any{"after": after, "before": before, "resource": resource})
	}
	return map[string]any{"changes": changes, "summary": map[string]any{"candidate": legacyObject(candidate["summary"]), "changed": len(changes), "control": legacyObject(control["summary"]), "file_changes": files, "read_path_changes": reads, "resources": len(names), "status_changes": statuses, "unchanged": unchanged}}
}

func legacyRegistrySignature(entry map[string]any) map[string]any {
	read, listing, source := legacyObject(entry["read"]), legacyObject(entry["list"]), legacyObject(entry["source"])
	return map[string]any{"candidate_count": legacyValueOr(source["candidate_count"], 0), "client_call_count": legacyValueOr(source["client_call_count"], 0), "files": legacyValueOr(source["files"], []any{}), "graphql": legacyBool(source["graphql"]), "list_operation_id": legacyValueOr(listing["operation_id"], nil), "list_path": legacyValueOr(listing["path"], nil), "package_call_count": legacyValueOr(source["package_call_count"], 0), "raw_rest_call_count": legacyValueOr(source["raw_rest_call_count"], 0), "read_evidence_kind": legacyValueOr(read["evidence_kind"], nil), "read_operation_id": legacyValueOr(read["operation_id"], nil), "read_path": legacyValueOr(read["path"], nil), "reason": legacyValueOr(entry["reason"], nil), "status": legacyValueOr(entry["status"], nil)}
}
func legacyValueOr(value, fallback any) any {
	if value == nil {
		return fallback
	}
	return value
}

func legacyMapJSONEqual(left, right map[string]any) bool { return legacyJSONValueEqual(left, right) }
func legacyJSONValueEqual(left, right any) bool {
	leftBytes, leftErr := json.Marshal(left)
	rightBytes, rightErr := json.Marshal(right)
	if leftErr != nil || rightErr != nil {
		return false
	}
	var leftValue, rightValue any
	if json.Unmarshal(leftBytes, &leftValue) != nil || json.Unmarshal(rightBytes, &rightValue) != nil {
		return false
	}
	return reflect.DeepEqual(leftValue, rightValue)
}
