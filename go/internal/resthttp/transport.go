package resthttp

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/dvmrry/infrawright-dev/go/internal/collectors"
)

type transport struct {
	roundTripper  http.RoundTripper
	proxySelector *proxySelector
	production    bool
	jar           *toughCookieJar
	timeoutMs     int
	responseLimit int64
	maxRedirects  int
	performance   PerformanceRecorder
	sleep         func(float64) error
	closeFn       func() error
	destroyFn     func() error

	mu      sync.Mutex
	drained *sync.Cond
	active  int
	closing bool
}

var _ collectors.HttpTransport = (*transport)(nil)

func configuredPositive(value *int, fallback, maximum int, label string) (int, error) {
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

// CreateRestHTTPTransport ports createRestHttpTransport from
// node-src/io/rest-http-transport.ts and returns a concurrent-use-safe value
// satisfying collectors.HttpTransport.
func CreateRestHTTPTransport(
	environment collectors.Environment,
	options RestHTTPTransportOptions,
) (collectors.HttpTransport, error) {
	timeoutMs, err := configuredPositive(
		options.RequestTimeoutMs,
		RESTHTTPTimeoutMs,
		maximumRequestTimeoutMs,
		"request timeout",
	)
	if err != nil {
		return nil, err
	}
	responseLimit, err := configuredPositive(
		options.ResponseLimitBytes,
		RESTHTTPResponseLimitBytes,
		maximumResponseLimitByte,
		"response limit",
	)
	if err != nil {
		return nil, err
	}
	maxRedirects := defaultMaxRedirects
	if options.MaxRedirects != nil {
		maxRedirects = *options.MaxRedirects
	}
	if maxRedirects < 0 || maxRedirects > maximumMaxRedirects {
		return nil, errors.New("max redirects must be between 0 and 20")
	}
	includeCustomCA := options.IncludeCustomCA == nil || *options.IncludeCustomCA
	roots, err := trustedCertificates(environment, includeCustomCA)
	if err != nil {
		return nil, err
	}
	proxySnapshot, err := SnapshotRestProxyEnvironment(environment)
	if err != nil {
		return nil, err
	}
	selector, err := newProxySelector(proxySnapshot)
	if err != nil {
		return nil, err
	}
	tlsConfiguration := &tls.Config{
		MinVersion: tls.VersionTLS12,
		RootCAs:    roots,
		NextProtos: []string{"http/1.1"},
	}
	productionTransport := newProductionDispatcher(tlsConfiguration, selector)
	roundTripper := options.RoundTripper
	usesProductionTransport := roundTripper == nil
	if roundTripper == nil {
		roundTripper = productionTransport
	}
	closeFn := options.Close
	if closeFn == nil {
		if closer, ok := roundTripper.(interface{ CloseIdleConnections() }); ok {
			closeFn = func() error {
				closer.CloseIdleConnections()
				return nil
			}
		} else {
			closeFn = func() error { return nil }
		}
	}
	jar, err := newToughCookieJar()
	if err != nil {
		return nil, err
	}
	sleep := options.Sleep
	if sleep == nil {
		sleep = defaultSleep
	}
	created := &transport{
		roundTripper:  roundTripper,
		proxySelector: selector,
		production:    usesProductionTransport,
		jar:           jar,
		timeoutMs:     timeoutMs,
		responseLimit: int64(responseLimit),
		maxRedirects:  maxRedirects,
		performance:   options.Performance,
		sleep:         sleep,
		closeFn:       closeFn,
		destroyFn:     options.Destroy,
	}
	created.drained = sync.NewCond(&created.mu)
	return created, nil
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
	leftHost := strings.ToLower(left.Hostname())
	rightHost := strings.ToLower(right.Hostname())
	if leftHost != rightHost {
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

func cookieHeader(cookies []*http.Cookie) string {
	parts := make([]string, 0, len(cookies))
	for _, cookie := range cookies {
		parts = append(parts, toughCookieRequestPair(cookie))
	}
	return strings.Join(parts, "; ")
}

func (t *transport) admitLogicalRequest() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return !t.closing
}

func (t *transport) beginWireRequest() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closing {
		return false
	}
	t.active++
	return true
}

func (t *transport) endWireRequest() {
	t.mu.Lock()
	t.active--
	if t.active == 0 {
		t.drained.Broadcast()
	}
	t.mu.Unlock()
}

func validateUniqueStringHeaders(headers map[string]string) error {
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

func validateUniqueResponseHeaders(headers http.Header) error {
	seen := make(map[string]string, len(headers))
	for name := range headers {
		lower := strings.ToLower(name)
		if _, exists := seen[lower]; exists {
			return errors.New("ambiguous duplicate HTTP response header names")
		}
		seen[lower] = name
	}
	return nil
}

func contentLengthMatchesBody(value string, length int) bool {
	canonical := strings.TrimLeft(value, "0")
	if canonical == "" {
		canonical = "0"
	}
	return canonical == fmt.Sprintf("%d", length)
}

func configureProductionRequest(
	request *http.Request,
	target *url.URL,
	method string,
	body []byte,
	headers map[string]string,
) error {
	request.Host = target.Host
	for name, value := range headers {
		switch strings.ToLower(name) {
		case "host":
			request.Host = value
			// Preserve explicit presence separately from Request.Host so an
			// empty caller value does not fall back to the URL authority. The
			// production serializer consumes this entry instead of emitting it
			// through the generic header loop.
			request.Header[name] = []string{value}
		case "content-length":
			if value == "" || strings.IndexFunc(value, func(character rune) bool {
				return character < '0' || character > '9'
			}) >= 0 {
				return errors.New("invalid content-length header")
			}
			if len(body) != 0 && strings.EqualFold(method, http.MethodPost) &&
				!contentLengthMatchesBody(value, len(body)) {
				return errors.New("request body length does not match content-length header")
			}
		case "transfer-encoding", "keep-alive", "upgrade":
			return errors.New("invalid " + strings.ToLower(name) + " header")
		case "connection":
			for _, token := range strings.Split(value, ",") {
				trimmed := strings.Trim(token, " \t\u00a0")
				if !validHTTPToken(trimmed) {
					return errors.New("invalid connection header")
				}
				if strings.EqualFold(trimmed, "close") {
					request.Close = true
				}
			}
		case "expect":
			return errors.New("expect header not supported")
		default:
			request.Header[name] = []string{value}
		}
	}
	request.ContentLength = int64(len(body))
	return nil
}

func (t *transport) attemptStart(context *collectors.HTTPRequestPerformanceContext) *float64 {
	if context == nil || t.performance == nil {
		return nil
	}
	started := t.performance.Now()
	return &started
}

func (t *transport) recordAttempt(
	context *collectors.HTTPRequestPerformanceContext,
	started *float64,
	status *int,
) error {
	if context == nil || started == nil || t.performance == nil {
		return nil
	}
	contextCopy := *context
	var statusCopy *int
	if status != nil {
		value := *status
		statusCopy = &value
	}
	return t.performance.RecordHTTPAttempt(HTTPAttemptPerformance{
		Context:    contextCopy,
		DurationMs: t.performance.DurationSince(*started),
		Status:     statusCopy,
	})
}

func (t *transport) wireRequest(
	target *url.URL,
	method string,
	body []byte,
	headers map[string]string,
	timeoutMs int,
) (*http.Response, error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutMs)*time.Millisecond)
	var reader *bytes.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	} else {
		reader = bytes.NewReader(nil)
	}
	request, err := http.NewRequestWithContext(
		ctx,
		method,
		target.Scheme+"://resthttp.invalid/",
		reader,
	)
	if err != nil {
		cancel()
		return nil, err
	}
	wireURL := *target
	rawPath := rawWHATWGPath(target)
	if rawPath == "" {
		rawPath = "/"
	}
	if t.production {
		wireURL.Fragment = ""
		wireURL.RawFragment = ""
		// Every selected proxy route is CONNECT-tunneled, including plain
		// HTTP targets, so the inner request always uses origin-form.
		wireURL.Opaque = rawPath
	} else {
		// The injected seam observes an absolute URL, like the Node
		// httpRequest seam. Production always uses origin-form because every
		// selected proxy route is CONNECT-tunneled.
		authority := target.Host
		if target.User != nil {
			authority = target.User.String() + "@" + authority
		}
		wireURL.Opaque = "//" + authority + rawPath
		if target.Fragment != "" || target.RawFragment != "" {
			if target.ForceQuery || target.RawQuery != "" {
				wireURL.Opaque += "?" + target.RawQuery
				wireURL.ForceQuery = false
				wireURL.RawQuery = ""
			}
			fragment := target.RawFragment
			if fragment == "" {
				fragment = target.EscapedFragment()
			}
			wireURL.Opaque += "#" + fragment
			wireURL.Fragment = ""
			wireURL.RawFragment = ""
		}
	}
	request.URL = &wireURL
	request.Host = target.Host
	if body == nil {
		request.Body = nil
		request.GetBody = nil
		request.ContentLength = 0
	}
	if t.production {
		if err := configureProductionRequest(request, target, method, body, headers); err != nil {
			cancel()
			return nil, err
		}
	} else {
		for name, value := range headers {
			request.Header.Set(name, value)
		}
	}
	if !t.production && !hasHeader(headers, "user-agent") {
		request.Header["User-Agent"] = []string{""}
	}
	if !t.beginWireRequest() {
		cancel()
		return nil, errors.New("HTTP dispatcher is closed")
	}
	response, err := t.roundTripper.RoundTrip(request)
	if err != nil {
		t.endWireRequest()
		cancel()
		if response != nil {
			closeBody(response.Body)
		}
		return nil, err
	}
	if response == nil {
		t.endWireRequest()
		cancel()
		return nil, errors.New("HTTP round trip returned no response")
	}
	if response.Body == nil {
		t.endWireRequest()
		cancel()
		return response, nil
	}
	response.Body = &cancelOnCloseBody{
		ioReadCloser: response.Body,
		cancel:       cancel,
		finish:       t.endWireRequest,
	}
	return response, nil
}

