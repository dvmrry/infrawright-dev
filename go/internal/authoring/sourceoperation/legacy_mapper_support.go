package sourceoperation

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/dvmrry/infrawright-dev/go/internal/canonjson"
)

func legacySortHits(hits []legacyHit, list bool) {
	sort.SliceStable(hits, func(left, right int) bool {
		leftScore, rightScore := hits[left].ReadScore, hits[right].ReadScore
		if list {
			leftScore, rightScore = hits[left].ListScore, hits[right].ListScore
		}
		if leftScore != rightScore {
			return leftScore > rightScore
		}
		if compared := canonjson.ComparePythonStrings(hits[left].Path, hits[right].Path); compared != 0 {
			return compared < 0
		}
		return canonjson.ComparePythonStrings(hits[left].OperationID, hits[right].OperationID) < 0
	})
}
func legacyDedupe(hits []legacyHit) []legacyHit {
	groups := map[string]int{}
	var out []legacyHit
	for _, item := range hits {
		key := strings.Join([]string{item.Method, item.Path, item.OperationID, item.PathKind, item.SourceRole}, "\x00")
		if index, ok := groups[key]; ok {
			existing := &out[index]
			existing.ReadScore = max(existing.ReadScore, item.ReadScore)
			existing.ListScore = max(existing.ListScore, item.ListScore)
			aliases := legacySet(append(existing.MatchedAliases, item.MatchedAliases...))
			existing.MatchedAliases = legacySortedSet(aliases)
			symbols := legacySet(append(append(existing.Alternate, existing.ClientSymbol), item.ClientSymbol))
			delete(symbols, "")
			if len(symbols) > 0 {
				existing.Alternate = legacySortedSet(symbols)
			}
			continue
		}
		groups[key] = len(out)
		out = append(out, item)
	}
	return out
}
func legacySelect(hits []legacyHit, role string) (*legacyHit, []legacyHit) {
	candidates := make([]legacyHit, 0, len(hits))
	for _, item := range hits {
		if item.SourceRole != "" && item.SourceRole != role {
			continue
		}
		if role == "list" && item.PathKind != "list" {
			continue
		}
		candidates = append(candidates, item)
	}
	legacySortHits(candidates, role == "list")
	if len(candidates) == 0 {
		return nil, nil
	}
	best := candidates[0]
	score := best.ReadScore
	if role == "list" {
		score = best.ListScore
	}
	if role == "read" && best.PathKind != "detail" {
		var detail []legacyHit
		for _, item := range candidates[1:] {
			if item.PathKind == "detail" && score-item.ReadScore <= legacyReadDetailDelta {
				detail = append(detail, item)
			}
		}
		if len(detail) > 0 {
			return nil, append([]legacyHit{best}, detail[:min(4, len(detail))]...)
		}
	}
	delta := legacyAmbiguityDelta
	if best.ClientSymbol != "" {
		delta = legacySourceCallDelta
	}
	var ambiguous []legacyHit
	for _, item := range candidates[1:] {
		itemScore := item.ReadScore
		if role == "list" {
			itemScore = item.ListScore
		}
		if item.PathKind == best.PathKind && score-itemScore <= delta {
			ambiguous = append(ambiguous, item)
		}
	}
	if len(ambiguous) > 0 {
		return nil, append([]legacyHit{best}, ambiguous[:min(4, len(ambiguous))]...)
	}
	return &best, nil
}
func legacyRelationship(resource, prefix string) bool {
	tokens := legacyFilteredTokens(resource, prefix)
	joined := strings.Join(tokens, "_")
	for _, phrase := range []string{"secret_repositories", "variable_repositories", "role_team", "role_user", "team_assignment", "user_assignment", "repository_topics", "repository_collaborator", "sync_group_mapping"} {
		if len(tokens) >= 2 && strings.Contains(joined, phrase) {
			return true
		}
	}
	relationships := legacySet([]string{"assignment", "collaborator", "collaborators", "dependency", "dependencies", "mapping", "members", "membership", "repositories", "subscriber", "subscribers", "topics"})
	for _, token := range tokens {
		if relationships[token] {
			return true
		}
	}
	return false
}
func legacyRelationshipHit(hits []legacyHit, resource, prefix string) (*legacyHit, []legacyHit) {
	if !legacyRelationship(resource, prefix) {
		return nil, nil
	}
	relationships := legacySet([]string{"assignment", "collaborator", "collaborators", "dependency", "dependencies", "mapping", "members", "membership", "repositories", "subscriber", "subscribers", "topics"})
	var tokens []string
	for _, token := range legacyFilteredTokens(resource, prefix) {
		if relationships[token] {
			tokens = append(tokens, token)
		}
	}
	var candidates []legacyHit
	for _, item := range hits {
		if item.SourceRole == "list" {
			candidates = append(candidates, item)
		}
	}
	var with []legacyHit
	for _, item := range candidates {
		for _, token := range tokens {
			if legacyMention(item.legacyOperation, token) {
				with = append(with, item)
				break
			}
		}
	}
	if len(with) > 0 {
		candidates = with
	}
	legacySortHits(candidates, false)
	if len(candidates) == 0 {
		return nil, nil
	}
	best := candidates[0]
	var ambiguous []legacyHit
	for _, item := range candidates[1:] {
		if best.ReadScore-item.ReadScore <= legacyAmbiguityDelta {
			ambiguous = append(ambiguous, item)
		}
	}
	if len(ambiguous) > 0 {
		return nil, append([]legacyHit{best}, ambiguous[:min(4, len(ambiguous))]...)
	}
	return &best, nil
}
func legacyCandidateEntry(item legacyHit) map[string]any {
	out := map[string]any{"list_score": item.ListScore, "method": item.Method, "operation_id": item.OperationID, "path": item.Path, "path_kind": item.PathKind, "read_score": item.ReadScore}
	if item.OperationIDSource != "openapi" {
		out["operation_id_source"] = item.OperationIDSource
	}
	if item.ClientSymbol != "" {
		out["client_symbol"] = item.ClientSymbol
	}
	if item.SourceRole != "" {
		out["source_role"] = item.SourceRole
	}
	if len(item.Alternate) > 0 {
		out["alternate_client_symbols"] = item.Alternate
	}
	return out
}
func legacyOperationEntry(item legacyHit, kind string, files []string) map[string]any {
	openapi := map[string]any{"kind": "openapi_operation", "method": item.Method, "operation_id": item.OperationID, "path": item.Path}
	if item.OperationIDSource != "openapi" {
		openapi["operation_id_source"] = item.OperationIDSource
	}
	symbol := item.ClientSymbol
	if symbol == "" {
		symbol = item.OperationID
	}
	provider := map[string]any{"client_symbol": symbol, "kind": "provider_call", "matched_aliases": item.MatchedAliases, "source_files": files}
	for key, value := range map[string]string{"sdk_method": item.SDKMethod, "sdk_package": item.SDKPackage, "sdk_package_path": item.SDKPackagePath, "raw_rest_path": item.RawPath, "source_role": item.SourceRole} {
		if value != "" {
			provider[key] = value
		}
	}
	if len(item.Alternate) > 0 {
		provider["alternate_client_symbols"] = item.Alternate
	}
	hops := []any{provider}
	if item.SDKPathTemplate != "" {
		hops = append(hops, map[string]any{"kind": "sdk_path", "method": firstNonEmpty(item.SDKPathMethod, item.Method), "path_template": item.SDKPathTemplate, "sdk_file": nullableString(item.SDKPathFile)})
	}
	hops = append(hops, openapi)
	out := map[string]any{"confidence": "high", "evidence_kind": kind, "hops": hops, "method": item.Method, "operation_id": item.OperationID, "path": item.Path, "path_kind": item.PathKind}
	if item.OperationIDSource != "openapi" {
		out["operation_id_source"] = item.OperationIDSource
	}
	return out
}
func legacyCandidateOperation(item legacyHit, kind string, files []string) map[string]any {
	out := legacyOperationEntry(item, kind, files)
	out["confidence"] = "low"
	out["list_score"] = item.ListScore
	out["read_score"] = item.ReadScore
	return out
}
func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}
func firstNonEmpty(left, right string) string {
	if left != "" {
		return left
	}
	return right
}
func legacyProviderFromSchema(data map[string]any, source string) (map[string]any, error) {
	if _, ok := data["resource_schemas"]; ok {
		return data, nil
	}
	providers := legacyObject(data["provider_schemas"])
	if source != "" {
		if candidate, ok := providers[source].(map[string]any); ok {
			return candidate, nil
		}
		var matches []map[string]any
		for name, value := range providers {
			if strings.HasSuffix(name, "/"+source) {
				if candidate, ok := value.(map[string]any); ok {
					matches = append(matches, candidate)
				}
			}
		}
		if len(matches) == 1 {
			return matches[0], nil
		}
		return nil, fmt.Errorf("provider source %q not found", source)
	}
	var values []map[string]any
	for _, value := range providers {
		if candidate, ok := value.(map[string]any); ok {
			values = append(values, candidate)
		}
	}
	if len(values) == 1 {
		return values[0], nil
	}
	return nil, errors.New("schema has multiple providers; pass providerSource")
}
func legacyResourceFiles(root string, resources []string, prefix string) (map[string][]string, error) {
	entries, err := DiscoverProviderGoFiles(root)
	if err != nil {
		return nil, err
	}
	functions := map[string][]string{}
	for _, filename := range entries {
		text, _ := os.ReadFile(filename)
		for _, match := range legacyFunction.FindAllStringSubmatch(GoCodeWithoutCommentsAndStrings(string(text)), -1) {
			functions[match[1]] = append(functions[match[1]], filename)
		}
	}
	qualified := regexp.MustCompile(`"([^"]+)"\s*:\s*([A-Za-z_][A-Za-z0-9_]*)\.([A-Za-z_][A-Za-z0-9_]*)\s*\(`)
	out := map[string][]string{}
	for _, resource := range resources {
		set := map[string]bool{}
		registrations := map[string]bool{}
		exact := false
		bare := resource
		if prefix != "" && strings.HasPrefix(resource, prefix+"_") {
			bare = resource[len(prefix)+1:]
		}
		names := legacySet([]string{"resource_" + resource + ".go", "resource_" + bare + ".go"})
		for _, filename := range entries {
			if names[filepath.Base(filename)] {
				set[filename] = true
				exact = true
			}
		}
		service := filepath.Join(root, "internal", "services", bare)
		if legacyDirExists(service) {
			files, _ := DiscoverProviderGoFiles(service)
			for _, file := range files {
				set[file] = true
				exact = true
			}
		}
		for _, directory := range []string{"resources", "datasources"} {
			candidate := filepath.Join(root, "internal", "framework", directory, bare+".go")
			if legacyFileExists(candidate) {
				set[candidate] = true
				exact = true
			}
		}
		for _, filename := range entries {
			text, _ := os.ReadFile(filename)
			for _, match := range legacyQuoted.FindAllStringSubmatch(string(text), -1) {
				if match[1] == resource {
					set[filename] = true
				}
			}
			code := GoCodeWithoutComments(string(text))
			for _, match := range legacyRegister.FindAllStringSubmatch(code, -1) {
				if match[1] != resource || strings.HasPrefix(CanonicalSourceSymbol(match[2]), "datasource") {
					continue
				}
				for _, target := range functions[match[2]] {
					registrations[target] = true
				}
			}
			imports := GoImportAliases(string(text))
			for _, match := range qualified.FindAllStringSubmatch(code, -1) {
				if match[1] != resource || strings.HasPrefix(CanonicalSourceSymbol(match[3]), "datasource") {
					continue
				}
				directory := legacyLocalImportDirectory(root, imports[match[2]])
				if directory == "" {
					continue
				}
				files, _ := DiscoverProviderGoFiles(directory)
				for _, target := range files {
					if !strings.Contains(filepath.Base(target), "datasource") && !strings.HasPrefix(filepath.Base(target), "data_source_") {
						registrations[target] = true
					}
				}
			}
		}
		for filename := range registrations {
			set[filename] = true
		}
		if exact || len(registrations) > 0 {
			for filename := range set {
				base := filepath.Base(filename)
				if base == "provider.go" || base == "main.go" || strings.HasPrefix(base, "data_source_") {
					delete(set, filename)
				}
			}
		}
		for filename := range set {
			text, _ := os.ReadFile(filename)
			for _, match := range legacyReadCB.FindAllStringSubmatch(GoCodeWithoutCommentsAndStrings(string(text)), -1) {
				for _, target := range functions[match[1]] {
					if filepath.Dir(target) == filepath.Dir(filename) {
						set[target] = true
					}
				}
			}
		}
		paths := make([]string, 0, len(set))
		for path := range set {
			paths = append(paths, path)
		}
		sortLegacyPaths(root, paths)
		out[resource] = paths
	}
	return out, nil
}
func legacyFactsFiles(root string, resources []string, facts map[string]any) map[string][]string {
	out := map[string][]string{}
	for _, resource := range resources {
		out[resource] = nil
	}
	for _, item := range legacyArray(facts["resource_references"]) {
		record := legacyObject(item)
		resource, file := legacyString(record["resource"]), legacyFactPath(root, legacyString(record["file"]))
		if _, ok := out[resource]; ok && file != "" {
			out[resource] = append(out[resource], file)
		}
	}
	for resource := range out {
		out[resource] = legacySorted(out[resource])
	}
	return out
}
func legacyFactPath(root, value string) string {
	if value == "" {
		return ""
	}
	if filepath.IsAbs(value) {
		return filepath.Clean(value)
	}
	return filepath.Join(root, filepath.FromSlash(value))
}
func legacyTextEvidence(root string, files []string) (map[string]bool, []map[string]any, []map[string]any, []map[string]any, bool) {
	var all []string
	for _, filename := range files {
		text, err := os.ReadFile(filename)
		if err == nil {
			all = append(all, string(text))
		}
	}
	joined := strings.Join(all, "\n")
	return GoIdentifierTokens(joined), SDKClientCalls(joined, true), PackageCalls(joined, root), RawRESTCalls(joined), IsGraphQLSource(joined)
}
func legacySelected(files []string) map[string]bool {
	out := map[string]bool{}
	for _, file := range files {
		out[filepath.ToSlash(file)] = true
	}
	return out
}
func legacySDKCallsFromFacts(root string, files []string, facts map[string]any, requireRole bool) []map[string]any {
	selected := legacySelected(files)
	calls := map[string]map[string]any{}
	for _, item := range legacyArray(facts["selector_calls"]) {
		record := legacyObject(item)
		if !selected[filepath.ToSlash(legacyFactPath(root, legacyString(record["file"])))] {
			continue
		}
		var parts []string
		for _, part := range legacyArray(record["parts"]) {
			parts = append(parts, legacyString(part))
		}
		if len(parts) == 0 {
			parts = strings.Split(legacyString(record["symbol"]), ".")
		}
		last := -1
		for i, part := range parts {
			if part == "api" || part == "client" {
				last = i
			}
		}
		if last < 0 || last+1 >= len(parts) {
			continue
		}
		suffix := parts[last+1:]
		method := suffix[len(suffix)-1]
		role := SDKMethodRole(method)
		if role == "" && requireRole {
			continue
		}
		symbol := strings.Join(suffix, ".")
		calls[symbol] = map[string]any{"chain": suffix[:len(suffix)-1], "client_symbol": symbol, "method": method, "source_role": legacyNullableRole(role)}
	}
	return legacyCallMap(calls)
}
