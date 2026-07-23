package sourceoperation

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The compatibility corpus is inert Go test evidence and requires no retired runtime.
const legacyMapperCompatibilitySHA256 = "38030168aaa30856c45c9acaf365a508eee9c6fd8fe9b3a80d9af9c19db44b0d"

type legacyMapperCompatibilityFixture struct {
	Derive  []legacyMapperCompatibilityDeriveCase `json:"derive_cases"`
	Reports []legacyMapperCompatibilityReportCase `json:"compatibility_report_cases"`
}

type legacyMapperCompatibilityDeriveCase struct {
	Name   string         `json:"name"`
	Input  map[string]any `json:"input"`
	Report map[string]any `json:"report"`
}

type legacyMapperCompatibilityReportCase struct {
	Name   string         `json:"name"`
	Report map[string]any `json:"report"`
}

func loadLegacyMapperCompatibilityFixture(t *testing.T) legacyMapperCompatibilityFixture {
	t.Helper()
	path := filepath.Join("testdata", "legacy_mapper_compatibility.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("os.ReadFile(%q) error: %v", path, err)
	}
	if got := fmt.Sprintf("%x", sha256.Sum256(data)); got != legacyMapperCompatibilitySHA256 {
		t.Fatalf("SHA256(%q) = %q, want %q", path, got, legacyMapperCompatibilitySHA256)
	}
	var fixture legacyMapperCompatibilityFixture
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatalf("json.Unmarshal(%q) error: %v", path, err)
	}
	if got, want := len(fixture.Derive), 39; got != want {
		t.Fatalf("compatibility derive cases = %d, want %d", got, want)
	}
	if got, want := len(fixture.Reports), 10; got != want {
		t.Fatalf("compatibility report cases = %d, want %d", got, want)
	}
	return fixture
}

func TestLegacyMapperCompatibilityDeriveCases(t *testing.T) {
	fixture := loadLegacyMapperCompatibilityFixture(t)
	for _, test := range fixture.Derive {
		t.Run(test.Name, func(t *testing.T) {
			root := t.TempDir()
			provider := filepath.Join(root, "provider")
			sdk := filepath.Join(root, "sdk")
			if legacyBool(test.Input["source_root_exists"]) {
				if err := os.MkdirAll(provider, 0o755); err != nil {
					t.Fatalf("os.MkdirAll(%q) error: %v", provider, err)
				}
				writeLegacyFixtureFiles(t, provider, legacyObject(test.Input["source_files"]))
			}
			if test.Input["sdk_files"] != nil {
				if err := os.MkdirAll(sdk, 0o755); err != nil {
					t.Fatalf("os.MkdirAll(%q) error: %v", sdk, err)
				}
				writeLegacyFixtureFiles(t, sdk, legacyObject(test.Input["sdk_files"]))
			}
			options := LegacyOptions{
				SchemaData:     legacyObject(test.Input["schema"]),
				OpenAPI:        legacyObject(test.Input["openapi"]),
				SourceRoot:     provider,
				ProviderSource: legacyString(test.Input["provider_source"]),
				ResourcePrefix: legacyString(test.Input["resource_prefix"]),
			}
			if values := legacyArray(test.Input["resource_filter"]); values != nil {
				for _, value := range values {
					options.Resources = append(options.Resources, legacyString(value))
				}
			}
			if test.Input["source_facts"] != nil {
				options.SourceFacts = legacyObject(test.Input["source_facts"])
			}
			if test.Input["sdk_files"] != nil {
				options.SDKRoot = sdk
			}
			got, err := DeriveLegacySourceOperationRegistry(options)
			if err != nil {
				t.Fatalf("DeriveLegacySourceOperationRegistry(%s) error = %v, want nil", test.Name, err)
			}
			want := marshalLegacyMapperCompatibilityJSON(t, test.Report)
			actual := marshalLegacyMapperCompatibilityJSON(t, got)
			if string(actual) != string(want) {
				t.Errorf("DeriveLegacySourceOperationRegistry(%s) mismatch: got %s, want %s", test.Name, actual, want)
			}
		})
	}
}

