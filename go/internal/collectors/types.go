// Package collectors implements registry-driven REST collection, including
// selection, provider-source authority, retries, diagnostics, and the built-in
// Zscaler product adapters. Compiled adapters own authentication and URL
// composition; callers supply an HttpTransport for wire I/O.
package collectors

import "net/url"

// CollectorAuthMode identifies the authentication mode used by an adapter.
type CollectorAuthMode string

const (
	// AuthModeLegacy selects product-specific legacy authentication.
	AuthModeLegacy CollectorAuthMode = "legacy"
	// AuthModeOneAPI selects OneAPI authentication.
	AuthModeOneAPI CollectorAuthMode = "oneapi"
)

// Environment provides collector credentials and configuration. Callers can
// distinguish an absent key from a present empty value with a two-result map
// lookup.
type Environment = map[string]string

// CollectorContext holds tenant- and product-specific values derived during
// authentication. Empty optional fields use their product defaults.
type CollectorContext struct {
	Cloud         string
	CustomerID    string
	ZiaLegacyBase string
	ZpaCloud      string
	ZpaLegacyBase string
}

// HTTPRequestClassification labels a request for performance reporting.
type HTTPRequestClassification string

const (
	ClassificationAction         HTTPRequestClassification = "action"
	ClassificationAuthentication HTTPRequestClassification = "authentication"
	ClassificationDetail         HTTPRequestClassification = "detail"
	ClassificationList           HTTPRequestClassification = "list"
)

// HTTPRequestPerformanceContext describes the operation represented by a
// request. Empty Product and ResourceFamily values mean unspecified.
type HTTPRequestPerformanceContext struct {
	Classification HTTPRequestClassification
	EndpointFamily string
	Phase          string
	Product        string
	ResourceFamily string
}

// HTTPRequest is the collector transport request. Method is GET or POST, a nil
// Body means no request body, and TimeoutMs == 0 selects the transport default.
type HTTPRequest struct {
	Method      string
	URL         *url.URL
	Headers     map[string]string
	Body        []byte
	TimeoutMs   int
	Performance *HTTPRequestPerformanceContext
}

// HTTPResponse is the collector transport response. Headers use net/http's
// multi-value representation.
type HTTPResponse struct {
	Status  int
	Headers map[string][]string
	Body    []byte
}

// HttpTransport performs collector HTTP requests. Implementations must be safe
// for concurrent Request calls when FetchResourcesOptions.Concurrency is
// greater than one. Close should be a no-op when there are no resources to
// release.
type HttpTransport interface {
	Request(request HTTPRequest) (HTTPResponse, error)
	Close() error
}

// CollectorAuthContext contains headers produced by adapter authentication.
type CollectorAuthContext struct {
	Headers map[string]string
}

// CollectorAcquireInput contains the inputs needed to authenticate a product.
// A nil NowMs uses the real clock; a nil PerformanceContext disables auth
// performance metadata.
type CollectorAcquireInput struct {
	Mode               CollectorAuthMode
	Environment        Environment
	Context            CollectorContext
	Transport          HttpTransport
	NowMs              *int64
	PerformanceContext *AuthPerformanceContext
}

// AuthPerformanceContext labels an adapter authentication request. The adapter
// supplies the classification and endpoint family.
type AuthPerformanceContext struct {
	Phase          string
	Product        string
	ResourceFamily string
}

// CollectorComposeUrlInput contains the inputs needed to compose a product URL.
type CollectorComposeUrlInput struct {
	Mode    CollectorAuthMode
	Context CollectorContext
	Path    string
}

// CollectorAdapter owns product-specific authentication and URL composition.
// Pack metadata cannot add an adapter at runtime; callers inject the compiled
// adapters they are willing to execute.
type CollectorAdapter struct {
	Product    string
	Acquire    func(input CollectorAcquireInput) (CollectorAuthContext, error)
	ComposeURL func(input CollectorComposeUrlInput) (*url.URL, error)
}

// PerformanceSpan records one collector operation. Pointer-valued counters
// distinguish an omitted value from an observed zero.
type PerformanceSpan struct {
	DurationMs      float64
	Phase           string
	Status          string
	AuthIdentity    string
	Product         string
	ResourceFamily  string
	Instances       *int
	LogicalRequests *int
	Pages           *int
}

// PerformanceRecorder receives collector timing and count data. Callers may
// pass nil when no report is requested.
type PerformanceRecorder interface {
	Now() float64
	DurationSince(startedMs float64) float64
	SetFetchConcurrency(value int) error
	RecordSpan(span PerformanceSpan) error
}

// FetchRunResult summarizes a collection batch.
type FetchRunResult struct {
	Failed    map[string]string
	Processed []string
	Skipped   map[string]string
}
