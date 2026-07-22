package httptransport

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dvmrry/infrawright-dev/go/internal/collectors"
	"github.com/dvmrry/infrawright-dev/go/internal/procerr"
)

func TestTransportPersistsScopedLegacySessionCookie(t *testing.T) {
	// Ports the retired resthttp package's cookie-session test: legacy ZIA
	// auth (collectors/zscaler_adapters.go's acquireZiaLegacy) POSTs to
	// /api/v1/authenticatedSession on a single fixed host and relies on the
	// transport replaying the resulting host-only session cookie on later
	// requests to that same host -- see New's doc comment.
	var mu sync.Mutex
	var observed []*http.Request
	transport, err := New(collectors.Environment{}, Options{
		RoundTripper: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			mu.Lock()
			observed = append(observed, request.Clone(request.Context()))
			index := len(observed)
			mu.Unlock()
			if index == 1 {
				headers := make(http.Header)
				headers.Add("Set-Cookie", "JSESSIONID=session-value; Path=/api; Secure; HttpOnly")
				headers.Add("Set-Cookie", "narrow=ignored; Path=/different; Secure")
				return response("{}", 200, headers), nil
			}
			return response("[]", 200, nil), nil
		}),
	})
	if err != nil {
		t.Fatalf("New() failed: %v", err)
	}
	t.Cleanup(func() { _ = transport.Close() })
	_, err = transport.Request(collectors.HTTPRequest{
		Method:  "POST",
		URL:     mustURL(t, "https://zsapi.example.test/api/v1/authenticatedSession"),
		Headers: map[string]string{"content-type": "application/json"},
		Body:    []byte("{}"),
	})
	if err != nil {
		t.Fatalf("legacy auth request failed: %v", err)
	}
	_, err = transport.Request(collectors.HTTPRequest{
		Method: "GET",
		URL:    mustURL(t, "https://zsapi.example.test/api/v1/urlCategories"),
	})
	if err != nil {
		t.Fatalf("data request failed: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if got := observed[1].Header.Get("Cookie"); got != "JSESSIONID=session-value" {
		t.Errorf("data request Cookie = %q, want %q", got, "JSESSIONID=session-value")
	}
}

func TestTransportDoesNotShareCookiesAcrossHosts(t *testing.T) {
	// A plain cookiejar.New(nil) treats every domain as its own public
	// suffix: a Set-Cookie without a Domain attribute is host-only and
	// never replayed to a different host, even a same-parent-domain one.
	// This is a deliberate narrowing from the retired resthttp package's
	// tough-cookie-based cross-subdomain sharing (see New's doc comment
	// for why the product never needed that).
	var observed []string
	transport, err := New(collectors.Environment{}, Options{
		RoundTripper: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			observed = append(observed, request.Header.Get("Cookie"))
			if len(observed) == 1 {
				return response("{}", 200, http.Header{"Set-Cookie": []string{"sid=value; Path=/; Secure"}}), nil
			}
			return response("{}", 200, nil), nil
		}),
	})
	if err != nil {
		t.Fatalf("New() failed: %v", err)
	}
	t.Cleanup(func() { _ = transport.Close() })
	for _, target := range []string{"https://auth.example.com/session", "https://api.example.com/data"} {
		if _, err := transport.Request(collectors.HTTPRequest{Method: "GET", URL: mustURL(t, target)}); err != nil {
			t.Fatalf("Request(%q) failed: %v", target, err)
		}
	}
	if observed[1] != "" {
		t.Errorf("cross-host Cookie = %q, want absent (host-only jar)", observed[1])
	}
}

