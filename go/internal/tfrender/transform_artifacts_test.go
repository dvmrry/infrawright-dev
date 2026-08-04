package tfrender

// These tests exercise artifact compilation and publication with explicit
// current inputs and expected outputs.
import (
	"encoding/json"
	"errors"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dvmrry/infrawright-dev/go/internal/canonjson"
	"github.com/dvmrry/infrawright-dev/go/internal/deployment"
)

// testDeployment returns a minimal deployment for artifact tests.
func testDeployment(overlay string, hcl bool) deployment.Deployment {
	d := deployment.Deployment{Overlay: overlay, Roots: map[string]deployment.RootProviderConfig{}}
	if hcl {
		d.HasTfvarsFormat = true
		d.TfvarsFormat = "hcl"
	}
	return d
}

// newArtifactOptions ports this test file's `artifactCompileOptions`
// helper, with resourceType and workspace as required parameters (Go has
// no optional-field-merging equivalent to the TS helper's partial options
// bag); callers mutate the returned struct's fields directly for anything
// else the TS helper made optional.
func newArtifactOptions(workspace, resourceType string) TransformArtifactCompileOptions {
	nameField := "name"
	return TransformArtifactCompileOptions{
		BindingContext: BindingContext{
			Mode:          deployment.ReferenceBindingDisabled,
			Derived:       map[string]bool{},
			Generated:     map[string]bool{resourceType: true},
			ResourceRoots: map[string]string{resourceType: resourceType},
			References:    map[string]TransformReferenceSpec{},
		},
		Deployment:      testDeployment(workspace, false),
		LookupNameField: &nameField,
		Override:        map[string]any{},
		References:      map[string]TransformReferenceSpec{},
		ResourceType:    resourceType,
		Result: PullTransformResult{
			Drops:     []string{},
			Items:     map[string]map[string]any{"example": {"name": "Example"}},
			Originals: map[string]map[string]any{"example": {"id": "id-1", "name": "Example"}},
		},
		Tenant:       "tenant",
		VariableName: "items",
	}
}

// staleBookPlaceholder is the content the stale-lookup fixtures below commit at
// a location publish must clean. It is a VALID lookup carrying one key the fresh
// compile does not: every committed lookup is now read at compile time by the
// key-shrinkage guard, so an unparseable placeholder would fail the compile
// for reasons the test is not about, and a lookup with no dropped key would skip
// the guard's dependent scan entirely rather than exercise it.
func staleBookPlaceholder() string {
	return `{"by_id":{"stale-id":"Stale"},"id_by_key":{"stale":"stale-id"},"key_by_id":{"stale-id":"stale"}}`
}

func defaultArtifactOptions(workspace string) TransformArtifactCompileOptions {
	return newArtifactOptions(workspace, "sample_resource")
}

func mustComputePaths(t *testing.T, options TransformArtifactCompileOptions) TransformArtifactPaths {
	t.Helper()
	paths, err := ComputeTransformArtifactPaths(
		options.Deployment, options.ResourceType, options.Tenant, options.ArtifactMode,
	)
	if err != nil {
		t.Fatalf("ComputeTransformArtifactPaths: %v", err)
	}
	return paths
}

func fileExists(t *testing.T, path string) bool {
	t.Helper()
	_, err := os.Stat(path)
	if err == nil {
		return true
	}
	if errors.Is(err, os.ErrNotExist) {
		return false
	}
	t.Fatalf("stat %s: %v", path, err)
	return false
}

func readFileText(t *testing.T, path string) string {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return string(content)
}

func stringSlicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func stringSliceContains(haystack []string, want string) bool {
	for _, got := range haystack {
		if got == want {
			return true
		}
	}
	return false
}

func writeFileMkdir(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o777); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o666); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
}

// TestRenderTransformLookup ports "lookup rendering is sorted,
// survivor-based, unknown-safe, and last-key-wins".
func TestRenderTransformLookup(t *testing.T) {
	got, err := RenderTransformLookup(
		map[string]map[string]any{
			"alpha": {"configured_name": "Alpha projected"},
			"beta":  {"configured_name": "   "},
			"omega": {"configured_name": "Omega"},
		},
		map[string]map[string]any{
			"alpha": {"configured_name": "Raw Alpha", "id": "CUSTOM_01"},
			"beta":  {"id": "CUSTOM_02"},
			"omega": {"id": "CUSTOM_01"},
		},
		"configured_name",
	)
	if err != nil {
		t.Fatalf("RenderTransformLookup: %v", err)
	}
	// id_by_key keeps one row per item key even where key_by_id collapses
	// colliding IDs to the last sorted key: alpha and omega genuinely share
	// CUSTOM_01, so a hand-written token naming either key decodes to the
	// right ID.
	want := "{\n" +
		"  \"by_id\": {\n" +
		"    \"CUSTOM_01\": \"Omega\",\n" +
		"    \"CUSTOM_02\": \"<unknown>\"\n" +
		"  },\n" +
		"  \"id_by_key\": {\n" +
		"    \"alpha\": \"CUSTOM_01\",\n" +
		"    \"beta\": \"CUSTOM_02\",\n" +
		"    \"omega\": \"CUSTOM_01\"\n" +
		"  },\n" +
		"  \"key_by_id\": {\n" +
		"    \"CUSTOM_01\": \"omega\",\n" +
		"    \"CUSTOM_02\": \"beta\"\n" +
		"  }\n" +
		"}\n"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestRenderTransformLookupNestedRejectsDuplicateCanonicalIDs(t *testing.T) {
	_, err := RenderTransformLookupWithShape(
		map[string]map[string]any{
			"alpha": {"name": "Alpha"},
			"beta":  {"name": "Beta"},
		},
		map[string]map[string]any{
			"alpha": {"id": json.Number("101"), "name": "Alpha"},
			"beta":  {"id": "101", "name": "Beta"},
		},
		"name",
		TransformLookupShapeNested,
		nil,
	)
	if err == nil {
		t.Fatal("RenderTransformLookupWithShape(nested duplicate IDs) error = nil, want refusal before data-lane publication")
	}
	for _, want := range []string{"101", "alpha", "beta"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("RenderTransformLookupWithShape(nested duplicate IDs) error = %q, want it to name %s", err, want)
		}
	}
}

