package resthttp

import (
	"bufio"
	"crypto/tls"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/dvmrry/infrawright-dev/go/internal/collectors"
)

type capturedWireRequest struct {
	line       string
	headers    map[string][]string
	rawHeaders map[string][]string
	body       string
}

func readCapturedWireRequest(reader *bufio.Reader) (capturedWireRequest, error) {
	line, err := reader.ReadString('\n')
	if err != nil {
		return capturedWireRequest{}, err
	}
	request := capturedWireRequest{
		line:       strings.TrimSuffix(line, "\r\n"),
		headers:    make(map[string][]string),
		rawHeaders: make(map[string][]string),
	}
	for {
		line, err = reader.ReadString('\n')
		if err != nil {
			return capturedWireRequest{}, err
		}
		if line == "\r\n" {
			break
		}
		name, value, ok := strings.Cut(strings.TrimSuffix(line, "\r\n"), ":")
		if !ok {
			return capturedWireRequest{}, fmt.Errorf("malformed header line %q", line)
		}
		name = strings.ToLower(name)
		request.rawHeaders[name] = append(request.rawHeaders[name], strings.TrimPrefix(value, " "))
		request.headers[name] = append(request.headers[name], strings.TrimSpace(value))
	}
	if values := request.headers["content-length"]; len(values) != 0 {
		length, parseErr := strconv.Atoi(values[0])
		if parseErr != nil {
			return capturedWireRequest{}, parseErr
		}
		body := make([]byte, length)
		if _, err = io.ReadFull(reader, body); err != nil {
			return capturedWireRequest{}, err
		}
		request.body = string(body)
	}
	return request, nil
}

type captureServer struct {
	address  string
	requests <-chan capturedWireRequest
	done     <-chan error
	listener net.Listener
}

