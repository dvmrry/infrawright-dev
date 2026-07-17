package resthttp

import (
	"bufio"
	"errors"
	"io"
	"net"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dvmrry/infrawright-dev/go/internal/collectors"
)

func TestProductionResponseHeadRejectsUndiciInvalidForms(t *testing.T) {
	for _, tc := range []struct {
		name string
		raw  string
	}{
		{
			name: "unsolicited 100",
			raw: "HTTP/1.1 100 Continue\r\n\r\n" +
				"HTTP/1.1 200 OK\r\nContent-Length: 2\r\n\r\nok",
		},
		{
			name: "unsolicited 101",
			raw: "HTTP/1.1 101 Switching Protocols\r\n" +
				"Connection: upgrade\r\nUpgrade: websocket\r\n\r\n",
		},
		{
			name: "status 000 before valid response",
			raw: "HTTP/1.1 000 Invalid\r\n\r\n" +
				"HTTP/1.1 200 OK\r\nContent-Length: 2\r\n\r\nok",
		},
		{
			name: "status 020 before valid response",
			raw: "HTTP/1.1 020 Invalid\r\n\r\n" +
				"HTTP/1.1 200 OK\r\nContent-Length: 2\r\n\r\nok",
		},
		{
			name: "status 099 before valid response",
			raw: "HTTP/1.1 099 Invalid\r\n\r\n" +
				"HTTP/1.1 200 OK\r\nContent-Length: 2\r\n\r\nok",
		},
		{
			name: "duplicate content length",
			raw:  "HTTP/1.1 200 OK\r\nContent-Length: 2\r\nContent-Length: 2\r\n\r\nok",
		},
		{
			name: "transfer encoding and content length",
			raw: "HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\n" +
				"Content-Length: 2\r\n\r\n2\r\nok\r\n0\r\n\r\n",
		},
		{
			name: "obsolete line folding",
			raw:  "HTTP/1.1 200 OK\r\nX-Test: one\r\n two\r\nContent-Length: 2\r\n\r\nok",
		},
		{
			name: "signed content length",
			raw:  "HTTP/1.1 200 OK\r\nContent-Length: +2\r\n\r\nok",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := startRawHTTPServer(t, rawHTTPExchange{response: []byte(tc.raw)})
			transport, err := CreateRestHTTPTransport(collectors.Environment{}, RestHTTPTransportOptions{})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = transport.Close() })
			_, err = transport.Request(collectors.HTTPRequest{
				Method: "GET", URL: mustURL(t, "http://"+server.address+"/"),
			})
			requireProcessFailure(t, err, "REST_HTTP_TRANSPORT_FAILED")
			awaitRawServer(t, server, "GET / HTTP/1.1")
		})
	}
}

func TestProductionResponseAcceptsNonChunkedTransferEncoding(t *testing.T) {
	server := startRawHTTPServer(t, rawHTTPExchange{response: []byte(
		"HTTP/1.1 200 OK\r\nTransfer-Encoding: identity\r\n\r\nok",
	)})
	transport, err := CreateRestHTTPTransport(collectors.Environment{}, RestHTTPTransportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = transport.Close() })
	response, err := transport.Request(collectors.HTTPRequest{
		Method: "GET", URL: mustURL(t, "http://"+server.address+"/"),
	})
	if err != nil {
		t.Fatalf("Request() failed: %v", err)
	}
	if string(response.Body) != "ok" {
		t.Errorf("response body = %q, want ok", response.Body)
	}
	if got := response.Headers["transfer-encoding"]; !slices.Equal(got, []string{"identity"}) {
		t.Errorf("Transfer-Encoding = %#v, want identity", got)
	}
	awaitRawServer(t, server, "GET / HTTP/1.1")
}

