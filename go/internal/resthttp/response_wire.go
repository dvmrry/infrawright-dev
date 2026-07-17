package resthttp

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
)

// undiciMaxResponseHeaderSize is Undici Client's Node-default
// http.maxHeaderSize. Undici counts decoded field-name and field-value bytes
// and rejects once the running total reaches this value.
const undiciMaxResponseHeaderSize = 16 * 1024

// productionMaxChunkSizeLineSize is a deliberate fail-closed bound. Undici
// 7.28 accepts substantially larger chunk extensions, but leaving this framing
// line unbounded lets a peer consume the entire request deadline before one
// response-body byte becomes available.
const productionMaxChunkSizeLineSize = 16 * 1024

var errResponseHeadersOverflow = errors.New("HTTP response headers overflow")
var errResponseChunkSizeLineOverflow = errors.New("HTTP response chunk-size line overflow")

type productionResponseHead struct {
	statusCode       int
	protoMajor       int
	protoMinor       int
	headers          http.Header
	contentLength    uint64
	hasContentLength bool
	chunked          bool
}

func readStatusToken(reader *bufio.Reader, delimiter byte, maximum int) (string, error) {
	output := make([]byte, 0, maximum)
	for {
		value, err := reader.ReadByte()
		if err != nil {
			return "", err
		}
		if value == delimiter {
			return string(output), nil
		}
		if value == '\r' || value == '\n' || len(output) == maximum {
			return "", errors.New("invalid HTTP response status line")
		}
		output = append(output, value)
	}
}

func consumeStatusReason(reader *bufio.Reader) error {
	for {
		value, err := reader.ReadByte()
		if err != nil {
			return err
		}
		if value == '\r' {
			next, nextErr := reader.ReadByte()
			if nextErr != nil {
				return nextErr
			}
			if next != '\n' {
				return errors.New("invalid HTTP response status line ending")
			}
			return nil
		}
		if value == '\n' || (value < 0x20 && value != '\t') || value == 0x7f {
			return errors.New("invalid HTTP response status text")
		}
	}
}

func readProductionStatusLine(reader *bufio.Reader) (int, int, int, error) {
	protocol, err := readStatusToken(reader, ' ', len("HTTP/1.1"))
	if err != nil {
		return 0, 0, 0, err
	}
	major, minor := 0, 0
	switch protocol {
	case "HTTP/1.0":
		major = 1
	case "HTTP/1.1":
		major, minor = 1, 1
	default:
		return 0, 0, 0, errors.New("unsupported HTTP response protocol")
	}

	statusBytes := make([]byte, 0, 3)
	for {
		value, readErr := reader.ReadByte()
		if readErr != nil {
			return 0, 0, 0, readErr
		}
		if value == ' ' || value == '\r' {
			if len(statusBytes) != 3 {
				return 0, 0, 0, errors.New("invalid HTTP response status code")
			}
			status, parseErr := strconv.Atoi(string(statusBytes))
			if parseErr != nil {
				return 0, 0, 0, errors.New("invalid HTTP response status code")
			}
			if value == '\r' {
				next, nextErr := reader.ReadByte()
				if nextErr != nil {
					return 0, 0, 0, nextErr
				}
				if next != '\n' {
					return 0, 0, 0, errors.New("invalid HTTP response status line ending")
				}
				return status, major, minor, nil
			}
			if err = consumeStatusReason(reader); err != nil {
				return 0, 0, 0, err
			}
			return status, major, minor, nil
		}
		if value < '0' || value > '9' || len(statusBytes) == 3 {
			return 0, 0, 0, errors.New("invalid HTTP response status code")
		}
		statusBytes = append(statusBytes, value)
	}
}