func startCaptureServer(t *testing.T, responses ...[]byte) captureServer {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	requests := make(chan capturedWireRequest, len(responses))
	done := make(chan error, 1)
	go func() {
		defer close(requests)
		defer close(done)
		for _, response := range responses {
			connection, acceptErr := listener.Accept()
			if acceptErr != nil {
				done <- acceptErr
				return
			}
			request, readErr := readCapturedWireRequest(bufio.NewReader(connection))
			if readErr == nil {
				requests <- request
				_, readErr = connection.Write(response)
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
	return captureServer{
		address: listener.Addr().String(), requests: requests, done: done, listener: listener,
	}
}

func awaitCaptureServer(t *testing.T, server captureServer) []capturedWireRequest {
	t.Helper()
	var requests []capturedWireRequest
	for request := range server.requests {
		requests = append(requests, request)
	}
	if err := <-server.done; err != nil {
		t.Fatalf("capture server failed: %v", err)
	}
	return requests
}

func testWireCertificate(t *testing.T) (tls.Certificate, []byte) {
	t.Helper()
	seed := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	certificate := seed.TLS.Certificates[0]
	der := append([]byte(nil), seed.Certificate().Raw...)
	seed.Close()
	return certificate, der
}

func writeTestCABundle(t *testing.T, der []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "wire-ca.pem")
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

type tunnelObservation struct {
	connect    capturedWireRequest
	inner      capturedWireRequest
	proxyALPN  string
	targetALPN string
}

type tunnelCaptureServer struct {
	address     string
	observation <-chan tunnelObservation
	done        <-chan error
}

type tlsCaptureResult struct {
	request    capturedWireRequest
	serverName string
	alpn       string
	err        error
}

type tlsCaptureServer struct {
	address string
	result  <-chan tlsCaptureResult
}

func startTLSCaptureServer(
	t *testing.T,
	certificate tls.Certificate,
	response []byte,
) tlsCaptureServer {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	result := make(chan tlsCaptureResult, 1)
	go func() {
		connection, acceptErr := listener.Accept()
		if acceptErr != nil {
			result <- tlsCaptureResult{err: acceptErr}
			return
		}
		defer connection.Close()
		observedServerName := ""
		secured := tls.Server(connection, &tls.Config{
			Certificates: []tls.Certificate{certificate},
			MinVersion:   tls.VersionTLS12,
			NextProtos:   []string{"h2", "http/1.1"},
			GetConfigForClient: func(info *tls.ClientHelloInfo) (*tls.Config, error) {
				observedServerName = info.ServerName
				return nil, nil
			},
		})
		if handshakeErr := secured.Handshake(); handshakeErr != nil {
			result <- tlsCaptureResult{serverName: observedServerName, err: handshakeErr}
			return
		}
		request, readErr := readCapturedWireRequest(bufio.NewReader(secured))
		if readErr == nil {
			_, readErr = secured.Write(response)
		}
		result <- tlsCaptureResult{
			request: request, serverName: observedServerName,
			alpn: secured.ConnectionState().NegotiatedProtocol, err: readErr,
		}
	}()
	return tlsCaptureServer{address: listener.Addr().String(), result: result}
}

func startTunnelCaptureServer(
	t *testing.T,
	proxyTLS bool,
	targetTLS bool,
	certificate tls.Certificate,
) tunnelCaptureServer {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	if proxyTLS {
		listener = tls.NewListener(listener, &tls.Config{
			Certificates: []tls.Certificate{certificate},
			MinVersion:   tls.VersionTLS12,
			NextProtos:   []string{"h2", "http/1.1"},
		})
	}
	t.Cleanup(func() { _ = listener.Close() })
	observation := make(chan tunnelObservation, 1)
	done := make(chan error, 1)
	go func() {
		defer close(observation)
		defer close(done)
		connection, acceptErr := listener.Accept()
		if acceptErr != nil {
			done <- acceptErr
			return
		}
		reader := bufio.NewReader(connection)
		connect, readErr := readCapturedWireRequest(reader)
		proxyALPN := ""
		if secured, ok := connection.(*tls.Conn); ok {
			proxyALPN = secured.ConnectionState().NegotiatedProtocol
		}
		if readErr == nil {
			_, readErr = io.WriteString(connection, "HTTP/1.1 200 Connection Established\r\n\r\n")
		}
		if readErr == nil && targetTLS {
			secured := tls.Server(connection, &tls.Config{
				Certificates: []tls.Certificate{certificate},
				MinVersion:   tls.VersionTLS12,
				NextProtos:   []string{"h2", "http/1.1"},
			})
			readErr = secured.Handshake()
			connection = secured
			reader = bufio.NewReader(connection)
		}
		var inner capturedWireRequest
		targetALPN := ""
		if readErr == nil {
			inner, readErr = readCapturedWireRequest(reader)
		}
		if secured, ok := connection.(*tls.Conn); ok && targetTLS {
			targetALPN = secured.ConnectionState().NegotiatedProtocol
		}
		if readErr == nil {
			observation <- tunnelObservation{
				connect: connect, inner: inner, proxyALPN: proxyALPN, targetALPN: targetALPN,
			}
			_, readErr = connection.Write(rawHTTPResponse(200, nil, "[]"))
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
		done <- nil
	}()
	return tunnelCaptureServer{
		address: listener.Addr().String(), observation: observation, done: done,
	}
}

func awaitTunnelCapture(t *testing.T, server tunnelCaptureServer) tunnelObservation {
	t.Helper()
	observation := <-server.observation
	if err := <-server.done; err != nil {
		t.Fatalf("tunnel capture failed: %v", err)
	}
	return observation
}

func TestProductionProxyAlwaysConnectTunnelsWithOriginForm(t *testing.T) {
	certificate, certificateDER := testWireCertificate(t)
	bundle := writeTestCABundle(t, certificateDER)
	for _, tc := range []struct {
		name      string
		proxyTLS  bool
		targetTLS bool
	}{
		{name: "HTTP target via HTTP proxy"},
		{name: "HTTPS target via HTTP proxy", targetTLS: true},
		{name: "HTTP target via HTTPS proxy", proxyTLS: true},
		{name: "HTTPS target via HTTPS proxy", proxyTLS: true, targetTLS: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			proxy := startTunnelCaptureServer(t, tc.proxyTLS, tc.targetTLS, certificate)
			proxyScheme := "http"
			if tc.proxyTLS {
				proxyScheme = "https"
			}
			targetScheme := "http"
			proxyVariable := "HTTP_PROXY"
			if tc.targetTLS {
				targetScheme = "https"
				proxyVariable = "HTTPS_PROXY"
			}
			environment := collectors.Environment{
				proxyVariable:        proxyScheme + "://user:pass@" + proxy.address,
				"REQUESTS_CA_BUNDLE": bundle,
			}
			transport, err := CreateRestHTTPTransport(environment, RestHTTPTransportOptions{})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = transport.Close() })
			target := &url.URL{
				Scheme: targetScheme, Host: "127.0.0.1:4444", Path: "/a/%zz|raw", RawPath: "/a/%zz|raw",
				RawQuery: "q=%zz|ok", User: url.UserPassword("target-user", "target-pass"),
			}
			_, err = transport.Request(collectors.HTTPRequest{
				Method: "POST", URL: target, Body: []byte("abc"),
				Headers: map[string]string{
					"Host":           "example.com:9443",
					"Content-Length": "003",
					"Trailer":        "x-check",
				},
			})
			if err != nil {
				t.Fatalf("Request() failed: %v", err)
			}
			got := awaitTunnelCapture(t, proxy)
			if got.connect.line != "CONNECT 127.0.0.1:4444 HTTP/1.1" {
				t.Errorf("CONNECT line = %q", got.connect.line)
			}
			if got.connect.headers["host"][0] != "127.0.0.1:4444" ||
				got.connect.headers["connection"][0] != "close" ||
				got.connect.headers["proxy-connection"][0] != "keep-alive" ||
				got.connect.headers["proxy-authorization"][0] != "Basic "+base64.StdEncoding.EncodeToString([]byte("user:pass")) {
				t.Errorf("CONNECT headers = %#v", got.connect.headers)
			}
			if got.inner.line != "POST /a/%zz|raw?q=%zz|ok HTTP/1.1" {
				t.Errorf("inner line = %q", got.inner.line)
			}
			if got.inner.headers["host"][0] != "example.com:9443" ||
				got.inner.headers["content-length"][0] != "3" ||
				got.inner.headers["trailer"][0] != "x-check" || got.inner.body != "abc" {
				t.Errorf("inner request = %#v", got.inner)
			}
			if len(got.inner.headers["authorization"]) != 0 || len(got.inner.headers["proxy-authorization"]) != 0 {
				t.Errorf("target or proxy authorization leaked inside tunnel: %#v", got.inner.headers)
			}
			if tc.proxyTLS && got.proxyALPN != "http/1.1" {
				t.Errorf("HTTPS proxy ALPN = %q, want http/1.1", got.proxyALPN)
			}
			if tc.targetTLS && got.targetALPN != "http/1.1" {
				t.Errorf("HTTPS target ALPN = %q, want http/1.1", got.targetALPN)
			}
		})
	}
}

func TestHTTPSNoProxyBypassesConfiguredProxy(t *testing.T) {
	server, certificateDER := startRawHTTPSServer(t,
		rawHTTPExchange{response: rawHTTPResponse(200, nil, "[]")},
	)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	proxyContact := make(chan struct{}, 1)
	go func() {
		connection, acceptErr := listener.Accept()
		if acceptErr == nil {
			proxyContact <- struct{}{}
			_ = connection.Close()
		}
	}()
	transport, err := CreateRestHTTPTransport(collectors.Environment{
		"HTTPS_PROXY":        "http://" + listener.Addr().String(),
		"NO_PROXY":           "127.0.0.1",
		"SSL_CERT_FILE":      writeTestCABundle(t, certificateDER),
		"REQUESTS_CA_BUNDLE": "",
	}, RestHTTPTransportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = transport.Close() })
	if _, err = transport.Request(collectors.HTTPRequest{
		Method: "GET", URL: mustURL(t, "https://"+server.address+"/no-proxy"),
	}); err != nil {
		t.Fatalf("NO_PROXY HTTPS Request() failed: %v", err)
	}
	awaitRawServer(t, server, "GET /no-proxy HTTP/1.1")
	select {
	case <-proxyContact:
		t.Fatal("HTTPS NO_PROXY request contacted the proxy")
	case <-time.After(20 * time.Millisecond):
	}
}

