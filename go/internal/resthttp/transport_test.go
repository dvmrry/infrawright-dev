package resthttp

import (
	"bytes"
	"crypto/tls"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dvmrry/infrawright-dev/go/internal/collectors"
	"github.com/dvmrry/infrawright-dev/go/internal/procerr"
)

func TestConfiguredCAAddsToPinnedNodeTrustAndRealTLSRequestSucceeds(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if got := request.Header.Get("User-Agent"); got != "" {
			t.Errorf("User-Agent = %q, want absent", got)
		}
		if got := request.Header.Get("Accept-Encoding"); got != "" {
			t.Errorf("Accept-Encoding = %q, want absent", got)
		}
		_, _ = io.WriteString(writer, "[]")
	}))
	t.Cleanup(server.Close)

	certificate := server.Certificate()
	bundle := filepath.Join(t.TempDir(), "custom.pem")
	encoded := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificate.Raw})
	if err := os.WriteFile(bundle, encoded, 0o600); err != nil {
		t.Fatalf("os.WriteFile(%q) failed: %v", bundle, err)
	}
	combined, err := trustedCertificates(collectors.Environment{"REQUESTS_CA_BUNDLE": bundle}, true)
	if err != nil {
		t.Fatalf("trustedCertificates() failed: %v", err)
	}
	if len(combined.Subjects()) != nodeBundledRootCount+1 {
		t.Fatalf("combined trust has %d subjects, want %d pinned roots plus one custom root", len(combined.Subjects()), nodeBundledRootCount)
	}

	transport, err := CreateRestHTTPTransport(
		collectors.Environment{"REQUESTS_CA_BUNDLE": bundle},
		RestHTTPTransportOptions{},
	)
	if err != nil {
		t.Fatalf("CreateRestHTTPTransport() failed: %v", err)
	}
	t.Cleanup(func() { _ = transport.Close() })
	got, err := transport.Request(collectors.HTTPRequest{Method: "GET", URL: mustURL(t, server.URL)})
	if err != nil {
		t.Fatalf("Request(%q) failed: %v", server.URL, err)
	}
	if string(got.Body) != "[]" {
		t.Errorf("Request(%q).Body = %q, want %q", server.URL, got.Body, "[]")
	}
}

func TestProductionTransportUsesSnapshottedHTTPProxy(t *testing.T) {
	proxy := startTunnelCaptureServer(t, false, false, tls.Certificate{})

	transport, err := CreateRestHTTPTransport(
		collectors.Environment{"HTTP_PROXY": "http://" + proxy.address},
		RestHTTPTransportOptions{},
	)
	if err != nil {
		t.Fatalf("CreateRestHTTPTransport() failed: %v", err)
	}
	t.Cleanup(func() { _ = transport.Close() })
	result, err := transport.Request(collectors.HTTPRequest{
		Method: "GET",
		URL:    mustURL(t, "http://collector.invalid/resource"),
	})
	if err != nil {
		t.Fatalf("proxied Request() failed: %v", err)
	}
	if string(result.Body) != "[]" {
		t.Errorf("proxied Request() body = %q, want []", result.Body)
	}
	observed := awaitTunnelCapture(t, proxy)
	if observed.connect.line != "CONNECT collector.invalid:80 HTTP/1.1" ||
		observed.inner.line != "GET /resource HTTP/1.1" {
		t.Errorf("proxied request = CONNECT %q inner %q", observed.connect.line, observed.inner.line)
	}
}

func TestHTTPProxyAloneDoesNotCaptureRealHTTPSRequest(t *testing.T) {
	var proxyCalls atomic.Int64
	proxy := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		proxyCalls.Add(1)
		http.Error(writer, "HTTPS must not use HTTP_PROXY", http.StatusBadGateway)
	}))
	t.Cleanup(proxy.Close)
	target := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, _ = io.WriteString(writer, "direct")
	}))
	t.Cleanup(target.Close)

	bundle := filepath.Join(t.TempDir(), "target.pem")
	encoded := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: target.Certificate().Raw})
	if err := os.WriteFile(bundle, encoded, 0o600); err != nil {
		t.Fatalf("os.WriteFile(%q) failed: %v", bundle, err)
	}
	transport, err := CreateRestHTTPTransport(collectors.Environment{
		"HTTP_PROXY":         proxy.URL,
		"REQUESTS_CA_BUNDLE": bundle,
	}, RestHTTPTransportOptions{})
	if err != nil {
		t.Fatalf("CreateRestHTTPTransport() failed: %v", err)
	}
	t.Cleanup(func() { _ = transport.Close() })
	result, err := transport.Request(collectors.HTTPRequest{Method: "GET", URL: mustURL(t, target.URL)})
	if err != nil {
		t.Fatalf("direct HTTPS Request() failed: %v", err)
	}
	if string(result.Body) != "direct" || proxyCalls.Load() != 0 {
		t.Errorf("HTTPS Request() = body %q proxy calls %d, want direct/0", result.Body, proxyCalls.Load())
	}
}

