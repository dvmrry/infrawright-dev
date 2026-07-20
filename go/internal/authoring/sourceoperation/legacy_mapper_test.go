package sourceoperation

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

type legacyFixture struct {
	Derive  []legacyDeriveCase `json:"derive_cases"`
	Helpers map[string]any     `json:"helper_cases"`
	Node    []legacyNodeCase   `json:"node_differential_cases"`
}
type legacyDeriveCase struct {
	Name   string         `json:"name"`
	Input  map[string]any `json:"input"`
	Report map[string]any `json:"report"`
}
type legacyNodeCase struct {
	Name   string         `json:"name"`
	Report map[string]any `json:"report"`
}

func TestLegacyMapperFrozenV1DeriveCases(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "node-tests", "fixtures", "python-source-operation-map-v1.json"))
	if err != nil {
		t.Fatalf("ReadFile(frozen fixture) error = %v, want nil", err)
	}
	if got := fmt.Sprintf("%x", sha256.Sum256(data)); got != "1ca673c06162f24e9c3a10a91724a98ce9c317af8906d6105cbb0c226ec8fd14" {
		t.Fatalf("frozen fixture SHA256 = %s, want 1ca673c06162f24e9c3a10a91724a98ce9c317af8906d6105cbb0c226ec8fd14", got)
	}
	var fixture legacyFixture
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatalf("Unmarshal(frozen fixture) error = %v, want nil", err)
	}
	if len(fixture.Derive) != 39 {
		t.Fatalf("frozen derive case count = %d, want 39", len(fixture.Derive))
	}
	for _, test := range fixture.Derive {
		t.Run(test.Name, func(t *testing.T) {
			root := t.TempDir()
			provider := filepath.Join(root, "provider")
			sdk := filepath.Join(root, "sdk")
			if legacyBool(test.Input["source_root_exists"]) {
				if err := os.MkdirAll(provider, 0o755); err != nil {
					t.Fatal(err)
				}
				writeLegacyFixtureFiles(t, provider, legacyObject(test.Input["source_files"]))
			}
			if test.Input["sdk_files"] != nil {
				if err := os.MkdirAll(sdk, 0o755); err != nil {
					t.Fatal(err)
				}
				writeLegacyFixtureFiles(t, sdk, legacyObject(test.Input["sdk_files"]))
			}
			options := LegacyOptions{SchemaData: legacyObject(test.Input["schema"]), OpenAPI: legacyObject(test.Input["openapi"]), SourceRoot: provider, ProviderSource: legacyString(test.Input["provider_source"]), ResourcePrefix: legacyString(test.Input["resource_prefix"])}
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
			var normalizedWant, normalizedGot any
			wantBytes, _ := json.Marshal(test.Report)
			gotBytes, _ := json.Marshal(got)
			_ = json.Unmarshal(wantBytes, &normalizedWant)
			_ = json.Unmarshal(gotBytes, &normalizedGot)
			if !reflect.DeepEqual(normalizedWant, normalizedGot) {
				want, _ := json.MarshalIndent(test.Report, "", "  ")
				actual, _ := json.MarshalIndent(got, "", "  ")
				t.Errorf("DeriveLegacySourceOperationRegistry(%s) mismatch\nwant %s\ngot  %s", test.Name, want, actual)
			}
		})
	}
}

