package providerprobe

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const providerProbeAuthoritySHA256 = "5337acada00b380e79468af316c9caa287a4e1f044b850567a97e58c79e49bf2"

func TestLegacyFixtureParity(t *testing.T) {
	authorityPath := filepath.Join("..", "..", "..", "..", "node-tests", "fixtures", "python-provider-probe-v1.json")
	authority, err := os.ReadFile(authorityPath)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(authority)
	if got := hex.EncodeToString(sum[:]); got != providerProbeAuthoritySHA256 {
		t.Fatalf("authority SHA = %s", got)
	}
	root := t.TempDir()
	writeLegacyFixture(t, root)
	recipe, err := loadRecipe(filepath.Join(root, "recipe.json"))
	if err != nil {
		t.Fatal(err)
	}
	result, err := runLegacy(context.Background(), recipe, RunOptions{WorkDirectory: filepath.Join(root, "differential-work")})
	if err != nil {
		t.Fatal(err)
	}
	expected := frozenArtifacts(t, authority, "local-provider-probe")
	artifacts := result.Artifacts()
	if len(artifacts) != 5 {
		t.Fatalf("artifact count = %d", len(artifacts))
	}
	wantNames := []string{"source-registry.json", "source-diagnostics.json", "openapi-map.json", "summary.json", "summary.md"}
	for i, artifact := range artifacts {
		if artifact.Name != wantNames[i] {
			t.Fatalf("artifact %d = %s, want %s", i, artifact.Name, wantNames[i])
		}
		want := strings.ReplaceAll(expected[artifact.Name], "<fixture-root>", root)
		if string(artifact.Bytes) != want {
			t.Fatalf("%s differs from frozen authority\nwant: %s\ngot: %s", artifact.Name, want, artifact.Bytes)
		}
	}
	markdownCopy, copyErr := result.MarkdownCopy()
	if copyErr != nil {
		t.Fatalf("Result.MarkdownCopy: %v", copyErr)
	}
	if strings.Contains(string(markdownCopy), "## Artifacts\n") {
		t.Fatalf("legacy Markdown copy contains published artifact appendix: %q", markdownCopy)
	}
	publishedMarkdown := artifacts[len(artifacts)-1].Bytes
	if !strings.Contains(string(publishedMarkdown), "## Artifacts\n") {
		t.Fatalf("published Markdown lacks artifact appendix: %q", publishedMarkdown)
	}
	if len(markdownCopy) == 0 {
		t.Fatal("legacy Markdown copy is empty")
	}
	markdownCopy[0] ^= 1
	freshCopy, freshErr := result.MarkdownCopy()
	if freshErr != nil {
		t.Fatalf("Result.MarkdownCopy second call: %v", freshErr)
	}
	if freshCopy[0] == markdownCopy[0] {
		t.Fatal("Result.MarkdownCopy returned caller-mutable bytes")
	}
	if _, err := os.Stat(filepath.Join(result.WorkDirectory(), "artifacts")); !os.IsNotExist(err) {
		t.Fatalf("legacy runner created public artifact directory: %v", err)
	}
}

func TestResultMarkdownCopyRejectsIncompleteResult(t *testing.T) {
	for _, result := range []Result{{}, {mode: LegacyV1}, {mode: QualifiedV2}} {
		if _, err := result.MarkdownCopy(); err == nil {
			t.Fatalf("Result(%q).MarkdownCopy error = nil, want incomplete-result rejection", result.mode)
		}
	}
}

