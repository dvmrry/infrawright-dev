package resthttp

import (
	"errors"
	"slices"
	"testing"

	"github.com/dvmrry/infrawright-dev/go/internal/collectors"
	"github.com/dvmrry/infrawright-dev/go/internal/procerr"
)

func TestSnapshotRestProxyEnvironmentLowercasePrecedence(t *testing.T) {
	cases := []struct {
		name string
		env  collectors.Environment
		want RestProxyEnvironment
	}{
		{name: "empty", env: collectors.Environment{}, want: RestProxyEnvironment{}},
		{
			name: "lowercase including explicit empty",
			env: collectors.Environment{
				"HTTP_PROXY":  "http://upper.invalid:8080",
				"http_proxy":  "http://lower.invalid:8081",
				"HTTPS_PROXY": "http://https-upper.invalid:8082",
				"https_proxy": "",
				"NO_PROXY":    "upper.invalid",
				"no_proxy":    "lower.invalid",
			},
			want: RestProxyEnvironment{
				HTTPProxy:  "http://lower.invalid:8081/",
				HTTPSProxy: "",
				NoProxy:    "lower.invalid",
			},
		},
		{
			name: "HTTP proxy does not become HTTPS proxy",
			env:  collectors.Environment{"HTTP_PROXY": "http://proxy.invalid:8080"},
			want: RestProxyEnvironment{HTTPProxy: "http://proxy.invalid:8080/"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := SnapshotRestProxyEnvironment(tc.env)
			if err != nil {
				t.Fatalf("SnapshotRestProxyEnvironment(%v) failed: %v", tc.env, err)
			}
			if got != tc.want {
				t.Errorf("SnapshotRestProxyEnvironment(%v) = %#v, want %#v", tc.env, got, tc.want)
			}
		})
	}
}

func TestSnapshotRestProxyEnvironmentRejectsInvalidProxy(t *testing.T) {
	cases := []struct {
		value   string
		message string
	}{
		{"not a URL", "HTTP proxy configuration must be an http:// or https:// URL"},
		{"http://proxy.example:65536/", "HTTP proxy configuration must be an http:// or https:// URL"},
		{"ftp://proxy.example/", "HTTP proxy configuration must be an http:// or https:// host URL"},
		{"http://proxy.example/path", "HTTP proxy configuration must be an http:// or https:// host URL"},
		{"http://proxy.example/?query=value", "HTTP proxy configuration must be an http:// or https:// host URL"},
		{`http://proxy.example/?x=\foo`, "HTTP proxy configuration must be an http:// or https:// host URL"},
		{"http://09/", "HTTP proxy configuration must be an http:// or https:// URL"},
		{"http://08/", "HTTP proxy configuration must be an http:// or https:// URL"},
		{"http://1..2/", "HTTP proxy configuration must be an http:// or https:// URL"},
		{"http://[%3A%3A1]/", "HTTP proxy configuration must be an http:// or https:// URL"},
		{"http://[::%31]/", "HTTP proxy configuration must be an http:// or https:// URL"},
	}
	for _, tc := range cases {
		_, err := SnapshotRestProxyEnvironment(collectors.Environment{"HTTP_PROXY": tc.value})
		failure := requireProcessFailure(t, err, "INVALID_REST_PROXY")
		if failure.Message != tc.message {
			t.Errorf("proxy %q failure message = %q, want %q", tc.value, failure.Message, tc.message)
		}
	}
}