func readProductionHeader(
	reader *bufio.Reader,
	headerSize *int,
) (string, string, bool, error) {
	first, err := reader.ReadByte()
	if err != nil {
		return "", "", false, err
	}
	if first == '\r' {
		next, nextErr := reader.ReadByte()
		if nextErr != nil {
			return "", "", false, nextErr
		}
		if next != '\n' {
			return "", "", false, errors.New("invalid HTTP response header ending")
		}
		return "", "", true, nil
	}
	if first == ' ' || first == '\t' {
		return "", "", false, errors.New("obsolete folded HTTP response header")
	}

	name := make([]byte, 0, 32)
	value := first
	for {
		if value == ':' {
			break
		}
		if !isHTTPTokenByte(value) {
			return "", "", false, errors.New("invalid HTTP response header name")
		}
		(*headerSize)++
		if *headerSize >= undiciMaxResponseHeaderSize {
			return "", "", false, errResponseHeadersOverflow
		}
		name = append(name, value)
		value, err = reader.ReadByte()
		if err != nil {
			return "", "", false, err
		}
	}
	if len(name) == 0 {
		return "", "", false, errors.New("empty HTTP response header name")
	}

	valueBytes := make([]byte, 0, 64)
	leading := true
	for {
		value, err = reader.ReadByte()
		if err != nil {
			return "", "", false, err
		}
		if value == '\r' {
			next, nextErr := reader.ReadByte()
			if nextErr != nil {
				return "", "", false, nextErr
			}
			if next != '\n' {
				return "", "", false, errors.New("invalid HTTP response header ending")
			}
			return string(name), string(valueBytes), false, nil
		}
		if value == '\n' || (value < 0x20 && value != '\t') || value == 0x7f {
			return "", "", false, errors.New("invalid HTTP response header value")
		}
		if leading && (value == ' ' || value == '\t') {
			continue
		}
		leading = false
		(*headerSize)++
		if *headerSize >= undiciMaxResponseHeaderSize {
			return "", "", false, errResponseHeadersOverflow
		}
		valueBytes = append(valueBytes, value)
	}
}

func isHTTPTokenByte(value byte) bool {
	return value >= '0' && value <= '9' || value >= 'A' && value <= 'Z' ||
		value >= 'a' && value <= 'z' || strings.ContainsRune("!#$%&'*+-.^_`|~", rune(value))
}

func parseResponseContentLength(value string) (uint64, error) {
	// llhttp discards SP/HTAB before the first digit but, after digits begin,
	// permits trailing SP only. A trailing HTAB is not equivalent to OWS here.
	canonical := strings.TrimLeft(value, " \t")
	canonical = strings.TrimRight(canonical, " ")
	if canonical == "" {
		return 0, errors.New("empty HTTP response content length")
	}
	for index := 0; index < len(canonical); index++ {
		if canonical[index] < '0' || canonical[index] > '9' {
			return 0, errors.New("invalid HTTP response content length")
		}
	}
	length, err := strconv.ParseUint(canonical, 10, 64)
	if err != nil {
		return 0, errors.New("HTTP response content length overflows uint64")
	}
	return length, nil
}

func equalASCIIFoldBytes(value, expected string) bool {
	if len(value) != len(expected) {
		return false
	}
	for index := range value {
		left := value[index]
		if left >= 'A' && left <= 'Z' {
			left += 'a' - 'A'
		}
		if left != expected[index] {
			return false
		}
	}
	return true
}

func responseUsesChunkedEncoding(values []string) bool {
	chunked := false
	for _, value := range values {
		// llhttp does not revisit its Transfer-Encoding state for an empty or
		// OWS-only repeated field, so such a field preserves the preceding
		// nonempty field's result.
		start := 0
		for start < len(value) && (value[start] == ' ' || value[start] == '\t') {
			start++
		}
		if start == len(value) {
			continue
		}

		// llhttp treats commas as separators even inside otherwise opaque
		// syntax. It recognizes the final candidate only when it is exactly
		// "chunked" (ASCII case-insensitive), allowing leading OWS and
		// trailing SP but not trailing HTAB.
		candidate := value[start:]
		if comma := strings.LastIndexByte(candidate, ','); comma >= 0 {
			candidate = candidate[comma+1:]
		}
		for len(candidate) != 0 && (candidate[0] == ' ' || candidate[0] == '\t') {
			candidate = candidate[1:]
		}
		for len(candidate) != 0 && candidate[len(candidate)-1] == ' ' {
			candidate = candidate[:len(candidate)-1]
		}
		chunked = equalASCIIFoldBytes(candidate, "chunked")
	}
	return chunked
}

