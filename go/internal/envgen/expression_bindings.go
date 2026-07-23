// Package envgen ports the original implementation,
// the original implementation, and
// the original implementation: Terraform expression-binding
// compilation, the cross-state reference DAG, and gen-env's env-root
// generation (backend blocks, provider headers, module source resolution,
// variable wiring, binding emission, and generated-root file lifecycle).
// See docs/terraform-expression-bindings.md for the operator-facing design
// intent behind expression bindings.
//
// Every exported symbol's doc comment names the the original implementation export
// it ports; those TypeScript files remain the differential oracle until
// this port is independently qualified, per the Go runtime contract.
//
// # Error model
//
// The three ported TS sources throw a plain `TypeError` (never a
// ProcessFailure) at every validation failure. This package follows the
// same convention go/internal/tfrender/hcl_tfvars.go and
// go/internal/metadata/validation.go already established for this shape of
// exception-heavy source: a package-local panic-carrying error type
// (bindingsError) plus a fail()-style helper, with every exported entry
// point deferring a recover that converts the panic back into a normal Go
// error return. This lets the many small, deeply nested validation helpers
// ported below (parsePath, parseBinding, applyExpressionBindings's tree
// walk, the schema-cursor traversal) abandon a call from arbitrary depth
// exactly the way `throw` does in the TS source, without threading an
// explicit error return through every intermediate call.
//
// # Value model
//
// Wherever the TS source types a value `unknown` and narrows it at runtime
// (record()/Array.isArray() checks), this port uses `any` holding one of
// this repository's canonjson.Value shapes (map[string]any, []any,
// json.Number, float64, string, bool, nil) -- see
// go/internal/canonjson/render.go's Value doc comment. An
// ExpressionBinding's PathParts holds a mix of string (an attribute name)
// and int (a canonical, already-range-checked list index), mirroring the TS
// `readonly (string | number)[]`.
package envgen

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/dvmrry/infrawright-dev/go/internal/canonjson"
	"github.com/dvmrry/infrawright-dev/go/internal/metadata"
	"github.com/dvmrry/infrawright-dev/go/internal/tfrender"
)

// bindingsError is this file's "throw new TypeError(message)" analogue; see
// the package doc comment's "Error model" section.
type bindingsError struct{ message string }

func (e *bindingsError) Error() string { return e.message }

// bindingsFail panics with a *bindingsError carrying message, the Go
// analogue of every bare `throw new TypeError(...)` in
// the original implementation.
func bindingsFail(format string, args ...any) {
	panic(&bindingsError{message: fmt.Sprintf(format, args...)})
}

// recoverBindingsError is deferred by every exported entry point in this
// package (as `defer recoverBindingsError(&err)`) to convert a recovered
// *bindingsError panic into a normal error return. Any other recovered
// value is re-panicked, since it indicates a genuine bug rather than an
// expected validation failure.
func recoverBindingsError(err *error) {
	if r := recover(); r != nil {
		if be, ok := r.(*bindingsError); ok {
			*err = be
			return
		}
		panic(r)
	}
}

// Regular expressions ported from the original implementation.
//
// The TS ALLOWED_EXPRESSIONS grammar relies on negative lookahead
// (`\$(?!\{)`, `%(?!\{)`) inside its HCL_STRING sub-pattern, which Go's
// RE2-based regexp engine cannot express (RE2 deliberately excludes
// backtracking-only features for its linear-time guarantee). That grammar
// is therefore hand-scanned below (matchesAllowedExpression and its
// helpers) rather than compiled as a single regexp -- a lookahead is just a
// one-character peek once written as an explicit scanner, so no expressive
// power is lost, only the regexp-engine shorthand for it. Every other
// regexp below (none of which need lookahead) is a direct, unmodified port.
//
// Probed against the compiled TypeScript (npx esbuild
// the original implementation --bundle --platform=node
// --format=esm --external:lossless-json --outfile=.../expression-bindings.mjs,
// then a Node driver calling validateExpression over the allowlist/malformed
// vectors from the original test corpus plus adjacent edge
// cases -- unterminated/leading-zero indexes, "${"/"%{" inside a quoted list
// literal, multi-selector chains, whitespace-only vs. non-Python-whitespace
// separators) to confirm matchesAllowedExpression's byte-for-byte agreement
// with the lookahead-bearing regexp grammar. See this package's port report
// for the full probe transcript.
var (
	pathSegmentPattern  = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	exactVarExprPattern = regexp.MustCompile(`^var\.([A-Za-z_][A-Za-z0-9_]*)$`)
	numericIndexToken   = regexp.MustCompile(`^(?:0|[1-9][0-9]*)$`)
)

// jsMaxSafeInteger is JavaScript's Number.MAX_SAFE_INTEGER (2^53 - 1), the
// threshold parsePath's list-selector range check uses.
const jsMaxSafeInteger = int64(1)<<53 - 1

// ExpressionBinding is the Go analogue of the ExpressionBinding interface in
// the original implementation.
type ExpressionBinding struct {
	Address string
	Key     string
	Path    string
	// PathParts holds a mix of string (an attribute name) and int (a
	// canonical, already-range-checked list index), in traversal order.
	PathParts []any
	// Expression is always non-empty and already validated against the v1
	// allowlist (see ValidateExpression).
	Expression string
	Sensitive  bool
	// Reason is nil for the TS source's `reason: string | null` being null.
	Reason *string
}

// HclExpression is the Go analogue of the HclExpression class in
// the original implementation: a marker value distinguishing an
// already-validated Terraform expression from an ordinary JSON scalar
// inside a canonjson.Value tree, used as a *HclExpression sentinel wherever
// the TS source checks `value instanceof HclExpression`.
type HclExpression struct {
	Expression string
}

// newHclExpression panics (via bindingsFail, through validateExpression) if
// expression is outside the v1 allowlist; ported from the HclExpression
// constructor in the original implementation, which likewise
// throws from `validateExpression(expression, "HclExpression")`.
func newHclExpression(expression string) *HclExpression {
	return &HclExpression{Expression: validateExpression(expression, "HclExpression")}
}

// NewHclExpression ports the HclExpression constructor from
// the original implementation.
func NewHclExpression(expression string) (result *HclExpression, err error) {
	defer recoverBindingsError(&err)
	return newHclExpression(expression), nil
}

// pythonJSONString ports the local pythonJsonString helper from
// the original implementation: `JSON.stringify(value)` with
// every UTF-16 code unit at or above 0x7F additionally escaped as \uXXXX
// (so, unlike a bare JSON.stringify, this never emits a raw non-ASCII byte).
//
// This duplicates go/internal/canonjson's unexported artifact-string
// encoder (same control-character shorthands, same 0x7F threshold) rather
// than reusing it: canonjson exports no single-string-quoting primitive
// (only whole-document renderers), and the TS source itself does not import
// python-lossless-artifact.ts's encoder for this helper either -- it is a
// second, independent implementation of the same "ensure_ascii json.dumps"
// string quoting rule in the TS tree already, not a gap this port
// introduces.
func pythonJSONString(value string) string {
	var sb strings.Builder
	sb.WriteByte('"')
	for _, unit := range utf16.Encode([]rune(value)) {
		switch unit {
		case 0x22:
			sb.WriteString(`\"`)
		case 0x5c:
			sb.WriteString(`\\`)
		case 0x08:
			sb.WriteString(`\b`)
		case 0x09:
			sb.WriteString(`\t`)
		case 0x0a:
			sb.WriteString(`\n`)
		case 0x0c:
			sb.WriteString(`\f`)
		case 0x0d:
			sb.WriteString(`\r`)
		default:
			if unit < 0x20 || unit >= 0x7f {
				fmt.Fprintf(&sb, `\u%04x`, unit)
			} else {
				sb.WriteByte(byte(unit))
			}
		}
	}
	sb.WriteByte('"')
	return sb.String()
}