func TestRenderTransformLookupNestedRejectsEmptyCanonicalID(t *testing.T) {
	_, err := RenderTransformLookupWithShape(
		map[string]map[string]any{"alpha": {"name": "Alpha"}},
		map[string]map[string]any{"alpha": {"id": "", "name": "Alpha"}},
		"name",
		TransformLookupShapeNested,
		nil,
	)
	if err == nil || !strings.Contains(err.Error(), "alpha") {
		t.Fatalf("RenderTransformLookupWithShape(nested empty ID) error = %v, want a refusal naming alpha", err)
	}
}

func TestRenderTransformLookupLegacyStillSkipsEmptyIdentity(t *testing.T) {
	got, err := RenderTransformLookupWithShape(
		map[string]map[string]any{"alpha": {"name": "Alpha"}},
		map[string]map[string]any{"alpha": {"id": "", "name": "Alpha"}},
		"name",
		TransformLookupShapeLegacy,
		nil,
	)
	if err != nil {
		t.Fatalf("RenderTransformLookupWithShape(legacy empty ID) error = %v, want nil", err)
	}
	if got != "{}\n" {
		t.Fatalf("RenderTransformLookupWithShape(legacy empty ID) = %q, want empty legacy lookup", got)
	}
}

// TestRenderTransformLookupWithSpacePinsExactBytes pins the exact rendered
// bytes for a lookup that declares one alternate numeric space ("val"),
// covering both the canonical maps (unchanged) and the new top-level
// "spaces" section.
func TestRenderTransformLookupWithSpacePinsExactBytes(t *testing.T) {
	got, err := RenderTransformLookupWithShape(
		map[string]map[string]any{
			"alpha": {"name": "Alpha"},
			"beta":  {"name": "Beta"},
		},
		map[string]map[string]any{
			"alpha": {"id": "CUSTOM_01", "val": json.Number("101"), "name": "Alpha"},
			"beta":  {"id": "CUSTOM_02", "val": json.Number("202"), "name": "Beta"},
		},
		"name",
		TransformLookupShapeLegacy,
		[]string{"val"},
	)
	if err != nil {
		t.Fatalf("RenderTransformLookupWithShape(one space): %v", err)
	}
	want := "{\n" +
		"  \"by_id\": {\n" +
		"    \"CUSTOM_01\": \"Alpha\",\n" +
		"    \"CUSTOM_02\": \"Beta\"\n" +
		"  },\n" +
		"  \"id_by_key\": {\n" +
		"    \"alpha\": \"CUSTOM_01\",\n" +
		"    \"beta\": \"CUSTOM_02\"\n" +
		"  },\n" +
		"  \"key_by_id\": {\n" +
		"    \"CUSTOM_01\": \"alpha\",\n" +
		"    \"CUSTOM_02\": \"beta\"\n" +
		"  },\n" +
		"  \"spaces\": {\n" +
		"    \"val\": {\n" +
		"      \"id_by_key\": {\n" +
		"        \"alpha\": \"101\",\n" +
		"        \"beta\": \"202\"\n" +
		"      },\n" +
		"      \"key_by_id\": {\n" +
		"        \"101\": \"alpha\",\n" +
		"        \"202\": \"beta\"\n" +
		"      }\n" +
		"    }\n" +
		"  }\n" +
		"}\n"
	if got != want {
		t.Fatalf("RenderTransformLookupWithShape(one space) = %q, want %q", got, want)
	}
}

// TestRenderTransformLookupEmptySpacesByteIdenticalToBefore proves an
// explicit empty (non-nil) spaces slice renders exactly the bytes the
// pre-spaces caller produced -- the "spaces" key must never appear absent a
// requested space, and the canonical maps must be untouched.
func TestRenderTransformLookupEmptySpacesByteIdenticalToBefore(t *testing.T) {
	items := map[string]map[string]any{
		"alpha": {"configured_name": "Alpha projected"},
		"beta":  {"configured_name": "   "},
		"omega": {"configured_name": "Omega"},
	}
	originals := map[string]map[string]any{
		"alpha": {"configured_name": "Raw Alpha", "id": "CUSTOM_01"},
		"beta":  {"id": "CUSTOM_02"},
		"omega": {"id": "CUSTOM_01"},
	}
	before, err := RenderTransformLookupWithShape(items, originals, "configured_name", TransformLookupShapeLegacy, nil)
	if err != nil {
		t.Fatalf("RenderTransformLookupWithShape(nil spaces): %v", err)
	}
	after, err := RenderTransformLookupWithShape(items, originals, "configured_name", TransformLookupShapeLegacy, []string{})
	if err != nil {
		t.Fatalf("RenderTransformLookupWithShape(empty spaces): %v", err)
	}
	if after != before {
		t.Fatalf("RenderTransformLookupWithShape(empty spaces) = %q, want byte-identical to nil-spaces %q", after, before)
	}
	if strings.Contains(after, "\"spaces\"") {
		t.Fatalf("RenderTransformLookupWithShape(empty spaces) = %q, want no spaces key", after)
	}
}

// TestRenderTransformLookupDuplicateSpaceIdentityFails proves a duplicate
// identity within one alternate space across items fails loudly, mirroring
// the canonical id path's seenDataIDs duplicate handling.
func TestRenderTransformLookupDuplicateSpaceIdentityFails(t *testing.T) {
	_, err := RenderTransformLookupWithShape(
		map[string]map[string]any{
			"alpha": {"name": "Alpha"},
			"beta":  {"name": "Beta"},
		},
		map[string]map[string]any{
			"alpha": {"id": "CUSTOM_01", "val": json.Number("101"), "name": "Alpha"},
			"beta":  {"id": "CUSTOM_02", "val": json.Number("101"), "name": "Beta"},
		},
		"name",
		TransformLookupShapeLegacy,
		[]string{"val"},
	)
	if err == nil {
		t.Fatal("RenderTransformLookupWithShape(duplicate space identity) error = nil, want refusal")
	}
	for _, want := range []string{"val", "101", "alpha", "beta"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("RenderTransformLookupWithShape(duplicate space identity) error = %q, want it to name %s", err, want)
		}
	}
}

