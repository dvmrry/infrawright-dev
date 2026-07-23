// Package collectors ports node-src/collectors/*.ts: the registry-driven
// REST fetch engine (rest.ts), its retry-delay/masking/selection/authority
// helpers (retry.ts, diagnostics.ts, selection.ts, authority.ts), the
// built-in Zscaler product adapters (zscaler-adapters.ts), and the
// fetch-diagnostics TLS probe surface (rest-diagnostics.ts) -- everything
// under node-src/collectors/ except the real HTTP transport
// (node-src/io/rest-http-transport.ts), which is a separate, not-yet-ported
// parcel. This package is written entirely against the HttpTransport seam
// below; the transport parcel's job is to produce a value satisfying that
// seam, not to change anything in this package.
//
// Every exported symbol's doc comment names the Node source file it ports;
// that TypeScript remains the differential oracle until this port is
// independently qualified (docs/go-runtime-plan.md).
package collectors

import "net/url"

// types.go ports node-src/collectors/types.ts: the HttpTransport seam this
// entire package is built against, plus the CollectorAdapter/context shapes
// every product adapter and the fetch engine share.

// CollectorAuthMode ports the CollectorAuthMode union type from
// node-src/collectors/types.ts.
type CollectorAuthMode string

const (
	// AuthModeLegacy ports the "legacy" auth mode literal.
	AuthModeLegacy CollectorAuthMode = "legacy"
	// AuthModeOneAPI ports the "oneapi" auth mode literal.
	AuthModeOneAPI CollectorAuthMode = "oneapi"
)

// Environment ports the NodeJS.ProcessEnv shape this package reads
// credentials and configuration from: a plain string-keyed map where an
// absent key is the same "unset" state Node's `environment[name] ===
// undefined` check observes (Go map lookup miss reports the same way via
// the two-result form), and present-but-empty-string is a distinct,
// deliberately different state every call site here also checks for
// explicitly (matching the Node source's own `=== undefined || === ""`
// pattern rather than collapsing the two).
type Environment = map[string]string

// CollectorContext ports the CollectorContext interface from
// node-src/collectors/types.ts. The four optional TS fields
// (ziaLegacyBase, zpaCloud, zpaLegacyBase are `readonly field?: string`)
// become plain strings here: every call site in the Node source treats
// `value ?? ""` and an explicit `""` identically (see e.g.
// zscaler-adapters.ts's ziaLegacyBase/zpaLegacyBase helpers), so Go's zero
// value for string already carries the same "absent" meaning without a
// separate optionality wrapper.
type CollectorContext struct {
	Cloud         string
	CustomerID    string
	ZiaLegacyBase string
	ZpaCloud      string
	ZpaLegacyBase string
}

// HTTPRequestClassification ports the HttpRequestClassification union type
// from node-src/performance/recorder.ts (re-exported here as the type this
// package's HttpRequest.Performance field carries; the performance
// recorder implementation itself is a separate, not-yet-ported package --
// see PerformanceRecorder's doc comment below).
type HTTPRequestClassification string

const (
	ClassificationAction         HTTPRequestClassification = "action"
	ClassificationAuthentication HTTPRequestClassification = "authentication"
	ClassificationDetail         HTTPRequestClassification = "detail"
	ClassificationList           HTTPRequestClassification = "list"
)

// HTTPRequestPerformanceContext ports the HttpRequestPerformanceContext
// interface from node-src/performance/recorder.ts. Product and
// ResourceFamily being "" mean the TS `field?: string` was omitted.
type HTTPRequestPerformanceContext struct {
	Classification HTTPRequestClassification
	EndpointFamily string
	Phase          string
	Product        string
	ResourceFamily string
}

// HTTPRequest ports the HttpRequest interface from
// node-src/collectors/types.ts. Method is always "GET" or "POST", matching
// the TS union; Body is nil when the TS `body?:` field was omitted.
// TimeoutMs is 0 when the TS `timeoutMs?:` field was omitted (a caller that
// needs a genuine zero-millisecond timeout is not a real scenario this
// product has).
//
// Unlike the TS interface, this struct carries no context.Context: the Node
// seam has no cancellation-token analogue (per-request cancellation is
// expressed only via TimeoutMs), and this port keeps the seam a literal
// match so the future rest-http-transport parcel's job is exactly "produce
// a value satisfying this struct's contract," not "also decide whether to
// add cancellation."
type HTTPRequest struct {
	Method      string
	URL         *url.URL
	Headers     map[string]string
	Body        []byte
	TimeoutMs   int
	Performance *HTTPRequestPerformanceContext
}

// HTTPResponse ports the HttpResponse interface from
// node-src/collectors/types.ts. Headers uses net/http's
// map[string][]string convention (the TS type additionally allows a bare
// string per header; this package never reads response headers itself --
// only status and body -- so the multi-value shape is the more useful
// default for a future transport implementer to populate from
// net/http.Header directly).
type HTTPResponse struct {
	Status  int
	Headers map[string][]string
	Body    []byte
}

