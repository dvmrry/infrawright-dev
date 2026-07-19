package openapiadapter

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/dvmrry/infrawright-dev/go/internal/authoring/contracts"
)

func inventoryRequired(ctx context.Context, document Document, source contracts.SourceEvidenceReport) ([]Operation, error) {
	root, _ := document.files[document.root].(map[string]any)
	paths, _ := root["paths"].(map[string]any)
	operations := []Operation{}
	for _, row := range source.Resources {
		if row.Classification != contracts.SourceObservedHTTP || len(row.Chains) != 1 || row.Chains[0].Endpoint == nil {
			continue
		}
		endpoint := *row.Chains[0].Endpoint
		for _, pathTemplate := range sortedKeys(paths) {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if !viable(Operation{Method: endpoint.Method, PathTemplate: pathTemplate}, endpoint.Method, endpoint.PathTemplate) {
				continue
			}
			item, _, err := resolvePathItem(document, document.root, paths[pathTemplate], map[string]bool{}, 0)
			if err != nil {
				return nil, err
			}
			value, exists := item[strings.ToLower(endpoint.Method)]
			if !exists {
				continue
			}
			operation, ok := value.(map[string]any)
			if _, present := operation["$ref"]; !ok || present {
				return nil, fmt.Errorf("relevant operation must be an object without $ref")
			}
			candidate := Operation{Method: endpoint.Method, PathTemplate: pathTemplate}
			if id := stringValue(operation["operationId"]); id != "" {
				candidate.OperationID = &id
			}
			operations = append(operations, candidate)
		}
	}
	sortOperations(operations)
	return uniqueOperations(operations), nil
}

