package collectors

import "regexp"

// diagnostics.go ports the original implementation: masking
// tenant-identifying vanity/customer values out of diagnostic text before
// it is ever printed or embedded in an error message.

// vanityHost ports the regexp `/(^|[/.])([^/.]+)(\.zslogin[a-z0-9]*\.net)/gi`
// from the original implementation: a vanity-domain label
// immediately preceding a ".zslogin<suffix>.net" host, anchored to either
// the start of the string or a preceding "/" or "." so it never matches a
// label embedded deeper inside an unrelated token.
var vanityHost = regexp.MustCompile(`(?i)(^|[/.])([^/.]+)(\.zslogin[a-z0-9]*\.net)`)

// customerSegment ports the regexp `/(\/customers\/)[^/?#]+/gi` from
// the original implementation: the path segment immediately
// following "/customers/".
var customerSegment = regexp.MustCompile(`(?i)(/customers/)[^/?#]+`)

// MaskCollectorIdentifiers ports maskCollectorIdentifiers from
// the original implementation: it removes tenant-identifying vanity
// and customer values from diagnostic text, replacing a vanity domain
// label with "<vanity>" and a customer-id path segment with
// "<customer-id>", leaving everything else (including the rest of the URL,
// e.g. a query string) untouched.
func MaskCollectorIdentifiers(value string) string {
	masked := vanityHost.ReplaceAllString(value, "${1}<vanity>${3}")
	return customerSegment.ReplaceAllString(masked, "${1}<customer-id>")
}