// jsStringify approximates plain `JSON.stringify(value)` (no additional
// non-ASCII escaping) for the several error-message interpolations in
// the original implementation that use it directly rather than
// through pythonJsonString: control characters below 0x20 get the standard
// JSON escapes, quote and backslash are escaped, and every other character
// (including non-ASCII) is left literal, exactly like JavaScript's
// JSON.stringify on a plain string.
func jsStringify(value string) string {
	var sb strings.Builder
	sb.WriteByte('"')
	for _, r := range value {
		switch r {
		case '"':
			sb.WriteString(`\"`)
		case '\\':
			sb.WriteString(`\\`)
		case '\b':
			sb.WriteString(`\b`)
		case '\f':
			sb.WriteString(`\f`)
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

// hclKey ports hclKey from the original implementation.
func hclKey(value string) string {
	if pathSegmentPattern.MatchString(value) {
		return value
	}
	return pythonJSONString(value)
}

// containsControlCharacter ports the CONTROL_CHARACTERS regexp test
// (`/[\x00-\x1f\x7f]/u`) from the original implementation.
func containsControlCharacter(value string) bool {
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return true
		}
	}
	return false
}

// --- matchesAllowedExpression: hand-scanned ALLOWED_EXPRESSIONS grammar ---
// (see this file's regexp doc comment for why this is not a compiled
// regexp). Every helper below consumes from a byte offset and returns the
// offset just past its match, or -1 for no match; matchesAllowedExpression
// requires a full-string match (offset advances to len(expression)) from at
// least one of the five alternatives, mirroring `^...$`-anchored
// alternation.

func isIdentStartByte(b byte) bool {
	return b == '_' || (b >= 'A' && b <= 'Z') || (b >= 'a' && b <= 'z')
}

func isIdentPartByte(b byte) bool {
	return isIdentStartByte(b) || (b >= '0' && b <= '9')
}

// matchIdent consumes IDENT (`[A-Za-z_][A-Za-z0-9_]*`) at pos.
func matchIdent(s string, pos int) int {
	if pos >= len(s) || !isIdentStartByte(s[pos]) {
		return -1
	}
	i := pos + 1
	for i < len(s) && isIdentPartByte(s[i]) {
		i++
	}
	return i
}

func matchLiteral(s string, pos int, literal string) int {
	if pos > len(s) || !strings.HasPrefix(s[pos:], literal) {
		return -1
	}
	return pos + len(literal)
}

// matchHclString consumes one HCL_STRING at pos (must start with `"`):
// `"(?:[^"\\$%]|\$(?!\{)|%(?!\{)|\\["\\nrt])*"`, ported as an explicit scan
// (see this file's regexp doc comment). Byte-at-a-time iteration over the
// "any other character" branch is safe for multi-byte UTF-8 runes: none of
// the four excluded bytes ('"', '\\', '$', '%') ever appears as a leading or
// continuation byte of a valid multi-byte UTF-8 sequence (all four are pure
// ASCII), so consuming one byte at a time still consumes every such rune in
// full before the next structural check.
func matchHclString(s string, pos int) int {
	if pos >= len(s) || s[pos] != '"' {
		return -1
	}
	i := pos + 1
	for i < len(s) {
		switch s[i] {
		case '"':
			return i + 1
		case '\\':
			if i+1 >= len(s) {
				return -1
			}
			switch s[i+1] {
			case '"', '\\', 'n', 'r', 't':
				i += 2
			default:
				return -1
			}
		case '$', '%':
			if i+1 < len(s) && s[i+1] == '{' {
				return -1
			}
			i++
		default:
			i++
		}
	}
	return -1
}

// matchDigits consumes a bare `[0-9]+` run at pos (used for NUMERIC_INDEX's
// bracket contents; leading-zero canonicalization is enforced later by
// parsePath, not by this allowlist grammar -- matching the TS regexp, which
// accepts any digit run here too).
func matchDigits(s string, pos int) int {
	if pos >= len(s) || s[pos] < '0' || s[pos] > '9' {
		return -1
	}
	i := pos + 1
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	return i
}

// matchSelectorTail consumes SELECTOR_TAIL:
// `(?:\.${IDENT}|\[${HCL_STRING}\]|${NUMERIC_INDEX})*`.
func matchSelectorTail(s string, pos int) int {
	i := pos
	for {
		if i < len(s) && s[i] == '.' {
			if next := matchIdent(s, i+1); next >= 0 {
				i = next
				continue
			}
		}
		if i < len(s) && s[i] == '[' {
			if hs := matchHclString(s, i+1); hs >= 0 && hs < len(s) && s[hs] == ']' {
				i = hs + 1
				continue
			}
			if ds := matchDigits(s, i+1); ds >= 0 && ds < len(s) && s[ds] == ']' {
				i = ds + 1
				continue
			}
		}
		break
	}
	return i
}

// matchModuleSelector consumes MODULE_SELECTOR: `module\.${IDENT}${SELECTOR_TAIL}`.
func matchModuleSelector(s string, pos int) int {
	p := matchLiteral(s, pos, "module.")
	if p < 0 {
		return -1
	}
	p = matchIdent(s, p)
	if p < 0 {
		return -1
	}
	return matchSelectorTail(s, p)
}

// matchDataSelector consumes DATA_SELECTOR: `data\.${IDENT}\.${IDENT}${SELECTOR_TAIL}`.
func matchDataSelector(s string, pos int) int {
	p := matchLiteral(s, pos, "data.")
	if p < 0 {
		return -1
	}
	p = matchIdent(s, p)
	if p < 0 {
		return -1
	}
	p = matchLiteral(s, p, ".")
	if p < 0 {
		return -1
	}
	p = matchIdent(s, p)
	if p < 0 {
		return -1
	}
	return matchSelectorTail(s, p)
}

// matchListElement consumes LIST_ELEMENT: `(?:${MODULE_SELECTOR}|${DATA_SELECTOR}|${HCL_STRING})`.
func matchListElement(s string, pos int) int {
	if p := matchModuleSelector(s, pos); p >= 0 {
		return p
	}
	if p := matchDataSelector(s, pos); p >= 0 {
		return p
	}
	return matchHclString(s, pos)
}

// isPythonWhitespaceRune ports the PYTHON_WHITESPACE character class from
// the original implementation: Python's `re` whitespace set,
// deliberately not JavaScript's native `\s` (which additionally matches
// U+FEFF, the byte-order mark -- excluded here on purpose).
func isPythonWhitespaceRune(r rune) bool {
	switch {
	case r >= 0x09 && r <= 0x0d:
		return true
	case r >= 0x1c && r <= 0x20:
		return true
	case r == 0x85, r == 0xa0, r == 0x1680:
		return true
	case r >= 0x2000 && r <= 0x200a:
		return true
	case r >= 0x2028 && r <= 0x2029:
		return true
	case r == 0x202f, r == 0x205f, r == 0x3000:
		return true
	}
	return false
}

// matchWhitespaceRun consumes zero or more PYTHON_WHITESPACE runes at pos.
func matchWhitespaceRun(s string, pos int) int {
	i := pos
	for i < len(s) {
		r, size := decodeRuneAt(s, i)
		if !isPythonWhitespaceRune(r) {
			break
		}
		i += size
	}
	return i
}

// decodeRuneAt decodes the rune starting at byte offset i in s, treating any
// invalid encoding as a single-byte rune (utf8.DecodeRuneInString's own
// fallback behavior).
func decodeRuneAt(s string, i int) (rune, int) {
	return utf8.DecodeRuneInString(s[i:])
}

// matchListLiteral consumes the fifth ALLOWED_EXPRESSIONS alternative: a
// bracketed, comma-separated (Python-whitespace-tolerant) list of
// LIST_ELEMENTs, possibly empty.
func matchListLiteral(s string, pos int) int {
	if pos >= len(s) || s[pos] != '[' {
		return -1
	}
	i := matchWhitespaceRun(s, pos+1)
	if i < len(s) && s[i] == ']' {
		return i + 1
	}
	e := matchListElement(s, i)
	if e < 0 {
		return -1
	}
	i = e
	for {
		j := matchWhitespaceRun(s, i)
		if j >= len(s) || s[j] != ',' {
			break
		}
		k := matchWhitespaceRun(s, j+1)
		e2 := matchListElement(s, k)
		if e2 < 0 {
			break
		}
		i = e2
	}
	j := matchWhitespaceRun(s, i)
	if j < len(s) && s[j] == ']' {
		return j + 1
	}
	return -1
}

// matchesAllowedExpression reports whether expression matches one of the
// five ALLOWED_EXPRESSIONS alternatives from
// the original implementation in full (see this file's regexp
// doc comment).
func matchesAllowedExpression(expression string) bool {
	if p := matchLiteral(expression, 0, "var."); p >= 0 {
		if e := matchIdent(expression, p); e == len(expression) {
			return true
		}
	}
	if p := matchLiteral(expression, 0, "local."); p >= 0 {
		if e := matchIdent(expression, p); e == len(expression) {
			return true
		}
	}
	if e := matchDataSelector(expression, 0); e == len(expression) {
		return true
	}
	if e := matchModuleSelector(expression, 0); e == len(expression) {
		return true
	}
	if e := matchListLiteral(expression, 0); e == len(expression) {
		return true
	}
	return false
}

// validateExpression ports validateExpression from
// the original implementation.
func validateExpression(expression any, context string) string {
	s, ok := expression.(string)
	if !ok || len(s) == 0 {
		bindingsFail("%s expression must be a non-empty string", context)
	}
	if containsControlCharacter(s) {
		bindingsFail("%s expression must not contain control characters", context)
	}
	if !matchesAllowedExpression(s) {
		bindingsFail(
			"%s expression %s is outside the v1 allowlist (allowed roots: var., local., data., module.)",
			context, jsStringify(s),
		)
	}
	return s
}

// ValidateExpression ports validateExpression from
// the original implementation.
func ValidateExpression(expression any, context string) (result string, err error) {
	defer recoverBindingsError(&err)
	return validateExpression(expression, context), nil
}

// renderPath ports renderPath from the original implementation.
func renderPath(parts []any) string {
	var sb strings.Builder
	for index, part := range parts {
		if idx, isIndex := part.(int); isIndex {
			fmt.Fprintf(&sb, "[%d]", idx)
			continue
		}
		name, _ := part.(string)
		if index != 0 {
			sb.WriteByte('.')
		}
		sb.WriteString(name)
	}
	return sb.String()
}

// safeIndexFromToken parses a canonical (parsePath already validated,
// no-leading-zero) non-negative integer token, reporting ok=false if it
// exceeds JavaScript's safe-integer range. Uses arbitrary-precision parsing
// so an arbitrarily long digit token cannot overflow before the range check
// runs, matching `Number.isSafeInteger(Number(token))` never silently
// wrapping in the TS source either (Number() saturates to +Infinity for an
// enormous digit string, which also fails the safe-integer check).
func safeIndexFromToken(token string) (int, bool) {
	if len(token) > 19 {
		// More than 19 decimal digits already exceeds
		// Number.MAX_SAFE_INTEGER (16 digits) for any canonical
		// (no-leading-zero) token; short-circuit before constructing a
		// big.Int for pathological input sizes.
		return 0, false
	}
	var value int64
	for i := 0; i < len(token); i++ {
		value = value*10 + int64(token[i]-'0')
		if value > jsMaxSafeInteger {
			return 0, false
		}
	}
	if value > jsMaxSafeInteger {
		return 0, false
	}
	return int(value), true
}

// parsePath ports parsePath from the original implementation.
func parsePath(value any, context string) []any {
	s, ok := value.(string)
	if !ok || len(s) == 0 {
		bindingsFail("%s path must be a non-empty attribute path", context)
	}
	var parts []any
	offset := 0
	for offset < len(s) {
		if len(parts) > 0 && s[offset] == '.' {
			offset++
		}
		attributeEnd := matchIdent(s, offset)
		if attributeEnd < 0 {
			bindingsFail(
				"%s path %s has an unsupported segment; use dotted attributes and exact canonical numeric list selectors",
				context, jsStringify(s),
			)
		}
		attribute := s[offset:attributeEnd]
		parts = append(parts, attribute)
		offset = attributeEnd
		for offset < len(s) && s[offset] == '[' {
			close := strings.IndexByte(s[offset+1:], ']')
			if close < 0 {
				bindingsFail("%s path %s has an unterminated list selector", context, jsStringify(s))
			}
			closeAbsolute := offset + 1 + close
			token := s[offset+1 : closeAbsolute]
			if !numericIndexToken.MatchString(token) {
				bindingsFail(
					"%s path %s has unsupported list selector %s; use an exact canonical non-negative index",
					context, jsStringify(s), jsStringify(token),
				)
			}
			index, ok := safeIndexFromToken(token)
			if !ok {
				bindingsFail("%s path %s list selector exceeds the safe integer range", context, jsStringify(s))
			}
			parts = append(parts, index)
			offset = closeAbsolute + 1
		}
		if offset < len(s) && s[offset] != '.' {
			bindingsFail(
				"%s path %s has an unsupported segment; use dotted attributes and exact canonical numeric list selectors",
				context, jsStringify(s),
			)
		}
	}
	return parts
}

// parseBinding ports parseBinding from
// the original implementation. address and bindingPath are typed
// `unknown` in the TS source purely to accept the `String(...)`-coerced
// object-key values `Object.keys(...)` always actually supplies as plain
// strings at this function's only call site; Go's map[string]any keys are
// already strings, so both parameters are typed string directly here,
// dropping the unreachable `String(...)` coercion generality.
func parseBinding(address, bindingPath string, value any, resourceType string) ExpressionBinding {
	context := address + "." + bindingPath
	obj, ok := value.(map[string]any)
	if !ok {
		bindingsFail("%s binding must be an object", context)
	}
	allowed := map[string]bool{"expression": true, "sensitive": true, "reason": true}
	var unknownKeys []string
	for key := range obj {
		if !allowed[key] {
			unknownKeys = append(unknownKeys, key)
		}
	}
	if len(unknownKeys) > 0 {
		bindingsFail("%s binding has unknown key(s): %s", context, strings.Join(canonjson.SortedStrings(unknownKeys), ", "))
	}
	expressionValue, hasExpression := obj["expression"]
	if !hasExpression {
		bindingsFail("%s binding is missing expression", context)
	}
	sensitive := false
	if sensitiveValue, has := obj["sensitive"]; has {
		b, ok := sensitiveValue.(bool)
		if !ok {
			bindingsFail("%s sensitive must be a boolean", context)
		}
		sensitive = b
	}
	var reason *string
	if reasonValue, has := obj["reason"]; has && reasonValue != nil {
		s, ok := reasonValue.(string)
		if !ok {
			bindingsFail("%s reason must be a string when present", context)
		}
		reason = &s
	}
	prefix := resourceType + "."
	if !strings.HasPrefix(address, prefix) {
		bindingsFail("%s address must be %s<key>", context, prefix)
	}
	key := address[len(prefix):]
	if len(key) == 0 {
		bindingsFail("%s address has empty resource key", context)
	}
	if containsControlCharacter(key) {
		bindingsFail("%s resource key must not contain control characters", context)
	}
	pathParts := parsePath(bindingPath, context)
	return ExpressionBinding{
		Address:    address,
		Key:        key,
		Path:       renderPath(pathParts),
		PathParts:  pathParts,
		Expression: validateExpression(expressionValue, context),
		Sensitive:  sensitive,
		Reason:     reason,
	}
}

func mapKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	return keys
}

