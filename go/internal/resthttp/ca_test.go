package resthttp

import (
	"bytes"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/dvmrry/infrawright-dev/go/internal/collectors"
)

const (
	negativeSerialCAFixture = "testdata/negative-serial-ca.pem"
	sanlessCNFixture        = "testdata/sanless-cn.pem"
	sanlessCNHostname       = "sanless.example.test"
)

type nodeOptionsCACase struct {
	name          string
	options       string
	wantReject    bool
	wantNodeValid bool
}

func nodeOptionsCACorpus() []nodeOptionsCACase {
	return []nodeOptionsCACase{
		{name: "empty", options: "", wantNodeValid: true},
		{name: "bundled_ca", options: "--trace-warnings --use-bundled-ca", wantNodeValid: true},
		{name: "title_attached_value", options: "--title=--use-system-ca", wantNodeValid: true},
		{name: "conditions_attached_value", options: "--conditions=--use-openssl-ca", wantNodeValid: true},
		{name: "trace_categories_attached_value", options: "--trace-event-categories=--use-system-ca", wantNodeValid: true},
		{name: "quoted_attached_value", options: `--title="mentions --use-system-ca and --use-openssl-ca"`, wantNodeValid: true},
		{name: "tab_in_attached_value", options: "--title=tab\t--use-system-ca", wantNodeValid: true},
		{name: "separate_plain_value", options: "--title mentions--use-system-ca", wantNodeValid: true},
		{name: "separate_escaped_value", options: `--conditions \--use-system-ca`, wantNodeValid: true},
		{name: "short_separate_escaped_value", options: `-C \--use-openssl-ca`, wantNodeValid: true},
		{name: "quoted_escaped_value", options: `--conditions "\\--use-system-ca"`, wantNodeValid: true},
		{name: "negated_system", options: "--no-use-system-ca", wantNodeValid: true},
		{name: "negated_system_underscores", options: "--no_use_system_ca", wantNodeValid: true},
		{name: "negated_system_attached", options: "--no-use-system-ca=false", wantNodeValid: true},
		{name: "negated_openssl", options: "--no-use-openssl-ca", wantNodeValid: true},
		{name: "system_enabled_then_disabled", options: "--use-system-ca --no-use-system-ca", wantNodeValid: true},
		{name: "openssl_enabled_then_disabled", options: "--use-openssl-ca --no-use-openssl-ca", wantNodeValid: true},

		{name: "system", options: "--use-system-ca", wantReject: true, wantNodeValid: true},
		{name: "system_underscores", options: "--use_system_ca", wantReject: true, wantNodeValid: true},
		{name: "system_true", options: "--use-system-ca=true", wantReject: true, wantNodeValid: true},
		{name: "system_false_is_still_enabled", options: "--use-system-ca=false", wantReject: true, wantNodeValid: true},
		{name: "system_zero_is_still_enabled", options: "--use-system-ca=0", wantReject: true, wantNodeValid: true},
		{name: "system_empty_is_still_enabled", options: "--use-system-ca=", wantReject: true, wantNodeValid: true},
		{name: "quoted_system", options: `"--use-system-ca"`, wantReject: true, wantNodeValid: true},
		{name: "quoted_escaped_system", options: `"--use-system\-ca"`, wantReject: true, wantNodeValid: true},
		{name: "system_split_by_quotes", options: `--use-"system-ca"`, wantReject: true, wantNodeValid: true},
		{name: "openssl", options: "--use-openssl-ca", wantReject: true, wantNodeValid: true},
		{name: "openssl_underscores_false", options: "--use_openssl_ca=false", wantReject: true, wantNodeValid: true},
		{name: "system_disabled_then_enabled", options: "--no-use-system-ca --use-system-ca", wantReject: true, wantNodeValid: true},

		{name: "tab_separated_target", options: "--trace-warnings\t--use-system-ca", wantReject: true},
		{name: "option_like_unescaped_value", options: `--title "--use-system-ca"`, wantReject: true},
		{name: "unknown_attached_target", options: "--unknown=--use-system-ca", wantReject: true},
		{name: "single_quotes_are_literal", options: `'--use-system-ca'`, wantNodeValid: true},
		{name: "non_option_stops_parsing", options: "literal --use-system-ca", wantNodeValid: true},
		{name: "single_quoted_token_stops_parsing", options: `'literal' --use-openssl-ca`, wantNodeValid: true},
		{name: "non_option_after_flag_stops_parsing", options: "--trace-warnings literal --use-system-ca", wantNodeValid: true},
		{name: "unterminated_quote", options: `--title="unterminated`, wantReject: true},
		{name: "invalid_escape", options: `--title="trailing\`, wantReject: true},
		{name: "end_of_options", options: "--", wantReject: true},
		{name: "empty_required_value", options: "--title=", wantReject: true},
		{name: "missing_required_value", options: "--title", wantReject: true},
	}
}

func TestNodeOptionsCARuntimeDetection(t *testing.T) {
	for _, test := range nodeOptionsCACorpus() {
		t.Run(test.name, func(t *testing.T) {
			err := validateNodeCARuntimeOptions(collectors.Environment{"NODE_OPTIONS": test.options})
			gotReject := err != nil
			if gotReject != test.wantReject {
				t.Errorf("validateNodeCARuntimeOptions(NODE_OPTIONS=%q) rejection = %t, want %t (error %v)", test.options, gotReject, test.wantReject, err)
			}
			if err != nil {
				requireProcessFailure(t, err, "REST_CA_RUNTIME_OPTIONS_UNSUPPORTED")
			}
		})
	}
}

type nodeOptionsCAOracleResult struct {
	UseSystemCA  bool `json:"system"`
	UseOpenSSLCA bool `json:"openssl"`
}

func nodeOracleEnvironment(nodeOptions string) []string {
	environment := make([]string, 0, len(os.Environ())+1)
	for _, entry := range os.Environ() {
		key := entry
		if equals := strings.IndexByte(entry, '='); equals >= 0 {
			key = entry[:equals]
		}
		switch key {
		case "NODE_OPTIONS", "NODE_EXTRA_CA_CERTS", "NODE_USE_SYSTEM_CA",
			"SSL_CERT_FILE", "SSL_CERT_DIR", "OPENSSL_CONF":
			continue
		default:
			environment = append(environment, entry)
		}
	}
	return append(environment, "NODE_OPTIONS="+nodeOptions)
}

func requireNode2415(t *testing.T) string {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skipf("Node 24.15 oracle unavailable: exec.LookPath(node) error = %v", err)
	}
	command := exec.Command(node, "--version")
	command.Env = nodeOracleEnvironment("")
	version, err := command.Output()
	if err != nil {
		t.Skipf("Node 24.15 oracle unavailable: node --version error = %v", err)
	}
	if got := strings.TrimSpace(string(version)); got != "v24.15.0" {
		t.Skipf("Node CA oracle requires v24.15.0, got %q", got)
	}
	return node
}

func runNodeOptionsCAOracle(t *testing.T, node, nodeOptions string) (nodeOptionsCAOracleResult, bool, string) {
	t.Helper()
	const script = `
const options = require('internal/options');
process.stdout.write(JSON.stringify({
  system: options.getOptionValue('--use-system-ca'),
  openssl: options.getOptionValue('--use-openssl-ca'),
}));
`
	command := exec.Command(node, "--expose-internals", "--eval", script)
	// Accepted NODE_OPTIONS values may enable trace-event output. Confine any
	// Node-created trace logs to the test's disposable directory.
	command.Dir = t.TempDir()
	command.Env = nodeOracleEnvironment(nodeOptions)
	var stderr bytes.Buffer
	command.Stderr = &stderr
	output, err := command.Output()
	if err != nil {
		return nodeOptionsCAOracleResult{}, false, stderr.String()
	}
	var result nodeOptionsCAOracleResult
	if err := json.Unmarshal(output, &result); err != nil {
		t.Fatalf("json.Unmarshal(Node NODE_OPTIONS oracle %q) error = %v, want nil", output, err)
	}
	return result, true, stderr.String()
}

func TestNodeOptionsCARuntimeDetectionMatchesNode2415(t *testing.T) {
	node := requireNode2415(t)
	for _, test := range nodeOptionsCACorpus() {
		t.Run(test.name, func(t *testing.T) {
			result, gotNodeValid, stderr := runNodeOptionsCAOracle(t, node, test.options)
			if gotNodeValid != test.wantNodeValid {
				t.Fatalf("Node 24.15 NODE_OPTIONS=%q validity = %t, want %t; stderr:\n%s", test.options, gotNodeValid, test.wantNodeValid, stderr)
			}
			if !gotNodeValid {
				if !test.wantReject {
					t.Errorf("validateNodeCARuntimeOptions(NODE_OPTIONS=%q) rejection = false, want fail-closed rejection for Node-invalid input", test.options)
				}
				return
			}
			gotNodeUsesUnsupportedCA := result.UseSystemCA || result.UseOpenSSLCA
			if gotNodeUsesUnsupportedCA != test.wantReject {
				t.Errorf("Node 24.15 NODE_OPTIONS=%q unsupported CA state = %t, want %t (result %#v)", test.options, gotNodeUsesUnsupportedCA, test.wantReject, result)
			}
		})
	}
}

func readCertificateDER(t *testing.T, filePath string) []byte {
	t.Helper()
	content, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("os.ReadFile(%q) error = %v, want nil", filePath, err)
	}
	block, rest := pem.Decode(content)
	if block == nil || block.Type != "CERTIFICATE" || strings.TrimSpace(string(rest)) != "" {
		t.Fatalf("pem.Decode(%q) = block type %q residue %q, want one CERTIFICATE block", filePath, blockType(block), rest)
	}
	return block.Bytes
}

func blockType(block *pem.Block) string {
	if block == nil {
		return ""
	}
	return block.Type
}

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

func TestSANlessCommonNameFailsClosedIndependentOfGODEBUG(t *testing.T) {
	t.Setenv("GODEBUG", "x509ignoreCN=0")
	certificates, err := customCACertificates(sanlessCNFixture)
	if err != nil {
		t.Fatalf("customCACertificates(%q) error = %v, want nil", sanlessCNFixture, err)
	}
	if len(certificates) != 1 {
		t.Fatalf("customCACertificates(%q) count = %d, want 1", sanlessCNFixture, len(certificates))
	}
	certificate := certificates[0]
	if got := certificate.Subject.CommonName; got != sanlessCNHostname {
		t.Errorf("x509.ParseCertificate(%q).Subject.CommonName = %q, want %q", sanlessCNFixture, got, sanlessCNHostname)
	}
	if got := certificate.DNSNames; len(got) != 0 {
		t.Fatalf("x509.ParseCertificate(%q).DNSNames = %v, want none", sanlessCNFixture, got)
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

func runNodeCAFixtureOracle(t *testing.T, node, script, fixture string) string {
	t.Helper()
	command := exec.Command(node, "--eval", script, fixture)
	command.Env = nodeOracleEnvironment("")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("Node 24.15 CA oracle(%q) error = %v, want nil; output:\n%s", fixture, err, output)
	}
	return strings.TrimSpace(string(output))
}

func TestNode2415AcceptsNegativeSerialCAFixture(t *testing.T) {
	node := requireNode2415(t)
	const script = `
const fs = require('node:fs');
const tls = require('node:tls');
const { X509Certificate } = require('node:crypto');
const pem = fs.readFileSync(process.argv[1]);
tls.createSecureContext({ ca: pem });
process.stdout.write(new X509Certificate(pem).serialNumber);
`
	if got := runNodeCAFixtureOracle(t, node, script, negativeSerialCAFixture); got != "-01" {
		t.Errorf("Node 24.15 negative-serial fixture serial = %q, want %q", got, "-01")
	}
}

func TestNode2415AcceptsSANlessCommonNameFixture(t *testing.T) {
	node := requireNode2415(t)
	const script = `
const fs = require('node:fs');
const tls = require('node:tls');
const { X509Certificate } = require('node:crypto');
const certificate = new X509Certificate(fs.readFileSync(process.argv[1]));
if (certificate.subjectAltName !== undefined) throw new Error('fixture unexpectedly has a SAN');
const error = tls.checkServerIdentity('sanless.example.test', certificate.toLegacyObject());
if (error !== undefined) throw error;
process.stdout.write(certificate.subject);
`
	if got := runNodeCAFixtureOracle(t, node, script, sanlessCNFixture); got != "CN=sanless.example.test" {
		t.Errorf("Node 24.15 SAN-less fixture subject = %q, want %q", got, "CN=sanless.example.test")
	}
}