func TestRealTLSVerificationFailureIsClassifiedAsCertificate(t *testing.T) {
	target := httptest.NewUnstartedServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, _ = io.WriteString(writer, "unreachable")
	}))
	target.Config.ErrorLog = log.New(io.Discard, "", 0)
	target.StartTLS()
	t.Cleanup(target.Close)

	transport, err := CreateRestHTTPTransport(collectors.Environment{}, RestHTTPTransportOptions{})
	if err != nil {
		t.Fatalf("CreateRestHTTPTransport() failed: %v", err)
	}
	t.Cleanup(func() { _ = transport.Close() })
	_, err = transport.Request(collectors.HTTPRequest{Method: "GET", URL: mustURL(t, target.URL)})
	failure := requireProcessFailure(t, err, "REST_HTTP_TRANSPORT_FAILED")
	if !strings.Contains(failure.Message, "(certificate failure)") || failure.Retryable {
		t.Errorf("real TLS failure = message %q retryable %t, want non-retryable certificate failure", failure.Message, failure.Retryable)
	}
}

func TestInvalidCAFailsBeforeRoundTripperUse(t *testing.T) {
	bundle := filepath.Join(t.TempDir(), "invalid.pem")
	if err := os.WriteFile(bundle, []byte("not a certificate"), 0o600); err != nil {
		t.Fatalf("os.WriteFile(%q) failed: %v", bundle, err)
	}
	requests := 0
	_, err := CreateRestHTTPTransport(
		collectors.Environment{"SSL_CERT_FILE": bundle},
		RestHTTPTransportOptions{RoundTripper: roundTripFunc(func(*http.Request) (*http.Response, error) {
			requests++
			return response("[]", 200, nil), nil
		})},
	)
	requireProcessFailure(t, err, "REST_CA_BUNDLE_FAILED")
	if requests != 0 {
		t.Errorf("round-tripper calls = %d, want 0", requests)
	}
}

func TestConfiguredCASelectionAndValidation(t *testing.T) {
	directory := t.TempDir()
	badResidue := filepath.Join(directory, "residue.pem")
	if err := os.WriteFile(badResidue, []byte("# comment\nnot allowed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	tooLarge := filepath.Join(directory, "large.pem")
	if err := os.WriteFile(tooLarge, bytes.Repeat([]byte{'x'}, caBundleLimitBytes+1), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{directory, badResidue, tooLarge, filepath.Join(directory, "missing.pem")} {
		_, err := customCACertificates(path)
		requireProcessFailure(t, err, "REST_CA_BUNDLE_FAILED")
	}
}

func TestCABundleResidueUsesECMAScriptTrimSemantics(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	t.Cleanup(server.Close)
	encoded := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})
	for _, tc := range []struct {
		name    string
		residue string
		wantErr bool
	}{
		{name: "BOM is JavaScript whitespace", residue: "\ufeff"},
		{name: "next-line is not JavaScript whitespace", residue: "\u0085", wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bundle := filepath.Join(t.TempDir(), "bundle.pem")
			content := append([]byte(tc.residue+"\n"), encoded...)
			if err := os.WriteFile(bundle, content, 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := customCACertificates(bundle)
			if tc.wantErr {
				requireProcessFailure(t, err, "REST_CA_BUNDLE_FAILED")
			} else if err != nil {
				t.Fatalf("customCACertificates() failed: %v", err)
			}
		})
	}
}

func TestCustomCAEnvironmentPrecedenceAndOptOut(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing.pem")
	includeFalse := false
	transport, err := CreateRestHTTPTransport(collectors.Environment{
		"REQUESTS_CA_BUNDLE": missing,
	}, RestHTTPTransportOptions{
		IncludeCustomCA: &includeFalse,
		RoundTripper: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return response("ok", 200, nil), nil
		}),
	})
	if err != nil {
		t.Fatalf("CreateRestHTTPTransport(includeCustomCA=false) failed: %v", err)
	}
	if err := transport.Close(); err != nil {
		t.Fatalf("Close() failed: %v", err)
	}

	_, err = CreateRestHTTPTransport(collectors.Environment{
		"REQUESTS_CA_BUNDLE": missing,
		"SSL_CERT_FILE":      filepath.Join(t.TempDir(), "also-missing.pem"),
	}, RestHTTPTransportOptions{})
	requireProcessFailure(t, err, "REST_CA_BUNDLE_FAILED")
}

