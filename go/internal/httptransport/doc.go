// Package httptransport implements collectors.HttpTransport over net/http.
// It preserves the product-relevant wire behavior:
//
//   - explicit REQUESTS_CA_BUNDLE, then SSL_CERT_FILE, layered onto the
//     system trust pool, with a clear, actionable REST_CA_BUNDLE_FAILED
//     error on load failure (ca.go);
//   - standard proxy-from-env via net/http's stdlib ProxyFromEnvironment
//     (HTTP_PROXY/HTTPS_PROXY/NO_PROXY), not a hand-rolled parser;
//   - a bounded response body and a bounded, secret-safe redirect policy
//     (transport.go);
//   - a cookie jar, because legacy ZIA session authentication
//     (collectors/zscaler-adapters.go's acquireZiaLegacy) depends on the
//     transport persisting and replaying a Set-Cookie session token; see
//     transport.go's New doc comment for why net/http/cookiejar's default
//     (host-only) public-suffix policy is sufficient here;
//   - secret-safe diagnostics, reusing collectors.MaskCollectorIdentifiers
//     rather than reimplementing masking (errors.go).
//
// The package intentionally relies on standard-library URL parsing, cookie,
// proxy, TLS-root, redirect, and HTTP framing behavior rather than maintaining
// a second compatibility transport.
package httptransport