func readProductionResponseHead(reader *bufio.Reader) (productionResponseHead, error) {
	status, major, minor, err := readProductionStatusLine(reader)
	if err != nil {
		return productionResponseHead{}, err
	}
	head := productionResponseHead{
		statusCode: status,
		protoMajor: major,
		protoMinor: minor,
		headers:    make(http.Header),
	}
	headerSize := 0
	seenContentLength := false
	seenNonemptyTransferEncoding := false
	for {
		name, value, complete, readErr := readProductionHeader(reader, &headerSize)
		if readErr != nil {
			return productionResponseHead{}, readErr
		}
		if complete {
			break
		}
		canonicalName := http.CanonicalHeaderKey(name)
		head.headers[canonicalName] = append(head.headers[canonicalName], value)
		switch canonicalName {
		case "Content-Length":
			if seenContentLength {
				return productionResponseHead{}, errors.New("duplicate HTTP response content length")
			}
			if seenNonemptyTransferEncoding {
				return productionResponseHead{}, errors.New("HTTP response has both transfer encoding and content length")
			}
			head.contentLength, err = parseResponseContentLength(value)
			if err != nil {
				return productionResponseHead{}, err
			}
			head.hasContentLength = true
			seenContentLength = true
		case "Transfer-Encoding":
			// llhttp rejects any Transfer-Encoding field after Content-Length,
			// including an empty one. In the opposite order an empty/OWS-only
			// field does not set its transfer-encoding flag, so Content-Length
			// remains valid.
			if seenContentLength {
				return productionResponseHead{}, errors.New("HTTP response has both transfer encoding and content length")
			}
			if value != "" {
				seenNonemptyTransferEncoding = true
			}
		}
	}

	transferEncodings := head.headers.Values("Transfer-Encoding")
	if len(transferEncodings) != 0 {
		head.chunked = responseUsesChunkedEncoding(transferEncodings)
	}
	return head, nil
}

type exactLengthReader struct {
	reader    io.Reader
	remaining uint64
}

func (r *exactLengthReader) Read(output []byte) (int, error) {
	if r.remaining == 0 {
		return 0, io.EOF
	}
	if uint64(len(output)) > r.remaining {
		output = output[:r.remaining]
	}
	read, err := r.reader.Read(output)
	r.remaining -= uint64(read)
	if err == io.EOF && r.remaining != 0 {
		return read, io.ErrUnexpectedEOF
	}
	return read, err
}

type productionResponseBody struct {
	reader     io.Reader
	connection net.Conn
	once       sync.Once
}

type productionChunkedReader struct {
	reader       *bufio.Reader
	remaining    uint64
	needChunkEnd bool
	finished     bool
}

func newProductionChunkedReader(reader *bufio.Reader) io.Reader {
	return &productionChunkedReader{reader: reader}
}

func readExpectedCRLF(reader *bufio.Reader) error {
	carriageReturn, err := reader.ReadByte()
	if err != nil {
		return err
	}
	lineFeed, err := reader.ReadByte()
	if err != nil {
		return err
	}
	if carriageReturn != '\r' || lineFeed != '\n' {
		return errors.New("invalid chunk data ending")
	}
	return nil
}

type productionChunkExtensionState uint8

const (
	productionChunkSizeDigits productionChunkExtensionState = iota
	productionChunkExtensionName
	productionChunkExtensionValue
	productionChunkExtensionQuotedValue
	productionChunkExtensionQuotedPair
	productionChunkExtensionQuotedDone
)

