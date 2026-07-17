package resthttp

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

// productionDispatcher is deliberately HTTP/1.1-only. The pinned Undici
// configuration disables transparent content decoding and tunnels every
// selected HTTP(S) proxy route. Owning the serializer here also preserves the
// reviewed WHATWG request-target bytes that net/http.URL cannot represent.
type productionDispatcher struct {
	selector  *proxySelector
	tlsConfig *tls.Config
	dialer    net.Dialer
}

func newProductionDispatcher(configuration *tls.Config, selector *proxySelector) *productionDispatcher {
	return &productionDispatcher{
		selector:  selector,
		tlsConfig: configuration.Clone(),
		dialer: net.Dialer{
			Timeout:   10 * time.Second,
			KeepAlive: 30 * time.Second,
		},
	}
}

func (*productionDispatcher) CloseIdleConnections() {}

func defaultPort(scheme string) string {
	switch scheme {
	case "http":
		return "80"
	case "https":
		return "443"
	default:
		return ""
	}
}

func endpointAddress(target *url.URL) (string, error) {
	if target == nil || target.Hostname() == "" {
		return "", errors.New("HTTP endpoint has no hostname")
	}
	port := target.Port()
	if port == "" {
		port = defaultPort(strings.ToLower(target.Scheme))
	}
	if port == "" {
		return "", errors.New("HTTP endpoint has an unsupported scheme")
	}
	return net.JoinHostPort(target.Hostname(), port), nil
}

func (d *productionDispatcher) tlsClient(
	ctx context.Context,
	connection net.Conn,
	target *url.URL,
	serverName string,
) (net.Conn, error) {
	configuration := d.tlsConfig.Clone()
	configuration.ServerName = serverName
	if configuration.ServerName == "" {
		configuration.ServerName = target.Hostname()
	}
	secured := tls.Client(connection, configuration)
	if err := secured.HandshakeContext(ctx); err != nil {
		_ = connection.Close()
		return nil, err
	}
	return secured, nil
}

func (d *productionDispatcher) dialEndpoint(
	ctx context.Context,
	target *url.URL,
	serverName string,
) (net.Conn, error) {
	address, err := endpointAddress(target)
	if err != nil {
		return nil, err
	}
	connection, err := d.dialer.DialContext(ctx, "tcp", address)
	if err != nil {
		return nil, err
	}
	if deadline, ok := ctx.Deadline(); ok {
		if err := connection.SetDeadline(deadline); err != nil {
			_ = connection.Close()
			return nil, err
		}
	}
	if strings.EqualFold(target.Scheme, "https") {
		return d.tlsClient(ctx, connection, target, serverName)
	}
	return connection, nil
}

type bufferedConnection struct {
	net.Conn
	reader *bufio.Reader
}

func (c *bufferedConnection) Read(output []byte) (int, error) {
	return c.reader.Read(output)
}

func proxyAuthorization(proxy *url.URL) string {
	if proxy == nil || proxy.User == nil {
		return ""
	}
	password, hasPassword := proxy.User.Password()
	if !hasPassword || proxy.User.Username() == "" || password == "" {
		return ""
	}
	credentials := proxy.User.Username() + ":" + password
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(credentials))
}

func connectHostHeader(target *url.URL) string {
	if target == nil {
		return ""
	}
	return target.Host
}

