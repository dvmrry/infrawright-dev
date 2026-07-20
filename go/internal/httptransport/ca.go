package httptransport

import (
	"crypto/x509"
	"encoding/pem"
	"os"
	"regexp"
	"strings"

	"github.com/dvmrry/infrawright-dev/go/internal/collectors"
)

// certificatePattern locates PEM CERTIFICATE blocks within a bundle file.
// Content between/around blocks (residue) is allowed only if it is
// whitespace or a '#' comment line -- the same "no surprise trailing
// garbage" guard resthttp's customCACertificates enforced.
var certificatePattern = regexp.MustCompile(`(?s)-----BEGIN CERTIFICATE-----.*?-----END CERTIFICATE-----`)

func caBundleFailure() error {
	return ioFailure(
		"REST_CA_BUNDLE_FAILED",
		"configured CA bundle could not be loaded: set REQUESTS_CA_BUNDLE (or SSL_CERT_FILE) "+
			"to a regular, readable PEM file containing one or more valid CERTIFICATE blocks",
	)
}

// customCACertificates parses filePath as a PEM bundle of trusted root
// certificates. It fails closed -- returning REST_CA_BUNDLE_FAILED rather
// than a partially-trusted pool -- on anything that isn't a clean bundle:
// a missing/oversized/irregular file, non-PEM residue, an unparsable
// block, or (regardless of the ambient GODEBUG=x509negativeserial
// setting) a certificate with a negative serial number. A custom trust
// anchor an operator explicitly configured must never silently admit a
// certificate Go's default verifier would otherwise reject.
func customCACertificates(filePath string) ([]*x509.Certificate, error) {
	info, err := os.Stat(filePath)
	if err != nil || !info.Mode().IsRegular() || info.Size() > caBundleLimitBytes {
		return nil, caBundleFailure()
	}
	content, err := os.ReadFile(filePath)
	if err != nil || int64(len(content)) > caBundleLimitBytes {
		return nil, caBundleFailure()
	}
	matches := certificatePattern.FindAllIndex(content, -1)
	if len(matches) == 0 {
		return nil, caBundleFailure()
	}

	var residue strings.Builder
	certificates := make([]*x509.Certificate, 0, len(matches))
	offset := 0
	for _, match := range matches {
		residue.Write(content[offset:match[0]])
		block, rest := pem.Decode(content[match[0]:match[1]])
		if block == nil || block.Type != "CERTIFICATE" || strings.TrimSpace(string(rest)) != "" {
			return nil, caBundleFailure()
		}
		certificate, parseErr := x509.ParseCertificate(block.Bytes)
		if parseErr != nil {
			return nil, caBundleFailure()
		}
		if certificate.SerialNumber.Sign() < 0 {
			return nil, caBundleFailure()
		}
		certificates = append(certificates, certificate)
		offset = match[1]
	}
	residue.Write(content[offset:])
	for _, line := range strings.Split(residue.String(), "\n") {
		trimmed := strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if trimmed != "" && !strings.HasPrefix(trimmed, "#") {
			return nil, caBundleFailure()
		}
	}
	return certificates, nil
}

// trustedCertificates builds the pool a transport verifies peer
// certificates against: the platform's system trust store, plus an
// explicit operator-configured bundle layered on top. REQUESTS_CA_BUNDLE
// takes precedence over SSL_CERT_FILE (both are the standard `requests`/
// `curl` env vars this product already documents); includeCustomCA=false
// is a diagnostic-only escape hatch (see fetch-diag's system-trust-only
// probe in cmd/iw/commands_fetch.go) that skips both.
func trustedCertificates(environment collectors.Environment, includeCustomCA bool) (*x509.CertPool, error) {
	pool, err := x509.SystemCertPool()
	if err != nil || pool == nil {
		pool = x509.NewCertPool()
	}
	if !includeCustomCA {
		return pool, nil
	}
	customPath := environment["REQUESTS_CA_BUNDLE"]
	if customPath == "" {
		customPath = environment["SSL_CERT_FILE"]
	}
	if customPath == "" {
		return pool, nil
	}
	customCertificates, err := customCACertificates(customPath)
	if err != nil {
		return nil, err
	}
	for _, certificate := range customCertificates {
		pool.AddCert(certificate)
	}
	return pool, nil
}
