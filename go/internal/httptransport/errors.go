package httptransport

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"

	"github.com/dvmrry/infrawright-dev/go/internal/collectors"
	"github.com/dvmrry/infrawright-dev/go/internal/procerr"
)

// ioFailure builds a *procerr.ProcessFailure in the "io" category, the
// same shape every transport-layer error in this package (and the retired
// resthttp package before it) surfaces to callers.
func ioFailure(code, message string, retryable ...bool) *procerr.ProcessFailure {
	canRetry := false
	if len(retryable) != 0 {
		canRetry = retryable[0]
	}
	return procerr.NewProcessFailure(procerr.NewProcessFailureOptions{
		Category:  procerr.CategoryIO,
		Code:      code,
		Message:   message,
		Retryable: canRetry,
	})
}

func ordinaryValidationError(label string) error {
	return errors.New(label + " must be a positive bounded integer")
}

// requestLocation renders target for an error/diagnostic message. The
// query string, fragment, and any userinfo are dropped outright -- never
// echoed back, even masked -- and the remaining host is passed through
// collectors.MaskCollectorIdentifiers, which masks a ZSCALER_VANITY_DOMAIN
// label and a /customers/<id> path segment. This is the one piece of
// resthttp's error-formatting behavior every operator-facing failure
// message in this package depends on for not leaking tenant identifiers
// or query-string secrets (e.g. an OAuth token) into logs.
func requestLocation(input *url.URL) string {
	if input == nil {
		return "<invalid-url>"
	}
	safe := *input
	safe.RawQuery = ""
	safe.ForceQuery = false
	safe.Fragment = ""
	safe.RawFragment = ""
	safe.User = nil
	if safe.Path == "" {
		safe.Path = "/"
	}
	return collectors.MaskCollectorIdentifiers(safe.String())
}

type failureKind string

const (
	failureCertificate failureKind = "certificate"
	failureTimeout     failureKind = "timeout"
	failureConnection  failureKind = "connection"
)

// classifyFailure buckets a transport error into certificate/timeout/
// connection so connectionFailure can attach an actionable hint and the
// right retryability. Unlike the retired resthttp transport (which also
// matched undici's synthetic error-code strings, e.g. "UND_ERR_CONNECT_
// TIMEOUT"), this only recognizes the real Go stdlib error shapes a
// net/http-backed transport actually produces -- Node/undici error-code
// text is explicitly not a product requirement (the Go runtime contract
// §2's "allowed to change" column).
func classifyFailure(err error) failureKind {
	var unknownAuthority x509.UnknownAuthorityError
	var hostname x509.HostnameError
	var invalid x509.CertificateInvalidError
	var systemRoots x509.SystemRootsError
	var verification *tls.CertificateVerificationError
	var recordHeader tls.RecordHeaderError
	var alert tls.AlertError
	if errors.As(err, &unknownAuthority) || errors.As(err, &hostname) ||
		errors.As(err, &invalid) || errors.As(err, &systemRoots) ||
		errors.As(err, &verification) || errors.As(err, &recordHeader) ||
		errors.As(err, &alert) {
		return failureCertificate
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return failureTimeout
	}
	var networkError net.Error
	if errors.As(err, &networkError) && networkError.Timeout() {
		return failureTimeout
	}
	for current := err; current != nil; current = errors.Unwrap(current) {
		if strings.HasPrefix(strings.ToLower(current.Error()), "tls:") {
			return failureCertificate
		}
	}
	return failureConnection
}

// connectionFailure ports the operator-facing shape of resthttp's
// connectionFailure: a masked, query-free target location, a
// certificate/timeout/connection classification, and a hint pointing at
// the one env var or setting that usually explains it. Only certificate
// failures are non-retryable.
func connectionFailure(target *url.URL, err error) *procerr.ProcessFailure {
	kind := classifyFailure(err)
	var hint string
	switch kind {
	case failureCertificate:
		hint = "corporate TLS inspection? set REQUESTS_CA_BUNDLE to the exported proxy root CA"
	case failureTimeout:
		hint = "request timed out; check HTTPS_PROXY/NO_PROXY and outbound connectivity"
	default:
		hint = "check HTTPS_PROXY/NO_PROXY, DNS, and outbound connectivity"
	}
	return ioFailure(
		"REST_HTTP_TRANSPORT_FAILED",
		fmt.Sprintf("cannot reach %s (%s failure)\nhint: %s", requestLocation(target), kind, hint),
		kind != failureCertificate,
	)
}

func hasHeader(headers map[string]string, wanted string) bool {
	for name := range headers {
		if strings.EqualFold(name, wanted) {
			return true
		}
	}
	return false
}