// parseExpressionBindings ports the exported parseExpressionBindings from
// the original implementation: "Parse one resource type's
// operator or generated expression-binding document."
func parseExpressionBindings(data any, resourceType string) []ExpressionBinding {
	if data == nil {
		return []ExpressionBinding{}
	}
	obj, ok := data.(map[string]any)
	if !ok {
		bindingsFail("expression bindings must be a JSON object")
	}
	var unknownKeys []string
	for key := range obj {
		if key != "resources" {
			unknownKeys = append(unknownKeys, key)
		}
	}
	if len(unknownKeys) > 0 {
		bindingsFail("expression bindings have unknown top-level key(s): %s", strings.Join(canonjson.SortedStrings(unknownKeys), ", "))
	}
	var resources map[string]any
	if resourcesValue, hasResources := obj["resources"]; hasResources {
		resources, ok = resourcesValue.(map[string]any)
		if !ok {
			bindingsFail("expression bindings resources must be an object")
		}
	} else {
		resources = map[string]any{}
	}
	bindings := []ExpressionBinding{}
	seen := map[string]bool{}
	for _, address := range canonjson.SortedStrings(mapKeys(resources)) {
		paths, ok := resources[address].(map[string]any)
		if !ok {
			bindingsFail("%s bindings must be an object", address)
		}
		for _, bindingPath := range canonjson.SortedStrings(mapKeys(paths)) {
			binding := parseBinding(address, bindingPath, paths[bindingPath], resourceType)
			identity := binding.Key + "\x00" + binding.Path
			if seen[identity] {
				bindingsFail("duplicate expression binding for %s.%s", binding.Address, binding.Path)
			}
			seen[identity] = true
			bindings = append(bindings, binding)
		}
	}
	return bindings
}