func TestTransportPersistsScopedLegacySessionCookie(t *testing.T) {
	var mu sync.Mutex
	var observed []*http.Request
	transport, err := CreateRestHTTPTransport(collectors.Environment{}, RestHTTPTransportOptions{
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
		t.Fatalf("CreateRestHTTPTransport() failed: %v", err)
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

func TestTransportRejectsPublicSuffixCookiesAndAcceptsParentDomain(t *testing.T) {
	var observed []http.Header
	transport, err := CreateRestHTTPTransport(collectors.Environment{}, RestHTTPTransportOptions{
		RoundTripper: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			observed = append(observed, request.Header.Clone())
			if len(observed) == 1 {
				headers := make(http.Header)
				headers.Add("Set-Cookie", "public=reject; Domain=com; Path=/; Secure")
				headers.Add("Set-Cookie", "parent=accept; Domain=example.com; Path=/; Secure")
				headers.Add("Set-Cookie", "host=only; Path=/; Secure")
				return response("{}", 200, headers), nil
			}
			return response("[]", 200, nil), nil
		}),
	})
	if err != nil {
		t.Fatalf("CreateRestHTTPTransport() failed: %v", err)
	}
	t.Cleanup(func() { _ = transport.Close() })
	for _, target := range []string{"https://auth.example.com/session", "https://api.example.com/data"} {
		if _, err := transport.Request(collectors.HTTPRequest{Method: "GET", URL: mustURL(t, target)}); err != nil {
			t.Fatalf("Request(%q) failed: %v", target, err)
		}
	}
	if got := observed[1].Get("Cookie"); got != "parent=accept" {
		t.Errorf("cross-host Cookie = %q, want %q", got, "parent=accept")
	}
}

func TestCookiePathMatchingPreservesWHATWGPercentEscapes(t *testing.T) {
	var observed []string
	transport, err := CreateRestHTTPTransport(collectors.Environment{}, RestHTTPTransportOptions{
		RoundTripper: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			observed = append(observed, request.Header.Get("Cookie"))
			if len(observed) == 1 {
				return response("{}", 200, http.Header{
					"Set-Cookie": []string{"scoped=secret; Path=/a; Secure"},
				}), nil
			}
			return response("{}", 200, nil), nil
		}),
	})
	if err != nil {
		t.Fatalf("CreateRestHTTPTransport() failed: %v", err)
	}
	t.Cleanup(func() { _ = transport.Close() })
	for _, target := range []string{
		"https://api.example.com/a/start",
		"https://api.example.com/a/b",
		"https://api.example.com/a%2Fb",
		"https://api.example.com/a%41",
		"https://api.example.com/a%2E/b",
	} {
		if _, err := transport.Request(collectors.HTTPRequest{Method: "GET", URL: mustURL(t, target)}); err != nil {
			t.Fatalf("Request(%q) failed: %v", target, err)
		}
	}
	if got, want := observed, []string{"", "scoped=secret", "", "", ""}; !slices.Equal(got, want) {
		t.Errorf("Cookie headers = %v, want %v", got, want)
	}
}