func TestLegacyMapperFrozenV1Helpers(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "node-tests", "fixtures", "python-source-operation-map-v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fixture legacyFixture
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	if got := SDKMethodRole("GetIPAddresses"); got != fixture.Helpers["sdk_ip_get_member_role"] {
		t.Errorf("SDKMethodRole(GetIPAddresses) = %q, want %v", got, fixture.Helpers["sdk_ip_get_member_role"])
	}
	if got := SDKMethodRole("ListIPAddresses"); got != fixture.Helpers["sdk_ip_list_members_role"] {
		t.Errorf("SDKMethodRole(ListIPAddresses) = %q, want %v", got, fixture.Helpers["sdk_ip_list_members_role"])
	}
	if got := legacySortedSet(GoIdentifierTokens("r client IAM UserGroups Members List rclientiamusergroupsmemberslist")); !reflect.DeepEqual(got, []string{"client", "iam", "list", "members", "r", "rclientiamusergroupsmemberslist", "usergroups"}) {
		t.Errorf("GoIdentifierTokens() = %#v, want frozen helper output", got)
	}
	for _, test := range []struct{ name, path string }{{"path_kind_playlist", "/playlists/{id}"}, {"path_kind_product_list", "/products/list/{id}"}, {"path_kind_product_search", "/products/search/{id}"}} {
		if got := legacyPathKind(legacyOperation{Path: test.path}); got != fixture.Helpers[test.name] {
			t.Errorf("legacyPathKind(%q) = %q, want %v", test.path, got, fixture.Helpers[test.name])
		}
	}
	if got := legacyMethodTokens(map[string]any{"method": "ListIPAddresses"}); !reflect.DeepEqual(got, []string{"addresses", "ips"}) {
		t.Errorf("legacyMethodTokens(ListIPAddresses) = %#v, want [addresses ips]", got)
	}
	if got := OperationAliases("RouteGetWidget"); !reflect.DeepEqual(got, []string{"getwidget", "getwidgetwithresponse", "retrievewidretrieve", "retrievewidretrievewithresponse", "routegetwidget", "routegetwidgetwithresponse", "routeretrievewidretrieve", "routeretrievewidretrievewithresponse"}) {
		t.Errorf("OperationAliases(RouteGetWidget) = %#v, want frozen alias expansion", got)
	}
	spec := map[string]any{"paths": map[string]any{"/z": map[string]any{"get": map[string]any{}}, "/a": map[string]any{"post": map[string]any{"operationId": "CreateA"}}}}
	inventory := OpenAPIOperationInventory(spec)
	if got := []string{legacyString(inventory[0]["path"]), legacyString(inventory[0]["operation_id"]), legacyString(inventory[1]["path"]), legacyString(inventory[1]["operation_id"])}; !reflect.DeepEqual(got, []string{"/a", "CreateA", "/z", "GET /z"}) {
		t.Errorf("OpenAPIOperationInventory() = %#v, want sorted explicit/synthetic inventory", got)
	}
	if got := NormalizeRawRESTPath(" orgs/%s//actions/%08d "); got != "/orgs/{arg}/actions/{arg}" {
		t.Errorf("NormalizeRawRESTPath() = %q, want /orgs/{arg}/actions/{arg}", got)
	}
	if len(fixture.Derive) != 39 {
		t.Errorf("derive authority vectors = %d, want 39", len(fixture.Derive))
	}
}

func TestCompareLegacySourceOperationReports(t *testing.T) {
	control := map[string]any{"registry": map[string]any{"a": map[string]any{"reason": nil, "status": "mapped", "source": map[string]any{"candidate_count": 1, "files": []any{"a.go"}}, "read": map[string]any{"evidence_kind": "read", "operation_id": "GetA", "path": "/a/{id}"}}}, "summary": map[string]any{"mapped": 1}}
	candidate := map[string]any{"registry": map[string]any{"a": map[string]any{"reason": nil, "status": "mapped", "source": map[string]any{"candidate_count": 1, "files": []any{"a.go"}}, "read": map[string]any{"evidence_kind": "read", "operation_id": "GetA", "path": "/a/{id}"}}, "b": map[string]any{"reason": "resource_file_not_found", "status": "unmapped", "source": map[string]any{}}}, "summary": map[string]any{"mapped": 1, "unmapped": 1}}
	got := CompareLegacySourceOperationReports(control, candidate)
	if summary := legacyObject(got["summary"]); legacyNumber(summary["changed"]) != 1 || legacyNumber(summary["resources"]) != 2 || legacyNumber(summary["unchanged"]) != 1 || legacyNumber(summary["status_changes"]) != 1 {
		t.Errorf("CompareLegacySourceOperationReports() summary = %#v, want one ordered status change", summary)
	}
}

