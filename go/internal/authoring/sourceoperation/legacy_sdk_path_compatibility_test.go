package sourceoperation

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/dvmrry/infrawright-dev/go/internal/canonjson"
)

const sdkPathCompatibilitySHA256 = "cf86f1e8e7095562ed4f777997af4c93735aa6610e4b199e855c3deef5f80b0c"
const digitalOceanProvider = "registry.terraform.io/digitalocean/digitalocean"

const sdkScannerSource = `package sdk
import ("context"; "fmt"; "net/http")
const widgetsBasePath = "v2/widgets"
const ( groupedBasePath = "v2/grouped" )
type WidgetsServiceOp struct { client *Client }
type GroupedClient struct { client *Client }
type RawAPI struct { client *Client }
func (s *WidgetsServiceOp) Get(ctx context.Context, widgetID int) error {
  // path := "ignored"
  quoted := "brace } retained by body scanner"
  _ = quoted
  path := fmt.Sprintf("%s/%d", widgetsBasePath, widgetID)
  path = fmt.Sprintf("%s/%s/%v", path, "literal", complexID())
  _, err := s.client.NewRequest(ctx, http.MethodGet, path, nil)
  return err
}
func (s WidgetsServiceOp) List(ctx context.Context) error {
  path := widgetsBasePath
  _, err := s.client.NewRequest(ctx, "GET", path, nil)
  return err
}
func (s *GroupedClient) Read(ctx context.Context, id string) error {
  path := fmt.Sprintf("%s/%s", groupedBasePath, id)
  _, err := s.client.NewRequest(ctx, http.MethodGet, path, nil)
  return err
}
func (s *RawAPI) Create(ctx context.Context) error {
  path := widgetsBasePath
  raw := ` + "`quoted { raw }`" + `
  _ = raw
  _, err := s.client.NewRequest(ctx, http.MethodPost, path, nil)
  return err
}
func (s *WidgetsService) MissingMethod(ctx context.Context, id string) error {
  path := fmt.Sprintf("%s/%s", widgetsBasePath, id)
  return nil
}
func (s *WidgetsService) MissingPath(ctx context.Context) error {
  _, err := s.client.NewRequest(ctx, http.MethodGet, unknown, nil)
  return err
}
`

const sdkTestsOnlySource = `package sdk
const testsBasePath = "v2/tests"
type TestsServiceOp struct { client *Client }
func (s *TestsServiceOp) Get(ctx context.Context) error {
  path := testsBasePath
  _, err := s.client.NewRequest(ctx, http.MethodGet, path, nil)
  return err
}
`

const domainsSDK = `package godo
const domainsBasePath = "v2/domains"
type DomainsServiceOp struct { client *Client }
func (s *DomainsServiceOp) Get(ctx context.Context, domain string) error { path := fmt.Sprintf("%s/%s", domainsBasePath, domain); _, err := s.client.NewRequest(ctx, http.MethodGet, path, nil); return err }
func (s *DomainsServiceOp) List(ctx context.Context) error { path := domainsBasePath; _, err := s.client.NewRequest(ctx, http.MethodGet, path, nil); return err }
`
const dropletsSDK = `package godo
const dropletsBasePath = "v2/droplets"
type DropletsServiceOp struct { client *Client }
func (s *DropletsServiceOp) Get(ctx context.Context, id int) error { path := fmt.Sprintf("%s/%d", dropletsBasePath, id); _, err := s.client.NewRequest(ctx, http.MethodGet, path, nil); return err }
`
const vpcsSDK = `package godo
const vpcsBasePath = "v2/vpcs"
type VPCsServiceOp struct { client *Client }
func (s *VPCsServiceOp) Get(ctx context.Context, vpcID string) error { path := fmt.Sprintf("%s/%s", vpcsBasePath, vpcID); _, err := s.client.NewRequest(ctx, http.MethodGet, path, nil); return err }
`
const reservedIPsSDK = `package godo
const reservedIPsBasePath = "v2/reserved_ips"
type ReservedIPsServiceOp struct { client *Client }
func (s *ReservedIPsServiceOp) Get(ctx context.Context, ip string) error { path := fmt.Sprintf("%s/%s", reservedIPsBasePath, ip); _, err := s.client.NewRequest(ctx, http.MethodGet, path, nil); return err }
func (s *ReservedIPsServiceOp) Assign(ctx context.Context, ip string) error { path := fmt.Sprintf("%s/%s/actions", reservedIPsBasePath, ip); _, err := s.client.NewRequest(ctx, http.MethodPost, path, nil); return err }
`
const reservedIPv6sSDK = `package godo
const reservedIPv6sBasePath = "v2/reserved_ipv6"
type ReservedIPV6sServiceOp struct { client *Client }
func (s *ReservedIPV6sServiceOp) Get(ctx context.Context, ip string) error { path := fmt.Sprintf("%s/%s", reservedIPv6sBasePath, ip); _, err := s.client.NewRequest(ctx, http.MethodGet, path, nil); return err }
`
const actionsSDK = `package godo
const actionsBasePath = "v2/actions"
type ActionsServiceOp struct { client *Client }
func (s *ActionsServiceOp) Get(ctx context.Context, id int) error { path := fmt.Sprintf("%s/%d", actionsBasePath, id); _, err := s.client.NewRequest(ctx, http.MethodGet, path, nil); return err }
`
const thingsSDK = `package godo
const thingsBasePath = "v2/things"
type ThingsServiceOp struct { client *Client }
func (s *ThingsServiceOp) Get(ctx context.Context, id int) error { path := fmt.Sprintf("%s/%d", thingsBasePath, id); _, err := s.client.NewRequest(ctx, http.MethodGet, path, nil); return err }
`