func TestCookieJarRejectsMultiLabelAndPrivatePublicSuffixes(t *testing.T) {
	jar, err := cookiejar.New(&cookiejar.Options{PublicSuffixList: collectorPublicSuffixList{}})
	if err != nil {
		t.Fatalf("cookiejar.New() failed: %v", err)
	}
	cases := []struct {
		host   string
		domain string
	}{
		{"auth.example.co.uk", "co.uk"},
		{"user.github.io", "github.io"},
	}
	for _, tc := range cases {
		target := mustURL(t, "https://"+tc.host+"/")
		jar.SetCookies(target, []*http.Cookie{{Name: "leak", Value: "blocked", Domain: tc.domain, Path: "/", Secure: true}})
		if got := jar.Cookies(target); len(got) != 0 {
			t.Errorf("Domain=%q cookies for %q = %v, want rejected", tc.domain, tc.host, got)
		}
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
	transport, err := CreateRestHTTPTransport(collectors.Environment{}, RestHTTPTransportOptions{
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
		t.Fatalf("CreateRestHTTPTransport() failed: %v", err)
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
			transport, err := CreateRestHTTPTransport(collectors.Environment{}, RestHTTPTransportOptions{
				RoundTripper: roundTripFunc(func(*http.Request) (*http.Response, error) {
					requests++
					headers := make(http.Header)
					headers.Set("Location", "https://auth.example.test/replay")
					return response("redirect", status, headers), nil
				}),
			})
			if err != nil {
				t.Fatalf("CreateRestHTTPTransport() failed: %v", err)
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
		message  string
	}{
		{
			name: "missing location", code: "INVALID_REST_HTTP_RESPONSE",
			message: "redirect response is missing a location header",
		},
		{
			name: "invalid location", location: []string{"http://[invalid"},
			code: "INVALID_REST_HTTP_RESPONSE", message: "redirect response has an invalid location header",
		},
		{
			name: "network path missing host", location: []string{"//"},
			code: "INVALID_REST_HTTP_RESPONSE", message: "redirect response has an invalid location header",
		},
		{
			name: "unsupported scheme", location: []string{"file:///secret"},
			code: "REST_HTTP_REDIRECT_REFUSED", message: "redirect response selected an unsupported URL scheme",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			transport, err := CreateRestHTTPTransport(collectors.Environment{}, RestHTTPTransportOptions{
				RoundTripper: roundTripFunc(func(*http.Request) (*http.Response, error) {
					headers := make(http.Header)
					if tc.location != nil {
						headers["Location"] = tc.location
					}
					return response("redirect", 302, headers), nil
				}),
			})
			if err != nil {
				t.Fatalf("CreateRestHTTPTransport() failed: %v", err)
			}
			t.Cleanup(func() { _ = transport.Close() })
			_, err = transport.Request(collectors.HTTPRequest{Method: "GET", URL: mustURL(t, "https://api.example.test/start")})
			failure := requireProcessFailure(t, err, tc.code)
			if failure.Message != tc.message {
				t.Errorf("redirect failure message = %q, want %q", failure.Message, tc.message)
			}
		})
	}
}

type testPerformanceRecorder struct {
	mu       sync.Mutex
	now      float64
	attempts []HTTPAttemptPerformance
	retries  []HTTPRetryPerformance
}

func (p *testPerformanceRecorder) Now() float64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	value := p.now
	p.now += 5
	return value
}

func (p *testPerformanceRecorder) DurationSince(startedMs float64) float64 {
	return p.Now() - startedMs
}

func (p *testPerformanceRecorder) RecordHTTPAttempt(input HTTPAttemptPerformance) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.attempts = append(p.attempts, input)
	return nil
}

func (p *testPerformanceRecorder) RecordHTTPRetry(input HTTPRetryPerformance) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.retries = append(p.retries, input)
	return nil
}

func TestRedirectLocationsUseWHATWGSpecialURLSemantics(t *testing.T) {
	cases := []struct {
		name          string
		location      string
		wantURL       string
		wantSensitive bool
	}{
		{"same-scheme relative", "https:next", "https://base.example/a/next", true},
		{"same-scheme rooted", "https:/next", "https://base.example/next", true},
		{"backslash authority", `\\other.example\p`, "https://other.example/p", false},
		{"surrounding whitespace", " \t/next\r\n", "https://base.example/next", true},
		{"IDN authority", "https://bücher.example/final", "https://xn--bcher-kva.example/final", false},
		{"percent-encoded authority", "https://%65xample.com/final", "https://example.com/final", false},
		{"legacy IPv4 authority", "https://127.1/final", "https://127.0.0.1/final", false},
		{"extra authority slashes", "///other.example/final", "https://other.example/final", false},
		{"percent-encoded parent segment", "%2e%2e/final", "https://base.example/final", true},
		{"query backslash preserved", `?x=\foo`, `https://base.example/a/b?x=\foo`, true},
		{"path and query backslashes differ", `next\child?x=\foo`, `https://base.example/a/next/child?x=\foo`, true},
		{"query space encoded", "?q=hello world", "https://base.example/a/b?q=hello%20world", true},
		{"special query apostrophe encoded", "?q='", "https://base.example/a/b?q=%27", true},
		{"query angle brackets encoded", "?q=<x>", "https://base.example/a/b?q=%3Cx%3E", true},
		{"path control encoded", "/a\x00b", "https://base.example/a%00b", true},
		{"malformed percent preserved", "/a/%zz", "https://base.example/a/%zz", true},
		{"raw pipe preserved cross-origin", "https://other.example/x|y", "https://other.example/x|y", false},
		{"same-scheme colon remains relative", "https:foo:bar", "https://base.example/a/foo:bar", true},
		{"same-scheme digit remains relative", "https:a1:b", "https://base.example/a/a1:b", true},
		{"same-scheme plus remains relative", "https:a+b:c", "https://base.example/a/a+b:c", true},
		{"same-scheme hyphen remains relative", "https:a-b:c", "https://base.example/a/a-b:c", true},
		{"same-scheme dot remains relative", "https:a.b:c", "https://base.example/a/a.b:c", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			requests := 0
			transport, err := CreateRestHTTPTransport(collectors.Environment{}, RestHTTPTransportOptions{
				RoundTripper: roundTripFunc(func(request *http.Request) (*http.Response, error) {
					requests++
					if requests == 1 {
						return response("redirect", 302, http.Header{"Location": []string{tc.location}}), nil
					}
					if got := request.URL.String(); got != tc.wantURL {
						t.Errorf("redirected URL = %q, want %q", got, tc.wantURL)
					}
					for _, name := range []string{"Authorization", "Cookie"} {
						present := request.Header.Get(name) != ""
						if present != tc.wantSensitive {
							t.Errorf("redirected %s present = %t, want %t", name, present, tc.wantSensitive)
						}
					}
					return response("ok", 200, nil), nil
				}),
			})
			if err != nil {
				t.Fatalf("CreateRestHTTPTransport() failed: %v", err)
			}
			t.Cleanup(func() { _ = transport.Close() })
			_, err = transport.Request(collectors.HTTPRequest{
				Method:  "GET",
				URL:     mustURL(t, "https://base.example/a/b"),
				Headers: map[string]string{"Authorization": "Bearer secret", "Cookie": "manual=value"},
			})
			if err != nil {
				t.Fatalf("Request() failed: %v", err)
			}
			if requests != 2 {
				t.Errorf("requests = %d, want 2", requests)
			}
		})
	}
}