// TestParseLookupSidecarRoundTripsSpaces proves the parser decodes a
// rendered "spaces" section back into TransformLookupData.Spaces.
func TestParseLookupSidecarRoundTripsSpaces(t *testing.T) {
	rendered, err := RenderTransformLookupWithShape(
		map[string]map[string]any{
			"alpha": {"name": "Alpha"},
			"beta":  {"name": "Beta"},
		},
		map[string]map[string]any{
			"alpha": {"id": "CUSTOM_01", "val": json.Number("101"), "name": "Alpha"},
			"beta":  {"id": "CUSTOM_02", "val": json.Number("202"), "name": "Beta"},
		},
		"name",
		TransformLookupShapeLegacy,
		[]string{"val"},
	)
	if err != nil {
		t.Fatalf("RenderTransformLookupWithShape: %v", err)
	}
	value, err := canonjson.ParseDataJSONLosslessly(rendered)
	if err != nil {
		t.Fatalf("ParseDataJSONLosslessly: %v", err)
	}
	data, err := ParseLookupSidecar(value)
	if err != nil {
		t.Fatalf("ParseLookupSidecar: %v", err)
	}
	if data.Spaces == nil {
		t.Fatal("ParseLookupSidecar Spaces = nil, want a \"val\" entry")
	}
	val, ok := data.Spaces["val"]
	if !ok {
		t.Fatalf("ParseLookupSidecar Spaces = %v, want a %q key", data.Spaces, "val")
	}
	wantIDByKey := map[string]string{"alpha": "101", "beta": "202"}
	wantKeyByID := map[string]string{"101": "alpha", "202": "beta"}
	if !mapStringsEqual(val.IDByKey, wantIDByKey) {
		t.Errorf("Spaces[val].IDByKey = %v, want %v", val.IDByKey, wantIDByKey)
	}
	if !mapStringsEqual(val.KeyByID, wantKeyByID) {
		t.Errorf("Spaces[val].KeyByID = %v, want %v", val.KeyByID, wantKeyByID)
	}
}

// TestParseLookupSidecarWithoutSpacesYieldsNilSpaces proves a sidecar
// carrying no "spaces" key -- every sidecar written before this field
// existed -- decodes to a nil Spaces, never a derived or empty map.
func TestParseLookupSidecarWithoutSpacesYieldsNilSpaces(t *testing.T) {
	value, err := canonjson.ParseDataJSONLosslessly(
		`{"by_id":{"CUSTOM_01":"Alpha"},"id_by_key":{"alpha":"CUSTOM_01"},"key_by_id":{"CUSTOM_01":"alpha"}}`,
	)
	if err != nil {
		t.Fatalf("ParseDataJSONLosslessly: %v", err)
	}
	data, err := ParseLookupSidecar(value)
	if err != nil {
		t.Fatalf("ParseLookupSidecar: %v", err)
	}
	if data.Spaces != nil {
		t.Fatalf("ParseLookupSidecar Spaces = %v, want nil", data.Spaces)
	}
}

// TestParseLookupSidecarRejectsUnknownKeyInSpaceEntry proves the parser is
// strict inside a space entry: an unknown key alongside id_by_key/key_by_id
// is a refusal, not a silently ignored field.
func TestParseLookupSidecarRejectsUnknownKeyInSpaceEntry(t *testing.T) {
	value, err := canonjson.ParseDataJSONLosslessly(
		`{"by_id":{},"id_by_key":{},"key_by_id":{},"spaces":{"val":{"id_by_key":{},"key_by_id":{},"unexpected":{}}}}`,
	)
	if err != nil {
		t.Fatalf("ParseDataJSONLosslessly: %v", err)
	}
	_, err = ParseLookupSidecar(value)
	if err == nil {
		t.Fatal("ParseLookupSidecar(unknown key in space entry) error = nil, want refusal")
	}
	for _, want := range []string{"val", "unexpected"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("ParseLookupSidecar(unknown key in space entry) error = %q, want it to name %s", err, want)
		}
	}
}

func mapStringsEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

func TestCompileDataReferentRejectsDuplicateIDsBeforePublication(t *testing.T) {
	options := newArtifactOptions(t.TempDir(), "sample_groups_data")
	options.ArtifactMode = TransformArtifactModeDataReferent
	options.Result = PullTransformResult{
		Items: map[string]map[string]any{
			"alpha": {"name": "Alpha"},
			"beta":  {"name": "Beta"},
		},
		Originals: map[string]map[string]any{
			"alpha": {"id": json.Number("101"), "name": "Alpha"},
			"beta":  {"id": "101", "name": "Beta"},
		},
		Drops: []string{},
	}
	paths := mustComputePaths(t, options)
	if _, err := CompileTransformArtifacts(options); err == nil {
		t.Fatal("CompileTransformArtifacts(data duplicate IDs) error = nil, want refusal before publication")
	} else {
		for _, want := range []string{"101", "alpha", "beta"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("CompileTransformArtifacts(data duplicate IDs) error = %q, want it to name %s", err, want)
			}
		}
	}
	if fileExists(t, paths.Config) || fileExists(t, paths.Lookup) {
		t.Fatalf("CompileTransformArtifacts(data duplicate IDs) published config or lookup: config=%q lookup=%q", paths.Config, paths.Lookup)
	}
}

func TestCompileDataReferentRejectsEmptyIDBeforePublication(t *testing.T) {
	options := newArtifactOptions(t.TempDir(), "sample_groups_data")
	options.ArtifactMode = TransformArtifactModeDataReferent
	options.Result = PullTransformResult{
		Items:     map[string]map[string]any{"alpha": {"name": "Alpha"}},
		Originals: map[string]map[string]any{"alpha": {"id": "", "name": "Alpha"}},
		Drops:     []string{},
	}
	paths := mustComputePaths(t, options)
	if _, err := CompileTransformArtifacts(options); err == nil {
		t.Fatal("CompileTransformArtifacts(data empty ID) error = nil, want refusal before publication")
	}
	if fileExists(t, paths.Config) || fileExists(t, paths.Lookup) {
		t.Fatalf("CompileTransformArtifacts(data empty ID) published config or lookup: config=%q lookup=%q", paths.Config, paths.Lookup)
	}
}