func chunkSizeHexDigit(value byte) (uint64, bool) {
	switch {
	case value >= '0' && value <= '9':
		return uint64(value - '0'), true
	case value >= 'A' && value <= 'F':
		return uint64(value-'A') + 10, true
	case value >= 'a' && value <= 'f':
		return uint64(value-'a') + 10, true
	default:
		return 0, false
	}
}

func chunkSizeLineCanEnd(
	state productionChunkExtensionState,
	hasDigit bool,
	extensionNameLength int,
) bool {
	switch state {
	case productionChunkSizeDigits:
		return hasDigit
	case productionChunkExtensionName:
		return extensionNameLength != 0
	case productionChunkExtensionValue, productionChunkExtensionQuotedDone:
		return true
	default:
		return false
	}
}

func readProductionChunkSize(reader *bufio.Reader) (uint64, error) {
	state := productionChunkSizeDigits
	var length uint64
	hasDigit := false
	extensionNameLength := 0
	lineSize := 0
	for {
		value, err := reader.ReadByte()
		if err != nil {
			return 0, err
		}
		if value == '\r' {
			if !chunkSizeLineCanEnd(state, hasDigit, extensionNameLength) {
				return 0, errors.New("invalid HTTP response chunk extension")
			}
			next, nextErr := reader.ReadByte()
			if nextErr != nil {
				return 0, nextErr
			}
			if next != '\n' {
				return 0, errors.New("invalid chunk-size line ending")
			}
			break
		}
		if value == '\n' {
			return 0, errors.New("invalid chunk-size line")
		}
		lineSize++
		if lineSize >= productionMaxChunkSizeLineSize {
			return 0, errResponseChunkSizeLineOverflow
		}

		switch state {
		case productionChunkSizeDigits:
			if value == ';' {
				if !hasDigit {
					return 0, errors.New("empty HTTP response chunk size")
				}
				state = productionChunkExtensionName
				extensionNameLength = 0
				continue
			}
			digit, ok := chunkSizeHexDigit(value)
			if !ok {
				return 0, errors.New("invalid HTTP response chunk size")
			}
			if length > (math.MaxUint64-digit)/16 {
				return 0, errors.New("HTTP response chunk size overflows uint64")
			}
			length = length*16 + digit
			hasDigit = true
		case productionChunkExtensionName:
			switch {
			case isHTTPTokenByte(value):
				extensionNameLength++
			case value == '=':
				state = productionChunkExtensionValue
			case value == ';':
				state = productionChunkExtensionName
				extensionNameLength = 0
			default:
				return 0, errors.New("invalid HTTP response chunk extension name")
			}
		case productionChunkExtensionValue:
			switch {
			case isHTTPTokenByte(value):
			case value == '"':
				state = productionChunkExtensionQuotedValue
			case value == ';':
				state = productionChunkExtensionName
				extensionNameLength = 0
			default:
				return 0, errors.New("invalid HTTP response chunk extension value")
			}
		case productionChunkExtensionQuotedValue:
			switch {
			case value == '"':
				state = productionChunkExtensionQuotedDone
			case value == '\\':
				state = productionChunkExtensionQuotedPair
			case value == '\t' || value >= 0x20:
			default:
				return 0, errors.New("invalid HTTP response quoted chunk extension value")
			}
		case productionChunkExtensionQuotedPair:
			if (value != '\t' && value < 0x20) || value == 0x7f {
				return 0, errors.New("invalid HTTP response quoted chunk extension escape")
			}
			state = productionChunkExtensionQuotedValue
		case productionChunkExtensionQuotedDone:
			if value != ';' {
				return 0, errors.New("invalid HTTP response chunk extension after quoted value")
			}
			state = productionChunkExtensionName
			extensionNameLength = 0
		}
	}
	return length, nil
}