func TestSnapshotRestProxyEnvironmentUsesWHATWGHostCanonicalization(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"http:Proxy.Example:80", "http://proxy.example/"},
		{"https:/Proxy.Example:443", "https://proxy.example/"},
		{"http://USER:pass@EXAMPLE.COM:80", "http://USER:pass@example.com/"},
		{"http://@proxy.example/", "http://proxy.example/"},
		{"http://:@proxy.example/", "http://proxy.example/"},
		{"http://user@proxy.example/", "http://user@proxy.example/"},
		{"http://user:@proxy.example/", "http://user@proxy.example/"},
		{"http://:pass@proxy.example/", "http://:pass@proxy.example/"},
		{"http://u!;=:p!;=@proxy.example/", "http://u!%3B%3D:p!%3B%3D@proxy.example/"},
		{"http://%41:%42@proxy.example/", "http://%41:%42@proxy.example/"},
		{"http://%zz:pass@proxy.example/", "http://%zz:pass@proxy.example/"},
		{"http://u@foo:p@proxy.example/", "http://u%40foo:p@proxy.example/"},
		{"http://proxy.example/?", "http://proxy.example/?"},
		{"http://proxy.example/#", "http://proxy.example/#"},
		{"http://bücher.example:8080", "http://xn--bcher-kva.example:8080/"},
		{"http://%65xample.com", "http://example.com/"},
		{"http://%30x7f.0.0.1", "http://127.0.0.1/"},
		{"http://b%C3%BCcher.example", "http://xn--bcher-kva.example/"},
		{"http://127.1:8080", "http://127.0.0.1:8080/"},
		{"http://proxy.example/a/..", "http://proxy.example/"},
		{"http://proxy.example/%2e%2e", "http://proxy.example/"},
		{"http://proxy.example:00080", "http://proxy.example/"},
		{" \thttp://Proxy.Example:80\r\n", "http://proxy.example/"},
		{"http:\\\\Proxy.Example\\", "http://proxy.example/"},
		{"http://ＥＸＡＭＰＬＥ.com/", "http://example.com/"},
		{"http://1.2.3../", "http://1.2.3../"},
	}
	for _, tc := range cases {
		got, err := SnapshotRestProxyEnvironment(collectors.Environment{"HTTP_PROXY": tc.input})
		if err != nil {
			t.Fatalf("SnapshotRestProxyEnvironment(%q) failed: %v", tc.input, err)
		}
		if got.HTTPProxy != tc.want {
			t.Errorf("SnapshotRestProxyEnvironment(%q).HTTPProxy = %q, want %q", tc.input, got.HTTPProxy, tc.want)
		}
	}
}

func TestSnapshotRestProxyEnvironmentRejectsWHATWGInvalidIPv6Zone(t *testing.T) {
	_, err := SnapshotRestProxyEnvironment(collectors.Environment{
		"HTTP_PROXY": "http://[fe80::1%25eth0]/",
	})
	failure := requireProcessFailure(t, err, "INVALID_REST_PROXY")
	if failure.Message != "HTTP proxy configuration must be an http:// or https:// URL" {
		t.Errorf("IPv6 zone failure = %q", failure.Message)
	}
}

func TestProxySelectorMatchesUndiciNoProxyRules(t *testing.T) {
	selector, err := newProxySelector(RestProxyEnvironment{
		HTTPProxy:  "http://proxy.invalid:8080/",
		HTTPSProxy: "http://secure-proxy.invalid:8443/",
		NoProxy:    ".example.test,port.test:8443 *.suffix.test",
	})
	if err != nil {
		t.Fatalf("newProxySelector() failed: %v", err)
	}
	cases := []struct {
		url       string
		wantProxy string
	}{
		{"http://example.test/path", ""},
		{"https://child.example.test/path", ""},
		{"https://port.test:8443/path", ""},
		{"https://port.test:443/path", "http://secure-proxy.invalid:8443/"},
		{"http://other.test/path", "http://proxy.invalid:8080/"},
		{"https://other.test/path", "http://secure-proxy.invalid:8443/"},
		{"https://deep.suffix.test/path", ""},
	}
	for _, tc := range cases {
		got, err := selector.proxyURL(mustURL(t, tc.url))
		if err != nil {
			t.Fatalf("proxyURL(%q) failed: %v", tc.url, err)
		}
		gotString := ""
		if got != nil {
			gotString = got.String()
		}
		if gotString != tc.wantProxy {
			t.Errorf("proxyURL(%q) = %q, want %q", tc.url, gotString, tc.wantProxy)
		}
	}

	wildcard, err := newProxySelector(RestProxyEnvironment{
		HTTPProxy: "http://proxy.invalid:8080/",
		NoProxy:   "*",
	})
	if err != nil {
		t.Fatalf("newProxySelector(wildcard) failed: %v", err)
	}
	got, err := wildcard.proxyURL(mustURL(t, "http://anything.invalid/"))
	if err != nil || got != nil {
		t.Errorf("wildcard proxyURL = %v, %v; want nil, nil", got, err)
	}
}