func TestLegacyFalseyPrimariesFixtureParity(t *testing.T) {
	authority, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "node-tests", "fixtures", "python-provider-probe-v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	writeLegacyFixture(t, root)
	schema := `{"provider_schemas":{"registry.terraform.io/example/multi-part-provider":{"resource_schemas":{"example_folder":{"block":{"attributes":{"name":{"required":true,"type":"string"}}}}}}}}`
	if err := os.WriteFile(filepath.Join(root, "target-schema.json"), []byte(schema), 0600); err != nil {
		t.Fatal(err)
	}
	provider := filepath.Join(root, "provider", "internal", "resource_folder.go")
	source, err := os.ReadFile(provider)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "source-repository", "internal"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "source-repository", "internal", "resource_folder.go"), source, 0600); err != nil {
		t.Fatal(err)
	}
	openAPIURL := (&url.URL{Scheme: "file", Path: filepath.Join(root, "openapi.json")}).String()
	recipeBody := `{"name":"","openapi":{"format":"","path":"","url":` + quoted(openAPIURL) + `},"provider_source":"registry.terraform.io/example/multi-part-provider","provider_version":"1.2.3","resource_prefix":"example","source":{"git":` + quoted(filepath.Join(root, "source-repository")) + `,"path":"","ref":"v1.2.3","subdir":""},"terraform_provider":{"local_name":"","source":"","version":""},"terraform_schema":{"path":""},"tools":{"terraform":"fake-terraform"}}`
	if err := os.WriteFile(filepath.Join(root, "falsey-recipe.json"), []byte(recipeBody), 0600); err != nil {
		t.Fatal(err)
	}
	host := fixtureLegacyHost{schema: []byte(schema)}
	recipe, err := loadRecipe(filepath.Join(root, "falsey-recipe.json"))
	if err != nil {
		t.Fatal(err)
	}
	result, err := runLegacy(context.Background(), recipe, RunOptions{WorkDirectory: filepath.Join(root, "falsey-differential-work"), LegacyHost: &host})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(host.request.MainHCL), "terraform {\n  required_providers {\n    multi_part_provider = {\n      source = \"example/multi-part-provider\"\n      version = \"1.2.3\"\n    }\n  }\n}\n"; got != want {
		t.Fatalf("HCL differs\nwant %q\ngot  %q", want, got)
	}
	expected := frozenArtifacts(t, authority, "empty-recipe-primaries")
	for _, artifact := range result.Artifacts() {
		if want := strings.ReplaceAll(expected[artifact.Name], "<fixture-root>", root); string(artifact.Bytes) != want {
			t.Fatalf("%s differs from frozen authority", artifact.Name)
		}
	}
	if host.downloadCalls != 1 || host.cloneCalls != 1 || host.captureCalls != 1 {
		t.Fatalf("falsey fallback host calls = download:%d clone:%d capture:%d", host.downloadCalls, host.cloneCalls, host.captureCalls)
	}
}

