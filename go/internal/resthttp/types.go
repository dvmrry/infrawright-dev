package resthttp

import (
	"net/http"

	"github.com/dvmrry/infrawright-dev/go/internal/collectors"
)

const (
	// RESTHTTPTimeoutMs ports REST_HTTP_TIMEOUT_MS from
	// node-src/io/rest-http-transport.ts.
	RESTHTTPTimeoutMs = 30_000
	// RESTHTTPResponseLimitBytes ports REST_HTTP_RESPONSE_LIMIT_BYTES from
	// node-src/io/rest-http-transport.ts.
	RESTHTTPResponseLimitBytes = 64 * 1024 * 1024
)

const (
	caBundleLimitBytes       = 4 * 1024 * 1024
	defaultMaxRedirects      = 10
	maximumMaxRedirects      = 20
	maximumRequestTimeoutMs  = 10 * 60 * 1000
	maximumResponseLimitByte = 1024 * 1024 * 1024
)

// RestProxyEnvironment ports RestProxyEnvironment from
// node-src/io/rest-http-transport.ts. Values are detached snapshots, not live
// process-environment lookups.
type RestProxyEnvironment struct {
	HTTPProxy  string
	HTTPSProxy string
	NoProxy    string
}

// HTTPAttemptPerformance ports the recordHttpAttempt input used by
// node-src/io/rest-http-transport.ts.
type HTTPAttemptPerformance struct {
	Context    collectors.HTTPRequestPerformanceContext
	DurationMs float64
	Status     *int
}

// HTTPRetryPerformance ports the recordHttpRetry input used by
// node-src/io/rest-http-transport.ts.
type HTTPRetryPerformance struct {
	Context collectors.HTTPRequestPerformanceContext
	DelayMs float64
	Status  int
}

// PerformanceRecorder is the transport's narrow seam onto the eventual Go
// port of node-src/performance/recorder.ts. Implementations must be safe for
// concurrent calls because HttpTransport.Request is concurrent-use safe.
type PerformanceRecorder interface {
	Now() float64
	DurationSince(startedMs float64) float64
	RecordHTTPAttempt(input HTTPAttemptPerformance) error
	RecordHTTPRetry(input HTTPRetryPerformance) error
}

// RestHTTPTransportOptions ports RestHttpTransportOptions from
// node-src/io/rest-http-transport.ts. Pointer scalar fields preserve the
// source distinction between omission and explicit zero: zero redirects is
// valid, while zero timeout/response limit is an error.
//
// RoundTripper, Close, and Destroy are test seams corresponding to the Node
// source's createDispatcher/httpRequest seams. Production callers leave them
// nil. An injected RoundTripper must be safe for concurrent use.
type RestHTTPTransportOptions struct {
	IncludeCustomCA    *bool
	MaxRedirects       *int
	RequestTimeoutMs   *int
	ResponseLimitBytes *int
	RoundTripper       http.RoundTripper
	Close              func() error
	Destroy            func() error
	Performance        PerformanceRecorder
	Sleep              func(milliseconds float64) error
}