func TestPOSTRedirectDropsBodyAndSensitiveHeadersOnCrossOriginDowngrade(t *testing.T) {
	type observation struct {
		method  string
		url     string
		headers http.Header
		body    string
	}
	var observed []observation
	transport, err := New(collectors.Environment{}, Options{
		RoundTripper: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			var body []byte
			if request.Body != nil {
				var readErr error
				body, readErr = io.ReadAll(request.Body)
				if readErr != nil {
					return nil, readErr
				}
			}
			observed = append(observed, observation{
				method: request.Method, url: request.URL.String(), headers: request.Header.Clone(), body: string(body),
			})
			if len(observed) == 1 {
				headers := make(http.Header)
				headers.Set("Location", "http://other.example.test/final")
				headers.Add("Set-Cookie", "sid=value; Path=/; Secure")
				return response("redirect", 302, headers), nil
			}
			return response("done", 200, nil), nil
		}),
	})
	if err != nil {
		t.Fatalf("New() failed: %v", err)
	}
	t.Cleanup(func() { _ = transport.Close() })
	result, err := transport.Request(collectors.HTTPRequest{
		Method: "POST",
		URL:    mustURL(t, "https://first.example.test/start"),
		Body:   []byte("secret-body"),
		Headers: map[string]string{
			"authorization": "Bearer secret",
			"content-type":  "application/x-www-form-urlencoded",
		},
	})
	if err != nil {
		t.Fatalf("redirected Request() failed: %v", err)
	}
	if string(result.Body) != "done" {
		t.Errorf("redirected body = %q, want %q", result.Body, "done")
	}
	if len(observed) != 2 {
		t.Fatalf("request count = %d, want 2", len(observed))
	}
	second := observed[1]
	if second.method != "GET" || second.body != "" {
		t.Errorf("redirected request = method %q body %q, want GET with empty body", second.method, second.body)
	}
	for _, name := range []string{"Authorization", "Cookie", "Content-Type", "Content-Length"} {
		if got := second.headers.Get(name); got != "" {
			t.Errorf("redirected %s = %q, want absent", name, got)
		}
	}
}

func TestPOST307And308RedirectsAreRefusedWithoutReplay(t *testing.T) {
	for _, status := range []int{307, 308} {
		t.Run(fmt.Sprintf("HTTP_%d", status), func(t *testing.T) {
			requests := 0
			transport, err := New(collectors.Environment{}, Options{
				RoundTripper: roundTripFunc(func(*http.Request) (*http.Response, error) {
					requests++
					headers := make(http.Header)
					headers.Set("Location", "https://auth.example.test/replay")
					return response("redirect", status, headers), nil
				}),
			})
			if err != nil {
				t.Fatalf("New() failed: %v", err)
			}
			t.Cleanup(func() { _ = transport.Close() })
			_, err = transport.Request(collectors.HTTPRequest{
				Method: "POST",
				URL:    mustURL(t, "https://auth.example.test/token"),
				Body:   []byte("client_secret=must-not-replay"),
			})
			requireProcessFailure(t, err, "REST_HTTP_REDIRECT_REFUSED")
			if requests != 1 {
				t.Errorf("HTTP %d request count = %d, want 1", status, requests)
			}
		})
	}
}

func TestRedirectFailurePriorityAndExactCodes(t *testing.T) {
	cases := []struct {
		name     string
		location []string
		code     string
	}{
		{name: "missing location", code: "INVALID_REST_HTTP_RESPONSE"},
		{name: "invalid location", location: []string{"http://[invalid"}, code: "INVALID_REST_HTTP_RESPONSE"},
		{name: "unsupported scheme", location: []string{"file:///secret"}, code: "REST_HTTP_REDIRECT_REFUSED"},
		// Deliberately not covered: "Location: //" (a network-path
		// reference with an empty authority). net/url.URL.ResolveReference
		// only recognizes a net-path reference when ref.Host != "", so an
		// empty-but-present authority silently falls back to inheriting
		// the base URL's host -- a WHATWG-URL-edge-case net/url does not
		// reproduce and no real API response exercises (see
		// the Go runtime contract §3's WHATWG-URL-edge-case drop).
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			transport, err := New(collectors.Environment{}, Options{
				RoundTripper: roundTripFunc(func(*http.Request) (*http.Response, error) {
					headers := make(http.Header)
					if tc.location != nil {
						headers["Location"] = tc.location
					}
					return response("redirect", 302, headers), nil
				}),
			})
			if err != nil {
				t.Fatalf("New() failed: %v", err)
			}
			t.Cleanup(func() { _ = transport.Close() })
			_, err = transport.Request(collectors.HTTPRequest{Method: "GET", URL: mustURL(t, "https://api.example.test/start")})
			requireProcessFailure(t, err, tc.code)
		})
	}
}