func loadSDKPathCompatibility(t *testing.T) map[string]any {
	t.Helper()
	path := filepath.Join("testdata", "sdk_path_compatibility.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("os.ReadFile(%q) error: %v", path, err)
	}
	digest := sha256.Sum256(data)
	if got := hex.EncodeToString(digest[:]); got != sdkPathCompatibilitySHA256 {
		t.Fatalf("SHA256(%q) = %q, want %q", path, got, sdkPathCompatibilitySHA256)
	}
	decoded, err := canonjson.Decode(data)
	if err != nil {
		t.Fatalf("canonjson.Decode(%q) error: %v", path, err)
	}
	root := legacyObject(decoded)
	if legacyV1Number(root["schema_version"]) != 1 {
		t.Fatalf("%s schema_version = %v, want 1", path, root["schema_version"])
	}
	return legacyObject(root["cases"])
}

func TestExtractSDKPathsCompatibility(t *testing.T) {
	root := t.TempDir()
	files := map[string][]byte{
		".git/ignored.go":     []byte(sdkScannerSource),
		"a/widgets.go":        []byte(sdkScannerSource),
		"a/widgets_test.go":   []byte(sdkScannerSource),
		"test/ignored.go":     []byte(sdkScannerSource),
		"testdata/ignored.go": []byte(sdkScannerSource),
		"tests/only.go":       []byte(sdkTestsOnlySource),
		"z/invalid.go":        {0xff, 0xfe, 0xfd},
	}
	for relative, content := range files {
		writeSDKCompatibilityFile(t, root, relative, content)
	}
	evidence, unresolved, err := ExtractSDKPaths(root)
	if err != nil {
		t.Fatalf("ExtractSDKPaths() error: %v", err)
	}
	assertSDKCompatibilityJSON(t, map[string]any{"evidence": evidence, "unresolved": unresolved}, loadSDKPathCompatibility(t)["supported_path_shapes"])

	missingEvidence, missingUnresolved, err := ExtractSDKPaths(filepath.Join(root, "missing"))
	if err != nil {
		t.Fatalf("ExtractSDKPaths(missing) error: %v", err)
	}
	assertSDKCompatibilityJSON(t, map[string]any{"evidence": missingEvidence, "unresolved": missingUnresolved}, loadSDKPathCompatibility(t)["missing_root"])

	discoveryRoot := t.TempDir()
	for _, relative := range []string{"a/ä.go", "a/z.go", "b/a.go", "b/a_test.go", "tests/included.go"} {
		writeSDKCompatibilityFile(t, discoveryRoot, relative, []byte("package fixture"))
	}
	discovered, err := DiscoverSDKGoFiles(discoveryRoot)
	if err != nil {
		t.Fatalf("DiscoverSDKGoFiles() error: %v", err)
	}
	for index := range discovered {
		discovered[index], _ = filepath.Rel(discoveryRoot, discovered[index])
		discovered[index] = filepath.ToSlash(discovered[index])
	}
	if want := []string{"a/z.go", "a/ä.go", "b/a.go", "tests/included.go"}; !reflect.DeepEqual(discovered, want) {
		t.Errorf("DiscoverSDKGoFiles() = %#v, want %#v", discovered, want)
	}
}

