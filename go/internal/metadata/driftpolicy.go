package metadata

// This file ports the narrow slice of node-src/domain/drift-policy.ts (and
// its node-src/domain/policy-paths.ts path-syntax helper) that
// node-src/metadata/packs.ts actually exercises. validatePackManifest there
// does exactly this, and nothing else, with a manifest's drift_policy
// value:
//
//	if (Object.hasOwn(data, "drift_policy")) {
//	  try {
//	    new DriftPolicy(data.drift_policy, `${source}.drift_policy`);
//	  } catch (error: unknown) {
//	    const detail = error instanceof Error ? error.message : String(error);
//	    fail(detail);
//	  }
//	}
//
// The constructed DriftPolicy instance is never stored -- PackManifest
// carries no drift-policy field -- or used for anything else; only whether
// construction throws, and with what message, matters. validateDriftPolicy
// below reproduces exactly that constructor's failure behavior:
// validatePolicy's structural checks, per-entry validation for all five
// modes, and the per-resource-type plan_tolerate wildcard-count limit.
//
// Deliberately NOT ported here, because no caller in this package's port
// needs it and it would pull substantial, functionally distinct domain
// logic into a package whose job is metadata *validation*: DriftPolicy's
// runtime matching API (projectionOmits, toleratesPlanPath, staleEntries,
// markMatched, and the compiled exact/wildcard plan_tolerate maps behind
// them -- all of node-src/domain/drift-policy.ts's actual plan/state drift
// matching), and the node-src/domain/policy-paths.ts helpers that exist
// only to serve that API (policySelectorMatches, normalizePolicyPath,
// formatPolicyPath).
//
// One further simplification: node-src/domain/policy-paths.ts's
// parsePolicyPath takes an optional `what` label (default "policy path")
// used to phrase its error messages; every call site reachable from
// node-src/domain/drift-policy.ts's validatePolicy calls it with no
// override, so this port hardcodes "policy path" rather than threading an
// unused parameter.

import (
	"encoding/json"
	"fmt"
	"math/big"
	"regexp"
	"strings"

	"github.com/dvmrry/infrawright-dev/go/internal/canonjson"
)

// policyWildcard is the wildcard path-selector marker. Ports POLICY_WILDCARD
// from node-src/domain/policy-paths.ts.
const policyWildcard = "*"

// maxPolicyEntries and maxPlanTolerateWildcardsPerResource port
// MAX_POLICY_ENTRIES and MAX_PLAN_TOLERATE_WILDCARDS_PER_RESOURCE from
// node-src/domain/drift-policy.ts.
const (
	maxPolicyEntries                    = 50_000
	maxPlanTolerateWildcardsPerResource = 1_000
)

// policyModes ports the MODES tuple from node-src/domain/drift-policy.ts.
var policyModes = []string{
	"projection_omit",
	"projection_sync",
	"projection_fill",
	"projection_omit_if",
	"plan_tolerate",
}

// policyTopLevelKeys, policyResourceKeys, and policyCommonKeys port
// TOP_LEVEL_KEYS, RESOURCE_KEYS, and COMMON_KEYS from
// node-src/domain/drift-policy.ts.
var (
	policyTopLevelKeys = stringSet("version", "resource_types")
	policyResourceKeys = stringSet(
		"projection_omit", "projection_sync", "projection_fill",
		"projection_omit_if", "plan_tolerate",
	)
	policyCommonKeys = stringSet("path", "reason", "approved_by", "ticket")
)

// driftResourceTypeName validates a drift-policy resource_types key.
// Ports the inline /^[A-Za-z_][A-Za-z0-9_]*$/ test in
// node-src/domain/drift-policy.ts's validatePolicy.
var driftResourceTypeName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// driftPolicyFailure is the local panic payload used to unwind out of the
// deeply nested validation helpers in this file, mirroring how
// node-src/domain/drift-policy.ts's fail() (a `throw`) unwinds the
// TypeScript call stack. validateDriftPolicy recovers it at its own
// boundary and converts it to a normal error; no other file in this
// package observes this panic type.
type driftPolicyFailure struct{ message string }

func driftFail(format string, args ...any) {
	panic(driftPolicyFailure{message: fmt.Sprintf(format, args...)})
}

func unionStringSets(sets ...map[string]struct{}) map[string]struct{} {
	out := make(map[string]struct{})
	for _, set := range sets {
		for key := range set {
			out[key] = struct{}{}
		}
	}
	return out
}

