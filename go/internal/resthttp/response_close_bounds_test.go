package resthttp

import (
	"bufio"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/dvmrry/infrawright-dev/go/internal/collectors"
)

func waitForProductionPeerClose(connection net.Conn) error {
	if err := connection.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
		return err
	}
	buffer := make([]byte, 1)
	_, err := connection.Read(buffer)
	return err
}

func TestProductionChunkExtensionLimitClosesBeforeLineEnding(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	serverErr := make(chan error, 1)
	go func() {
		connection, acceptErr := listener.Accept()
		if acceptErr != nil {
			serverErr <- acceptErr
			return
		}
		defer connection.Close()
		if _, readErr := readCapturedWireRequest(bufio.NewReader(connection)); readErr != nil {
			serverErr <- readErr
			return
		}
		if _, writeErr := io.WriteString(connection,
			"HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\n\r\n1;"+
				strings.Repeat("a", productionMaxChunkSizeLineSize),
		); writeErr != nil {
			serverErr <- writeErr
			return
		}
		serverErr <- waitForProductionPeerClose(connection)
	}()
	timeout := 2000
	transport, err := CreateRestHTTPTransport(collectors.Environment{}, RestHTTPTransportOptions{
		RequestTimeoutMs: &timeout,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = transport.Close() })
	started := time.Now()
	_, err = transport.Request(collectors.HTTPRequest{
		Method: "GET", URL: mustURL(t, "http://"+listener.Addr().String()+"/"),
	})
	requireProcessFailure(t, err, "REST_HTTP_TRANSPORT_FAILED")
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Errorf("chunk-extension overflow cleanup took %s, want under 1s", elapsed)
	}
	if err = <-serverErr; err == nil {
		t.Fatal("peer close read unexpectedly succeeded")
	} else if timeoutError, ok := err.(net.Error); ok && timeoutError.Timeout() {
		t.Fatalf("client waited for the oversized chunk extension to end: %v", err)
	}
}

func TestProductionDeclaredResponseLimitClosesStalledKeepAliveImmediately(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	serverErr := make(chan error, 1)
	go func() {
		connection, acceptErr := listener.Accept()
		if acceptErr != nil {
			serverErr <- acceptErr
			return
		}
		defer connection.Close()
		if _, readErr := readCapturedWireRequest(bufio.NewReader(connection)); readErr != nil {
			serverErr <- readErr
			return
		}
		if _, writeErr := io.WriteString(connection,
			"HTTP/1.1 200 OK\r\nContent-Length: 5\r\nConnection: keep-alive\r\n\r\n",
		); writeErr != nil {
			serverErr <- writeErr
			return
		}
		serverErr <- waitForProductionPeerClose(connection)
	}()
	limit, timeout := 4, 2000
	transport, err := CreateRestHTTPTransport(collectors.Environment{}, RestHTTPTransportOptions{
		ResponseLimitBytes: &limit, RequestTimeoutMs: &timeout,
	})
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	_, err = transport.Request(collectors.HTTPRequest{
		Method: "GET", URL: mustURL(t, "http://"+listener.Addr().String()+"/"),
	})
	requireProcessFailure(t, err, "REST_HTTP_RESPONSE_LIMIT")
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Errorf("declared response limit cleanup took %s, want under 1s", elapsed)
	}
	if err = <-serverErr; err == nil {
		t.Fatal("peer close read unexpectedly succeeded")
	} else if timeoutError, ok := err.(net.Error); ok && timeoutError.Timeout() {
		t.Fatalf("client did not immediately close declared-limit response: %v", err)
	}
}