func (d *productionDispatcher) tunnel(
	ctx context.Context,
	connection net.Conn,
	target *url.URL,
	proxy *url.URL,
	targetServerName string,
) (net.Conn, error) {
	authority, err := endpointAddress(target)
	if err != nil {
		_ = connection.Close()
		return nil, err
	}
	writer := bufio.NewWriter(connection)
	if _, err = fmt.Fprintf(writer, "CONNECT %s HTTP/1.1\r\nhost: %s\r\nconnection: close\r\n", authority, connectHostHeader(target)); err != nil {
		_ = connection.Close()
		return nil, err
	}
	if authorization := proxyAuthorization(proxy); authorization != "" {
		if _, err = fmt.Fprintf(writer, "proxy-authorization: %s\r\n", authorization); err != nil {
			_ = connection.Close()
			return nil, err
		}
	}
	if _, err = io.WriteString(writer, "proxy-connection: keep-alive\r\n\r\n"); err != nil {
		_ = connection.Close()
		return nil, err
	}
	if err = writer.Flush(); err != nil {
		_ = connection.Close()
		return nil, err
	}

	reader := bufio.NewReader(connection)
	head, err := readProductionResponseHead(reader)
	if err != nil {
		_ = connection.Close()
		return nil, err
	}
	if head.statusCode == http.StatusContinue {
		_ = connection.Close()
		return nil, errors.New("unsolicited HTTP 100 proxy response")
	}
	if head.statusCode != http.StatusOK {
		// Proxy rejection is terminal. Close before touching any advertised
		// framing so a keep-alive response cannot make cleanup drain or stall.
		_ = connection.Close()
		return nil, fmt.Errorf("proxy response (%d) is not 200 when HTTP tunneling", head.statusCode)
	}
	if reader.Buffered() != 0 {
		connection = &bufferedConnection{Conn: connection, reader: reader}
	}
	if strings.EqualFold(target.Scheme, "https") {
		return d.tlsClient(ctx, connection, target, targetServerName)
	}
	return connection, nil
}

func (d *productionDispatcher) openConnection(
	ctx context.Context,
	target *url.URL,
	proxy *url.URL,
	targetServerName string,
) (net.Conn, error) {
	if proxy == nil {
		return d.dialEndpoint(ctx, target, targetServerName)
	}
	connection, err := d.dialEndpoint(ctx, proxy, proxy.Hostname())
	if err != nil {
		return nil, err
	}
	return d.tunnel(ctx, connection, target, proxy, targetServerName)
}

func validHTTPToken(value string) bool {
	if value == "" {
		return false
	}
	for index := 0; index < len(value); index++ {
		character := value[index]
		if character >= '0' && character <= '9' || character >= 'A' && character <= 'Z' ||
			character >= 'a' && character <= 'z' || strings.ContainsRune("!#$%&'*+-.^_`|~", rune(character)) {
			continue
		}
		return false
	}
	return true
}

func validHTTPHeaderValue(value string) bool {
	for _, character := range value {
		if character == '\t' || character >= 0x20 && character <= 0x7e ||
			character >= 0x80 && character <= 0xff {
			continue
		}
		return false
	}
	return true
}

func nodeLatin1(input string) []byte {
	output := make([]byte, 0, len(input))
	for _, value := range input {
		output = append(output, byte(value))
	}
	return output
}

func requestWireHost(request *http.Request) string {
	if request != nil {
		for name, values := range request.Header {
			if strings.EqualFold(name, "host") && len(values) != 0 {
				return values[0]
			}
		}
		if request.Host != "" {
			return request.Host
		}
		if request.URL != nil {
			return request.URL.Host
		}
	}
	return ""
}

func targetTLSServerName(host string, target *url.URL) (string, error) {
	serverName := host
	if strings.HasPrefix(serverName, "[") {
		closing := strings.IndexByte(serverName, ']')
		if closing < 0 {
			return "", errors.New("invalid Host header")
		}
		serverName = serverName[1:closing]
	} else if colon := strings.IndexByte(serverName, ':'); colon >= 0 {
		serverName = serverName[:colon]
	}
	if serverName == "" || net.ParseIP(serverName) != nil {
		if target == nil {
			return "", nil
		}
		return target.Hostname(), nil
	}
	return serverName, nil
}

func requestTarget(request *http.Request) (string, error) {
	if request == nil || request.URL == nil {
		return "", errors.New("HTTP request URL is missing")
	}
	path := request.URL.Opaque
	if path == "" {
		path = rawWHATWGPath(request.URL)
	}
	if path == "" {
		path = "/"
	}
	if path[0] != '/' {
		return "", errors.New("HTTP request path must start with a slash")
	}
	for index := 0; index < len(path); index++ {
		if path[index] <= 0x20 || path[index] == 0x7f {
			return "", errors.New("HTTP request path contains an invalid byte")
		}
	}
	if request.URL.ForceQuery || request.URL.RawQuery != "" {
		path += "?" + request.URL.RawQuery
	}
	return path, nil
}

