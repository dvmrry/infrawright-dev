package sourceoperation

import (
	"regexp"
	"strings"
)

const (
	legacyAmbiguityDelta  = 5
	legacySourceCallDelta = 15
	legacyReadDetailDelta = 25
)

type legacyOperation struct {
	Method, OperationID, OperationIDSource, Path string
	Aliases                                      []string
}
type legacyHit struct {
	legacyOperation
	ListScore, ReadScore                                                                                                  int
	PathKind                                                                                                              string
	MatchedAliases                                                                                                        []string
	ClientSymbol, SourceRole, SDKMethod, SDKPackage, SDKPackagePath, RawPath, SDKPathFile, SDKPathMethod, SDKPathTemplate string
	Alternate                                                                                                             []string
}

// LegacyOptions is the closed input surface for the frozen v1 mapper.
// It must never be used as v2 source-first input or readiness evidence.
type LegacyOptions struct {
	SchemaData, OpenAPI                                 map[string]any
	SourceRoot, ProviderSource, ResourcePrefix, SDKRoot string
	Resources                                           []string
	SourceFacts                                         map[string]any
}

func legacyObject(value any) map[string]any {
	result, _ := value.(map[string]any)
	if result == nil {
		return map[string]any{}
	}
	return result
}
func legacyArray(value any) []any   { result, _ := value.([]any); return result }
func legacyString(value any) string { result, _ := value.(string); return result }
func legacyBool(value any) bool     { result, _ := value.(bool); return result }
func legacyNumber(value any) int {
	switch value := value.(type) {
	case int:
		return value
	case float64:
		return int(value)
	}
	return 0
}
func legacySet(values []string) map[string]bool {
	result := map[string]bool{}
	for _, value := range values {
		result[value] = true
	}
	return result
}

func legacyCanonical(value string) string { return CanonicalSourceSymbol(value) }
func legacyBaseTokens(resource, prefix string) []string {
	if prefix != "" && strings.HasPrefix(resource, prefix+"_") {
		resource = resource[len(prefix)+1:]
	}
	var out []string
	for _, item := range strings.Split(resource, "_") {
		if item != "" {
			out = append(out, item)
		}
	}
	return out
}
func legacyPathParts(value string) []string {
	value = strings.Trim(value, "/")
	if value == "" {
		return nil
	}
	return strings.Split(value, "/")
}
func legacyParameter(value string) bool {
	return strings.HasPrefix(value, "{") && strings.HasSuffix(value, "}")
}
func legacyPathWords(value string) []string {
	var output []string
	for _, part := range legacyPathParts(value) {
		if legacyParameter(part) {
			continue
		}
		for _, word := range regexp.MustCompile(`[^A-Za-z0-9]+`).Split(part, -1) {
			if word != "" {
				output = append(output, strings.ToLower(word))
			}
		}
	}
	return output
}

// OperationAliases returns v1 aliases in Python/code-point order.
func OperationAliases(operationID string) []string {
	raw := legacySet([]string{operationID})
	patterns := [][2]string{{"retrieve", "read"}, {"retrieve", "get"}, {"read", "retrieve"}, {"get", "retrieve"}}
	for _, replacement := range patterns {
		re := regexp.MustCompile(`(?i)` + replacement[0])
		raw[re.ReplaceAllString(operationID, replacement[1])] = true
	}
	for alias := range raw {
		if strings.HasPrefix(strings.ToLower(alias), "route") {
			raw[alias[5:]] = true
		}
	}
	aliases := map[string]bool{}
	for alias := range raw {
		canonical := legacyCanonical(alias)
		if canonical != "" {
			aliases[canonical] = true
			aliases[canonical+"withresponse"] = true
		}
	}
	return legacySortedSet(aliases)
}

// OpenAPIOperationInventory flattens the frozen v1 operation inventory.
func OpenAPIOperationInventory(spec map[string]any) []map[string]any {
	operations := legacyOperations(spec)
	out := make([]map[string]any, 0, len(operations))
	for _, op := range operations {
		out = append(out, map[string]any{"aliases": op.Aliases, "method": op.Method, "operation_id": op.OperationID, "operation_id_source": op.OperationIDSource, "path": op.Path})
	}
	return out
}
func legacyOperations(spec map[string]any) []legacyOperation {
	paths := legacyObject(spec["paths"])
	keys := make([]string, 0, len(paths))
	for key := range paths {
		keys = append(keys, key)
	}
	keys = legacySorted(keys)
	var output []legacyOperation
	for _, path := range keys {
		methods := legacyObject(paths[path])
		names := make([]string, 0, len(methods))
		for name := range methods {
			names = append(names, name)
		}
		for _, name := range legacySorted(names) {
			operation := legacyObject(methods[name])
			explicit := legacyString(operation["operationId"])
			source := "openapi"
			if explicit == "" {
				explicit = strings.ToUpper(name) + " " + path
				source = "synthetic_path"
			}
			output = append(output, legacyOperation{Method: strings.ToUpper(name), OperationID: explicit, OperationIDSource: source, Path: path, Aliases: OperationAliases(explicit)})
		}
	}
	return output
}