func TestProductionResponseTransferEncodingMatchesUndiciRawByteClassification(t *testing.T) {
	literalChunkStream := []byte("2\r\nok\r\n0\r\n\r\n")
	for _, tc := range []struct {
		name     string
		headers  [][]byte
		wantBody []byte
	}{
		{name: "UTF-8-looking NBSP suffix is opaque", headers: [][]byte{append([]byte("chunked"), 0xc2, 0xa0)}, wantBody: literalChunkStream},
		{name: "UTF-8-looking EM SPACE suffix is opaque", headers: [][]byte{append([]byte("chunked"), 0xe2, 0x80, 0x83)}, wantBody: literalChunkStream},
		{name: "trailing HTAB is opaque", headers: [][]byte{[]byte("chunked\t")}, wantBody: literalChunkStream},
		{name: "space inside opaque token", headers: [][]byte{[]byte("foo bar")}, wantBody: literalChunkStream},
		{name: "semicolon inside opaque token", headers: [][]byte{[]byte("foo;bar")}, wantBody: literalChunkStream},
		{name: "parameter-like opaque token", headers: [][]byte{[]byte("gzip; level=1")}, wantBody: literalChunkStream},
		{name: "chunked with parameter is opaque", headers: [][]byte{[]byte("chunked;foo=bar")}, wantBody: literalChunkStream},
		{name: "chunked before final coding is opaque", headers: [][]byte{[]byte("chunked, gzip")}, wantBody: literalChunkStream},
		{name: "space before final empty candidate is opaque", headers: [][]byte{[]byte("chunked ,")}, wantBody: literalChunkStream},
		{name: "two final empty candidates are opaque", headers: [][]byte{[]byte("chunked,,")}, wantBody: literalChunkStream},
		{name: "nonfinal chunked without OWS is opaque", headers: [][]byte{[]byte("chunked,gzip")}, wantBody: literalChunkStream},
		{name: "chunked prefix without comma is opaque", headers: [][]byte{[]byte("chunked;foo")}, wantBody: literalChunkStream},
		{name: "trailing SP then HTAB is opaque", headers: [][]byte{[]byte("chunked \t")}, wantBody: literalChunkStream},
		{name: "leading empty candidate is chunked", headers: [][]byte{[]byte(",chunked")}, wantBody: []byte("ok")},
		{name: "opaque parameter then final chunked", headers: [][]byte{[]byte("gzip ; level=1, chunked")}, wantBody: []byte("ok")},
		{name: "two empty candidates before chunked", headers: [][]byte{[]byte("gzip,,chunked")}, wantBody: []byte("ok")},
		{name: "repeated chunked candidates", headers: [][]byte{[]byte("chunked,chunked")}, wantBody: []byte("ok")},
		{name: "ASCII case folding", headers: [][]byte{[]byte("ChUnKeD")}, wantBody: []byte("ok")},
		{name: "trailing SP is chunked", headers: [][]byte{[]byte("chunked  ")}, wantBody: []byte("ok")},
		{name: "leading OWS after comma", headers: [][]byte{[]byte("gzip, \tchunked")}, wantBody: []byte("ok")},
		{name: "comma in opaque quote-like bytes is separator", headers: [][]byte{[]byte("gzip;foo=\",chunked")}, wantBody: []byte("ok")},
		{name: "later nonempty field replaces chunked", headers: [][]byte{[]byte("chunked"), []byte("identity")}, wantBody: literalChunkStream},
		{name: "later chunked field replaces identity", headers: [][]byte{[]byte("identity"), []byte("chunked")}, wantBody: []byte("ok")},
		{name: "later empty field preserves chunked", headers: [][]byte{[]byte("chunked"), nil}, wantBody: []byte("ok")},
		{name: "later OWS-only field preserves chunked", headers: [][]byte{[]byte("chunked"), []byte(" \t ")}, wantBody: []byte("ok")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := []byte("HTTP/1.1 200 OK\r\n")
			for _, value := range tc.headers {
				raw = append(raw, []byte("Transfer-Encoding: ")...)
				raw = append(raw, value...)
				raw = append(raw, '\r', '\n')
			}
			raw = append(raw, '\r', '\n')
			raw = append(raw, literalChunkStream...)

			server := startRawHTTPServer(t, rawHTTPExchange{response: raw})
			transport, err := CreateRestHTTPTransport(collectors.Environment{}, RestHTTPTransportOptions{})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = transport.Close() })
			response, err := transport.Request(collectors.HTTPRequest{
				Method: "GET", URL: mustURL(t, "http://"+server.address+"/"),
			})
			if err != nil {
				t.Fatalf("Request() failed: %v", err)
			}
			if !slices.Equal(response.Body, tc.wantBody) {
				t.Errorf("response body = %q, want exact bytes %q", response.Body, tc.wantBody)
			}
			awaitRawServer(t, server, "GET / HTTP/1.1")
		})
	}
}