func uniqueOperations(values []Operation) []Operation {
	if len(values) < 2 {
		return values
	}
	out := values[:1]
	for _, value := range values[1:] {
		previous := out[len(out)-1]
		if value.Method != previous.Method || value.PathTemplate != previous.PathTemplate || stringValuePointer(value.OperationID) != stringValuePointer(previous.OperationID) {
			out = append(out, value)
		}
	}
	return out
}
func stringValuePointer(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func comparisonRows(source contracts.SourceEvidenceReport, state contracts.OpenAPIDocumentState, operations []Operation) map[string]contracts.OpenAPIComparisonRow {
	result := make(map[string]contracts.OpenAPIComparisonRow, len(source.Resources))
	for resource, row := range source.Resources {
		if state == contracts.OpenAPIAbsent || state == contracts.OpenAPIUnavailable {
			result[resource] = contracts.OpenAPIComparisonRow{State: contracts.ComparisonNotAttempted, Operations: []contracts.OpenAPIOperationCandidate{}}
			continue
		}
		if row.Classification != contracts.SourceObservedHTTP || len(row.Chains) != 1 || row.Chains[0].Endpoint == nil {
			result[resource] = contracts.OpenAPIComparisonRow{State: contracts.ComparisonNotComparable, Operations: []contracts.OpenAPIOperationCandidate{}}
			continue
		}
		endpoint := row.Chains[0].Endpoint
		candidates := make([]Operation, 0)
		for _, operation := range operations {
			if viable(operation, endpoint.Method, endpoint.PathTemplate) {
				candidates = append(candidates, operation)
			}
		}
		sortOperations(candidates)
		switch len(candidates) {
		case 0:
			result[resource] = contracts.OpenAPIComparisonRow{State: contracts.ComparisonMissingPath, Operations: []contracts.OpenAPIOperationCandidate{}}
		case 1:
			candidate := candidate(candidates[0])
			// The report validator requires the source spelling for a sole
			// parameter-name-only match; ambiguity must retain literal paths.
			candidate.PathTemplate = endpoint.PathTemplate
			basis := contracts.ComparisonBasisSourceEndpoint
			result[resource] = contracts.OpenAPIComparisonRow{State: contracts.ComparisonCorroborated, Basis: &basis, Operations: []contracts.OpenAPIOperationCandidate{candidate}}
		default:
			values := make([]contracts.OpenAPIOperationCandidate, len(candidates))
			for i := range candidates {
				values[i] = candidate(candidates[i])
			}
			basis := contracts.ComparisonBasisSourceEndpoint
			result[resource] = contracts.OpenAPIComparisonRow{State: contracts.ComparisonAmbiguous, Basis: &basis, Operations: values}
		}
	}
	return result
}

func summary(source contracts.SourceEvidenceReport, state contracts.OpenAPIDocumentState, rows map[string]contracts.OpenAPIComparisonRow) contracts.OpenAPIComparisonSummary {
	var counts contracts.OpenAPIComparisonCounts
	for _, row := range rows {
		switch row.State {
		case contracts.ComparisonNotAttempted:
			counts.NotAttempted++
		case contracts.ComparisonNotComparable:
			counts.NotComparable++
		case contracts.ComparisonCorroborated:
			counts.Corroborated++
		case contracts.ComparisonMissingPath:
			counts.MissingPath++
		case contracts.ComparisonAmbiguous:
			counts.Ambiguous++
		case contracts.ComparisonConflict:
			counts.Conflict++
		}
	}
	degraded := 0
	if state == contracts.OpenAPIDegraded {
		degraded = source.Summary.ClassificationCounts.ObservedHTTP
	}
	return contracts.OpenAPIComparisonSummary{ComparisonEligibleTotal: source.Summary.ClassificationCounts.ObservedHTTP, DegradedComparisonTotal: degraded, ComparisonCounts: counts}
}

func candidate(operation Operation) contracts.OpenAPIOperationCandidate {
	return contracts.OpenAPIOperationCandidate{Method: operation.Method, PathTemplate: operation.PathTemplate, OperationID: cloneString(operation.OperationID)}
}
func sortOperations(values []Operation) {
	sort.Slice(values, func(i, j int) bool {
		left, right := values[i], values[j]
		li, ri := "", ""
		if left.OperationID != nil {
			li = *left.OperationID
		}
		if right.OperationID != nil {
			ri = *right.OperationID
		}
		return left.Method+"\x00"+left.PathTemplate+"\x00"+li < right.Method+"\x00"+right.PathTemplate+"\x00"+ri
	})
}

func viable(operation Operation, method, endpoint string) bool {
	if operation.Method != method {
		return false
	}
	left, right := strings.Split(operation.PathTemplate, "/"), strings.Split(endpoint, "/")
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] && !(template(left[index]) && template(right[index])) {
			return false
		}
	}
	return true
}
func template(value string) bool {
	return len(value) > 2 && value[0] == '{' && value[len(value)-1] == '}' && !strings.ContainsAny(value[1:len(value)-1], "{}")
}

func resolveRef(document Document, current, ref string) (string, any, string, error) {
	if document.metadataOnly && !strings.HasPrefix(ref, "#/") {
		return "", nil, "", fmt.Errorf("metadata references must use a fragment-local JSON pointer")
	}
	file, fragment, err := splitRef(current, ref)
	if err != nil {
		return "", nil, "", err
	}
	if document.metadataOnly && file != document.root {
		return "", nil, "", fmt.Errorf("external metadata references are unsupported")
	}
	root, err := documentGraph(document, file)
	if err != nil {
		return "", nil, "", err
	}
	target, err := pointer(root, fragment)
	if err != nil {
		return "", nil, "", err
	}
	return file, target, file + "#" + fragment, nil
}

func documentGraph(document Document, file string) (any, error) {
	if value, ok := document.files[file]; ok {
		return value, nil
	}
	data, ok := document.raw[file]
	if !ok {
		return nil, fmt.Errorf("unlisted local reference")
	}
	value, err := parseDocument(data)
	if err != nil {
		return nil, fmt.Errorf("malformed local reference")
	}
	return value, nil
}

