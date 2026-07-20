package httptransport

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	"github.com/dvmrry/infrawright-dev/go/internal/collectors"
)

const (
	// DefaultTimeoutMs is the per-attempt request timeout applied when
	// neither Options.RequestTimeoutMs nor a request's own TimeoutMs is
	// set. It ports REST_HTTP_TIMEOUT_MS from the retired resthttp
	// package.
	DefaultTimeoutMs = 30_000
	// DefaultResponseLimitBytes bounds a single response body. It is a
	// DoS guard against an unbounded or compromised peer, not a Node
	// parity requirement -- see docs/go-runtime-v2.md §2.
	DefaultResponseLimitBytes = 64 * 1024 * 1024
	// DefaultMaxRedirects caps automatic redirect following.
	DefaultMaxRedirects = 10

	maxMaxRedirects       = 20
	maxTimeoutMs          = 10 * 60 * 1000
	maxResponseLimitBytes = 1024 * 1024 * 1024
	caBundleLimitBytes    = 4 * 1024 * 1024
)

// Options configures New. Every field is optional; the zero value selects
// the product defaults. RoundTripper and Sleep are test seams: production
// callers leave them nil.
type Options struct {
	IncludeCustomCA    *bool
	MaxRedirects       *int
	RequestTimeoutMs   *int
	ResponseLimitBytes *int

	// RoundTripper replaces the real net/http.Transport dial/TLS/proxy
	// stack. An injected RoundTripper must be safe for concurrent use,
	// matching collectors.HttpTransport's documented contract.
	RoundTripper http.RoundTripper
	// Sleep replaces the real time.Sleep used between 429 retry
	// attempts. Returning an error aborts the retry with
	// REST_HTTP_RETRY_CLOCK_FAILED.
	Sleep func(milliseconds float64) error
}

type transport struct {
	roundTripper  http.RoundTripper
	jar           http.CookieJar
	parent        context.Context
	timeoutMs     int
	responseLimit int64
	maxRedirects  int
	sleep         func(float64) error
	contextSleep  func(context.Context, float64) error
	defaultSleep  bool
	closed        atomic.Bool
}

var _ collectors.HttpTransport = (*transport)(nil)

func boundedOption(value *int, fallback, maximum int, label string) (int, error) {
	selected := fallback
	if value != nil {
		selected = *value
	}
	if selected <= 0 || selected > maximum {
		return 0, ordinaryValidationError(label)
	}
	return selected, nil
}

func defaultSleep(milliseconds float64) error {
	time.Sleep(time.Duration(milliseconds * float64(time.Millisecond)))
	return nil
}