func TestRedirectCapRejectsAfterConfiguredLimit(t *testing.T) {
	requests := 0
	transport, err := New(collectors.Environment{}, Options{
		MaxRedirects: intPointer(0),
		RoundTripper: roundTripFunc(func(*http.Request) (*http.Response, error) {
			requests++
			return response("redirect", 302, http.Header{"Location": []string{"/next"}}), nil
		}),
	})
	if err != nil {
		t.Fatalf("New() failed: %v", err)
	}
	t.Cleanup(func() { _ = transport.Close() })
	_, err = transport.Request(collectors.HTTPRequest{Method: "GET", URL: mustURL(t, "https://tenant.zslogin.net/start?secret=yes")})
	failure := requireProcessFailure(t, err, "REST_HTTP_REDIRECT_LIMIT")
	if failure.Message != "too many redirects while requesting https://<vanity>.zslogin.net/start" {
		t.Errorf("redirect-limit message = %q", failure.Message)
	}
	if requests != 1 {
		t.Errorf("maxRedirects=0 request count = %d, want 1", requests)
	}
}

type testPerformanceWaits struct {
	mu    sync.Mutex
	waits []float64
}

func (w *testPerformanceWaits) record(milliseconds float64) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.waits = append(w.waits, milliseconds)
	return nil
}

func TestTransportRetries429UpToTheCollectorBudget(t *testing.T) {
	requests := 0
	waits := &testPerformanceWaits{}
	transport, err := New(collectors.Environment{}, Options{
		RoundTripper: roundTripFunc(func(*http.Request) (*http.Response, error) {
			requests++
			switch requests {
			case 1:
				return response("rate", 429, http.Header{"Retry-After": []string{"0.25"}}), nil
			case 2:
				return response("rate", 429, nil), nil
			default:
				return response("ok", 200, nil), nil
			}
		}),
		Sleep: waits.record,
	})
	if err != nil {
		t.Fatalf("New() failed: %v", err)
	}
	t.Cleanup(func() { _ = transport.Close() })
	result, err := transport.Request(collectors.HTTPRequest{
		Method: "POST",
		URL:    mustURL(t, "https://tenant.zslogin.net/oauth2/v1/token"),
		Body:   []byte("grant_type=client_credentials"),
	})
	if err != nil {
		t.Fatalf("Request() failed: %v", err)
	}
	if result.Status != 200 || requests != 3 {
		t.Errorf("Request() = status %d after %d requests, want 200 after 3", result.Status, requests)
	}
	if fmt.Sprint(waits.waits) != "[250 2000]" {
		t.Errorf("retry waits = %v, want [250 2000]", waits.waits)
	}
}

func TestFinal429ReturnsAfterSixAttemptsWithoutExtraSleep(t *testing.T) {
	attempts := 0
	waits := 0
	transport, err := New(collectors.Environment{}, Options{
		RoundTripper: roundTripFunc(func(*http.Request) (*http.Response, error) {
			attempts++
			return response("rate", 429, nil), nil
		}),
		Sleep: func(float64) error {
			waits++
			return nil
		},
	})
	if err != nil {
		t.Fatalf("New() failed: %v", err)
	}
	t.Cleanup(func() { _ = transport.Close() })
	result, err := transport.Request(collectors.HTTPRequest{Method: "GET", URL: mustURL(t, "https://api.example.test/")})
	if err != nil {
		t.Fatalf("Request() failed: %v", err)
	}
	if result.Status != 429 || attempts != 6 || waits != 5 {
		t.Errorf("final 429 = status %d attempts %d waits %d, want 429/6/5", result.Status, attempts, waits)
	}
}