func TestProductionResponseTransferEncodingContentLengthOrderingMatchesUndici(t *testing.T) {
	for _, tc := range []struct {
		name    string
		headers string
		wantErr bool
	}{
		{name: "empty transfer encoding before content length", headers: "Transfer-Encoding:\r\nContent-Length: 2\r\n"},
		{name: "OWS-only transfer encoding before content length", headers: "Transfer-Encoding: \t \r\nContent-Length: 2\r\n"},
		{name: "repeated empty transfer encoding before content length", headers: "Transfer-Encoding:\r\nTransfer-Encoding: \t\r\nContent-Length: 2\r\n"},
		{name: "content length before empty transfer encoding", headers: "Content-Length: 2\r\nTransfer-Encoding:\r\n", wantErr: true},
		{name: "content length before OWS-only transfer encoding", headers: "Content-Length: 2\r\nTransfer-Encoding: \t \r\n", wantErr: true},
		{name: "nonempty transfer encoding before content length", headers: "Transfer-Encoding: identity\r\nContent-Length: 2\r\n", wantErr: true},
		{name: "nonempty then empty transfer encoding before content length", headers: "Transfer-Encoding: chunked\r\nTransfer-Encoding:\r\nContent-Length: 2\r\n", wantErr: true},
		{name: "content length before nonempty transfer encoding", headers: "Content-Length: 2\r\nTransfer-Encoding: identity\r\n", wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := "HTTP/1.1 200 OK\r\n" + tc.headers + "\r\nok"
			server := startRawHTTPServer(t, rawHTTPExchange{response: []byte(raw)})
			transport, err := CreateRestHTTPTransport(collectors.Environment{}, RestHTTPTransportOptions{})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = transport.Close() })
			response, err := transport.Request(collectors.HTTPRequest{
				Method: "GET", URL: mustURL(t, "http://"+server.address+"/"),
			})
			if tc.wantErr {
				requireProcessFailure(t, err, "REST_HTTP_TRANSPORT_FAILED")
			} else {
				if err != nil {
					t.Fatalf("Request() failed: %v", err)
				}
				if string(response.Body) != "ok" {
					t.Errorf("response body = %q, want ok", response.Body)
				}
			}
			awaitRawServer(t, server, "GET / HTTP/1.1")
		})
	}
}

func TestProductionResponseContentLengthWhitespaceMatchesUndici(t *testing.T) {
	for _, tc := range []struct {
		name    string
		value   string
		wantErr bool
	}{
		{name: "leading mixed OWS", value: " \t 2"},
		{name: "one trailing SP", value: "2 "},
		{name: "two trailing SP", value: "2  "},
		{name: "trailing HTAB", value: "2\t", wantErr: true},
		{name: "SP then trailing HTAB", value: "2 \t", wantErr: true},
		{name: "HTAB then trailing SP", value: "2\t ", wantErr: true},
		{name: "internal SP", value: "2 3", wantErr: true},
		{name: "internal HTAB", value: "2\t3", wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := "HTTP/1.1 200 OK\r\nContent-Length:" + tc.value + "\r\n\r\nok"
			server := startRawHTTPServer(t, rawHTTPExchange{response: []byte(raw)})
			transport, err := CreateRestHTTPTransport(collectors.Environment{}, RestHTTPTransportOptions{})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = transport.Close() })
			response, err := transport.Request(collectors.HTTPRequest{
				Method: "GET", URL: mustURL(t, "http://"+server.address+"/"),
			})
			if tc.wantErr {
				requireProcessFailure(t, err, "REST_HTTP_TRANSPORT_FAILED")
			} else {
				if err != nil {
					t.Fatalf("Request() failed: %v", err)
				}
				if string(response.Body) != "ok" {
					t.Errorf("response body = %q, want ok", response.Body)
				}
			}
			awaitRawServer(t, server, "GET / HTTP/1.1")
		})
	}
}

