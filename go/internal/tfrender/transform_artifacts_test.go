package tfrender

// These tests exercise artifact compilation and publication with explicit
// current inputs and expected outputs.
import (
	"errors"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

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

func defaultArtifactOptions(workspace string) TransformArtifactCompileOptions {
	return newArtifactOptions(workspace, "sample_resource")
}

func mustComputePaths(t *testing.T, options TransformArtifactCompileOptions) TransformArtifactPaths {
	t.Helper()
	paths, err := ComputeTransformArtifactPaths(options.Deployment, options.ResourceType, options.Tenant)
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

// artifactSnapshot reads every one of paths's six files, matching this test
// file's TS counterpart's `artifactSnapshot`: a missing file reads as a nil
// *string (TS: null) rather than failing the read.
func artifactSnapshot(t *testing.T, paths TransformArtifactPaths) map[string]*string {
	t.Helper()
	snapshot := map[string]*string{}
	for key, path := range map[string]string{
		"config": paths.Config, "generatedBindings": paths.GeneratedBindings,
		"imports": paths.Imports, "lookup": paths.Lookup, "moves": paths.Moves, "staleConfig": paths.StaleConfig,
	} {
		content, err := os.ReadFile(path)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				snapshot[key] = nil
				continue
			}
			t.Fatalf("reading %s: %v", path, err)
		}
		text := string(content)
		snapshot[key] = &text
	}
	return snapshot
}

func assertSnapshotsEqual(t *testing.T, got, want map[string]*string) {
	t.Helper()
	for key, wantValue := range want {
		gotValue, ok := got[key]
		if !ok {
			t.Fatalf("snapshot missing key %q", key)
			continue
		}
		if (wantValue == nil) != (gotValue == nil) {
			t.Fatalf("%s: got present=%v, want present=%v", key, gotValue != nil, wantValue != nil)
			continue
		}
		if wantValue != nil && *gotValue != *wantValue {
			t.Fatalf("%s: got %q, want %q", key, *gotValue, *wantValue)
		}
	}
}

func basenames(paths []string) []string {
	out := make([]string, len(paths))
	for i, p := range paths {
		out[i] = filepath.Base(p)
	}
	return out
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
	want := "{\n" +
		"  \"by_id\": {\n" +
		"    \"CUSTOM_01\": \"Omega\",\n" +
		"    \"CUSTOM_02\": \"<unknown>\"\n" +
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

// TestArtifactBatchPreflightsBeforePublishingAnyMember ports "artifact
// batch preflights every compile before publishing any member".
func TestArtifactBatchPreflightsBeforePublishingAnyMember(t *testing.T) {
	workspace := t.TempDir()
	valid := newArtifactOptions(workspace, "sample_first")
	invalid := newArtifactOptions(workspace, "sample_second")
	invalid.Override = map[string]any{"import_id": "{missing}"}

	_, err := CompileTransformArtifactBatch([]TransformArtifactCompileOptions{valid, invalid})
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), `references missing field "missing"`) {
		t.Fatalf("got error %q, want a match for 'references missing field \"missing\"'", err.Error())
	}
	if fileExists(t, filepath.Join(workspace, "config")) {
		t.Fatal("config directory should not exist after a failed preflight")
	}
	if fileExists(t, filepath.Join(workspace, "imports")) {
		t.Fatal("imports directory should not exist after a failed preflight")
	}
}

