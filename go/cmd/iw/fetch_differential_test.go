package main

// Fetch/fetch-diag command coverage keeps the ordinary contract cases
// credential-free and network-free. A separate recorded-transport differential
// uses only a local TLS server and fixture credentials to exercise the complete
// legacy ZIA auth, cookie, custom-CA, resource, and artifact path against the
// built Node oracle.

import (
	"bytes"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dvmrry/infrawright-dev/go/internal/collectors"
)

type fetchClosingTransport struct {
	closeErr error
	closed   bool
}

const (
	recordedFetchAPIKey       = "0123456789ab"
	recordedFetchPassword     = "fixture-password"
	recordedFetchResourceType = "zia_recorded_fixture"
	recordedFetchSessionName  = "JSESSIONID"
	recordedFetchSessionValue = "recorded-session"
	recordedFetchTimeWindow   = 2 * time.Minute
	recordedFetchUsername     = "fixture-user"
	recordedFetchWireJSON     = `[{"name":"Recorded fixture","id":"fixture-1","enabled":true}]`
	recordedFetchArtifactJSON = "[\n" +
		"  {\n" +
		"    \"enabled\": true,\n" +
		"    \"id\": \"fixture-1\",\n" +
		"    \"name\": \"Recorded fixture\"\n" +
		"  }\n" +
		"]\n"
)

type recordedFetchRequest struct {
	contract string
	method   string
	uri      string
}

type recordedFetchFixture struct {
	baseURL string
	bundle  string
	host    string

	mu         sync.Mutex
	requests   []recordedFetchRequest
	violations []string
}

type recordedZIAAuthBody struct {
	APIKey    string `json:"apiKey"`
	Username  string `json:"username"`
	Password  string `json:"password"`
	Timestamp string `json:"timestamp"`
}

func (fixture *recordedFetchFixture) record(request recordedFetchRequest) {
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	fixture.requests = append(fixture.requests, request)
}

func (fixture *recordedFetchFixture) violation(format string, arguments ...any) {
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	fixture.violations = append(fixture.violations, fmt.Sprintf(format, arguments...))
}

func (fixture *recordedFetchFixture) take() ([]recordedFetchRequest, []string) {
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	requests := append([]recordedFetchRequest(nil), fixture.requests...)
	violations := append([]string(nil), fixture.violations...)
	fixture.requests = nil
	fixture.violations = nil
	return requests, violations
}

func (fixture *recordedFetchFixture) validateLegacyAuth(request *http.Request) bool {
	valid := true
	if got := request.Header.Get("Content-Type"); got != "application/json" {
		fixture.violation("legacy auth Content-Type = %q, want application/json", got)
		valid = false
	}
	if got := request.Header.Get("Cookie"); got != "" {
		fixture.violation("legacy auth Cookie is present, want absent")
		valid = false
	}
	decoder := json.NewDecoder(io.LimitReader(request.Body, 4*1024+1))
	decoder.DisallowUnknownFields()
	var body recordedZIAAuthBody
	if err := decoder.Decode(&body); err != nil {
		fixture.violation("legacy auth body decode error = %v, want valid JSON", err)
		return false
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		fixture.violation("legacy auth trailing JSON error = %v, want EOF", err)
		valid = false
	}
	if body.Username != recordedFetchUsername {
		fixture.violation("legacy auth username does not match the fixture username")
		valid = false
	}
	if body.Password != recordedFetchPassword {
		fixture.violation("legacy auth password does not match the fixture password")
		valid = false
	}
	if len(body.Timestamp) != 13 || strings.Trim(body.Timestamp, "0123456789") != "" {
		fixture.violation("legacy auth timestamp must be 13 decimal millisecond digits")
		valid = false
	} else if timestamp, err := strconv.ParseInt(body.Timestamp, 10, 64); err != nil {
		fixture.violation("legacy auth timestamp parse error = %v, want int64 milliseconds", err)
		valid = false
	} else {
		received := time.Now().UnixMilli()
		window := recordedFetchTimeWindow.Milliseconds()
		if timestamp < received-window || timestamp > received+window {
			fixture.violation("legacy auth timestamp is outside the two-minute server receipt window")
			valid = false
		}
	}
	wantAPIKey, err := collectors.ObfuscateZiaAPIKey(recordedFetchAPIKey, body.Timestamp)
	if err != nil {
		fixture.violation("ObfuscateZiaAPIKey(%q) error = %v, want nil", body.Timestamp, err)
		valid = false
	} else if body.APIKey != wantAPIKey {
		fixture.violation("legacy auth apiKey does not match the obfuscation for its timestamp")
		valid = false
	}
	return valid
}