// jsonQuote approximates JSON.stringify(s) for embedding raw values in
// human-readable validation error text: a JSON string literal with
// standard escapes. Unlike this package's canonical renderer
// (go/internal/canonjson.Render), it does not ASCII-escape non-ASCII
// characters, matching JSON.stringify's own default behavior. This is
// never a byte-for-byte contract (these messages are not asserted by this
// port's ported tests), so an approximation is acceptable.
func jsonQuote(s string) string {
	var sb strings.Builder
	sb.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			sb.WriteString(`\"`)
		case '\\':
			sb.WriteString(`\\`)
		case '\n':
			sb.WriteString(`\n`)
		case '\r':
			sb.WriteString(`\r`)
		case '\t':
			sb.WriteString(`\t`)
		default:
			if r < 0x20 {
				fmt.Fprintf(&sb, `\u%04x`, r)
			} else {
				sb.WriteRune(r)
			}
		}
	}
	sb.WriteByte('"')
	return sb.String()
}

// mustMarshalJSON renders value the way JSON.stringify would for the
// scalar-only slices validateEntry calls it with (a projection_omit_if
// entry's `values`), used only to build an internal dedup key -- both
// sides of every comparison are produced by this same function, so it
// need not match JS's literal bytes, only be deterministic.
func mustMarshalJSON(value any) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprintf("%v", value)
	}
	return string(encoded)
}

// policyPathSegment is one parsed segment of the drift-policy path
// dialect: a string (a field name, or the literal wildcard marker
// policyWildcard), an int64 (a small numeric index), or a *big.Int (a
// numeric index beyond int64 range). Ports the PolicyPathSegment union
// type from node-src/domain/policy-paths.ts.
type policyPathSegment = any