func TestLegacyMapperCompatibilityReports(t *testing.T) {
	fixture := loadLegacyMapperCompatibilityFixture(t)
	provider := "example/provider"
	schema := func(resources ...string) map[string]any {
		entries := map[string]any{}
		for _, resource := range resources {
			entries[resource] = map[string]any{"block": map[string]any{"attributes": map[string]any{}}}
		}
		return map[string]any{"provider_schemas": map[string]any{provider: map[string]any{"resource_schemas": entries}}}
	}
	openFolders := map[string]any{"paths": map[string]any{"/api/folders/{uid}": map[string]any{"get": map[string]any{"operationId": "RouteGetFolder"}}, "/api/folders": map[string]any{"get": map[string]any{"operationId": "RouteGetFolders"}}}}
	folderSource := "package provider\nfunc read() { client.Provisioning.GetFolder(ctx, id); client.Provisioning.GetFolders(ctx) }\n"
	t.Run("text_scanner", func(t *testing.T) {
		root := materializeLegacy(t, map[string]string{"internal/resource_folder.go": folderSource})
		assertLegacyMapperCompatibilityDerivation(t, fixture, "text_scanner", LegacyOptions{SchemaData: schema("example_folder"), OpenAPI: openFolders, SourceRoot: root, ProviderSource: provider, ResourcePrefix: "example"})
	})
	t.Run("ast_facts", func(t *testing.T) {
		root := materializeLegacy(t, map[string]string{"resource_folder.go": "package provider\n"})
		facts := legacyFacts("resource_folder.go", []any{legacySelector("resource_folder.go", []string{"client", "Provisioning", "GetFolders"}), legacySelector("resource_folder.go", []string{"client", "Provisioning", "GetFolder"})})
		candidate, err := DeriveLegacySourceOperationRegistry(LegacyOptions{SchemaData: schema("example_folder"), OpenAPI: openFolders, SourceRoot: root, ProviderSource: provider, ResourcePrefix: "example", SourceFacts: facts})
		if err != nil {
			t.Fatalf("DeriveLegacySourceOperationRegistry(ast_facts) error: %v", err)
		}
		assertLegacyMapperCompatibilityReport(t, fixture, "ast_facts", candidate)
		control, err := DeriveLegacySourceOperationRegistry(LegacyOptions{SchemaData: schema("example_folder"), OpenAPI: openFolders, SourceRoot: root, ProviderSource: provider, ResourcePrefix: "example"})
		if err != nil {
			t.Fatalf("DeriveLegacySourceOperationRegistry(ast_facts_comparison control) error: %v", err)
		}
		assertLegacyMapperCompatibilityReport(t, fixture, "ast_facts_comparison", CompareLegacySourceOperationReports(control, candidate))
	})
	t.Run("source_layout", func(t *testing.T) {
		files := map[string]string{"provider.go": "package provider\nimport project \"example.com/provider/internal/project\"\nvar resources = map[string]func(){\"example_registered\": resourceRegistered, \"example_packaged\": project.NewResource}\nfunc resourceRegistered() { _ = &Resource{Read: readRegistered} }\n", "registered/read.go": "package registered\nfunc readRegistered() { client.Registered.GetRegistered(ctx) }\n", "internal/project/resource.go": "package project\nfunc NewResource() { client.Packaged.GetPackaged(ctx) }\n", "internal/project/data_source_skip.go": "package project\nfunc ignored() { client.Wrong.GetWrong(ctx) }\n", "internal/services/service/resource.go": "package service\nfunc read() { client.Service.GetService(ctx) }\n", "internal/framework/resources/framework.go": "package resources\nimport external \"example.net/sdk\"\nfunc read() { external.GetFramework(ctx) }\n", "resource_raw.go": "package provider\nimport (\"fmt\"; \"net/http\")\nfunc readRaw() { _, _ = client.NewRequest(http.MethodGet, fmt.Sprintf(\"/raw/%s\", id), nil) }\n", "resource_graphql.go": "package provider\nimport \"github.com/shurcooL/githubv4\"\nfunc readGraphql() { githubv4.NewRequest() }\n"}
		root := materializeLegacy(t, files)
		names := []string{"registered", "packaged", "service", "framework", "raw", "graphql"}
		paths := map[string]any{}
		for _, name := range names {
			if name != "graphql" {
				paths["/"+name+"/{id}"] = map[string]any{"get": map[string]any{"operationId": "Get" + strings.ToUpper(name[:1]) + name[1:]}}
			}
		}
		assertLegacyMapperCompatibilityDerivation(t, fixture, "source_layout", LegacyOptions{SchemaData: schema("example_registered", "example_packaged", "example_service", "example_framework", "example_raw", "example_graphql"), OpenAPI: map[string]any{"paths": paths}, SourceRoot: root, ProviderSource: provider, ResourcePrefix: "example"})
	})
	widgetSDK := "package sdk\nconst widgetsBasePath = \"v2/widgets\"\ntype WidgetsServiceOp struct { client *Client }\nfunc (s *WidgetsServiceOp) Get(ctx context.Context, id string) error { path := fmt.Sprintf(\"%s/%s\", widgetsBasePath, id); _, err := s.client.NewRequest(ctx, http.MethodGet, path, nil); return err }\nfunc (s *WidgetsServiceOp) Create(ctx context.Context) error { path := widgetsBasePath; _, err := s.client.NewRequest(ctx, http.MethodPost, path, nil); return err }\n"
	widgetOpenAPI := map[string]any{"paths": map[string]any{"/v2/widgets/{widget_id}": map[string]any{"get": map[string]any{"operationId": "GetWidget"}}, "/v2/widgets": map[string]any{"post": map[string]any{"operationId": "CreateWidget"}}}}
	t.Run("sdk_text_and_facts", func(t *testing.T) {
		root := materializeLegacy(t, map[string]string{"resource_widget.go": "package provider\nfunc read() { client.Widgets.Get(ctx, id); client.Widgets.Create(ctx) }\n"})
		sdk := materializeLegacy(t, map[string]string{"widgets.go": widgetSDK})
		base := LegacyOptions{SchemaData: schema("example_widget"), OpenAPI: widgetOpenAPI, SourceRoot: root, SDKRoot: sdk, ProviderSource: provider, ResourcePrefix: "example"}
		assertLegacyMapperCompatibilityDerivation(t, fixture, "sdk_text", base)
		facts := legacyFacts("resource_widget.go", []any{legacySelector("resource_widget.go", []string{"client", "Widgets", "Get"}), legacySelector("resource_widget.go", []string{"client", "Widgets", "Create"})})
		base.SourceFacts = facts
		assertLegacyMapperCompatibilityDerivation(t, fixture, "sdk_facts", base)
	})
	t.Run("ambiguity_relationship", func(t *testing.T) {
		root := materializeLegacy(t, map[string]string{"resource_thing.go": "package provider\nvar name = \"example_thing\"\nfunc read() { client.ThingsAPI.GetThing(ctx, id); client.ThingsAPI.RetrieveThing(ctx, uid) }\n", "resource_repository_topics.go": "package provider\nvar name = \"example_repository_topics\"\nfunc read() { client.Repositories.ListAllTopics(ctx, owner, repo, nil) }\n"})
		openapi := map[string]any{"paths": map[string]any{"/things/{id}": map[string]any{"get": map[string]any{"operationId": "GetThing"}}, "/things/{uid}": map[string]any{"get": map[string]any{"operationId": "RetrieveThing"}}, "/repos/{owner}/{repo}/topics": map[string]any{"get": map[string]any{"operationId": "repos/get-all-topics"}}}}
		assertLegacyMapperCompatibilityDerivation(t, fixture, "ambiguity_relationship", LegacyOptions{SchemaData: schema("example_thing", "example_repository_topics"), OpenAPI: openapi, SourceRoot: root, ProviderSource: provider, ResourcePrefix: "example"})
	})
	t.Run("escaped_and_unresolved_rest", func(t *testing.T) {
		openapi := map[string]any{"paths": map[string]any{"/widgets/{id}": map[string]any{"get": map[string]any{"operationId": "GetWidget"}}}}
		root := materializeLegacy(t, map[string]string{"resource_widget.go": "package provider\nfunc read() { client.NewRequest(\"GET\", \"\\x2fwidgets\\u002f%s\", nil) }\n"})
		base := LegacyOptions{SchemaData: schema("example_widget"), OpenAPI: openapi, SourceRoot: root, ProviderSource: provider, ResourcePrefix: "example"}
		assertLegacyMapperCompatibilityDerivation(t, fixture, "escaped_rest_text", base)
		base.SourceFacts = legacyFacts("resource_widget.go", []any{legacySelector("resource_widget.go", []string{})})
		base.SourceFacts["selector_calls"] = []any{map[string]any{"file": "resource_widget.go", "parts": []any{}, "symbol": "client.Widgets.Get"}}
		assertLegacyMapperCompatibilityDerivation(t, fixture, "escaped_rest_facts", base)
		base.SourceFacts = legacyFacts("resource_widget.go", nil)
		base.SourceFacts["raw_rest_calls"] = []any{map[string]any{"file": "resource_widget.go", "method": "GET", "symbol": "client.NewRequest"}}
		assertLegacyMapperCompatibilityDerivation(t, fixture, "unresolved_rest_facts", base)
	})
}