func sleepWithContext(ctx context.Context, milliseconds float64) error {
	timer := time.NewTimer(time.Duration(milliseconds * float64(time.Millisecond)))
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// New builds a collectors.HttpTransport backed by net/http, satisfying
// the seam go/internal/collectors/types.go documents: the fetch engine
// (pagination, retry policy, masking, failure hints, the Zscaler product
// adapters) sits on this seam unchanged. New owns everything the retired
// resthttp package owned that is still a product requirement:
//
//   - TLS trust: the system pool plus an explicit REQUESTS_CA_BUNDLE/
//     SSL_CERT_FILE bundle (ca.go);
//   - proxy selection: net/http's stdlib ProxyFromEnvironment, which
//     reads HTTP_PROXY/HTTPS_PROXY/NO_PROXY (and their lowercase forms)
//     from the real process environment -- not the environment map this
//     function is handed, which (unlike the retired resthttp package)
//     is consulted only for the CA bundle. This is a deliberate,
//     documented behavior change: proxy configuration is no longer
//     injectable independently of the process environment, in exchange
//     for deleting ~250 lines of hand-rolled undici-proxy-parity URL
//     parsing (docs/go-runtime-v2.md §3's "allowed to change" column);
//   - a cookie jar. Legacy ZIA session authentication
//     (collectors/zscaler_adapters.go's acquireZiaLegacy) authenticates
//     once against https://zsapi.<cloud>.net/api/v1/authenticatedSession
//     and relies on the transport persisting and replaying the
//     resulting host-only Set-Cookie session token on every later
//     request -- see that function's "the injected transport owns and
//     persists the authenticated session cookie" comment. Every legacy
//     ZIA/ZPA base URL used for both auth and data calls is a single
//     fixed host (zscaler_adapters.go's ziaLegacyBase/zpaLegacyBase), so
//     this never needs cross-subdomain cookie sharing; a plain
//     cookiejar.New(nil) -- whose nil PublicSuffixList makes every
//     domain its own public suffix, i.e. host-only cookies only -- is
//     therefore sufficient, and needed no golang.org/x/net/publicsuffix
//     dependency or a hand-ported public-suffix trie to prove it.
//
// requestOnce (below) drives the returned RoundTripper and cookie jar
// directly rather than wrapping them in an *http.Client: net/http's own
// Client.Do pre-parses a redirect response's Location header itself,
// before ever consulting a CheckRedirect hook, so a malformed Location
// surfaces as a generic *url.Error rather than this package's own
// INVALID_REST_HTTP_RESPONSE classification. Calling RoundTrip directly
// (and replicating Client's jar-cookie attach/store around it) keeps
// every redirect decision -- and its error shape -- under this package's
// control.
func New(environment collectors.Environment, options Options) (collectors.HttpTransport, error) {
	return NewContext(context.Background(), environment, options)
}

// NewContext builds a collectors.HttpTransport whose requests and production
// retry waits are cancelled when parent is cancelled. The legacy New entry
// point remains detached from caller cancellation by using context.Background.
func NewContext(parent context.Context, environment collectors.Environment, options Options) (collectors.HttpTransport, error) {
	if parent == nil {
		parent = context.Background()
	}
	timeoutMs, err := boundedOption(options.RequestTimeoutMs, DefaultTimeoutMs, maxTimeoutMs, "request timeout")
	if err != nil {
		return nil, err
	}
	responseLimit, err := boundedOption(options.ResponseLimitBytes, DefaultResponseLimitBytes, maxResponseLimitBytes, "response limit")
	if err != nil {
		return nil, err
	}
	maxRedirects := DefaultMaxRedirects
	if options.MaxRedirects != nil {
		maxRedirects = *options.MaxRedirects
	}
	if maxRedirects < 0 || maxRedirects > maxMaxRedirects {
		return nil, errors.New("max redirects must be between 0 and 20")
	}
	includeCustomCA := options.IncludeCustomCA == nil || *options.IncludeCustomCA
	roots, err := trustedCertificates(environment, includeCustomCA)
	if err != nil {
		return nil, err
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, err
	}
	roundTripper := options.RoundTripper
	if roundTripper == nil {
		roundTripper = &http.Transport{
			Proxy: http.ProxyFromEnvironment,
			DialContext: (&net.Dialer{
				Timeout:   10 * time.Second,
				KeepAlive: 30 * time.Second,
			}).DialContext,
			TLSClientConfig: &tls.Config{
				MinVersion: tls.VersionTLS12,
				RootCAs:    roots,
			},
			// The product never negotiated transparent compression; keep
			// response bytes exactly what the peer sent so the
			// declared-Content-Length fast path in readBoundedBody stays
			// meaningful.
			DisableCompression:  true,
			MaxIdleConnsPerHost: 8,
			IdleConnTimeout:     90 * time.Second,
		}
	}
	sleep := options.Sleep
	usesDefaultSleep := sleep == nil
	if usesDefaultSleep {
		sleep = defaultSleep
	}
	return &transport{
		roundTripper:  roundTripper,
		jar:           jar,
		parent:        parent,
		timeoutMs:     timeoutMs,
		responseLimit: int64(responseLimit),
		maxRedirects:  maxRedirects,
		sleep:         sleep,
		contextSleep:  sleepWithContext,
		defaultSleep:  usesDefaultSleep,
	}, nil
}

func cloneStringMap(input map[string]string) map[string]string {
	output := make(map[string]string, len(input))
	for key, value := range input {
		output[key] = value
	}
	return output
}

func stripHeaders(input map[string]string, names ...string) map[string]string {
	dropped := make(map[string]struct{}, len(names))
	for _, name := range names {
		dropped[strings.ToLower(name)] = struct{}{}
	}
	output := make(map[string]string, len(input))
	for name, value := range input {
		if _, remove := dropped[strings.ToLower(name)]; !remove {
			output[name] = value
		}
	}
	return output
}

func sameOrigin(left, right *url.URL) bool {
	if left == nil || right == nil || !strings.EqualFold(left.Scheme, right.Scheme) {
		return false
	}
	if !strings.EqualFold(left.Hostname(), right.Hostname()) {
		return false
	}
	port := func(input *url.URL) string {
		if explicit := input.Port(); explicit != "" {
			return explicit
		}
		if strings.EqualFold(input.Scheme, "http") {
			return "80"
		}
		if strings.EqualFold(input.Scheme, "https") {
			return "443"
		}
		return ""
	}
	return port(left) == port(right)
}

// validateUniqueHeaders rejects a caller-supplied header map that
// collides only by case (e.g. both "Content-Type" and "content-type" set
// at once): collectors.HTTPRequest.Headers is a plain Go map, so such a
// collision would otherwise silently pick one value depending on map
// iteration order once translated into an http.Header.
func validateUniqueHeaders(headers map[string]string) error {
	seen := make(map[string]string, len(headers))
	for name := range headers {
		lower := strings.ToLower(name)
		if _, exists := seen[lower]; exists {
			return errors.New("ambiguous duplicate HTTP request header names")
		}
		seen[lower] = name
	}
	return nil
}

func (t *transport) buildRequest(
	ctx context.Context,
	target *url.URL,
	method string,
	body []byte,
	headers map[string]string,
) (*http.Request, error) {
	var reader *bytes.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	} else {
		reader = bytes.NewReader(nil)
	}
	request, err := http.NewRequestWithContext(ctx, method, target.String(), reader)
	if err != nil {
		return nil, err
	}
	if body == nil {
		request.Body = nil
		request.GetBody = nil
		request.ContentLength = 0
	}
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	if !hasHeader(headers, "user-agent") {
		// A present-but-empty header value tells net/http's request
		// writer to omit the User-Agent line entirely rather than fall
		// back to its default "Go-http-client/x.y" -- matching the
		// retired resthttp transport, and every product API this
		// collector calls, none of which need (or want) a client
		// identity string echoed to a Zscaler endpoint.
		request.Header["User-Agent"] = []string{""}
	}
	return request, nil
}