// TestCompileTransformArtifactsPerformsNoFilesystemMutation ports
// "transform artifact compilation performs no filesystem mutation".
func TestCompileTransformArtifactsPerformsNoFilesystemMutation(t *testing.T) {
	workspace := t.TempDir()
	options := defaultArtifactOptions(workspace)
	paths := mustComputePaths(t, options)

	compiled, err := CompileTransformArtifacts(options)
	if err != nil {
		t.Fatalf("CompileTransformArtifacts: %v", err)
	}
	if compiled.Paths != paths {
		t.Fatalf("compiled.Paths = %+v, want %+v", compiled.Paths, paths)
	}
	if fileExists(t, filepath.Join(workspace, "config")) {
		t.Fatal("config directory should not exist before publish")
	}
	if fileExists(t, filepath.Join(workspace, "imports")) {
		t.Fatal("imports directory should not exist before publish")
	}

	if _, err := PublishCompiledTransformArtifacts(compiled); err != nil {
		t.Fatalf("PublishCompiledTransformArtifacts: %v", err)
	}
	if !fileExists(t, paths.Config) {
		t.Fatal("expected config to exist after publish")
	}
	if !fileExists(t, paths.Imports) {
		t.Fatal("expected imports to exist after publish")
	}
	if !fileExists(t, paths.Lookup) {
		t.Fatal("expected lookup to exist after publish")
	}
}

func TestDisabledBindingsRetainLiteralIDsAndRemoveStaleArtifact(t *testing.T) {
	workspace := t.TempDir()
	options := newArtifactOptions(workspace, "sample_item")
	options.References = map[string]TransformReferenceSpec{
		"group_id": {NameField: "name", Referent: "sample_group"},
	}
	options.Result = PullTransformResult{
		Drops:     []string{},
		Items:     map[string]map[string]any{"item": {"group_id": "group-id", "name": "Item"}},
		Originals: map[string]map[string]any{"item": {"id": "item-id", "name": "Item"}},
	}
	options.BindingContext = BindingContext{
		Mode:       deployment.ReferenceBindingDisabled,
		Derived:    map[string]bool{},
		Generated:  map[string]bool{"sample_group": true, "sample_item": true},
		References: options.References,
		ResourceRoots: map[string]string{
			"sample_group": "sample_group",
			"sample_item":  "sample_item",
		},
	}
	options.LookupOverrides = map[string]*TransformLookupData{
		"sample_group": {KeyByID: map[string]string{"group-id": "group_one"}},
	}
	paths := mustComputePaths(t, options)
	writeFileMkdir(t, paths.GeneratedBindings, "stale generated bindings\n")

	compiled, err := CompileTransformArtifacts(options)
	if err != nil {
		t.Fatalf("CompileTransformArtifacts: %v", err)
	}
	if len(compiled.Binding.Resources) != 0 {
		t.Fatalf("disabled binding resources = %#v, want empty", compiled.Binding.Resources)
	}
	if _, err := PublishCompiledTransformArtifacts(compiled); err != nil {
		t.Fatalf("PublishCompiledTransformArtifacts: %v", err)
	}
	if _, err := os.Stat(paths.GeneratedBindings); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("os.Stat(%q) after disabled publish = %v, want os.ErrNotExist", paths.GeneratedBindings, err)
	}
	config := readFileText(t, paths.Config)
	if !strings.Contains(config, `"group-id"`) {
		t.Errorf("disabled config = %q, want literal group ID", config)
	}
}

// TestPublishTokenisedCompileWritesNoBindingsCache pins Task A2: a
// cross-state compile still derives Binding.Resources in memory (so
// assertMintedTokensCovered and every other in-process consumer keep
// working), but PublishCompiledTransformArtifacts never writes
// .generated.expressions.json for it -- the file is a cache of something
// gen-env now derives at render time (predecessor commit), so committing it
// is redundant. A pre-existing committed copy is stale-cleaned and reported
// exactly like any other stale artifact.
func TestPublishTokenisedCompileWritesNoBindingsCache(t *testing.T) {
	workspace := t.TempDir()
	options := newArtifactOptions(workspace, "sample_item")
	options.References = map[string]TransformReferenceSpec{
		"group_id": {NameField: "name", Referent: "sample_group"},
	}
	options.Result = PullTransformResult{
		Drops:     []string{},
		Items:     map[string]map[string]any{"item": {"group_id": "group-id", "name": "Item"}},
		Originals: map[string]map[string]any{"item": {"id": "item-id", "name": "Item"}},
	}
	options.BindingContext = BindingContext{
		Mode:       deployment.ReferenceBindingCrossState,
		Derived:    map[string]bool{},
		Generated:  map[string]bool{"sample_group": true, "sample_item": true},
		References: options.References,
		ResourceRoots: map[string]string{
			"sample_group": "sample_group",
			"sample_item":  "sample_item",
		},
	}
	options.LookupOverrides = map[string]*TransformLookupData{
		"sample_group": {KeyByID: map[string]string{"group-id": "group_one"}},
	}
	var diagnostics []string
	options.OnDiagnostic = func(message string) { diagnostics = append(diagnostics, message) }
	paths := mustComputePaths(t, options)
	writeFileMkdir(t, paths.GeneratedBindings, "stale generated bindings\n")

	compiled, err := CompileTransformArtifacts(options)
	if err != nil {
		t.Fatalf("CompileTransformArtifacts: %v", err)
	}
	if len(compiled.Binding.Resources) == 0 {
		t.Fatal("expected a non-empty derived binding (a tokenised, cross-state compile) for this test to be meaningful")
	}

	result, err := PublishCompiledTransformArtifacts(compiled)
	if err != nil {
		t.Fatalf("PublishCompiledTransformArtifacts: %v", err)
	}
	if fileExists(t, paths.GeneratedBindings) {
		t.Errorf("publish must not write %s; the binding is derivable at render time", paths.GeneratedBindings)
	}
	if !stringSliceContains(result.Removed, paths.GeneratedBindings) {
		t.Errorf("result.Removed = %v, want it to contain %s", result.Removed, paths.GeneratedBindings)
	}
	wantNote := "removed stale " + paths.GeneratedBindings
	if !stringSliceContains(diagnostics, wantNote) {
		t.Errorf("diagnostics = %v, want it to contain %q", diagnostics, wantNote)
	}
	config := readFileText(t, paths.Config)
	if !strings.Contains(config, `"sample_group.group_one"`) {
		t.Errorf("tokenised config = %q, want the substituted reference token", config)
	}
}