func TestTransportRetries429InsideProductionSeam(t *testing.T) {
	requests := 0
	waits := []float64{}
	performance := &testPerformanceRecorder{}
	transport, err := CreateRestHTTPTransport(collectors.Environment{}, RestHTTPTransportOptions{
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
		Performance: performance,
		Sleep: func(milliseconds float64) error {
			waits = append(waits, milliseconds)
			return nil
		},
	})
	if err != nil {
		t.Fatalf("CreateRestHTTPTransport() failed: %v", err)
	}
	t.Cleanup(func() { _ = transport.Close() })
	context := &collectors.HTTPRequestPerformanceContext{
		Classification: collectors.ClassificationAuthentication,
		EndpointFamily: "oauth2/v1/token",
		Phase:          "fetch",
		Product:        "oneapi",
	}
	result, err := transport.Request(collectors.HTTPRequest{
		Method:      "POST",
		URL:         mustURL(t, "https://tenant.zslogin.net/oauth2/v1/token"),
		Body:        []byte("grant_type=client_credentials"),
		Performance: context,
	})
	if err != nil {
		t.Fatalf("Request() failed: %v", err)
	}
	if result.Status != 200 || requests != 3 {
		t.Errorf("Request() = status %d after %d requests, want 200 after 3", result.Status, requests)
	}
	if fmt.Sprint(waits) != "[250 2000]" {
		t.Errorf("retry waits = %v, want [250 2000]", waits)
	}
	if len(performance.attempts) != 3 || len(performance.retries) != 2 {
		t.Errorf("performance attempts/retries = %d/%d, want 3/2", len(performance.attempts), len(performance.retries))
	}
}