func (fixture *recordedFetchFixture) serveHTTP(writer http.ResponseWriter, request *http.Request) {
	switch {
	case request.Method == http.MethodGet && request.RequestURI == "/":
		fixture.record(recordedFetchRequest{contract: "diagnostic", method: request.Method, uri: request.RequestURI})
		if got := request.Header.Get("Accept"); got != "*/*" {
			fixture.violation("diagnostic Accept = %q, want */*", got)
			http.Error(writer, "invalid diagnostic request", http.StatusBadRequest)
			return
		}
		writer.WriteHeader(http.StatusNoContent)
	case request.Method == http.MethodPost && request.RequestURI == "/api/v1/authenticatedSession":
		fixture.record(recordedFetchRequest{contract: "legacy-zia-auth", method: request.Method, uri: request.RequestURI})
		if !fixture.validateLegacyAuth(request) {
			http.Error(writer, "invalid legacy auth request", http.StatusBadRequest)
			return
		}
		http.SetCookie(writer, &http.Cookie{
			Name: recordedFetchSessionName, Value: recordedFetchSessionValue,
			Path: "/api", Secure: true, HttpOnly: true,
		})
		http.SetCookie(writer, &http.Cookie{
			Name: "narrow", Value: "must-not-be-forwarded",
			Path: "/different", Secure: true,
		})
		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(writer, "{}")
	case request.Method == http.MethodGet && request.RequestURI == "/api/v1/recordedFixture":
		fixture.record(recordedFetchRequest{contract: "resource", method: request.Method, uri: request.RequestURI})
		valid := true
		if got := request.Header.Get("Accept"); got != "application/json" {
			fixture.violation("resource Accept = %q, want application/json", got)
			valid = false
		}
		wantCookie := recordedFetchSessionName + "=" + recordedFetchSessionValue
		if got := request.Header.Get("Cookie"); got != wantCookie {
			fixture.violation("resource session cookie is missing, changed, or out of scope")
			valid = false
		}
		if got := request.Header.Get("Authorization"); got != "" {
			fixture.violation("resource Authorization is present, want absent in legacy ZIA mode")
			valid = false
		}
		if !valid {
			http.Error(writer, "invalid resource request", http.StatusUnauthorized)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(writer, recordedFetchWireJSON)
	default:
		fixture.record(recordedFetchRequest{contract: "unexpected", method: request.Method, uri: request.RequestURI})
		fixture.violation("unexpected request = %s %s", request.Method, request.RequestURI)
		http.NotFound(writer, request)
	}
}

func newRecordedFetchFixture(t *testing.T) *recordedFetchFixture {
	t.Helper()
	fixture := &recordedFetchFixture{}
	server := httptest.NewUnstartedServer(http.HandlerFunc(fixture.serveHTTP))
	server.Config.ErrorLog = log.New(io.Discard, "", 0)
	server.StartTLS()
	t.Cleanup(server.Close)
	fixture.baseURL = server.URL
	fixture.host = strings.TrimPrefix(server.URL, "https://")
	encoded := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})
	if len(encoded) == 0 {
		t.Fatal("pem.EncodeToMemory(server certificate) returned no bytes")
	}
	fixture.bundle = filepath.Join(t.TempDir(), "recorded-ca.pem")
	if err := os.WriteFile(fixture.bundle, encoded, 0o600); err != nil {
		t.Fatalf("os.WriteFile(%q) failed: %v", fixture.bundle, err)
	}
	return fixture
}

type recordedFetchPack struct {
	profile string
	root    string
}