type cancelOnCloseBody struct {
	ioReadCloser
	cancel context.CancelFunc
	finish func()
	once   sync.Once
}

type ioReadCloser interface {
	Read([]byte) (int, error)
	Close() error
}

func (b *cancelOnCloseBody) Close() error {
	err := b.ioReadCloser.Close()
	b.once.Do(func() {
		b.cancel()
		b.finish()
	})
	return err
}

func (t *transport) requestOnce(input collectors.HTTPRequest) (collectors.HTTPResponse, error) {
	if input.URL == nil {
		return collectors.HTTPResponse{}, connectionFailure(nil, errors.New("request URL is nil"))
	}
	target, _, targetErr := parseWHATWGURLReference(whatwgURLString(input.URL, true), nil)
	if targetErr != nil {
		return collectors.HTTPResponse{}, connectionFailure(input.URL, targetErr)
	}
	if err := validateUniqueStringHeaders(input.Headers); err != nil {
		return collectors.HTTPResponse{}, connectionFailure(target, err)
	}
	method := input.Method
	body := append([]byte(nil), input.Body...)
	if input.Body == nil {
		body = nil
	}
	baseHeaders := cloneStringMap(input.Headers)
	for redirect := 0; ; redirect++ {
		if redirect > t.maxRedirects {
			return collectors.HTTPResponse{}, ioFailure(
				"REST_HTTP_REDIRECT_LIMIT",
				fmt.Sprintf("too many redirects while requesting %s", requestLocation(input.URL)),
			)
		}
		headers := cloneStringMap(baseHeaders)
		cookieTarget := cookieContextURL(target)
		if !hasHeader(headers, "cookie") {
			if cookie := cookieHeader(t.jar.Cookies(cookieTarget)); cookie != "" {
				headers["cookie"] = cookie
			}
		}
		selectedTimeout := t.timeoutMs
		if input.TimeoutMs != 0 {
			selectedTimeout = input.TimeoutMs
		}
		if selectedTimeout <= 0 || selectedTimeout > maximumRequestTimeoutMs {
			return collectors.HTTPResponse{}, ordinaryValidationError("request timeout")
		}

		started := t.attemptStart(input.Performance)
		raw, requestErr := t.wireRequest(target, method, body, headers, selectedTimeout)
		if requestErr != nil {
			if recordErr := t.recordAttempt(input.Performance, started, nil); recordErr != nil {
				return collectors.HTTPResponse{}, recordErr
			}
			return collectors.HTTPResponse{}, connectionFailure(target, requestErr)
		}
		status := raw.StatusCode
		if status < 100 || status > 599 {
			closeBody(raw.Body)
			return collectors.HTTPResponse{}, ioFailure(
				"INVALID_REST_HTTP_RESPONSE",
				"HTTP response status is invalid",
			)
		}
		if err := validateUniqueResponseHeaders(raw.Header); err != nil {
			closeBody(raw.Body)
			return collectors.HTTPResponse{}, ioFailure(
				"INVALID_REST_HTTP_RESPONSE",
				"HTTP response headers are ambiguous",
			)
		}
		normalizedHeaders := normalizedResponseHeaders(raw.Header, t.production)
		t.jar.SetCookies(cookieTarget, acceptedResponseCookies(cookieTarget, raw.Header, t.production))
		if !redirectStatus(status) {
			responseBody, bodyErr := readBoundedBody(raw, t.responseLimit)
			recordErr := t.recordAttempt(input.Performance, started, &status)
			if recordErr != nil {
				return collectors.HTTPResponse{}, recordErr
			}
			if bodyErr != nil {
				return collectors.HTTPResponse{}, bodyErr
			}
			return collectors.HTTPResponse{
				Status:  status,
				Headers: normalizedHeaders,
				Body:    responseBody,
			}, nil
		}

		location, hasLocation := firstHeader(raw.Header, "location")
		closeBody(raw.Body)
		if recordErr := t.recordAttempt(input.Performance, started, &status); recordErr != nil {
			return collectors.HTTPResponse{}, recordErr
		}
		if (status == 307 || status == 308) && method == "POST" {
			return collectors.HTTPResponse{}, ioFailure(
				"REST_HTTP_REDIRECT_REFUSED",
				"redirect response would replay a POST request body",
			)
		}
		if !hasLocation {
			return collectors.HTTPResponse{}, ioFailure(
				"INVALID_REST_HTTP_RESPONSE",
				"redirect response is missing a location header",
			)
		}
		if t.production {
			location = latin1HeaderValue(location)
		}
		next, _, parseErr := parseWHATWGURLReference(location, target)
		if parseErr != nil {
			return collectors.HTTPResponse{}, ioFailure(
				"INVALID_REST_HTTP_RESPONSE",
				"redirect response has an invalid location header",
			)
		}
		if next.Scheme != "https" && next.Scheme != "http" {
			return collectors.HTTPResponse{}, ioFailure(
				"REST_HTTP_REDIRECT_REFUSED",
				"redirect response selected an unsupported URL scheme",
			)
		}
		if !sameOrigin(next, target) {
			baseHeaders = stripHeaders(baseHeaders, "authorization", "cookie")
		}
		body = nil
		baseHeaders = stripHeaders(baseHeaders, "content-type", "content-length")
		if status == 303 || ((status == 301 || status == 302) && method == "POST") {
			method = "GET"
		}
		target = next
	}
}