func TestLegacyRecipeValidation(t *testing.T) {
	valid := func() map[string]any {
		return map[string]any{"name": "example", "provider_source": "registry.test/example", "provider_version": "1.2.3", "resource_prefix": "example", "api_prefix": "/api/", "openapi": map[string]any{"path": "openapi.json"}, "source": map[string]any{"path": "provider"}}
	}
	cases := []struct {
		name  string
		value any
		want  string
	}{
		{name: "nonobject root", value: []any{}, want: "recipe root must be an object"},
		{name: "nonobject openapi", value: func() any { v := valid(); v["openapi"] = []any{}; return v }(), want: "recipe openapi must be an object"},
		{name: "nonobject source", value: func() any { v := valid(); v["source"] = false; return v }(), want: "recipe source must be an object"},
		{name: "nonobject schema", value: func() any { v := valid(); v["terraform_schema"] = 1; return v }(), want: "recipe terraform_schema must be an object"},
		{name: "nonobject terraform provider", value: func() any { v := valid(); v["terraform_provider"] = true; return v }(), want: "recipe terraform_provider must be an object"},
		{name: "nonobject tools", value: func() any { v := valid(); v["tools"] = []any{}; return v }(), want: "recipe tools must be an object"},
		{name: "missing openapi", value: func() any { v := valid(); delete(v, "openapi"); return v }(), want: "recipe openapi must include path or url"},
		{name: "null openapi", value: func() any { v := valid(); v["openapi"] = nil; return v }(), want: "recipe openapi must include path or url"},
		{name: "missing source", value: func() any { v := valid(); delete(v, "source"); return v }(), want: "recipe source must include path or git"},
		{name: "null source", value: func() any { v := valid(); v["source"] = nil; return v }(), want: "recipe source must include path or git"},
		{name: "git missing ref", value: func() any { v := valid(); v["source"] = map[string]any{"git": "repo"}; return v }(), want: "recipe source.ref is required"},
		{name: "schema capture missing provider", value: func() any { v := valid(); delete(v, "provider_source"); return v }(), want: "recipe provider_source is required"},
		{name: "schema capture missing version", value: func() any { v := valid(); delete(v, "provider_version"); return v }(), want: "recipe provider_version or terraform_provider.version is required"},
	}
	for _, field := range []string{"name", "provider_source", "provider_version", "resource_prefix", "api_prefix"} {
		field := field
		cases = append(cases, struct {
			name  string
			value any
			want  string
		}{name: "root scalar " + field, value: func() any { v := valid(); v[field] = []any{}; return v }(), want: "recipe " + field + " must be a string"})
	}
	for _, field := range []struct{ section, field string }{{"openapi", "path"}, {"openapi", "url"}, {"openapi", "format"}, {"source", "path"}, {"source", "git"}, {"source", "ref"}, {"source", "subdir"}, {"terraform_schema", "path"}, {"terraform_provider", "source"}, {"terraform_provider", "version"}, {"terraform_provider", "local_name"}, {"tools", "terraform"}} {
		field := field
		cases = append(cases, struct {
			name  string
			value any
			want  string
		}{name: "section scalar " + field.section + "." + field.field, value: func() any {
			v := valid()
			section, ok := v[field.section].(map[string]any)
			if !ok {
				section = map[string]any{}
				v[field.section] = section
			}
			section[field.field] = []any{}
			return v
		}(), want: "recipe " + field.section + "." + field.field + " must be a string"})
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if err := loadLegacyRecipeValue(t, test.value); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
	for _, test := range []struct {
		name   string
		mutate func(map[string]any)
	}{
		{name: "null optional sections", mutate: func(v map[string]any) { v["terraform_schema"] = nil; v["terraform_provider"] = nil; v["tools"] = nil }},
		{name: "missing optional sections", mutate: func(v map[string]any) {
			delete(v, "terraform_schema")
			delete(v, "terraform_provider")
			delete(v, "tools")
		}},
		{name: "unknown keys", mutate: func(v map[string]any) {
			v["unknown"] = map[string]any{"nested": true}
			v["openapi"].(map[string]any)["extra"] = 42
			v["source"].(map[string]any)["extra"] = false
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			v := valid()
			test.mutate(v)
			if err := loadLegacyRecipeValue(t, v); err != nil {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestLegacyLocalInputPrecedenceAvoidsHost(t *testing.T) {
	root := t.TempDir()
	writeLegacyFixture(t, root)
	recipeBody := map[string]any{"name": "precedence", "provider_source": "registry.terraform.io/example/example", "provider_version": "1.2.3", "resource_prefix": "example", "openapi": map[string]any{"path": "openapi.json", "url": "https://example.invalid/openapi.json"}, "source": map[string]any{"path": "provider", "git": "https://example.invalid/provider.git", "ref": "v1"}, "terraform_schema": map[string]any{"path": "schema.json"}}
	recipePath := filepath.Join(root, "precedence.json")
	writeJSONTestFile(t, recipePath, recipeBody)
	recipe, err := loadRecipe(recipePath)
	if err != nil {
		t.Fatal(err)
	}
	host := &cloneCountingHost{}
	if _, err := runLegacy(context.Background(), recipe, RunOptions{WorkDirectory: filepath.Join(root, "work"), LegacyHost: host}); err != nil {
		t.Fatal(err)
	}
	if host.cloneCalls != 0 || host.downloadCalls != 0 || host.captureCalls != 0 {
		t.Fatalf("host calls = download:%d clone:%d capture:%d", host.downloadCalls, host.cloneCalls, host.captureCalls)
	}
}

func TestLegacyOperationProfileEdges(t *testing.T) {
	for _, paths := range []any{nil, false, "", 0, []any{}, map[string]any{}} {
		profile, err := openAPIOperationProfile(map[string]any{"paths": paths})
		if err != nil || profile["operations"] != 0 || profile["operation_id_coverage_ratio"] != nil {
			t.Fatalf("falsey paths %#v => %#v, %v", paths, profile, err)
		}
	}
	for _, item := range []any{nil, false, "", 0, []any{}, map[string]any{}} {
		profile, err := openAPIOperationProfile(map[string]any{"paths": map[string]any{"/items": item}})
		if err != nil || profile["operations"] != 0 {
			t.Fatalf("falsey item %#v => %#v, %v", item, profile, err)
		}
	}
	for _, input := range []map[string]any{{"paths": []any{"unexpected"}}, {"paths": map[string]any{"/items": []any{"unexpected"}}}} {
		if _, err := openAPIOperationProfile(input); err == nil {
			t.Fatalf("truthy nonobject accepted: %#v", input)
		}
	}
	profile, err := openAPIOperationProfile(map[string]any{"paths": map[string]any{"/items": map[string]any{"parameters": []any{}, "summary": "metadata", "get": "not-an-operation", "post": nil}}})
	if err != nil || profile["operations"] != 0 {
		t.Fatalf("metadata/nonobject operations => %#v, %v", profile, err)
	}
	paths := map[string]any{}
	for i := 0; i < 160; i++ {
		operation := map[string]any{}
		if i == 0 {
			operation["operationId"] = "documented"
		}
		paths[fmt.Sprintf("/items/%d", i)] = map[string]any{"get": operation}
	}
	profile, err = openAPIOperationProfile(map[string]any{"paths": paths})
	if err != nil || profile["operation_id_coverage_ratio"] != 0.0063 {
		t.Fatalf("half-even profile => %#v, %v", profile, err)
	}
}

func TestLegacyOpenAPIFormatAndSafeParsing(t *testing.T) {
	if !isYAML(recipeOpenAPI{}, stringPointer("openapi.yaml"), nil) || isYAML(recipeOpenAPI{}, stringPointer("openapi.txt"), nil) || !isYAML(recipeOpenAPI{format: stringPointer("YML")}, stringPointer("openapi.json"), nil) {
		t.Fatal("format selection mismatch")
	}
	if _, err := decodeLegacyOpenAPI([]byte("openapi: 3.0.3\npaths: {}\n"), true); err != nil {
		t.Fatalf("safe yaml rejected: %v", err)
	}
	if _, err := decodeLegacyOpenAPI([]byte("operation: &operation\n  operationId: getItem\nopenapi: 3.0.3\npaths:\n  /items:\n    get: *operation\n"), true); err != nil {
		t.Fatalf("safe yaml alias rejected: %v", err)
	}
	for _, test := range []struct {
		name string
		data []byte
		yaml bool
	}{{"unsafe yaml", []byte("--- !ruby/object:Object {}\n"), true}, {"duplicate yaml", []byte("openapi: 3.0.3\nopenapi: 3.0.3\npaths: {}\n"), true}, {"duplicate json", []byte(`{"openapi":"3.0.3","openapi":"3.0.3","paths":{}}`), false}} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := decodeLegacyOpenAPI(test.data, test.yaml); err == nil {
				t.Fatal("malicious or duplicate input accepted")
			}
		})
	}
}

func TestLegacyRandomWorkIsPrivateAndUnpublished(t *testing.T) {
	work, err := legacyWorkDirectory(loadedRecipe{name: stringPointer("test")}, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(work) })
	info, err := os.Lstat(work)
	if err != nil || !privateDirectory(info) {
		t.Fatalf("random work private/info = %v/%v", privateDirectory(info), err)
	}
	if valid, err := legacyMarker(work, legacyWorkMarker, legacyWorkMarkerText); err != nil || !valid {
		t.Fatalf("work marker valid/error = %t/%v", valid, err)
	}
	if _, err := os.Stat(filepath.Join(work, "artifacts")); !os.IsNotExist(err) {
		t.Fatalf("artifacts directory exists: %v", err)
	}
}

func loadLegacyRecipeValue(t *testing.T, value any) error {
	t.Helper()
	path := filepath.Join(t.TempDir(), "recipe.json")
	writeJSONTestFile(t, path, value)
	_, err := loadRecipe(path)
	return err
}
func writeJSONTestFile(t *testing.T, path string, value any) {
	t.Helper()
	bytes, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, bytes, 0600); err != nil {
		t.Fatal(err)
	}
}
func stringPointer(value string) *string { return &value }

func TestLegacyCloneRefusesForeignDestinationBeforeHost(t *testing.T) {
	work := claimedLegacyWork(t)
	source := filepath.Join(work, "source")
	if err := os.Mkdir(source, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "foreign.txt"), []byte("do not remove"), 0600); err != nil {
		t.Fatal(err)
	}
	host := &cloneCountingHost{}
	_, err := prepareLegacySource(context.Background(), legacyGitRecipe(), work, RunOptions{LegacyHost: host})
	if err == nil || !strings.Contains(err.Error(), "without probe marker") {
		t.Fatalf("error = %v", err)
	}
	if host.cloneCalls != 0 {
		t.Fatalf("Clone calls = %d, want 0", host.cloneCalls)
	}
	if _, err := os.Stat(filepath.Join(source, "foreign.txt")); err != nil {
		t.Fatalf("foreign source was altered: %v", err)
	}
}