func TestConcurrentRetryBudgetsStayIsolated(t *testing.T) {
	var mu sync.Mutex
	attempts := map[string]int{}
	waits := []float64{}
	active := 0
	maxActive := 0
	transport, err := CreateRestHTTPTransport(collectors.Environment{}, RestHTTPTransportOptions{
		RoundTripper: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			mu.Lock()
			active++
			if active > maxActive {
				maxActive = active
			}
			mu.Unlock()
			time.Sleep(10 * time.Millisecond)
			mu.Lock()
			active--
			attempts[request.URL.Path]++
			attempt := attempts[request.URL.Path]
			mu.Unlock()
			if request.URL.Path == "/slow" && attempt == 1 {
				return response("rate", 429, http.Header{"Retry-After": []string{"0.25"}}), nil
			}
			return response("ok", 200, nil), nil
		}),
		Sleep: func(milliseconds float64) error {
			mu.Lock()
			defer mu.Unlock()
			waits = append(waits, milliseconds)
			return nil
		},
	})
	if err != nil {
		t.Fatalf("CreateRestHTTPTransport() failed: %v", err)
	}
	t.Cleanup(func() { _ = transport.Close() })
	var wait sync.WaitGroup
	errorsByPath := make(chan error, 2)
	for _, path := range []string{"slow", "fast"} {
		path := path
		wait.Add(1)
		go func() {
			defer wait.Done()
			_, requestErr := transport.Request(collectors.HTTPRequest{
				Method: "GET",
				URL:    mustURL(t, "https://api.example.test/"+path),
			})
			errorsByPath <- requestErr
		}()
	}
	wait.Wait()
	close(errorsByPath)
	for requestErr := range errorsByPath {
		if requestErr != nil {
			t.Errorf("concurrent Request() failed: %v", requestErr)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if attempts["/slow"] != 2 || attempts["/fast"] != 1 {
		t.Errorf("attempts = %v, want /slow:2 /fast:1", attempts)
	}
	if fmt.Sprint(waits) != "[250]" {
		t.Errorf("waits = %v, want [250]", waits)
	}
	if maxActive != 2 {
		t.Errorf("max active = %d, want 2", maxActive)
	}
}

func TestResponseLimitClosesOversizedDeclaredBody(t *testing.T) {
	body := &trackingBody{reader: strings.NewReader("too large")}
	transport, err := CreateRestHTTPTransport(collectors.Environment{}, RestHTTPTransportOptions{
		ResponseLimitBytes: intPointer(4),
		RoundTripper: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Header: http.Header{"Content-Length": []string{"9"}}, Body: body}, nil
		}),
	})
	if err != nil {
		t.Fatalf("CreateRestHTTPTransport() failed: %v", err)
	}
	t.Cleanup(func() { _ = transport.Close() })
	_, err = transport.Request(collectors.HTTPRequest{Method: "GET", URL: mustURL(t, "https://api.example.test/data")})
	requireProcessFailure(t, err, "REST_HTTP_RESPONSE_LIMIT")
	if got := body.closeCount(); got != 1 {
		t.Errorf("body close count = %d, want 1", got)
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
			transport, err := CreateRestHTTPTransport(collectors.Environment{}, RestHTTPTransportOptions{
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
				t.Fatalf("CreateRestHTTPTransport() failed: %v", err)
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
	transport, err := CreateRestHTTPTransport(collectors.Environment{}, RestHTTPTransportOptions{
		RoundTripper: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return response("ok", 200, headers), nil
		}),
	})
	if err != nil {
		t.Fatalf("CreateRestHTTPTransport() failed: %v", err)
	}
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

func TestFinal429ReturnsAfterSixAttemptsWithoutExtraSleep(t *testing.T) {
	attempts := 0
	waits := 0
	transport, err := CreateRestHTTPTransport(collectors.Environment{}, RestHTTPTransportOptions{
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
		t.Fatalf("CreateRestHTTPTransport() failed: %v", err)
	}
	result, err := transport.Request(collectors.HTTPRequest{Method: "GET", URL: mustURL(t, "https://api.example.test/")})
	if err != nil {
		t.Fatalf("Request() failed: %v", err)
	}
	if result.Status != 429 || attempts != 6 || waits != 5 {
		t.Errorf("final 429 = status %d attempts %d waits %d, want 429/6/5", result.Status, attempts, waits)
	}
}

type codedFailure struct {
	code    string
	message string
}

func (e codedFailure) Error() string     { return e.message }
func (e codedFailure) ErrorCode() string { return e.code }

func TestTransportFailureMasksIdentityAndNestedSecrets(t *testing.T) {
	secret := "nested-proxy-secret"
	transport, err := CreateRestHTTPTransport(collectors.Environment{}, RestHTTPTransportOptions{
		RoundTripper: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return nil, codedFailure{code: "ECONNREFUSED", message: secret}
		}),
	})
	if err != nil {
		t.Fatalf("CreateRestHTTPTransport() failed: %v", err)
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

func TestFailureClassificationLiteralMessagesAndRetryability(t *testing.T) {
	cases := []struct {
		name      string
		err       error
		kind      string
		hint      string
		retryable bool
	}{
		{"certificate", codedFailure{code: "SELF_SIGNED_CERT_IN_CHAIN"}, "certificate", "corporate TLS inspection?", false},
		{"timeout", codedFailure{code: "UND_ERR_CONNECT_TIMEOUT"}, "timeout", "request timed out;", true},
		{"connection", codedFailure{code: "ECONNREFUSED"}, "connection", "check HTTPS_PROXY/NO_PROXY, DNS", true},
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

func TestNativeTLSProtocolFailureIsCertificateAndNonRetryable(t *testing.T) {
	transport, err := CreateRestHTTPTransport(collectors.Environment{}, RestHTTPTransportOptions{
		RoundTripper: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return nil, tls.RecordHeaderError{Msg: "first record does not look like a TLS handshake"}
		}),
	})
	if err != nil {
		t.Fatalf("CreateRestHTTPTransport() failed: %v", err)
	}
	_, err = transport.Request(collectors.HTTPRequest{
		Method: "GET",
		URL:    mustURL(t, "https://api.example.test/"),
	})
	failure := requireProcessFailure(t, err, "REST_HTTP_TRANSPORT_FAILED")
	if failure.Retryable || !strings.Contains(failure.Message, "(certificate failure)") {
		t.Errorf("native TLS failure = %#v, want non-retryable certificate failure", failure)
	}
}

func TestPlainTLSProtocolFailureIsCertificateAndNonRetryable(t *testing.T) {
	transport, err := CreateRestHTTPTransport(collectors.Environment{}, RestHTTPTransportOptions{
		RoundTripper: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return nil, errors.New("tls: server selected unsupported protocol version")
		}),
	})
	if err != nil {
		t.Fatalf("CreateRestHTTPTransport() failed: %v", err)
	}
	t.Cleanup(func() { _ = transport.Close() })
	_, err = transport.Request(collectors.HTTPRequest{Method: "GET", URL: mustURL(t, "https://api.example.test/")})
	failure := requireProcessFailure(t, err, "REST_HTTP_TRANSPORT_FAILED")
	if failure.Retryable || !strings.Contains(failure.Message, "(certificate failure)") {
		t.Errorf("plain TLS failure = %#v, want non-retryable certificate failure", failure)
	}
}

func TestRedirectAndOptionBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name    string
		options RestHTTPTransportOptions
		message string
	}{
		{"zero timeout", RestHTTPTransportOptions{RequestTimeoutMs: intPointer(0)}, "request timeout must be a positive bounded integer"},
		{"large timeout", RestHTTPTransportOptions{RequestTimeoutMs: intPointer(maximumRequestTimeoutMs + 1)}, "request timeout must be a positive bounded integer"},
		{"zero response", RestHTTPTransportOptions{ResponseLimitBytes: intPointer(0)}, "response limit must be a positive bounded integer"},
		{"large response", RestHTTPTransportOptions{ResponseLimitBytes: intPointer(maximumResponseLimitByte + 1)}, "response limit must be a positive bounded integer"},
		{"negative redirects", RestHTTPTransportOptions{MaxRedirects: intPointer(-1)}, "max redirects must be between 0 and 20"},
		{"large redirects", RestHTTPTransportOptions{MaxRedirects: intPointer(21)}, "max redirects must be between 0 and 20"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := CreateRestHTTPTransport(collectors.Environment{}, tc.options)
			if err == nil || err.Error() != tc.message {
				t.Errorf("CreateRestHTTPTransport() error = %v, want %q", err, tc.message)
			}
		})
	}

	requests := 0
	transport, err := CreateRestHTTPTransport(collectors.Environment{}, RestHTTPTransportOptions{
		MaxRedirects: intPointer(0),
		RoundTripper: roundTripFunc(func(*http.Request) (*http.Response, error) {
			requests++
			return response("redirect", 302, http.Header{"Location": []string{"/next"}}), nil
		}),
	})
	if err != nil {
		t.Fatalf("CreateRestHTTPTransport() failed: %v", err)
	}
	_, err = transport.Request(collectors.HTTPRequest{Method: "GET", URL: mustURL(t, "https://tenant.zslogin.net/start?secret=yes")})
	failure := requireProcessFailure(t, err, "REST_HTTP_REDIRECT_LIMIT")
	if failure.Message != "too many redirects while requesting https://<vanity>.zslogin.net/start" {
		t.Errorf("redirect-limit message = %q", failure.Message)
	}
	if requests != 1 {
		t.Errorf("maxRedirects=0 request count = %d, want 1", requests)
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

func TestBodyReadFailureUsesFixedRetryableMessage(t *testing.T) {
	body := &trackingBody{reader: &failingReader{}}
	transport, err := CreateRestHTTPTransport(collectors.Environment{}, RestHTTPTransportOptions{
		RoundTripper: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: body}, nil
		}),
	})
	if err != nil {
		t.Fatalf("CreateRestHTTPTransport() failed: %v", err)
	}
	_, err = transport.Request(collectors.HTTPRequest{Method: "GET", URL: mustURL(t, "https://api.example.test/data")})
	failure := requireProcessFailure(t, err, "REST_HTTP_TRANSPORT_FAILED")
	if failure.Message != "HTTP response body could not be read" || !failure.Retryable {
		t.Errorf("body failure = message %q retryable %t", failure.Message, failure.Retryable)
	}
}