// TestPublishWritesLookupUnderLookupsAndStaleCleansLegacy pins Task B1's
// single-artifact publish contract: the lookup lands at its current location
// (config/<tenant>/lookups/<type>.lookup.json) and a pre-existing copy at
// the pre-migration legacy location (config/<tenant>/<type>.lookup.json) is
// stale-cleaned and reported, mirroring StaleConfig's own unconditional
// cleanup.
func TestPublishWritesLookupUnderLookupsAndStaleCleansLegacy(t *testing.T) {
	workspace := t.TempDir()
	options := defaultArtifactOptions(workspace)
	var diagnostics []string
	options.OnDiagnostic = func(message string) { diagnostics = append(diagnostics, message) }
	paths := mustComputePaths(t, options)
	if !strings.Contains(paths.Lookup, filepath.Join("lookups", "sample_resource.lookup.json")) {
		t.Fatalf("sanity: paths.Lookup = %s, want it under a lookups/ subdirectory", paths.Lookup)
	}
	writeFileMkdir(t, paths.LegacyLookup, staleBookPlaceholder())

	compiled, err := CompileTransformArtifacts(options)
	if err != nil {
		t.Fatalf("CompileTransformArtifacts: %v", err)
	}
	result, err := PublishCompiledTransformArtifacts(compiled)
	if err != nil {
		t.Fatalf("PublishCompiledTransformArtifacts: %v", err)
	}
	if !fileExists(t, paths.Lookup) {
		t.Error("expected the lookup to exist at the current lookups/ path after publish")
	}
	if fileExists(t, paths.LegacyLookup) {
		t.Error("expected the legacy-path lookup to be removed after publish")
	}
	if !stringSliceContains(result.Written, paths.Lookup) {
		t.Errorf("result.Written = %v, want it to contain %s", result.Written, paths.Lookup)
	}
	if !stringSliceContains(result.Removed, paths.LegacyLookup) {
		t.Errorf("result.Removed = %v, want it to contain %s", result.Removed, paths.LegacyLookup)
	}
	wantNote := "removed stale legacy lookup " + paths.LegacyLookup
	if !stringSliceContains(diagnostics, wantNote) {
		t.Errorf("diagnostics = %v, want it to contain %q", diagnostics, wantNote)
	}
}

// TestDeriveGeneratedBindingsNestedIndexedPaths ports "nested pack
// references emit deterministic concrete indexed binding paths".
func TestDeriveGeneratedBindingsNestedIndexedPaths(t *testing.T) {
	context := BindingContext{
		Derived:   map[string]bool{},
		Generated: map[string]bool{"zpa_app_connector_group": true, "zpa_server_group": true},
		Mode:      deployment.ReferenceBindingCrossState,
		References: map[string]TransformReferenceSpec{
			"server_groups.id": {NameField: "name", Referent: "zpa_server_group"},
		},
		ResourceRoots: map[string]string{
			"zpa_app_connector_group": "zpa_app_connector_group",
			"zpa_server_group":        "zpa_server_group",
		},
	}
	items := map[string]map[string]any{
		"connector_one": {
			"server_groups": []any{
				map[string]any{"id": []any{"sg-2", "sg-1"}, "name": "Second and first"},
				map[string]any{"id": []any{"sg-3"}, "name": "Third"},
			},
		},
	}
	lookupKeys := map[string]map[string]string{
		"zpa_server_group": {"sg-1": "server_one", "sg-2": "server_two", "sg-3": "server_three"},
	}
	result, err := DeriveGeneratedBindings(context, items, lookupKeys, "zpa_app_connector_group")
	if err != nil {
		t.Fatalf("DeriveGeneratedBindings: %v", err)
	}
	rendered, err := RenderGeneratedBindings(result.Resources)
	if err != nil {
		t.Fatalf("RenderGeneratedBindings: %v", err)
	}
	want := "{\n" +
		"  \"resources\": {\n" +
		"    \"zpa_app_connector_group.connector_one\": {\n" +
		"      \"server_groups[0].id\": {\n" +
		"        \"expression\": \"[data.terraform_remote_state.zpa_server_group.outputs.iw_reference_ids.zpa_server_group[\\\"server_two\\\"], data.terraform_remote_state.zpa_server_group.outputs.iw_reference_ids.zpa_server_group[\\\"server_one\\\"]]\",\n" +
		"        \"reason\": \"cross-state reference binding via zpa_server_group root output\"\n" +
		"      },\n" +
		"      \"server_groups[1].id\": {\n" +
		"        \"expression\": \"[data.terraform_remote_state.zpa_server_group.outputs.iw_reference_ids.zpa_server_group[\\\"server_three\\\"]]\",\n" +
		"        \"reason\": \"cross-state reference binding via zpa_server_group root output\"\n" +
		"      }\n" +
		"    }\n" +
		"  }\n" +
		"}\n"
	if rendered != want {
		t.Fatalf("got %q, want %q", rendered, want)
	}
	wantNotes := []string{"zpa_app_connector_group: 3 bound, 0 skipped"}
	if !stringSlicesEqual(result.Notes, wantNotes) {
		t.Fatalf("notes = %v, want %v", result.Notes, wantNotes)
	}
}

