package collectors

import "errors"

// http_status_error.go ports the original implementation: an
// HTTP failure that retains its status code separately from the
// operator-facing error text wrapped around it.

// HTTPStatusError ports the CollectorHttpStatusError class from
// the original implementation: an error carrying an HTTP
// status alongside its message, so callers that need the raw status (to
// decide FAILED vs SKIPPED against a resource's optional_http_statuses, or
// to build failureHints) don't have to parse it back out of the message
// text.
type HTTPStatusError struct {
	message string
	Status  int
}

// NewHTTPStatusError constructs an *HTTPStatusError, ported from the
// CollectorHttpStatusError constructor.
func NewHTTPStatusError(message string, status int) *HTTPStatusError {
	return &HTTPStatusError{message: message, Status: status}
}

// Error implements the error interface.
func (e *HTTPStatusError) Error() string { return e.message }

// CollectorHTTPStatus ports collectorHttpStatus from
// the original implementation: it reports the HTTP status
// carried by err if err is (or wraps) an *HTTPStatusError, or ok=false
// otherwise -- the Go analogue of the TS function returning `number | null`
// via `error instanceof CollectorHttpStatusError`.
func CollectorHTTPStatus(err error) (status int, ok bool) {
	var statusErr *HTTPStatusError
	if !errors.As(err, &statusErr) {
		return 0, false
	}
	return statusErr.Status, true
}