func TestCloseFallbackIdempotencyAndClosedRequest(t *testing.T) {
	closeCalls := 0
	destroyCalls := 0
	transport, err := CreateRestHTTPTransport(collectors.Environment{}, RestHTTPTransportOptions{
		RoundTripper: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return response("ok", 200, nil), nil
		}),
		Close: func() error {
			closeCalls++
			return errors.New("close failed")
		},
		Destroy: func() error {
			destroyCalls++
			return nil
		},
	})
	if err != nil {
		t.Fatalf("CreateRestHTTPTransport() failed: %v", err)
	}
	if err := transport.Close(); err != nil {
		t.Fatalf("Close() with successful destroy fallback failed: %v", err)
	}
	if err := transport.Close(); err != nil {
		t.Fatalf("second Close() failed: %v", err)
	}
	if closeCalls != 1 || destroyCalls != 1 {
		t.Errorf("close/destroy calls = %d/%d, want 1/1", closeCalls, destroyCalls)
	}
	_, err = transport.Request(collectors.HTTPRequest{Method: "GET", URL: mustURL(t, "https://api.example.test/")})
	requireProcessFailure(t, err, "REST_HTTP_CLOSED")

	failed, err := CreateRestHTTPTransport(collectors.Environment{}, RestHTTPTransportOptions{
		RoundTripper: roundTripFunc(func(*http.Request) (*http.Response, error) { return response("ok", 200, nil), nil }),
		Close:        func() error { return errors.New("close failed") },
		Destroy:      func() error { return errors.New("destroy failed") },
	})
	if err != nil {
		t.Fatalf("CreateRestHTTPTransport(failed cleanup) failed: %v", err)
	}
	requireProcessFailure(t, failed.Close(), "REST_HTTP_CLEANUP_FAILED")
}

func TestCloseWaitsForAdmittedLogicalRequest(t *testing.T) {
	roundTripStarted := make(chan struct{})
	releaseRoundTrip := make(chan struct{})
	cleanupStarted := make(chan struct{})
	transportValue, err := CreateRestHTTPTransport(collectors.Environment{}, RestHTTPTransportOptions{
		RoundTripper: roundTripFunc(func(*http.Request) (*http.Response, error) {
			close(roundTripStarted)
			<-releaseRoundTrip
			return response("ok", 200, nil), nil
		}),
		Close: func() error {
			close(cleanupStarted)
			return nil
		},
	})
	if err != nil {
		t.Fatalf("CreateRestHTTPTransport() failed: %v", err)
	}
	transport := transportValue.(*transport)
	target := mustURL(t, "https://api.example.test/")
	requestDone := make(chan error, 1)
	go func() {
		_, requestErr := transport.Request(collectors.HTTPRequest{
			Method: "GET",
			URL:    target,
		})
		requestDone <- requestErr
	}()
	<-roundTripStarted
	closeDone := make(chan error, 1)
	go func() { closeDone <- transport.Close() }()

	closing := false
	for attempt := 0; attempt < 10_000; attempt++ {
		transport.mu.Lock()
		closing = transport.closing
		transport.mu.Unlock()
		if closing {
			break
		}
		runtime.Gosched()
	}
	if !closing {
		t.Fatal("Close did not enter the closing state")
	}
	select {
	case <-cleanupStarted:
		t.Fatal("Close began cleanup before the admitted request drained")
	default:
	}
	select {
	case err := <-closeDone:
		t.Fatalf("Close returned before the admitted request drained: %v", err)
	default:
	}

	close(releaseRoundTrip)
	select {
	case requestErr := <-requestDone:
		if requestErr != nil {
			t.Fatalf("admitted Request failed: %v", requestErr)
		}
	case <-time.After(time.Second):
		t.Fatal("admitted Request did not finish")
	}
	select {
	case closeErr := <-closeDone:
		if closeErr != nil {
			t.Fatalf("Close failed: %v", closeErr)
		}
	case <-time.After(time.Second):
		t.Fatal("Close did not finish after the request drained")
	}
	select {
	case <-cleanupStarted:
	default:
		t.Fatal("Close returned without running cleanup")
	}
}

func TestRetryClockFailureHasExactProcessFailure(t *testing.T) {
	transport, err := CreateRestHTTPTransport(collectors.Environment{}, RestHTTPTransportOptions{
		RoundTripper: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return response("rate", 429, nil), nil
		}),
		Sleep: func(float64) error { return errors.New("clock secret") },
	})
	if err != nil {
		t.Fatalf("CreateRestHTTPTransport() failed: %v", err)
	}
	_, err = transport.Request(collectors.HTTPRequest{Method: "GET", URL: mustURL(t, "https://api.example.test/")})
	failure := requireProcessFailure(t, err, "REST_HTTP_RETRY_CLOCK_FAILED")
	if failure.Category != procerr.CategoryIO || !failure.Retryable || failure.Message != "HTTP retry clock failed" {
		t.Errorf("retry clock failure = %#v", failure)
	}
}
