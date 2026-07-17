package resthttp

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strings"

	"github.com/dvmrry/infrawright-dev/go/internal/collectors"
	"github.com/dvmrry/infrawright-dev/go/internal/procerr"
)

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
	return collectors.MaskCollectorIdentifiers(whatwgURLString(&safe, false))
}

type failureKind string

const (
	failureCertificate failureKind = "certificate"
	failureTimeout     failureKind = "timeout"
	failureConnection  failureKind = "connection"
)

var (
	certificateCode = regexp.MustCompile(`(?i)CERT|TLS|SSL|SELF_SIGNED|UNABLE_TO_VERIFY`)
	timeoutCode     = regexp.MustCompile(`(?i)TIMEOUT|TIMEDOUT`)
)

type errorCoder interface {
	ErrorCode() string
}

func classifyFailure(err error) failureKind {
	code := ""
	var coded errorCoder
	if errors.As(err, &coded) {
		code = coded.ErrorCode()
	}
	if certificateCode.MatchString(code) {
		return failureCertificate
	}

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

	if timeoutCode.MatchString(code) || errors.Is(err, context.DeadlineExceeded) {
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

func ordinaryValidationError(label string) error {
	return errors.New(label + " must be a positive bounded integer")
}

func hasHeader(headers map[string]string, wanted string) bool {
	for name := range headers {
		if strings.EqualFold(name, wanted) {
			return true
		}
	}
	return false
}