// TestDeriveGeneratedBindingsRetainsUnresolvedDiagnostics ports "nested
// pack references retain unresolved diagnostics without suppressing
// resolved siblings".
func TestDeriveGeneratedBindingsRetainsUnresolvedDiagnostics(t *testing.T) {
	context := BindingContext{
		Derived:   map[string]bool{},
		Generated: map[string]bool{"zpa_app_connector_group": true, "zpa_server_group": true},
		Mode:      deployment.ReferenceBindingCrossState,
		References: map[string]TransformReferenceSpec{
			"server_groups.id": {NameField: "name", Referent: "zpa_server_group"},
		},
		ResourceRoots: map[string]string{
			"zpa_app_connector_group": "zpa_app",
			"zpa_server_group":        "zpa_app",
		},
	}
	items := map[string]map[string]any{
		"connector_one": {
			"server_groups": []any{
				map[string]any{"id": []any{"sg-known", "sg-missing"}},
			},
		},
	}
	lookupKeys := map[string]map[string]string{
		"zpa_server_group": {"sg-known": "known"},
	}
	result, err := DeriveGeneratedBindings(context, items, lookupKeys, "zpa_app_connector_group")
	if err != nil {
		t.Fatalf("DeriveGeneratedBindings: %v", err)
	}
	rendered, err := RenderGeneratedBindings(result.Resources)
	if err != nil {
		t.Fatalf("RenderGeneratedBindings: %v", err)
	}
	want := "{\n" +
		"  \"resources\": {\n" +
		"    \"zpa_app_connector_group.connector_one\": {\n" +
		"      \"server_groups[0].id\": {\n" +
		"        \"expression\": \"[data.terraform_remote_state.zpa_app.outputs.iw_reference_ids.zpa_server_group[\\\"known\\\"], \\\"sg-missing\\\"]\",\n" +
		"        \"reason\": \"cross-state reference binding via zpa_server_group root output\"\n" +
		"      }\n" +
		"    }\n" +
		"  }\n" +
		"}\n"
	if rendered != want {
		t.Fatalf("got %q, want %q", rendered, want)
	}
	wantNotes := []string{
		`zpa_app_connector_group.connector_one.server_groups[0].id[1] value "sg-missing" skipped; id is absent from zpa_server_group lookup`,
		"zpa_app_connector_group: 1 bound, 1 skipped (id_absent=1)",
	}
	if !stringSlicesEqual(result.Notes, wantNotes) {
		t.Fatalf("notes = %v, want %v", result.Notes, wantNotes)
	}
}

// TestDeriveGeneratedBindingsTopLevel ports "top-level generated reference
// binding output remains byte-compatible".
func TestDeriveGeneratedBindingsTopLevel(t *testing.T) {
	context := BindingContext{
		Derived:   map[string]bool{},
		Generated: map[string]bool{"zpa_application_segment": true, "zpa_segment_group": true},
		Mode:      deployment.ReferenceBindingCrossState,
		References: map[string]TransformReferenceSpec{
			"segment_group_id": {NameField: "name", Referent: "zpa_segment_group"},
		},
		ResourceRoots: map[string]string{
			"zpa_application_segment": "zpa_custom",
			"zpa_segment_group":       "zpa_custom",
		},
	}
	items := map[string]map[string]any{"app_one": {"segment_group_id": "sg-1"}}
	lookupKeys := map[string]map[string]string{"zpa_segment_group": {"sg-1": "segment_one"}}
	result, err := DeriveGeneratedBindings(context, items, lookupKeys, "zpa_application_segment")
	if err != nil {
		t.Fatalf("DeriveGeneratedBindings: %v", err)
	}
	rendered, err := RenderGeneratedBindings(result.Resources)
	if err != nil {
		t.Fatalf("RenderGeneratedBindings: %v", err)
	}
	want := "{\n" +
		"  \"resources\": {\n" +
		"    \"zpa_application_segment.app_one\": {\n" +
		"      \"segment_group_id\": {\n" +
		"        \"expression\": \"data.terraform_remote_state.zpa_custom.outputs.iw_reference_ids.zpa_segment_group[\\\"segment_one\\\"]\",\n" +
		"        \"reason\": \"cross-state reference binding via zpa_segment_group root output\"\n" +
		"      }\n" +
		"    }\n" +
		"  }\n" +
		"}\n"
	if rendered != want {
		t.Fatalf("got %q, want %q", rendered, want)
	}
	wantNotes := []string{"zpa_application_segment: 1 bound, 0 skipped"}
	if !stringSlicesEqual(result.Notes, wantNotes) {
		t.Fatalf("notes = %v, want %v", result.Notes, wantNotes)
	}
}

// TestWriteDerivedTransformArtifact exercises writeDerivedTransformArtifact
// directly (the original test corpus's only test that
// reaches this path, "derived reorder writes config only", drives it
// through runTransformBatch and is skipped as runner-level -- see this
// file's package doc comment). Confirms: "derived resources write config
// only and intentionally create no imports" (no imports/moves/lookup/
// generated-bindings files are ever created), a stale opposite-format
// config is removed first, and the returned path plus onDiagnostic
// messages match the TS source's exact wording.
func TestWriteDerivedTransformArtifact(t *testing.T) {
	workspace := t.TempDir()
	dep := testDeployment(workspace, false)
	items := map[string]map[string]any{"reordered": {"name": "Reordered"}}
	references := map[string]TransformReferenceSpec{}

	paths, err := ComputeTransformArtifactPaths(
		dep, "sample_reorder", "tenant", TransformArtifactModeGenerated,
	)
	if err != nil {
		t.Fatalf("ComputeTransformArtifactPaths: %v", err)
	}
	writeFileMkdir(t, paths.StaleConfig, "stale hcl config\n")

	var diagnostics []string
	configPath, err := WriteDerivedTransformArtifact(
		dep, items, references, "sample_reorder", "sample_source", "tenant", "items",
		func(message string) { diagnostics = append(diagnostics, message) },
	)
	if err != nil {
		t.Fatalf("WriteDerivedTransformArtifact: %v", err)
	}
	if configPath != paths.Config {
		t.Fatalf("configPath = %q, want %q", configPath, paths.Config)
	}
	if !fileExists(t, paths.Config) {
		t.Fatal("expected config to be written")
	}
	if fileExists(t, paths.StaleConfig) {
		t.Fatal("expected the stale opposite-format config to be removed")
	}
	for _, mustNotExist := range []string{paths.Imports, paths.Moves, paths.Lookup, paths.GeneratedBindings} {
		if fileExists(t, mustNotExist) {
			t.Fatalf("derived artifact write must not create %s", mustNotExist)
		}
	}
	gotConfig := readFileText(t, paths.Config)
	wantConfigText := "{\n  \"items\": {\n    \"reordered\": {\n      \"name\": \"Reordered\"\n    }\n  }\n}\n"
	if gotConfig != wantConfigText {
		t.Fatalf("got %q, want %q", gotConfig, wantConfigText)
	}
	wantDiagnostics := []string{
		"removed stale " + paths.StaleConfig,
		"wrote " + paths.Config + " (derived from sample_source; not importable — no imports)",
	}
	if !stringSlicesEqual(diagnostics, wantDiagnostics) {
		t.Fatalf("diagnostics = %v, want %v", diagnostics, wantDiagnostics)
	}
}