func TestProductionReservedHeaderDirectWireTable(t *testing.T) {
	for _, tc := range []struct {
		name    string
		method  string
		body    []byte
		headers map[string]string
		user    *url.Userinfo
		check   func(*testing.T, capturedWireRequest)
	}{
		{
			name: "host override", method: "GET", headers: map[string]string{"Host": "override.example"},
			check: func(t *testing.T, request capturedWireRequest) {
				if request.headers["host"][0] != "override.example" {
					t.Errorf("Host = %#v", request.headers["host"])
				}
			},
		},
		{
			name: "leading-zero content length canonicalized", method: "POST", body: []byte("abc"),
			headers: map[string]string{"Content-Length": "003"},
			check: func(t *testing.T, request capturedWireRequest) {
				if request.headers["content-length"][0] != "3" || request.body != "abc" {
					t.Errorf("request = %#v", request)
				}
			},
		},
		{
			name: "bodyless POST ignores declared positive length", method: "POST",
			headers: map[string]string{"Content-Length": "3"},
			check: func(t *testing.T, request capturedWireRequest) {
				if request.headers["content-length"][0] != "0" || request.body != "" {
					t.Errorf("request = %#v", request)
				}
			},
		},
		{
			name: "bodyless GET ignores declared positive length", method: "GET",
			headers: map[string]string{"Content-Length": "3"},
			check: func(t *testing.T, request capturedWireRequest) {
				if len(request.headers["content-length"]) != 0 {
					t.Errorf("Content-Length = %#v, want absent", request.headers["content-length"])
				}
			},
		},
		{
			name: "GET body uses actual length", method: "GET", body: []byte("abc"),
			headers: map[string]string{"Content-Length": "2"},
			check: func(t *testing.T, request capturedWireRequest) {
				if request.headers["content-length"][0] != "3" || request.body != "abc" {
					t.Errorf("request = %#v", request)
				}
			},
		},
		{
			name: "trailer header transmitted", method: "POST", body: []byte("abc"),
			headers: map[string]string{"Trailer": "x-check"},
			check: func(t *testing.T, request capturedWireRequest) {
				if request.headers["trailer"][0] != "x-check" {
					t.Errorf("Trailer = %#v", request.headers["trailer"])
				}
			},
		},
		{
			name: "connection close is consumed once", method: "GET",
			headers: map[string]string{"Connection": "close"},
			check: func(t *testing.T, request capturedWireRequest) {
				if got := request.headers["connection"]; !slices.Equal(got, []string{"close"}) {
					t.Errorf("Connection = %#v, want one close", got)
				}
			},
		},
		{
			name: "non-close connection token is consumed", method: "GET",
			headers: map[string]string{"Connection": "foo"},
			check: func(t *testing.T, request capturedWireRequest) {
				if got := request.headers["connection"]; !slices.Equal(got, []string{"keep-alive"}) {
					t.Errorf("Connection = %#v, want one keep-alive", got)
				}
			},
		},
		{
			name: "explicit empty user agent is transmitted", method: "GET",
			headers: map[string]string{"User-Agent": ""},
			check: func(t *testing.T, request capturedWireRequest) {
				if got := request.rawHeaders["user-agent"]; !slices.Equal(got, []string{""}) {
					t.Errorf("User-Agent = %#v, want one explicit empty value", got)
				}
			},
		},
		{
			name: "absent user agent remains absent", method: "GET",
			check: func(t *testing.T, request capturedWireRequest) {
				if got := request.rawHeaders["user-agent"]; len(got) != 0 {
					t.Errorf("User-Agent = %#v, want absent", got)
				}
			},
		},
		{
			name: "target userinfo does not synthesize authorization", method: "GET",
			user: url.UserPassword("target-user", "target-pass"),
			check: func(t *testing.T, request capturedWireRequest) {
				if len(request.headers["authorization"]) != 0 {
					t.Errorf("Authorization = %#v, want absent", request.headers["authorization"])
				}
			},
		},
		{
			name: "direct proxy authorization is forwarded", method: "GET",
			headers: map[string]string{"Proxy-Authorization": "Bearer caller"},
			check: func(t *testing.T, request capturedWireRequest) {
				if request.headers["proxy-authorization"][0] != "Bearer caller" {
					t.Errorf("Proxy-Authorization = %#v", request.headers["proxy-authorization"])
				}
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := startCaptureServer(t, rawHTTPResponse(200, nil, "[]"))
			transport, err := CreateRestHTTPTransport(collectors.Environment{}, RestHTTPTransportOptions{})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = transport.Close() })
			target := &url.URL{Scheme: "http", Host: server.address, Path: "/resource", User: tc.user}
			if _, err = transport.Request(collectors.HTTPRequest{
				Method: tc.method, URL: target, Body: tc.body, Headers: tc.headers,
			}); err != nil {
				t.Fatalf("Request() failed: %v", err)
			}
			requests := awaitCaptureServer(t, server)
			if len(requests) != 1 {
				t.Fatalf("request count = %d", len(requests))
			}
			tc.check(t, requests[0])
		})
	}
}

