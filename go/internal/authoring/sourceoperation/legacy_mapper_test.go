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

func TestDeriveLegacySourceOperationRegistryPinsAmbiguityPrecedenceAndReadiness(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"resource_thing.go": `package provider

var resourceName = "example_thing"

func read() {
	client.ThingsAPI.GetThing(ctx, id)
	client.ThingsAPI.RetrieveThing(ctx, uid)
}
`,
		"resource_project.go": "package provider\n",
	}
	for name, content := range files {
		path := filepath.Join(root, name)
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatalf("os.WriteFile(%q) error: %v", path, err)
		}
	}
	providerSource := "registry.terraform.io/example/example"
	schema := map[string]any{"provider_schemas": map[string]any{providerSource: map[string]any{
		"resource_schemas": map[string]any{
			"example_thing":   map[string]any{"block": map[string]any{"attributes": map[string]any{"name": map[string]any{"required": true, "type": "string"}}}},
			"example_project": map[string]any{"block": map[string]any{"attributes": map[string]any{"name": map[string]any{"required": true, "type": "string"}}}},
			"example_missing": map[string]any{"block": map[string]any{"attributes": map[string]any{"name": map[string]any{"required": true, "type": "string"}}}},
		},
	}}}
	openAPI := map[string]any{"paths": map[string]any{
		"/things/{id}":   map[string]any{"get": map[string]any{"operationId": "GetThing"}},
		"/things/{uid}":  map[string]any{"get": map[string]any{"operationId": "RetrieveThing"}},
		"/projects/{id}": map[string]any{"get": map[string]any{"operationId": "ProjectsRetrieve"}},
	}}
	report, err := DeriveLegacySourceOperationRegistry(LegacyOptions{
		SchemaData: schema, OpenAPI: openAPI, SourceRoot: root,
		ProviderSource: providerSource, ResourcePrefix: "example",
	})
	if err != nil {
		t.Fatalf("DeriveLegacySourceOperationRegistry() error: %v", err)
	}
	summary := legacyObject(report["summary"])
	wantSummary := map[string]int{
		"resources": 3, "mapped": 0, "ambiguous": 1, "unmapped": 2, "graphql_source": 0,
		"resources_with_source_files": 2, "resources_without_source_files": 1,
	}
	for name, want := range wantSummary {
		if got := legacyNumber(summary[name]); got != want {
			t.Errorf("DeriveLegacySourceOperationRegistry() summary[%q] = %d, want %d (summary: %#v)", name, got, want, summary)
		}
	}

	registry := legacyObject(report["registry"])
	thing := legacyObject(registry["example_thing"])
	if got := legacyString(thing["status"]); got != "ambiguous_source_operation" {
		t.Fatalf("example_thing status = %q, want ambiguous_source_operation", got)
	}
	if got := legacyString(thing["reason"]); got != "ambiguous_source_operation" {
		t.Errorf("example_thing reason = %q, want ambiguous_source_operation", got)
	}
	candidates := legacyArray(thing["candidates"])
	if len(candidates) != 2 {
		t.Fatalf("example_thing candidates = %d, want 2: %#v", len(candidates), candidates)
	}
	for index, want := range []struct {
		operationID string
		path        string
		readScore   int
		listScore   int
	}{
		{operationID: "RetrieveThing", path: "/things/{uid}", readScore: 332, listScore: 277},
		{operationID: "GetThing", path: "/things/{id}", readScore: 320, listScore: 265},
	} {
		candidate := legacyObject(candidates[index])
		if gotOperation, gotPath := legacyString(candidate["operation_id"]), legacyString(candidate["path"]); gotOperation != want.operationID || gotPath != want.path {
			t.Errorf("example_thing candidate[%d] operation/path = %q/%q, want %q/%q", index, gotOperation, gotPath, want.operationID, want.path)
		}
		if gotRead, gotList := legacyNumber(candidate["read_score"]), legacyNumber(candidate["list_score"]); gotRead != want.readScore || gotList != want.listScore {
			t.Errorf("example_thing candidate[%d] read/list scores = %d/%d, want %d/%d", index, gotRead, gotList, want.readScore, want.listScore)
		}
	}

	project := legacyObject(registry["example_project"])
	if gotStatus, gotReason := legacyString(project["status"]), legacyString(project["reason"]); gotStatus != "unmapped" || gotReason != "no_source_operation_match" {
		t.Errorf("example_project status/reason = %q/%q, want unmapped/no_source_operation_match", gotStatus, gotReason)
	}
	projectSource := legacyObject(project["source"])
	if got := projectSource["files"]; !reflect.DeepEqual(got, []string{"resource_project.go"}) {
		t.Errorf("example_project source files = %#v, want [resource_project.go]", got)
	}

	missing := legacyObject(registry["example_missing"])
	if gotStatus, gotReason := legacyString(missing["status"]), legacyString(missing["reason"]); gotStatus != "unmapped" || gotReason != "resource_file_not_found" {
		t.Errorf("example_missing status/reason = %q/%q, want unmapped/resource_file_not_found", gotStatus, gotReason)
	}
	if got := legacyObject(missing["source"])["files"]; !reflect.DeepEqual(got, []string{}) {
		t.Errorf("example_missing source files = %#v, want empty", got)
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