func TestSDKSourceOperationReportsCompatibility(t *testing.T) {
	cases := legacyObject(loadSDKPathCompatibility(t)["source_operation_reports"])
	detail := func(segment, parameter string) map[string]any {
		return map[string]any{"/v2/" + segment + "/{" + parameter + "}": map[string]any{"get": map[string]any{"responses": map[string]any{"200": map[string]any{"description": "ok"}}}}}
	}
	domainPaths := detail("domains", "domain_name")
	domainPaths["/v2/domains"] = map[string]any{"get": map[string]any{"responses": map[string]any{"200": map[string]any{"description": "ok"}}}}
	actionPaths := detail("reserved_ips", "ip")
	actionPaths["/v2/reserved_ips/{ip}/actions"] = map[string]any{"post": map[string]any{"responses": map[string]any{"201": map[string]any{"description": "ok"}}}}
	tests := []struct {
		name, resource, calls string
		paths                 map[string]any
		sdk                   map[string]string
		facts                 func(string, string) map[string]any
	}{
		{name: "domain", resource: "digitalocean_domain", calls: "client.Domains.Get(ctx, name)", paths: domainPaths, sdk: map[string]string{"domains.go": domainsSDK}},
		{name: "droplet", resource: "digitalocean_droplet", calls: "client.Droplets.Get(ctx, id)", paths: detail("droplets", "droplet_id"), sdk: map[string]string{"droplets.go": dropletsSDK}},
		{name: "vpc", resource: "digitalocean_vpc", calls: "client.VPCs.Get(ctx, id)", paths: detail("vpcs", "vpc_id"), sdk: map[string]string{"vpcs.go": vpcsSDK}},
		{name: "reserved_ip_action", resource: "digitalocean_reserved_ip", calls: "client.ReservedIPs.Get(ctx, name)\nclient.ReservedIPs.Assign(ctx, name)", paths: actionPaths, sdk: map[string]string{"reserved_ips.go": reservedIPsSDK}},
		{name: "ast_sdk_action", resource: "digitalocean_reserved_ip", paths: actionPaths, sdk: map[string]string{"reserved_ips.go": reservedIPsSDK}, facts: reservedIPSourceFacts},
		{name: "helper_action_disambiguation", resource: "digitalocean_reserved_ipv6", calls: "client.Actions.Get(ctx, actionID)\nclient.ReservedIPV6s.Get(ctx, name)", paths: mergeSDKPaths(detail("actions", "action_id"), detail("reserved_ipv6", "reserved_ipv6")), sdk: map[string]string{"action.go": actionsSDK, "reserved_ipv6.go": reservedIPv6sSDK}},
		{name: "fuzzy_fallback", resource: "digitalocean_domain", calls: "client.Domains.Get(ctx, name)", paths: domainPaths},
		{name: "unresolved_ambiguous_path", resource: "digitalocean_thing", calls: "client.Things.Get(ctx, id)", paths: mergeSDKPaths(detail("things", "a"), detail("things", "b")), sdk: map[string]string{"things.go": thingsSDK}},
		{name: "unresolved_openapi_path", resource: "digitalocean_domain", calls: "client.Domains.Get(ctx, name)", paths: map[string]any{"/v2/something_else": map[string]any{"get": map[string]any{"responses": map[string]any{"200": map[string]any{"description": "ok"}}}}}, sdk: map[string]string{"domains.go": domainsSDK}},
		{name: "unresolved_symbol", resource: "digitalocean_domain", calls: "client.Domains.Get(ctx, name)", paths: detail("domains", "domain_name"), sdk: map[string]string{"vpcs.go": vpcsSDK}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := deriveSDKCompatibilityReport(t, test.resource, test.calls, test.paths, test.sdk, test.facts)
			assertSDKCompatibilityJSON(t, got, cases[test.name])
		})
	}
}

