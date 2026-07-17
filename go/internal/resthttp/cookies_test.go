package resthttp

import (
	"bufio"
	"crypto/tls"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/dvmrry/infrawright-dev/go/internal/collectors"
)

type toughCookieExpiryCorpus struct {
	SchemaVersion int `json:"schema_version"`
	Oracle        struct {
		Runtime        string `json:"runtime"`
		Package        string `json:"package"`
		PackageVersion string `json:"package_version"`
	} `json:"oracle"`
	DateVectors []struct {
		Name    string  `json:"name"`
		Input   string  `json:"input"`
		WantUTC *string `json:"want_utc"`
	} `json:"date_vectors"`
	MaxAgeVectors []struct {
		Name  string `json:"name"`
		Input string `json:"input"`
		Want  string `json:"want"`
	} `json:"max_age_vectors"`
	JarVectors []struct {
		Name          string `json:"name"`
		SetURL        string `json:"set_url"`
		GetURL        string `json:"get_url"`
		SeedSetCookie string `json:"seed_set_cookie"`
		SetCookie     string `json:"set_cookie"`
		WantCookie    string `json:"want_cookie"`
	} `json:"jar_vectors"`
}

type toughCookiePairCorpus struct {
	SchemaVersion int `json:"schema_version"`
	Oracle        struct {
		Runtime            string `json:"runtime"`
		Package            string `json:"package"`
		PackageVersion     string `json:"package_version"`
		WirePackage        string `json:"wire_package"`
		WirePackageVersion string `json:"wire_package_version"`
	} `json:"oracle"`
	ValueVectors []struct {
		Name           string `json:"name"`
		SetCookie      string `json:"set_cookie"`
		ParsedValue    string `json:"parsed_value"`
		WantCookie     string `json:"want_cookie"`
		ProductionWire bool   `json:"production_wire"`
	} `json:"value_vectors"`
	NameVectors []struct {
		Name           string `json:"name"`
		SetCookie      string `json:"set_cookie"`
		ParsedKey      string `json:"parsed_key"`
		WantCookie     string `json:"want_cookie"`
		ProductionWire bool   `json:"production_wire"`
	} `json:"name_vectors"`
	AttributeVectors []struct {
		Name      string `json:"name"`
		SetCookie string `json:"set_cookie"`
		SetURL    string `json:"set_url"`
		GetURL    string `json:"get_url"`
		Parsed    struct {
			Domain   *string `json:"domain"`
			Path     *string `json:"path"`
			Secure   bool    `json:"secure"`
			HTTPOnly bool    `json:"http_only"`
			SameSite *string `json:"same_site"`
			MaxAge   *int    `json:"max_age"`
			Expires  string  `json:"expires"`
		} `json:"parsed"`
		WantCookie string `json:"want_cookie"`
	} `json:"attribute_vectors"`
	DomainScopeVectors []struct {
		Name         string  `json:"name"`
		SetCookie    string  `json:"set_cookie"`
		SetURL       string  `json:"set_url"`
		GetURL       string  `json:"get_url"`
		StoredDomain *string `json:"stored_domain"`
		HostOnly     *bool   `json:"host_only"`
		WantCookie   string  `json:"want_cookie"`
	} `json:"domain_scope_vectors"`
}

func loadToughCookieExpiryCorpus(t *testing.T) toughCookieExpiryCorpus {
	t.Helper()
	path := filepath.Join("testdata", "tough_cookie_6_0_2_expiry.json")
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("os.ReadFile(%q) failed: %v", path, err)
	}
	var corpus toughCookieExpiryCorpus
	if err := json.Unmarshal(content, &corpus); err != nil {
		t.Fatalf("json.Unmarshal(%q) failed: %v", path, err)
	}
	if corpus.SchemaVersion != 1 || corpus.Oracle.Package != "tough-cookie" ||
		corpus.Oracle.PackageVersion != "6.0.2" || corpus.Oracle.Runtime != "node v24.15.0" {
		t.Fatalf("expiry corpus provenance = version %d, %q %q on %q, want schema 1 tough-cookie 6.0.2 on node v24.15.0",
			corpus.SchemaVersion, corpus.Oracle.Package, corpus.Oracle.PackageVersion, corpus.Oracle.Runtime)
	}
	return corpus
}

func loadToughCookiePairCorpus(t *testing.T) toughCookiePairCorpus {
	t.Helper()
	path := filepath.Join("testdata", "tough_cookie_6_0_2_values.json")
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("os.ReadFile(%q) failed: %v", path, err)
	}
	var corpus toughCookiePairCorpus
	if err := json.Unmarshal(content, &corpus); err != nil {
		t.Fatalf("json.Unmarshal(%q) failed: %v", path, err)
	}
	if corpus.SchemaVersion != 1 || corpus.Oracle.Package != "tough-cookie" ||
		corpus.Oracle.PackageVersion != "6.0.2" || corpus.Oracle.Runtime != "node v24.15.0" ||
		corpus.Oracle.WirePackage != "undici" || corpus.Oracle.WirePackageVersion != "7.28.0" {
		t.Fatalf("pair corpus provenance = version %d, %q %q with %q %q on %q, want schema 1 tough-cookie 6.0.2 with undici 7.28.0 on node v24.15.0",
			corpus.SchemaVersion, corpus.Oracle.Package, corpus.Oracle.PackageVersion,
			corpus.Oracle.WirePackage, corpus.Oracle.WirePackageVersion, corpus.Oracle.Runtime)
	}
	return corpus
}