// ParseExpressionBindings ports parseExpressionBindings from
// the original implementation.
func ParseExpressionBindings(data any, resourceType string) (bindings []ExpressionBinding, err error) {
	defer recoverBindingsError(&err)
	return parseExpressionBindings(data, resourceType), nil
}

// LoadExpressionBindings ports loadExpressionBindings from
// the original implementation.
func LoadExpressionBindings(file, resourceType string) (bindings []ExpressionBinding, err error) {
	defer recoverBindingsError(&err)
	text, readErr := metadata.ReadOptionalUTF8(file, resourceType+" expression bindings")
	if readErr != nil {
		return nil, readErr
	}
	if text == nil {
		return []ExpressionBinding{}, nil
	}
	value, parseErr := canonjson.ParseDataJSONLosslessly(*text)
	if parseErr != nil {
		return nil, parseErr
	}
	return parseExpressionBindings(value, resourceType), nil
}

// ExpressionVariables ports expressionVariables from
// the original implementation. The TS source returns keys in
// sorted order (`Object.fromEntries(sortedStrings(...))`); this returns an
// ordinary Go map since every caller in this port's scope (renderExpressionBindingsHcl)
// re-sorts its keys before use anyway.
func ExpressionVariables(bindings []ExpressionBinding) map[string]bool {
	variables := map[string]bool{}
	for _, binding := range bindings {
		match := exactVarExprPattern.FindStringSubmatch(binding.Expression)
		if match == nil {
			continue
		}
		name := match[1]
		variables[name] = variables[name] || binding.Sensitive
	}
	return variables
}

// cloneJSON ports cloneJson from the original implementation.
// The TS source's LosslessNumber branch (constructing a fresh
// `new LosslessNumber(value.toString())`) has no Go analogue to reproduce:
// json.Number is an immutable string-backed value type, so copying it by
// assignment (the `default: return value` branch below) already produces an
// independent, unaliased clone with no shared mutable state -- unlike a JS
// LosslessNumber, which is a distinguishable object identity elsewhere in
// this codebase (see HclExpression's own instanceof-style Go analogue) and
// so is deliberately re-boxed by the TS source's clone.
func cloneJSON(value any) any {
	switch v := value.(type) {
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = cloneJSON(item)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(v))
		for key, item := range v {
			out[key] = cloneJSON(item)
		}
		return out
	default:
		return value
	}
}