func TestProductionHostOverrideUsesRawHeaderValueGrammar(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value string
	}{
		{name: "empty", value: ""},
		{name: "space only", value: " "},
		{name: "tab only", value: "\t"},
		{name: "printable and Latin-1", value: " a\"`{|}/?#@\\<>^\u0080\u00ff \t"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := startCaptureServer(t, rawHTTPResponse(200, nil, "[]"))
			transport, err := CreateRestHTTPTransport(collectors.Environment{}, RestHTTPTransportOptions{})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = transport.Close() })
			_, err = transport.Request(collectors.HTTPRequest{
				Method: "GET", URL: mustURL(t, "http://"+server.address+"/"),
				Headers: map[string]string{"Host": tc.value},
			})
			if err != nil {
				t.Fatalf("Request() failed: %v", err)
			}
			requests := awaitCaptureServer(t, server)
			if len(requests) != 1 {
				t.Fatalf("request count = %d, want 1", len(requests))
			}
			got := []byte(requests[0].rawHeaders["host"][0])
			want := nodeLatin1(tc.value)
			if !slices.Equal(got, want) {
				t.Errorf("raw Host bytes = %x, want %x", got, want)
			}
		})
	}
}

func TestProductionProxyKeepsRawHostOverrideInsideTunnel(t *testing.T) {
	for _, host := range []string{"", " a\"`{|}/?#@\\<>^\u0080\u00ff \t"} {
		t.Run(fmt.Sprintf("%x", nodeLatin1(host)), func(t *testing.T) {
			proxy := startTunnelCaptureServer(t, false, false, tls.Certificate{})
			transport, err := CreateRestHTTPTransport(
				collectors.Environment{"HTTP_PROXY": "http://" + proxy.address},
				RestHTTPTransportOptions{},
			)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = transport.Close() })
			_, err = transport.Request(collectors.HTTPRequest{
				Method: "GET", URL: mustURL(t, "http://127.0.0.1:4444/"),
				Headers: map[string]string{"Host": host},
			})
			if err != nil {
				t.Fatalf("Request() failed: %v", err)
			}
			observation := awaitTunnelCapture(t, proxy)
			if got := observation.connect.rawHeaders["host"]; !slices.Equal(got, []string{"127.0.0.1:4444"}) {
				t.Errorf("CONNECT Host = %#v, want URL target", got)
			}
			if got, want := []byte(observation.inner.rawHeaders["host"][0]), nodeLatin1(host); !slices.Equal(got, want) {
				t.Errorf("inner raw Host bytes = %x, want %x", got, want)
			}
		})
	}
}