// Request implements collectors.HttpTransport. Its 429 budget is local to
// this call and wraps the complete redirect chain, matching the Node source's
// requestOnce placement. Logical retry sleeps and redirect-processing gaps are
// not dispatcher work: Close may finish while a previously admitted request is
// in either gap, and its next wire attempt then observes the closed dispatcher.
func (t *transport) Request(request collectors.HTTPRequest) (collectors.HTTPResponse, error) {
	if !t.admitLogicalRequest() {
		return collectors.HTTPResponse{}, ioFailure("REST_HTTP_CLOSED", "HTTP transport is already closed")
	}
	maximumRetries := collectors.CollectorMaxRetries()
	for attempt := 0; attempt <= maximumRetries; attempt++ {
		response, err := t.requestOnce(request)
		if err != nil {
			return collectors.HTTPResponse{}, err
		}
		if response.Status != http.StatusTooManyRequests || attempt == maximumRetries {
			return response, nil
		}
		retryAfter, _ := firstStringSlice(response.Headers, "retry-after")
		delay := collectors.RetryDelayMs(attempt, retryAfter)
		if err := t.sleep(delay); err != nil {
			return collectors.HTTPResponse{}, ioFailure(
				"REST_HTTP_RETRY_CLOCK_FAILED",
				"HTTP retry clock failed",
				true,
			)
		}
		if request.Performance != nil && t.performance != nil {
			if err := t.performance.RecordHTTPRetry(HTTPRetryPerformance{
				Context: *request.Performance,
				DelayMs: delay,
				Status:  response.Status,
			}); err != nil {
				return collectors.HTTPResponse{}, ioFailure(
					"REST_HTTP_RETRY_CLOCK_FAILED",
					"HTTP retry clock failed",
					true,
				)
			}
		}
	}
	return collectors.HTTPResponse{}, ioFailure("REST_HTTP_INTERNAL", "HTTP retry state is unreachable")
}

func firstStringSlice(headers map[string][]string, wanted string) (string, bool) {
	for name, values := range headers {
		if strings.EqualFold(name, wanted) && len(values) != 0 {
			return values[0], true
		}
	}
	return "", false
}

// Close implements collectors.HttpTransport. It marks the logical transport
// closed immediately, then waits only for active RoundTrip/response-body work.
// It is idempotent; a failed graceful close falls back to Destroy exactly once.
func (t *transport) Close() error {
	t.mu.Lock()
	if t.closing {
		t.mu.Unlock()
		return nil
	}
	t.closing = true
	for t.active != 0 {
		t.drained.Wait()
	}
	t.mu.Unlock()

	var closeErr error
	if err := t.closeFn(); err != nil {
		if t.destroyFn == nil || t.destroyFn() != nil {
			closeErr = ioFailure("REST_HTTP_CLEANUP_FAILED", "HTTP transport cleanup failed")
		}
	}
	return closeErr
}