// applyExpressionBindings ports the exported applyExpressionBindings from
// the original implementation: "Validate binding paths against
// items and replace leaves with expression sentinels."
func applyExpressionBindings(items any, bindings []ExpressionBinding) map[string]any {
	output, ok := cloneJSON(items).(map[string]any)
	if !ok {
		bindingsFail("expression binding items must be an object")
	}
	for _, binding := range bindings {
		current, ok := output[binding.Key]
		if !ok {
			bindingsFail("expression binding references unknown resource address %s", binding.Address)
		}
		for _, part := range binding.PathParts[:len(binding.PathParts)-1] {
			if idx, isIndex := part.(int); isIndex {
				arr, ok := current.([]any)
				if !ok {
					bindingsFail("expression binding %s.%s indexes a non-list value", binding.Address, binding.Path)
				}
				if idx >= len(arr) {
					bindingsFail("expression binding %s.%s has out-of-range list index [%d]", binding.Address, binding.Path, idx)
				}
				current = arr[idx]
				continue
			}
			name, _ := part.(string)
			if _, isArray := current.([]any); isArray {
				bindingsFail(
					"expression binding %s.%s traverses a list at %s; use an exact numeric list selector",
					binding.Address, binding.Path, name,
				)
			}
			obj, isObject := current.(map[string]any)
			childValue, hasChild := obj[name]
			if !isObject || !hasChild {
				bindingsFail("expression binding %s.%s has missing parent path", binding.Address, binding.Path)
			}
			current = childValue
		}
		leaf := binding.PathParts[len(binding.PathParts)-1]
		if idx, isIndex := leaf.(int); isIndex {
			arr, ok := current.([]any)
			if !ok {
				bindingsFail("expression binding %s.%s indexes a non-list value", binding.Address, binding.Path)
			}
			if idx >= len(arr) {
				bindingsFail("expression binding %s.%s has out-of-range list index [%d]", binding.Address, binding.Path, idx)
			}
			arr[idx] = newHclExpression(binding.Expression)
			continue
		}
		name, _ := leaf.(string)
		if _, isArray := current.([]any); isArray {
			bindingsFail(
				"expression binding %s.%s traverses a list at %s; use an exact numeric list selector",
				binding.Address, binding.Path, name,
			)
		}
		obj, isObject := current.(map[string]any)
		if !isObject {
			bindingsFail("expression binding %s.%s parent is not an object", binding.Address, binding.Path)
		}
		if _, hasLeaf := obj[name]; !hasLeaf {
			bindingsFail("expression binding %s.%s has missing target leaf", binding.Address, binding.Path)
		}
		obj[name] = newHclExpression(binding.Expression)
	}
	return output
}

// ApplyExpressionBindings ports applyExpressionBindings from
// the original implementation.
func ApplyExpressionBindings(items any, bindings []ExpressionBinding) (result map[string]any, err error) {
	defer recoverBindingsError(&err)
	return applyExpressionBindings(items, bindings), nil
}

// bindingSchemaCursorKind is the Go analogue of the BindingSchemaCursor
// discriminated union's `kind` field in
// the original implementation.
type bindingSchemaCursorKind int

const (
	cursorBlock bindingSchemaCursorKind = iota
	cursorEncoding
	cursorBlockList
	cursorBlockSet
)

// bindingSchemaCursor is the Go analogue of the BindingSchemaCursor type in
// the original implementation.
type bindingSchemaCursor struct {
	kind     bindingSchemaCursorKind
	block    metadata.JsonObject
	encoding metadata.TerraformTypeEncoding
	label    string
}

// schemaPathError ports schemaPathError from
// the original implementation, typed there as returning `never`
// because it always throws; this Go version returns bindingSchemaCursor
// purely so call sites can write `return schemaPathError(...)` to satisfy
// the compiler's return-value requirement -- schemaPathError itself always
// panics before returning, so that zero value is never actually observed.
func schemaPathError(binding ExpressionBinding, message string) bindingSchemaCursor {
	bindingsFail("expression binding %s.%s %s", binding.Address, binding.Path, message)
	return bindingSchemaCursor{}
}

func requireTerraformObject(value any, label string) metadata.JsonObject {
	obj, err := metadata.TerraformRequireObject(value, label)
	if err != nil {
		bindingsFail("%s", err.Error())
	}
	return obj
}

// encodingCursor ports encodingCursor from
// the original implementation.
func encodingCursor(binding ExpressionBinding, cursor bindingSchemaCursor, part any) bindingSchemaCursor {
	switch encoding := cursor.encoding.(type) {
	case metadata.TerraformPrimitiveType:
		return schemaPathError(binding, fmt.Sprintf("traverses scalar %s", cursor.label))
	case metadata.TerraformCollectionType:
		switch encoding.Kind {
		case "list":
			index, isIndex := part.(int)
			if !isIndex {
				return schemaPathError(binding, fmt.Sprintf("traverses list %s without an exact numeric selector", cursor.label))
			}
			return bindingSchemaCursor{
				kind:     cursorEncoding,
				encoding: encoding.Inner,
				label:    fmt.Sprintf("%s[%d]", cursor.label, index),
			}
		case "set":
			return schemaPathError(binding, fmt.Sprintf("cannot traverse unordered set %s; bind the complete set leaf", cursor.label))
		case "map":
			return schemaPathError(binding, fmt.Sprintf("cannot traverse map %s; bind the complete map leaf", cursor.label))
		default:
			return schemaPathError(binding, fmt.Sprintf("cannot traverse %s %s", encoding.Kind, cursor.label))
		}
	case metadata.TerraformObjectType:
		name, isName := part.(string)
		if !isName {
			return schemaPathError(binding, fmt.Sprintf("indexes object %s as a list", cursor.label))
		}
		member, ok := encoding.Members[name]
		if !ok {
			return schemaPathError(binding, fmt.Sprintf("references unknown schema path %s.%s", cursor.label, name))
		}
		return bindingSchemaCursor{kind: cursorEncoding, encoding: member, label: cursor.label + "." + name}
	default:
		return schemaPathError(binding, fmt.Sprintf("traverses scalar %s", cursor.label))
	}
}

// blockCursor ports blockCursor from the original implementation.
func blockCursor(binding ExpressionBinding, cursor bindingSchemaCursor, part any) bindingSchemaCursor {
	name, isName := part.(string)
	if !isName {
		return schemaPathError(binding, fmt.Sprintf("indexes object %s as a list", cursor.label))
	}
	attributes, err := metadata.TerraformAttributesForBlock(cursor.block, cursor.label)
	if err != nil {
		bindingsFail("%s", err.Error())
	}
	if attributeValue, hasAttribute := attributes[name]; hasAttribute {
		attribute := requireTerraformObject(attributeValue, fmt.Sprintf("%s.attributes.%s", cursor.label, name))
		if !metadata.TerraformBooleanField(attribute, "required") && !metadata.TerraformBooleanField(attribute, "optional") {
			return schemaPathError(binding, fmt.Sprintf("targets computed-only attribute %s.%s", cursor.label, name))
		}
		encoding, err := metadata.TerraformAttributeType(attribute, fmt.Sprintf("%s.attributes.%s", cursor.label, name))
		if err != nil {
			bindingsFail("%s", err.Error())
		}
		return bindingSchemaCursor{kind: cursorEncoding, encoding: encoding, label: cursor.label + "." + name}
	}
	blockTypes, err := metadata.TerraformBlockTypesForBlock(cursor.block, cursor.label)
	if err != nil {
		bindingsFail("%s", err.Error())
	}
	blockTypeValue, hasBlockType := blockTypes[name]
	if !hasBlockType {
		return schemaPathError(binding, fmt.Sprintf("references unknown schema path %s.%s", cursor.label, name))
	}
	blockType := requireTerraformObject(blockTypeValue, fmt.Sprintf("%s.block_types.%s", cursor.label, name))
	child := requireTerraformObject(blockType["block"], fmt.Sprintf("%s.block_types.%s.block", cursor.label, name))
	label := cursor.label + "." + name
	if metadata.TerraformBlockIsSingle(blockType) {
		return bindingSchemaCursor{kind: cursorBlock, block: child, label: label}
	}
	nestingMode, _ := blockType["nesting_mode"].(string)
	switch nestingMode {
	case "list":
		return bindingSchemaCursor{kind: cursorBlockList, block: child, label: label}
	case "set":
		return bindingSchemaCursor{kind: cursorBlockSet, label: label}
	default:
		return schemaPathError(binding, fmt.Sprintf("cannot traverse %s block %s", jsStringifyNestingMode(blockType["nesting_mode"]), label))
	}
}

