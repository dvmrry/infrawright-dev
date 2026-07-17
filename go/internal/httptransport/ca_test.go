package httptransport

import (
	"bytes"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dvmrry/infrawright-dev/go/internal/collectors"
)

const (
	negativeSerialCAFixture = "testdata/negative-serial-ca.pem"
	sanlessCNFixture        = "testdata/sanless-cn.pem"
	sanlessCNHostname       = "sanless.example.test"
)

func TestConfiguredCABundleAddsToSystemTrustAndRealTLSRequestSucceeds(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if got := request.Header.Get("User-Agent"); got != "" {
			t.Errorf("User-Agent = %q, want absent", got)
		}
		if got := request.Header.Get("Accept-Encoding"); got != "" {
			t.Errorf("Accept-Encoding = %q, want absent", got)
		}
		_, _ = io.WriteString(writer, "[]")
	}))
	t.Cleanup(server.Close)

	bundle := filepath.Join(t.TempDir(), "custom.pem")
	encoded := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})
	if err := os.WriteFile(bundle, encoded, 0o600); err != nil {
		t.Fatalf("os.WriteFile(%q) failed: %v", bundle, err)
	}

	transport, err := New(collectors.Environment{"REQUESTS_CA_BUNDLE": bundle}, Options{})
	if err != nil {
		t.Fatalf("New() failed: %v", err)
	}
	t.Cleanup(func() { _ = transport.Close() })
	got, err := transport.Request(collectors.HTTPRequest{Method: "GET", URL: mustURL(t, server.URL)})
	if err != nil {
		t.Fatalf("Request(%q) failed: %v", server.URL, err)
	}
	if string(got.Body) != "[]" {
		t.Errorf("Request(%q).Body = %q, want %q", server.URL, got.Body, "[]")
	}
}

func TestRealTLSVerificationFailureIsClassifiedAsCertificate(t *testing.T) {
	target := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	t.Cleanup(target.Close)

	// No REQUESTS_CA_BUNDLE/SSL_CERT_FILE and no matching system root: the
	// httptest server's self-signed leaf must fail verification.
	transport, err := New(collectors.Environment{}, Options{})
	if err != nil {
		t.Fatalf("New() failed: %v", err)
	}
	t.Cleanup(func() { _ = transport.Close() })
	_, err = transport.Request(collectors.HTTPRequest{Method: "GET", URL: mustURL(t, target.URL)})
	failure := requireProcessFailure(t, err, "REST_HTTP_TRANSPORT_FAILED")
	if !strings.Contains(failure.Message, "(certificate failure)") || failure.Retryable {
		t.Errorf("real TLS failure = message %q retryable %t, want non-retryable certificate failure", failure.Message, failure.Retryable)
	}
}

func TestInvalidCABundleFailsBeforeRoundTripperUse(t *testing.T) {
	bundle := filepath.Join(t.TempDir(), "invalid.pem")
	if err := os.WriteFile(bundle, []byte("not a certificate"), 0o600); err != nil {
		t.Fatalf("os.WriteFile(%q) failed: %v", bundle, err)
	}
	requests := 0
	_, err := New(
		collectors.Environment{"SSL_CERT_FILE": bundle},
		Options{RoundTripper: roundTripFunc(func(*http.Request) (*http.Response, error) {
			requests++
			return response("[]", 200, nil), nil
		})},
	)
	requireProcessFailure(t, err, "REST_CA_BUNDLE_FAILED")
	if requests != 0 {
		t.Errorf("round-tripper calls = %d, want 0", requests)
	}
}