func TestToughCookieRequestValueDifferentialCorpus(t *testing.T) {
	corpus := loadToughCookiePairCorpus(t)
	for _, vector := range corpus.ValueVectors {
		t.Run(vector.Name, func(t *testing.T) {
			jar, err := cookiejar.New(&cookiejar.Options{PublicSuffixList: collectorPublicSuffixList{}})
			if err != nil {
				t.Fatalf("cookiejar.New() failed: %v", err)
			}
			setTarget := cookieContextURL(mustURL(t, "https://example.test/start"))
			jar.SetCookies(
				setTarget,
				acceptedResponseCookies(
					setTarget,
					http.Header{"set-cookie": []string{vector.SetCookie}},
					false,
				),
			)
			getTarget := cookieContextURL(mustURL(t, "https://example.test/final"))
			if got := cookieHeader(jar.Cookies(getTarget)); got != vector.WantCookie {
				t.Errorf("Set-Cookie %q then Cookie = %q, want %q",
					vector.SetCookie, got, vector.WantCookie)
			}
			if got := validHTTPHeaderValue(vector.WantCookie); got != vector.ProductionWire {
				t.Errorf("validHTTPHeaderValue(%q) = %t, want %t",
					vector.WantCookie, got, vector.ProductionWire)
			}
		})
	}
}

func TestToughCookieRequestNameDifferentialCorpus(t *testing.T) {
	corpus := loadToughCookiePairCorpus(t)
	for _, vector := range corpus.NameVectors {
		t.Run(vector.Name, func(t *testing.T) {
			jar, err := cookiejar.New(&cookiejar.Options{PublicSuffixList: collectorPublicSuffixList{}})
			if err != nil {
				t.Fatalf("cookiejar.New() failed: %v", err)
			}
			target := cookieContextURL(mustURL(t, "https://example.test/start"))
			jar.SetCookies(
				target,
				acceptedResponseCookies(
					target,
					http.Header{"set-cookie": []string{vector.SetCookie}},
					false,
				),
			)
			if got := cookieHeader(jar.Cookies(target)); got != vector.WantCookie {
				t.Errorf("Set-Cookie %q then Cookie = %q, want %q",
					vector.SetCookie, got, vector.WantCookie)
			}
			if got := validHTTPHeaderValue(vector.WantCookie); got != vector.ProductionWire {
				t.Errorf("validHTTPHeaderValue(%q) = %t, want %t",
					vector.WantCookie, got, vector.ProductionWire)
			}
		})
	}
}

func optionalString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func sameSiteOracleName(mode http.SameSite) *string {
	var value string
	switch mode {
	case http.SameSiteStrictMode:
		value = "strict"
	case http.SameSiteLaxMode:
		value = "lax"
	case http.SameSiteNoneMode:
		value = "none"
	default:
		return nil
	}
	return &value
}

func TestToughCookieAttributeWhitespaceDifferentialCorpus(t *testing.T) {
	corpus := loadToughCookiePairCorpus(t)
	for _, vector := range corpus.AttributeVectors {
		t.Run(vector.Name, func(t *testing.T) {
			setTarget := cookieContextURL(mustURL(t, vector.SetURL))
			accepted := acceptedResponseCookies(
				setTarget,
				http.Header{"set-cookie": []string{vector.SetCookie}},
				false,
			)
			if len(accepted) != 1 {
				t.Fatalf("acceptedResponseCookies(%q) count = %d, want 1", vector.SetCookie, len(accepted))
			}
			cookie := accepted[0]
			wantDomain := ""
			if vector.Parsed.Domain != nil {
				wantDomain = *vector.Parsed.Domain
			}
			if cookie.Domain != wantDomain {
				t.Errorf("Set-Cookie %q Domain = %q, want %q", vector.SetCookie, cookie.Domain, wantDomain)
			}
			wantPath := ""
			if vector.Parsed.Path != nil {
				wantPath = *vector.Parsed.Path
			}
			if cookie.Path != wantPath {
				t.Errorf("Set-Cookie %q Path = %q, want %q", vector.SetCookie, cookie.Path, wantPath)
			}
			if cookie.Secure != vector.Parsed.Secure {
				t.Errorf("Set-Cookie %q Secure = %t, want %t", vector.SetCookie, cookie.Secure, vector.Parsed.Secure)
			}
			if cookie.HttpOnly != vector.Parsed.HTTPOnly {
				t.Errorf("Set-Cookie %q HttpOnly = %t, want %t", vector.SetCookie, cookie.HttpOnly, vector.Parsed.HTTPOnly)
			}
			gotSameSite := sameSiteOracleName(cookie.SameSite)
			if got, want := optionalString(gotSameSite), optionalString(vector.Parsed.SameSite); got != want {
				t.Errorf("Set-Cookie %q SameSite = %q, want %q", vector.SetCookie, got, want)
			}
			wantMaxAge := 0
			if vector.Parsed.MaxAge != nil {
				wantMaxAge = *vector.Parsed.MaxAge
			}
			if cookie.MaxAge != wantMaxAge {
				t.Errorf("Set-Cookie %q MaxAge = %d, want %d", vector.SetCookie, cookie.MaxAge, wantMaxAge)
			}
			gotExpires := "Infinity"
			if !cookie.Expires.IsZero() {
				gotExpires = cookie.Expires.UTC().Format("2006-01-02T15:04:05.000Z")
			}
			if gotExpires != vector.Parsed.Expires {
				t.Errorf("Set-Cookie %q Expires = %q, want %q", vector.SetCookie, gotExpires, vector.Parsed.Expires)
			}

			jar, err := cookiejar.New(&cookiejar.Options{PublicSuffixList: collectorPublicSuffixList{}})
			if err != nil {
				t.Fatalf("cookiejar.New() failed: %v", err)
			}
			jar.SetCookies(setTarget, accepted)
			getTarget := cookieContextURL(mustURL(t, vector.GetURL))
			if got := cookieHeader(jar.Cookies(getTarget)); got != vector.WantCookie {
				t.Errorf("Set-Cookie %q from %q then Cookie for %q = %q, want %q",
					vector.SetCookie, vector.SetURL, vector.GetURL, got, vector.WantCookie)
			}
		})
	}
}

