// Package httptransport implements collectors.HttpTransport
// (go/internal/collectors/types.go) over the Go standard library's
// net/http. It replaces the retired internal/resthttp package, which
// reimplemented undici/tough-cookie/WHATWG-URL/Node-cert-roots wire
// behavior in ~7,100 lines to stay byte-compatible with the Node CLI's
// HTTP client.
//
// the Go runtime contract §§2-3 re-scopes that compatibility contract around
// one test: would a difference change infrastructure, evidence,
// automation, or an operator decision? For the wire/IO layer, the answer
// is almost always no. This package therefore preserves only the
// product-relevant behavior:
//
//   - explicit REQUESTS_CA_BUNDLE, then SSL_CERT_FILE, layered onto the
//     system trust pool, with a clear, actionable REST_CA_BUNDLE_FAILED
//     error on load failure (ca.go);
//   - standard proxy-from-env via net/http's stdlib ProxyFromEnvironment
//     (HTTP_PROXY/HTTPS_PROXY/NO_PROXY), not a hand-rolled parser;
//   - a bounded response body (a DoS guard, not a Node parity concern)
//     and a bounded, secret-safe redirect policy (transport.go);
//   - a cookie jar, because legacy ZIA session authentication
//     (collectors/zscaler-adapters.go's acquireZiaLegacy) depends on the
//     transport persisting and replaying a Set-Cookie session token; see
//     transport.go's New doc comment for why net/http/cookiejar's default
//     (host-only) public-suffix policy is sufficient here;
//   - secret-safe diagnostics, reusing collectors.MaskCollectorIdentifiers
//     rather than reimplementing masking (errors.go).
//
// Deliberately NOT reproduced: WHATWG URL edge cases (net/url suffices),
// tough-cookie's cross-subdomain/public-suffix cookie sharing, Node's
// bundled root certificate inventory (the system trust store plus an
// explicit bundle is the product requirement), raw HTTP/1.1 wire framing,
// malformed-header/malformed-URL acceptance, Node-specific error wording,
// and the NODE_OPTIONS tokenizer.
package httptransport