func writeRecordedFetchPack(t *testing.T) recordedFetchPack {
	t.Helper()
	directory := t.TempDir()
	root := filepath.Join(directory, "packs")
	pack := filepath.Join(root, "zia")
	if err := os.MkdirAll(pack, 0o755); err != nil {
		t.Fatalf("os.MkdirAll(%q) failed: %v", pack, err)
	}
	files := map[string]string{
		filepath.Join(pack, "pack.json"): "{\n" +
			"  \"provider_prefixes\": {\"zia_\": \"zia\"},\n" +
			"  \"provider_sources\": {\"zia\": \"zscaler/zia\"}\n" +
			"}\n",
		filepath.Join(pack, "registry.json"): "{\n" +
			"  \"" + recordedFetchResourceType + "\": {\n" +
			"    \"fetch\": {\"pagination\": \"single\", \"path\": \"recordedFixture\"},\n" +
			"    \"product\": \"zia\"\n" +
			"  }\n" +
			"}\n",
	}
	profile := filepath.Join(directory, "packset.json")
	files[profile] = "{\n" +
		"  \"kind\": \"infrawright.pack-set\",\n" +
		"  \"version\": 1,\n" +
		"  \"packs\": [\"zia\"],\n" +
		"  \"shared\": []\n" +
		"}\n"
	for path, content := range files {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatalf("os.WriteFile(%q) failed: %v", path, err)
		}
	}
	return recordedFetchPack{profile: profile, root: root}
}

func reviewerZIAOnlyPack(t *testing.T, repositoryRoot string) recordedFetchPack {
	t.Helper()
	packsRoot := t.TempDir()
	sharedRoot := filepath.Join(packsRoot, "_shared")
	if err := os.Mkdir(sharedRoot, 0o755); err != nil {
		t.Fatalf("os.Mkdir(%q) failed: %v", sharedRoot, err)
	}
	links := map[string]string{
		filepath.Join(packsRoot, "zia"):      filepath.Join(repositoryRoot, "packs", "zia"),
		filepath.Join(sharedRoot, "zscaler"): filepath.Join(repositoryRoot, "packs", "_shared", "zscaler"),
	}
	for link, target := range links {
		if err := os.Symlink(target, link); err != nil {
			// Windows may prohibit unprivileged symlinks. The reduced fixture has
			// the same zia-only product/authority shape and keeps the precedence
			// regression active there; Unix differential lanes use the reviewer's
			// exact production-pack reproducer.
			t.Logf("os.Symlink(%q, %q) failed: %v; using reduced zia fixture", target, link, err)
			return writeRecordedFetchPack(t)
		}
	}
	profile := filepath.Join(repositoryRoot, "packsets", "zia.json")
	return recordedFetchPack{profile: profile, root: packsRoot}
}

func (*fetchClosingTransport) Request(collectors.HTTPRequest) (collectors.HTTPResponse, error) {
	return collectors.HTTPResponse{}, errors.New("empty fetch unexpectedly issued a request")
}

func (transport *fetchClosingTransport) Close() error {
	transport.closed = true
	return transport.closeErr
}

func buildFetchTestBinary(t *testing.T, root string) string {
	t.Helper()
	placeholder, err := os.CreateTemp(filepath.Join(root, "dist"), "iw-go-diff-fetch-*")
	if err != nil {
		t.Fatalf("create unique Go CLI output: %v", err)
	}
	goBinary := placeholder.Name()
	t.Cleanup(func() {
		if err := os.Remove(goBinary); err != nil && !os.IsNotExist(err) {
			t.Errorf("remove fetch test binary: %v", err)
		}
	})
	if err := placeholder.Close(); err != nil {
		t.Fatalf("close unique Go CLI output %q: %v", goBinary, err)
	}
	build := exec.Command("go", "build", "-o", goBinary, ".")
	build.Dir = filepath.Join(root, "go", "cmd", "iw")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building Go CLI: %v\n%s", err, output)
	}
	return goBinary
}

func fetchNoNetworkEnvironment() []string {
	return []string{
		"ALL_PROXY=",
		"HTTP_PROXY=",
		"HTTPS_PROXY=",
		"NO_PROXY=",
		"REQUESTS_CA_BUNDLE=",
		"SSL_CERT_FILE=",
		"all_proxy=",
		"http_proxy=",
		"https_proxy=",
		"no_proxy=",
	}
}

func recordedFetchEnvironment(fixture *recordedFetchFixture) []string {
	environment := fetchNoNetworkEnvironment()
	for index, entry := range environment {
		if strings.HasPrefix(entry, "REQUESTS_CA_BUNDLE=") {
			environment[index] = "REQUESTS_CA_BUNDLE=" + fixture.bundle
			break
		}
	}
	return append(environment,
		"ZIA_API_KEY="+recordedFetchAPIKey,
		"ZIA_CLOUD=",
		"ZIA_LEGACY_BASE_URL="+fixture.baseURL,
		"ZIA_PASSWORD="+recordedFetchPassword,
		"ZIA_USERNAME="+recordedFetchUsername,
		"ZSCALER_CLOUD=",
		"ZSCALER_USE_LEGACY_CLIENT=1",
	)
}