func TestToughCookieDomainScopeDifferentialCorpus(t *testing.T) {
	corpus := loadToughCookiePairCorpus(t)
	for _, vector := range corpus.DomainScopeVectors {
		t.Run(vector.Name, func(t *testing.T) {
			jar, err := newToughCookieJar()
			if err != nil {
				t.Fatalf("newToughCookieJar() failed: %v", err)
			}
			setTarget := cookieContextURL(mustURL(t, vector.SetURL))
			jar.SetCookies(
				setTarget,
				acceptedResponseCookies(
					setTarget,
					http.Header{"set-cookie": []string{vector.SetCookie}},
					false,
				),
			)
			getTarget := cookieContextURL(mustURL(t, vector.GetURL))
			if got := cookieHeader(jar.Cookies(getTarget)); got != vector.WantCookie {
				t.Errorf("Set-Cookie %q from %q then Cookie for %q = %q, want %q",
					vector.SetCookie, vector.SetURL, vector.GetURL, got, vector.WantCookie)
			}
		})
	}
}

func TestToughCookieRequestValueControlAndTerminatorParity(t *testing.T) {
	for _, tc := range []struct {
		name      string
		setCookie string
		want      string
	}{
		{name: "edge tabs are trimmed", setCookie: "sid=\ta\t; Path=/", want: "sid=a"},
		{name: "internal tab is ignored", setCookie: "sid=a\tb; Path=/"},
		{name: "internal C0 is ignored", setCookie: "sid=a\x01b; Path=/"},
		{name: "name edge tabs are trimmed", setCookie: "\ts id\t=value; Path=/", want: "s id=value"},
		{name: "name internal tab is ignored", setCookie: "s\tid=value; Path=/"},
		{name: "name internal C0 is ignored", setCookie: "s\x01id=value; Path=/"},
		{name: "empty name is ignored", setCookie: "=value; Path=/"},
		{name: "LF terminates", setCookie: "sid=a\nb", want: "sid=a"},
		{name: "CR terminates", setCookie: "sid=a\rb", want: "sid=a"},
		{name: "NUL terminates", setCookie: "sid=a\x00b", want: "sid=a"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			jar, err := cookiejar.New(&cookiejar.Options{PublicSuffixList: collectorPublicSuffixList{}})
			if err != nil {
				t.Fatalf("cookiejar.New() failed: %v", err)
			}
			target := cookieContextURL(mustURL(t, "https://example.test/start"))
			jar.SetCookies(
				target,
				acceptedResponseCookies(
					target,
					http.Header{"set-cookie": []string{tc.setCookie}},
					false,
				),
			)
			if got := cookieHeader(jar.Cookies(target)); got != tc.want {
				t.Errorf("Set-Cookie %q then Cookie = %q, want %q", tc.setCookie, got, tc.want)
			}
		})
	}
}

func toughCookieRedirectResponse(setCookie string) []byte {
	headers := append([]byte("Location: /final\r\nSet-Cookie: "), nodeLatin1(setCookie)...)
	headers = append(headers, "\r\n"...)
	return rawHTTPResponse(http.StatusFound, headers, "")
}

type cookieRedirectProxyExchange struct {
	connect capturedWireRequest
	inner   capturedWireRequest
}

type cookieRedirectProxy struct {
	address      string
	observations <-chan []cookieRedirectProxyExchange
	done         <-chan error
}

func startCookieRedirectProxy(
	t *testing.T,
	certificate tls.Certificate,
	targetTLS [2]bool,
	location string,
	setCookie string,
) cookieRedirectProxy {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen() failed: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	observations := make(chan []cookieRedirectProxyExchange, 1)
	done := make(chan error, 1)
	go func() {
		defer close(observations)
		defer close(done)
		captured := make([]cookieRedirectProxyExchange, 0, 2)
		for attempt := 0; attempt < 2; attempt++ {
			connection, acceptErr := listener.Accept()
			if acceptErr != nil {
				done <- acceptErr
				return
			}
			connect, exchangeErr := readCapturedWireRequest(bufio.NewReader(connection))
			if exchangeErr == nil {
				_, exchangeErr = io.WriteString(connection, "HTTP/1.1 200 Connection Established\r\n\r\n")
			}
			if exchangeErr == nil && targetTLS[attempt] {
				secured := tls.Server(connection, &tls.Config{
					Certificates: []tls.Certificate{certificate},
					MinVersion:   tls.VersionTLS12,
				})
				exchangeErr = secured.Handshake()
				connection = secured
			}
			var inner capturedWireRequest
			if exchangeErr == nil {
				inner, exchangeErr = readCapturedWireRequest(bufio.NewReader(connection))
			}
			if exchangeErr == nil {
				response := rawHTTPResponse(http.StatusOK, nil, "done")
				if attempt == 0 {
					headers := []byte("Location: " + location + "\r\nSet-Cookie: " + setCookie + "\r\n")
					response = rawHTTPResponse(http.StatusFound, headers, "")
				}
				_, exchangeErr = connection.Write(response)
			}
			closeErr := connection.Close()
			if exchangeErr != nil {
				done <- exchangeErr
				return
			}
			if closeErr != nil {
				done <- closeErr
				return
			}
			captured = append(captured, cookieRedirectProxyExchange{connect: connect, inner: inner})
		}
		observations <- captured
		done <- nil
	}()
	return cookieRedirectProxy{
		address:      listener.Addr().String(),
		observations: observations,
		done:         done,
	}
}

func awaitCookieRedirectProxy(
	t *testing.T,
	proxy cookieRedirectProxy,
) []cookieRedirectProxyExchange {
	t.Helper()
	observations := <-proxy.observations
	if err := <-proxy.done; err != nil {
		t.Fatalf("cookie attribute redirect proxy failed: %v", err)
	}
	return observations
}