func discardProductionTrailers(reader *bufio.Reader) error {
	headerSize := 0
	for {
		_, _, complete, err := readProductionHeader(reader, &headerSize)
		if err != nil {
			return err
		}
		if complete {
			return nil
		}
	}
}

func (r *productionChunkedReader) nextChunk() error {
	if r.needChunkEnd {
		if err := readExpectedCRLF(r.reader); err != nil {
			return err
		}
		r.needChunkEnd = false
	}
	length, err := readProductionChunkSize(r.reader)
	if err != nil {
		return err
	}
	if length == 0 {
		if err = discardProductionTrailers(r.reader); err != nil {
			return err
		}
		r.finished = true
		return nil
	}
	r.remaining = length
	r.needChunkEnd = true
	return nil
}

func (r *productionChunkedReader) Read(output []byte) (int, error) {
	for r.remaining == 0 && !r.finished {
		if err := r.nextChunk(); err != nil {
			return 0, err
		}
	}
	if r.finished {
		return 0, io.EOF
	}
	if uint64(len(output)) > r.remaining {
		output = output[:r.remaining]
	}
	read, err := r.reader.Read(output)
	r.remaining -= uint64(read)
	if err == io.EOF && r.remaining != 0 {
		return read, io.ErrUnexpectedEOF
	}
	return read, err
}

func (b *productionResponseBody) Read(output []byte) (int, error) {
	return b.reader.Read(output)
}

func (b *productionResponseBody) Close() error {
	var err error
	b.once.Do(func() {
		// Undici destroys a response body by closing its socket immediately.
		// Closing the socket first prevents any framing reader from draining a
		// caller-abandoned keep-alive response.
		err = b.connection.Close()
	})
	return err
}

func responseHasNoBody(status int, method string) bool {
	return strings.EqualFold(method, http.MethodHead) || status == http.StatusNoContent || status == http.StatusNotModified
}

func productionResponse(
	connection net.Conn,
	reader *bufio.Reader,
	request *http.Request,
	head productionResponseHead,
) *http.Response {
	var bodyReader io.Reader
	contentLength := int64(-1)
	transferEncoding := []string(nil)
	switch {
	case responseHasNoBody(head.statusCode, request.Method):
		bodyReader = strings.NewReader("")
		contentLength = 0
	case head.hasContentLength:
		bodyReader = &exactLengthReader{reader: reader, remaining: head.contentLength}
		if head.contentLength <= math.MaxInt64 {
			contentLength = int64(head.contentLength)
		}
	case head.chunked:
		bodyReader = newProductionChunkedReader(reader)
		transferEncoding = []string{"chunked"}
	default:
		bodyReader = reader
	}
	return &http.Response{
		Status:           fmt.Sprintf("%d %s", head.statusCode, http.StatusText(head.statusCode)),
		StatusCode:       head.statusCode,
		Proto:            fmt.Sprintf("HTTP/%d.%d", head.protoMajor, head.protoMinor),
		ProtoMajor:       head.protoMajor,
		ProtoMinor:       head.protoMinor,
		Header:           head.headers,
		Body:             &productionResponseBody{reader: bodyReader, connection: connection},
		ContentLength:    contentLength,
		TransferEncoding: transferEncoding,
		Request:          request,
	}
}

func readProductionResponse(
	connection net.Conn,
	reader *bufio.Reader,
	request *http.Request,
) (*http.Response, error) {
	for {
		head, err := readProductionResponseHead(reader)
		if err != nil {
			return nil, err
		}
		if head.statusCode < 100 {
			return nil, errors.New("invalid HTTP response status below 100")
		}
		if head.statusCode == http.StatusContinue {
			return nil, errors.New("unsolicited HTTP 100 response")
		}
		if head.statusCode == http.StatusSwitchingProtocols {
			return nil, errors.New("unsolicited HTTP upgrade response")
		}
		if head.statusCode < 200 {
			continue
		}
		return productionResponse(connection, reader, request, head), nil
	}
}