func resolvePathItem(document Document, current string, value any, active map[string]bool, depth int) (map[string]any, string, error) {
	if depth > maxRefDepth {
		return nil, "", fmt.Errorf("reference recursion limit")
	}
	item, ok := value.(map[string]any)
	if !ok {
		return nil, "", fmt.Errorf("path item is not object")
	}
	raw, present := item["$ref"]
	if !present {
		return item, current, nil
	}
	ref, ok := raw.(string)
	if !ok || strings.ContainsRune(ref, '\x00') {
		return nil, "", fmt.Errorf("path item reference must be string")
	}
	file, target, key, err := resolveRef(document, current, ref)
	if err != nil {
		return nil, "", err
	}
	if active[key] {
		return nil, "", fmt.Errorf("path item reference cycle")
	}
	active[key] = true
	resolved, resolvedFile, err := resolvePathItem(document, file, target, active, depth+1)
	delete(active, key)
	return resolved, resolvedFile, err
}

func splitRef(current, ref string) (string, string, error) {
	if ref == "" || strings.Contains(ref, "\\") || strings.ContainsRune(ref, '\x00') || strings.Contains(ref, "%") || strings.Contains(ref, "?") {
		return "", "", fmt.Errorf("unsafe reference")
	}
	parts := strings.SplitN(ref, "#", 2)
	if len(parts) == 2 && strings.Contains(parts[1], "#") {
		return "", "", fmt.Errorf("invalid fragment")
	}
	fragment := ""
	if len(parts) == 2 {
		fragment = parts[1]
		if fragment != "" && !strings.HasPrefix(fragment, "/") {
			return "", "", fmt.Errorf("invalid JSON pointer")
		}
	}
	if parts[0] == "" {
		return current, fragment, nil
	}
	if strings.Contains(parts[0], ":") || strings.HasPrefix(parts[0], "/") || strings.Contains(parts[0], "//") {
		return "", "", fmt.Errorf("external reference")
	}
	candidate := pathJoin(current, parts[0])
	key, err := canonicalPath(candidate)
	if err != nil {
		return "", "", err
	}
	return key, fragment, nil
}
func pathJoin(current, relative string) string {
	base := strings.Split(current, "/")
	if len(base) > 1 {
		base = base[:len(base)-1]
	} else {
		base = nil
	}
	return strings.Join(append(base, strings.Split(relative, "/")...), "/")
}

func pointer(root any, fragment string) (any, error) {
	if fragment == "" {
		return root, nil
	}
	value := root
	for _, raw := range strings.Split(strings.TrimPrefix(fragment, "/"), "/") {
		token, err := pointerToken(raw)
		if err != nil {
			return nil, err
		}
		switch current := value.(type) {
		case map[string]any:
			var ok bool
			value, ok = current[token]
			if !ok {
				return nil, fmt.Errorf("pointer does not exist")
			}
		case []any:
			index := 0
			if token == "" || strings.HasPrefix(token, "+") {
				return nil, fmt.Errorf("invalid array pointer")
			}
			for _, char := range token {
				if char < '0' || char > '9' || index > (len(current)-int(char-'0'))/10 {
					return nil, fmt.Errorf("invalid array pointer")
				}
				index = index*10 + int(char-'0')
			}
			if index >= len(current) {
				return nil, fmt.Errorf("pointer out of range")
			}
			value = current[index]
		default:
			return nil, fmt.Errorf("pointer crosses scalar")
		}
	}
	return value, nil
}
func pointerToken(value string) (string, error) {
	for i := 0; i < len(value); i++ {
		if value[i] == '~' {
			if i+1 >= len(value) || (value[i+1] != '0' && value[i+1] != '1') {
				return "", fmt.Errorf("invalid JSON pointer escape")
			}
			i++
		}
	}
	return strings.NewReplacer("~1", "/", "~0", "~").Replace(value), nil
}
