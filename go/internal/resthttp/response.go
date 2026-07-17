package resthttp

import (
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
)

var canonicalContentLength = regexp.MustCompile(`^(?:0|[1-9][0-9]*)$`)

func latin1HeaderValue(input string) string {
	var output strings.Builder
	output.Grow(len(input))
	for index := 0; index < len(input); index++ {
		output.WriteRune(rune(input[index]))
	}
	return output.String()
}

func firstHeader(headers http.Header, wanted string) (string, bool) {
	for name, values := range headers {
		if strings.EqualFold(name, wanted) {
			if len(values) == 0 {
				return "", false
			}
			return values[0], true
		}
	}
	return "", false
}

func normalizedResponseHeaders(headers http.Header, fromProductionWire bool) map[string][]string {
	output := make(map[string][]string, len(headers))
	for name, values := range headers {
		lower := strings.ToLower(name)
		for _, value := range values {
			if fromProductionWire {
				value = latin1HeaderValue(value)
			}
			output[lower] = append(output[lower], value)
		}
	}
	return output
}

func closeBody(body io.ReadCloser) {
	if body != nil {
		_ = body.Close()
	}
}

func readBoundedBody(response *http.Response, limit int64) ([]byte, error) {
	if response == nil || response.Body == nil {
		return nil, ioFailure("INVALID_REST_HTTP_RESPONSE", "HTTP response body is invalid")
	}
	if declared, ok := firstHeader(response.Header, "content-length"); ok && canonicalContentLength.MatchString(declared) {
		length, err := strconv.ParseInt(declared, 10, 64)
		if err != nil || length > limit {
			closeBody(response.Body)
			return nil, ioFailure(
				"REST_HTTP_RESPONSE_LIMIT",
				"HTTP response exceeded the collection size limit",
			)
		}
	}

	reader := &io.LimitedReader{R: response.Body, N: limit + 1}
	body, err := io.ReadAll(reader)
	closeBody(response.Body)
	if err != nil {
		return nil, ioFailure(
			"REST_HTTP_TRANSPORT_FAILED",
			"HTTP response body could not be read",
			true,
		)
	}
	if int64(len(body)) > limit {
		return nil, ioFailure(
			"REST_HTTP_RESPONSE_LIMIT",
			"HTTP response exceeded the collection size limit",
		)
	}
	return append([]byte(nil), body...), nil
}

func redirectStatus(status int) bool {
	return status == 301 || status == 302 || status == 303 || status == 307 || status == 308
}