// TestWriteTransformArtifactsDetectsRename exercises the moved/pending-move
// lifecycle at the pure library level, standing in for the runner-level
// "unresolved move evidence survives reruns, stages for plan, and rejects
// conflicts atomically" test (which drives this through
// runTransformBatch/stageImports/planEnvironmentRoots and is out of this
// package's scope -- see this file's package doc comment): a re-keyed item
// (same import id, new key) produces a *_moves.tf file with a single
// `moved` block on the second write, and re-running with the identical
// inputs a third time leaves that file byte-identical (RenderedMoves is
// deterministic and existingMoves already matches it).
func TestWriteTransformArtifactsDetectsRename(t *testing.T) {
	workspace := t.TempDir()
	options := newArtifactOptions(workspace, "sample_reorder")
	options.LookupNameField = nil
	options.Result = PullTransformResult{
		Drops:     []string{},
		Items:     map[string]map[string]any{"original_name": {"name": "Example"}},
		Originals: map[string]map[string]any{"original_name": {"id": "7"}},
	}
	if _, err := WriteTransformArtifacts(options); err != nil {
		t.Fatalf("WriteTransformArtifacts (first write): %v", err)
	}
	paths := mustComputePaths(t, options)
	if fileExists(t, paths.Moves) {
		t.Fatal("no moves file should exist before any rename")
	}

	options.Result = PullTransformResult{
		Drops:     []string{},
		Items:     map[string]map[string]any{"renamed_thing": {"name": "Example"}},
		Originals: map[string]map[string]any{"renamed_thing": {"id": "7"}},
	}
	if _, err := WriteTransformArtifacts(options); err != nil {
		t.Fatalf("WriteTransformArtifacts (rename): %v", err)
	}
	wantMoves := "moved {\n" +
		"  from = module.sample_reorder.sample_reorder.this[\"original_name\"]\n" +
		"  to   = module.sample_reorder.sample_reorder.this[\"renamed_thing\"]\n" +
		"}\n"
	if got := readFileText(t, paths.Moves); got != wantMoves {
		t.Fatalf("got moves %q, want %q", got, wantMoves)
	}

	if _, err := WriteTransformArtifacts(options); err != nil {
		t.Fatalf("WriteTransformArtifacts (rerun): %v", err)
	}
	if got := readFileText(t, paths.Moves); got != wantMoves {
		t.Fatalf("moves file changed on rerun: got %q, want %q", got, wantMoves)
	}
}

// TestCompileTransformArtifactsRejectsConflictingMoveEvidence ports the
// atomic-rejection half of "unresolved move evidence survives reruns,
// stages for plan, and rejects conflicts atomically": if
// paths.moves already holds move evidence that does not match this
// compile's own newly derived moves, CompileTransformArtifacts must fail
// with the exact "unresolved/conflicting move evidence" message rather
// than silently overwrite operator-pending migration evidence.
func TestCompileTransformArtifactsRejectsConflictingMoveEvidence(t *testing.T) {
	workspace := t.TempDir()
	options := newArtifactOptions(workspace, "sample_reorder")
	options.LookupNameField = nil
	options.Result = PullTransformResult{
		Drops:     []string{},
		Items:     map[string]map[string]any{"a": {"name": "A"}},
		Originals: map[string]map[string]any{"a": {"id": "7"}},
	}
	if _, err := WriteTransformArtifacts(options); err != nil {
		t.Fatalf("WriteTransformArtifacts (seed): %v", err)
	}
	paths := mustComputePaths(t, options)

	options.Result = PullTransformResult{
		Drops:     []string{},
		Items:     map[string]map[string]any{"b": {"name": "A"}},
		Originals: map[string]map[string]any{"b": {"id": "7"}},
	}
	conflicting := "moved {\n" +
		"  from = module.sample_reorder.sample_reorder.this[\"a\"]\n" +
		"  to   = module.sample_reorder.sample_reorder.this[\"unrelated_target\"]\n" +
		"}\n"
	writeFileMkdir(t, paths.Moves, conflicting)

	_, err := CompileTransformArtifacts(options)
	if err == nil {
		t.Fatal("expected a conflicting-move-evidence error")
	}
	if !strings.Contains(err.Error(), "unresolved/conflicting move evidence for sample_reorder") {
		t.Fatalf("got error %q, want a match for 'unresolved/conflicting move evidence for sample_reorder'", err.Error())
	}
	if got := readFileText(t, paths.Moves); got != conflicting {
		t.Fatalf("moves file mutated by a failed compile: got %q, want unchanged %q", got, conflicting)
	}
}