func TestProductionStreamingResponseLimitClosesStalledKeepAliveImmediately(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	serverErr := make(chan error, 1)
	go func() {
		connection, acceptErr := listener.Accept()
		if acceptErr != nil {
			serverErr <- acceptErr
			return
		}
		defer connection.Close()
		if _, readErr := readCapturedWireRequest(bufio.NewReader(connection)); readErr != nil {
			serverErr <- readErr
			return
		}
		if _, writeErr := io.WriteString(connection,
			"HTTP/1.1 200 OK\r\nTransfer-Encoding: identity\r\nConnection: keep-alive\r\n\r\n12345",
		); writeErr != nil {
			serverErr <- writeErr
			return
		}
		serverErr <- waitForProductionPeerClose(connection)
	}()
	limit, timeout := 4, 2000
	transport, err := CreateRestHTTPTransport(collectors.Environment{}, RestHTTPTransportOptions{
		ResponseLimitBytes: &limit, RequestTimeoutMs: &timeout,
	})
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	_, err = transport.Request(collectors.HTTPRequest{
		Method: "GET", URL: mustURL(t, "http://"+listener.Addr().String()+"/"),
	})
	requireProcessFailure(t, err, "REST_HTTP_RESPONSE_LIMIT")
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Errorf("streaming response limit cleanup took %s, want under 1s", elapsed)
	}
	if err = <-serverErr; err == nil {
		t.Fatal("peer close read unexpectedly succeeded")
	} else if timeoutError, ok := err.(net.Error); ok && timeoutError.Timeout() {
		t.Fatalf("client did not immediately close streaming-limit response: %v", err)
	}
}

func TestProductionRedirectClosesStalledKeepAliveBodyBeforeNextWire(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	serverErr := make(chan error, 1)
	go func() {
		first, acceptErr := listener.Accept()
		if acceptErr != nil {
			serverErr <- acceptErr
			return
		}
		if _, readErr := readCapturedWireRequest(bufio.NewReader(first)); readErr != nil {
			_ = first.Close()
			serverErr <- readErr
			return
		}
		if _, writeErr := io.WriteString(first,
			"HTTP/1.1 302 Found\r\nLocation: /next\r\nContent-Length: 100\r\n"+
				"Connection: keep-alive\r\n\r\n",
		); writeErr != nil {
			_ = first.Close()
			serverErr <- writeErr
			return
		}
		closeErr := waitForProductionPeerClose(first)
		_ = first.Close()
		if timeoutError, ok := closeErr.(net.Error); ok && timeoutError.Timeout() {
			serverErr <- closeErr
			return
		}

		second, acceptErr := listener.Accept()
		if acceptErr != nil {
			serverErr <- acceptErr
			return
		}
		defer second.Close()
		request, readErr := readCapturedWireRequest(bufio.NewReader(second))
		if readErr != nil {
			serverErr <- readErr
			return
		}
		if request.line != "GET /next HTTP/1.1" {
			serverErr <- io.ErrUnexpectedEOF
			return
		}
		_, writeErr := second.Write(rawHTTPResponse(200, nil, "[]"))
		serverErr <- writeErr
	}()
	timeout := 2000
	transport, err := CreateRestHTTPTransport(collectors.Environment{}, RestHTTPTransportOptions{
		RequestTimeoutMs: &timeout,
	})
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	response, err := transport.Request(collectors.HTTPRequest{
		Method: "GET", URL: mustURL(t, "http://"+listener.Addr().String()+"/start"),
	})
	if err != nil {
		t.Fatalf("Request() failed: %v", err)
	}
	if string(response.Body) != "[]" {
		t.Errorf("response body = %q, want []", response.Body)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Errorf("redirect cleanup took %s, want under 1s", elapsed)
	}
	if err = <-serverErr; err != nil {
		t.Fatalf("redirect capture failed: %v", err)
	}
}

func TestProductionProxyRejectionClosesStalledKeepAliveBodyImmediately(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	serverErr := make(chan error, 1)
	go func() {
		connection, acceptErr := listener.Accept()
		if acceptErr != nil {
			serverErr <- acceptErr
			return
		}
		defer connection.Close()
		if _, readErr := readCapturedWireRequest(bufio.NewReader(connection)); readErr != nil {
			serverErr <- readErr
			return
		}
		if _, writeErr := io.WriteString(connection,
			"HTTP/1.1 407 Proxy Authentication Required\r\nContent-Length: 100\r\n"+
				"Connection: keep-alive\r\n\r\n",
		); writeErr != nil {
			serverErr <- writeErr
			return
		}
		serverErr <- waitForProductionPeerClose(connection)
	}()
	timeout := 2000
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
	requireProcessFailure(t, err, "REST_HTTP_TRANSPORT_FAILED")
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Errorf("proxy rejection cleanup took %s, want under 1s", elapsed)
	}
	if err = <-serverErr; err == nil {
		t.Fatal("peer close read unexpectedly succeeded")
	} else if timeoutError, ok := err.(net.Error); ok && timeoutError.Timeout() {
		t.Fatalf("client did not immediately close proxy rejection: %v", err)
	}
}
