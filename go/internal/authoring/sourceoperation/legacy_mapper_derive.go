package sourceoperation

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// DeriveLegacySourceOperationRegistry reproduces the frozen v1 mapper report.
// The result is historical compatibility output only; it is deliberately not a
// v2 source-analysis or readiness input.
func DeriveLegacySourceOperationRegistry(options LegacyOptions) (map[string]any, error) {
	if options.SourceFacts != nil {
		for _, key := range []string{"files", "functions", "resource_registrations", "resource_references", "identifier_references", "read_callbacks", "selector_calls", "package_calls", "raw_rest_calls"} {
			if _, ok := options.SourceFacts[key].([]any); !ok {
				return nil, fmt.Errorf("malformed source facts: expected arrays for %s", key)
			}
		}
	}
	provider, err := legacyProviderFromSchema(options.SchemaData, options.ProviderSource)
	if err != nil {
		return nil, err
	}
	schemas := legacyObject(provider["resource_schemas"])
	if len(options.Resources) > 0 {
		wanted := legacySorted(append([]string(nil), options.Resources...))
		selected := map[string]any{}
		var missing []string
		for _, resource := range wanted {
			value, ok := schemas[resource]
			if !ok {
				missing = append(missing, resource)
			} else {
				selected[resource] = value
			}
		}
		if len(missing) > 0 {
			return nil, fmt.Errorf("resources not found in provider schema: %s", strings.Join(missing, ", "))
		}
		schemas = selected
	}
	resources := make([]string, 0, len(schemas))
	for resource := range schemas {
		resources = append(resources, resource)
	}
	resources = legacySorted(resources)
	var filesByResource map[string][]string
	if options.SourceFacts != nil {
		filesByResource, err = legacyResourceFiles(options.SourceRoot, resources, options.ResourcePrefix)
		if err != nil {
			return nil, err
		}
		for resource, files := range legacyFactsFiles(options.SourceRoot, resources, options.SourceFacts) {
			set := map[string]bool{}
			for _, file := range filesByResource[resource] {
				set[file] = true
			}
			for _, file := range files {
				set[file] = true
			}
			merged := make([]string, 0, len(set))
			for file := range set {
				merged = append(merged, file)
			}
			sortLegacyPaths(options.SourceRoot, merged)
			filesByResource[resource] = merged
		}
	} else {
		filesByResource, err = legacyResourceFiles(options.SourceRoot, resources, options.ResourcePrefix)
		if err != nil {
			return nil, err
		}
	}
	operations := legacyOperations(options.OpenAPI)
	sdkEvidence, sdkUnresolved, err := ExtractSDKPaths(options.SDKRoot)
	if err != nil {
		return nil, err
	}
	registry := map[string]any{}
	diagnostics := make([]any, 0, len(resources))
	withFiles := 0
	for _, resource := range resources {
		absolute := filesByResource[resource]
		files := make([]string, 0, len(absolute))
		for _, file := range absolute {
			files = append(files, legacyRelative(options.SourceRoot, file))
		}
		if len(absolute) > 0 {
			withFiles++
		}
		var identifiers map[string]bool
		var sdkCalls, packageCalls, rawCalls []map[string]any
		var graphql bool
		if options.SourceFacts != nil {
			identifiers, sdkCalls, packageCalls, rawCalls, graphql = legacyFactsEvidenceFull(options.SourceRoot, absolute, options.SourceFacts)
		} else {
			identifiers, sdkCalls, packageCalls, rawCalls, graphql = legacyTextEvidence(options.SourceRoot, absolute)
		}
		hits := []legacyHit{}
		unresolved, actions := []any{}, []any{}
		schema := legacyObject(schemas[resource])
		if options.SDKRoot != "" {
			var allCalls []map[string]any
			if options.SourceFacts != nil {
				allCalls = legacySDKCallsFromFacts(options.SourceRoot, absolute, options.SourceFacts, false)
			} else {
				var texts []string
				for _, file := range absolute {
					if data, readErr := osRead(file); readErr == nil {
						texts = append(texts, data)
					}
				}
				allCalls = SDKClientCalls(strings.Join(texts, "\n"), false)
			}
			for _, call := range allCalls {
				symbol := legacyString(call["client_symbol"])
				evidence, found := sdkEvidence[symbol]
				if !found {
					item := sdkUnresolved[symbol]
					reason, file := "sdk_symbol_not_found", any(nil)
					if item != nil {
						reason = legacyString(item["reason"])
						file = nullableString(legacyString(item["sdk_file"]))
					}
					unresolved = append(unresolved, map[string]any{"client_symbol": symbol, "reason": reason, "sdk_file": file})
					continue
				}
				method := legacyString(evidence["method"])
				if method != "GET" {
					actions = append(actions, map[string]any{"client_symbol": symbol, "method": method, "path_template": legacyString(evidence["path_template"]), "sdk_file": legacyString(evidence["sdk_file"])})
					continue
				}
				op, ambiguous := legacyMatchOpenAPIBySDKPath(operations, legacyString(evidence["path_template"]), "GET")
				if op == nil {
					record := map[string]any{"client_symbol": symbol, "sdk_file": legacyString(evidence["sdk_file"])}
					if len(ambiguous) > 0 {
						paths := make([]any, 0, len(ambiguous))
						for _, candidate := range ambiguous {
							paths = append(paths, candidate.Path)
						}
						record["ambiguous_openapi_paths"] = paths
						record["reason"] = "openapi_path_ambiguous"
					} else {
						record["reason"] = "openapi_path_not_found"
					}
					unresolved = append(unresolved, record)
					continue
				}
				relevant := legacyPathSequenceScore(resource, options.ResourcePrefix, *op) != 0 || legacyTerminalScore(resource, options.ResourcePrefix, *op) != 0 || legacyPrefixScore(resource, options.ResourcePrefix, *op) != 0 || legacyScopeScore(*op, legacyScopeHints(schema)) > 0
				if !relevant {
					for _, token := range legacyFilteredTokens(resource, options.ResourcePrefix) {
						if legacyMention(*op, token) {
							relevant = true
							break
						}
					}
				}
				if !relevant {
					continue
				}
				bonus := 1000
				if hit, ok := legacyHitFor(resource, options.ResourcePrefix, *op, call, schema, "sdk", &bonus); ok {
					hit.SDKPathFile = legacyString(evidence["sdk_file"])
					hit.SDKPathMethod = method
					hit.SDKPathTemplate = legacyString(evidence["path_template"])
					if role := legacyString(evidence["source_role"]); role != "" {
						hit.SourceRole = role
					}
					hits = append(hits, hit)
				}
			}
		}
		for _, operation := range operations {
			if operation.Method != "GET" {
				continue
			}
			var aliases []string
			for _, alias := range operation.Aliases {
				if identifiers[alias] {
					aliases = append(aliases, alias)
				}
			}
			aliases = legacySorted(aliases)
			if len(aliases) > 0 {
				hits = append(hits, legacyHit{legacyOperation: operation, ListScore: legacyListScore(resource, options.ResourcePrefix, operation), ReadScore: legacyCandidateScore(resource, options.ResourcePrefix, operation), PathKind: legacyPathKind(operation), MatchedAliases: aliases})
			}
			for _, call := range sdkCalls {
				if hit, ok := legacyHitFor(resource, options.ResourcePrefix, operation, call, schema, "sdk", nil); ok {
					hits = append(hits, hit)
				}
			}
			for _, call := range packageCalls {
				if hit, ok := legacyHitFor(resource, options.ResourcePrefix, operation, call, schema, "package", nil); ok {
					hits = append(hits, hit)
				}
			}
			for _, call := range rawCalls {
				if hit, ok := legacyHitFor(resource, options.ResourcePrefix, operation, call, schema, "raw", nil); ok {
					hits = append(hits, hit)
				}
			}
		}
		legacySortHits(hits, false)
		unique := legacyDedupe(hits)
		legacySortHits(unique, false)
		read, readAmbiguous := legacySelect(unique, "read")
		listing, listAmbiguous := legacySelect(unique, "list")
		relation, relationAmbiguous := legacyRelationshipHit(unique, resource, options.ResourcePrefix)
		source := map[string]any{"candidate_count": len(unique), "files": files}
		if options.SourceFacts != nil {
			source["evidence_backend"] = "ast_facts"
		}
		if len(sdkCalls) > 0 {
			symbols := make([]any, 0, min(20, len(sdkCalls)))
			for _, call := range sdkCalls[:min(20, len(sdkCalls))] {
				symbols = append(symbols, call["client_symbol"])
			}
			source["client_call_count"] = len(sdkCalls)
			source["client_calls"] = symbols
		}
		if len(packageCalls) > 0 {
			symbols := make([]any, 0, min(20, len(packageCalls)))
			for _, call := range packageCalls[:min(20, len(packageCalls))] {
				symbols = append(symbols, call["client_symbol"])
			}
			source["package_call_count"] = len(packageCalls)
			source["package_calls"] = symbols
		}
		if len(rawCalls) > 0 {
			symbols := make([]any, 0, min(20, len(rawCalls)))
			for _, call := range rawCalls[:min(20, len(rawCalls))] {
				symbols = append(symbols, call["client_symbol"])
			}
			source["raw_rest_call_count"] = len(rawCalls)
			source["raw_rest_calls"] = symbols
		}
		if graphql {
			source["graphql"] = true
		}
		if len(unresolved) > 0 {
			source["sdk_path_unresolved"] = unresolved
		}
		if len(actions) > 0 {
			source["sdk_action_paths"] = actions
		}
		status, reason := "unmapped", any(nil)
		entry := map[string]any{"product": options.ResourcePrefix, "reason": nil, "source": source, "status": status, "surface": options.ResourcePrefix}
		if len(readAmbiguous) > 0 {
			status = "ambiguous_source_operation"
			reason = status
			candidates := make([]any, 0, len(readAmbiguous))
			for _, item := range readAmbiguous {
				candidates = append(candidates, legacyCandidateOperation(item, "read", files))
			}
			entry["candidates"] = candidates
		} else if read != nil {
			status = "mapped"
			entry["read"] = legacyOperationEntry(*read, "read", files)
			if listing != nil && listing.Path != read.Path {
				entry["list"] = legacyOperationEntry(*listing, "list", files)
			}
			if len(listAmbiguous) > 0 {
				items := make([]any, 0, len(listAmbiguous))
				for _, item := range listAmbiguous {
					items = append(items, legacyCandidateEntry(item))
				}
				source["list_ambiguous"] = items
			}
		} else if len(relationAmbiguous) > 0 {
			status = "ambiguous_source_operation"
			reason = status
			candidates := make([]any, 0, len(relationAmbiguous))
			for _, item := range relationAmbiguous {
				candidates = append(candidates, legacyCandidateOperation(item, "relationship_list_read", files))
			}
			entry["candidates"] = candidates
		} else if relation != nil {
			status = "mapped"
			entry["read"] = legacyOperationEntry(*relation, "relationship_list_read", files)
			source["relationship_list_read"] = true
		} else if graphql {
			status = "graphql_source"
			reason = status
		} else if len(absolute) == 0 {
			reason = "resource_file_not_found"
		} else {
			reason = "no_source_operation_match"
		}
		entry["status"], entry["reason"] = status, reason
		registry[resource] = entry
		ambiguousEntries := make([]any, 0, len(readAmbiguous))
		for _, item := range readAmbiguous {
			ambiguousEntries = append(ambiguousEntries, legacyCandidateEntry(item))
		}
		hitEntries := make([]any, 0, min(10, len(unique)))
		for _, item := range unique[:min(10, len(unique))] {
			hitEntries = append(hitEntries, legacyCandidateEntry(item))
		}
		diagnostics = append(diagnostics, map[string]any{"ambiguous": ambiguousEntries, "files": files, "hits": hitEntries, "reason": reason, "resource": resource, "status": status})
	}
	mapped, ambiguous, graphqlCount := 0, 0, 0
	for _, item := range diagnostics {
		record := legacyObject(item)
		switch record["status"] {
		case "mapped":
			mapped++
		case "ambiguous_source_operation":
			ambiguous++
		case "graphql_source":
			graphqlCount++
		}
	}
	return map[string]any{"diagnostics": diagnostics, "registry": registry, "summary": map[string]any{"ambiguous": ambiguous, "graphql_source": graphqlCount, "mapped": mapped, "resources": len(resources), "resources_with_source_files": withFiles, "resources_without_source_files": len(resources) - withFiles, "unmapped": len(resources) - mapped - ambiguous - graphqlCount}}, nil
}

func osRead(path string) (string, error) {
	data, err := os.ReadFile(filepath.Clean(path))
	return string(data), err
}