// requestOnce ports resthttp's requestOnce: one logical request, followed
// through a bounded chain of redirects. Its 429 budget is applied by the
// caller (Request, below), which wraps the complete chain -- a fresh
// requestOnce call per attempt, exactly like the retired transport.
func (t *transport) requestOnce(input collectors.HTTPRequest) (collectors.HTTPResponse, error) {
	if err := t.parent.Err(); err != nil {
		return collectors.HTTPResponse{}, err
	}
	if input.URL == nil {
		return collectors.HTTPResponse{}, connectionFailure(nil, errors.New("request URL is nil"))
	}
	if err := validateUniqueHeaders(input.Headers); err != nil {
		return collectors.HTTPResponse{}, connectionFailure(input.URL, err)
	}
	timeoutMs := t.timeoutMs
	if input.TimeoutMs != 0 {
		timeoutMs = input.TimeoutMs
	}
	if timeoutMs <= 0 || timeoutMs > maxTimeoutMs {
		return collectors.HTTPResponse{}, ordinaryValidationError("request timeout")
	}

	target := input.URL
	method := input.Method
	var body []byte
	if input.Body != nil {
		body = append([]byte(nil), input.Body...)
	}
	baseHeaders := cloneStringMap(input.Headers)

	for redirect := 0; ; redirect++ {
		if err := t.parent.Err(); err != nil {
			return collectors.HTTPResponse{}, err
		}
		if redirect > t.maxRedirects {
			return collectors.HTTPResponse{}, ioFailure(
				"REST_HTTP_REDIRECT_LIMIT",
				fmt.Sprintf("too many redirects while requesting %s", requestLocation(input.URL)),
			)
		}

		ctx, cancel := context.WithTimeout(t.parent, time.Duration(timeoutMs)*time.Millisecond)
		request, buildErr := t.buildRequest(ctx, target, method, body, baseHeaders)
		if buildErr != nil {
			cancel()
			return collectors.HTTPResponse{}, connectionFailure(target, buildErr)
		}
		if !hasHeader(baseHeaders, "cookie") {
			for _, cookie := range t.jar.Cookies(target) {
				request.AddCookie(cookie)
			}
		}
		response, doErr := t.roundTripper.RoundTrip(request)
		if doErr != nil {
			cancel()
			if parentErr := t.parent.Err(); parentErr != nil {
				return collectors.HTTPResponse{}, parentErr
			}
			return collectors.HTTPResponse{}, connectionFailure(target, doErr)
		}
		if parentErr := t.parent.Err(); parentErr != nil {
			closeBody(response.Body)
			cancel()
			return collectors.HTTPResponse{}, parentErr
		}
		if cookies := response.Cookies(); len(cookies) != 0 {
			t.jar.SetCookies(target, cookies)
		}
		status := response.StatusCode
		if status < 100 || status > 599 {
			closeBody(response.Body)
			cancel()
			return collectors.HTTPResponse{}, ioFailure("INVALID_REST_HTTP_RESPONSE", "HTTP response status is invalid")
		}

		if !redirectStatus(status) {
			responseBody, bodyErr := readBoundedBody(response, t.responseLimit)
			cancel()
			if parentErr := t.parent.Err(); parentErr != nil {
				return collectors.HTTPResponse{}, parentErr
			}
			if bodyErr != nil {
				return collectors.HTTPResponse{}, bodyErr
			}
			return collectors.HTTPResponse{
				Status:  status,
				Headers: normalizedResponseHeaders(response.Header),
				Body:    responseBody,
			}, nil
		}

		location, hasLocation := firstHeaderValue(response.Header, "Location")
		closeBody(response.Body)
		cancel()
		if parentErr := t.parent.Err(); parentErr != nil {
			return collectors.HTTPResponse{}, parentErr
		}
		if (status == http.StatusTemporaryRedirect || status == http.StatusPermanentRedirect) && len(body) != 0 {
			return collectors.HTTPResponse{}, ioFailure(
				"REST_HTTP_REDIRECT_REFUSED",
				"redirect response would replay a POST request body",
			)
		}
		if !hasLocation {
			return collectors.HTTPResponse{}, ioFailure("INVALID_REST_HTTP_RESPONSE", "redirect response is missing a location header")
		}
		locationURL, parseErr := url.Parse(location)
		if parseErr != nil {
			return collectors.HTTPResponse{}, ioFailure("INVALID_REST_HTTP_RESPONSE", "redirect response has an invalid location header")
		}
		next := target.ResolveReference(locationURL)
		if next.Scheme != "https" && next.Scheme != "http" {
			return collectors.HTTPResponse{}, ioFailure("REST_HTTP_REDIRECT_REFUSED", "redirect response selected an unsupported URL scheme")
		}
		if next.Host == "" {
			return collectors.HTTPResponse{}, ioFailure("INVALID_REST_HTTP_RESPONSE", "redirect response has an invalid location header")
		}
		if !sameOrigin(next, target) {
			// A cross-origin hop must not carry a bearer token or a
			// manually supplied Cookie header to a host the caller never
			// asked to see them. Jar-managed cookies (see New's doc
			// comment) are unaffected: they are already scoped per-host
			// by net/http/cookiejar and are looked up fresh for next's
			// host on the following iteration's RoundTrip call.
			baseHeaders = stripHeaders(baseHeaders, "authorization", "cookie")
		}
		body = nil
		baseHeaders = stripHeaders(baseHeaders, "content-type", "content-length")
		if status == http.StatusSeeOther ||
			((status == http.StatusMovedPermanently || status == http.StatusFound) && method == http.MethodPost) {
			method = http.MethodGet
		}
		target = next
	}
}