func TestProductionChunkExtensionsMatchUndiciGrammar(t *testing.T) {
	for _, tc := range []struct {
		name      string
		extension []byte
		wantErr   bool
	}{
		{name: "bare semicolon", extension: []byte(";"), wantErr: true},
		{name: "semicolon then space", extension: []byte("; "), wantErr: true},
		{name: "semicolon then space and name", extension: []byte("; a"), wantErr: true},
		{name: "quote where name starts", extension: []byte(";\""), wantErr: true},
		{name: "trailing empty name", extension: []byte(";;"), wantErr: true},
		{name: "second equals in unquoted value", extension: []byte(";foo==bar"), wantErr: true},
		{name: "unterminated quoted value", extension: []byte(";foo=\"bar"), wantErr: true},
		{name: "HTAB before name", extension: []byte(";\tfoo"), wantErr: true},
		{name: "space in unquoted value", extension: []byte(";foo=bar baz"), wantErr: true},
		{name: "bytes after quoted value", extension: []byte(";foo=\"bar\"x"), wantErr: true},
		{name: "SP after quoted value", extension: []byte(";foo=\"bar\" "), wantErr: true},
		{name: "HTAB after quoted value", extension: []byte(";foo=\"bar\"\t"), wantErr: true},
		{name: "unterminated quoted pair", extension: []byte(";foo=\"bar\\"), wantErr: true},
		{name: "escaped DEL", extension: []byte{';', 'f', 'o', 'o', '=', '"', '\\', 0x7f, '"'}, wantErr: true},
		{name: "name without value", extension: []byte(";foo")},
		{name: "empty unquoted value", extension: []byte(";foo=")},
		{name: "empty value before next extension", extension: []byte(";foo=;bar")},
		{name: "empty name and value", extension: []byte(";=")},
		{name: "unquoted value", extension: []byte(";foo=bar")},
		{name: "quoted value", extension: []byte(";foo=\"bar\"")},
		{name: "empty name with value", extension: []byte(";=x")},
		{name: "empty name between semicolons", extension: []byte(";;foo")},
		{name: "multiple extensions", extension: []byte(";foo;bar=baz")},
		{name: "quoted whitespace", extension: []byte(";foo=\"bar baz\tqux\"")},
		{name: "quoted pair", extension: []byte(";foo=\"bar\\\"baz\"")},
		{name: "escaped obs-text", extension: []byte{';', 'f', 'o', 'o', '=', '"', '\\', 0x80, '"'}},
		{name: "quote after unquoted prefix", extension: []byte(";foo=bar\"baz\"")},
		{name: "obs-text inside quote", extension: []byte{';', 'f', 'o', 'o', '=', '"', 0x80, '"'}},
		{name: "DEL inside quote", extension: []byte{';', 'f', 'o', 'o', '=', '"', 0x7f, '"'}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := []byte("HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\n\r\n1")
			raw = append(raw, tc.extension...)
			raw = append(raw, []byte("\r\nx\r\n0\r\n\r\n")...)
			server := startRawHTTPServer(t, rawHTTPExchange{response: raw})
			transport, err := CreateRestHTTPTransport(collectors.Environment{}, RestHTTPTransportOptions{})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = transport.Close() })
			response, err := transport.Request(collectors.HTTPRequest{
				Method: "GET", URL: mustURL(t, "http://"+server.address+"/"),
			})
			if tc.wantErr {
				requireProcessFailure(t, err, "REST_HTTP_TRANSPORT_FAILED")
			} else {
				if err != nil {
					t.Fatalf("Request() failed: %v", err)
				}
				if string(response.Body) != "x" {
					t.Errorf("response body = %q, want x", response.Body)
				}
			}
			awaitRawServer(t, server, "GET / HTTP/1.1")
		})
	}
}