func validateProductionWireRequest(request *http.Request) error {
	if request == nil || !validHTTPToken(request.Method) {
		return errors.New("invalid HTTP request method")
	}
	if _, err := requestTarget(request); err != nil {
		return err
	}
	host := requestWireHost(request)
	if !validHTTPHeaderValue(host) {
		return errors.New("invalid Host header")
	}
	if _, err := targetTLSServerName(host, request.URL); err != nil {
		return err
	}
	for name, values := range request.Header {
		if !validHTTPToken(name) {
			return errors.New("invalid HTTP header name")
		}
		for _, value := range values {
			if !validHTTPHeaderValue(value) {
				return errors.New("invalid HTTP header value")
			}
		}
	}
	return nil
}

func (d *productionDispatcher) writeRequest(connection net.Conn, request *http.Request) error {
	if err := validateProductionWireRequest(request); err != nil {
		return err
	}
	target, err := requestTarget(request)
	if err != nil {
		return err
	}
	host := requestWireHost(request)

	writer := bufio.NewWriter(connection)
	if _, err = writer.WriteString(request.Method + " " + target + " HTTP/1.1\r\nhost: "); err != nil {
		return err
	}
	if _, err = writer.Write(nodeLatin1(host)); err != nil {
		return err
	}
	connectionValue := "keep-alive"
	if request.Close {
		connectionValue = "close"
	}
	if _, err = writer.WriteString("\r\nconnection: " + connectionValue + "\r\n"); err != nil {
		return err
	}

	names := make([]string, 0, len(request.Header))
	for name := range request.Header {
		names = append(names, name)
	}
	sort.Slice(names, func(left, right int) bool {
		return strings.ToLower(names[left]) < strings.ToLower(names[right])
	})
	for _, name := range names {
		lower := strings.ToLower(name)
		if lower == "host" || lower == "content-length" {
			continue
		}
		for _, value := range request.Header[name] {
			if _, err = writer.WriteString(name + ": "); err != nil {
				return err
			}
			if _, err = writer.Write(nodeLatin1(value)); err != nil {
				return err
			}
			if _, err = writer.WriteString("\r\n"); err != nil {
				return err
			}
		}
	}

	if request.ContentLength >= 0 && (request.ContentLength != 0 || strings.EqualFold(request.Method, http.MethodPost)) {
		if _, err = writer.WriteString("content-length: " + strconv.FormatInt(request.ContentLength, 10) + "\r\n"); err != nil {
			return err
		}
	}
	if _, err = writer.WriteString("\r\n"); err != nil {
		return err
	}
	if request.Body != nil {
		if _, err = io.Copy(writer, request.Body); err != nil {
			return err
		}
	}
	return writer.Flush()
}

func (d *productionDispatcher) RoundTrip(request *http.Request) (*http.Response, error) {
	if request == nil || request.URL == nil {
		return nil, errors.New("HTTP request URL is missing")
	}
	proxy, err := d.selector.proxyURL(request.URL)
	if err != nil {
		return nil, err
	}
	if proxy != nil && hasHTTPHeader(request.Header, "proxy-authorization") {
		return nil, errors.New("Proxy-Authorization should be sent in ProxyAgent constructor")
	}
	if err := validateProductionWireRequest(request); err != nil {
		return nil, err
	}
	host := requestWireHost(request)
	targetServerName, err := targetTLSServerName(host, request.URL)
	if err != nil {
		return nil, err
	}
	connection, err := d.openConnection(request.Context(), request.URL, proxy, targetServerName)
	if err != nil {
		return nil, err
	}
	if deadline, ok := request.Context().Deadline(); ok {
		if err := connection.SetDeadline(deadline); err != nil {
			_ = connection.Close()
			return nil, err
		}
	}
	if err = d.writeRequest(connection, request); err != nil {
		_ = connection.Close()
		return nil, err
	}
	reader := bufio.NewReader(connection)
	response, err := readProductionResponse(connection, reader, request)
	if err != nil {
		_ = connection.Close()
		return nil, err
	}
	return response, nil
}

func hasHTTPHeader(headers http.Header, wanted string) bool {
	for name := range headers {
		if strings.EqualFold(name, wanted) {
			return true
		}
	}
	return false
}