func TestLegacyMapperNodeDifferentialAuthorities(t *testing.T) {
	fixture := loadLegacyFixture(t)
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
		assertLegacyNodeReport(t, fixture, "text_scanner", LegacyOptions{SchemaData: schema("example_folder"), OpenAPI: openFolders, SourceRoot: root, ProviderSource: provider, ResourcePrefix: "example"})
	})
	t.Run("ast_facts", func(t *testing.T) {
		root := materializeLegacy(t, map[string]string{"resource_folder.go": "package provider\n"})
		facts := legacyFacts("resource_folder.go", []any{legacySelector("resource_folder.go", []string{"client", "Provisioning", "GetFolders"}), legacySelector("resource_folder.go", []string{"client", "Provisioning", "GetFolder"})})
		candidate, err := DeriveLegacySourceOperationRegistry(LegacyOptions{SchemaData: schema("example_folder"), OpenAPI: openFolders, SourceRoot: root, ProviderSource: provider, ResourcePrefix: "example", SourceFacts: facts})
		if err != nil {
			t.Fatal(err)
		}
		assertLegacyReport(t, fixture, "ast_facts", candidate)
		control, err := DeriveLegacySourceOperationRegistry(LegacyOptions{SchemaData: schema("example_folder"), OpenAPI: openFolders, SourceRoot: root, ProviderSource: provider, ResourcePrefix: "example"})
		if err != nil {
			t.Fatal(err)
		}
		assertLegacyReport(t, fixture, "ast_facts_comparison", CompareLegacySourceOperationReports(control, candidate))
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
		assertLegacyNodeReport(t, fixture, "source_layout", LegacyOptions{SchemaData: schema("example_registered", "example_packaged", "example_service", "example_framework", "example_raw", "example_graphql"), OpenAPI: map[string]any{"paths": paths}, SourceRoot: root, ProviderSource: provider, ResourcePrefix: "example"})
	})
	widgetSDK := "package sdk\nconst widgetsBasePath = \"v2/widgets\"\ntype WidgetsServiceOp struct { client *Client }\nfunc (s *WidgetsServiceOp) Get(ctx context.Context, id string) error { path := fmt.Sprintf(\"%s/%s\", widgetsBasePath, id); _, err := s.client.NewRequest(ctx, http.MethodGet, path, nil); return err }\nfunc (s *WidgetsServiceOp) Create(ctx context.Context) error { path := widgetsBasePath; _, err := s.client.NewRequest(ctx, http.MethodPost, path, nil); return err }\n"
	widgetOpenAPI := map[string]any{"paths": map[string]any{"/v2/widgets/{widget_id}": map[string]any{"get": map[string]any{"operationId": "GetWidget"}}, "/v2/widgets": map[string]any{"post": map[string]any{"operationId": "CreateWidget"}}}}
	t.Run("sdk_text_and_facts", func(t *testing.T) {
		root := materializeLegacy(t, map[string]string{"resource_widget.go": "package provider\nfunc read() { client.Widgets.Get(ctx, id); client.Widgets.Create(ctx) }\n"})
		sdk := materializeLegacy(t, map[string]string{"widgets.go": widgetSDK})
		base := LegacyOptions{SchemaData: schema("example_widget"), OpenAPI: widgetOpenAPI, SourceRoot: root, SDKRoot: sdk, ProviderSource: provider, ResourcePrefix: "example"}
		assertLegacyNodeReport(t, fixture, "sdk_text", base)
		facts := legacyFacts("resource_widget.go", []any{legacySelector("resource_widget.go", []string{"client", "Widgets", "Get"}), legacySelector("resource_widget.go", []string{"client", "Widgets", "Create"})})
		base.SourceFacts = facts
		assertLegacyNodeReport(t, fixture, "sdk_facts", base)
	})
	t.Run("ambiguity_relationship", func(t *testing.T) {
		root := materializeLegacy(t, map[string]string{"resource_thing.go": "package provider\nvar name = \"example_thing\"\nfunc read() { client.ThingsAPI.GetThing(ctx, id); client.ThingsAPI.RetrieveThing(ctx, uid) }\n", "resource_repository_topics.go": "package provider\nvar name = \"example_repository_topics\"\nfunc read() { client.Repositories.ListAllTopics(ctx, owner, repo, nil) }\n"})
		openapi := map[string]any{"paths": map[string]any{"/things/{id}": map[string]any{"get": map[string]any{"operationId": "GetThing"}}, "/things/{uid}": map[string]any{"get": map[string]any{"operationId": "RetrieveThing"}}, "/repos/{owner}/{repo}/topics": map[string]any{"get": map[string]any{"operationId": "repos/get-all-topics"}}}}
		assertLegacyNodeReport(t, fixture, "ambiguity_relationship", LegacyOptions{SchemaData: schema("example_thing", "example_repository_topics"), OpenAPI: openapi, SourceRoot: root, ProviderSource: provider, ResourcePrefix: "example"})
	})
	t.Run("escaped_and_unresolved_rest", func(t *testing.T) {
		openapi := map[string]any{"paths": map[string]any{"/widgets/{id}": map[string]any{"get": map[string]any{"operationId": "GetWidget"}}}}
		root := materializeLegacy(t, map[string]string{"resource_widget.go": "package provider\nfunc read() { client.NewRequest(\"GET\", \"\\x2fwidgets\\u002f%s\", nil) }\n"})
		base := LegacyOptions{SchemaData: schema("example_widget"), OpenAPI: openapi, SourceRoot: root, ProviderSource: provider, ResourcePrefix: "example"}
		assertLegacyNodeReport(t, fixture, "escaped_rest_text", base)
		base.SourceFacts = legacyFacts("resource_widget.go", []any{legacySelector("resource_widget.go", []string{})})
		base.SourceFacts["selector_calls"] = []any{map[string]any{"file": "resource_widget.go", "parts": []any{}, "symbol": "client.Widgets.Get"}}
		assertLegacyNodeReport(t, fixture, "escaped_rest_facts", base)
		base.SourceFacts = legacyFacts("resource_widget.go", nil)
		base.SourceFacts["raw_rest_calls"] = []any{map[string]any{"file": "resource_widget.go", "method": "GET", "symbol": "client.NewRequest"}}
		assertLegacyNodeReport(t, fixture, "unresolved_rest_facts", base)
	})
}