func requireRunResult(t *testing.T, actual runResult, exit int, stdout, stderr string) {
	t.Helper()
	if actual.exit != exit {
		t.Errorf("exit=%d, want %d\nstderr: %s", actual.exit, exit, actual.stderr)
	}
	if !bytes.Equal(actual.stdout, []byte(stdout)) {
		t.Errorf("stdout=%q, want %q", actual.stdout, stdout)
	}
	if !bytes.Equal(actual.stderr, []byte(stderr)) {
		t.Errorf("stderr=%q, want %q", actual.stderr, stderr)
	}
}

func compareFetchOracle(t *testing.T, root, goBinary string, args, environment []string) {
	t.Helper()
	oracleBundle := filepath.Join(root, "dist", "infrawright-cli.mjs")
	if _, err := os.Stat(oracleBundle); err != nil {
		if os.IsNotExist(err) {
			t.Skipf("Node oracle bundle absent (%s); build it with `npm run build:metadata-cli`", oracleBundle)
		}
		t.Fatalf("os.Stat(%q) failed: %v", oracleBundle, err)
	}
	nodeBinary, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not on PATH; the differential lane needs the pinned Node 24")
	}
	directory := t.TempDir()
	oracle := runBinaryWithEnv(
		t,
		directory,
		nodeBinary,
		append([]string{oracleBundle}, args...),
		environment,
	)
	candidate := runBinaryWithEnv(t, directory, goBinary, args, environment)
	if oracle.exit != candidate.exit {
		t.Errorf("exit: node=%d go=%d\nnode stderr: %s\ngo stderr: %s",
			oracle.exit, candidate.exit, oracle.stderr, candidate.stderr)
	}
	if !bytes.Equal(oracle.stdout, candidate.stdout) {
		t.Errorf("stdout diverges\nnode: %q\ngo:   %q", oracle.stdout, candidate.stdout)
	}
	if !bytes.Equal(oracle.stderr, candidate.stderr) {
		t.Errorf("stderr diverges\nnode: %q\ngo:   %q", oracle.stderr, candidate.stderr)
	}
}

func requireRunParity(t *testing.T, oracle, candidate runResult) {
	t.Helper()
	if oracle.exit != candidate.exit {
		t.Errorf("exit: node=%d go=%d\nnode stderr: %s\ngo stderr: %s",
			oracle.exit, candidate.exit, oracle.stderr, candidate.stderr)
	}
	if !bytes.Equal(oracle.stdout, candidate.stdout) {
		t.Errorf("stdout diverges\nnode: %q\ngo:   %q", oracle.stdout, candidate.stdout)
	}
	if !bytes.Equal(oracle.stderr, candidate.stderr) {
		t.Errorf("stderr diverges\nnode: %q\ngo:   %q", oracle.stderr, candidate.stderr)
	}
}

func requireRecordedFetchTranscript(
	t *testing.T,
	label string,
	actual, expected []recordedFetchRequest,
) {
	t.Helper()
	if len(actual) != len(expected) {
		t.Errorf("%s transcript has %d requests, want %d\n got: %+v\nwant: %+v",
			label, len(actual), len(expected), actual, expected)
		return
	}
	for index := range expected {
		if actual[index] != expected[index] {
			t.Errorf("%s transcript request %d = %+v, want %+v",
				label, index, actual[index], expected[index])
		}
	}
}

func takeRecordedFetchTranscript(
	t *testing.T,
	fixture *recordedFetchFixture,
	label string,
) []recordedFetchRequest {
	t.Helper()
	requests, violations := fixture.take()
	if len(violations) != 0 {
		t.Errorf("%s violated the recorded HTTP contract:\n  %s",
			label, strings.Join(violations, "\n  "))
	}
	return requests
}

func requireRecordedFetchTree(
	t *testing.T,
	label string,
	actual, expected map[string][]byte,
) {
	t.Helper()
	paths := make(map[string]struct{}, len(actual)+len(expected))
	for path := range actual {
		paths[path] = struct{}{}
	}
	for path := range expected {
		paths[path] = struct{}{}
	}
	ordered := make([]string, 0, len(paths))
	for path := range paths {
		ordered = append(ordered, path)
	}
	sort.Strings(ordered)
	for _, path := range ordered {
		actualBytes, actualOK := actual[path]
		expectedBytes, expectedOK := expected[path]
		switch {
		case !actualOK:
			t.Errorf("%s tree is missing %q", label, path)
		case !expectedOK:
			t.Errorf("%s tree has unexpected file %q", label, path)
		case !bytes.Equal(actualBytes, expectedBytes):
			t.Errorf("%s tree file %q differs\n got: %q\nwant: %q",
				label, path, actualBytes, expectedBytes)
		}
	}
}