func TestRetryClockFailureHasExactProcessFailure(t *testing.T) {
	transport, err := New(collectors.Environment{}, Options{
		RoundTripper: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return response("rate", 429, nil), nil
		}),
		Sleep: func(float64) error { return errors.New("clock secret") },
	})
	if err != nil {
		t.Fatalf("New() failed: %v", err)
	}
	t.Cleanup(func() { _ = transport.Close() })
	_, err = transport.Request(collectors.HTTPRequest{Method: "GET", URL: mustURL(t, "https://api.example.test/")})
	failure := requireProcessFailure(t, err, "REST_HTTP_RETRY_CLOCK_FAILED")
	if failure.Category != procerr.CategoryIO || !failure.Retryable || failure.Message != "HTTP retry clock failed" {
		t.Errorf("retry clock failure = %#v", failure)
	}
}

func TestResponseLimitClosesOversizedDeclaredBody(t *testing.T) {
	body := &trackingBody{reader: strings.NewReader("too large")}
	transport, err := New(collectors.Environment{}, Options{
		ResponseLimitBytes: intPointer(4),
		RoundTripper: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Header: http.Header{"Content-Length": []string{"9"}}, Body: body}, nil
		}),
	})
	if err != nil {
		t.Fatalf("New() failed: %v", err)
	}
	t.Cleanup(func() { _ = transport.Close() })
	_, err = transport.Request(collectors.HTTPRequest{Method: "GET", URL: mustURL(t, "https://api.example.test/data")})
	requireProcessFailure(t, err, "REST_HTTP_RESPONSE_LIMIT")
	if body.closed != 1 {
		t.Errorf("body close count = %d, want 1", body.closed)
	}
}

func TestResponseLimitCoversStreamingAndCanonicalContentLength(t *testing.T) {
	for _, tc := range []struct {
		name        string
		body        string
		contentLen  string
		wantFailure bool
	}{
		{name: "stream overflow", body: "12345", wantFailure: true},
		{name: "noncanonical declared length ignored", body: "1234", contentLen: "05", wantFailure: false},
		{name: "exact limit", body: "1234", contentLen: "4", wantFailure: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			transport, err := New(collectors.Environment{}, Options{
				ResponseLimitBytes: intPointer(4),
				RoundTripper: roundTripFunc(func(*http.Request) (*http.Response, error) {
					headers := make(http.Header)
					if tc.contentLen != "" {
						headers.Set("Content-Length", tc.contentLen)
					}
					return response(tc.body, 200, headers), nil
				}),
			})
			if err != nil {
				t.Fatalf("New() failed: %v", err)
			}
			t.Cleanup(func() { _ = transport.Close() })
			result, err := transport.Request(collectors.HTTPRequest{Method: "GET", URL: mustURL(t, "https://api.example.test/data")})
			if tc.wantFailure {
				requireProcessFailure(t, err, "REST_HTTP_RESPONSE_LIMIT")
				return
			}
			if err != nil {
				t.Fatalf("Request() failed: %v", err)
			}
			if string(result.Body) != tc.body {
				t.Errorf("Request().Body = %q, want %q", result.Body, tc.body)
			}
		})
	}
}

func TestResponseHeadersAreLowercaseDetachedCopies(t *testing.T) {
	headers := http.Header{"X-Trace": []string{"first", "second"}}
	transport, err := New(collectors.Environment{}, Options{
		RoundTripper: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return response("ok", 200, headers), nil
		}),
	})
	if err != nil {
		t.Fatalf("New() failed: %v", err)
	}
	t.Cleanup(func() { _ = transport.Close() })
	result, err := transport.Request(collectors.HTTPRequest{Method: "GET", URL: mustURL(t, "https://api.example.test/")})
	if err != nil {
		t.Fatalf("Request() failed: %v", err)
	}
	if fmt.Sprint(result.Headers["x-trace"]) != "[first second]" {
		t.Errorf("response x-trace = %v, want [first second]", result.Headers["x-trace"])
	}
	headers["X-Trace"][0] = "mutated"
	if result.Headers["x-trace"][0] != "first" {
		t.Errorf("response headers alias round-tripper headers: %v", result.Headers["x-trace"])
	}
}

