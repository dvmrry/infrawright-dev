package httptransport

import (
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
)

// canonicalContentLength matches a Content-Length value with no leading
// zero (RFC 9110's canonical form). A noncanonical declared length (e.g.
// "05") is ignored for the size-limit fast path below and falls back to
// the streaming bound, matching resthttp's original behavior.
var canonicalContentLength = regexp.MustCompile(`^(?:0|[1-9][0-9]*)$`)

func closeBody(body io.ReadCloser) {
	if body != nil {
		_ = body.Close()
	}
}

// firstHeaderValue returns wanted's first value from a wire http.Header
// (canonical MIME casing), and whether it was present at all -- Header.Get
// alone cannot distinguish "absent" from "present with an empty value".
func firstHeaderValue(headers http.Header, wanted string) (string, bool) {
	values := headers[http.CanonicalHeaderKey(wanted)]
	if len(values) == 0 {
		return "", false
	}
	return values[0], true
}

// firstMapValue is firstHeaderValue's counterpart for the already-
// lowercased map[string][]string shape collectors.HTTPResponse.Headers
// carries (see normalizedResponseHeaders).
func firstMapValue(headers map[string][]string, wanted string) (string, bool) {
	values, ok := headers[wanted]
	if !ok || len(values) == 0 {
		return "", false
	}
	return values[0], true
}

// normalizedResponseHeaders lowercases every header name and returns
// detached copies of the value slices, matching
// collectors.HTTPResponse.Headers's documented convention (types.go) and
// ensuring a caller can't observe later mutation of the underlying
// *http.Response.
func normalizedResponseHeaders(headers http.Header) map[string][]string {
	output := make(map[string][]string, len(headers))
	for name, values := range headers {
		output[strings.ToLower(name)] = append([]string(nil), values...)
	}
	return output
}

// readBoundedBody enforces the response-size DoS guard: a declared
// Content-Length above limit fails fast without reading the body: an
// undeclared or lying Content-Length is caught by the streaming
// io.LimitedReader bound (N+1 lets a body of exactly limit bytes succeed
// while anything larger overflows the check below). response.Body is
// always closed exactly once.
func readBoundedBody(response *http.Response, limit int64) ([]byte, error) {
	if response == nil || response.Body == nil {
		return nil, ioFailure("INVALID_REST_HTTP_RESPONSE", "HTTP response body is invalid")
	}
	if declared, ok := firstHeaderValue(response.Header, "Content-Length"); ok && canonicalContentLength.MatchString(declared) {
		length, err := strconv.ParseInt(declared, 10, 64)
		if err != nil || length > limit {
			closeBody(response.Body)
			return nil, ioFailure("REST_HTTP_RESPONSE_LIMIT", "HTTP response exceeded the collection size limit")
		}
	}
	reader := &io.LimitedReader{R: response.Body, N: limit + 1}
	body, err := io.ReadAll(reader)
	closeBody(response.Body)
	if err != nil {
		return nil, ioFailure("REST_HTTP_TRANSPORT_FAILED", "HTTP response body could not be read", true)
	}
	if int64(len(body)) > limit {
		return nil, ioFailure("REST_HTTP_RESPONSE_LIMIT", "HTTP response exceeded the collection size limit")
	}
	return body, nil
}

func redirectStatus(status int) bool {
	switch status {
	case http.StatusMovedPermanently, http.StatusFound, http.StatusSeeOther,
		http.StatusTemporaryRedirect, http.StatusPermanentRedirect:
		return true
	default:
		return false
	}
}