func materializeLegacy(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for name, text := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("os.MkdirAll(%q) error: %v", filepath.Dir(path), err)
		}
		if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
			t.Fatalf("os.WriteFile(%q) error: %v", path, err)
		}
	}
	return root
}

func legacyFacts(file string, selectors []any) map[string]any {
	return map[string]any{
		"source_root":            "fixture",
		"files":                  []any{map[string]any{"path": file, "package": "provider", "imports": []any{}}},
		"functions":              []any{},
		"resource_registrations": []any{},
		"resource_references":    []any{},
		"identifier_references":  []any{},
		"read_callbacks":         []any{},
		"package_calls":          []any{},
		"raw_rest_calls":         []any{},
		"selector_calls":         selectors,
	}
}

func legacySelector(file string, parts []string) map[string]any {
	values := make([]any, len(parts))
	for i := range parts {
		values[i] = parts[i]
	}
	symbol := "client"
	if len(parts) > 1 {
		symbol += "." + strings.Join(parts[1:], ".")
	}
	return map[string]any{"file": file, "parts": values, "symbol": symbol}
}

func assertLegacyMapperCompatibilityDerivation(
	t *testing.T,
	fixture legacyMapperCompatibilityFixture,
	name string,
	options LegacyOptions,
) {
	t.Helper()
	got, err := DeriveLegacySourceOperationRegistry(options)
	if err != nil {
		t.Fatalf("DeriveLegacySourceOperationRegistry(%s) error: %v", name, err)
	}
	assertLegacyMapperCompatibilityReport(t, fixture, name, got)
}

func assertLegacyMapperCompatibilityReport(
	t *testing.T,
	fixture legacyMapperCompatibilityFixture,
	name string,
	got map[string]any,
) {
	t.Helper()
	for _, test := range fixture.Reports {
		if test.Name == name {
			want := marshalLegacyMapperCompatibilityJSON(t, test.Report)
			actual := marshalLegacyMapperCompatibilityJSON(t, got)
			if string(actual) != string(want) {
				t.Errorf("compatibility report %q mismatch: got %s, want %s", name, actual, want)
			}
			return
		}
	}
	t.Fatalf("compatibility authority %q not found", name)
}

func marshalLegacyMapperCompatibilityJSON(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("json.Marshal(%T) error: %v", value, err)
	}
	return data
}

func writeLegacyFixtureFiles(t *testing.T, root string, files map[string]any) {
	t.Helper()
	for name, value := range files {
		filename := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(filename), 0o755); err != nil {
			t.Fatalf("os.MkdirAll(%q) error: %v", filepath.Dir(filename), err)
		}
		if err := os.WriteFile(filename, []byte(legacyString(value)), 0o600); err != nil {
			t.Fatalf("os.WriteFile(%q) error: %v", filename, err)
		}
	}
}