func TestProxySelectorStripsOnlyOneNoProxyPrefix(t *testing.T) {
	selector, err := newProxySelector(RestProxyEnvironment{
		HTTPProxy: "http://proxy.example/",
		NoProxy:   "*..example.test",
	})
	if err != nil {
		t.Fatal(err)
	}
	selected, err := selector.proxyURL(mustURL(t, "http://api.example.test/"))
	if err != nil {
		t.Fatal(err)
	}
	if selected == nil {
		t.Fatal("proxyURL returned direct; Undici retains one leading dot after stripping '*.'")
	}
}

func TestNoProxyUsesECMAScriptWhitespaceSet(t *testing.T) {
	got := parseNoProxy("first.test\ufeffsecond.test\u0085third.test")
	want := []noProxyEntry{{hostname: "first.test"}, {hostname: "second.test\u0085third.test"}}
	if !slices.Equal(got, want) {
		t.Errorf("parseNoProxy ECMAScript whitespace = %#v, want %#v", got, want)
	}
}

func TestProxyAuthenticationRequiresBothCredentialsLikeUndici(t *testing.T) {
	for _, value := range []string{"http://user@proxy.example/", "http://:pass@proxy.example/"} {
		selector, err := newProxySelector(RestProxyEnvironment{HTTPProxy: value})
		if err != nil {
			t.Fatalf("newProxySelector(%q) failed: %v", value, err)
		}
		selected, err := selector.proxyURL(mustURL(t, "http://target.example/"))
		if err != nil {
			t.Fatalf("proxyURL with %q failed: %v", value, err)
		}
		if selected.User != nil {
			t.Errorf("proxyURL with incomplete credentials %q retained User=%v", value, selected.User)
		}
	}
	selector, err := newProxySelector(RestProxyEnvironment{HTTPProxy: "http://user:pass@proxy.example/"})
	if err != nil {
		t.Fatal(err)
	}
	selected, err := selector.proxyURL(mustURL(t, "http://target.example/"))
	if err != nil || selected.User == nil {
		t.Fatalf("proxyURL with complete credentials = %v, %v", selected, err)
	}
}

func TestProxyAuthenticationRejectsMalformedDecodedCredentialsLikeUndici(t *testing.T) {
	for _, value := range []string{
		"http://%zz:pass@proxy.example/",
		"http://user:%zz@proxy.example/",
		"http://%FF:pass@proxy.example/",
		"http://user:%FF@proxy.example/",
	} {
		selector, err := newProxySelector(RestProxyEnvironment{HTTPProxy: value})
		if selector != nil {
			t.Errorf("newProxySelector(%q) = %v, want nil", value, selector)
		}
		if err == nil || err.Error() != "URI malformed" {
			t.Errorf("newProxySelector(%q) error = %v, want raw URI malformed", value, err)
		}
		var failure *procerr.ProcessFailure
		if errors.As(err, &failure) {
			t.Errorf("newProxySelector(%q) error = %#v, want raw non-ProcessFailure", value, failure)
		}
	}
	for _, value := range []string{
		"http://%zz@proxy.example/",
		"http://:%FF@proxy.example/",
		"http://%25:pass@proxy.example/",
		"http://%C3%A9:pass@proxy.example/",
	} {
		if _, err := newProxySelector(RestProxyEnvironment{HTTPProxy: value}); err != nil {
			t.Errorf("newProxySelector(%q) failed: %v", value, err)
		}
	}
}

func TestHTTPProxyAloneLeavesHTTPSDirect(t *testing.T) {
	snapshot, err := SnapshotRestProxyEnvironment(collectors.Environment{
		"HTTP_PROXY": "http://proxy.invalid:8080",
	})
	if err != nil {
		t.Fatalf("SnapshotRestProxyEnvironment() failed: %v", err)
	}
	selector, err := newProxySelector(snapshot)
	if err != nil {
		t.Fatalf("newProxySelector() failed: %v", err)
	}
	httpProxy, err := selector.proxyURL(mustURL(t, "http://api.example.test/data"))
	if err != nil {
		t.Fatalf("HTTP proxyURL failed: %v", err)
	}
	if got := httpProxy.String(); got != "http://proxy.invalid:8080/" {
		t.Errorf("HTTP proxy = %q, want %q", got, "http://proxy.invalid:8080/")
	}
	httpsProxy, err := selector.proxyURL(mustURL(t, "https://api.example.test/data"))
	if err != nil {
		t.Fatalf("HTTPS proxyURL failed: %v", err)
	}
	if httpsProxy != nil {
		t.Errorf("HTTPS proxy = %v, want direct", httpsProxy)
	}
}