func TestLegacyCloneRefusesNonDirectoryAndSymlinkDestinationBeforeHost(t *testing.T) {
	for _, test := range []struct {
		name       string
		makeTarget func(t *testing.T, path string)
	}{
		{name: "file", makeTarget: func(t *testing.T, path string) {
			if err := os.WriteFile(path, []byte("foreign"), 0600); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "symlink", makeTarget: func(t *testing.T, path string) {
			target := filepath.Join(filepath.Dir(path), "outside")
			if err := os.Mkdir(target, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, path); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			work := claimedLegacyWork(t)
			test.makeTarget(t, filepath.Join(work, "source"))
			host := &cloneCountingHost{}
			_, err := prepareLegacySource(context.Background(), legacyGitRecipe(), work, RunOptions{LegacyHost: host})
			if err == nil || !strings.Contains(err.Error(), "not a directory") {
				t.Fatalf("error = %v", err)
			}
			if host.cloneCalls != 0 {
				t.Fatalf("Clone calls = %d, want 0", host.cloneCalls)
			}
		})
	}
}

func TestLegacyCloneReplacesOnlyOwnedDestination(t *testing.T) {
	work := claimedLegacyWork(t)
	source := filepath.Join(work, "source")
	if err := os.Mkdir(source, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "old.txt"), []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := writeLegacyMarker(source, legacySourceMarker, legacySourceMarkerText); err != nil {
		t.Fatal(err)
	}
	host := &cloneCountingHost{}
	root, err := prepareLegacySource(context.Background(), legacyGitRecipe(), work, RunOptions{LegacyHost: host})
	if err != nil {
		t.Fatal(err)
	}
	if root != source || host.cloneCalls != 1 {
		t.Fatalf("root/calls = %q/%d", root, host.cloneCalls)
	}
	if _, err := os.Stat(filepath.Join(source, "old.txt")); !os.IsNotExist(err) {
		t.Fatalf("old checkout remains: %v", err)
	}
	if valid, err := legacyMarker(source, legacySourceMarker, legacySourceMarkerText); err != nil || !valid {
		t.Fatalf("new source marker valid/error = %t/%v", valid, err)
	}
}

func TestLegacyCloneWorkPathRebindCannotDeleteOutsideSentinel(t *testing.T) {
	base := t.TempDir()
	work := filepath.Join(base, "work")
	if _, err := legacyWorkDirectory(loadedRecipe{}, work); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(work, "source")
	if err := os.Mkdir(source, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "old.txt"), []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := writeLegacyMarker(source, legacySourceMarker, legacySourceMarkerText); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(base, "outside")
	if err := os.Mkdir(outside, 0700); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(outside, "sentinel.txt")
	if err := os.WriteFile(sentinel, []byte("outside"), 0600); err != nil {
		t.Fatal(err)
	}
	legacyAfterWorkRootBound = func() {
		if err := os.Rename(work, filepath.Join(base, "work-original")); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, work); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { legacyAfterWorkRootBound = nil })
	host := &cloneCountingHost{}
	_, err := prepareLegacySource(context.Background(), legacyGitRecipe(), work, RunOptions{LegacyHost: host})
	if err == nil || !strings.Contains(err.Error(), "changed before clone") {
		t.Fatalf("error = %v", err)
	}
	if host.cloneCalls != 0 {
		t.Fatalf("Clone calls = %d, want 0", host.cloneCalls)
	}
	if bytes, err := os.ReadFile(sentinel); err != nil || string(bytes) != "outside" {
		t.Fatalf("outside sentinel changed: %q, %v", bytes, err)
	}
	if _, err := os.Stat(filepath.Join(base, "work-original", "source")); !os.IsNotExist(err) {
		t.Fatalf("bound old source was not removed: %v", err)
	}
}

