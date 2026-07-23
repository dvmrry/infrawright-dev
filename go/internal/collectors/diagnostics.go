package collectors

import "regexp"

// diagnostics.go ports node-src/collectors/diagnostics.ts: masking
// tenant-identifying vanity/customer values out of diagnostic text before
// it is ever printed or embedded in an error message.

// vanityHost ports the regexp `/(^|[/.])([^/.]+)(\.zslogin[a-z0-9]*\.net)/gi`
// from node-src/collectors/diagnostics.ts: a vanity-domain label
// immediately preceding a ".zslogin<suffix>.net" host, anchored to either
// the start of the string or a preceding "/" or "." so it never matches a
// label embedded deeper inside an unrelated token.
var vanityHost = regexp.MustCompile(`(?i)(^|[/.])([^/.]+)(\.zslogin[a-z0-9]*\.net)`)

// customerSegment ports the regexp `/(\/customers\/)[^/?#]+/gi` from
// node-src/collectors/diagnostics.ts: the path segment immediately
// following "/customers/".
var customerSegment = regexp.MustCompile(`(?i)(/customers/)[^/?#]+`)

// MaskCollectorIdentifiers ports maskCollectorIdentifiers from
// node-src/collectors/diagnostics.ts: it removes tenant-identifying vanity
// and customer values from diagnostic text, replacing a vanity domain
// label with "<vanity>" and a customer-id path segment with
// "<customer-id>", leaving everything else (including the rest of the URL,
// e.g. a query string) untouched.
func MaskCollectorIdentifiers(value string) string {
	masked := vanityHost.ReplaceAllString(value, "${1}<vanity>${3}")
	return customerSegment.ReplaceAllString(masked, "${1}<customer-id>")
}
