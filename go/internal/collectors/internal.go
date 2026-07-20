package collectors

import "strings"

// internal.go holds small package-local helpers with no direct Node source
// counterpart: plain-Go plumbing (set construction, JSON-style string
// quoting for error messages) that the TS source gets for free from
// JS's Set/JSON.stringify and this package must spell out explicitly.

// toSet builds a membership set from values, for the same O(1)
// `new Set(array).has(x)` membership checks node-src/collectors/selection.ts
// and authority.ts build inline.
func toSet(values []string) map[string]struct{} {
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		set[value] = struct{}{}
	}
	return set
}

// setKeys returns set's members in unspecified order, for feeding into
// canonjson.SortedStrings (the Go analogue of the Node source's own
// `[...set]` spread immediately before `sortedStrings(...)`/`.sort()`).
func setKeys(set map[string]struct{}) []string {
	keys := make([]string, 0, len(set))
	for key := range set {
		keys = append(keys, key)
	}
	return keys
}

// jsonQuote renders value as JSON.stringify(value) would for a plain
// string: double-quoted, with the standard JSON control-character escapes.
// Every call site in this package quotes short, ASCII, non-control-
// character identifiers (resource types, product/provider names), so this
// intentionally does not reproduce JSON.stringify's \uXXXX escaping of
// arbitrary control characters or non-BMP astral pairs -- ported error
// messages never exercise that path -- matching the same-scoped jsonQuote
// helper in go/internal/metadata/driftpolicy.go.
func jsonQuote(value string) string {
	var sb strings.Builder
	sb.WriteByte('"')
	for _, r := range value {
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
			sb.WriteRune(r)
		}
	}
	sb.WriteByte('"')
	return sb.String()
}