func TestLegacyWorkDirectoryClaimsOnlyPrivateFreshOrOwnedDirectories(t *testing.T) {
	base := t.TempDir()
	fresh := filepath.Join(base, "fresh")
	if err := os.Mkdir(fresh, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := legacyWorkDirectory(loadedRecipe{}, fresh); err != nil {
		t.Fatal(err)
	}
	if valid, err := legacyMarker(fresh, legacyWorkMarker, legacyWorkMarkerText); err != nil || !valid {
		t.Fatalf("fresh marker valid/error = %t/%v", valid, err)
	}
	if _, err := legacyWorkDirectory(loadedRecipe{}, fresh); err != nil {
		t.Fatalf("owned existing work rejected: %v", err)
	}
	foreign := filepath.Join(base, "foreign")
	if err := os.Mkdir(foreign, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(foreign, "foreign.txt"), []byte("foreign"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := legacyWorkDirectory(loadedRecipe{}, foreign); err == nil {
		t.Fatal("foreign work directory accepted")
	}
}

func claimedLegacyWork(t *testing.T) string {
	t.Helper()
	work := filepath.Join(t.TempDir(), "work")
	if _, err := legacyWorkDirectory(loadedRecipe{}, work); err != nil {
		t.Fatal(err)
	}
	return work
}

func legacyGitRecipe() loadedRecipe {
	repository, revision := "fixture-repository", "fixture-revision"
	return loadedRecipe{source: recipeSource{git: &repository, ref: &revision}}
}

type cloneCountingHost struct{ downloadCalls, cloneCalls, captureCalls int }

func (h *cloneCountingHost) Download(context.Context, DownloadRequest) error {
	h.downloadCalls++
	return nil
}
func (h *cloneCountingHost) CaptureTerraformSchema(context.Context, TerraformSchemaRequest) ([]byte, error) {
	h.captureCalls++
	return nil, nil
}
func (h *cloneCountingHost) Clone(_ context.Context, request CloneRequest) error {
	h.cloneCalls++
	if err := os.MkdirAll(request.Destination, 0755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(request.Destination, "new.txt"), []byte("new"), 0600)
}

type fixtureLegacyHost struct {
	schema                                  []byte
	request                                 TerraformSchemaRequest
	downloadCalls, cloneCalls, captureCalls int
}

func (h *fixtureLegacyHost) Download(_ context.Context, request DownloadRequest) error {
	h.downloadCalls++
	parsed, err := url.Parse(request.URL)
	if err != nil {
		return err
	}
	bytes, err := os.ReadFile(parsed.Path)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(request.Destination), 0700); err != nil {
		return err
	}
	return os.WriteFile(request.Destination, bytes, 0600)
}
func (h *fixtureLegacyHost) Clone(_ context.Context, request CloneRequest) error {
	h.cloneCalls++
	bytes, err := os.ReadFile(filepath.Join(request.Repository, "internal", "resource_folder.go"))
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(request.Destination, "internal"), 0700); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(request.Destination, "internal", "resource_folder.go"), bytes, 0600)
}
func (h *fixtureLegacyHost) CaptureTerraformSchema(_ context.Context, request TerraformSchemaRequest) ([]byte, error) {
	h.captureCalls++
	h.request = request
	return append([]byte(nil), h.schema...), nil
}
func quoted(value string) string { return `"` + strings.ReplaceAll(value, `\`, `\\`) + `"` }

func frozenArtifacts(t *testing.T, authority []byte, name string) map[string]string {
	t.Helper()
	value, err := decodeLegacyJSON(authority)
	if err != nil {
		t.Fatal(err)
	}
	cases, _ := value["cases"].([]any)
	for _, item := range cases {
		entry, _ := item.(map[string]any)
		if entry["name"] != name {
			continue
		}
		raw, _ := entry["artifacts"].(map[string]any)
		result := map[string]string{}
		for key, value := range raw {
			result[key], _ = value.(string)
		}
		return result
	}
	t.Fatalf("frozen case %q not found", name)
	return nil
}

func writeLegacyFixture(t *testing.T, root string) {
	t.Helper()
	write := func(name, body string) {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write("schema.json", `{"provider_schemas":{"registry.terraform.io/example/example":{"resource_schemas":{"example_folder":{"block":{"attributes":{"name":{"required":true,"type":"string"}}}},"example_graphql":{"block":{"attributes":{}}},"example_missing":{"block":{"attributes":{}}}}}}}`)
	write("openapi.json", `{"info":{"title":"provider probe fixture","version":"1"},"openapi":"3.0.3","paths":{"/api/folders":{"get":{"operationId":"RouteGetFolders","responses":{"200":{"description":"ok"}}},"post":{"responses":{"200":{"description":"ok"}}}},"/api/folders/{uid}":{"get":{"operationId":"RouteGetFolder","responses":{"200":{"description":"ok"}}},"patch":{"responses":{"200":{"description":"ok"}}}},"/api/multi-file":{"$ref":"resources/multi-file.yml#/paths/~1api~1multi-file"}}}`)
	write("provider/internal/resource_folder.go", "package internal\n\nfunc resourceFolder() {\n    resourceName := \"example_folder\"\n    _ = resourceName\n    client.Provisioning.GetFolders(ctx)\n    client.Provisioning.GetFolder(\"abc\")\n}\n")
	write("provider/internal/resource_graphql.go", "package internal\n\nimport \"github.com/shurcooL/githubv4\"\n\nfunc resourceGraphql() {\n    resourceName := \"example_graphql\"\n    _ = resourceName\n    githubv4.NewRequest()\n}\n")
	write("recipe.json", `{"api_prefix":"/api/","name":"example","openapi":{"format":"json","path":"openapi.json"},"provider_source":"registry.terraform.io/example/example","provider_version":"1.2.3","resource_prefix":"example","source":{"path":"provider"},"terraform_schema":{"path":"schema.json"}}`)
}