func TestToughCookieWhitespaceAttributesSurviveProductionRedirect(t *testing.T) {
	certificate, certificateDER := testWireCertificate(t)
	bundle := writeTestCABundle(t, certificateDER)
	for _, tc := range []struct {
		name              string
		startURL          string
		location          string
		setCookie         string
		secondTargetTLS   bool
		wantSecondConnect string
		wantCookie        string
	}{
		{
			name:              "Secure key blocks HTTPS to HTTP leak",
			startURL:          "https://api.example.com/start",
			location:          "http://api.example.com/final",
			setCookie:         "sid=secure; Secure =yes; Path = /",
			wantSecondConnect: "CONNECT api.example.com:80 HTTP/1.1",
		},
		{
			name:              "Domain key reaches sibling host",
			startURL:          "https://api.example.com/start",
			location:          "https://sibling.example.com/final",
			setCookie:         "sid=domain; Domain = example.com; Path = /",
			secondTargetTLS:   true,
			wantSecondConnect: "CONNECT sibling.example.com:443 HTTP/1.1",
			wantCookie:        "sid=domain",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			proxy := startCookieRedirectProxy(
				t,
				certificate,
				[2]bool{true, tc.secondTargetTLS},
				tc.location,
				tc.setCookie,
			)
			proxyURL := "http://" + proxy.address
			transport, err := CreateRestHTTPTransport(collectors.Environment{
				"HTTP_PROXY":         proxyURL,
				"HTTPS_PROXY":        proxyURL,
				"REQUESTS_CA_BUNDLE": bundle,
			}, RestHTTPTransportOptions{})
			if err != nil {
				t.Fatalf("CreateRestHTTPTransport() failed: %v", err)
			}
			t.Cleanup(func() { _ = transport.Close() })
			if _, err := transport.Request(collectors.HTTPRequest{
				Method: http.MethodGet,
				URL:    mustURL(t, tc.startURL),
			}); err != nil {
				t.Fatalf("Request(%q) failed: %v", tc.startURL, err)
			}
			observations := awaitCookieRedirectProxy(t, proxy)
			if len(observations) != 2 {
				t.Fatalf("proxy exchange count = %d, want 2", len(observations))
			}
			if got := observations[0].connect.line; got != "CONNECT api.example.com:443 HTTP/1.1" {
				t.Errorf("first CONNECT line = %q, want %q", got, "CONNECT api.example.com:443 HTTP/1.1")
			}
			if got := observations[1].connect.line; got != tc.wantSecondConnect {
				t.Errorf("second CONNECT line = %q, want %q", got, tc.wantSecondConnect)
			}
			gotCookie := ""
			if values := observations[1].inner.headers["cookie"]; len(values) != 0 {
				gotCookie = values[0]
			}
			if gotCookie != tc.wantCookie {
				t.Errorf("redirect Cookie header = %q, want %q", gotCookie, tc.wantCookie)
			}
		})
	}
}

func TestToughCookieDomainScopeProductionRedirect(t *testing.T) {
	for _, tc := range []struct {
		name              string
		startURL          string
		location          string
		setCookie         string
		wantFirstConnect  string
		wantSecondConnect string
		wantCookie        string
	}{
		{
			name:              "IPv6 Domain attribute survives same-host redirect",
			startURL:          "http://[::1]/start",
			location:          "http://[::1]/final",
			setCookie:         "sid=ipv6; Domain=::1; Path=/",
			wantFirstConnect:  "CONNECT [::1]:80 HTTP/1.1",
			wantSecondConnect: "CONNECT [::1]:80 HTTP/1.1",
			wantCookie:        "sid=ipv6",
		},
		{
			name:              "multi-label trailing dot remains unreachable on same host",
			startURL:          "http://api.example.test./start",
			location:          "http://api.example.test./final",
			setCookie:         "sid=dot; Path=/",
			wantFirstConnect:  "CONNECT api.example.test.:80 HTTP/1.1",
			wantSecondConnect: "CONNECT api.example.test.:80 HTTP/1.1",
		},
		{
			name:              "trailing dot does not widen to no dot",
			startURL:          "http://api.example.test./start",
			location:          "http://api.example.test/final",
			setCookie:         "sid=dot; Path=/",
			wantFirstConnect:  "CONNECT api.example.test.:80 HTTP/1.1",
			wantSecondConnect: "CONNECT api.example.test:80 HTTP/1.1",
		},
		{
			name:              "no dot does not widen to trailing dot",
			startURL:          "http://api.example.test/start",
			location:          "http://api.example.test./final",
			setCookie:         "sid=plain; Path=/",
			wantFirstConnect:  "CONNECT api.example.test:80 HTTP/1.1",
			wantSecondConnect: "CONNECT api.example.test.:80 HTTP/1.1",
		},
		{
			name:              "single-label trailing dot rejects Domain attribute",
			startURL:          "http://invalid./start",
			location:          "http://invalid./final",
			setCookie:         "sid=domain; Domain=invalid; Path=/",
			wantFirstConnect:  "CONNECT invalid.:80 HTTP/1.1",
			wantSecondConnect: "CONNECT invalid.:80 HTTP/1.1",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			proxy := startCookieRedirectProxy(
				t,
				tls.Certificate{},
				[2]bool{},
				tc.location,
				tc.setCookie,
			)
			proxyURL := "http://" + proxy.address
			transport, err := CreateRestHTTPTransport(collectors.Environment{
				"HTTP_PROXY":  proxyURL,
				"HTTPS_PROXY": proxyURL,
			}, RestHTTPTransportOptions{})
			if err != nil {
				t.Fatalf("CreateRestHTTPTransport() failed: %v", err)
			}
			t.Cleanup(func() { _ = transport.Close() })
			if _, err := transport.Request(collectors.HTTPRequest{
				Method: http.MethodGet,
				URL:    mustURL(t, tc.startURL),
			}); err != nil {
				t.Fatalf("Request(%q) failed: %v", tc.startURL, err)
			}
			observations := awaitCookieRedirectProxy(t, proxy)
			if len(observations) != 2 {
				t.Fatalf("proxy exchange count = %d, want 2", len(observations))
			}
			if got := observations[0].connect.line; got != tc.wantFirstConnect {
				t.Errorf("first CONNECT line for %q = %q, want %q", tc.startURL, got, tc.wantFirstConnect)
			}
			if got := observations[1].connect.line; got != tc.wantSecondConnect {
				t.Errorf("second CONNECT line for %q = %q, want %q", tc.location, got, tc.wantSecondConnect)
			}
			gotCookie := ""
			if values := observations[1].inner.headers["cookie"]; len(values) != 0 {
				gotCookie = values[0]
			}
			if gotCookie != tc.wantCookie {
				t.Errorf("redirect Cookie header from %q to %q = %q, want %q",
					tc.startURL, tc.location, gotCookie, tc.wantCookie)
			}
		})
	}
}