func TestProductionChunkSizeUsesCheckedUint64Accumulation(t *testing.T) {
	for _, tc := range []struct {
		name      string
		chunkSize string
		wantErr   bool
	}{
		{name: "17 digits with leading zeros", chunkSize: strings.Repeat("0", 16) + "1"},
		{name: "101 digits with leading zeros", chunkSize: strings.Repeat("0", 100) + "1"},
		{name: "uint64 overflow", chunkSize: "10000000000000000", wantErr: true},
		{name: "uint64 overflow after leading zero", chunkSize: "010000000000000000", wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := "HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\n\r\n" +
				tc.chunkSize + "\r\nx\r\n0\r\n\r\n"
			server := startRawHTTPServer(t, rawHTTPExchange{response: []byte(raw)})
			transport, err := CreateRestHTTPTransport(collectors.Environment{}, RestHTTPTransportOptions{})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = transport.Close() })
			response, err := transport.Request(collectors.HTTPRequest{
				Method: "GET", URL: mustURL(t, "http://"+server.address+"/"),
			})
			if tc.wantErr {
				requireProcessFailure(t, err, "REST_HTTP_TRANSPORT_FAILED")
			} else {
				if err != nil {
					t.Fatalf("Request() failed: %v", err)
				}
				if string(response.Body) != "x" {
					t.Errorf("response body = %q, want x", response.Body)
				}
			}
			awaitRawServer(t, server, "GET / HTTP/1.1")
		})
	}
}

func TestProductionChunkSizeAcceptsUint64Maximum(t *testing.T) {
	length, err := readProductionChunkSize(bufio.NewReader(strings.NewReader("ffffffffffffffff\r\n")))
	if err != nil {
		t.Fatalf("readProductionChunkSize() failed: %v", err)
	}
	if length != ^uint64(0) {
		t.Errorf("readProductionChunkSize() = %d, want uint64 maximum", length)
	}
}

func TestProductionNumericChunkSizeLineUsesFailClosedBoundary(t *testing.T) {
	for _, tc := range []struct {
		name         string
		digitCount   int
		wantOverflow bool
	}{
		{name: "16383 numeric bytes accepted", digitCount: 16383},
		{name: "16384 numeric bytes rejected", digitCount: 16384, wantOverflow: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			line := strings.Repeat("0", tc.digitCount) + "\r\n"
			length, err := readProductionChunkSize(bufio.NewReader(strings.NewReader(line)))
			if tc.wantOverflow {
				if !errors.Is(err, errResponseChunkSizeLineOverflow) {
					t.Fatalf("readProductionChunkSize() error = %v, want overflow", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("readProductionChunkSize() failed: %v", err)
			}
			if length != 0 {
				t.Errorf("readProductionChunkSize() = %d, want 0", length)
			}
		})
	}
}

func TestProductionResponseSkipsAllowedInformationalHead(t *testing.T) {
	for _, statusLine := range []string{
		"102 Processing",
		"103 Early Hints",
		"199 Informational",
	} {
		t.Run(statusLine, func(t *testing.T) {
			server := startRawHTTPServer(t, rawHTTPExchange{response: []byte(
				"HTTP/1.1 " + statusLine + "\r\nLink: </preload>\r\n\r\n" +
					"HTTP/1.1 200 OK\r\nContent-Length: 2\r\n\r\nok",
			)})
			transport, err := CreateRestHTTPTransport(collectors.Environment{}, RestHTTPTransportOptions{})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = transport.Close() })
			response, err := transport.Request(collectors.HTTPRequest{
				Method: "GET", URL: mustURL(t, "http://"+server.address+"/"),
			})
			if err != nil {
				t.Fatalf("Request() failed: %v", err)
			}
			if string(response.Body) != "ok" || response.Status != http.StatusOK {
				t.Errorf("response = status %d body %q, want 200/ok", response.Status, response.Body)
			}
			if _, exists := response.Headers["link"]; exists {
				t.Errorf("informational Link leaked into final headers: %#v", response.Headers["link"])
			}
			awaitRawServer(t, server, "GET / HTTP/1.1")
		})
	}
}