func loadLegacyFixture(t *testing.T) legacyFixture {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "node-tests", "fixtures", "python-source-operation-map-v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fixture legacyFixture
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	if len(fixture.Node) != 10 {
		t.Fatalf("node differential case count = %d, want 10", len(fixture.Node))
	}
	return fixture
}
func materializeLegacy(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for name, text := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}
func legacyFacts(file string, selectors []any) map[string]any {
	return map[string]any{"source_root": "fixture", "files": []any{map[string]any{"path": file, "package": "provider", "imports": []any{}}}, "functions": []any{}, "resource_registrations": []any{}, "resource_references": []any{}, "identifier_references": []any{}, "read_callbacks": []any{}, "package_calls": []any{}, "raw_rest_calls": []any{}, "selector_calls": selectors}
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
func assertLegacyNodeReport(t *testing.T, fixture legacyFixture, name string, options LegacyOptions) {
	t.Helper()
	got, err := DeriveLegacySourceOperationRegistry(options)
	if err != nil {
		t.Fatal(err)
	}
	assertLegacyReport(t, fixture, name, got)
}
func assertLegacyReport(t *testing.T, fixture legacyFixture, name string, got map[string]any) {
	t.Helper()
	for _, test := range fixture.Node {
		if test.Name == name {
			wantBytes, _ := json.Marshal(test.Report)
			gotBytes, _ := json.Marshal(got)
			var want, actual any
			_ = json.Unmarshal(wantBytes, &want)
			_ = json.Unmarshal(gotBytes, &actual)
			if !reflect.DeepEqual(want, actual) {
				t.Errorf("%s report mismatch\nwant %s\ngot %s", name, wantBytes, gotBytes)
			}
			return
		}
	}
	t.Fatalf("node authority %q not found", name)
}

func writeLegacyFixtureFiles(t *testing.T, root string, files map[string]any) {
	t.Helper()
	for name, value := range files {
		filename := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(filename), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filename, []byte(legacyString(value)), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}