func TestTransportFailureMasksIdentityAndNestedSecrets(t *testing.T) {
	secret := "nested-proxy-secret"
	transport, err := New(collectors.Environment{}, Options{
		RoundTripper: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return nil, errors.New(secret)
		}),
	})
	if err != nil {
		t.Fatalf("New() failed: %v", err)
	}
	t.Cleanup(func() { _ = transport.Close() })
	_, err = transport.Request(collectors.HTTPRequest{
		Method: "GET",
		URL: mustURL(t,
			"https://tenant.zslogin.net/zpa/customers/customer-secret/resource?token=secret"),
	})
	failure := requireProcessFailure(t, err, "REST_HTTP_TRANSPORT_FAILED")
	for _, forbidden := range []string{secret, "tenant.zslogin.net", "customer-secret", "?token="} {
		if strings.Contains(failure.Message, forbidden) {
			t.Errorf("failure message %q contains forbidden %q", failure.Message, forbidden)
		}
	}
	for _, required := range []string{"<vanity>.zslogin.net", "customers/<customer-id>", "(connection failure)"} {
		if !strings.Contains(failure.Message, required) {
			t.Errorf("failure message %q does not contain %q", failure.Message, required)
		}
	}
}

func TestFailureClassificationHintsAndRetryability(t *testing.T) {
	cases := []struct {
		name      string
		err       error
		kind      string
		hint      string
		retryable bool
	}{
		{"certificate", x509.HostnameError{Certificate: nil, Host: "tenant.zslogin.net"}, "certificate", "corporate TLS inspection?", false},
		{"timeout", &net.OpError{Op: "dial", Err: timeoutError{}}, "timeout", "request timed out;", true},
		{"connection", &net.OpError{Op: "dial", Err: errors.New("connection refused")}, "connection", "check HTTPS_PROXY/NO_PROXY, DNS", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			failure := connectionFailure(mustURL(t, "https://tenant.zslogin.net/path?secret=yes"), tc.err)
			wantPrefix := "cannot reach https://<vanity>.zslogin.net/path (" + tc.kind + " failure)\nhint: "
			if !strings.HasPrefix(failure.Message, wantPrefix) || !strings.Contains(failure.Message, tc.hint) {
				t.Errorf("connectionFailure() message = %q, want prefix %q and hint %q", failure.Message, wantPrefix, tc.hint)
			}
			if failure.Retryable != tc.retryable {
				t.Errorf("connectionFailure().Retryable = %t, want %t", failure.Retryable, tc.retryable)
			}
		})
	}
}

type timeoutError struct{}

func (timeoutError) Error() string   { return "i/o timeout" }
func (timeoutError) Timeout() bool   { return true }
func (timeoutError) Temporary() bool { return true }

func TestNativeTLSProtocolFailureIsCertificateAndNonRetryable(t *testing.T) {
	transport, err := New(collectors.Environment{}, Options{
		RoundTripper: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return nil, tls.RecordHeaderError{Msg: "first record does not look like a TLS handshake"}
		}),
	})
	if err != nil {
		t.Fatalf("New() failed: %v", err)
	}
	t.Cleanup(func() { _ = transport.Close() })
	_, err = transport.Request(collectors.HTTPRequest{Method: "GET", URL: mustURL(t, "https://api.example.test/")})
	failure := requireProcessFailure(t, err, "REST_HTTP_TRANSPORT_FAILED")
	if failure.Retryable || !strings.Contains(failure.Message, "(certificate failure)") {
		t.Errorf("native TLS failure = %#v, want non-retryable certificate failure", failure)
	}
}

func TestOptionBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name    string
		options Options
		message string
	}{
		{"zero timeout", Options{RequestTimeoutMs: intPointer(0)}, "request timeout must be a positive bounded integer"},
		{"large timeout", Options{RequestTimeoutMs: intPointer(maxTimeoutMs + 1)}, "request timeout must be a positive bounded integer"},
		{"zero response", Options{ResponseLimitBytes: intPointer(0)}, "response limit must be a positive bounded integer"},
		{"large response", Options{ResponseLimitBytes: intPointer(maxResponseLimitBytes + 1)}, "response limit must be a positive bounded integer"},
		{"negative redirects", Options{MaxRedirects: intPointer(-1)}, "max redirects must be between 0 and 20"},
		{"large redirects", Options{MaxRedirects: intPointer(21)}, "max redirects must be between 0 and 20"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := New(collectors.Environment{}, tc.options)
			if err == nil || err.Error() != tc.message {
				t.Errorf("New() error = %v, want %q", err, tc.message)
			}
		})
	}
}