func TestToughCookieRequestValuesSurviveProductionRedirectWire(t *testing.T) {
	corpus := loadToughCookiePairCorpus(t)
	for _, vector := range corpus.ValueVectors {
		if !vector.ProductionWire {
			continue
		}
		t.Run(vector.Name, func(t *testing.T) {
			server := startCaptureServer(
				t,
				toughCookieRedirectResponse(vector.SetCookie),
				rawHTTPResponse(http.StatusOK, nil, "done"),
			)
			transport, err := CreateRestHTTPTransport(collectors.Environment{}, RestHTTPTransportOptions{})
			if err != nil {
				t.Fatalf("CreateRestHTTPTransport() failed: %v", err)
			}
			t.Cleanup(func() { _ = transport.Close() })
			if _, err := transport.Request(collectors.HTTPRequest{
				Method: http.MethodGet,
				URL:    mustURL(t, "http://"+server.address+"/start"),
			}); err != nil {
				t.Fatalf("Request(redirect with Set-Cookie %q) failed: %v", vector.SetCookie, err)
			}
			requests := awaitCaptureServer(t, server)
			if len(requests) != 2 {
				t.Fatalf("captured request count = %d, want 2", len(requests))
			}
			got := requests[1].headers["cookie"]
			if len(got) != 1 || !slices.Equal([]byte(got[0]), nodeLatin1(vector.WantCookie)) {
				t.Errorf("redirect Cookie wire bytes = %q, want %q", got, nodeLatin1(vector.WantCookie))
			}
		})
	}
}

func TestToughCookieRequestNamesSurviveProductionRedirectWire(t *testing.T) {
	corpus := loadToughCookiePairCorpus(t)
	for _, vector := range corpus.NameVectors {
		if !vector.ProductionWire {
			continue
		}
		t.Run(vector.Name, func(t *testing.T) {
			server := startCaptureServer(
				t,
				toughCookieRedirectResponse(vector.SetCookie),
				rawHTTPResponse(http.StatusOK, nil, "done"),
			)
			transport, err := CreateRestHTTPTransport(collectors.Environment{}, RestHTTPTransportOptions{})
			if err != nil {
				t.Fatalf("CreateRestHTTPTransport() failed: %v", err)
			}
			t.Cleanup(func() { _ = transport.Close() })
			if _, err := transport.Request(collectors.HTTPRequest{
				Method: http.MethodGet,
				URL:    mustURL(t, "http://"+server.address+"/start"),
			}); err != nil {
				t.Fatalf("Request(redirect with Set-Cookie %q) failed: %v", vector.SetCookie, err)
			}
			requests := awaitCaptureServer(t, server)
			if len(requests) != 2 {
				t.Fatalf("captured request count = %d, want 2", len(requests))
			}
			got := requests[1].headers["cookie"]
			if len(got) != 1 || !slices.Equal([]byte(got[0]), nodeLatin1(vector.WantCookie)) {
				t.Errorf("redirect Cookie wire bytes = %q, want %q", got, nodeLatin1(vector.WantCookie))
			}
		})
	}
}

func TestToughCookieProductionRedirectPreservesUTF8LookingBytes(t *testing.T) {
	for _, tc := range []struct {
		name       string
		setCookie  string
		wantCookie string
	}{
		{name: "value bytes", setCookie: "sid=雪; Path=/", wantCookie: "sid=雪"},
		{name: "name bytes", setCookie: "雪=value; Path=/", wantCookie: "雪=value"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			headers := append([]byte("Location: /final\r\nSet-Cookie: "), []byte(tc.setCookie)...)
			headers = append(headers, "\r\n"...)
			server := startCaptureServer(
				t,
				rawHTTPResponse(http.StatusFound, headers, ""),
				rawHTTPResponse(http.StatusOK, nil, "done"),
			)
			transport, err := CreateRestHTTPTransport(collectors.Environment{}, RestHTTPTransportOptions{})
			if err != nil {
				t.Fatalf("CreateRestHTTPTransport() failed: %v", err)
			}
			t.Cleanup(func() { _ = transport.Close() })
			if _, err := transport.Request(collectors.HTTPRequest{
				Method: http.MethodGet,
				URL:    mustURL(t, "http://"+server.address+"/start"),
			}); err != nil {
				t.Fatalf("Request(redirect with UTF-8-looking Set-Cookie %q) failed: %v", tc.setCookie, err)
			}
			requests := awaitCaptureServer(t, server)
			if len(requests) != 2 {
				t.Fatalf("captured request count = %d, want 2", len(requests))
			}
			got := requests[1].headers["cookie"]
			if len(got) != 1 || !slices.Equal([]byte(got[0]), []byte(tc.wantCookie)) {
				t.Errorf("redirect Cookie wire bytes = %q, want %q", got, []byte(tc.wantCookie))
			}
		})
	}
}

func TestPinnedPublicSuffixTrieMatchesTLDTS748Oracle(t *testing.T) {
	// Oracle command (repository-pinned tldts 7.4.8):
	// node --input-type=module -e 'import {getPublicSuffix} from "tldts";
	// for (const d of process.argv.slice(1)) console.log(d, getPublicSuffix(d,
	// {allowIcannDomains:true,allowPrivateDomains:true}))' -- <domains...>
	cases := []struct {
		domain string
		want   string
	}{
		{"www.example.co.uk", "co.uk"},
		{"a.herokuapp.com", "herokuapp.com"},
		{"a.b.ck", "b.ck"},
		{"www.ck", "ck"},
		{"a.city.kawasaki.jp", "kawasaki.jp"},
		{"a.example.invalidtld", "invalidtld"},
		{"a.github.io", "github.io"},
	}
	list := collectorPublicSuffixList{}
	for _, tc := range cases {
		t.Run(tc.domain, func(t *testing.T) {
			if got := list.PublicSuffix(tc.domain); got != tc.want {
				t.Errorf("PublicSuffix(%q) = %q, want %q", tc.domain, got, tc.want)
			}
		})
	}
}

