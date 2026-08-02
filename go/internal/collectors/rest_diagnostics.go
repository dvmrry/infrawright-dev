package collectors

import (
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/dvmrry/infrawright-dev/go/internal/canonjson"
)

// The fetch-diag surface validates hosts and probes connectivity through an
// injected transport. The CLI owns real-transport construction and lifetime;
// these helpers own deterministic host selection and the rule that any HTTP
// response proves DNS/TCP/TLS connectivity.

// defaultProbeTimeoutMs bounds an individual diagnostic request at 15 seconds.
const defaultProbeTimeoutMs = 15_000

// RestHostProbeResult describes one host connectivity probe.
type RestHostProbeResult struct {
	Detail string
	Host   string
	OK     bool
}

// RestHostProbeOptions configures a host probe. Transport is required;
// TimeoutMs == 0 uses defaultProbeTimeoutMs.
type RestHostProbeOptions struct {
	TimeoutMs int
	Transport HttpTransport
}

// hostURL validates a host and converts it to an HTTPS root URL.
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

// ProbeRestHost probes one collector host; any HTTP response proves
// DNS/TCP/TLS success. ProbeRestHost never closes the caller-supplied
// transport.
func ProbeRestHost(host string, options RestHostProbeOptions) (RestHostProbeResult, error) {
	target, err := hostURL(host)
	if err != nil {
		return RestHostProbeResult{}, err
	}
	if options.Transport == nil {
		return RestHostProbeResult{}, errors.New("ProbeRestHost requires an injected transport")
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
		return RestHostProbeResult{Detail: requestErr.Error(), Host: host, OK: false}, nil
	}
	return RestHostProbeResult{Detail: fmt.Sprintf("HTTP %d", response.Status), Host: host, OK: true}, nil
}

// ProbeRestHosts probes a deterministic host list through the supplied
// transport.
func ProbeRestHosts(hosts []string, options RestHostProbeOptions) ([]RestHostProbeResult, error) {
	unique := make(map[string]struct{}, len(hosts))
	for _, host := range hosts {
		unique[host] = struct{}{}
	}
	names := make([]string, 0, len(unique))
	for host := range unique {
		names = append(names, host)
	}
	// Hostnames are ASCII, so the canonical string ordering is sufficient.
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