func TestConfiguredCABundleSelectionAndValidation(t *testing.T) {
	directory := t.TempDir()
	badResidue := filepath.Join(directory, "residue.pem")
	if err := os.WriteFile(badResidue, []byte("# comment\nnot allowed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	tooLarge := filepath.Join(directory, "large.pem")
	if err := os.WriteFile(tooLarge, bytes.Repeat([]byte{'x'}, caBundleLimitBytes+1), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{directory, badResidue, tooLarge, filepath.Join(directory, "missing.pem")} {
		_, err := customCACertificates(path)
		requireProcessFailure(t, err, "REST_CA_BUNDLE_FAILED")
	}
}

func TestCABundleAllowsCommentAndBlankResidueLines(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	t.Cleanup(server.Close)
	encoded := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})
	bundle := filepath.Join(t.TempDir(), "bundle.pem")
	content := append([]byte("# exported proxy root CA\n\n"), encoded...)
	if err := os.WriteFile(bundle, content, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := customCACertificates(bundle); err != nil {
		t.Fatalf("customCACertificates() failed: %v", err)
	}
}

func TestCustomCAEnvironmentPrecedenceAndOptOut(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing.pem")
	includeFalse := false
	transport, err := New(collectors.Environment{
		"REQUESTS_CA_BUNDLE": missing,
	}, Options{
		IncludeCustomCA: &includeFalse,
		RoundTripper: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return response("ok", 200, nil), nil
		}),
	})
	if err != nil {
		t.Fatalf("New(includeCustomCA=false) failed: %v", err)
	}
	if err := transport.Close(); err != nil {
		t.Fatalf("Close() failed: %v", err)
	}

	_, err = New(collectors.Environment{
		"REQUESTS_CA_BUNDLE": missing,
		"SSL_CERT_FILE":      filepath.Join(t.TempDir(), "also-missing.pem"),
	}, Options{})
	requireProcessFailure(t, err, "REST_CA_BUNDLE_FAILED")
}

func readCertificateDER(t *testing.T, filePath string) []byte {
	t.Helper()
	content, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("os.ReadFile(%q) error = %v, want nil", filePath, err)
	}
	block, rest := pem.Decode(content)
	if block == nil || block.Type != "CERTIFICATE" || strings.TrimSpace(string(rest)) != "" {
		t.Fatalf("pem.Decode(%q) did not yield exactly one CERTIFICATE block", filePath)
	}
	return block.Bytes
}

// TestNegativeSerialCustomCAFailsClosedIndependentOfGODEBUG preserves a
// genuine product-safety property from the retired resthttp package: an
// operator-configured custom trust anchor must fail closed even when the
// ambient process has opted into Go's legacy negative-serial leniency, so
// a corrupted or malicious bundle can never silently widen trust.
func TestNegativeSerialCustomCAFailsClosedIndependentOfGODEBUG(t *testing.T) {
	t.Setenv("GODEBUG", "x509negativeserial=1")
	certificate, err := x509.ParseCertificate(readCertificateDER(t, negativeSerialCAFixture))
	if err != nil {
		t.Fatalf("x509.ParseCertificate(%q) with x509negativeserial=1 error = %v, want nil", negativeSerialCAFixture, err)
	}
	if got := certificate.SerialNumber.Sign(); got >= 0 {
		t.Fatalf("x509.ParseCertificate(%q).SerialNumber.Sign() = %d, want negative", negativeSerialCAFixture, got)
	}

	_, err = customCACertificates(negativeSerialCAFixture)
	requireProcessFailure(t, err, "REST_CA_BUNDLE_FAILED")
}

func TestSANlessCommonNameCertificateParsesButFailsHostnameVerification(t *testing.T) {
	certificates, err := customCACertificates(sanlessCNFixture)
	if err != nil {
		t.Fatalf("customCACertificates(%q) error = %v, want nil", sanlessCNFixture, err)
	}
	if len(certificates) != 1 {
		t.Fatalf("customCACertificates(%q) count = %d, want 1", sanlessCNFixture, len(certificates))
	}
	certificate := certificates[0]
	if got := certificate.Subject.CommonName; got != sanlessCNHostname {
		t.Errorf("customCACertificates(%q)[0].Subject.CommonName = %q, want %q", sanlessCNFixture, got, sanlessCNHostname)
	}
	err = certificate.VerifyHostname(sanlessCNHostname)
	if err == nil {
		t.Fatal("Certificate.VerifyHostname(sanless.example.test) error = nil, want SAN-only hostname rejection")
	}
	var hostnameError x509.HostnameError
	if !errors.As(err, &hostnameError) {
		t.Errorf("Certificate.VerifyHostname(%q) error = %T %v, want x509.HostnameError", sanlessCNHostname, err, err)
	}
}