func TestBodyReadFailureUsesFixedRetryableMessage(t *testing.T) {
	body := &trackingBody{reader: &failingReader{}}
	transport, err := New(collectors.Environment{}, Options{
		RoundTripper: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: body}, nil
		}),
	})
	if err != nil {
		t.Fatalf("New() failed: %v", err)
	}
	t.Cleanup(func() { _ = transport.Close() })
	_, err = transport.Request(collectors.HTTPRequest{Method: "GET", URL: mustURL(t, "https://api.example.test/data")})
	failure := requireProcessFailure(t, err, "REST_HTTP_TRANSPORT_FAILED")
	if failure.Message != "HTTP response body could not be read" || !failure.Retryable {
		t.Errorf("body failure = message %q retryable %t", failure.Message, failure.Retryable)
	}
}

type failingReader struct {
	read bool
}

func (r *failingReader) Read(buffer []byte) (int, error) {
	if !r.read {
		r.read = true
		copy(buffer, "part")
		return 4, nil
	}
	return 0, errors.New("nested body secret")
}

func TestCloseIsIdempotentAndRejectsLaterRequests(t *testing.T) {
	requests := 0
	transport, err := New(collectors.Environment{}, Options{
		RoundTripper: roundTripFunc(func(*http.Request) (*http.Response, error) {
			requests++
			return response("ok", 200, nil), nil
		}),
	})
	if err != nil {
		t.Fatalf("New() failed: %v", err)
	}
	if _, err := transport.Request(collectors.HTTPRequest{Method: "GET", URL: mustURL(t, "https://api.example.test/")}); err != nil {
		t.Fatalf("Request() before Close failed: %v", err)
	}
	if err := transport.Close(); err != nil {
		t.Fatalf("Close() failed: %v", err)
	}
	if err := transport.Close(); err != nil {
		t.Fatalf("second Close() failed: %v", err)
	}
	_, err = transport.Request(collectors.HTTPRequest{Method: "GET", URL: mustURL(t, "https://api.example.test/")})
	requireProcessFailure(t, err, "REST_HTTP_CLOSED")
	if requests != 1 {
		t.Errorf("round-tripper calls = %d, want 1 (the pre-Close request only)", requests)
	}
}

func TestCloseDuringRetrySleepStopsFurtherWork(t *testing.T) {
	// Close does not need to wait for a retry sleep in progress -- see
	// Close's doc comment -- but a sleeping attempt that resumes after
	// Close must not keep retrying against the network.
	requests := 0
	sleepStarted := make(chan struct{})
	releaseSleep := make(chan struct{})
	transport, err := New(collectors.Environment{}, Options{
		RoundTripper: roundTripFunc(func(*http.Request) (*http.Response, error) {
			requests++
			return response("rate", 429, nil), nil
		}),
		Sleep: func(float64) error {
			close(sleepStarted)
			<-releaseSleep
			return nil
		},
	})
	if err != nil {
		t.Fatalf("New() failed: %v", err)
	}
	requestDone := make(chan error, 1)
	go func() {
		_, requestErr := transport.Request(collectors.HTTPRequest{Method: "GET", URL: mustURL(t, "https://api.example.test/")})
		requestDone <- requestErr
	}()
	<-sleepStarted
	if err := transport.Close(); err != nil {
		t.Fatalf("Close() failed: %v", err)
	}
	close(releaseSleep)
	select {
	case requestErr := <-requestDone:
		requireProcessFailure(t, requestErr, "REST_HTTP_CLOSED")
	case <-time.After(5 * time.Second):
		t.Fatal("Request did not resume and fail closed after Close")
	}
	if requests != 1 {
		t.Errorf("round-tripper calls = %d, want 1 (no retry after Close)", requests)
	}
}