// HttpTransport ports the HttpTransport interface from
// node-src/collectors/types.ts. Close is a plain method (not optional, as
// in TS's `close?()`); a transport with nothing to close should implement
// it as a no-op returning nil, matching how this package invokes it
// unconditionally in rest_diagnostics.go's owned-transport cleanup.
//
// Concurrency contract for implementers: rest.go's runFetchWorkers calls
// Request concurrently, from multiple goroutines at once, whenever
// FetchResourcesOptions.Concurrency is greater than 1. The Node source has
// no equivalent obligation to document -- Node is single-threaded, so
// "concurrent" HttpTransport.request calls there are merely interleaved,
// never simultaneous -- but a Go transport implementation genuinely must
// be safe for concurrent use (its own internal state, e.g. a connection
// pool or cookie jar, needs its own locking). This package's own test
// doubles (helpers_test.go's queueTransport/delayedPathTransport) do this
// locking themselves, as any real implementation -- including the future
// rest-http-transport parcel -- must.
type HttpTransport interface {
	Request(request HTTPRequest) (HTTPResponse, error)
	Close() error
}

// CollectorAuthContext ports the CollectorAuthContext interface from
// node-src/collectors/types.ts.
type CollectorAuthContext struct {
	Headers map[string]string
}

// CollectorAcquireInput ports the CollectorAcquireInput interface from
// node-src/collectors/types.ts. NowMs is nil when the TS `nowMs?:` field
// was omitted (acquireZiaLegacy then falls back to the real clock, see
// zscaler_adapters.go). PerformanceContext is nil when the TS
// `performanceContext?:` field was omitted.
type CollectorAcquireInput struct {
	Mode               CollectorAuthMode
	Environment        Environment
	Context            CollectorContext
	Transport          HttpTransport
	NowMs              *int64
	PerformanceContext *AuthPerformanceContext
}

// AuthPerformanceContext ports the object-literal type
// `Omit<HttpRequestPerformanceContext, "classification" | "endpointFamily">`
// that CollectorAcquireInput.performanceContext carries in
// node-src/collectors/types.ts: every HttpRequestPerformanceContext field
// except the two the adapter itself supplies per auth call.
type AuthPerformanceContext struct {
	Phase          string
	Product        string
	ResourceFamily string
}

// CollectorComposeUrlInput ports the CollectorComposeUrlInput interface
// from node-src/collectors/types.ts.
type CollectorComposeUrlInput struct {
	Mode    CollectorAuthMode
	Context CollectorContext
	Path    string
}

// CollectorAdapter ports the CollectorAdapter interface from
// node-src/collectors/types.ts: product-specific authentication and URL
// composition only. It is a plain struct of one data field and two
// function fields, rather than a Go interface type, so that test doubles
// and the built-in Zscaler adapters (zscaler_adapters.go) can both be
// built as ordinary struct literals -- the same shape node-tests's own
// `adapter()`/inline object-literal test doubles use for the TS interface.
type CollectorAdapter struct {
	Product    string
	Acquire    func(input CollectorAcquireInput) (CollectorAuthContext, error)
	ComposeURL func(input CollectorComposeUrlInput) (*url.URL, error)
}

// PerformanceSpan ports the load-bearing subset of the PerformanceSpanInput
// interface from node-src/performance/recorder.ts that this package
// populates when it calls RecordSpan: every field fetch.go's
// fetchResourcesBatch/FetchResources ever sets. Fields holding a TS
// `field?:` optional are zero-valued (""/0/false) when omitted; Instances,
// LogicalRequests, and Pages distinguish "0" from "omitted" via pointers
// because the TS source omits them entirely for outcome kinds that never
// set them (see PerformanceRecorder's doc comment for why this package
// defines this shape locally rather than importing an internal/performance
// package).
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

// PerformanceRecorder is this package's seam onto
// node-src/performance/recorder.ts's PerformanceRecorder class, scoped to
// exactly the four operations rest.go calls (now/durationSince via Now/
// DurationSince, setFetchConcurrency, and recordSpan). node-src/performance
// is its own not-yet-ported Node source tree -- outside this port slice's
// scope (docs/go-runtime-plan.md's Slice 5 covers collectors + zscaler
// adapters only, and the performance report is a separate output surface
// under the plan's Reports row) -- so this package declares the minimal
// interface it needs rather than depending on an internal/performance
// package that does not exist yet. Whatever ports
// node-src/performance/recorder.ts later must produce a type satisfying
// this interface; byte-for-byte parity of the performance *report* itself
// is out of scope here (see this package's doc comment and the port
// report's reviewer-attention notes).
type PerformanceRecorder interface {
	Now() float64
	DurationSince(startedMs float64) float64
	SetFetchConcurrency(value int) error
	RecordSpan(span PerformanceSpan) error
}

// FetchRunResult ports the FetchRunResult interface from
// node-src/collectors/types.ts.
type FetchRunResult struct {
	Failed    map[string]string
	Processed []string
	Skipped   map[string]string
}