// jsStringifyNestingMode renders an arbitrary schema `nesting_mode` value
// the way the TS source's template-literal `${blockType.nesting_mode}`
// stringification would (JS's default String(value) coercion), used only in
// the "cannot traverse ... block" error text for a malformed/unsupported
// schema.
func jsStringifyNestingMode(value any) string {
	if s, ok := value.(string); ok {
		return s
	}
	if value == nil {
		return "undefined"
	}
	return fmt.Sprintf("%v", value)
}

// ValidateExpressionBindingSchemaPaths ports the exported
// validateExpressionBindingSchemaPaths from
// the original implementation: "Validate target paths against
// the provider schema, including native-HCL configs."
func ValidateExpressionBindingSchemaPaths(schema metadata.JsonObject, resourceType string, bindings []ExpressionBinding) (err error) {
	defer recoverBindingsError(&err)
	rootBlock, blockErr := metadata.TerraformBlockForSchema(schema, resourceType)
	if blockErr != nil {
		bindingsFail("%s", blockErr.Error())
	}
	for _, binding := range bindings {
		cursor := bindingSchemaCursor{kind: cursorBlock, block: rootBlock, label: resourceType}
		for _, part := range binding.PathParts {
			switch cursor.kind {
			case cursorBlock:
				cursor = blockCursor(binding, cursor, part)
			case cursorEncoding:
				cursor = encodingCursor(binding, cursor, part)
			case cursorBlockList:
				index, isIndex := part.(int)
				if !isIndex {
					schemaPathError(binding, fmt.Sprintf("traverses list block %s without an exact numeric selector", cursor.label))
				}
				cursor = bindingSchemaCursor{kind: cursorBlock, block: cursor.block, label: fmt.Sprintf("%s[%d]", cursor.label, index)}
			default: // cursorBlockSet
				schemaPathError(binding, fmt.Sprintf("cannot traverse unordered set block %s; bind the complete block leaf", cursor.label))
			}
		}
	}
	return nil
}

// renderExpressionHclValue ports the exported renderExpressionHclValue from
// the original implementation.
func renderExpressionHclValue(value any, indent int) string {
	switch v := value.(type) {
	case *HclExpression:
		return v.Expression
	case string:
		return pythonJSONString(v)
	case bool:
		if v {
			return "true"
		}
		return "false"
	case nil:
		return "null"
	case json.Number:
		token, err := canonjson.CanonicalNumberToken(string(v))
		if err != nil {
			bindingsFail("cannot render %s as HCL", string(v))
		}
		return token
	case float64:
		if v == float64(int64(v)) && v >= -jsMaxSafeIntegerFloat && v <= jsMaxSafeIntegerFloat {
			return fmt.Sprintf("%d", int64(v))
		}
		token, err := canonjson.FiniteFloatToken(v)
		if err != nil {
			bindingsFail("cannot render %v as HCL", v)
		}
		return token
	case []any:
		parts := make([]string, len(v))
		for i, item := range v {
			parts[i] = renderExpressionHclValue(item, indent)
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case map[string]any:
		pad := strings.Repeat(" ", indent)
		childPad := strings.Repeat(" ", indent+2)
		lines := []string{"{"}
		for _, key := range canonjson.SortedStrings(mapKeys(v)) {
			lines = append(lines, fmt.Sprintf("%s%s = %s", childPad, hclKey(key), renderExpressionHclValue(v[key], indent+2)))
		}
		lines = append(lines, pad+"}")
		return strings.Join(lines, "\n")
	default:
		bindingsFail("cannot render %v as HCL", value)
		return ""
	}
}

// jsMaxSafeIntegerFloat is jsMaxSafeInteger as a float64, for the plain
// `number` branch of renderExpressionHclValue/toTerraformJsonValue's
// Number.isSafeInteger checks.
const jsMaxSafeIntegerFloat = float64(jsMaxSafeInteger)

// RenderExpressionHclValue ports renderExpressionHclValue from
// the original implementation. indent mirrors the TS source's
// `indent = 0` default parameter; pass 0 for a top-level call.
func RenderExpressionHclValue(value any, indent int) (result string, err error) {
	defer recoverBindingsError(&err)
	return renderExpressionHclValue(value, indent), nil
}

// toTerraformJsonValue ports the exported toTerraformJsonValue from
// the original implementation.
func toTerraformJsonValue(value any) any {
	switch v := value.(type) {
	case *HclExpression:
		return "${" + v.Expression + "}"
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = toTerraformJsonValue(item)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(v))
		for _, key := range canonjson.SortedStrings(mapKeys(v)) {
			out[key] = toTerraformJsonValue(v[key])
		}
		return out
	default:
		return value
	}
}

// ToTerraformJsonValue ports toTerraformJsonValue from
// the original implementation.
func ToTerraformJsonValue(value any) any {
	return toTerraformJsonValue(value)
}

// bindingTreeValueKind distinguishes bindingTree's two child-value shapes:
// a leaf expression string, or a nested *bindingTree. Go analogue of the TS
// `type BindingTreeValue = string | BindingTree` union.
type bindingTreeValueKind int

const (
	bindingTreeLeaf bindingTreeValueKind = iota
	bindingTreeNode
)

// bindingTreeValue is the Go analogue of BindingTreeValue.
type bindingTreeValue struct {
	kind bindingTreeValueKind
	leaf string
	node *bindingTree
}

// bindingTreeKind is the Go analogue of BindingTree's `kind` field:
// "attributes" | "indices" | null.
type bindingTreeKind int

const (
	bindingTreeKindNone bindingTreeKind = iota
	bindingTreeKindAttributes
	bindingTreeKindIndices
)

// bindingTree is the Go analogue of the BindingTree interface in
// the original implementation. children is keyed by either a
// string (attribute name) or an int (list index), mirroring PathParts.
type bindingTree struct {
	kind     bindingTreeKind
	children map[any]bindingTreeValue
}

func emptyBindingTree() *bindingTree {
	return &bindingTree{children: map[any]bindingTreeValue{}}
}