func TestFetchRecordedTransportDifferentialAgainstNodeOracle(t *testing.T) {
	root := repoRoot(t)
	oracleBundle := filepath.Join(root, "dist", "infrawright-cli.mjs")
	if _, err := os.Stat(oracleBundle); err != nil {
		if os.IsNotExist(err) {
			t.Skipf("Node oracle bundle absent (%s); build it with `npm run build:metadata-cli`", oracleBundle)
		}
		t.Fatalf("os.Stat(%q) failed: %v", oracleBundle, err)
	}
	nodeBinary, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not on PATH; the differential lane needs the pinned Node 24")
	}

	goBinary := buildFetchTestBinary(t, root)
	pack := writeRecordedFetchPack(t)
	fixture := newRecordedFetchFixture(t)
	environment := recordedFetchEnvironment(fixture)
	metadataArguments := []string{
		"--root", pack.root,
		"--profile", pack.profile,
		"--catalog", pack.profile,
	}

	t.Run("fetch-diag-custom-ca", func(t *testing.T) {
		arguments := append([]string{"fetch-diag"}, metadataArguments...)
		nodeDirectory := t.TempDir()
		oracle := runBinaryWithEnv(
			t,
			nodeDirectory,
			nodeBinary,
			append([]string{oracleBundle}, arguments...),
			environment,
		)
		oracleTranscript := takeRecordedFetchTranscript(t, fixture, "node fetch-diag")

		goDirectory := t.TempDir()
		candidate := runBinaryWithEnv(t, goDirectory, goBinary, arguments, environment)
		candidateTranscript := takeRecordedFetchTranscript(t, fixture, "go fetch-diag")

		expectedStderr := fixture.host + ": system-trust FAIL (cannot reach " + fixture.baseURL + "/ (certificate failure)\n" +
			"hint: corporate TLS inspection? set REQUESTS_CA_BUNDLE to the exported proxy root CA); +bundle OK (HTTP 204)\n"
		requireRunResult(t, oracle, 0, "", expectedStderr)
		requireRunResult(t, candidate, 0, "", expectedStderr)
		requireRunParity(t, oracle, candidate)

		expectedTranscript := []recordedFetchRequest{{
			contract: "diagnostic",
			method:   http.MethodGet,
			uri:      "/",
		}}
		requireRecordedFetchTranscript(t, "node fetch-diag", oracleTranscript, expectedTranscript)
		requireRecordedFetchTranscript(t, "go fetch-diag", candidateTranscript, expectedTranscript)
		requireRecordedFetchTranscript(t, "node/go fetch-diag", candidateTranscript, oracleTranscript)

		emptyTree := map[string][]byte{}
		nodeTree := treeBytes(t, nodeDirectory)
		goTree := treeBytes(t, goDirectory)
		requireRecordedFetchTree(t, "node fetch-diag", nodeTree, emptyTree)
		requireRecordedFetchTree(t, "go fetch-diag", goTree, emptyTree)
		requireRecordedFetchTree(t, "node/go fetch-diag", goTree, nodeTree)
	})

	t.Run("fetch-legacy-zia-session", func(t *testing.T) {
		outputDirectory := filepath.Join("pulls", "tenant-a")
		arguments := append([]string{
			"fetch",
			"--tenant", "tenant-a",
			"--out", outputDirectory,
			"--resource", recordedFetchResourceType,
		}, metadataArguments...)

		nodeDirectory := t.TempDir()
		oracle := runBinaryWithEnv(
			t,
			nodeDirectory,
			nodeBinary,
			append([]string{oracleBundle}, arguments...),
			environment,
		)
		oracleTranscript := takeRecordedFetchTranscript(t, fixture, "node fetch")

		goDirectory := t.TempDir()
		candidate := runBinaryWithEnv(t, goDirectory, goBinary, arguments, environment)
		candidateTranscript := takeRecordedFetchTranscript(t, fixture, "go fetch")

		expectedStderr := "fetch: auth mode = legacy\n" +
			"fetch: proxy = not set\n" +
			"fetch: ZIA_CLOUD = <unset>\n" +
			"fetch: zia base = " + fixture.baseURL + " (override)\n" +
			"wrote " + filepath.Join(outputDirectory, recordedFetchResourceType+".json") + " (1 items)\n"
		requireRunResult(t, oracle, 0, "", expectedStderr)
		requireRunResult(t, candidate, 0, "", expectedStderr)
		requireRunParity(t, oracle, candidate)

		expectedTranscript := []recordedFetchRequest{
			{contract: "legacy-zia-auth", method: http.MethodPost, uri: "/api/v1/authenticatedSession"},
			{contract: "resource", method: http.MethodGet, uri: "/api/v1/recordedFixture"},
		}
		requireRecordedFetchTranscript(t, "node fetch", oracleTranscript, expectedTranscript)
		requireRecordedFetchTranscript(t, "go fetch", candidateTranscript, expectedTranscript)
		requireRecordedFetchTranscript(t, "node/go fetch", candidateTranscript, oracleTranscript)

		expectedTree := map[string][]byte{
			filepath.ToSlash(filepath.Join(outputDirectory, recordedFetchResourceType+".json")): []byte(recordedFetchArtifactJSON),
		}
		nodeTree := treeBytes(t, nodeDirectory)
		goTree := treeBytes(t, goDirectory)
		requireRecordedFetchTree(t, "node fetch", nodeTree, expectedTree)
		requireRecordedFetchTree(t, "go fetch", goTree, expectedTree)
		requireRecordedFetchTree(t, "node/go fetch", goTree, nodeTree)
	})
}

