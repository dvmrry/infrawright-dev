package httptransport

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"

	"github.com/dvmrry/infrawright-dev/go/internal/collectors"
)

// proxyEnvChildMarker, when set in the environment, tells this test binary
// to run as a subprocess helper instead of the normal `go test` suite: see
// TestHTTPProxySelectedFromRealProcessEnvironment's doc comment for why a
// subprocess is required at all.
const proxyEnvChildMarker = "HTTPTRANSPORT_PROXY_ENV_CHILD"

func TestMain(m *testing.M) {
	if os.Getenv(proxyEnvChildMarker) == "1" {
		runProxyEnvChild()
		return
	}
	os.Exit(m.Run())
}

// runProxyEnvChild performs one real Request through New's default
// (RoundTripper == nil) transport and reports success/failure on
// stdout/stderr plus a process exit code. It never touches the `testing`
// package: TestMain intercepts before m.Run(), so no -test.* flag
// handling is required.
func runProxyEnvChild() {
	target := os.Getenv("HTTPTRANSPORT_PROXY_ENV_TARGET")
	transport, err := New(collectors.Environment{}, Options{})
	if err != nil {
		fmt.Fprintf(os.Stderr, "New() failed: %v\n", err)
		os.Exit(1)
	}
	defer func() { _ = transport.Close() }()
	result, err := transport.Request(collectors.HTTPRequest{Method: "GET", URL: mustParseChildURL(target)})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Request(%q) failed: %v\n", target, err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stdout, "status=%d body=%s\n", result.Status, result.Body)
	os.Exit(0)
}

func mustParseChildURL(value string) *url.URL {
	parsed, err := url.Parse(value)
	if err != nil {
		fmt.Fprintf(os.Stderr, "url.Parse(%q) failed: %v\n", value, err)
		os.Exit(1)
	}
	return parsed
}

// capturingProxy is a plain (non-TLS) HTTP proxy: net/http.Transport sends
// a request for an http:// target to a configured proxy verbatim (in
// absolute-URI form), so no CONNECT tunneling is needed to observe that
// proxying actually happened.
type capturingProxy struct {
	server *httptest.Server

	mu       sync.Mutex
	requests []string
}

func startCapturingProxy(t *testing.T) *capturingProxy {
	t.Helper()
	proxy := &capturingProxy{}
	proxy.server = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		proxy.mu.Lock()
		proxy.requests = append(proxy.requests, request.Method+" "+request.URL.String())
		proxy.mu.Unlock()
		writer.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(writer, "via-proxy")
	}))
	t.Cleanup(proxy.server.Close)
	return proxy
}

func (p *capturingProxy) observed() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.requests...)
}

// TestHTTPProxySelectedFromRealProcessEnvironment preserves the "proxy
// selected from env" behavior New documents: net/http.ProxyFromEnvironment
// reads HTTP_PROXY (and friends) from the real process environment, not
// from the collectors.Environment map New is handed.
//
// That function's env snapshot is cached process-wide behind a
// sync.Once the first time any *http.Transport actually resolves a
// proxy (see $GOROOT/src/net/http/transport.go's envProxyFunc) with no
// public reset -- so t.Setenv followed by an in-process Request would
// only ever reliably observe the very first HTTP_PROXY this test binary
// happened to see, which makes an in-process test of this behavior
// nondeterministic. Spawning this same test binary as a subprocess (via
// TestMain's helper-process pattern, mirroring how Go's own standard
// library tests this exact stdlib gotcha) gives each case a fresh cache.
func TestHTTPProxySelectedFromRealProcessEnvironment(t *testing.T) {
	proxy := startCapturingProxy(t)
	proxyAddress := strings.TrimPrefix(proxy.server.URL, "http://")

	cmd := exec.Command(os.Args[0])
	cmd.Env = append(os.Environ(),
		proxyEnvChildMarker+"=1",
		"HTTP_PROXY=http://"+proxyAddress,
		"NO_PROXY=",
		"HTTPTRANSPORT_PROXY_ENV_TARGET=http://collector.invalid/resource",
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("proxy-env child process failed: %v\noutput:\n%s", err, output)
	}
	if !strings.Contains(string(output), "status=200") {
		t.Errorf("proxy-env child output = %q, want a 200 status line", output)
	}
	observed := proxy.observed()
	if len(observed) != 1 || observed[0] != "GET http://collector.invalid/resource" {
		t.Errorf("proxy observed requests = %v, want exactly [%q]", observed, "GET http://collector.invalid/resource")
	}
}

// TestNoProxyExemptsHostFromRealProcessEnvironment is
// TestHTTPProxySelectedFromRealProcessEnvironment's NO_PROXY counterpart:
// a host listed in NO_PROXY must reach its destination directly rather
// than through HTTP_PROXY. The child targets 127.0.0.1 directly (a plain
// httptest.Server, not the proxy), so success requires the client to have
// bypassed the proxy.
func TestNoProxyExemptsHostFromRealProcessEnvironment(t *testing.T) {
	proxy := startCapturingProxy(t)
	proxyAddress := strings.TrimPrefix(proxy.server.URL, "http://")

	direct := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, _ = io.WriteString(writer, "direct")
	}))
	t.Cleanup(direct.Close)

	cmd := exec.Command(os.Args[0])
	cmd.Env = append(os.Environ(),
		proxyEnvChildMarker+"=1",
		"HTTP_PROXY=http://"+proxyAddress,
		"NO_PROXY=127.0.0.1",
		"HTTPTRANSPORT_PROXY_ENV_TARGET="+direct.URL,
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("no-proxy child process failed: %v\noutput:\n%s", err, output)
	}
	if !strings.Contains(string(output), "status=200") {
		t.Errorf("no-proxy child output = %q, want a 200 status line", output)
	}
	if observed := proxy.observed(); len(observed) != 0 {
		t.Errorf("proxy observed requests = %v, want none (NO_PROXY should bypass it)", observed)
	}
}