var (
	policySegmentName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*`)
	policyAsciiDigits = regexp.MustCompile(`^[0-9]+$`)
)

// splitDotted ports splitDotted from node-src/domain/policy-paths.ts.
func splitDotted(text, what string) []string {
	var parts []string
	var buffer strings.Builder
	inQuote := false
	escaped := false
	for _, ch := range text {
		switch {
		case escaped:
			buffer.WriteRune(ch)
			escaped = false
		case ch == '\\' && inQuote:
			buffer.WriteRune(ch)
			escaped = true
		case ch == '"':
			inQuote = !inQuote
			buffer.WriteRune(ch)
		case ch == '.' && !inQuote:
			parts = append(parts, buffer.String())
			buffer.Reset()
		default:
			buffer.WriteRune(ch)
		}
	}
	if inQuote {
		driftFail("unterminated quoted %s selector in %s", what, jsonQuote(text))
	}
	parts = append(parts, buffer.String())
	return parts
}

// selectorEnd ports selectorEnd from node-src/domain/policy-paths.ts,
// operating over runes (rather than UTF-16 code units) since Go strings
// are UTF-8; the bracket/quote/backslash delimiters it scans for are all
// single-byte ASCII, so this is behaviorally identical for every valid
// path string.
func selectorEnd(raw []rune, start int, fullPath, what string) int {
	inQuote := false
	escaped := false
	for index := start + 1; index < len(raw); index++ {
		ch := raw[index]
		switch {
		case escaped:
			escaped = false
		case ch == '\\' && inQuote:
			escaped = true
		case ch == '"':
			inQuote = !inQuote
		case ch == ']' && !inQuote:
			return index
		}
	}
	driftFail("unterminated %s selector in %s", what, jsonQuote(fullPath))
	return -1
}

// unquoteSelector ports unquoteSelector from
// node-src/domain/policy-paths.ts. Order matters: unescaping `\"` before
// `\\` matches the Node source's own replaceAll ordering.
func unquoteSelector(text string) string {
	text = strings.ReplaceAll(text, `\"`, `"`)
	text = strings.ReplaceAll(text, `\\`, `\`)
	return text
}

// parseIndex ports parseIndex from node-src/domain/policy-paths.ts.
func parseIndex(text string) policyPathSegment {
	value, ok := new(big.Int).SetString(text, 10)
	if !ok {
		// Unreachable: callers only pass text already matched by
		// policyAsciiDigits.
		return text
	}
	if value.Cmp(maxSafeIntegerBig) <= 0 {
		return value.Int64()
	}
	return value
}

func invalidSegment(raw, fullPath, what string) {
	driftFail("invalid %s segment %s in %s", what, jsonQuote(raw), jsonQuote(fullPath))
}

// parseSegment ports parseSegment from node-src/domain/policy-paths.ts.
func parseSegment(raw []rune, fullPath, what string) []policyPathSegment {
	rawText := string(raw)
	match := policySegmentName.FindString(rawText)
	if match == "" {
		invalidSegment(rawText, fullPath, what)
		return nil
	}
	output := []policyPathSegment{match}
	position := len([]rune(match))
	for position < len(raw) {
		if raw[position] != '[' {
			invalidSegment(rawText, fullPath, what)
			return nil
		}
		end := selectorEnd(raw, position, fullPath, what)
		selector := string(raw[position+1 : end])
		switch {
		case selector == "" || selector == policyWildcard:
			output = append(output, policyWildcard)
		case policyAsciiDigits.MatchString(selector):
			output = append(output, parseIndex(selector))
		case len(selector) >= 2 && strings.HasPrefix(selector, `"`) && strings.HasSuffix(selector, `"`):
			output = append(output, unquoteSelector(selector[1:len(selector)-1]))
		default:
			driftFail("invalid %s selector %s in %s", what, jsonQuote(selector), jsonQuote(fullPath))
		}
		position = end + 1
	}
	return output
}

// parsePolicyPath parses the strict path dialect accepted by
// drift-policy entries. Ports parsePolicyPath from
// node-src/domain/policy-paths.ts with its `what` label hardcoded to
// "policy path" (see the file-level doc comment).
func parsePolicyPath(text string) []policyPathSegment {
	const what = "policy path"
	if text == "" {
		driftFail("%s must be a non-empty string", what)
	}
	var output []policyPathSegment
	for _, raw := range splitDotted(text, what) {
		output = append(output, parseSegment([]rune(raw), text, what)...)
	}
	return output
}

// isCollectionSelector ports isCollectionSelector from
// node-src/domain/policy-paths.ts.
func isCollectionSelector(segment policyPathSegment) bool {
	switch v := segment.(type) {
	case string:
		return v == policyWildcard
	case int64:
		return true
	case *big.Int:
		return true
	default:
		return false
	}
}

// policyPathHasWildcardOrIndex ports policyPathHasWildcardOrIndex from
// node-src/domain/policy-paths.ts.
func policyPathHasWildcardOrIndex(path []policyPathSegment) bool {
	for _, segment := range path {
		if isCollectionSelector(segment) {
			return true
		}
	}
	return false
}

// policySegmentEqual reports whether two path segments are identical,
// matching JavaScript `===` semantics: a number segment and a
// same-valued bigint segment are never equal, since they are distinct JS
// types.
func policySegmentEqual(left, right policyPathSegment) bool {
	switch l := left.(type) {
	case string:
		r, ok := right.(string)
		return ok && l == r
	case int64:
		r, ok := right.(int64)
		return ok && l == r
	case *big.Int:
		r, ok := right.(*big.Int)
		return ok && l.Cmp(r) == 0
	default:
		return false
	}
}

// policyPathsEqual ports policyPathsEqual from
// node-src/domain/policy-paths.ts.
func policyPathsEqual(left, right []policyPathSegment) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if !policySegmentEqual(left[i], right[i]) {
			return false
		}
	}
	return true
}

// pathMarker builds a dedup key for a parsed path, ports pathMarker from
// node-src/domain/drift-policy.ts. Both sides of every comparison this
// package makes against a pathMarker result are produced by this same
// function, so it need not reproduce JSON.stringify's literal bytes, only
// be injective and deterministic.
func pathMarker(path []policyPathSegment) string {
	var sb strings.Builder
	sb.WriteByte('[')
	for i, segment := range path {
		if i > 0 {
			sb.WriteByte(',')
		}
		switch v := segment.(type) {
		case string:
			fmt.Fprintf(&sb, "[%q,%q]", "string", v)
		case int64:
			fmt.Fprintf(&sb, "[%q,%d]", "number", v)
		case *big.Int:
			fmt.Fprintf(&sb, "[%q,%q]", "bigint", v.String())
		}
	}
	sb.WriteByte(']')
	return sb.String()
}

// driftRejectUnknownKeys ports drift-policy.ts's own local
// rejectUnknownKeys, distinct in message shape ("has unknown key" versus
// validation.ts's "unknown key") from this package's rejectUnknownKeys in
// validation.go.
func driftRejectUnknownKeys(object JsonObject, allowed map[string]struct{}, where string) {
	unknown := make([]string, 0)
	for key := range object {
		if _, ok := allowed[key]; !ok {
			unknown = append(unknown, key)
		}
	}
	if len(unknown) > 0 {
		sorted := canonjson.SortedStrings(unknown)
		driftFail("%s has unknown key %s", where, sorted[0])
	}
}

// driftEntriesFor ports entriesFor from node-src/domain/drift-policy.ts.
func driftEntriesFor(data JsonObject, resourceType, mode string) []JsonObject {
	resources, ok := data["resource_types"].(JsonObject)
	if !ok {
		return nil
	}
	resource, ok := resources[resourceType].(JsonObject)
	if !ok {
		return nil
	}
	entriesRaw, ok := resource[mode].([]any)
	if !ok {
		return nil
	}
	out := make([]JsonObject, 0, len(entriesRaw))
	for _, item := range entriesRaw {
		if obj, ok := item.(JsonObject); ok {
			out = append(out, obj)
		}
	}
	return out
}

// driftEntryKeys ports entryKeys from node-src/domain/drift-policy.ts.
func driftEntryKeys(mode string) map[string]struct{} {
	switch mode {
	case "projection_sync":
		return stringSet("target_path", "source_path", "reason", "approved_by", "ticket")
	case "projection_fill":
		return stringSet("path", "source", "reason", "approved_by", "ticket")
	case "projection_omit_if":
		return unionStringSets(policyCommonKeys, stringSet("values"))
	case "plan_tolerate":
		return unionStringSets(policyCommonKeys, stringSet("actions"))
	default:
		return policyCommonKeys
	}
}

// driftRequiredStrings ports requiredStrings from
// node-src/domain/drift-policy.ts.
func driftRequiredStrings(mode string) []string {
	switch mode {
	case "projection_sync":
		return []string{"target_path", "source_path", "reason", "approved_by"}
	case "projection_fill":
		return []string{"path", "source", "reason", "approved_by"}
	default:
		return []string{"path", "reason", "approved_by"}
	}
}

// driftRequireString ports drift-policy.ts's own local requireString,
// distinct in message shape ("missing <key>") from validation.ts's
// requireNonEmptyString ("must be a non-empty string").
func driftRequireString(entry JsonObject, key, context string) string {
	value, hasKey := entry[key]
	s, isString := value.(string)
	if !hasKey || !isString || len(s) == 0 {
		driftFail("%s missing %s", context, key)
	}
	return s
}

// driftIsJsonScalar ports drift-policy.ts's own local isJsonScalar, which
// -- unlike validation.ts's isJsonScalar -- does not accept a
// losslessly-preserved json.Number token as scalar, only a plain
// (already-demoted) float64. This is a faithful, if obscure, port of a
// real asymmetry in the Node source between its two same-named local
// helpers.
func driftIsJsonScalar(value any) bool {
	if value == nil {
		return true
	}
	switch value.(type) {
	case string, bool, float64:
		return true
	default:
		return false
	}
}

// validateEntry ports validateEntry from node-src/domain/drift-policy.ts.
func validateEntry(source, resourceType, mode string, entryValue any) string {
	context := fmt.Sprintf("%s %s entry for %s", source, mode, resourceType)
	entry, ok := entryValue.(JsonObject)
	if !ok {
		driftFail("%s must be an object", context)
		return ""
	}
	driftRejectUnknownKeys(entry, driftEntryKeys(mode), context)
	for _, key := range driftRequiredStrings(mode) {
		driftRequireString(entry, key, context)
	}
	if ticketRaw, hasTicket := entry["ticket"]; hasTicket {
		ticket, isString := ticketRaw.(string)
		if !isString || len(ticket) == 0 {
			driftFail("%s has invalid ticket", context)
		}
	}

	if mode == "projection_sync" {
		targetText := driftRequireString(entry, "target_path", context)
		sourceText := driftRequireString(entry, "source_path", context)
		target := parsePolicyPath(targetText)
		sourcePath := parsePolicyPath(sourceText)
		if policyPathsEqual(target, sourcePath) {
			driftFail("%s projection_sync entry for %s target_path and source_path must differ", source, resourceType)
		}
		if policyPathHasWildcardOrIndex(target) {
			driftFail("%s projection_sync entry for %s target_path must not contain wildcard or index selectors", source, resourceType)
		}
		if policyPathHasWildcardOrIndex(sourcePath) {
			driftFail("%s projection_sync entry for %s source_path must not contain wildcard or index selectors", source, resourceType)
		}
		return "projection_sync\x00" + targetText
	}

	pathText := driftRequireString(entry, "path", context)
	parsed := parsePolicyPath(pathText)

	switch mode {
	case "projection_fill":
		sourceText := driftRequireString(entry, "source", context)
		sourcePath := parsePolicyPath(sourceText)
		if len(parsed) != 1 {
			driftFail("%s projection_fill entry for %s path must be a single top-level name", source, resourceType)
		}
		if len(sourcePath) != 1 {
			driftFail("%s projection_fill entry for %s source must be a single top-level raw API name", source, resourceType)
		}
		if policyPathHasWildcardOrIndex(parsed) {
			driftFail("%s projection_fill entry for %s path must not contain wildcard or index selectors", source, resourceType)
		}
		if policyPathHasWildcardOrIndex(sourcePath) {
			driftFail("%s projection_fill entry for %s source must not contain wildcard or index selectors", source, resourceType)
		}
		return "projection_fill\x00" + pathText
	case "projection_omit_if":
		valuesRaw, hasValues := entry["values"]
		values, isArray := valuesRaw.([]any)
		if !hasValues || !isArray || len(values) == 0 {
			driftFail("%s projection_omit_if entry for %s values must be a non-empty JSON list", source, resourceType)
		}
		for _, item := range values {
			if !driftIsJsonScalar(item) {
				driftFail("%s projection_omit_if entry for %s values must contain only JSON scalars", source, resourceType)
			}
		}
		return fmt.Sprintf("projection_omit_if\x00%s\x00%s", pathText, mustMarshalJSON(values))
	case "plan_tolerate":
		var rawActions []any
		if actionsRaw, hasActions := entry["actions"]; hasActions {
			arr, isArray := actionsRaw.([]any)
			if !isArray {
				driftFail("%s plan_tolerate entries for %s actions must be a list", source, resourceType)
			}
			rawActions = arr
		} else {
			rawActions = []any{"update"}
		}
		if len(rawActions) == 0 {
			driftFail("%s plan_tolerate entry for %s actions must not be empty", source, resourceType)
		}
		seen := make(map[string]struct{})
		seenList := make([]string, 0, len(rawActions))
		for _, actionRaw := range rawActions {
			action, isString := actionRaw.(string)
			if !isString || len(action) == 0 {
				driftFail("%s plan_tolerate entry for %s has invalid action", source, resourceType)
			}
			if action != "update" {
				driftFail("%s plan_tolerate entry for %s has unsupported action %s", source, resourceType, jsonQuote(action))
			}
			if _, dup := seen[action]; dup {
				driftFail("%s plan_tolerate entry for %s has duplicate action %s", source, resourceType, jsonQuote(action))
			}
			seen[action] = struct{}{}
			seenList = append(seenList, action)
		}
		return fmt.Sprintf("plan_tolerate\x00%s\x00%s", pathText, strings.Join(canonjson.SortedStrings(seenList), "\x00"))
	default: // projection_omit
		return fmt.Sprintf("projection_omit\x00%s\x00%s", pathText, pathMarker(parsed))
	}
}

// isDriftPolicyVersionOne reports whether value is the plain (already
// numeric-token-demoted) number 1, matching a real asymmetry in the Node
// source: node-src/domain/drift-policy.ts's own version check is a bare
// `data.version !== 1`, with no LosslessNumber special-case (unlike
// node-src/metadata/packs.ts's PACK_SET_VERSION check, which explicitly
// treats a LosslessNumber("1") as equal to 1). A drift_policy document
// whose version token happened to survive as a preserved json.Number
// would therefore fail this check even though its numeric value is 1 --
// ported here exactly as the Node source has it, oddity included.
func isDriftPolicyVersionOne(value any) bool {
	number, ok := value.(float64)
	return ok && number == 1
}

// validateDriftPolicyData ports the structural half of validatePolicy from
// node-src/domain/drift-policy.ts (everything up to, and including, its
// per-resource-type entry and fill/omit-conflict validation).
func validateDriftPolicyData(data any, source string) JsonObject {
	obj, ok := data.(JsonObject)
	if !ok {
		driftFail("%s: drift policy must be an object", source)
		return nil
	}
	driftRejectUnknownKeys(obj, policyTopLevelKeys, fmt.Sprintf("%s top-level drift policy", source))
	if _, hasVersion := obj["version"]; !hasVersion {
		driftFail("%s: drift policy missing version", source)
	}
	if !isDriftPolicyVersionOne(obj["version"]) {
		driftFail("%s: unsupported drift policy version", source)
	}
	if _, hasResourceTypes := obj["resource_types"]; !hasResourceTypes {
		driftFail("%s: drift policy missing resource_types", source)
	}
	resourceTypes, ok := obj["resource_types"].(JsonObject)
	if !ok {
		driftFail("%s: resource_types must be an object", source)
		return nil
	}
	entryCount := 0
	for _, resourceType := range sortedKeys(resourceTypes) {
		if !driftResourceTypeName.MatchString(resourceType) {
			driftFail("%s: invalid resource type %s", source, jsonQuote(resourceType))
		}
		resource, ok := resourceTypes[resourceType].(JsonObject)
		if !ok {
			driftFail("%s: policy for %s must be an object", source, resourceType)
			return nil
		}
		driftRejectUnknownKeys(resource, policyResourceKeys, fmt.Sprintf("%s policy for %s", source, resourceType))
		for _, mode := range policyModes {
			var rawEntries []any
			if rawEntriesValue, has := resource[mode]; has {
				arr, isArray := rawEntriesValue.([]any)
				if !isArray {
					driftFail("%s %s entries for %s must be a list", source, mode, resourceType)
				}
				rawEntries = arr
			}
			entryCount += len(rawEntries)
			if entryCount > maxPolicyEntries {
				driftFail("%s: drift policy exceeds the entry-count limit", source)
			}
			scopes := make(map[string]struct{})
			for _, entry := range rawEntries {
				scope := validateEntry(source, resourceType, mode, entry)
				if _, duplicate := scopes[scope]; duplicate {
					display := "unknown"
					if entryObject, ok := entry.(JsonObject); ok {
						if path, ok := entryObject["path"]; ok {
							display = fmt.Sprintf("%v", path)
						} else if targetPath, ok := entryObject["target_path"]; ok {
							display = fmt.Sprintf("%v", targetPath)
						}
					}
					driftFail("%s duplicate %s entry for %s path %s", source, mode, resourceType, display)
				}
				scopes[scope] = struct{}{}
			}
		}
		fill := make(map[string]string)
		for _, entry := range driftEntriesFor(obj, resourceType, "projection_fill") {
			text, _ := entry["path"].(string)
			fill[pathMarker(parsePolicyPath(text))] = text
		}
		for _, entry := range driftEntriesFor(obj, resourceType, "projection_omit") {
			text, _ := entry["path"].(string)
			if _, conflict := fill[pathMarker(parsePolicyPath(text))]; conflict {
				driftFail("%s projection_fill and projection_omit entries for %s conflict on path %s", source, resourceType, text)
			}
		}
	}
	return obj
}

// validateDriftPolicyWildcardLimits ports the per-resource-type
// plan_tolerate wildcard-count limit enforced by the DriftPolicy
// constructor in node-src/domain/drift-policy.ts, after validatePolicy
// succeeds.
func validateDriftPolicyWildcardLimits(data JsonObject, source string) {
	resourceTypes, _ := data["resource_types"].(JsonObject)
	for _, resourceType := range sortedKeys(resourceTypes) {
		wildcardCount := 0
		for _, entry := range driftEntriesFor(data, resourceType, "plan_tolerate") {
			pathText, _ := entry["path"].(string)
			for _, segment := range parsePolicyPath(pathText) {
				if s, ok := segment.(string); ok && s == policyWildcard {
					wildcardCount++
					break
				}
			}
		}
		if wildcardCount > maxPlanTolerateWildcardsPerResource {
			driftFail("%s: plan_tolerate wildcard entries exceed the per-resource limit", source)
		}
	}
}

// validateDriftPolicy reproduces the failure behavior of
// `new DriftPolicy(data, source)` in node-src/domain/drift-policy.ts: nil
// data is treated as the constructor's own `data === null` default (the
// empty policy `{version: 1, resource_types: {}}`), so a manifest with no
// drift_policy key produces no error. Returns nil when the document is
// valid, or an error whose message is exactly what
// node-src/metadata/packs.ts's validatePackManifest would have re-raised
// through its own fail(detail) after catching the constructor's thrown
// error.
func validateDriftPolicy(data any, source string) (err error) {
	defer func() {
		if r := recover(); r != nil {
			failure, ok := r.(driftPolicyFailure)
			if !ok {
				panic(r)
			}
			err = fmt.Errorf("%s", failure.message)
		}
	}()
	if data == nil {
		data = JsonObject{"version": float64(1), "resource_types": JsonObject{}}
	}
	validated := validateDriftPolicyData(data, source)
	validateDriftPolicyWildcardLimits(validated, source)
	return nil
}