func TestFetchDiagValidatesHostBeforeTransportSetup(t *testing.T) {
	root := repoRoot(t)
	goBinary := buildFetchTestBinary(t, root)
	pack := reviewerZIAOnlyPack(t, root)
	arguments := []string{
		"fetch-diag",
		"--root", pack.root,
		"--profile", pack.profile,
		"--catalog", pack.profile,
	}
	// The first case is the credential-free zia-only adversarial-review
	// reproducer. HTTPS_PROXY is intentionally invalid: if transport setup runs
	// before ProbeRestHost validates the ZIA-derived target, INVALID_REST_PROXY
	// wins. The second case proves that deferring setup did not demote a genuine
	// construction failure into a nonfatal connectivity result.
	cases := []struct {
		name       string
		cloud      string
		wantStderr string
	}{
		{
			name:       "invalid-target-precedes-invalid-proxy",
			cloud:      "bad@evil",
			wantStderr: "error: diagnostic host must be a hostname with an optional port\n",
		},
		{
			name:  "valid-target-preserves-transport-setup-error",
			cloud: "example",
			wantStderr: "error: HTTP proxy configuration must be an http:// or https:// URL\n" +
				"  code: INVALID_REST_PROXY\n" +
				"  category: io\n" +
				"  retryable: no\n",
		},
	}
	candidates := make(map[string]runResult, len(cases))
	for _, testCase := range cases {
		t.Run("go/"+testCase.name, func(t *testing.T) {
			environment := []string{
				"HTTPS_PROXY=not-a-url",
				"ZIA_CLOUD=" + testCase.cloud,
				"ZSCALER_USE_LEGACY_CLIENT=1",
			}
			directory := t.TempDir()
			candidate := runBinaryWithEnv(t, directory, goBinary, arguments, environment)
			requireRunResult(t, candidate, 1, "", testCase.wantStderr)
			if artifacts := treeBytes(t, directory); len(artifacts) != 0 {
				t.Errorf("fetch-diag wrote artifacts before failing: %v", artifacts)
			}
			candidates[testCase.name] = candidate
		})
	}

	oracleBundle := filepath.Join(root, "dist", "infrawright-cli.mjs")
	if _, err := os.Stat(oracleBundle); err != nil {
		if os.IsNotExist(err) {
			t.Skipf("Node oracle bundle absent (%s); build it with `npm run build:metadata-cli`", oracleBundle)
		}
		t.Fatalf("os.Stat(%q) failed: %v", oracleBundle, err)
	}
	nodeBinary, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not on PATH; the differential lane needs the pinned Node 24")
	}
	for _, testCase := range cases {
		t.Run("node/"+testCase.name, func(t *testing.T) {
			environment := []string{
				"HTTPS_PROXY=not-a-url",
				"ZIA_CLOUD=" + testCase.cloud,
				"ZSCALER_USE_LEGACY_CLIENT=1",
			}
			directory := t.TempDir()
			oracle := runBinaryWithEnv(
				t,
				directory,
				nodeBinary,
				append([]string{oracleBundle}, arguments...),
				environment,
			)
			requireRunResult(t, oracle, 1, "", testCase.wantStderr)
			if artifacts := treeBytes(t, directory); len(artifacts) != 0 {
				t.Errorf("Node fetch-diag wrote artifacts before failing: %v", artifacts)
			}
			requireRunParity(t, oracle, candidates[testCase.name])
		})
	}
}