func deriveSDKCompatibilityReport(t *testing.T, resource, calls string, paths map[string]any, sdk map[string]string, facts func(string, string) map[string]any) map[string]any {
	t.Helper()
	root := t.TempDir()
	bare := strings.TrimPrefix(resource, "digitalocean_")
	relative := filepath.ToSlash(filepath.Join(bare, "resource_"+resource+".go"))
	writeSDKCompatibilityFile(t, filepath.Join(root, "provider"), relative, []byte("package "+bare+"\nfunc read() {\n"+calls+"\n}\n"))
	for name, content := range sdk {
		writeSDKCompatibilityFile(t, filepath.Join(root, "sdk"), name, []byte(content))
	}
	options := LegacyOptions{
		SchemaData: digitalOceanCompatibilitySchema(resource),
		OpenAPI:    map[string]any{"openapi": "3.0.3", "paths": paths},
		SourceRoot: filepath.Join(root, "provider"), ProviderSource: digitalOceanProvider, ResourcePrefix: "digitalocean",
	}
	if sdk != nil {
		options.SDKRoot = filepath.Join(root, "sdk")
	}
	if facts != nil {
		options.SourceFacts = facts(options.SourceRoot, relative)
	}
	report, err := DeriveLegacySourceOperationRegistry(options)
	if err != nil {
		t.Fatalf("DeriveLegacySourceOperationRegistry() error: %v", err)
	}
	return report
}

func digitalOceanCompatibilitySchema(resource string) map[string]any {
	return map[string]any{"provider_schemas": map[string]any{digitalOceanProvider: map[string]any{"resource_schemas": map[string]any{resource: map[string]any{"block": map[string]any{"attributes": map[string]any{"name": map[string]any{"required": true, "type": "string"}}}}}}}}
}

func reservedIPSourceFacts(sourceRoot, relative string) map[string]any {
	return map[string]any{
		"files": []any{map[string]any{"package": "reserved_ip", "path": relative}}, "functions": []any{}, "identifier_references": []any{}, "package_calls": []any{}, "raw_rest_calls": []any{}, "read_callbacks": []any{},
		"resource_references": []any{map[string]any{"file": relative, "resource": "digitalocean_reserved_ip"}}, "resource_registrations": []any{},
		"selector_calls": []any{
			map[string]any{"file": relative, "function": "read", "parts": []any{"client", "ReservedIPs", "Get"}, "symbol": "client.ReservedIPs.Get"},
			map[string]any{"file": relative, "function": "read", "parts": []any{"client", "ReservedIPs", "Assign"}, "symbol": "client.ReservedIPs.Assign"},
		},
		"source_root": sourceRoot,
	}
}

func mergeSDKPaths(left, right map[string]any) map[string]any {
	result := map[string]any{}
	for key, value := range left {
		result[key] = value
	}
	for key, value := range right {
		result[key] = value
	}
	return result
}

func writeSDKCompatibilityFile(t *testing.T, root, relative string, content []byte) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("os.MkdirAll(%q) error: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatalf("os.WriteFile(%q) error: %v", path, err)
	}
}

func assertSDKCompatibilityJSON(t *testing.T, got, want any) {
	t.Helper()
	gotStandardJSON, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("json.Marshal(got) error: %v", err)
	}
	gotNormalized, err := canonjson.Decode(gotStandardJSON)
	if err != nil {
		t.Fatalf("canonjson.Decode(got) error: %v", err)
	}
	gotJSON, err := canonjson.Render(gotNormalized)
	if err != nil {
		t.Fatalf("canonjson.Render(got) error: %v", err)
	}
	wantJSON, err := canonjson.Render(want)
	if err != nil {
		t.Fatalf("canonjson.Render(want) error: %v", err)
	}
	if gotJSON != wantJSON {
		t.Errorf("compatibility JSON = %s, want %s", gotJSON, wantJSON)
	}
}
