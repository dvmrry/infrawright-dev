package resthttp

//go:generate go run generate_node_roots.go

import (
	"crypto/x509"
	"encoding/pem"
	"os"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/dvmrry/infrawright-dev/go/internal/collectors"
)

var certificatePattern = regexp.MustCompile(`(?s)-----BEGIN CERTIFICATE-----.*?-----END CERTIFICATE-----`)

func caBundleFailure() error {
	return ioFailure("REST_CA_BUNDLE_FAILED", "configured CA bundle could not be loaded")
}

func unsupportedNodeCARuntimeOption() error {
	return ioFailure(
		"REST_CA_RUNTIME_OPTIONS_UNSUPPORTED",
		"Node-specific CA runtime options are not supported by the Go transport",
	)
}

// parseNodeOptions tokenizes NODE_OPTIONS the same way as Node 24's
// ParseNodeOptionsEnvVar: only an ASCII space delimits arguments, double
// quotes group text, and backslashes escape the next byte only inside quotes.
func parseNodeOptions(nodeOptions string) ([]string, bool) {
	arguments := make([]string, 0)
	current := make([]byte, 0, len(nodeOptions))
	inQuotes := false
	for index := 0; index < len(nodeOptions); index++ {
		character := nodeOptions[index]
		switch {
		case character == '\\' && inQuotes:
			if index+1 == len(nodeOptions) {
				return nil, false
			}
			index++
			current = append(current, nodeOptions[index])
		case character == ' ' && !inQuotes:
			if len(current) > 0 {
				arguments = append(arguments, string(current))
				current = current[:0]
			}
		case character == '"':
			inQuotes = !inQuotes
		default:
			current = append(current, character)
		}
	}
	if inQuotes {
		return nil, false
	}
	if len(current) > 0 {
		arguments = append(arguments, string(current))
	}
	return arguments, true
}

func normalizedNodeOptionName(argument string) (string, string, bool) {
	if !strings.HasPrefix(argument, "--") {
		return argument, "", false
	}
	name := argument
	value := ""
	attached := false
	if equals := strings.IndexByte(argument, '='); equals >= 0 {
		name = argument[:equals]
		value = argument[equals+1:]
		attached = true
	}
	name = "--" + strings.ReplaceAll(name[2:], "_", "-")
	return name, value, attached
}

// nodeOptionTakesValue is pinned to the Node 24.15 options that are both
// allowed in NODE_OPTIONS and consume one required value. Keeping the arity
// here prevents a value which mentions a CA option from being mistaken for
// the option itself.
func nodeOptionTakesValue(name string) bool {
	switch name {
	case "-C", "-r",
		"--allow-fs-read", "--allow-fs-write", "--conditions",
		"--cpu-prof-dir", "--cpu-prof-interval", "--cpu-prof-name",
		"--debug-port", "--diagnostic-dir", "--disable-proto", "--disable-warning",
		"--dns-result-order", "--es-module-specifier-resolution", "--experimental-loader",
		"--heap-prof-dir", "--heap-prof-interval", "--heap-prof-name",
		"--heapsnapshot-near-heap-limit", "--heapsnapshot-signal", "--icu-data-dir",
		"--import", "--input-type", "--inspect-port", "--inspect-publish-uid",
		"--loader", "--localstorage-file", "--max-http-header-size",
		"--max-old-space-size-percentage", "--network-family-autoselection-attempt-timeout",
		"--openssl-config", "--redirect-warnings", "--report-dir", "--report-directory",
		"--report-filename", "--report-signal", "--require", "--secure-heap",
		"--secure-heap-min", "--snapshot-blob", "--stack-trace-limit",
		"--test-coverage-branches", "--test-coverage-exclude", "--test-coverage-functions",
		"--test-coverage-include", "--test-coverage-lines", "--test-global-setup",
		"--test-isolation", "--test-name-pattern", "--test-reporter",
		"--test-reporter-destination", "--test-rerun-failures", "--test-shard",
		"--test-skip-pattern", "--title", "--tls-cipher-list", "--tls-keylog",
		"--trace-event-categories", "--trace-event-file-pattern", "--trace-require-module",
		"--unhandled-rejections", "--use-largepages", "--v8-pool-size",
		"--watch-kill-signal", "--watch-path":
		return true
	default:
		return false
	}
}