func TestFetchArgumentContractCredentialFree(t *testing.T) {
	root := repoRoot(t)
	goBinary := buildFetchTestBinary(t, root)
	environment := fetchNoNetworkEnvironment()
	cases := []struct {
		name   string
		args   []string
		exit   int
		stderr string
	}{
		{
			name:   "tenant-required",
			args:   []string{"fetch"},
			exit:   2,
			stderr: "error: fetch requires --tenant\n",
		},
		{
			name:   "tenant-validated",
			args:   []string{"fetch", "--tenant", "../escape"},
			exit:   2,
			stderr: "error: TENANT must match [A-Za-z0-9_.-]+ and not be . or .. (got '../escape')\n",
		},
		{
			name:   "concurrency-positive",
			args:   []string{"fetch", "--tenant", "tenant-a", "--concurrency", "0"},
			exit:   2,
			stderr: "error: --concurrency must be a positive integer\n",
		},
		{
			name:   "concurrency-bounded",
			args:   []string{"fetch", "--tenant", "tenant-a", "--concurrency", "65"},
			exit:   2,
			stderr: "error: --concurrency must not exceed 64\n",
		},
		{
			name:   "concurrency-overflow",
			args:   []string{"fetch", "--tenant", "tenant-a", "--concurrency", "999999999999999999999999999999"},
			exit:   2,
			stderr: "error: --concurrency must not exceed 64\n",
		},
		{
			name: "concurrency-not-repeatable",
			args: []string{
				"fetch", "--tenant", "tenant-a",
				"--concurrency", "1", "--concurrency", "2",
			},
			exit:   2,
			stderr: "error: --concurrency may be specified only once\n",
		},
		{
			name:   "diag-rejects-tenant",
			args:   []string{"fetch-diag", "--tenant", "tenant-a"},
			exit:   2,
			stderr: "error: fetch-diag does not accept --tenant\n",
		},
		{
			name:   "diag-rejects-resource",
			args:   []string{"fetch-diag", "--resource", "zia_advanced_settings"},
			exit:   2,
			stderr: "error: fetch-diag does not accept --resource\n",
		},
		{
			name:   "diag-rejects-concurrency",
			args:   []string{"fetch-diag", "--concurrency", "2"},
			exit:   2,
			stderr: "error: fetch-diag does not accept --concurrency\n",
		},
		{
			name:   "diag-rejects-output",
			args:   []string{"fetch-diag", "--out", "somewhere"},
			exit:   2,
			stderr: "error: fetch-diag does not accept --out\n",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			actual := runBinaryWithEnv(t, root, goBinary, testCase.args, environment)
			requireRunResult(t, actual, testCase.exit, "", testCase.stderr)
			compareFetchOracle(t, root, goBinary, testCase.args, environment)
		})
	}
}