func legacyWordMatches(word, token string) bool {
	normalized := legacyCanonical(token)
	aliases := legacySet([]string{normalized, normalized + "s"})
	if strings.HasSuffix(normalized, "y") {
		aliases[normalized[:len(normalized)-1]+"ies"] = true
	}
	if strings.HasSuffix(normalized, "s") {
		aliases[normalized[:len(normalized)-1]] = true
	}
	if normalized == "application" {
		aliases["app"] = true
		aliases["apps"] = true
	}
	if normalized == "app" || normalized == "apps" {
		aliases["application"] = true
		aliases["applications"] = true
	}
	for _, group := range [][]string{{"repo", "repos", "repository", "repositories"}, {"org", "orgs", "organization", "organizations"}} {
		for _, candidate := range group {
			if normalized == candidate {
				for _, item := range group {
					aliases[item] = true
				}
			}
		}
	}
	return aliases[legacyCanonical(word)]
}
func legacyMention(op legacyOperation, token string) bool {
	token = legacyCanonical(token)
	if token == "" {
		return false
	}
	if strings.Contains(legacyCanonical(op.Path), token) || strings.Contains(legacyCanonical(op.OperationID), token) {
		return true
	}
	for _, word := range legacyPathWords(op.Path) {
		if legacyWordMatches(word, token) {
			return true
		}
	}
	for _, word := range IdentifierWords(op.OperationID) {
		if legacyWordMatches(word, token) {
			return true
		}
	}
	return false
}
func legacyFilteredTokens(resource, prefix string) []string {
	drop := legacySet([]string{"cloud", "apps", "asserts", "k6", "machine", "learning", "monitoring", "oncall", "synthetic", "trust", "zero"})
	var out []string
	for _, token := range legacyBaseTokens(resource, prefix) {
		if !drop[token] {
			out = append(out, token)
		}
	}
	return out
}
func legacyPathSequenceScore(resource, prefix string, op legacyOperation) int {
	tokens, words := legacyFilteredTokens(resource, prefix), legacyPathWords(op.Path)
	if len(tokens) == 0 || len(tokens) > len(words) {
		return 0
	}
	best := 0
	for start := 0; start <= len(words)-len(tokens); start++ {
		yes := true
		for offset, token := range tokens {
			if !legacyWordMatches(words[start+offset], token) {
				yes = false
				break
			}
		}
		if yes {
			terminal := start+len(tokens) == len(words)
			if len(tokens) == 1 && !terminal {
				continue
			}
			if terminal {
				best = max(best, 60)
			} else {
				best = max(best, 40)
			}
		}
	}
	return best
}
func legacyTerminalScore(resource, prefix string, op legacyOperation) int {
	tokens := legacyFilteredTokens(resource, prefix)
	parts := legacyPathParts(op.Path)
	filtered := parts[:0]
	for _, part := range parts {
		if !legacyParameter(part) {
			filtered = append(filtered, part)
		}
	}
	if len(tokens) > 0 && len(filtered) > 0 && strings.Contains(legacyCanonical(filtered[len(filtered)-1]), legacyCanonical(tokens[len(tokens)-1])) {
		return 35
	}
	return 0
}
func legacyPrefixScore(resource, prefix string, op legacyOperation) int {
	if prefix == "" {
		return 0
	}
	tokens := legacyFilteredTokens(resource, prefix)
	var parts []string
	for _, part := range legacyPathParts(op.Path) {
		if !legacyParameter(part) {
			parts = append(parts, part)
		}
	}
	if len(tokens) > 0 && len(parts) >= 2 && legacyWordMatches(parts[0], prefix) && legacyWordMatches(parts[1], tokens[0]) {
		return 30
	}
	return 0
}
func legacyScopeHints(schema map[string]any) map[string]string {
	attributes := legacyObject(legacyObject(schema["block"])["attributes"])
	groups := map[string][]string{"account": {"account_id", "account_identifier", "account_tag"}, "user": {"user_id"}, "zone": {"zone_id", "zone_identifier", "zone_tag"}}
	out := map[string]string{}
	for scope, names := range groups {
		var present []map[string]any
		for _, name := range names {
			if item, ok := attributes[name].(map[string]any); ok {
				present = append(present, item)
			}
		}
		if len(present) > 0 {
			optional := true
			for _, item := range present {
				if legacyBool(item["required"]) {
					optional = false
				}
			}
			if optional {
				out[scope] = "optional"
			} else {
				out[scope] = "required"
			}
		}
	}
	return out
}
func legacyOperationScopes(op legacyOperation) map[string]bool {
	out := map[string]bool{}
	for _, part := range legacyPathParts(op.Path) {
		clean := strings.ToLower(strings.Trim(part, "{}"))
		switch clean {
		case "account_id", "account_identifier", "account_tag", "accounts":
			out["account"] = true
		case "zone_id", "zone_identifier", "zone_tag", "zones":
			out["zone"] = true
		case "user_id", "user":
			out["user"] = true
		}
	}
	return out
}
func legacyScopeScore(op legacyOperation, scopes map[string]string) int {
	if len(scopes) == 0 {
		return 0
	}
	actual := legacyOperationScopes(op)
	var names, required []string
	for name, kind := range scopes {
		names = append(names, name)
		if kind == "required" {
			required = append(required, name)
		}
	}
	if len(required) > 0 {
		for _, name := range required {
			if actual[name] {
				return 80
			}
		}
		if len(actual) > 0 {
			return -80
		}
	}
	if len(names) == 1 {
		if actual[names[0]] {
			return 40
		}
		if len(actual) > 0 {
			return -40
		}
	}
	for name := range actual {
		if scopes[name] == "" {
			return -40
		}
	}
	return 0
}
func legacyPathKind(op legacyOperation) string {
	parts := legacyPathParts(op.Path)
	if len(parts) > 0 && legacyParameter(parts[len(parts)-1]) {
		return "detail"
	}
	return "list"
}
func legacyListOperation(id string) bool {
	words := IdentifierWords(id)
	return len(words) > 0 && (words[0] == "list" || words[0] == "search" || len(words) > 1 && words[0] == "get" && words[1] == "all")
}
func legacyActionShaped(path string) bool {
	actions := legacySet([]string{"batch", "bulk", "export", "import", "preview", "review", "scan", "search", "trigger", "usage"})
	for _, part := range legacyPathParts(path) {
		if actions[part] {
			return true
		}
	}
	return false
}
func legacyCallStrings(call map[string]any, key string) []string {
	value := call[key]
	array, ok := value.([]string)
	if ok {
		return array
	}
	generic := legacyArray(value)
	output := make([]string, 0, len(generic))
	for _, item := range generic {
		output = append(output, legacyString(item))
	}
	return output
}
func legacyChainTokens(call map[string]any) []string {
	drop := legacySet([]string{"api", "client", "cloudflare", "path", "paths", "zerotrust"})
	var out []string
	for _, part := range legacyCallStrings(call, "chain") {
		for _, token := range IdentifierWords(part) {
			if !drop[legacyCanonical(token)] {
				out = append(out, token)
			}
		}
	}
	if len(out) > 0 {
		return out
	}
	return legacyCallStrings(call, "chain")
}
func legacyMethodTokens(call map[string]any) []string {
	drop := legacySet([]string{"by", "fetch", "get", "list", "read", "search", "with"})
	words := IdentifierWords(legacyString(call["method"]))
	var extra []string
	for index, word := range words[:max(0, len(words)-1)] {
		if word == "ip" && index+1 < len(words) && (words[index+1] == "address" || words[index+1] == "addresses") {
			extra = append(extra, "ips")
		}
	}
	words = append(words, extra...)
	var out []string
	for _, token := range words {
		if !drop[token] && len(legacyCanonical(token)) >= 3 {
			out = append(out, token)
		}
	}
	return out
}
func legacyExactAlias(op legacyOperation, method string) bool {
	target := legacyCanonical(method)
	for _, alias := range op.Aliases {
		if alias == target {
			return true
		}
	}
	return false
}
func legacySDKScore(resource, prefix string, op legacyOperation, call, schema map[string]any) (int, bool) {
	if op.Method != "GET" {
		return 0, false
	}
	score := 0
	chains, hits := legacyChainTokens(call), 0
	for _, token := range chains {
		if legacyMention(op, token) {
			hits++
			score += 30
		}
	}
	if len(chains) > 0 && hits < min(2, len(chains)) {
		return 0, false
	}
	methods, methodHits := legacyMethodTokens(call), 0
	for _, token := range methods {
		if legacyMention(op, token) {
			methodHits++
			score += 22
		}
	}
	if len(chains) > 0 && hits == 0 {
		return 0, false
	}
	tokens := legacyFilteredTokens(resource, prefix)
	resourceHits := 0
	for _, token := range tokens {
		if legacyMention(op, token) {
			resourceHits++
		}
	}
	sequence, terminal := legacyPathSequenceScore(resource, prefix, op), legacyTerminalScore(resource, prefix, op)
	exact := legacyExactAlias(op, legacyString(call["method"]))
	if len(chains) == 0 {
		if (!exact && methodHits == 0) || (len(tokens) > 0 && resourceHits == 0 && sequence == 0 && terminal == 0) {
			return 0, false
		}
		if exact {
			score += 110
		} else {
			score += 35 + methodHits*18
			score -= (len(methods) - methodHits) * 20
		}
	} else if exact {
		score += 80
	}
	score -= (len(chains) - hits) * 35
	score += resourceHits*8 + sequence + terminal + legacyPrefixScore(resource, prefix, op) + legacyScopeScore(op, legacyScopeHints(schema))
	role := legacyString(call["source_role"])
	if role == "read" {
		if legacyPathKind(op) == "detail" {
			score += 30
		} else {
			score += 5
		}
		for _, word := range IdentifierWords(op.OperationID) {
			if word == "detail" || word == "details" || word == "get" {
				score += 10
			}
		}
		if legacyListOperation(op.OperationID) {
			score -= 20
		}
		if legacyActionShaped(op.Path) {
			score -= 25
		}
	} else if role == "list" {
		if legacyPathKind(op) == "list" {
			score += 30
		} else {
			score -= 20
		}
		if legacyListOperation(op.OperationID) {
			score += 15
		}
		if legacyActionShaped(op.Path) {
			score -= 20
		}
	}
	return score, score >= 35
}
func legacyPackageTokens(call map[string]any) []string {
	dropPart, drop := legacySet([]string{"services", "zscaler", "v3"}), legacySet([]string{"by", "get", "id", "list", "or", "read", "search"})
	var out []string
	for _, part := range strings.Split(legacyString(call["package_path"]), "/") {
		if !dropPart[part] {
			out = append(out, IdentifierWords(part)...)
		}
	}
	out = append(out, IdentifierWords(legacyString(call["package"]))...)
	out = append(out, IdentifierWords(legacyString(call["method"]))...)
	var filtered []string
	for _, token := range out {
		if !drop[token] && len(legacyCanonical(token)) >= 3 {
			filtered = append(filtered, token)
		}
	}
	return filtered
}
func legacyPackageScore(resource, prefix string, op legacyOperation, call, schema map[string]any) (int, bool) {
	if op.Method != "GET" {
		return 0, false
	}
	score, hits := 0, 0
	for _, token := range legacyPackageTokens(call) {
		if legacyMention(op, token) {
			hits++
			score += 18
		}
	}
	if hits == 0 {
		return 0, false
	}
	for _, token := range legacyFilteredTokens(resource, prefix) {
		if legacyMention(op, token) {
			score += 10
		}
	}
	score += legacyPathSequenceScore(resource, prefix, op) + legacyTerminalScore(resource, prefix, op) + legacyPrefixScore(resource, prefix, op) + legacyScopeScore(op, legacyScopeHints(schema))
	lower := strings.ToLower(legacyString(call["method"]))
	hint := ""
	if strings.Contains(lower, "byid") || strings.Contains(lower, "detail") {
		hint = "detail"
	} else if strings.HasPrefix(lower, "list") || strings.HasPrefix(lower, "search") || strings.Contains(lower, "all") {
		hint = "list"
	}
	kind := legacyPathKind(op)
	if legacyString(call["source_role"]) == "read" {
		if hint == "detail" && kind == "detail" {
			score += 45
		} else if hint == "list" && kind == "list" {
			score += 30
		} else if kind == "detail" {
			score += 20
		}
		if legacyListOperation(op.OperationID) {
			score -= 20
		}
		if legacyActionShaped(op.Path) {
			score -= 20
		}
	} else if legacyString(call["source_role"]) == "list" {
		if kind == "list" {
			score += 35
		} else {
			score -= 25
		}
		if legacyListOperation(op.OperationID) {
			score += 10
		}
	}
	return score, score >= 35
}
func legacySequenceMatches(haystack, needle []string) bool {
	if len(needle) == 0 || len(needle) > len(haystack) {
		return false
	}
	for start := 0; start <= len(haystack)-len(needle); start++ {
		yes := true
		for index, token := range needle {
			if !legacyWordMatches(haystack[start+index], token) {
				yes = false
				break
			}
		}
		if yes {
			return true
		}
	}
	return false
}
func legacyRawScore(resource, prefix string, op legacyOperation, call, schema map[string]any) (int, bool) {
	if op.Method != legacyString(call["method"]) {
		return 0, false
	}
	left, right := legacyPathWords(legacyString(call["path"])), legacyPathWords(op.Path)
	if len(left) == 0 || len(right) == 0 || (!legacySequenceMatches(right, left) && !legacySequenceMatches(left, right)) {
		return 0, false
	}
	score := 120 + len(left)*12
	if len(left) == len(right) {
		score += 50
	}
	score += legacyPathSequenceScore(resource, prefix, op) + legacyTerminalScore(resource, prefix, op) + legacyPrefixScore(resource, prefix, op) + legacyScopeScore(op, legacyScopeHints(schema))
	if legacyPathKind(op) == "detail" {
		score += 20
	}
	if legacyActionShaped(op.Path) {
		score -= 10
	}
	return score, score >= 70
}
func legacyCandidateScore(resource, prefix string, op legacyOperation) int {
	score := 0
	for _, token := range legacyFilteredTokens(resource, prefix) {
		if strings.Contains(legacyCanonical(op.Path), legacyCanonical(token)) {
			score += 5
		}
	}
	if strings.Contains(op.Path, "{") {
		score += 30
	}
	if legacyListOperation(op.OperationID) {
		score -= 10
	}
	lower := strings.ToLower(op.OperationID)
	for _, start := range []string{"get", "retrieve", "read", "routeget"} {
		if strings.HasPrefix(lower, start) {
			score += 10
			break
		}
	}
	if strings.HasSuffix(op.Path, "/search") || strings.Contains(op.Path, "/search/") {
		score -= 20
	}
	return score
}
func legacyListScore(resource, prefix string, op legacyOperation) int {
	score := 0
	for _, token := range legacyFilteredTokens(resource, prefix) {
		if strings.Contains(legacyCanonical(op.Path), legacyCanonical(token)) {
			score += 5
		}
	}
	if legacyListOperation(op.OperationID) {
		score += 20
	}
	parts := legacyPathParts(op.Path)
	if len(parts) > 0 && legacyParameter(parts[len(parts)-1]) {
		score -= 20
	} else {
		score += 15
	}
	lower := strings.ToLower(op.OperationID)
	for _, start := range []string{"get", "retrieve", "read", "routeget"} {
		if strings.HasPrefix(lower, start) {
			score += 5
			break
		}
	}
	return score
}
func legacyHitFor(resource, prefix string, op legacyOperation, call map[string]any, schema map[string]any, kind string, bonus *int) (legacyHit, bool) {
	score, ok := 0, false
	if bonus != nil {
		score = *bonus
		ok = true
	} else if kind == "sdk" {
		score, ok = legacySDKScore(resource, prefix, op, call, schema)
	} else if kind == "package" {
		score, ok = legacyPackageScore(resource, prefix, op, call, schema)
	} else {
		score, ok = legacyRawScore(resource, prefix, op, call, schema)
	}
	if !ok {
		return legacyHit{}, false
	}
	hit := legacyHit{legacyOperation: op, ListScore: legacyListScore(resource, prefix, op) + score, ReadScore: legacyCandidateScore(resource, prefix, op) + score, PathKind: legacyPathKind(op)}
	if call != nil {
		hit.ClientSymbol = legacyString(call["client_symbol"])
		hit.SourceRole = legacyString(call["source_role"])
		if kind == "raw" {
			hit.MatchedAliases = []string{legacyString(call["path"])}
			hit.RawPath = legacyString(call["path"])
		} else {
			hit.MatchedAliases = []string{hit.ClientSymbol}
			hit.SDKMethod = legacyString(call["method"])
		}
		if kind == "package" {
			hit.SDKPackage = legacyString(call["package"])
			hit.SDKPackagePath = legacyString(call["package_path"])
		}
	}
	return hit, true
}
