package resthttp

import (
	"bufio"
	"crypto/tls"
	"encoding/pem"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dvmrry/infrawright-dev/go/internal/collectors"
)

type rawHTTPExchange struct {
	response []byte
}

type rawHTTPServer struct {
	address  string
	requests <-chan string
	done     <-chan error
}

func startRawHTTPServer(t *testing.T, exchanges ...rawHTTPExchange) rawHTTPServer {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	return startRawHTTPServerOnListener(t, listener, exchanges...)
}

func startRawHTTPServerOnListener(
	t *testing.T,
	listener net.Listener,
	exchanges ...rawHTTPExchange,
) rawHTTPServer {
	t.Helper()
	t.Cleanup(func() { _ = listener.Close() })
	requests := make(chan string, len(exchanges))
	done := make(chan error, 1)
	go func() {
		defer close(requests)
		defer close(done)
		for _, exchange := range exchanges {
			connection, acceptErr := listener.Accept()
			if acceptErr != nil {
				done <- acceptErr
				return
			}
			reader := bufio.NewReader(connection)
			requestLine, readErr := reader.ReadString('\n')
			if readErr == nil {
				for {
					line, headerErr := reader.ReadString('\n')
					if headerErr != nil {
						readErr = headerErr
						break
					}
					if line == "\r\n" {
						break
					}
				}
			}
			if readErr == nil {
				requests <- strings.TrimSuffix(requestLine, "\r\n")
				_, readErr = connection.Write(exchange.response)
			}
			closeErr := connection.Close()
			if readErr != nil {
				done <- readErr
				return
			}
			if closeErr != nil {
				done <- closeErr
				return
			}
		}
		done <- nil
	}()
	return rawHTTPServer{address: listener.Addr().String(), requests: requests, done: done}
}

func startRawHTTPSServer(t *testing.T, exchanges ...rawHTTPExchange) (rawHTTPServer, []byte) {
	t.Helper()
	seed := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	certificate := seed.TLS.Certificates[0]
	certificateDER := append([]byte(nil), seed.Certificate().Raw...)
	seed.Close()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	tlsListener := tls.NewListener(listener, &tls.Config{
		Certificates: []tls.Certificate{certificate},
		MinVersion:   tls.VersionTLS12,
	})
	return startRawHTTPServerOnListener(t, tlsListener, exchanges...), certificateDER
}

func rawHTTPResponse(status int, headers []byte, body string) []byte {
	return []byte(fmt.Sprintf(
		"HTTP/1.1 %d status\r\n%sContent-Length: %d\r\nConnection: close\r\n\r\n%s",
		status,
		headers,
		len(body),
		body,
	))
}

func awaitRawServer(t *testing.T, server rawHTTPServer, want ...string) {
	t.Helper()
	got := make([]string, 0, len(want))
	for request := range server.requests {
		got = append(got, request)
	}
	if err := <-server.done; err != nil {
		t.Fatalf("raw HTTP server failed: %v", err)
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("raw request lines = %q, want %q", got, want)
	}
}

func TestProductionWirePreservesWHATWGRawRequestTargets(t *testing.T) {
	for _, tc := range []struct {
		name     string
		path     string
		rawQuery string
		want     string
	}{
		{name: "malformed percent", path: "/a/%zz", rawQuery: "bad=%zz|ok", want: "GET /a/%zz?bad=%zz|ok HTTP/1.1"},
		{name: "raw pipe", path: "/a/x|y", rawQuery: `q=\foo`, want: `GET /a/x|y?q=\foo HTTP/1.1`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := startRawHTTPServer(t, rawHTTPExchange{response: rawHTTPResponse(200, nil, "[]")})
			transport, err := CreateRestHTTPTransport(collectors.Environment{}, RestHTTPTransportOptions{})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = transport.Close() })
			target := &url.URL{
				Scheme:   "http",
				Host:     server.address,
				Path:     tc.path,
				RawPath:  tc.path,
				RawQuery: tc.rawQuery,
			}
			if _, err := transport.Request(collectors.HTTPRequest{Method: "GET", URL: target}); err != nil {
				t.Fatalf("Request() failed: %v", err)
			}
			awaitRawServer(t, server, tc.want)
		})
	}
}

func TestProductionHTTPSWirePreservesRawOriginForm(t *testing.T) {
	server, certificateDER := startRawHTTPSServer(t,
		rawHTTPExchange{response: rawHTTPResponse(200, nil, "[]")},
	)
	bundle := filepath.Join(t.TempDir(), "server.pem")
	if err := os.WriteFile(bundle, pem.EncodeToMemory(&pem.Block{
		Type: "CERTIFICATE", Bytes: certificateDER,
	}), 0o600); err != nil {
		t.Fatal(err)
	}
	transport, err := CreateRestHTTPTransport(
		collectors.Environment{"REQUESTS_CA_BUNDLE": bundle},
		RestHTTPTransportOptions{},
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = transport.Close() })
	target := &url.URL{
		Scheme:   "https",
		Host:     server.address,
		Path:     "/secure/%zz|raw",
		RawPath:  "/secure/%zz|raw",
		RawQuery: "q=%zz|ok",
	}
	if _, err := transport.Request(collectors.HTTPRequest{Method: "GET", URL: target}); err != nil {
		t.Fatalf("Request() failed: %v", err)
	}
	awaitRawServer(t, server, "GET /secure/%zz|raw?q=%zz|ok HTTP/1.1")
}