func TestProductionInvalidHostOverrideFailsBeforeDirectOrProxyWire(t *testing.T) {
	for _, value := range []string{"\x00", "\n", "\r", "\x1f", "\x7f", "\u0100", "[unterminated"} {
		for _, proxied := range []bool{false, true} {
			name := fmt.Sprintf("%x/direct=%t", []byte(value), !proxied)
			t.Run(name, func(t *testing.T) {
				listener, err := net.Listen("tcp", "127.0.0.1:0")
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = listener.Close() })
				accepted := make(chan struct{}, 1)
				go func() {
					connection, acceptErr := listener.Accept()
					if acceptErr == nil {
						accepted <- struct{}{}
						_ = connection.Close()
					}
				}()
				environment := collectors.Environment{}
				target := "http://" + listener.Addr().String() + "/"
				if proxied {
					environment["HTTP_PROXY"] = "http://" + listener.Addr().String()
					target = "http://collector.invalid/"
				}
				transport, err := CreateRestHTTPTransport(environment, RestHTTPTransportOptions{})
				if err != nil {
					t.Fatal(err)
				}
				_, err = transport.Request(collectors.HTTPRequest{
					Method: "GET", URL: mustURL(t, target), Headers: map[string]string{"Host": value},
				})
				requireProcessFailure(t, err, "REST_HTTP_TRANSPORT_FAILED")
				select {
				case <-accepted:
					t.Fatal("invalid Host override reached the network")
				case <-time.After(20 * time.Millisecond):
				}
			})
		}
	}
}

func TestProductionHTTPSHostOverrideControlsTargetSNIAndPreservesPresence(t *testing.T) {
	certificate, certificateDER := testWireCertificate(t)
	bundle := writeTestCABundle(t, certificateDER)
	for _, tc := range []struct {
		name           string
		host           string
		wantServerName string
		wantFailure    bool
	}{
		{name: "DNS override", host: "example.com:9443", wantServerName: "example.com"},
		{name: "explicit empty falls back to URL IP", host: "", wantServerName: ""},
		{name: "wrong override fails certificate", host: "wrong.example:9443", wantServerName: "wrong.example", wantFailure: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := startTLSCaptureServer(t, certificate, rawHTTPResponse(200, nil, "[]"))
			transport, err := CreateRestHTTPTransport(
				collectors.Environment{"REQUESTS_CA_BUNDLE": bundle}, RestHTTPTransportOptions{},
			)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = transport.Close() })
			_, requestErr := transport.Request(collectors.HTTPRequest{
				Method: "GET", URL: mustURL(t, "https://"+server.address+"/"),
				Headers: map[string]string{"Host": tc.host},
			})
			if tc.wantFailure {
				failure := requireProcessFailure(t, requestErr, "REST_HTTP_TRANSPORT_FAILED")
				if !strings.Contains(failure.Message, "certificate failure") {
					t.Errorf("failure message = %q, want certificate classification", failure.Message)
				}
			} else if requestErr != nil {
				t.Fatalf("Request() failed: %v", requestErr)
			}
			result := <-server.result
			if result.serverName != tc.wantServerName {
				t.Errorf("TLS SNI = %q, want %q", result.serverName, tc.wantServerName)
			}
			if tc.wantFailure {
				if result.err == nil {
					t.Error("TLS server handshake succeeded for a client-side certificate rejection")
				}
				return
			}
			if result.err != nil {
				t.Fatalf("TLS capture failed: %v", result.err)
			}
			if result.alpn != "http/1.1" {
				t.Errorf("direct HTTPS ALPN = %q, want http/1.1", result.alpn)
			}
			if got := result.request.rawHeaders["host"]; !slices.Equal(got, []string{tc.host}) {
				t.Errorf("raw Host = %#v, want %#v", got, []string{tc.host})
			}
		})
	}
}

func TestProductionReservedHeaderFailuresOccurBeforeWire(t *testing.T) {
	for _, tc := range []struct {
		name    string
		method  string
		body    []byte
		headers map[string]string
	}{
		{name: "string plus content length", method: "POST", body: []byte("abc"), headers: map[string]string{"Content-Length": "+3"}},
		{name: "short content length", method: "POST", body: []byte("abc"), headers: map[string]string{"Content-Length": "2"}},
		{name: "long content length", method: "POST", body: []byte("abc"), headers: map[string]string{"Content-Length": "4"}},
		{name: "transfer encoding", method: "POST", body: []byte("abc"), headers: map[string]string{"Transfer-Encoding": "chunked"}},
		{name: "connection invalid token", method: "GET", headers: map[string]string{"Connection": "a b"}},
		{name: "keep alive", method: "GET", headers: map[string]string{"Keep-Alive": "timeout=5"}},
		{name: "upgrade", method: "GET", headers: map[string]string{"Upgrade": "websocket"}},
		{name: "expect", method: "POST", body: []byte("abc"), headers: map[string]string{"Expect": "100-continue"}},
		{name: "case-colliding request headers", method: "GET", headers: map[string]string{"Host": "one", "host": "two"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := startCaptureServer(t, rawHTTPResponse(200, nil, "[]"))
			transport, err := CreateRestHTTPTransport(collectors.Environment{}, RestHTTPTransportOptions{})
			if err != nil {
				t.Fatal(err)
			}
			_, err = transport.Request(collectors.HTTPRequest{
				Method: tc.method, URL: mustURL(t, "http://"+server.address+"/"), Body: tc.body, Headers: tc.headers,
			})
			requireProcessFailure(t, err, "REST_HTTP_TRANSPORT_FAILED")
			select {
			case request := <-server.requests:
				t.Fatalf("failure reached wire: %#v", request)
			case <-time.After(50 * time.Millisecond):
			}
		})
	}
}