// Request implements collectors.HttpTransport. It retries a 429 response
// up to collectors.CollectorMaxRetries times, sleeping
// collectors.RetryDelayMs between attempts -- the same bounded schedule
// the retired resthttp transport applied, using the same collectors-owned
// policy functions (see collectors/retry.go's doc comment: those
// functions exist to be consumed by the transport, not by the fetch
// engine's own loop in rest.go, which has no retry logic of its own).
func (t *transport) Request(request collectors.HTTPRequest) (collectors.HTTPResponse, error) {
	maximumRetries := collectors.CollectorMaxRetries()
	for attempt := 0; attempt <= maximumRetries; attempt++ {
		if err := t.parent.Err(); err != nil {
			return collectors.HTTPResponse{}, err
		}
		if t.closed.Load() {
			return collectors.HTTPResponse{}, ioFailure("REST_HTTP_CLOSED", "HTTP transport is already closed")
		}
		response, err := t.requestOnce(request)
		if err != nil {
			return collectors.HTTPResponse{}, err
		}
		if response.Status != http.StatusTooManyRequests || attempt == maximumRetries {
			return response, nil
		}
		retryAfter, _ := firstMapValue(response.Headers, "retry-after")
		delay := collectors.RetryDelayMs(attempt, retryAfter)
		var sleepErr error
		if t.defaultSleep {
			sleepErr = t.contextSleep(t.parent, delay)
		} else {
			sleepErr = t.sleep(delay)
		}
		if parentErr := t.parent.Err(); parentErr != nil {
			return collectors.HTTPResponse{}, parentErr
		}
		if sleepErr != nil {
			return collectors.HTTPResponse{}, ioFailure("REST_HTTP_RETRY_CLOCK_FAILED", "HTTP retry clock failed", true)
		}
	}
	return collectors.HTTPResponse{}, ioFailure("REST_HTTP_INTERNAL", "HTTP retry state is unreachable")
}

// Close implements collectors.HttpTransport. It is idempotent and marks
// the transport closed so any later Request -- including one resuming
// from a 429 retry sleep that was in progress when Close was called --
// fails fast with REST_HTTP_CLOSED rather than continuing to do network
// work after the owner asked to shut down. It does not block waiting for
// in-flight requests: net/http.Transport.CloseIdleConnections only
// releases idle (unused) connections, never one an in-flight RoundTrip is
// still using, so there is no forced-abort hazard the retired resthttp
// transport's admit/drain bookkeeping existed to guard against.
func (t *transport) Close() error {
	if !t.closed.CompareAndSwap(false, true) {
		return nil
	}
	if closer, ok := t.roundTripper.(interface{ CloseIdleConnections() }); ok {
		closer.CloseIdleConnections()
	}
	return nil
}
