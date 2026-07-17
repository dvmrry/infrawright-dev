package metadata

// This file ports node-src/domain/drift-policy.ts and the path-syntax and
// selector helpers it uses from node-src/domain/policy-paths.ts. In addition
// to the validation path used by node-src/metadata/packs.ts, DriftPolicy is
// the shared runtime used by plan evaluation and state projection. It keeps
// declaration-order matching, canonical exact-selector alias precedence,
// wildcard ambiguity, and identity-based stale-entry accounting from the
// TypeScript source.
//
// The Go representation snapshots the validated JSON tree and exposes
// detached copies at its boundaries. That is a defensive adaptation of the
// TypeScript readonly API: callers cannot mutate policy meaning after
// construction, while PolicyEntry's hidden policy/id pair preserves the
// source's object-identity semantics for MarkMatched. Match accounting is
// mutex-protected so a policy may safely be shared by concurrent readers.
//
// parsePolicyPath retains node-src/domain/policy-paths.ts's optional `what`
// label because the future assessment-guidance consumer supplies it when
// normalizing report paths; validation and projection callers use the
// source-defined "policy path" default.

import (
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/dvmrry/infrawright-dev/go/internal/canonjson"
)

// policyListMarker and policyWildcard port POLICY_LIST_MARKER and
// POLICY_WILDCARD from node-src/domain/policy-paths.ts.
const (
	policyListMarker = "[]"
	policyWildcard   = "*"
)

// maxPolicyEntries and maxPlanTolerateWildcardsPerResource port
// MAX_POLICY_ENTRIES and MAX_PLAN_TOLERATE_WILDCARDS_PER_RESOURCE from
// node-src/domain/drift-policy.ts.
const (
	maxPolicyEntries                    = 50_000
	maxPlanTolerateWildcardsPerResource = 1_000
)

// PolicyMode identifies one entry list in node-src/domain/drift-policy.ts's
// PolicyMode union.
type PolicyMode string

const (
	// PolicyProjectionOmit ports MODES[0] from
	// node-src/domain/drift-policy.ts.
	PolicyProjectionOmit PolicyMode = "projection_omit"
	// PolicyProjectionSync ports MODES[1] from
	// node-src/domain/drift-policy.ts.
	PolicyProjectionSync PolicyMode = "projection_sync"
	// PolicyProjectionFill ports MODES[2] from
	// node-src/domain/drift-policy.ts.
	PolicyProjectionFill PolicyMode = "projection_fill"
	// PolicyProjectionOmitIf ports MODES[3] from
	// node-src/domain/drift-policy.ts.
	PolicyProjectionOmitIf PolicyMode = "projection_omit_if"
	// PolicyPlanTolerate ports MODES[4] from
	// node-src/domain/drift-policy.ts.
	PolicyPlanTolerate PolicyMode = "plan_tolerate"
)