func TestProductionHostAndBodyHeadersAcrossRedirect(t *testing.T) {
	server := startCaptureServer(t,
		rawHTTPResponse(302, []byte("Location: /next\r\n"), ""),
		rawHTTPResponse(200, nil, "[]"),
	)
	transport, err := CreateRestHTTPTransport(collectors.Environment{}, RestHTTPTransportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = transport.Close() })
	_, err = transport.Request(collectors.HTTPRequest{
		Method: "POST", URL: mustURL(t, "http://"+server.address+"/start"), Body: []byte("abc"),
		Headers: map[string]string{"Host": "override.example", "Content-Length": "3"},
	})
	if err != nil {
		t.Fatal(err)
	}
	requests := awaitCaptureServer(t, server)
	if len(requests) != 2 {
		t.Fatalf("request count = %d", len(requests))
	}
	if got := []string{requests[0].headers["host"][0], requests[1].headers["host"][0]}; !slices.Equal(got, []string{"override.example", "override.example"}) {
		t.Errorf("Host headers = %v", got)
	}
	if requests[0].line != "POST /start HTTP/1.1" || requests[0].body != "abc" ||
		requests[1].line != "GET /next HTTP/1.1" || len(requests[1].headers["content-length"]) != 0 {
		t.Errorf("redirect requests = %#v", requests)
	}
}

func TestProductionExplicitEmptyHostPersistsAcrossCrossOriginRedirect(t *testing.T) {
	final := startCaptureServer(t, rawHTTPResponse(200, nil, "[]"))
	initial := startCaptureServer(t, rawHTTPResponse(302,
		[]byte("Location: http://"+final.address+"/final\r\n"), "",
	))
	transport, err := CreateRestHTTPTransport(collectors.Environment{}, RestHTTPTransportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = transport.Close() })
	_, err = transport.Request(collectors.HTTPRequest{
		Method: "GET", URL: mustURL(t, "http://"+initial.address+"/start"),
		Headers: map[string]string{"Host": ""},
	})
	if err != nil {
		t.Fatalf("Request() failed: %v", err)
	}
	initialRequests := awaitCaptureServer(t, initial)
	finalRequests := awaitCaptureServer(t, final)
	for name, requests := range map[string][]capturedWireRequest{
		"initial": initialRequests,
		"final":   finalRequests,
	} {
		if len(requests) != 1 {
			t.Fatalf("%s request count = %d, want 1", name, len(requests))
		}
		if got := requests[0].rawHeaders["host"]; !slices.Equal(got, []string{""}) {
			t.Errorf("%s raw Host = %#v, want explicit empty", name, got)
		}
	}
}

func TestProductionInvalidConnectionHeadersFailBeforeSelectedProxy(t *testing.T) {
	for name, headers := range map[string]map[string]string{
		"invalid connection token": {"Connection": "a b"},
		"keep alive":               {"Keep-Alive": "timeout=5"},
		"transfer encoding":        {"Transfer-Encoding": "chunked"},
		"upgrade":                  {"Upgrade": "websocket"},
	} {
		t.Run(name, func(t *testing.T) {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = listener.Close() })
			accepted := make(chan struct{}, 1)
			go func() {
				connection, acceptErr := listener.Accept()
				if acceptErr == nil {
					accepted <- struct{}{}
					_ = connection.Close()
				}
			}()
			transport, err := CreateRestHTTPTransport(
				collectors.Environment{"HTTP_PROXY": "http://" + listener.Addr().String()},
				RestHTTPTransportOptions{},
			)
			if err != nil {
				t.Fatal(err)
			}
			_, err = transport.Request(collectors.HTTPRequest{
				Method: "GET", URL: mustURL(t, "http://collector.invalid/"), Headers: headers,
			})
			requireProcessFailure(t, err, "REST_HTTP_TRANSPORT_FAILED")
			select {
			case <-accepted:
				t.Fatal("invalid reserved header reached selected proxy")
			case <-time.After(20 * time.Millisecond):
			}
		})
	}
}

