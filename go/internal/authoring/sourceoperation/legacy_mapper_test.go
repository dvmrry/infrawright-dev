package sourceoperation

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestLegacyMapperHelpers(t *testing.T) {
	if got := SDKMethodRole("GetIPAddresses"); got != "read" {
		t.Errorf("SDKMethodRole(GetIPAddresses) = %q, want read", got)
	}
	if got := SDKMethodRole("ListIPAddresses"); got != "list" {
		t.Errorf("SDKMethodRole(ListIPAddresses) = %q, want list", got)
	}
	if got := legacySortedSet(GoIdentifierTokens("r client IAM UserGroups Members List rclientiamusergroupsmemberslist")); !reflect.DeepEqual(got, []string{"client", "iam", "list", "members", "r", "rclientiamusergroupsmemberslist", "usergroups"}) {
		t.Errorf("GoIdentifierTokens() = %#v, want stable identifier tokens", got)
	}
	for _, test := range []struct {
		name string
		path string
		want string
	}{
		{name: "playlist", path: "/playlists/{id}", want: "detail"},
		{name: "product_list", path: "/products/list/{id}", want: "detail"},
		{name: "product_search", path: "/products/search/{id}", want: "detail"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := legacyPathKind(legacyOperation{Path: test.path}); got != test.want {
				t.Errorf("legacyPathKind(%q) = %q, want %q", test.path, got, test.want)
			}
		})
	}
	if got := legacyMethodTokens(map[string]any{"method": "ListIPAddresses"}); !reflect.DeepEqual(got, []string{"addresses", "ips"}) {
		t.Errorf("legacyMethodTokens(ListIPAddresses) = %#v, want [addresses ips]", got)
	}
	if got := OperationAliases("RouteGetWidget"); !reflect.DeepEqual(got, []string{"getwidget", "getwidgetwithresponse", "retrievewidretrieve", "retrievewidretrievewithresponse", "routegetwidget", "routegetwidgetwithresponse", "routeretrievewidretrieve", "routeretrievewidretrievewithresponse"}) {
		t.Errorf("OperationAliases(RouteGetWidget) = %#v, want stable alias expansion", got)
	}
	spec := map[string]any{"paths": map[string]any{"/z": map[string]any{"get": map[string]any{}}, "/a": map[string]any{"post": map[string]any{"operationId": "CreateA"}}}}
	inventory := OpenAPIOperationInventory(spec)
	if got := []string{legacyString(inventory[0]["path"]), legacyString(inventory[0]["operation_id"]), legacyString(inventory[1]["path"]), legacyString(inventory[1]["operation_id"])}; !reflect.DeepEqual(got, []string{"/a", "CreateA", "/z", "GET /z"}) {
		t.Errorf("OpenAPIOperationInventory() = %#v, want sorted explicit/synthetic inventory", got)
	}
	if got := NormalizeRawRESTPath(" orgs/%s//actions/%08d "); got != "/orgs/{arg}/actions/{arg}" {
		t.Errorf("NormalizeRawRESTPath() = %q, want /orgs/{arg}/actions/{arg}", got)
	}
}

func TestDeriveLegacySourceOperationRegistryFindsReadAndListOperations(t *testing.T) {
	root := t.TempDir()
	filename := filepath.Join(root, "internal", "resource_folder.go")
	if err := os.MkdirAll(filepath.Dir(filename), 0o755); err != nil {
		t.Fatalf("os.MkdirAll(%q) error = %v, want nil", filepath.Dir(filename), err)
	}
	source := "package provider\nfunc read() { client.Provisioning.GetFolder(ctx, id); client.Provisioning.GetFolders(ctx) }\n"
	if err := os.WriteFile(filename, []byte(source), 0o600); err != nil {
		t.Fatalf("os.WriteFile(%q) error = %v, want nil", filename, err)
	}
	schema := map[string]any{"provider_schemas": map[string]any{"example/provider": map[string]any{"resource_schemas": map[string]any{"example_folder": map[string]any{"block": map[string]any{"attributes": map[string]any{}}}}}}}
	openAPI := map[string]any{"paths": map[string]any{
		"/api/folders/{uid}": map[string]any{"get": map[string]any{"operationId": "RouteGetFolder"}},
		"/api/folders":       map[string]any{"get": map[string]any{"operationId": "RouteGetFolders"}},
	}}
	report, err := DeriveLegacySourceOperationRegistry(LegacyOptions{
		SchemaData: schema, OpenAPI: openAPI, SourceRoot: root,
		ProviderSource: "example/provider", ResourcePrefix: "example",
	})
	if err != nil {
		t.Fatalf("DeriveLegacySourceOperationRegistry() error = %v, want nil", err)
	}
	entry := legacyObject(legacyObject(report["registry"])["example_folder"])
	if got := legacyString(entry["status"]); got != "mapped" {
		t.Fatalf("DeriveLegacySourceOperationRegistry() status = %q, want mapped", got)
	}
	if got := legacyString(legacyObject(entry["read"])["path"]); got != "/api/folders/{uid}" {
		t.Errorf("DeriveLegacySourceOperationRegistry() read path = %q, want /api/folders/{uid}", got)
	}
	if got := legacyString(legacyObject(entry["list"])["path"]); got != "/api/folders" {
		t.Errorf("DeriveLegacySourceOperationRegistry() list path = %q, want /api/folders", got)
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