func TestPrivateSuffixDomainCookieCannotCrossTenants(t *testing.T) {
	jar, err := cookiejar.New(&cookiejar.Options{PublicSuffixList: collectorPublicSuffixList{}})
	if err != nil {
		t.Fatal(err)
	}
	first := mustURL(t, "https://a.herokuapp.com/")
	second := mustURL(t, "https://b.herokuapp.com/")
	jar.SetCookies(first, []*http.Cookie{{
		Name:   "tenant",
		Value:  "must-not-cross",
		Domain: "herokuapp.com",
		Path:   "/",
		Secure: true,
	}})
	if cookies := jar.Cookies(second); len(cookies) != 0 {
		t.Fatalf("private-suffix cookie crossed tenants: %v", cookies)
	}
}

func TestToughCookiePrefixSecuritySilentParity(t *testing.T) {
	target := mustURL(t, "https://a.example.com/root")
	cases := []struct {
		name   string
		cookie *http.Cookie
		want   bool
	}{
		{"ordinary", &http.Cookie{Name: "ordinary", Value: "x"}, true},
		{"secure missing Secure", &http.Cookie{Name: "__Secure-bad", Value: "x", Path: "/"}, false},
		{"secure valid", &http.Cookie{Name: "__Secure-good", Value: "x", Path: "/", Secure: true}, true},
		{"host has Domain", &http.Cookie{Name: "__Host-domain", Value: "x", Domain: "example.com", Path: "/", Secure: true}, false},
		{"host wrong Path", &http.Cookie{Name: "__Host-path", Value: "x", Path: "/root", Secure: true}, false},
		{"host explicit root", &http.Cookie{Name: "__Host-good", Value: "x", Path: "/", Secure: true}, true},
		{"host default root", &http.Cookie{Name: "__Host-default", Value: "x", Secure: true}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := cookiePrefixAccepted(target, tc.cookie); got != tc.want {
				t.Errorf("cookiePrefixAccepted(%#v) = %t, want %t", tc.cookie, got, tc.want)
			}
		})
	}
	deeper := mustURL(t, "https://a.example.com/root/deeper")
	if cookiePrefixAccepted(deeper, &http.Cookie{Name: "__Host-default", Value: "x", Secure: true}) {
		t.Error("__Host- cookie with an implicit non-root default path was accepted")
	}
}

func TestToughCookieDomainAttributeParity(t *testing.T) {
	cases := []struct {
		name   string
		domain string
		want   bool
	}{
		{name: "host_only", domain: "", want: true},
		{name: "registrable", domain: "example.com", want: true},
		{name: "private_suffix", domain: "herokuapp.com", want: false},
		{name: "ICANN_suffix", domain: "com", want: false},
		{name: "bare_test", domain: "test", want: false},
		{name: "bare_localhost", domain: "localhost", want: true},
		{name: "bare_invalid", domain: "invalid", want: true},
		{name: "IPv4", domain: "127.0.0.1", want: false},
		{name: "IPv6", domain: "::1", want: true},
		{name: "sub_localhost", domain: "sub.localhost", want: true},
		{name: "api_test", domain: "api.test", want: true},
		{name: "foo_local", domain: "foo.local", want: true},
		{name: "x_example", domain: "x.example", want: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := cookieDomainAccepted(&http.Cookie{Domain: tc.domain}); got != tc.want {
				t.Errorf("cookieDomainAccepted(Domain=%q) = %t, want %t", tc.domain, got, tc.want)
			}
		})
	}
}

func TestToughCookieSpecialUseMultiLabelDomainScope(t *testing.T) {
	// Pinned tough-cookie 6.0.2 with allowSpecialUseDomain=true and
	// rejectPublicSuffixes=true accepts each explicit Domain attribute and
	// returns it for a child host. This guards the non-obvious distinction
	// between its getPublicSuffix result and a PSL rejection comparison.
	for _, tc := range []struct {
		name   string
		domain string
	}{
		{name: "sub_localhost", domain: "sub.localhost"},
		{name: "api_test", domain: "api.test"},
		{name: "foo_local", domain: "foo.local"},
		{name: "x_example", domain: "x.example"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			jar, err := newToughCookieJar()
			if err != nil {
				t.Fatalf("newToughCookieJar() failed: %v", err)
			}
			setTarget := mustURL(t, "https://origin."+tc.domain+"/start")
			setCookie := "sid=value; Domain=" + tc.domain + "; Path=/"
			jar.SetCookies(setTarget, acceptedResponseCookies(
				setTarget,
				http.Header{"set-cookie": []string{setCookie}},
				false,
			))
			getTarget := mustURL(t, "https://child."+tc.domain+"/final")
			if got := cookieHeader(jar.Cookies(getTarget)); got != "sid=value" {
				t.Errorf("Set-Cookie %q from %q then Cookie for %q = %q, want %q",
					setCookie, setTarget, getTarget, got, "sid=value")
			}
		})
	}
}