func TestSelectedProxyRejectsCallerProxyAuthorizationBeforeConnect(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	accepted := make(chan struct{}, 1)
	go func() {
		connection, acceptErr := listener.Accept()
		if acceptErr == nil {
			accepted <- struct{}{}
			_ = connection.Close()
		}
	}()
	transport, err := CreateRestHTTPTransport(
		collectors.Environment{"HTTP_PROXY": "http://" + listener.Addr().String()},
		RestHTTPTransportOptions{},
	)
	if err != nil {
		t.Fatal(err)
	}
	_, err = transport.Request(collectors.HTTPRequest{
		Method: "GET", URL: mustURL(t, "http://collector.invalid/"),
		Headers: map[string]string{"Proxy-Authorization": "Bearer caller"},
	})
	requireProcessFailure(t, err, "REST_HTTP_TRANSPORT_FAILED")
	select {
	case <-accepted:
		t.Fatal("selected proxy was contacted before Proxy-Authorization rejection")
	case <-time.After(50 * time.Millisecond):
	}
}

func TestCreateTransportReturnsRawURIErrorForMalformedCompleteProxyCredentials(t *testing.T) {
	_, err := CreateRestHTTPTransport(
		collectors.Environment{"HTTP_PROXY": "http://user:%zz@proxy.example/"},
		RestHTTPTransportOptions{},
	)
	if err == nil || err.Error() != "URI malformed" {
		t.Fatalf("CreateRestHTTPTransport() error = %v, want raw URI malformed", err)
	}
}