// bindingTreeChild ports bindingTreeChild from
// the original implementation.
func bindingTreeChild(tree *bindingTree, part any, binding ExpressionBinding) *bindingTree {
	_, isIndex := part.(int)
	kind := bindingTreeKindAttributes
	if isIndex {
		kind = bindingTreeKindIndices
	}
	if tree.kind != bindingTreeKindNone && tree.kind != kind {
		bindingsFail("conflicting expression binding shape below %s.%s", binding.Address, binding.Path)
	}
	tree.kind = kind
	existing, has := tree.children[part]
	if !has {
		child := emptyBindingTree()
		tree.children[part] = bindingTreeValue{kind: bindingTreeNode, node: child}
		return child
	}
	if existing.kind == bindingTreeLeaf {
		bindingsFail("conflicting expression binding below %s.%s", binding.Address, binding.Path)
	}
	return existing.node
}

// bindingTreeForBindings ports the local `bindingTree` function from
// the original implementation, renamed to avoid colliding with
// this file's bindingTree type.
func bindingTreeForBindings(bindings []ExpressionBinding) map[string]*bindingTree {
	output := map[string]*bindingTree{}
	for _, binding := range bindings {
		current, ok := output[binding.Key]
		if !ok {
			current = emptyBindingTree()
			output[binding.Key] = current
		}
		for _, part := range binding.PathParts[:len(binding.PathParts)-1] {
			current = bindingTreeChild(current, part, binding)
		}
		if len(binding.PathParts) == 0 {
			bindingsFail("empty expression binding path for %s", binding.Address)
		}
		leaf := binding.PathParts[len(binding.PathParts)-1]
		_, isIndex := leaf.(int)
		kind := bindingTreeKindAttributes
		if isIndex {
			kind = bindingTreeKindIndices
		}
		if current.kind != bindingTreeKindNone && current.kind != kind {
			bindingsFail("conflicting expression binding shape below %s.%s", binding.Address, binding.Path)
		}
		current.kind = kind
		if _, exists := current.children[leaf]; exists {
			bindingsFail("conflicting expression binding below %s.%s", binding.Address, binding.Path)
		}
		current.children[leaf] = bindingTreeValue{kind: bindingTreeLeaf, leaf: binding.Expression}
	}
	return output
}

// renderMerge ports renderMerge from the original implementation.
func renderMerge(baseExpression string, tree *bindingTree, indent int) string {
	if tree.kind == bindingTreeKindIndices {
		return renderListEdits(baseExpression, tree, indent)
	}
	pad := strings.Repeat(" ", indent)
	innerPad := strings.Repeat(" ", indent+2)
	lines := []string{fmt.Sprintf("merge(%s, {", baseExpression)}
	var names []string
	for part := range tree.children {
		if name, ok := part.(string); ok {
			names = append(names, name)
		}
	}
	for _, name := range canonjson.SortedStrings(names) {
		value := tree.children[name]
		if value.kind == bindingTreeLeaf {
			lines = append(lines, fmt.Sprintf("%s%s = %s", innerPad, name, value.leaf))
			continue
		}
		childReference := baseExpression + "." + name
		childBase := childReference
		if value.node.kind != bindingTreeKindIndices {
			childBase = fmt.Sprintf("try(%s, null) == null ? {} : %s", childReference, childReference)
		}
		lines = append(lines, fmt.Sprintf("%s%s = %s", innerPad, name, renderMerge(childBase, value.node, indent+2)))
	}
	lines = append(lines, pad+"})")
	return strings.Join(lines, "\n")
}

// renderListEdits ports renderListEdits from
// the original implementation.
func renderListEdits(baseExpression string, tree *bindingTree, indent int) string {
	var indexes []int
	for part := range tree.children {
		if index, ok := part.(int); ok {
			indexes = append(indexes, index)
		}
	}
	sortInts(indexes)
	var segments []string
	start := 0
	for _, index := range indexes {
		value := tree.children[index]
		segments = append(segments, fmt.Sprintf("slice(%s, %d, %d)", baseExpression, start, index))
		var replacement string
		if value.kind == bindingTreeLeaf {
			replacement = value.leaf
		} else {
			replacement = renderMerge(fmt.Sprintf("%s[%d]", baseExpression, index), value.node, indent+2)
		}
		segments = append(segments, "["+replacement+"]")
		start = index + 1
	}
	segments = append(segments, fmt.Sprintf("slice(%s, %d, length(%s))", baseExpression, start, baseExpression))
	return "concat(" + strings.Join(segments, ", ") + ")"
}

func sortInts(values []int) {
	for i := 1; i < len(values); i++ {
		for j := i; j > 0 && values[j-1] > values[j]; j-- {
			values[j-1], values[j] = values[j], values[j-1]
		}
	}
}

// RenderExpressionBindingsHclOptions ports the options bag
// renderExpressionBindingsHcl accepts in
// the original implementation. An empty field means "use the TS
// source's default" (ItemsVariable defaults to "items", LocalName defaults
// to "infrawright_expression_bound_items"), matching the TS source's own
// `options?.itemsVariable ?? "items"` fallback -- a caller can never
// distinguish an explicit empty-string override from an omitted option
// there either, so this port makes the same simplification.
type RenderExpressionBindingsHclOptions struct {
	ItemsVariable string
	LocalName     string
}

// renderExpressionBindingsHcl ports the exported renderExpressionBindingsHcl
// from the original implementation: "Render the exact root-layer
// HCL merge contract used by Python gen_env."
func renderExpressionBindingsHcl(bindings []ExpressionBinding, options RenderExpressionBindingsHclOptions) string {
	if len(bindings) == 0 {
		return ""
	}
	itemsVariable := options.ItemsVariable
	if itemsVariable == "" {
		itemsVariable = "items"
	}
	localName := options.LocalName
	if localName == "" {
		localName = "infrawright_expression_bound_items"
	}
	if !pathSegmentPattern.MatchString(itemsVariable) {
		bindingsFail("items_var must be a Terraform identifier")
	}
	if !pathSegmentPattern.MatchString(localName) {
		bindingsFail("local_name must be a Terraform identifier")
	}
	sections := []string{
		"# GENERATED by engine.gen_env from expression bindings — do not edit.",
		"# Regenerate: make gen-env TENANT=<tenant>",
		"",
	}
	variables := ExpressionVariables(bindings)
	varNames := make([]string, 0, len(variables))
	for name := range variables {
		varNames = append(varNames, name)
	}
	for _, name := range canonjson.SortedStrings(varNames) {
		sections = append(sections, fmt.Sprintf(`variable "%s" {`, name), "  type = string")
		if variables[name] {
			sections = append(sections, "  sensitive = true")
		}
		sections = append(sections, "}", "")
	}
	sections = append(sections, "locals {", fmt.Sprintf("  %s = merge(var.%s, {", localName, itemsVariable))
	trees := bindingTreeForBindings(bindings)
	treeKeys := make([]string, 0, len(trees))
	for key := range trees {
		treeKeys = append(treeKeys, key)
	}
	for _, key := range canonjson.SortedStrings(treeKeys) {
		tree := trees[key]
		rendered := strings.ReplaceAll(
			renderMerge(fmt.Sprintf("var.%s[%s]", itemsVariable, pythonJSONString(key)), tree, 4),
			"\n", "\n    ",
		)
		sections = append(sections, fmt.Sprintf("    %s = %s", pythonJSONString(key), rendered))
	}
	sections = append(sections, "  })", "}", "")
	return strings.Join(sections, "\n")
}