func TestToughCookieBareSpecialUseDomainStoreBoundary(t *testing.T) {
	for _, tc := range []struct {
		name   string
		domain string
	}{
		{name: "bare_localhost_parent_boundary", domain: "localhost"},
		{name: "bare_invalid_parent_boundary", domain: "invalid"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			jar, err := newToughCookieJar()
			if err != nil {
				t.Fatalf("newToughCookieJar() failed: %v", err)
			}
			setTarget := mustURL(t, "https://origin."+tc.domain+":8443/start")
			set := func(value string) {
				t.Helper()
				setCookie := "sid=" + value + "; Domain=" + tc.domain + "; Path=/"
				jar.SetCookies(setTarget, acceptedResponseCookies(
					setTarget,
					http.Header{"set-cookie": []string{setCookie}},
					false,
				))
			}
			check := func(label, wantParent string) {
				t.Helper()
				for _, target := range []struct {
					name string
					url  string
					want string
				}{
					{name: "parent", url: "https://" + tc.domain + ":8443/final", want: wantParent},
					{name: "origin", url: "https://origin." + tc.domain + ":8443/final"},
					{name: "child", url: "https://child." + tc.domain + ":8443/final"},
				} {
					getTarget := mustURL(t, target.url)
					if got := cookieHeader(jar.Cookies(getTarget)); got != target.want {
						t.Errorf("%s Cookie for %s %q = %q, want %q",
							label, target.name, getTarget, got, target.want)
					}
				}
			}

			set("first")
			check("initial", "sid=first")
			set("second")
			check("overwrite", "sid=second")
			setCookie := "sid=gone; Domain=" + tc.domain + "; Path=/; Max-Age=0"
			jar.SetCookies(setTarget, acceptedResponseCookies(
				setTarget,
				http.Header{"set-cookie": []string{setCookie}},
				false,
			))
			check("deletion", "")
		})
	}
}

type blockingCookieStorage struct {
	firstSet      chan struct{}
	release       chan struct{}
	cookiesCalled chan struct{}

	mu       sync.Mutex
	setCalls int
}

func (storage *blockingCookieStorage) SetCookies(*url.URL, []*http.Cookie) {
	storage.mu.Lock()
	storage.setCalls++
	call := storage.setCalls
	storage.mu.Unlock()
	if call == 1 {
		close(storage.firstSet)
		<-storage.release
	}
}

func (storage *blockingCookieStorage) Cookies(*url.URL) []*http.Cookie {
	select {
	case storage.cookiesCalled <- struct{}{}:
	default:
	}
	return []*http.Cookie{{Name: "visible", Value: "after-update"}}
}

func TestToughCookieJarSerializesResponseCookieUpdates(t *testing.T) {
	storage := &blockingCookieStorage{
		firstSet:      make(chan struct{}),
		release:       make(chan struct{}),
		cookiesCalled: make(chan struct{}, 1),
	}
	var releaseOnce sync.Once
	release := func() {
		releaseOnce.Do(func() { close(storage.release) })
	}
	t.Cleanup(release)
	jar := &toughCookieJar{
		regular:             storage,
		trailingSingleLabel: storage,
	}
	target := mustURL(t, "https://example.com/")
	setDone := make(chan struct{})
	go func() {
		defer close(setDone)
		jar.SetCookies(target, []*http.Cookie{
			{Name: "first", Value: "1"},
			{Name: "second", Value: "2"},
		})
	}()
	<-storage.firstSet
	if jar.mu.TryLock() {
		jar.mu.Unlock()
		t.Error("jar mutex was not held across the complete response update")
	}

	readDone := make(chan []*http.Cookie, 1)
	go func() {
		readDone <- jar.Cookies(target)
	}()
	select {
	case <-storage.cookiesCalled:
		t.Error("Cookies reached storage during a response update")
	case <-time.After(50 * time.Millisecond):
	}
	release()
	select {
	case <-setDone:
	case <-time.After(time.Second):
		t.Fatal("SetCookies did not finish after the storage barrier opened")
	}
	select {
	case cookies := <-readDone:
		if got := cookieHeader(cookies); got != "visible=after-update" {
			t.Errorf("Cookies() after response update = %q, want %q", got, "visible=after-update")
		}
	case <-time.After(time.Second):
		t.Fatal("Cookies did not finish after the response update")
	}
	storage.mu.Lock()
	setCalls := storage.setCalls
	storage.mu.Unlock()
	if setCalls != 2 {
		t.Errorf("storage SetCookies call count = %d, want 2", setCalls)
	}
}

func TestToughCookieDomainDotBecomesHostOnly(t *testing.T) {
	target := mustURL(t, "https://a.example.com/path")
	header := "dot=value; Domain=.; Path=/"
	accepted := acceptedResponseCookies(target, http.Header{"set-cookie": []string{header}}, false)
	if len(accepted) != 1 || accepted[0].Domain != "" {
		t.Fatalf("accepted Domain=. cookies = %#v, want one host-only cookie", accepted)
	}
	if header != "dot=value; Domain=.; Path=/" {
		t.Errorf("acceptedResponseCookies mutated raw header to %q", header)
	}
}

func TestToughCookieDateDifferentialCorpus(t *testing.T) {
	corpus := loadToughCookieExpiryCorpus(t)
	for _, vector := range corpus.DateVectors {
		t.Run(vector.Name, func(t *testing.T) {
			got, ok := parseToughCookieDate(vector.Input)
			if vector.WantUTC == nil {
				if ok {
					t.Errorf("parseToughCookieDate(%q) = %s, true, want zero, false", vector.Input, got.Format(time.RFC3339))
				}
				return
			}
			if !ok {
				t.Errorf("parseToughCookieDate(%q) = zero, false, want %s, true", vector.Input, *vector.WantUTC)
				return
			}
			if formatted := got.Format(time.RFC3339); formatted != *vector.WantUTC {
				t.Errorf("parseToughCookieDate(%q) = %s, want %s", vector.Input, formatted, *vector.WantUTC)
			}
		})
	}
}

func TestToughCookieMaxAgeDifferentialCorpus(t *testing.T) {
	corpus := loadToughCookieExpiryCorpus(t)
	for _, vector := range corpus.MaxAgeVectors {
		t.Run(vector.Name, func(t *testing.T) {
			value, ok := parseToughCookieMaxAge(vector.Input)
			got := "ignore"
			if ok && value <= 0 {
				got = "delete"
			} else if ok {
				got = "keep"
			}
			if got != vector.Want {
				t.Errorf("parseToughCookieMaxAge(%q) outcome = %q (value %d), want %q", vector.Input, got, value, vector.Want)
			}
		})
	}
}