func nodeCAOptionState(name string) (string, bool, bool) {
	enabled := true
	if strings.HasPrefix(name, "--no-") {
		name = "--" + strings.TrimPrefix(name, "--no-")
		enabled = false
	}
	switch name {
	case "--use-system-ca":
		return "system", enabled, true
	case "--use-openssl-ca":
		return "openssl", enabled, true
	default:
		return "", false, false
	}
}

func nodeOptionsUseUnsupportedCA(nodeOptions string) bool {
	arguments, valid := parseNodeOptions(nodeOptions)
	if !valid {
		return true
	}
	useSystemCA := false
	useOpenSSLCA := false
	for index := 0; index < len(arguments); index++ {
		argument := arguments[index]
		if len(argument) <= 1 || argument[0] != '-' {
			// Node stops option parsing at the first non-option argument.
			break
		}
		if argument == "--" {
			return true
		}
		name, attachedValue, hasAttachedValue := normalizedNodeOptionName(argument)
		if option, enabled, ok := nodeCAOptionState(name); ok {
			switch option {
			case "system":
				useSystemCA = enabled
			case "openssl":
				useOpenSSLCA = enabled
			}
			continue
		}
		if nodeOptionTakesValue(name) {
			if hasAttachedValue {
				if attachedValue == "" {
					return true
				}
				continue
			}
			if index+1 == len(arguments) || strings.HasPrefix(arguments[index+1], "-") {
				return true
			}
			index++
			continue
		}
		normalized := strings.ReplaceAll(argument, "_", "-")
		if strings.HasPrefix(argument, "-") &&
			(strings.Contains(normalized, "--use-system-ca") ||
				strings.Contains(normalized, "--use-openssl-ca")) {
			// Node would reject this target-bearing token rather than treat it
			// as one of the argument-bearing options handled above.
			return true
		}
	}
	return useSystemCA || useOpenSSLCA
}

func validateNodeCARuntimeOptions(environment collectors.Environment) error {
	if environment["NODE_EXTRA_CA_CERTS"] != "" ||
		environment["NODE_USE_SYSTEM_CA"] != "" && environment["NODE_USE_SYSTEM_CA"] != "0" {
		return unsupportedNodeCARuntimeOption()
	}
	if nodeOptionsUseUnsupportedCA(environment["NODE_OPTIONS"]) {
		return unsupportedNodeCARuntimeOption()
	}
	return nil
}

func customCACertificates(filePath string) ([]*x509.Certificate, error) {
	metadata, err := os.Stat(filePath)
	if err != nil || !metadata.Mode().IsRegular() || metadata.Size() > caBundleLimitBytes {
		return nil, caBundleFailure()
	}
	content, err := os.ReadFile(filePath)
	if err != nil || len(content) > caBundleLimitBytes || !utf8.Valid(content) {
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
		encoded := content[match[0]:match[1]]
		block, rest := pem.Decode(encoded)
		if block == nil || block.Type != "CERTIFICATE" || trimECMAScriptWhitespace(string(rest)) != "" {
			return nil, caBundleFailure()
		}
		certificate, parseErr := x509.ParseCertificate(block.Bytes)
		if parseErr != nil {
			return nil, caBundleFailure()
		}
		if certificate.SerialNumber.Sign() < 0 {
			// Go can be configured to accept negative serial numbers through
			// GODEBUG=x509negativeserial=1. Custom trust must remain fail-closed
			// regardless of ambient process compatibility settings.
			return nil, caBundleFailure()
		}
		certificates = append(certificates, certificate)
		offset = match[1]
	}
	residue.Write(content[offset:])
	for _, line := range strings.Split(strings.ReplaceAll(residue.String(), "\r\n", "\n"), "\n") {
		trimmed := trimECMAScriptWhitespace(line)
		if trimmed != "" && !strings.HasPrefix(trimmed, "#") {
			return nil, caBundleFailure()
		}
	}
	return certificates, nil
}

func trimECMAScriptWhitespace(input string) string {
	return strings.TrimFunc(input, isECMAScriptWhitespace)
}

func trustedCertificates(environment collectors.Environment, includeCustomCA bool) (*x509.CertPool, error) {
	if err := validateNodeCARuntimeOptions(environment); err != nil {
		return nil, err
	}
	certificates, err := nodeBundledRoots()
	if err != nil {
		return nil, caBundleFailure()
	}
	pool := x509.NewCertPool()
	for _, certificate := range certificates {
		pool.AddCert(certificate)
	}
	customPath := ""
	if includeCustomCA {
		customPath = environment["REQUESTS_CA_BUNDLE"]
		if customPath == "" {
			customPath = environment["SSL_CERT_FILE"]
		}
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