// TestCompileAndPublishPreserveLegacyArtifactBytesAndLifecycle ports
// "compile and publish preserve legacy artifact bytes and lifecycle".
func TestCompileAndPublishPreserveLegacyArtifactBytesAndLifecycle(t *testing.T) {
	workspace := t.TempDir()
	legacyOptions := defaultArtifactOptions(filepath.Join(workspace, "legacy"))
	splitOptions := defaultArtifactOptions(filepath.Join(workspace, "split"))

	legacy, err := WriteTransformArtifacts(legacyOptions)
	if err != nil {
		t.Fatalf("WriteTransformArtifacts: %v", err)
	}
	splitCompiled, err := CompileTransformArtifacts(splitOptions)
	if err != nil {
		t.Fatalf("CompileTransformArtifacts: %v", err)
	}
	split, err := PublishCompiledTransformArtifacts(splitCompiled)
	if err != nil {
		t.Fatalf("PublishCompiledTransformArtifacts: %v", err)
	}
	legacyPaths := mustComputePaths(t, legacyOptions)
	splitPaths := mustComputePaths(t, splitOptions)

	for _, pair := range [][2]string{{splitPaths.Config, legacyPaths.Config}, {splitPaths.Imports, legacyPaths.Imports}, {splitPaths.Lookup, legacyPaths.Lookup}} {
		gotText := readFileText(t, pair[0])
		wantText := readFileText(t, pair[1])
		if gotText != wantText {
			t.Fatalf("%s vs %s: got %q, want %q", pair[0], pair[1], gotText, wantText)
		}
	}
	if !stringSlicesEqual(basenames(split.Written), basenames(legacy.Written)) {
		t.Fatalf("written basenames: got %v, want %v", basenames(split.Written), basenames(legacy.Written))
	}
	if len(split.Removed) != 0 {
		t.Fatalf("split.Removed = %v, want empty", split.Removed)
	}
	if len(legacy.Removed) != 0 {
		t.Fatalf("legacy.Removed = %v, want empty", legacy.Removed)
	}
}