func TestProductionRedirectPreservesRawRequestTarget(t *testing.T) {
	server := startRawHTTPServer(t,
		rawHTTPExchange{response: rawHTTPResponse(302, []byte("Location: /next/%zz?x=|\r\n"), "")},
		rawHTTPExchange{response: rawHTTPResponse(200, nil, "[]")},
	)
	transport, err := CreateRestHTTPTransport(collectors.Environment{}, RestHTTPTransportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = transport.Close() })
	target := mustURL(t, "http://"+server.address+"/start")
	if _, err := transport.Request(collectors.HTTPRequest{Method: "GET", URL: target}); err != nil {
		t.Fatalf("Request() failed: %v", err)
	}
	awaitRawServer(t, server,
		"GET /start HTTP/1.1",
		"GET /next/%zz?x=| HTTP/1.1",
	)
}

func TestProductionHTTPProxyConnectTunnelPreservesRawOriginForm(t *testing.T) {
	proxy := startTunnelCaptureServer(t, false, false, tls.Certificate{})
	transport, err := CreateRestHTTPTransport(
		collectors.Environment{"HTTP_PROXY": "http://" + proxy.address},
		RestHTTPTransportOptions{},
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = transport.Close() })
	target := &url.URL{
		Scheme:   "http",
		Host:     "collector.invalid",
		Path:     "/a/%zz|raw",
		RawPath:  "/a/%zz|raw",
		RawQuery: "q=%zz|ok",
	}
	if _, err := transport.Request(collectors.HTTPRequest{Method: "GET", URL: target}); err != nil {
		t.Fatalf("Request() failed: %v", err)
	}
	observed := awaitTunnelCapture(t, proxy)
	if observed.connect.line != "CONNECT collector.invalid:80 HTTP/1.1" ||
		observed.inner.line != "GET /a/%zz|raw?q=%zz|ok HTTP/1.1" {
		t.Errorf("proxy wire = CONNECT %q inner %q", observed.connect.line, observed.inner.line)
	}
}

func TestInjectedRoundTripperObservesWHATWGURLHref(t *testing.T) {
	for _, tc := range []struct {
		name string
		url  *url.URL
		want string
	}{
		{
			name: "malformed percent and query",
			url: &url.URL{
				Scheme: "https", Host: "api.example.test", Path: "/a/%zz", RawPath: "/a/%zz", RawQuery: "q=|",
			},
			want: "https://api.example.test/a/%zz?q=|",
		},
		{
			name: "raw pipe and fragment backslash",
			url: &url.URL{
				Scheme: "https", Host: "api.example.test", Path: "/a/x|y", RawPath: "/a/x|y",
				Fragment: `frag\part`, RawFragment: `frag\part`,
			},
			want: `https://api.example.test/a/x|y#frag\part`,
		},
		{
			name: "userinfo",
			url: &url.URL{
				Scheme: "https", Host: "api.example.test", Path: "/", User: url.UserPassword("user", "pass"),
			},
			want: "https://user:pass@api.example.test/",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			transport, err := CreateRestHTTPTransport(collectors.Environment{}, RestHTTPTransportOptions{
				RoundTripper: roundTripFunc(func(request *http.Request) (*http.Response, error) {
					if got := request.URL.String(); got != tc.want {
						t.Errorf("injected URL = %q, want %q", got, tc.want)
					}
					return response("[]", 200, nil), nil
				}),
			})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = transport.Close() })
			if _, err := transport.Request(collectors.HTTPRequest{Method: "GET", URL: tc.url}); err != nil {
				t.Fatalf("Request() failed: %v", err)
			}
		})
	}
}

func TestProductionLocationHeaderUsesUndiciLatin1Boundary(t *testing.T) {
	for _, tc := range []struct {
		name     string
		location []byte
		want     string
	}{
		{name: "single obs-text byte", location: []byte{'/', 0xe9}, want: "GET /%C3%A9 HTTP/1.1"},
		{name: "UTF-8 bytes are separate Latin-1 code points", location: []byte{'/', 0xc3, 0xa9}, want: "GET /%C3%83%C2%A9 HTTP/1.1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			headers := append([]byte("Location: "), tc.location...)
			headers = append(headers, []byte("\r\n")...)
			server := startRawHTTPServer(t,
				rawHTTPExchange{response: rawHTTPResponse(302, headers, "")},
				rawHTTPExchange{response: rawHTTPResponse(200, nil, "[]")},
			)
			transport, err := CreateRestHTTPTransport(collectors.Environment{}, RestHTTPTransportOptions{})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = transport.Close() })
			if _, err := transport.Request(collectors.HTTPRequest{
				Method: "GET",
				URL:    mustURL(t, "http://"+server.address+"/start"),
			}); err != nil {
				t.Fatalf("Request() failed: %v", err)
			}
			awaitRawServer(t, server, "GET /start HTTP/1.1", tc.want)
		})
	}
}

func TestProductionResponseHeadersUseUndiciLatin1Boundary(t *testing.T) {
	headers := []byte{'X', '-', 'O', 'b', 's', ':', ' ', 0xe9, '\r', '\n'}
	server := startRawHTTPServer(t,
		rawHTTPExchange{response: rawHTTPResponse(200, headers, "[]")},
	)
	transport, err := CreateRestHTTPTransport(collectors.Environment{}, RestHTTPTransportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = transport.Close() })
	result, err := transport.Request(collectors.HTTPRequest{
		Method: "GET",
		URL:    mustURL(t, "http://"+server.address+"/headers"),
	})
	if err != nil {
		t.Fatalf("Request() failed: %v", err)
	}
	if got := result.Headers["x-obs"]; len(got) != 1 || got[0] != "é" {
		t.Errorf("x-obs = %q, want Latin-1-decoded é", got)
	}
	awaitRawServer(t, server, "GET /headers HTTP/1.1")
}