func TestInjectedRoundTripperRetainsReservedHeaderSeamValues(t *testing.T) {
	transport, err := CreateRestHTTPTransport(collectors.Environment{}, RestHTTPTransportOptions{
		RoundTripper: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			for name, want := range map[string]string{
				"Host":                "override.example",
				"Content-Length":      "003",
				"Transfer-Encoding":   "raw-transfer",
				"Expect":              "raw-expect",
				"Trailer":             "raw-trailer",
				"Proxy-Authorization": "raw-proxy-auth",
			} {
				if got := request.Header.Get(name); got != want {
					t.Errorf("injected %s = %q, want %q", name, got, want)
				}
			}
			if request.Host != "api.example.test" {
				t.Errorf("injected Request.Host = %q, want target authority", request.Host)
			}
			return response("[]", 200, nil), nil
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = transport.Request(collectors.HTTPRequest{
		Method: "POST", URL: mustURL(t, "https://api.example.test/"), Body: []byte("abc"),
		Headers: map[string]string{
			"Host":                "override.example",
			"Content-Length":      "003",
			"Transfer-Encoding":   "raw-transfer",
			"Expect":              "raw-expect",
			"Trailer":             "raw-trailer",
			"Proxy-Authorization": "raw-proxy-auth",
		},
	})
	if err != nil {
		t.Fatalf("injected seam Request() failed: %v", err)
	}
}

func TestNoProxyForwardsCallerProxyAuthorizationDirectly(t *testing.T) {
	server := startCaptureServer(t, rawHTTPResponse(200, nil, "[]"))
	transport, err := CreateRestHTTPTransport(collectors.Environment{
		"HTTP_PROXY": "http://127.0.0.1:1",
		"NO_PROXY":   "127.0.0.1",
	}, RestHTTPTransportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = transport.Close() })
	_, err = transport.Request(collectors.HTTPRequest{
		Method: "GET", URL: mustURL(t, "http://"+server.address+"/"),
		Headers: map[string]string{"Proxy-Authorization": "Bearer caller"},
	})
	if err != nil {
		t.Fatal(err)
	}
	requests := awaitCaptureServer(t, server)
	if got := requests[0].headers["proxy-authorization"]; !slices.Equal(got, []string{"Bearer caller"}) {
		t.Errorf("Proxy-Authorization = %#v", got)
	}
}

func TestCaseCollidingResponseSeamHeadersFailDeterministically(t *testing.T) {
	for iteration := 0; iteration < 100; iteration++ {
		body := &trackingBody{reader: strings.NewReader("redirect")}
		calls := 0
		transport, err := CreateRestHTTPTransport(collectors.Environment{}, RestHTTPTransportOptions{
			RoundTripper: roundTripFunc(func(*http.Request) (*http.Response, error) {
				calls++
				return &http.Response{
					StatusCode: 302,
					Header: http.Header{
						"Location": []string{"https://one.example/"},
						"location": []string{"https://two.example/"},
					},
					Body: body,
				}, nil
			}),
		})
		if err != nil {
			t.Fatal(err)
		}
		_, err = transport.Request(collectors.HTTPRequest{
			Method: "GET", URL: mustURL(t, "https://api.example.test/"),
		})
		failure := requireProcessFailure(t, err, "INVALID_REST_HTTP_RESPONSE")
		if failure.Message != "HTTP response headers are ambiguous" || calls != 1 || body.closeCount() != 1 {
			t.Fatalf("iteration %d: failure=%#v calls=%d bodyCloseCount=%d", iteration, failure, calls, body.closeCount())
		}
	}
}

func TestRequestLocationUsesSanitizedWHATWGSerialization(t *testing.T) {
	for _, tc := range []struct {
		name string
		url  *url.URL
		want string
	}{
		{
			name: "malformed percent and raw pipe",
			url: &url.URL{
				Scheme: "https", Host: "api.example.test", Path: "/a/%zz|raw", RawPath: "/a/%zz|raw",
				RawQuery: "secret=%zz|query", Fragment: "fragment", User: url.UserPassword("user", "pass"),
			},
			want: "https://api.example.test/a/%zz|raw",
		},
		{
			name: "encoded slash",
			url: &url.URL{
				Scheme: "http", Host: "api.example.test", Path: "/a/b", RawPath: "/a%2Fb", RawQuery: "drop=1",
			},
			want: "http://api.example.test/a%2Fb",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := requestLocation(tc.url); got != tc.want {
				t.Errorf("requestLocation() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestRedirectLimitDiagnosticPreservesWHATWGRawPath(t *testing.T) {
	zero := 0
	transport, err := CreateRestHTTPTransport(collectors.Environment{}, RestHTTPTransportOptions{
		MaxRedirects: &zero,
		RoundTripper: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return response("", 302, http.Header{"Location": []string{"/again"}}), nil
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	target := &url.URL{
		Scheme: "https", Host: "api.example.test", Path: "/a/%zz|raw", RawPath: "/a/%zz|raw",
		RawQuery: "drop=1", Fragment: "drop", User: url.UserPassword("drop", "secret"),
	}
	_, err = transport.Request(collectors.HTTPRequest{Method: "GET", URL: target})
	failure := requireProcessFailure(t, err, "REST_HTTP_REDIRECT_LIMIT")
	if failure.Message != "too many redirects while requesting https://api.example.test/a/%zz|raw" {
		t.Errorf("redirect limit message = %q", failure.Message)
	}
}

func TestUnsafeNodeHostPunctuationFailsClosedBeforeDirectOrProxyWire(t *testing.T) {
	for _, character := range []string{`"`, "`", "{", "}"} {
		for _, route := range []struct {
			name   string
			scheme string
			envKey string
		}{
			{name: "direct", scheme: "http"},
			{name: "HTTP proxy", scheme: "http", envKey: "HTTP_PROXY"},
			{name: "HTTPS proxy", scheme: "https", envKey: "HTTPS_PROXY"},
		} {
			t.Run(route.name+"_"+fmt.Sprintf("%x", character[0]), func(t *testing.T) {
				listener, err := net.Listen("tcp", "127.0.0.1:0")
				if err != nil {
					t.Fatal(err)
				}
				accepted := make(chan struct{}, 1)
				go func() {
					connection, acceptErr := listener.Accept()
					if acceptErr == nil {
						accepted <- struct{}{}
						_ = connection.Close()
					}
				}()
				environment := collectors.Environment{}
				if route.envKey != "" {
					environment[route.envKey] = "http://" + listener.Addr().String()
				}
				transport, err := CreateRestHTTPTransport(environment, RestHTTPTransportOptions{})
				if err != nil {
					t.Fatal(err)
				}
				target := &url.URL{Scheme: route.scheme, Host: "exa" + character + "mple.com", Path: "/"}
				_, err = transport.Request(collectors.HTTPRequest{Method: "GET", URL: target})
				requireProcessFailure(t, err, "REST_HTTP_TRANSPORT_FAILED")
				select {
				case <-accepted:
					t.Fatal("unsafe hostname reached the network")
				case <-time.After(20 * time.Millisecond):
				}
				_ = listener.Close()
			})
		}
	}
}

func TestProductionRequestTimeoutBoundsStalledProxyConnectResponse(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	connectRead := make(chan struct{})
	go func() {
		connection, acceptErr := listener.Accept()
		if acceptErr != nil {
			return
		}
		defer connection.Close()
		_, readErr := readCapturedWireRequest(bufio.NewReader(connection))
		if readErr == nil {
			close(connectRead)
		}
		buffer := make([]byte, 1)
		_, _ = connection.Read(buffer)
	}()
	timeout := 50
	transport, err := CreateRestHTTPTransport(
		collectors.Environment{"HTTP_PROXY": "http://" + listener.Addr().String()},
		RestHTTPTransportOptions{RequestTimeoutMs: &timeout},
	)
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	_, err = transport.Request(collectors.HTTPRequest{
		Method: "GET", URL: mustURL(t, "http://collector.invalid/"),
	})
	failure := requireProcessFailure(t, err, "REST_HTTP_TRANSPORT_FAILED")
	if !strings.Contains(failure.Message, "(timeout failure)") || !failure.Retryable {
		t.Errorf("stalled CONNECT failure = %#v", failure)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Errorf("stalled CONNECT timeout took %s, want under 1s", elapsed)
	}
	select {
	case <-connectRead:
	default:
		t.Fatal("proxy did not receive CONNECT before the timeout")
	}
}