var policyModes = []PolicyMode{
	PolicyProjectionOmit,
	PolicyProjectionSync,
	PolicyProjectionFill,
	PolicyProjectionOmitIf,
	PolicyPlanTolerate,
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

func driftFail(format string, args ...any) {
	// node-src/domain/drift-policy.ts and policy-paths.ts throw their own
	// error classes. This metadata package uses one exported error spine, so
	// their exact messages travel through the existing *MetadataError panic
	// and recoverMetadataError boundary instead of introducing parallel Go
	// error types.
	failf(format, args...)
}

// PolicyEntry is the immutable Go handle for PolicyEntry from
// node-src/domain/drift-policy.ts. Its hidden policy/id pair preserves source
// object identity without exposing mutable internals.
type PolicyEntry struct {
	policy *DriftPolicy
	id     int
}

// StalePolicyEntry ports StalePolicyEntry from
// node-src/domain/drift-policy.ts.
type StalePolicyEntry struct {
	// ResourceType ports StalePolicyEntry.resource_type from
	// node-src/domain/drift-policy.ts.
	ResourceType string `json:"resource_type"`
	// Mode ports StalePolicyEntry.mode from
	// node-src/domain/drift-policy.ts.
	Mode PolicyMode `json:"mode"`
	// Path ports StalePolicyEntry.path from
	// node-src/domain/drift-policy.ts.
	Path string `json:"path"`
}

// StaleEntriesOptions is the Go form of the options object accepted by
// DriftPolicy.staleEntries in node-src/domain/drift-policy.ts. A nil or empty
// ResourceTypes set selects every resource type; a nil or empty Modes slice
// selects every mode in source-defined order.
type StaleEntriesOptions struct {
	// ResourceTypes ports DriftPolicy.staleEntries options.resourceTypes from
	// node-src/domain/drift-policy.ts.
	ResourceTypes map[string]struct{}
	// Modes ports DriftPolicy.staleEntries options.modes from
	// node-src/domain/drift-policy.ts.
	Modes []PolicyMode
}

type policySnapshotEntry struct {
	id   int
	data JsonObject
	// sourceObject keeps sourceIdentity's map alive, preventing pointer reuse
	// while any policy or PolicyEntry handle can participate in identity matching.
	sourceObject   JsonObject
	sourceIdentity uintptr
}

type compiledPolicyEntry struct {
	entry    *policySnapshotEntry
	order    int
	selector []policyPathSegment
}

// DriftPolicy ports DriftPolicy from node-src/domain/drift-policy.ts as an
// immutable validated snapshot with identity-based match accounting. All
// methods are safe for concurrent use.
type DriftPolicy struct {
	data JsonObject

	entriesByResource    map[string]map[PolicyMode][]*compiledPolicyEntry
	entriesByID          []*policySnapshotEntry
	entriesBySourceID    map[uintptr]*policySnapshotEntry
	exactPlanTolerate    map[string]map[string]*compiledPolicyEntry
	wildcardPlanTolerate map[string][]*compiledPolicyEntry

	matchedMu sync.RWMutex
	matched   map[int]struct{}
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

// parsePolicyPath ports parsePolicyPath from
// node-src/domain/policy-paths.ts. The optional label supplies the source
// function's `what` argument.
func parsePolicyPath(text string, labels ...string) []policyPathSegment {
	what := "policy path"
	if len(labels) > 0 {
		what = labels[0]
	}
	if text == "" {
		driftFail("%s must be a non-empty string", what)
	}
	var output []policyPathSegment
	for _, raw := range splitDotted(text, what) {
		output = append(output, parseSegment([]rune(raw), text, what)...)
	}
	return output
}

// ParsePolicyPath ports parsePolicyPath from
// node-src/domain/policy-paths.ts. Omitting what uses "policy path" in errors;
// supplying it preserves the source's caller-specific diagnostic label.
func ParsePolicyPath(text string, what ...string) (path []any, err error) {
	defer recoverMetadataError(&err)
	return parsePolicyPath(text, what...), nil
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

// isConcreteInteger reports whether segment is the Go representation of a
// JavaScript integer-valued number. Go traversal code naturally produces int
// indexes while decoded numeric values are float64, so both families map to
// ConcretePathSegment's TypeScript `number`; json.Number and *big.Int remain
// distinct and fail closed.
func isConcreteInteger(segment any) bool {
	switch value := segment.(type) {
	case int, int8, int16, int32, int64:
		return true
	case uint, uint8, uint16, uint32, uint64:
		return true
	case float64:
		return !math.IsNaN(value) && !math.IsInf(value, 0) && math.Trunc(value) == value
	default:
		return false
	}
}

// concreteEqualsIndex compares a Go concrete-path number with a parsed policy
// index. Parsed exact indexes are non-negative and no larger than JavaScript's
// MAX_SAFE_INTEGER; larger selectors are bigint and intentionally never equal
// a ConcretePathSegment number under JavaScript strict equality.
func concreteEqualsIndex(segment any, index int64) bool {
	switch value := segment.(type) {
	case int:
		return value >= 0 && uint64(value) == uint64(index)
	case int8:
		return value >= 0 && int64(value) == index
	case int16:
		return value >= 0 && int64(value) == index
	case int32:
		return value >= 0 && int64(value) == index
	case int64:
		return value == index
	case uint:
		return uint64(value) == uint64(index)
	case uint8:
		return uint64(value) == uint64(index)
	case uint16:
		return uint64(value) == uint64(index)
	case uint32:
		return uint64(value) == uint64(index)
	case uint64:
		return value == uint64(index)
	case float64:
		return isConcreteInteger(value) && value == float64(index)
	default:
		return false
	}
}

// policySelectorMatches ports policySelectorMatches from
// node-src/domain/policy-paths.ts. The literal string "*" is deliberately
// indistinguishable from the wildcard marker after parsing, including when it
// came from a quoted selector such as fields["*"].
func policySelectorMatches(selector []policyPathSegment, actual []any) bool {
	if len(selector) != len(actual) {
		return false
	}
	for index, segment := range selector {
		candidate := actual[index]
		switch value := segment.(type) {
		case string:
			if value == policyWildcard {
				if !isConcreteInteger(candidate) {
					return false
				}
				continue
			}
			candidateString, ok := candidate.(string)
			if !ok || candidateString != value {
				return false
			}
		case int64:
			if !concreteEqualsIndex(candidate, value) {
				return false
			}
		case *big.Int:
			// parsePolicyPath emits bigint beyond MAX_SAFE_INTEGER. The
			// source's ConcretePathSegment excludes bigint, and strict
			// equality never equates one with a JavaScript number.
			return false
		default:
			return false
		}
	}
	return true
}

// PolicySelectorMatches ports policySelectorMatches from
// node-src/domain/policy-paths.ts. Selector must be a successful
// ParsePolicyPath result; actual accepts Go string and integer path segments.
func PolicySelectorMatches(selector, actual []any) bool {
	return policySelectorMatches(selector, actual)
}

// normalizePolicyPath ports normalizePolicyPath from
// node-src/domain/policy-paths.ts.
func normalizePolicyPath(path []policyPathSegment) []string {
	normalized := make([]string, len(path))
	for index, segment := range path {
		if isCollectionSelector(segment) {
			normalized[index] = policyListMarker
			continue
		}
		text, ok := segment.(string)
		if !ok {
			return nil
		}
		normalized[index] = text
	}
	return normalized
}

// NormalizePolicyPath ports normalizePolicyPath from
// node-src/domain/policy-paths.ts. Path must be a successful ParsePolicyPath
// result.
func NormalizePolicyPath(path []any) []string {
	return normalizePolicyPath(path)
}

// formatPolicyPath ports formatPolicyPath from
// node-src/domain/policy-paths.ts. It remains unexported because no current or
// planned plan/adopt consumer calls the source symbol.
func formatPolicyPath(path []policyPathSegment) string {
	if len(path) == 0 {
		return "<root>"
	}
	parts := make([]string, 0, len(path))
	appendSelector := func(rendered string) {
		if len(parts) == 0 {
			parts = append(parts, rendered)
			return
		}
		parts[len(parts)-1] += rendered
	}
	for _, segment := range path {
		switch value := segment.(type) {
		case string:
			if value == policyWildcard || value == policyListMarker {
				appendSelector(policyListMarker)
				continue
			}
			parts = append(parts, value)
		case int64:
			appendSelector(fmt.Sprintf("[%d]", value))
		case *big.Int:
			appendSelector("[" + value.String() + "]")
		default:
			return ""
		}
	}
	return strings.Join(parts, ".")
}

// concretePathMarker creates pathMarker's key for an exact concrete path.
// Exact policy indexes are always non-negative safe integers, so any other
// numeric shape cannot select an exact compiled entry.
func concretePathMarker(path []any) (string, bool) {
	parsed := make([]policyPathSegment, 0, len(path))
	for _, segment := range path {
		switch value := segment.(type) {
		case string:
			parsed = append(parsed, value)
		default:
			var index int64
			matched := false
			switch number := value.(type) {
			case int:
				if number >= 0 && uint64(number) <= uint64(maxSafeInteger) {
					index, matched = int64(number), true
				}
			case int8:
				if number >= 0 {
					index, matched = int64(number), true
				}
			case int16:
				if number >= 0 {
					index, matched = int64(number), true
				}
			case int32:
				if number >= 0 {
					index, matched = int64(number), true
				}
			case int64:
				if number >= 0 && number <= maxSafeInteger {
					index, matched = number, true
				}
			case uint:
				if uint64(number) <= uint64(maxSafeInteger) {
					index, matched = int64(number), true
				}
			case uint8:
				index, matched = int64(number), true
			case uint16:
				index, matched = int64(number), true
			case uint32:
				index, matched = int64(number), true
			case uint64:
				if number <= uint64(maxSafeInteger) {
					index, matched = int64(number), true
				}
			case float64:
				if isConcreteInteger(number) && number >= 0 && number <= float64(maxSafeInteger) {
					index, matched = int64(number), true
				}
			}
			if !matched {
				return "", false
			}
			parsed = append(parsed, index)
		}
	}
	return pathMarker(parsed), true
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

// driftIsJsonScalar ports drift-policy.ts's own local isJsonScalar. PR 247
// widened it to retain losslessly parsed numeric policy values while still
// rejecting non-finite native numbers.
func driftIsJsonScalar(value any) bool {
	if value == nil {
		return true
	}
	switch v := value.(type) {
	case string, bool, json.Number:
		return true
	case float64:
		return !math.IsNaN(v) && !math.IsInf(v, 0)
	default:
		return false
	}
}

// driftNumericScalarMarker ports numericScalarMarker from
// node-src/domain/drift-policy.ts. The literal marker bytes are internal; the
// contract is that values Node maps to the same Terraform numeric scope map to
// the same deterministic Go marker as well.
func driftNumericScalarMarker(value any) string {
	if token, ok := value.(json.Number); ok && jsonIntegerToken.MatchString(string(token)) {
		integer, valid := new(big.Int).SetString(string(token), 10)
		if valid {
			return "integer:" + integer.String()
		}
	}

	var numeric float64
	switch v := value.(type) {
	case json.Number:
		numeric, _ = strconv.ParseFloat(string(v), 64)
	case float64:
		numeric = v
	default:
		driftFail("drift policy numeric scalar marker received a non-number")
	}
	if !math.IsNaN(numeric) && !math.IsInf(numeric, 0) && math.Trunc(numeric) == numeric {
		integer, _ := new(big.Float).SetFloat64(numeric).Int(nil)
		return "integer:" + integer.String()
	}
	return "float:" + strconv.FormatFloat(numeric, 'g', -1, 64)
}

// driftJSONScalarMarker ports jsonScalarMarker from
// node-src/domain/drift-policy.ts for projection_omit_if duplicate scopes.
func driftJSONScalarMarker(value any) string {
	switch v := value.(type) {
	case nil:
		return "null"
	case bool:
		return "boolean:" + strconv.FormatBool(v)
	case string:
		return "string:" + jsonQuote(v)
	case json.Number, float64:
		return "number:" + driftNumericScalarMarker(v)
	default:
		driftFail("drift policy scalar marker received a non-scalar value")
		return ""
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
		markers := make([]string, len(values))
		for index, item := range values {
			markers[index] = driftJSONScalarMarker(item)
		}
		return fmt.Sprintf("projection_omit_if\x00%s\x00%s", pathText, mustMarshalJSON(markers))
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

// isDriftPolicyVersionOne ports isSupportedDriftPolicyVersion from
// node-src/domain/drift-policy.ts. Equivalent exact decimal spellings of one
// are accepted without rounding a near-one value through binary64.
func isDriftPolicyVersionOne(value any) bool {
	return canonjson.TerraformJSONExactlyEqual(value, float64(1))
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
			modeName := string(mode)
			var rawEntries []any
			if rawEntriesValue, has := resource[modeName]; has {
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
				scope := validateEntry(source, resourceType, modeName, entry)
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

// clonePolicyValue recursively copies a validated policy JSON tree while
// retaining shared object identity. JSON text cannot encode aliases, but the
// source constructor accepts in-memory objects and its WeakSet accounting
// observes when one entry object is deliberately reused across modes.
func clonePolicyValue(value any) any {
	return clonePolicyValueWithObjects(value, make(map[uintptr]JsonObject))
}

func clonePolicyValueWithObjects(value any, objects map[uintptr]JsonObject) any {
	switch typed := value.(type) {
	case JsonObject:
		identity := reflect.ValueOf(typed).Pointer()
		if prior, exists := objects[identity]; exists {
			return prior
		}
		cloned := make(JsonObject, len(typed))
		objects[identity] = cloned
		for key, item := range typed {
			cloned[key] = clonePolicyValueWithObjects(item, objects)
		}
		return cloned
	case []any:
		cloned := make([]any, len(typed))
		for index, item := range typed {
			cloned[index] = clonePolicyValueWithObjects(item, objects)
		}
		return cloned
	default:
		return value
	}
}

// newDriftPolicy contains the port of DriftPolicy.constructor from
// node-src/domain/drift-policy.ts.
func newDriftPolicy(data any, source string) *DriftPolicy {
	if data == nil {
		data = JsonObject{"version": float64(1), "resource_types": JsonObject{}}
	}
	validated := validateDriftPolicyData(data, source)
	validateDriftPolicyWildcardLimits(validated, source)
	snapshot := clonePolicyValue(validated).(JsonObject)

	policy := &DriftPolicy{
		data:                 snapshot,
		entriesByResource:    make(map[string]map[PolicyMode][]*compiledPolicyEntry),
		entriesBySourceID:    make(map[uintptr]*policySnapshotEntry),
		exactPlanTolerate:    make(map[string]map[string]*compiledPolicyEntry),
		wildcardPlanTolerate: make(map[string][]*compiledPolicyEntry),
		matched:              make(map[int]struct{}),
	}
	entryIdentity := make(map[uintptr]*policySnapshotEntry)
	resources, _ := snapshot["resource_types"].(JsonObject)
	for _, resourceType := range sortedKeys(resources) {
		byMode := make(map[PolicyMode][]*compiledPolicyEntry, len(policyModes))
		for _, mode := range policyModes {
			rawEntries := driftEntriesFor(snapshot, resourceType, string(mode))
			sourceEntries := driftEntriesFor(validated, resourceType, string(mode))
			compiledEntries := make([]*compiledPolicyEntry, 0, len(rawEntries))
			for order, rawEntry := range rawEntries {
				identity := reflect.ValueOf(rawEntry).Pointer()
				sourceEntry := sourceEntries[order]
				sourceIdentity := reflect.ValueOf(sourceEntry).Pointer()
				snapshotEntry := entryIdentity[identity]
				if snapshotEntry == nil {
					snapshotEntry = &policySnapshotEntry{
						id:             len(policy.entriesByID),
						data:           rawEntry,
						sourceObject:   sourceEntry,
						sourceIdentity: sourceIdentity,
					}
					entryIdentity[identity] = snapshotEntry
					policy.entriesByID = append(policy.entriesByID, snapshotEntry)
					policy.entriesBySourceID[sourceIdentity] = snapshotEntry
				}
				compiled := &compiledPolicyEntry{
					entry: snapshotEntry,
					order: order,
				}
				if mode == PolicyProjectionOmit || mode == PolicyPlanTolerate {
					pathText, _ := rawEntry["path"].(string)
					compiled.selector = parsePolicyPath(pathText)
				}
				compiledEntries = append(compiledEntries, compiled)
			}
			byMode[mode] = compiledEntries
		}
		policy.entriesByResource[resourceType] = byMode

		exact := make(map[string]*compiledPolicyEntry)
		var wildcard []*compiledPolicyEntry
		for _, entry := range byMode[PolicyPlanTolerate] {
			hasWildcard := false
			for _, segment := range entry.selector {
				if text, ok := segment.(string); ok && text == policyWildcard {
					hasWildcard = true
					break
				}
			}
			if hasWildcard {
				wildcard = append(wildcard, entry)
				continue
			}
			marker := pathMarker(entry.selector)
			if _, exists := exact[marker]; !exists {
				// Textually distinct aliases such as field[0] and
				// field[00] canonicalize to the same selector. Keep the
				// first exactly as the source does; later aliases stay stale.
				exact[marker] = entry
			}
		}
		policy.exactPlanTolerate[resourceType] = exact
		policy.wildcardPlanTolerate[resourceType] = wildcard
	}
	return policy
}

// NewDriftPolicy ports DriftPolicy.constructor from
// node-src/domain/drift-policy.ts. It snapshots validated input and compiles
// exact and wildcard plan-tolerance indexes; nil selects the source-defined
// empty version-1 policy.
func NewDriftPolicy(data any, source string) (policy *DriftPolicy, err error) {
	defer recoverMetadataError(&err)
	return newDriftPolicy(data, source), nil
}

func (e PolicyEntry) data() JsonObject {
	if e.policy == nil || e.id < 0 || e.id >= len(e.policy.entriesByID) {
		return nil
	}
	entry := e.policy.entriesByID[e.id]
	if entry.id != e.id {
		return nil
	}
	return clonePolicyValue(entry.data).(JsonObject)
}

// Data exposes the source fields of PolicyEntry from
// node-src/domain/drift-policy.ts as a detached JSON object. This detached Go
// view of the record has no separate Node method analogue; a zero or invalid
// handle returns nil.
func (e PolicyEntry) Data() JsonObject {
	return e.data()
}

func (p *DriftPolicy) entries(resourceType string, mode PolicyMode) []PolicyEntry {
	if p == nil {
		return []PolicyEntry{}
	}
	compiled := p.entriesByResource[resourceType][mode]
	entries := make([]PolicyEntry, len(compiled))
	for index, entry := range compiled {
		entries[index] = PolicyEntry{policy: p, id: entry.entry.id}
	}
	return entries
}

// Entries ports DriftPolicy.entries from node-src/domain/drift-policy.ts. It
// returns immutable identity handles in declaration order and detaches the
// returned slice from policy storage.
func (p *DriftPolicy) Entries(resourceType string, mode PolicyMode) []PolicyEntry {
	return p.entries(resourceType, mode)
}

func (p *DriftPolicy) markMatched(entry PolicyEntry) {
	if p == nil || entry.policy == nil || entry.id < 0 || entry.id >= len(entry.policy.entriesByID) {
		return
	}
	sourceEntry := entry.policy.entriesByID[entry.id]
	if sourceEntry.id != entry.id {
		return
	}
	localEntry := p.entriesBySourceID[sourceEntry.sourceIdentity]
	if localEntry == nil {
		return
	}
	p.markMatchedID(localEntry.id)
}

// MarkMatched ports DriftPolicy.markMatched from
// node-src/domain/drift-policy.ts. A handle from another policy marks an entry
// only when both policies were constructed from the same raw entry object;
// zero, invalid, and separately allocated equal entries have no effect. This
// matches the source WeakSet's object-identity behavior.
func (p *DriftPolicy) MarkMatched(entry PolicyEntry) {
	p.markMatched(entry)
}

func (p *DriftPolicy) markMatchedID(id int) {
	p.matchedMu.Lock()
	defer p.matchedMu.Unlock()
	p.matched[id] = struct{}{}
}

func (p *DriftPolicy) projectionOmits(resourceType string, path []any) bool {
	if p == nil {
		return false
	}
	actual := append([]any(nil), path...)
	for _, entry := range p.entriesByResource[resourceType][PolicyProjectionOmit] {
		if policySelectorMatches(entry.selector, actual) {
			p.markMatchedID(entry.entry.id)
			return true
		}
	}
	return false
}

// ProjectionOmits ports DriftPolicy.projectionOmits from
// node-src/domain/drift-policy.ts. A match marks only the first
// declaration-order selector.
func (p *DriftPolicy) ProjectionOmits(resourceType string, path []any) bool {
	return p.projectionOmits(resourceType, path)
}

func (p *DriftPolicy) toleratesPlanPath(resourceType string, path []any, action string) bool {
	if p == nil || action != "update" {
		return false
	}
	actual := append([]any(nil), path...)
	var matched *compiledPolicyEntry
	if marker, ok := concretePathMarker(actual); ok {
		exact := p.exactPlanTolerate[resourceType][marker]
		if exact != nil && policySelectorMatches(exact.selector, actual) {
			matched = exact
		}
	}
	for _, candidate := range p.wildcardPlanTolerate[resourceType] {
		if !policySelectorMatches(candidate.selector, actual) {
			continue
		}
		if matched == nil || candidate.order < matched.order {
			matched = candidate
		}
	}
	if matched == nil {
		return false
	}
	p.markMatchedID(matched.entry.id)
	return true
}

// ToleratesPlanPath ports DriftPolicy.toleratesPlanPath from
// node-src/domain/drift-policy.ts. Only update is supported; source order
// breaks exact/wildcard overlap and canonical-alias ties.
func (p *DriftPolicy) ToleratesPlanPath(resourceType string, path []any, action string) bool {
	return p.toleratesPlanPath(resourceType, path, action)
}

func (p *DriftPolicy) staleEntries(options StaleEntriesOptions) []StalePolicyEntry {
	stale := make([]StalePolicyEntry, 0)
	if p == nil {
		return stale
	}
	resourceTypes := make(map[string]struct{}, len(options.ResourceTypes))
	for resourceType := range options.ResourceTypes {
		resourceTypes[resourceType] = struct{}{}
	}
	modes := append([]PolicyMode(nil), options.Modes...)
	if len(modes) == 0 {
		modes = append([]PolicyMode(nil), policyModes...)
	}
	p.matchedMu.RLock()
	matched := make(map[int]struct{}, len(p.matched))
	for id := range p.matched {
		matched[id] = struct{}{}
	}
	p.matchedMu.RUnlock()

	resources, _ := p.data["resource_types"].(JsonObject)
	for _, resourceType := range sortedKeys(resources) {
		if len(resourceTypes) > 0 {
			if _, selected := resourceTypes[resourceType]; !selected {
				continue
			}
		}
		for _, mode := range modes {
			for _, entry := range p.entriesByResource[resourceType][mode] {
				if _, used := matched[entry.entry.id]; used {
					continue
				}
				path, ok := entry.entry.data["path"].(string)
				if !ok {
					path, _ = entry.entry.data["target_path"].(string)
				}
				stale = append(stale, StalePolicyEntry{
					ResourceType: resourceType,
					Mode:         mode,
					Path:         path,
				})
			}
		}
	}
	return stale
}

// StaleEntries ports DriftPolicy.staleEntries from
// node-src/domain/drift-policy.ts. Output order is resource type by source
// code point, requested mode order (MODES by default), then declaration order.
func (p *DriftPolicy) StaleEntries(options StaleEntriesOptions) []StalePolicyEntry {
	return p.staleEntries(options)
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
	defer recoverMetadataError(&err)
	_ = newDriftPolicy(data, source)
	return nil
}