// TestBatchPublicationRollsBackOnLaterMemberFailure ports "batch
// publication rolls every member back when a later member commit fails".
func TestBatchPublicationRollsBackOnLaterMemberFailure(t *testing.T) {
	workspace := t.TempDir()
	var diagnostics []string
	options := []TransformArtifactCompileOptions{
		newArtifactOptions(workspace, "sample_first"),
		newArtifactOptions(workspace, "sample_second"),
	}
	for i := range options {
		options[i].OnDiagnostic = func(message string) { diagnostics = append(diagnostics, message) }
	}

	seed, err := CompileTransformArtifactBatch(options)
	if err != nil {
		t.Fatalf("CompileTransformArtifactBatch (seed): %v", err)
	}
	for _, item := range seed {
		writeFileMkdir(t, item.Paths.Config, "old config for "+item.ResourceType+"\n")
		writeFileMkdir(t, item.Paths.StaleConfig, "old stale config for "+item.ResourceType+"\n")
		writeFileMkdir(t, item.Paths.GeneratedBindings, "old bindings for "+item.ResourceType+"\n")
		writeFileMkdir(t, item.Paths.Imports, item.NewImports)
		writeFileMkdir(t, item.Paths.Lookup, "old lookup for "+item.ResourceType+"\n")
	}
	var before []map[string]*string
	for _, item := range seed {
		before = append(before, artifactSnapshot(t, item.Paths))
	}

	compiled, err := CompileTransformArtifactBatch(options)
	if err != nil {
		t.Fatalf("CompileTransformArtifactBatch: %v", err)
	}
	cleanup, err := InstallTransformArtifactBatchCommitHookForTests(func(mutation BatchArtifactMutationRef, phase string) error {
		if mutation.ResourceType == "sample_second" && mutation.Target == compiled[1].Paths.Config {
			return errors.New("injected member-2 commit failure")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("InstallTransformArtifactBatchCommitHookForTests: %v", err)
	}
	_, publishErr := PublishCompiledTransformArtifactBatch(compiled)
	cleanup()
	if publishErr == nil || !strings.Contains(publishErr.Error(), "injected member-2 commit failure") {
		t.Fatalf("got error %v, want a match for 'injected member-2 commit failure'", publishErr)
	}

	for i, item := range seed {
		assertSnapshotsEqual(t, artifactSnapshot(t, item.Paths), before[i])
	}
	if len(diagnostics) != 0 {
		t.Fatalf("diagnostics = %v, want empty (no note() calls until a member fully commits)", diagnostics)
	}
}

// TestRollbackFailurePreservesTransactionBackups ports "rollback failure
// preserves transaction backups for operator recovery".
func TestRollbackFailurePreservesTransactionBackups(t *testing.T) {
	workspace := t.TempDir()
	options := []TransformArtifactCompileOptions{
		newArtifactOptions(workspace, "sample_first"),
		newArtifactOptions(workspace, "sample_second"),
	}
	compiled, err := CompileTransformArtifactBatch(options)
	if err != nil {
		t.Fatalf("CompileTransformArtifactBatch: %v", err)
	}
	for _, item := range compiled {
		writeFileMkdir(t, item.Paths.Config, "old config for "+item.ResourceType+"\n")
		writeFileMkdir(t, item.Paths.Imports, item.NewImports)
	}
	first, second := compiled[0], compiled[1]
	cleanup, err := InstallTransformArtifactBatchCommitHookForTests(func(mutation BatchArtifactMutationRef, phase string) error {
		if phase == "commit" && mutation.Target == second.Paths.Config {
			return errors.New("injected commit failure")
		}
		if phase == "rollback" && mutation.Target == first.Paths.Config {
			return errors.New("injected rollback failure")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("InstallTransformArtifactBatchCommitHookForTests: %v", err)
	}
	_, publishErr := PublishCompiledTransformArtifactBatch(compiled)
	cleanup()
	if publishErr == nil || !strings.Contains(publishErr.Error(), "recovery data preserved") {
		t.Fatalf("got error %v, want a match for 'recovery data preserved'", publishErr)
	}

	parent := filepath.Dir(first.Paths.Config)
	entries, err := os.ReadDir(parent)
	if err != nil {
		t.Fatalf("ReadDir(%s): %v", parent, err)
	}
	var transactionDirs []string
	for _, entry := range entries {
		if entry.IsDir() && strings.HasPrefix(entry.Name(), ".infrawright-artifact-batch-") {
			transactionDirs = append(transactionDirs, filepath.Join(parent, entry.Name()))
		}
	}
	if len(transactionDirs) != 1 {
		t.Fatalf("got %d transaction directories, want 1: %v", len(transactionDirs), transactionDirs)
	}
	recoveryFiles, err := os.ReadDir(transactionDirs[0])
	if err != nil {
		t.Fatalf("ReadDir(%s): %v", transactionDirs[0], err)
	}
	found := false
	for _, f := range recoveryFiles {
		content, err := os.ReadFile(filepath.Join(transactionDirs[0], f.Name()))
		if err != nil {
			t.Fatalf("reading %s: %v", f.Name(), err)
		}
		if string(content) == "old config for sample_first\n" {
			found = true
		}
	}
	if !found {
		t.Fatal("expected the preserved transaction directory to contain sample_first's original config bytes")
	}
}

// TestBatchPublicationRejectsDirectoryTarget ports "batch publication
// rejects a directory target without mutating or deleting it".
func TestBatchPublicationRejectsDirectoryTarget(t *testing.T) {
	workspace := t.TempDir()
	options := []TransformArtifactCompileOptions{
		newArtifactOptions(workspace, "sample_first"),
		newArtifactOptions(workspace, "sample_second"),
	}
	compiled, err := CompileTransformArtifactBatch(options)
	if err != nil {
		t.Fatalf("CompileTransformArtifactBatch: %v", err)
	}
	first, second := compiled[0], compiled[1]
	writeFileMkdir(t, first.Paths.Config, "first artifact before failure\n")
	writeFileMkdir(t, first.Paths.Imports, first.NewImports)
	firstBefore := artifactSnapshot(t, first.Paths)

	if err := os.MkdirAll(second.Paths.Config, 0o777); err != nil {
		t.Fatalf("mkdir %s: %v", second.Paths.Config, err)
	}
	sentinel := filepath.Join(second.Paths.Config, "sentinel.txt")
	writeFileMkdir(t, sentinel, "directory must survive\n")

	_, publishErr := PublishCompiledTransformArtifactBatch(compiled)
	if publishErr == nil || !strings.Contains(publishErr.Error(), "batch target is not a regular file") {
		t.Fatalf("got error %v, want a match for 'batch target is not a regular file'", publishErr)
	}
	assertSnapshotsEqual(t, artifactSnapshot(t, first.Paths), firstBefore)
	if got := readFileText(t, sentinel); got != "directory must survive\n" {
		t.Fatalf("sentinel content = %q, want unchanged", got)
	}
}

// TestSuccessfulBatchPublicationMatchesSequentialWriterBytes ports
// "successful batch publication preserves sequential writer bytes and
// lifecycle".
func TestSuccessfulBatchPublicationMatchesSequentialWriterBytes(t *testing.T) {
	workspace := t.TempDir()
	legacyWorkspace := filepath.Join(workspace, "legacy")
	batchWorkspace := filepath.Join(workspace, "batch")
	resourceTypes := []string{"sample_first", "sample_second"}

	var legacyOptions, batchOptions []TransformArtifactCompileOptions
	for _, rt := range resourceTypes {
		legacyOptions = append(legacyOptions, newArtifactOptions(legacyWorkspace, rt))
		batchOptions = append(batchOptions, newArtifactOptions(batchWorkspace, rt))
	}
	for _, options := range append(append([]TransformArtifactCompileOptions{}, legacyOptions...), batchOptions...) {
		paths := mustComputePaths(t, options)
		writeFileMkdir(t, paths.StaleConfig, "stale opposite-format config\n")
		writeFileMkdir(t, paths.GeneratedBindings, "stale generated bindings\n")
	}

	var legacyResults []TransformArtifactWriteResult
	for _, options := range legacyOptions {
		result, err := WriteTransformArtifacts(options)
		if err != nil {
			t.Fatalf("WriteTransformArtifacts: %v", err)
		}
		legacyResults = append(legacyResults, result)
	}
	compiledBatch, err := CompileTransformArtifactBatch(batchOptions)
	if err != nil {
		t.Fatalf("CompileTransformArtifactBatch: %v", err)
	}
	batchResults, err := PublishCompiledTransformArtifactBatch(compiledBatch)
	if err != nil {
		t.Fatalf("PublishCompiledTransformArtifactBatch: %v", err)
	}

	for i, rt := range resourceTypes {
		legacyPaths := mustComputePaths(t, legacyOptions[i])
		batchPaths := mustComputePaths(t, batchOptions[i])
		assertSnapshotsEqual(t, artifactSnapshot(t, batchPaths), artifactSnapshot(t, legacyPaths))

		gotWritten := relativeToWorkspace(batchResults[i].Written, batchWorkspace)
		wantWritten := relativeToWorkspace(legacyResults[i].Written, legacyWorkspace)
		if !stringSlicesEqual(gotWritten, wantWritten) {
			t.Fatalf("%s: written = %v, want %v", rt, gotWritten, wantWritten)
		}
		gotRemoved := relativeToWorkspace(batchResults[i].Removed, batchWorkspace)
		wantRemoved := relativeToWorkspace(legacyResults[i].Removed, legacyWorkspace)
		if !stringSlicesEqual(gotRemoved, wantRemoved) {
			t.Fatalf("%s: removed = %v, want %v", rt, gotRemoved, wantRemoved)
		}
	}
}

func relativeToWorkspace(paths []string, workspace string) []string {
	out := make([]string, len(paths))
	for i, p := range paths {
		rel, err := filepath.Rel(workspace, p)
		if err != nil {
			rel = p
		}
		out[i] = rel
	}
	return out
}

// TestBatchCompilationUsesNewLookupResultsForBindingsAndComments ports
// "batch compilation uses new lookup results for grouped bindings and HCL
// comments": a stale on-disk lookup sidecar for sample_group must be
// ignored in favor of sample_group's own freshly compiled (same-batch)
// lookup data, both for the referrer's generated bindings sidecar and for
// its HCL tfvars comment.
func TestBatchCompilationUsesNewLookupResultsForBindingsAndComments(t *testing.T) {
	workspace := t.TempDir()
	hclDeployment := testDeployment(workspace, true)
	references := map[string]TransformReferenceSpec{
		"group_id": {NameField: "name", Referent: "sample_group"},
	}
	generated := map[string]bool{"sample_group": true, "sample_item": true}
	resourceRoots := map[string]string{"sample_group": "sample_root", "sample_item": "sample_root"}

	referrer := newArtifactOptions(workspace, "sample_item")
	referrer.Deployment = hclDeployment
	referrer.LookupNameField = nil
	referrer.References = references
	referrer.Result = PullTransformResult{
		Drops:     []string{},
		Items:     map[string]map[string]any{"item": {"group_id": "new-id", "name": "Item"}},
		Originals: map[string]map[string]any{"item": {"id": "item-id", "name": "Item"}},
	}
	referrer.BindingContext = BindingContext{
		Mode:          deployment.ReferenceBindingCrossState,
		Derived:       map[string]bool{},
		Generated:     generated,
		References:    references,
		ResourceRoots: resourceRoots,
	}

	referent := newArtifactOptions(workspace, "sample_group")
	referent.Deployment = hclDeployment
	referent.Result = PullTransformResult{
		Drops:     []string{},
		Items:     map[string]map[string]any{"new_key": {"name": "Fresh Group"}},
		Originals: map[string]map[string]any{"new_key": {"id": "new-id", "name": "Fresh Group"}},
	}
	referent.BindingContext = BindingContext{
		Mode:          deployment.ReferenceBindingCrossState,
		Derived:       map[string]bool{},
		Generated:     generated,
		References:    map[string]TransformReferenceSpec{},
		ResourceRoots: resourceRoots,
	}

	staleLookup := mustComputePaths(t, referent).Lookup
	writeFileMkdir(t, staleLookup, `{"by_id":{"new-id":"Stale Group"},"key_by_id":{"new-id":"stale_key"}}`)

	compiled, err := CompileTransformArtifactBatch([]TransformArtifactCompileOptions{referrer, referent})
	if err != nil {
		t.Fatalf("CompileTransformArtifactBatch: %v", err)
	}
	if _, err := PublishCompiledTransformArtifactBatch(compiled); err != nil {
		t.Fatalf("PublishCompiledTransformArtifactBatch: %v", err)
	}

	referrerPaths := mustComputePaths(t, referrer)
	configText := readFileText(t, referrerPaths.Config)
	if !regexp.MustCompile(`group_id\s+= "new-id"\s+# Fresh Group`).MatchString(configText) {
		t.Fatalf("config text %q does not match /group_id\\s+= \"new-id\"\\s+# Fresh Group/", configText)
	}
	bindingsText := readFileText(t, referrerPaths.GeneratedBindings)
	if !strings.Contains(bindingsText, "data.terraform_remote_state.sample_root.outputs.infrawright_reference_ids.sample_group[\\\"new_key\\\"]") {
		t.Fatalf("generated bindings %q does not contain the fresh-lookup-keyed expression", bindingsText)
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
		"        \"expression\": \"[data.terraform_remote_state.zpa_server_group.outputs.infrawright_reference_ids.zpa_server_group[\\\"server_two\\\"], data.terraform_remote_state.zpa_server_group.outputs.infrawright_reference_ids.zpa_server_group[\\\"server_one\\\"]]\",\n" +
		"        \"reason\": \"cross-state reference binding via zpa_server_group root output\"\n" +
		"      },\n" +
		"      \"server_groups[1].id\": {\n" +
		"        \"expression\": \"[data.terraform_remote_state.zpa_server_group.outputs.infrawright_reference_ids.zpa_server_group[\\\"server_three\\\"]]\",\n" +
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
		"        \"expression\": \"[data.terraform_remote_state.zpa_app.outputs.infrawright_reference_ids.zpa_server_group[\\\"known\\\"], \\\"sg-missing\\\"]\",\n" +
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
		"        \"expression\": \"data.terraform_remote_state.zpa_custom.outputs.infrawright_reference_ids.zpa_segment_group[\\\"segment_one\\\"]\",\n" +
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

	paths, err := ComputeTransformArtifactPaths(dep, "sample_reorder", "tenant")
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
// the flat tenant/resource layout".
func TestComputeTransformArtifactPathsFlatLayout(t *testing.T) {
	got, err := ComputeTransformArtifactPaths(testDeployment("overlay", false), "zia_rule_labels", "tenant")
	if err != nil {
		t.Fatalf("ComputeTransformArtifactPaths: %v", err)
	}
	want := TransformArtifactPaths{
		Config:            path.Join("overlay", "config", "tenant", "zia_rule_labels.auto.tfvars.json"),
		GeneratedBindings: path.Join("overlay", "config", "tenant", "zia_rule_labels.generated.expressions.json"),
		Imports:           path.Join("overlay", "imports", "tenant", "zia_rule_labels_imports.tf"),
		Lookup:            path.Join("overlay", "config", "tenant", "zia_rule_labels.lookup.json"),
		Moves:             path.Join("overlay", "imports", "tenant", "zia_rule_labels_moves.tf"),
		StaleConfig:       path.Join("overlay", "config", "tenant", "zia_rule_labels.auto.tfvars"),
	}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}