// RenderExpressionBindingsHcl ports renderExpressionBindingsHcl from
// the original implementation.
func RenderExpressionBindingsHcl(bindings []ExpressionBinding, options RenderExpressionBindingsHclOptions) (result string, err error) {
	defer recoverBindingsError(&err)
	return renderExpressionBindingsHcl(bindings, options), nil
}

// bindingIdentity builds the internal dedup/selection key
// mergeExpressionBindingLayers and its Node-source counterpart derive from
// `JSON.stringify([binding.key, binding.path])`. Both fields are already
// control-character-free validated strings (see parseBinding), so a
// NUL-separated composite is an equally injective, much cheaper substitute
// -- never itself observed outside this package.
func bindingIdentity(binding ExpressionBinding) string {
	return binding.Key + "\x00" + binding.Path
}

// MergeExpressionBindingLayers ports the exported
// mergeExpressionBindingLayers from
// the original implementation.
func MergeExpressionBindingLayers(layers [][]ExpressionBinding) []ExpressionBinding {
	selected := map[string]ExpressionBinding{}
	var order []string
	for _, layer := range layers {
		for _, binding := range layer {
			identity := bindingIdentity(binding)
			if _, exists := selected[identity]; !exists {
				order = append(order, identity)
			}
			selected[identity] = binding
		}
	}
	result := make([]ExpressionBinding, 0, len(order))
	for _, identity := range order {
		result = append(result, selected[identity])
	}
	sortBindingsByKeyThenPath(result)
	return result
}

func sortBindingsByKeyThenPath(bindings []ExpressionBinding) {
	for i := 1; i < len(bindings); i++ {
		for j := i; j > 0 && compareBindingKeyPath(bindings[j-1], bindings[j]) > 0; j-- {
			bindings[j-1], bindings[j] = bindings[j], bindings[j-1]
		}
	}
}

func compareBindingKeyPath(left, right ExpressionBinding) int {
	if c := canonjson.ComparePythonStrings(left.Key, right.Key); c != 0 {
		return c
	}
	return canonjson.ComparePythonStrings(left.Path, right.Path)
}

// ExpressionModuleTargets ports the exported expressionModuleTargets from
// the original implementation: "Return module names referenced
// outside quoted strings."
func ExpressionModuleTargets(expression string) []string {
	targets := map[string]bool{}
	index := 0
	inString := false
	escaped := false
	for index < len(expression) {
		character := expression[index]
		if inString {
			switch {
			case escaped:
				escaped = false
			case character == '\\':
				escaped = true
			case character == '"':
				inString = false
			}
			index++
			continue
		}
		if character == '"' {
			inString = true
			index++
			continue
		}
		if strings.HasPrefix(expression[index:], "module.") {
			start := index + len("module.")
			end := start
			for end < len(expression) && isIdentPartByte(expression[end]) {
				end++
			}
			if end > start {
				targets[expression[start:end]] = true
			}
			index = end
			continue
		}
		index++
	}
	names := make([]string, 0, len(targets))
	for name := range targets {
		names = append(names, name)
	}
	return canonjson.SortedStrings(names)
}

// RemoteStateReference is the Go analogue of the RemoteStateReference
// interface in the original implementation.
type RemoteStateReference struct {
	Key          string
	ResourceType string
	Root         string
}

var remoteStateSelectorPattern = regexp.MustCompile(
	`^data\.terraform_remote_state\.([A-Za-z_][A-Za-z0-9_]*)\.outputs\.infrawright_reference_ids\.([A-Za-z_][A-Za-z0-9_]*)\[`,
)

// expressionRemoteStateReferences ports the exported
// expressionRemoteStateReferences from
// the original implementation: "Return canonical Infrawright
// remote-state selectors outside quoted strings."
func expressionRemoteStateReferences(expression string) []RemoteStateReference {
	const prefix = "data.terraform_remote_state."
	selected := map[string]RemoteStateReference{}
	var order []string
	index := 0
	inString := false
	escaped := false
	for index < len(expression) {
		character := expression[index]
		if inString {
			switch {
			case escaped:
				escaped = false
			case character == '\\':
				escaped = true
			case character == '"':
				inString = false
			}
			index++
			continue
		}
		if character == '"' {
			inString = true
			index++
			continue
		}
		if !strings.HasPrefix(expression[index:], prefix) {
			index++
			continue
		}
		match := remoteStateSelectorPattern.FindStringSubmatchIndex(expression[index:])
		if match == nil {
			bindingsFail("Infrawright terraform_remote_state expressions must use the canonical infrawright_reference_ids resource/key selector")
		}
		root := expression[index+match[2] : index+match[3]]
		resourceType := expression[index+match[4] : index+match[5]]
		matchEnd := index + match[1]
		quoted, err := tfrender.ParseHclQuotedString(expression, matchEnd)
		if err != nil {
			bindingsFail("%s", err.Error())
		}
		if quoted.End >= len(expression) || expression[quoted.End] != ']' {
			bindingsFail("cross-state reference key must end with a closing bracket")
		}
		boundary := quoted.End + 1
		for boundary < len(expression) {
			r, size := decodeRuneAt(expression, boundary)
			if !isPythonWhitespaceRune(r) {
				break
			}
			boundary += size
		}
		if boundary < len(expression) {
			next := expression[boundary]
			if next != ',' && next != ']' {
				bindingsFail("Infrawright terraform_remote_state expressions must end after the canonical resource/key selector")
			}
		}
		reference := RemoteStateReference{Key: quoted.Value, ResourceType: resourceType, Root: root}
		identity := root + "\x00" + resourceType + "\x00" + quoted.Value
		if _, exists := selected[identity]; !exists {
			order = append(order, identity)
		}
		selected[identity] = reference
		index = quoted.End + 1
	}
	result := make([]RemoteStateReference, 0, len(order))
	for _, identity := range order {
		result = append(result, selected[identity])
	}
	sortRemoteStateReferences(result)
	return result
}

func sortRemoteStateReferences(references []RemoteStateReference) {
	for i := 1; i < len(references); i++ {
		for j := i; j > 0 && compareRemoteStateReference(references[j-1], references[j]) > 0; j-- {
			references[j-1], references[j] = references[j], references[j-1]
		}
	}
}

func compareRemoteStateReference(left, right RemoteStateReference) int {
	if c := canonjson.ComparePythonStrings(left.Root, right.Root); c != 0 {
		return c
	}
	if c := canonjson.ComparePythonStrings(left.ResourceType, right.ResourceType); c != 0 {
		return c
	}
	return canonjson.ComparePythonStrings(left.Key, right.Key)
}

// ExpressionRemoteStateReferences ports expressionRemoteStateReferences from
// the original implementation.
func ExpressionRemoteStateReferences(expression string) (references []RemoteStateReference, err error) {
	defer recoverBindingsError(&err)
	return expressionRemoteStateReferences(expression), nil
}
