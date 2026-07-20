package collectors

// rest_diagnostics_test.go ports node-src/collectors/rest-diagnostics.ts's
// hostUrl validation and probeRestHost/probeRestHosts logic to the depth
// they are testable against an injected HttpTransport (see
// rest_diagnostics.go's doc comment for why the real-socket, default-
// transport-construction part of node-tests/rest-http-transport.test.ts
// is out of this port's scope). There is no dedicated
// node-tests/rest-diagnostics.test.ts in the Node source; these vectors
// are inferred directly from probeRestHost/probeRestHosts/hostUrl's own
// behavior in node-src/collectors/rest-diagnostics.ts.

import (
	"errors"
	"testing"
)

type fixedResponseTransport struct {
	response HTTPResponse
	err      error
	requests []HTTPRequest
}

func (f *fixedResponseTransport) Request(request HTTPRequest) (HTTPResponse, error) {
	f.requests = append(f.requests, request)
	if f.err != nil {
		return HTTPResponse{}, f.err
	}
	return f.response, nil
}

func (f *fixedResponseTransport) Close() error { return nil }

func TestProbeRestHostRejectsMalformedHosts(t *testing.T) {
	for _, host := range []string{"", "host/path", "user@host", "host?query", "host#fragment"} {
		_, err := ProbeRestHost(host, RestHostProbeOptions{Transport: &fixedResponseTransport{}})
		if err == nil {
			t.Errorf("ProbeRestHost(%q) did not error", host)
		}
	}
}

func TestProbeRestHostSuccessAndFailure(t *testing.T) {
	ok := &fixedResponseTransport{response: HTTPResponse{Status: 204}}
	result, err := ProbeRestHost("example.test", RestHostProbeOptions{Transport: ok})
	if err != nil {
		t.Fatalf("ProbeRestHost: %v", err)
	}
	if !result.OK || result.Detail != "HTTP 204" || result.Host != "example.test" {
		t.Errorf("result = %+v, want OK with detail 'HTTP 204'", result)
	}
	if len(ok.requests) != 1 || ok.requests[0].URL.String() != "https://example.test/" {
		t.Errorf("requests = %v, want a single GET to https://example.test/", ok.requests)
	}

	failing := &fixedResponseTransport{err: errors.New("connection refused")}
	result2, err := ProbeRestHost("unreachable.test", RestHostProbeOptions{Transport: failing})
	if err != nil {
		t.Fatalf("ProbeRestHost: %v", err)
	}
	if result2.OK || result2.Detail != "connection refused" {
		t.Errorf("result2 = %+v, want a failed probe with detail 'connection refused'", result2)
	}
}

func TestProbeRestHostsDeduplicatesAndSortsDeterministically(t *testing.T) {
	transport := &fixedResponseTransport{response: HTTPResponse{Status: 200}}
	results, err := ProbeRestHosts([]string{"b.example", "a.example", "b.example"}, RestHostProbeOptions{Transport: transport})
	if err != nil {
		t.Fatalf("ProbeRestHosts: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("len(results) = %d, want 2 (duplicates collapsed)", len(results))
	}
	if results[0].Host != "a.example" || results[1].Host != "b.example" {
		t.Errorf("results = %+v, want sorted [a.example, b.example]", results)
	}
	if len(transport.requests) != 2 {
		t.Errorf("requests = %d, want 2 (one probe per unique host)", len(transport.requests))
	}
}

func TestProbeRestHostRequiresAnInjectedTransport(t *testing.T) {
	_, err := ProbeRestHost("example.test", RestHostProbeOptions{})
	if err == nil {
		t.Fatal("expected an error when no transport is injected")
	}
}
