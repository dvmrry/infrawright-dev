package resthttp

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/url"
	"sync"
	"testing"

	"github.com/dvmrry/infrawright-dev/go/internal/procerr"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func response(text string, status int, headers http.Header) *http.Response {
	if headers == nil {
		headers = make(http.Header)
	}
	return &http.Response{
		StatusCode: status,
		Header:     headers,
		Body:       io.NopCloser(bytes.NewBufferString(text)),
	}
}

func mustURL(t *testing.T, value string) *url.URL {
	t.Helper()
	parsed, err := url.Parse(value)
	if err != nil {
		t.Fatalf("url.Parse(%q) failed: %v", value, err)
	}
	return parsed
}

func intPointer(value int) *int { return &value }

func boolPointer(value bool) *bool { return &value }

func requireProcessFailure(t *testing.T, err error, code string) *procerr.ProcessFailure {
	t.Helper()
	var failure *procerr.ProcessFailure
	if !errors.As(err, &failure) {
		t.Fatalf("error = %T %v, want *procerr.ProcessFailure with code %q", err, err, code)
	}
	if failure.Code != code {
		t.Fatalf("ProcessFailure.Code = %q, want %q (message %q)", failure.Code, code, failure.Message)
	}
	return failure
}

type trackingBody struct {
	mu     sync.Mutex
	reader io.Reader
	closed int
}

func (b *trackingBody) Read(buffer []byte) (int, error) {
	return b.reader.Read(buffer)
}

func (b *trackingBody) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.closed++
	return nil
}

func (b *trackingBody) closeCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.closed
}