func TestRawSetCookieExpiryDifferentialCorpus(t *testing.T) {
	corpus := loadToughCookieExpiryCorpus(t)
	for _, vector := range corpus.JarVectors {
		t.Run(vector.Name, func(t *testing.T) {
			jar, err := cookiejar.New(&cookiejar.Options{PublicSuffixList: collectorPublicSuffixList{}})
			if err != nil {
				t.Fatalf("cookiejar.New() failed: %v", err)
			}
			setTarget := cookieContextURL(mustURL(t, vector.SetURL))
			setRawCookie := func(header string) {
				if header == "" {
					return
				}
				cookies := acceptedResponseCookies(setTarget, http.Header{"set-cookie": []string{header}}, false)
				jar.SetCookies(setTarget, cookies)
			}
			setRawCookie(vector.SeedSetCookie)
			setRawCookie(vector.SetCookie)
			getTarget := cookieContextURL(mustURL(t, vector.GetURL))
			if got := cookieHeader(jar.Cookies(getTarget)); got != vector.WantCookie {
				t.Errorf("raw Set-Cookie %q from %q then Cookie for %q = %q, want %q",
					vector.SetCookie, vector.SetURL, vector.GetURL, got, vector.WantCookie)
			}
		})
	}
}

func TestRawSetCookieMaxAgePersistsOnRedirect(t *testing.T) {
	var observed []string
	transport, err := CreateRestHTTPTransport(collectors.Environment{}, RestHTTPTransportOptions{
		RoundTripper: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			observed = append(observed, request.Header.Get("Cookie"))
			if len(observed) == 1 {
				return response("redirect", http.StatusFound, http.Header{
					"Location":   []string{"/final"},
					"Set-Cookie": []string{"sid=redirect; Path=/; Max-Age=03600; Expires=Thu, 01 Jan 1970 00:00:00 GMT"},
				}), nil
			}
			return response("done", http.StatusOK, nil), nil
		}),
	})
	if err != nil {
		t.Fatalf("CreateRestHTTPTransport() failed: %v", err)
	}
	t.Cleanup(func() { _ = transport.Close() })
	_, err = transport.Request(collectors.HTTPRequest{
		Method: "GET",
		URL:    mustURL(t, "https://example.test/start"),
	})
	if err != nil {
		t.Fatalf("Request(redirect with leading-zero Max-Age) failed: %v", err)
	}
	if want := []string{"", "sid=redirect"}; !slices.Equal(observed, want) {
		t.Errorf("redirect Cookie headers after leading-zero Max-Age overriding past Expires = %v, want %v", observed, want)
	}
}

func TestRawSetCookieMaxAgeZeroDeletesOnRedirect(t *testing.T) {
	var observed []string
	transport, err := CreateRestHTTPTransport(collectors.Environment{}, RestHTTPTransportOptions{
		RoundTripper: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			observed = append(observed, request.Header.Get("Cookie"))
			switch len(observed) {
			case 1:
				return response("seeded", http.StatusOK, http.Header{
					"Set-Cookie": []string{"sid=seed; Path=/"},
				}), nil
			case 2:
				return response("redirect", http.StatusFound, http.Header{
					"Location":   []string{"/final"},
					"Set-Cookie": []string{"sid=deleted; Path=/; Max-Age=0; Expires=Wed, 01 Jan 2099 00:00:00 GMT"},
				}), nil
			default:
				return response("done", http.StatusOK, nil), nil
			}
		}),
	})
	if err != nil {
		t.Fatalf("CreateRestHTTPTransport() failed: %v", err)
	}
	t.Cleanup(func() { _ = transport.Close() })
	for _, target := range []string{"https://example.test/seed", "https://example.test/start"} {
		if _, err := transport.Request(collectors.HTTPRequest{Method: "GET", URL: mustURL(t, target)}); err != nil {
			t.Fatalf("Request(%q) failed: %v", target, err)
		}
	}
	if want := []string{"", "sid=seed", ""}; !slices.Equal(observed, want) {
		t.Errorf("Cookie headers after Max-Age=0 overriding future Expires on redirect = %v, want %v", observed, want)
	}
}

func TestCookieContextMatchesToughCookieDecodeURIPath(t *testing.T) {
	jar, err := cookiejar.New(&cookiejar.Options{PublicSuffixList: collectorPublicSuffixList{}})
	if err != nil {
		t.Fatal(err)
	}
	origin := mustURL(t, "https://api.example.test/")
	jar.SetCookies(cookieContextURL(origin), []*http.Cookie{
		{Name: "unreserved", Value: "no", Path: "/a%41", Secure: true},
		{Name: "reserved", Value: "yes", Path: "/a%2F", Secure: true},
		{Name: "malformed", Value: "yes", Path: "/a/%zz", Secure: true},
	})
	for _, tc := range []struct {
		name string
		url  *url.URL
		want []string
	}{
		{
			name: "unreserved escape decoded",
			url:  &url.URL{Scheme: "https", Host: "api.example.test", Path: "/aA", RawPath: "/a%41"},
			want: nil,
		},
		{
			name: "reserved slash remains escaped",
			url:  &url.URL{Scheme: "https", Host: "api.example.test", Path: "/a/", RawPath: "/a%2F"},
			want: []string{"reserved=yes"},
		},
		{
			name: "malformed escape leaves whole path unchanged",
			url:  &url.URL{Scheme: "https", Host: "api.example.test", Path: "/a/%zz", RawPath: "/a/%zz"},
			want: []string{"malformed=yes"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cookies := jar.Cookies(cookieContextURL(tc.url))
			got := make([]string, 0, len(cookies))
			for _, cookie := range cookies {
				got = append(got, cookie.String())
			}
			if !slices.Equal(got, tc.want) {
				t.Errorf("cookies = %v, want %v", got, tc.want)
			}
		})
	}
}