func TestProductionResponsePreservesInitialHeadersWhileFramingChunkedBody(t *testing.T) {
	raw := "HTTP/1.1 200 OK\r\n" +
		"Connection: close\r\n" +
		"Transfer-Encoding: chunked\r\n" +
		"Trailer: x-check\r\n" +
		"Pragma: no-cache\r\n\r\n" +
		"2\r\nok\r\n0\r\nX-Check: done\r\n\r\n"
	server := startRawHTTPServer(t, rawHTTPExchange{response: []byte(raw)})
	transport, err := CreateRestHTTPTransport(collectors.Environment{}, RestHTTPTransportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = transport.Close() })
	response, err := transport.Request(collectors.HTTPRequest{
		Method: "GET", URL: mustURL(t, "http://"+server.address+"/"),
	})
	if err != nil {
		t.Fatalf("Request() failed: %v", err)
	}
	if string(response.Body) != "ok" {
		t.Errorf("response body = %q, want ok", response.Body)
	}
	for name, want := range map[string][]string{
		"connection":        {"close"},
		"transfer-encoding": {"chunked"},
		"trailer":           {"x-check"},
		"pragma":            {"no-cache"},
	} {
		if got := response.Headers[name]; !slices.Equal(got, want) {
			t.Errorf("%s = %#v, want %#v", name, got, want)
		}
	}
	if _, exists := response.Headers["cache-control"]; exists {
		t.Errorf("Cache-Control was synthesized: %#v", response.Headers["cache-control"])
	}
	awaitRawServer(t, server, "GET / HTTP/1.1")
}

func TestProductionResponseHeaderSizeMatchesUndiciBoundary(t *testing.T) {
	for _, tc := range []struct {
		name    string
		length  int
		wantErr bool
	}{
		{name: "16383 name-value bytes accepted", length: 16382},
		{name: "16384 name-value bytes rejected", length: 16383, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := "HTTP/1.1 200 OK\r\nx:" + strings.Repeat("a", tc.length) + "\r\n\r\n"
			server := startRawHTTPServer(t, rawHTTPExchange{response: []byte(raw)})
			transport, err := CreateRestHTTPTransport(collectors.Environment{}, RestHTTPTransportOptions{})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = transport.Close() })
			response, err := transport.Request(collectors.HTTPRequest{
				Method: "GET", URL: mustURL(t, "http://"+server.address+"/"),
			})
			if tc.wantErr {
				requireProcessFailure(t, err, "REST_HTTP_TRANSPORT_FAILED")
			} else {
				if err != nil {
					t.Fatalf("Request() failed: %v", err)
				}
				if got := len(response.Headers["x"][0]); got != tc.length {
					t.Errorf("len(X) = %d, want %d", got, tc.length)
				}
			}
			awaitRawServer(t, server, "GET / HTTP/1.1")
		})
	}
}

func TestProductionConnectResponseHeaderSizeMatchesUndiciBoundary(t *testing.T) {
	for _, tc := range []struct {
		name      string
		length    int
		wantInner bool
	}{
		{name: "16383 name-value bytes accepted", length: 16382, wantInner: true},
		{name: "16384 name-value bytes rejected", length: 16383},
	} {
		t.Run(tc.name, func(t *testing.T) {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = listener.Close() })
			innerRead := make(chan bool, 1)
			serverErr := make(chan error, 1)
			go func() {
				connection, acceptErr := listener.Accept()
				if acceptErr != nil {
					serverErr <- acceptErr
					return
				}
				defer connection.Close()
				reader := bufio.NewReader(connection)
				if _, readErr := readCapturedWireRequest(reader); readErr != nil {
					serverErr <- readErr
					return
				}
				response := "HTTP/1.1 200 OK\r\nx:" + strings.Repeat("a", tc.length) + "\r\n\r\n"
				if _, writeErr := io.WriteString(connection, response); writeErr != nil {
					serverErr <- writeErr
					return
				}
				_, readErr := readCapturedWireRequest(reader)
				innerRead <- readErr == nil
				if readErr == nil {
					_, readErr = connection.Write(rawHTTPResponse(200, nil, "[]"))
				}
				if readErr != nil && tc.wantInner {
					serverErr <- readErr
					return
				}
				serverErr <- nil
			}()
			transport, err := CreateRestHTTPTransport(
				collectors.Environment{"HTTP_PROXY": "http://" + listener.Addr().String()},
				RestHTTPTransportOptions{},
			)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = transport.Close() })
			_, requestErr := transport.Request(collectors.HTTPRequest{
				Method: "GET", URL: mustURL(t, "http://collector.invalid/"),
			})
			if tc.wantInner {
				if requestErr != nil {
					t.Fatalf("Request() failed: %v", requestErr)
				}
			} else {
				requireProcessFailure(t, requestErr, "REST_HTTP_TRANSPORT_FAILED")
			}
			if got := <-innerRead; got != tc.wantInner {
				t.Errorf("inner request read = %t, want %t", got, tc.wantInner)
			}
			if err = <-serverErr; err != nil {
				t.Fatalf("proxy capture failed: %v", err)
			}
		})
	}
}

