package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

const sourceOperationSDKCLICompatibilitySHA256 = "3286fc0a962f3feb66bd6f5bc1416e5a9e1f2b6098b32148e4d34c83569da54a"
const sourceOperationSDKCLIProvider = "registry.terraform.io/digitalocean/digitalocean"

type sourceOperationSDKCLICompatibility struct {
	SchemaVersion int `json:"schema_version"`
	Case          struct {
		DiagnosticsBytes string         `json:"diagnostics_bytes"`
		ExitCode         int            `json:"exit_code"`
		RegistryBytes    string         `json:"registry_bytes"`
		Report           map[string]any `json:"report"`
		Stderr           string         `json:"stderr"`
		Stdout           string         `json:"stdout"`
	} `json:"case"`
}

func TestSourceOperationSDKRootCLICompatibility(t *testing.T) {
	fixturePath := filepath.Join("testdata", "source_operation_sdk_cli_compatibility.json")
	fixtureBytes, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatalf("os.ReadFile(%q) error: %v", fixturePath, err)
	}
	digest := sha256.Sum256(fixtureBytes)
	if got := hex.EncodeToString(digest[:]); got != sourceOperationSDKCLICompatibilitySHA256 {
		t.Fatalf("SHA256(%q) = %q, want %q", fixturePath, got, sourceOperationSDKCLICompatibilitySHA256)
	}
	var fixture sourceOperationSDKCLICompatibility
	if err := json.Unmarshal(fixtureBytes, &fixture); err != nil {
		t.Fatalf("json.Unmarshal(%q) error: %v", fixturePath, err)
	}
	if fixture.SchemaVersion != 1 {
		t.Fatalf("%s schema_version = %d, want 1", fixturePath, fixture.SchemaVersion)
	}
	if fixture.Case.Stdout != "" || fixture.Case.Stderr != "" {
		t.Fatalf("compatibility capture stdout/stderr = %q/%q, want empty", fixture.Case.Stdout, fixture.Case.Stderr)
	}

	root := t.TempDir()
	openAPIPath := filepath.Join(root, "openapi.json")
	schemaPath := filepath.Join(root, "schema.json")
	providerRoot := filepath.Join(root, "provider")
	sdkRoot := filepath.Join(root, "sdk")
	outputRoot := filepath.Join(root, "output")
	registryPath := filepath.Join(outputRoot, "registry.json")
	diagnosticsPath := filepath.Join(outputRoot, "diagnostics.json")
	if err := os.MkdirAll(outputRoot, 0o700); err != nil {
		t.Fatalf("os.MkdirAll(%q) error: %v", outputRoot, err)
	}
	writeSourceOperationSDKCLIFile(t, openAPIPath, `{"info":{"title":"SDK authority","version":"1"},"openapi":"3.0.3","paths":{"/v2/domains/{domain_name}":{"get":{"responses":{"200":{"description":"ok"}}}}}}`+"\n")
	writeSourceOperationSDKCLIFile(t, schemaPath, `{"provider_schemas":{"registry.terraform.io/digitalocean/digitalocean":{"resource_schemas":{"digitalocean_domain":{"block":{"attributes":{"name":{"required":true,"type":"string"}}}}}}}}`+"\n")
	writeSourceOperationSDKCLIFile(t, filepath.Join(providerRoot, "domain", "resource_digitalocean_domain.go"), "package domain\nfunc read() { client.Domains.Get(ctx, name) }\n")
	writeSourceOperationSDKCLIFile(t, filepath.Join(sdkRoot, "domains.go"), `package godo
const domainsBasePath = "v2/domains"
type DomainsServiceOp struct { client *Client }
func (s *DomainsServiceOp) Get(ctx context.Context, domain string) error { path := fmt.Sprintf("%s/%s", domainsBasePath, domain); _, err := s.client.NewRequest(ctx, http.MethodGet, path, nil); return err }
func (s *DomainsServiceOp) List(ctx context.Context) error { path := domainsBasePath; _, err := s.client.NewRequest(ctx, http.MethodGet, path, nil); return err }
`)

	repository := repoRoot(t)
	binary := buildGoV2AuthorityCLI(t, repository, "iw-go-source-operation-sdk")
	result := runBinaryWithEnv(t, repository, binary, []string{
		"source-operation-map",
		"--schema", schemaPath, "--openapi", openAPIPath, "--source-root", providerRoot,
		"--provider-source", sourceOperationSDKCLIProvider, "--resource-prefix", "digitalocean",
		"--sdk-root", sdkRoot, "--out", registryPath, "--diagnostics", diagnosticsPath,
	}, nil)
	if result.exit != fixture.Case.ExitCode {
		t.Errorf("iw source-operation-map exit = %d, want %d", result.exit, fixture.Case.ExitCode)
	}
	if got := string(result.stdout); got != fixture.Case.Stdout {
		t.Errorf("iw source-operation-map stdout = %q, want %q", got, fixture.Case.Stdout)
	}
	if got := string(result.stderr); got != fixture.Case.Stderr {
		t.Errorf("iw source-operation-map stderr = %q, want %q", got, fixture.Case.Stderr)
	}
	registryBytes, err := os.ReadFile(registryPath)
	if err != nil {
		t.Fatalf("os.ReadFile(%q) error: %v", registryPath, err)
	}
	if got := string(registryBytes); got != fixture.Case.RegistryBytes {
		t.Errorf("registry bytes = %q, want fixed %q", got, fixture.Case.RegistryBytes)
	}
	diagnosticsBytes, err := os.ReadFile(diagnosticsPath)
	if err != nil {
		t.Fatalf("os.ReadFile(%q) error: %v", diagnosticsPath, err)
	}
	if got := string(diagnosticsBytes); got != fixture.Case.DiagnosticsBytes {
		t.Errorf("diagnostics bytes = %q, want fixed %q", got, fixture.Case.DiagnosticsBytes)
	}
	outputTree := treeBytes(t, outputRoot)
	wantTree := map[string]string{
		"diagnostics.json": fixture.Case.DiagnosticsBytes,
		"registry.json":    fixture.Case.RegistryBytes,
	}
	if len(outputTree) != len(wantTree) {
		t.Errorf("iw source-operation-map output tree files = %d, want %d", len(outputTree), len(wantTree))
	}
	for path, want := range wantTree {
		got, ok := outputTree[path]
		if !ok {
			t.Errorf("iw source-operation-map output tree omitted %q", path)
			continue
		}
		if string(got) != want {
			t.Errorf("iw source-operation-map output %q = %q, want %q", path, got, want)
		}
	}
	for path := range outputTree {
		if _, ok := wantTree[path]; !ok {
			t.Errorf("iw source-operation-map output tree has unexpected path %q", path)
		}
	}

	var gotRegistry, gotDiagnostics any
	if err := json.Unmarshal(registryBytes, &gotRegistry); err != nil {
		t.Fatalf("json.Unmarshal(registry) error: %v", err)
	}
	if err := json.Unmarshal(diagnosticsBytes, &gotDiagnostics); err != nil {
		t.Fatalf("json.Unmarshal(diagnostics) error: %v", err)
	}
	wantRegistry := fixture.Case.Report["registry"]
	wantDiagnostics := map[string]any{"diagnostics": fixture.Case.Report["diagnostics"], "summary": fixture.Case.Report["summary"]}
	if !sourceOperationSDKCLIJSONEqual(gotRegistry, wantRegistry) {
		t.Errorf("registry JSON = %#v, want %#v", gotRegistry, wantRegistry)
	}
	if !sourceOperationSDKCLIJSONEqual(gotDiagnostics, wantDiagnostics) {
		t.Errorf("diagnostics JSON = %#v, want %#v", gotDiagnostics, wantDiagnostics)
	}
}

func writeSourceOperationSDKCLIFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("os.MkdirAll(%q) error: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("os.WriteFile(%q) error: %v", path, err)
	}
}

func sourceOperationSDKCLIJSONEqual(left, right any) bool {
	leftJSON, leftErr := json.Marshal(left)
	rightJSON, rightErr := json.Marshal(right)
	return leftErr == nil && rightErr == nil && string(leftJSON) == string(rightJSON)
}