// TestComputeTransformArtifactPathsFlatLayout ports "artifact paths retain
// the flat tenant/resource layout", extended for Task B1: the lookup (Lookup)
// is the one artifact that no longer sits flat in the tenant directory --
// it lives under a lookups/ subdirectory -- while LegacyLookup keeps naming
// its pre-migration flat location for dual-read and stale-cleanup.
func TestComputeTransformArtifactPathsFlatLayout(t *testing.T) {
	got, err := ComputeTransformArtifactPaths(
		testDeployment("overlay", false), "zia_rule_labels", "tenant", TransformArtifactModeGenerated,
	)
	if err != nil {
		t.Fatalf("ComputeTransformArtifactPaths: %v", err)
	}
	want := TransformArtifactPaths{
		Config:            path.Join("overlay", "config", "tenant", "zia_rule_labels.auto.tfvars.json"),
		GeneratedBindings: path.Join("overlay", "config", "tenant", "zia_rule_labels.generated.expressions.json"),
		Imports:           path.Join("overlay", "imports", "tenant", "zia_rule_labels_imports.tf"),
		LegacyConfig:      path.Join("overlay", "config", "tenant", "zia_rule_labels.auto.tfvars.json"),
		LegacyStaleConfig: path.Join("overlay", "config", "tenant", "zia_rule_labels.auto.tfvars"),
		Lookup:            path.Join("overlay", "config", "tenant", "lookups", "zia_rule_labels.lookup.json"),
		LegacyLookup:      path.Join("overlay", "config", "tenant", "zia_rule_labels.lookup.json"),
		Moves:             path.Join("overlay", "imports", "tenant", "zia_rule_labels_moves.tf"),
		StaleConfig:       path.Join("overlay", "config", "tenant", "zia_rule_labels.auto.tfvars"),
	}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestComputeTransformArtifactPathsDataReferentLayout(t *testing.T) {
	cases := []struct {
		name        string
		dep         deployment.Deployment
		config      string
		stale       string
		legacy      string
		legacyStale string
	}{
		{
			name:        "json",
			dep:         testDeployment("overlay", false),
			config:      path.Join("overlay", "config", "tenant", "data", "zia_location_groups.auto.tfvars.json"),
			stale:       path.Join("overlay", "config", "tenant", "data", "zia_location_groups.auto.tfvars"),
			legacy:      path.Join("overlay", "config", "tenant", "zia_location_groups.auto.tfvars.json"),
			legacyStale: path.Join("overlay", "config", "tenant", "zia_location_groups.auto.tfvars"),
		},
		{
			name:        "hcl",
			dep:         testDeployment("overlay", true),
			config:      path.Join("overlay", "config", "tenant", "data", "zia_location_groups.auto.tfvars"),
			stale:       path.Join("overlay", "config", "tenant", "data", "zia_location_groups.auto.tfvars.json"),
			legacy:      path.Join("overlay", "config", "tenant", "zia_location_groups.auto.tfvars"),
			legacyStale: path.Join("overlay", "config", "tenant", "zia_location_groups.auto.tfvars.json"),
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got, err := ComputeTransformArtifactPaths(
				testCase.dep, "zia_location_groups", "tenant", TransformArtifactModeDataReferent,
			)
			if err != nil {
				t.Fatalf("ComputeTransformArtifactPaths(data referent, %s) error = %v, want nil", testCase.name, err)
			}
			if got.Config != testCase.config {
				t.Errorf("ComputeTransformArtifactPaths(data referent, %s).Config = %q, want %q", testCase.name, got.Config, testCase.config)
			}
			if got.StaleConfig != testCase.stale {
				t.Errorf("ComputeTransformArtifactPaths(data referent, %s).StaleConfig = %q, want %q", testCase.name, got.StaleConfig, testCase.stale)
			}
			if got.LegacyConfig != testCase.legacy {
				t.Errorf("ComputeTransformArtifactPaths(data referent, %s).LegacyConfig = %q, want %q", testCase.name, got.LegacyConfig, testCase.legacy)
			}
			if got.LegacyStaleConfig != testCase.legacyStale {
				t.Errorf("ComputeTransformArtifactPaths(data referent, %s).LegacyStaleConfig = %q, want %q", testCase.name, got.LegacyStaleConfig, testCase.legacyStale)
			}
			wantLookup := path.Join("overlay", "config", "tenant", "lookups", "zia_location_groups.lookup.json")
			if got.Lookup != wantLookup {
				t.Errorf("ComputeTransformArtifactPaths(data referent, %s).Lookup = %q, want unified %q", testCase.name, got.Lookup, wantLookup)
			}
		})
	}
}

func TestPublishDataReferentRemovesFlatConfigInBothFormats(t *testing.T) {
	workspace := t.TempDir()
	options := newArtifactOptions(workspace, "zia_location_groups")
	options.ArtifactMode = TransformArtifactModeDataReferent
	paths := mustComputePaths(t, options)
	writeFileMkdir(t, paths.LegacyConfig, "stale json config\n")
	writeFileMkdir(t, paths.LegacyStaleConfig, "stale hcl config\n")

	compiled, err := CompileTransformArtifacts(options)
	if err != nil {
		t.Fatalf("CompileTransformArtifacts(data referent) error = %v, want nil", err)
	}
	result, err := PublishCompiledTransformArtifacts(compiled)
	if err != nil {
		t.Fatalf("PublishCompiledTransformArtifacts(data referent) error = %v, want nil", err)
	}
	if !fileExists(t, paths.Config) {
		t.Errorf("PublishCompiledTransformArtifacts(data referent).Config = %q, want published nested config", paths.Config)
	}
	for _, legacyPath := range []string{paths.LegacyConfig, paths.LegacyStaleConfig} {
		if fileExists(t, legacyPath) {
			t.Errorf("PublishCompiledTransformArtifacts(data referent) left stale flat config %q", legacyPath)
		}
		if !stringSliceContains(result.Removed, legacyPath) {
			t.Errorf("PublishCompiledTransformArtifacts(data referent).Removed = %v, want %q", result.Removed, legacyPath)
		}
	}
}

func TestPublishManagedLaneKeepsFlatConfigLifecycle(t *testing.T) {
	workspace := t.TempDir()
	options := defaultArtifactOptions(workspace)
	paths := mustComputePaths(t, options)
	writeFileMkdir(t, paths.Config, "preexisting managed config\n")
	dataConfig := filepath.Join(filepath.Dir(paths.Config), "data", filepath.Base(paths.Config))
	writeFileMkdir(t, dataConfig, "unrelated data-lane config\n")

	compiled, err := CompileTransformArtifacts(options)
	if err != nil {
		t.Fatalf("CompileTransformArtifacts(managed) error = %v, want nil", err)
	}
	result, err := PublishCompiledTransformArtifacts(compiled)
	if err != nil {
		t.Fatalf("PublishCompiledTransformArtifacts(managed) error = %v, want nil", err)
	}
	wantWritten := []string{paths.Lookup, paths.Config, paths.Imports}
	if !stringSlicesEqual(result.Written, wantWritten) {
		t.Errorf("PublishCompiledTransformArtifacts(managed).Written = %v, want %v", result.Written, wantWritten)
	}
	if len(result.Removed) != 0 {
		t.Errorf("PublishCompiledTransformArtifacts(managed).Removed = %v, want no new removals", result.Removed)
	}
	if got := readFileText(t, dataConfig); got != "unrelated data-lane config\n" {
		t.Errorf("PublishCompiledTransformArtifacts(managed) changed %q to %q, want unchanged sentinel", dataConfig, got)
	}
}
