package collectors

import (
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/dvmrry/infrawright-dev/go/internal/canonjson"
)

// rest_diagnostics.go ports node-src/collectors/rest-diagnostics.ts: the
// `fetch-diag` TLS/connectivity probe surface, to the depth it is testable
// against the HttpTransport seam.
//
// The Node source's probeRestHost falls back to constructing a *real*
// transport (createRestHttpTransport from node-src/io/rest-http-transport.ts)
// whenever its caller omits one; that fallback -- and the whole
// REST_HTTP_TIMEOUT_MS-derived default-timeout plumbing that comes with it
// -- is exactly the not-yet-ported transport parcel this package is built
// against as a seam, not a dependency (see this package's doc comment).
// ProbeRestHost/ProbeRestHosts therefore require an injected transport
// here; there is no default-construction path until that parcel lands.
// Everything else -- host validation, the deterministic sorted-unique host
// list, and one-HTTP-response-proves-connectivity semantics -- is ported
// in full.

// defaultProbeTimeoutMs mirrors `Math.min(15_000, REST_HTTP_TIMEOUT_MS)`
// from node-src/collectors/rest-diagnostics.ts's probeRestHost, where
// REST_HTTP_TIMEOUT_MS (node-src/io/rest-http-transport.ts) is 30_000; the
// min of the two constants is always 15_000.
const defaultProbeTimeoutMs = 15_000

// RestHostProbeResult ports the RestHostProbeResult interface from
// node-src/collectors/rest-diagnostics.ts.
type RestHostProbeResult struct {
	Detail string
	Host   string
	OK     bool
}

// RestHostProbeOptions ports the RestHostProbeOptions interface from
// node-src/collectors/rest-diagnostics.ts, minus the environment/
// includeCustomCa/transportOptions fields that only ever feed
// createRestHttpTransport's not-yet-ported default-transport
// construction (see this file's doc comment). Transport is required here,
// where the TS field is an optional test seam with a production fallback.
// TimeoutMs of 0 means "use defaultProbeTimeoutMs" (the TS
// `timeoutMs?: number` field being omitted).
type RestHostProbeOptions struct {
	TimeoutMs int
	Transport HttpTransport
}

// hostURL ports hostUrl from node-src/collectors/rest-diagnostics.ts.
func hostURL(host string) (*url.URL, error) {
	invalid := errors.New("diagnostic host must be a hostname with an optional port")
	if host == "" || strings.ContainsAny(host, "/@?#") {
		return nil, invalid
	}
	parsed, err := url.Parse("https://" + host + "/")
	if err != nil {
		return nil, invalid
	}
	if parsed.Hostname() == "" || parsed.User != nil {
		return nil, invalid
	}
	return parsed, nil
}

// ProbeRestHost ports probeRestHost from
// node-src/collectors/rest-diagnostics.ts: probe one collector host; any
// HTTP response proves DNS/TCP/TLS success. options.Transport is required
// (see RestHostProbeOptions's doc comment); ProbeRestHost never closes a
// caller-supplied transport, matching the TS source's `owned` guard around
// its own fallback-constructed transport, which this port has no
// counterpart for yet.
func ProbeRestHost(host string, options RestHostProbeOptions) (RestHostProbeResult, error) {
	target, err := hostURL(host)
	if err != nil {
		return RestHostProbeResult{}, err
	}
	if options.Transport == nil {
		return RestHostProbeResult{}, errors.New(
			"ProbeRestHost requires an injected transport until the rest-http-transport parcel lands",
		)
	}
	timeoutMs := options.TimeoutMs
	if timeoutMs == 0 {
		timeoutMs = defaultProbeTimeoutMs
	}
	response, requestErr := options.Transport.Request(HTTPRequest{
		Method:    "GET",
		URL:       target,
		Headers:   map[string]string{"accept": "*/*"},
		TimeoutMs: timeoutMs,
	})
	if requestErr != nil {
		// Ports `error instanceof Error ? error.message : "connection
		// failed"`: every Go error already carries a message via Error(),
		// so the "connection failed" fallback (for a thrown non-Error
		// value, which has no Go analogue) is unreachable here.
		return RestHostProbeResult{Detail: requestErr.Error(), Host: host, OK: false}, nil
	}
	return RestHostProbeResult{Detail: fmt.Sprintf("HTTP %d", response.Status), Host: host, OK: true}, nil
}

// ProbeRestHosts ports probeRestHosts from
// node-src/collectors/rest-diagnostics.ts: probe a deterministic host list
// without sharing cookies or connections.
func ProbeRestHosts(hosts []string, options RestHostProbeOptions) ([]RestHostProbeResult, error) {
	unique := make(map[string]struct{}, len(hosts))
	for _, host := range hosts {
		unique[host] = struct{}{}
	}
	names := make([]string, 0, len(unique))
	for host := range unique {
		names = append(names, host)
	}
	// The TS source sorts hosts with plain `.sort()` (UTF-16-code-unit
	// ordering), not comparePythonStrings/sortedStrings; for a hostname
	// corpus (ASCII, no astral characters), that distinction is
	// unobservable, so canonjson.SortedStrings is used here for
	// consistency with the rest of this package.
	names = canonjson.SortedStrings(names)
	results := make([]RestHostProbeResult, len(names))
	for i, host := range names {
		result, err := ProbeRestHost(host, options)
		if err != nil {
			return nil, err
		}
		results[i] = result
	}
	return results, nil
}