func TestFetchEmptyPackRootMakesNoRequests(t *testing.T) {
	root := repoRoot(t)
	goBinary := buildFetchTestBinary(t, root)
	emptyPacks := filepath.Join(t.TempDir(), "packs")
	if err := os.Mkdir(emptyPacks, 0o777); err != nil {
		t.Fatal(err)
	}
	emptySet := filepath.Join(root, "packsets", "empty.json")
	environment := append(fetchNoNetworkEnvironment(), "FETCH_CONCURRENCY=65")
	args := []string{
		"fetch",
		"--root", emptyPacks,
		"--profile", emptySet,
		"--catalog", emptySet,
		"--tenant", "tenant-a",
	}
	directory := t.TempDir()
	actual := runBinaryWithEnv(t, directory, goBinary, args, environment)
	requireRunResult(t, actual, 0, "", ""+
		"fetch: auth mode = oneapi\n"+
		"fetch: proxy = not set\n"+
		"fetch: ZSCALER_CLOUD = (production)\n"+
		"fetch: ZSCALER_VANITY_DOMAIN = <unset>\n"+
		"fetch: token host = https://<vanity>.zslogin.net\n"+
		"fetch: gateway = https://api.zsapi.net\n")
	outputDirectory := filepath.Join(directory, "pulls", "tenant-a")
	info, err := os.Stat(outputDirectory)
	if err != nil {
		t.Fatalf("default output directory: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("default output path is not a directory: %s", outputDirectory)
	}
	if artifacts := treeBytes(t, outputDirectory); len(artifacts) != 0 {
		t.Errorf("empty fetch wrote artifacts: %v", artifacts)
	}

	diagArgs := []string{
		"fetch-diag",
		"--root", emptyPacks,
		"--profile", emptySet,
		"--catalog", emptySet,
	}
	diagnostic := runBinaryWithEnv(t, t.TempDir(), goBinary, diagArgs, environment)
	requireRunResult(t, diagnostic, 0, "", "")

	unknownSelectors := append(append([]string(nil), args...),
		"--resource", "beta", "--resource", "alpha")
	unknown := runBinaryWithEnv(t, t.TempDir(), goBinary, unknownSelectors, environment)
	requireRunResult(t, unknown, 2, "", ""+
		"error: unknown resource type(s)/product(s): alpha, beta\n"+
		"valid products: \n"+
		"valid resources: \n")

	lastWinsDirectory := t.TempDir()
	selectedOutput := filepath.Join(lastWinsDirectory, "selected-output")
	lastWinsArgs := []string{
		"fetch",
		"--root", filepath.Join(lastWinsDirectory, "missing-root"),
		"--root", emptyPacks,
		"--profile", filepath.Join(lastWinsDirectory, "missing-profile.json"),
		"--profile", emptySet,
		"--catalog", filepath.Join(lastWinsDirectory, "missing-catalog.json"),
		"--catalog", emptySet,
		"--tenant", "ignored-tenant",
		"--tenant", "selected-tenant",
		"--out", filepath.Join(lastWinsDirectory, "ignored-output"),
		"--out", selectedOutput,
	}
	lastWins := runBinaryWithEnv(t, lastWinsDirectory, goBinary, lastWinsArgs, environment)
	requireRunResult(t, lastWins, 0, "", string(actual.stderr))
	if info, err := os.Stat(selectedOutput); err != nil || !info.IsDir() {
		t.Fatalf("last --out did not select %s: info=%v err=%v", selectedOutput, info, err)
	}
	if _, err := os.Stat(filepath.Join(lastWinsDirectory, "ignored-output")); !os.IsNotExist(err) {
		t.Errorf("non-final --out unexpectedly used: %v", err)
	}
	if _, err := os.Stat(filepath.Join(lastWinsDirectory, "pulls")); !os.IsNotExist(err) {
		t.Errorf("--out unexpectedly fell back to tenant directory: %v", err)
	}

	compareFetchOracle(t, root, goBinary, args, environment)
	compareFetchOracle(t, root, goBinary, diagArgs, environment)
	compareFetchOracle(t, root, goBinary, unknownSelectors, environment)
}

func TestFetchTransportClosePrecedence(t *testing.T) {
	closeFailure := errors.New("close failed")
	t.Run("close-failure-after-success", func(t *testing.T) {
		transport := &fetchClosingTransport{closeErr: closeFailure}
		_, err := fetchWithOwnedTransport(transport, collectors.FetchResourcesOptions{
			OutputDirectory: filepath.Join(t.TempDir(), "pulls"),
		})
		if !errors.Is(err, closeFailure) {
			t.Fatalf("error=%v, want close failure", err)
		}
		if !transport.closed {
			t.Fatal("transport was not closed")
		}
	})

	t.Run("fetch-failure-remains-primary", func(t *testing.T) {
		transport := &fetchClosingTransport{closeErr: closeFailure}
		invalidConcurrency := 0
		_, err := fetchWithOwnedTransport(transport, collectors.FetchResourcesOptions{
			Concurrency:     &invalidConcurrency,
			OutputDirectory: filepath.Join(t.TempDir(), "pulls"),
		})
		if err == nil || errors.Is(err, closeFailure) {
			t.Fatalf("error=%v, want primary fetch failure", err)
		}
		if !transport.closed {
			t.Fatal("transport was not closed")
		}
	})
}