func TestProductionChunkTrailerHeaderSizeUsesFailClosedBoundary(t *testing.T) {
	for _, tc := range []struct {
		name    string
		length  int
		wantErr bool
	}{
		{name: "16383 name-value bytes accepted", length: 16382},
		{name: "16384 name-value bytes rejected", length: 16383, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := "HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\n\r\n" +
				"0\r\nx:" + strings.Repeat("a", tc.length) + "\r\n\r\n"
			server := startRawHTTPServer(t, rawHTTPExchange{response: []byte(raw)})
			transport, err := CreateRestHTTPTransport(collectors.Environment{}, RestHTTPTransportOptions{})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = transport.Close() })
			_, err = transport.Request(collectors.HTTPRequest{
				Method: "GET", URL: mustURL(t, "http://"+server.address+"/"),
			})
			if tc.wantErr {
				requireProcessFailure(t, err, "REST_HTTP_TRANSPORT_FAILED")
			} else if err != nil {
				t.Fatalf("Request() failed: %v", err)
			}
			awaitRawServer(t, server, "GET / HTTP/1.1")
		})
	}
}

func TestProductionChunkSizeLineUsesFailClosedBoundary(t *testing.T) {
	for _, tc := range []struct {
		name            string
		extensionLength int
		wantOverflow    bool
	}{
		{name: "16383 line bytes accepted", extensionLength: 16381},
		{name: "16384 line bytes rejected", extensionLength: 16382, wantOverflow: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			line := "1;" + strings.Repeat("a", tc.extensionLength) + "\r\n"
			length, err := readProductionChunkSize(bufio.NewReader(strings.NewReader(line)))
			if tc.wantOverflow {
				if !errors.Is(err, errResponseChunkSizeLineOverflow) {
					t.Fatalf("readProductionChunkSize() error = %v, want overflow", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("readProductionChunkSize() failed: %v", err)
			}
			if length != 1 {
				t.Errorf("readProductionChunkSize() = %d, want 1", length)
			}
		})
	}
}

func TestProductionUint64ContentLengthClassifiesAsResponseLimit(t *testing.T) {
	for _, value := range []string{"9223372036854775808", "18446744073709551615"} {
		t.Run(value, func(t *testing.T) {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = listener.Close() })
			clientClosed := make(chan struct{})
			go func() {
				connection, acceptErr := listener.Accept()
				if acceptErr != nil {
					close(clientClosed)
					return
				}
				defer connection.Close()
				reader := bufio.NewReader(connection)
				_, _ = readCapturedWireRequest(reader)
				_, _ = io.WriteString(connection, "HTTP/1.1 200 OK\r\nContent-Length: "+value+"\r\n\r\n")
				buffer := make([]byte, 1)
				_, _ = connection.Read(buffer)
				close(clientClosed)
			}()
			transport, err := CreateRestHTTPTransport(collectors.Environment{}, RestHTTPTransportOptions{})
			if err != nil {
				t.Fatal(err)
			}
			started := time.Now()
			_, err = transport.Request(collectors.HTTPRequest{
				Method: "GET", URL: mustURL(t, "http://"+listener.Addr().String()+"/"),
			})
			requireProcessFailure(t, err, "REST_HTTP_RESPONSE_LIMIT")
			if elapsed := time.Since(started); elapsed > time.Second {
				t.Errorf("uint64 Content-Length limit took %s, want under 1s", elapsed)
			}
			select {
			case <-clientClosed:
			case <-time.After(time.Second):
				t.Fatal("client did not close the oversized Content-Length response")
			}
		})
	}
}
