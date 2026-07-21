package adopt

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/dvmrry/infrawright-dev/go/internal/deployment"
	"github.com/dvmrry/infrawright-dev/go/internal/metadata"
	"github.com/dvmrry/infrawright-dev/go/internal/procerr"
)

type stagingTerraformCall struct {
	Kind    string
	Request ImportStagingTerraformRequest
}

type fakeImportStagingTerraform struct {
	calls  []stagingTerraformCall
	result ImportStagingStateResult
	err    error
}

var _ ImportStagingTerraform = (*fakeImportStagingTerraform)(nil)

func (f *fakeImportStagingTerraform) Initialize(request ImportStagingTerraformRequest) error {
	f.calls = append(f.calls, stagingTerraformCall{Kind: "init", Request: request})
	return f.err
}

func (f *fakeImportStagingTerraform) ListState(request ImportStagingTerraformRequest) (ImportStagingStateResult, error) {
	f.calls = append(f.calls, stagingTerraformCall{Kind: "list", Request: request})
	return f.result, f.err
}

func stagingTestRoot(resourceTypes ...string) metadata.LoadedPackRoot {
	resources := make(map[string]metadata.LoadedResourceMetadata, len(resourceTypes))
	prefixes := map[string]string{"zia_": "zia", "zpa_": "zpa"}
	for _, resourceType := range resourceTypes {
		provider := "zia"
		if strings.HasPrefix(resourceType, "zpa_") {
			provider = "zpa"
		}
		resources[resourceType] = metadata.LoadedResourceMetadata{
			Type:     resourceType,
			Product:  provider,
			Provider: provider,
			Registry: metadata.JsonObject{"generate": true},
		}
	}
	return metadata.LoadedPackRoot{
		Packs:     metadata.PackMetadata{ProviderPrefixes: prefixes},
		Resources: resources,
	}
}

func stagingDeployment(workspace string) deployment.Deployment {
	return deployment.Deployment{Overlay: workspace, Roots: map[string]deployment.RootProviderConfig{}}
}

func writeStagingText(t *testing.T, file, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		t.Fatalf("os.MkdirAll(%q) error: %v", filepath.Dir(file), err)
	}
	if err := os.WriteFile(file, []byte(text), 0o600); err != nil {
		t.Fatalf("os.WriteFile(%q) error: %v", file, err)
	}
}

func readStagingText(t *testing.T, file string) string {
	t.Helper()
	content, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("os.ReadFile(%q) error: %v", file, err)
	}
	return string(content)
}

func requireStagingFailure(t *testing.T, err error, code string) *procerr.ProcessFailure {
	t.Helper()
	var failure *procerr.ProcessFailure
	if !errors.As(err, &failure) {
		t.Fatalf("operation error = %T(%v), want *procerr.ProcessFailure code %q", err, err, code)
	}
	if failure.Code != code {
		t.Errorf("operation ProcessFailure.Code = %q, want %q", failure.Code, code)
	}
	return failure
}

type stateAwareStagingFixture struct {
	destination     string
	environmentRoot string
	filteredText    string
	source          string
	terraform       *fakeImportStagingTerraform
	workspace       string
}

func newStateAwareStagingFixture(t *testing.T) stateAwareStagingFixture {
	t.Helper()
	workspace := t.TempDir()
	sourceText := stagingImports(t, stagingTestResource, "managed", "new")
	source := filepath.Join(workspace, "imports", "tenant", stagingTestResource+"_imports.tf")
	writeStagingText(t, source, sourceText)
	environmentRoot := filepath.Join(workspace, "envs", "tenant", stagingTestResource)
	if err := os.MkdirAll(environmentRoot, 0o700); err != nil {
		t.Fatalf("os.MkdirAll(%q) error: %v", environmentRoot, err)
	}
	managedAddress := stagingImportAddress(t, stagingTestResource, "managed")
	filtered, err := FilterGeneratedImports(sourceText, []string{managedAddress})
	if err != nil {
		t.Fatalf("FilterGeneratedImports(fixture) error: %v", err)
	}
	return stateAwareStagingFixture{
		destination:     filepath.Join(environmentRoot, filepath.Base(source)),
		environmentRoot: environmentRoot,
		filteredText:    filtered.Text,
		source:          source,
		terraform: &fakeImportStagingTerraform{result: ImportStagingStateResult{
			Success: true,
			Stdout:  managedAddress + "\n",
		}},
		workspace: workspace,
	}
}

func (f stateAwareStagingFixture) options() StageImportsOptions {
	return StageImportsOptions{
		Deployment: stagingDeployment(f.workspace),
		Root:       stagingTestRoot(stagingTestResource),
		Selectors:  []string{stagingTestResource},
		StateAware: true,
		Tenant:     "tenant",
		Terraform:  f.terraform,
		Workspace:  f.workspace,
	}
}

func TestStageImportsCopiesExactImportsAndMovesAndReportsMissingRoots(t *testing.T) {
	workspace := t.TempDir()
	dep := stagingDeployment(workspace)
	root := stagingTestRoot(stagingTestResource)
	importsText := stagingImports(t, stagingTestResource, "one")
	movesText := "moved {\n  from = x.old\n  to = x.new\n}\n"
	importsSource := filepath.Join(workspace, "imports", "tenant", stagingTestResource+"_imports.tf")
	movesSource := filepath.Join(workspace, "imports", "tenant", stagingTestResource+"_moves.tf")
	writeStagingText(t, importsSource, importsText)
	writeStagingText(t, movesSource, movesText)

	var diagnostics []string
	missing, err := StageImports(StageImportsOptions{
		Deployment: dep, OnDiagnostic: func(message string) { diagnostics = append(diagnostics, message) },
		Root: root, Selectors: []string{stagingTestResource}, Tenant: "tenant", Workspace: workspace,
	})
	if err != nil {
		t.Fatalf("StageImports(missing root) error: %v", err)
	}
	if missing != (StageImportsResult{Sources: 2, Staged: 0}) {
		t.Errorf("StageImports(missing root) = %#v, want sources=2 staged=0", missing)
	}
	if len(diagnostics) != 3 || !strings.Contains(diagnostics[0], "run make gen-env") ||
		!strings.Contains(diagnostics[1], "run make gen-env") || diagnostics[2] != "NOTE: 0 staged - every import is already managed or no roots matched" {
		t.Errorf("StageImports(missing root) diagnostics = %#v, want two skips then zero-staged note", diagnostics)
	}

	environmentRoot := filepath.Join(workspace, "envs", "tenant", stagingTestResource)
	if err := os.MkdirAll(environmentRoot, 0o700); err != nil {
		t.Fatalf("os.MkdirAll(%q) error: %v", environmentRoot, err)
	}
	existingDestination := filepath.Join(environmentRoot, filepath.Base(importsSource))
	writeStagingText(t, existingDestination, "stale\n")
	if err := os.Chmod(existingDestination, 0o644); err != nil {
		t.Fatalf("os.Chmod(%q) error: %v", existingDestination, err)
	}
	result, err := StageImports(StageImportsOptions{
		Deployment: dep, Root: root, Selectors: []string{stagingTestResource},
		Tenant: "tenant", Workspace: workspace,
	})
	if err != nil {
		t.Fatalf("StageImports(materialized root) error: %v", err)
	}
	if result != (StageImportsResult{Sources: 2, Staged: 2}) {
		t.Errorf("StageImports(materialized root) = %#v, want sources=2 staged=2", result)
	}
	if got := readStagingText(t, filepath.Join(environmentRoot, filepath.Base(importsSource))); got != importsText {
		t.Errorf("staged imports bytes = %q, want %q", got, importsText)
	}
	if got := readStagingText(t, filepath.Join(environmentRoot, filepath.Base(movesSource))); got != movesText {
		t.Errorf("staged moves bytes = %q, want %q", got, movesText)
	}
	stagedInfo, err := os.Stat(existingDestination)
	if err != nil {
		t.Fatalf("os.Stat(staged imports) error: %v", err)
	}
	if got, want := stagedInfo.Mode().Perm(), os.FileMode(0o600); got != want {
		t.Errorf("staged imports mode = %04o, want source mode %04o", got, want)
	}
}

func TestStageImportsCopyFailureNeverPublishesPartialArtifact(t *testing.T) {
	injected := errors.New("injected staging copy failure")
	stages := []struct {
		name  string
		hooks *stagingCopyHooks
	}{
		{name: "after_copy", hooks: &stagingCopyHooks{afterCopy: func() error { return injected }}},
		{name: "after_chmod", hooks: &stagingCopyHooks{afterChmod: func() error { return injected }}},
		{name: "after_close", hooks: &stagingCopyHooks{afterClose: func() error { return injected }}},
	}
	for _, stage := range stages {
		for _, existing := range []bool{false, true} {
			name := stage.name + "_new_destination"
			if existing {
				name = stage.name + "_existing_destination"
			}
			t.Run(name, func(t *testing.T) {
				workspace := t.TempDir()
				source := filepath.Join(workspace, "imports", "tenant", stagingTestResource+"_imports.tf")
				sourceText := stagingImports(t, stagingTestResource, "one")
				writeStagingText(t, source, sourceText)
				environmentRoot := filepath.Join(workspace, "envs", "tenant", stagingTestResource)
				if err := os.MkdirAll(environmentRoot, 0o700); err != nil {
					t.Fatalf("os.MkdirAll(%q) error: %v", environmentRoot, err)
				}
				destination := filepath.Join(environmentRoot, filepath.Base(source))
				const previous = "known-good staged imports\n"
				if existing {
					writeStagingText(t, destination, previous)
					if err := os.Chmod(destination, 0o640); err != nil {
						t.Fatalf("os.Chmod(%q) error: %v", destination, err)
					}
				}

				_, err := StageImports(StageImportsOptions{
					Deployment: stagingDeployment(workspace), Root: stagingTestRoot(stagingTestResource),
					Selectors: []string{stagingTestResource}, Tenant: "tenant", Workspace: workspace,
					copyHooks: stage.hooks,
				})
				if !errors.Is(err, injected) {
					t.Fatalf("StageImports(%s) error = %v, want injected failure", name, err)
				}
				if got := readStagingText(t, source); got != sourceText {
					t.Errorf("StageImports(%s) source bytes = %q, want %q", name, got, sourceText)
				}
				if existing {
					if got := readStagingText(t, destination); got != previous {
						t.Errorf("StageImports(%s) destination bytes = %q, want preserved %q", name, got, previous)
					}
					info, statErr := os.Stat(destination)
					if statErr != nil {
						t.Fatalf("os.Stat(%q) error: %v", destination, statErr)
					}
					if got, want := info.Mode().Perm(), os.FileMode(0o640); got != want {
						t.Errorf("StageImports(%s) destination mode = %04o, want preserved %04o", name, got, want)
					}
				} else if _, statErr := os.Stat(destination); !errors.Is(statErr, os.ErrNotExist) {
					t.Errorf("os.Stat(%q) error = %v, want no new destination", destination, statErr)
				}
				entries, readErr := os.ReadDir(environmentRoot)
				if readErr != nil {
					t.Fatalf("os.ReadDir(%q) error: %v", environmentRoot, readErr)
				}
				wantEntries := 0
				if existing {
					wantEntries = 1
				}
				if len(entries) != wantEntries {
					t.Errorf("StageImports(%s) environment entries = %v, want %d and no transaction remnant", name, entries, wantEntries)
				}
			})
		}
	}
}

func TestStageImportsPostRenameCloseFailureDoesNotReportFalseFailure(t *testing.T) {
	workspace := t.TempDir()
	source := filepath.Join(workspace, "imports", "tenant", stagingTestResource+"_imports.tf")
	destination := filepath.Join(workspace, "envs", "tenant", stagingTestResource, filepath.Base(source))
	sourceText := stagingImports(t, stagingTestResource, "one")
	writeStagingText(t, source, sourceText)
	if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		t.Fatalf("os.MkdirAll(%q) error: %v", filepath.Dir(destination), err)
	}
	injected := errors.New("injected post-rename root close failure")
	closeCalls := 0
	result, err := StageImports(StageImportsOptions{
		Deployment: stagingDeployment(workspace), Root: stagingTestRoot(stagingTestResource),
		Selectors: []string{stagingTestResource}, Tenant: "tenant", Workspace: workspace,
		copyHooks: &stagingCopyHooks{closeAfterRename: func(root *os.Root) error {
			closeCalls++
			if err := root.Close(); err != nil {
				return errors.Join(err, injected)
			}
			return injected
		}},
	})
	if err != nil {
		t.Fatalf("StageImports(post-rename close failure) error = %v, want committed success", err)
	}
	if result != (StageImportsResult{Sources: 1, Staged: 1}) {
		t.Errorf("StageImports(post-rename close failure) = %#v, want sources=1 staged=1", result)
	}
	if closeCalls != 1 {
		t.Errorf("post-rename close calls = %d, want 1", closeCalls)
	}
	if got := readStagingText(t, destination); got != sourceText {
		t.Errorf("committed destination bytes = %q, want %q", got, sourceText)
	}
}

func TestStageImportsRefusesReboundTemporaryArtifact(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink-rebind transaction test is not portable to Windows")
	}
	workspace := t.TempDir()
	source := filepath.Join(workspace, "imports", "tenant", stagingTestResource+"_imports.tf")
	environmentRoot := filepath.Join(workspace, "envs", "tenant", stagingTestResource)
	destination := filepath.Join(environmentRoot, filepath.Base(source))
	victim := filepath.Join(workspace, "outside-victim.tf")
	writeStagingText(t, source, stagingImports(t, stagingTestResource, "one"))
	writeStagingText(t, destination, "known-good destination\n")
	writeStagingText(t, victim, "outside victim\n")

	_, err := StageImports(StageImportsOptions{
		Deployment: stagingDeployment(workspace), Root: stagingTestRoot(stagingTestResource),
		Selectors: []string{stagingTestResource}, Tenant: "tenant", Workspace: workspace,
		copyHooks: &stagingCopyHooks{afterClose: func() error {
			entries, readErr := os.ReadDir(environmentRoot)
			if readErr != nil {
				return readErr
			}
			for _, entry := range entries {
				if strings.HasPrefix(entry.Name(), "."+filepath.Base(destination)+".infrawright-stage-") {
					temporary := filepath.Join(environmentRoot, entry.Name())
					if removeErr := os.Remove(temporary); removeErr != nil {
						return removeErr
					}
					return os.Symlink(victim, temporary)
				}
			}
			return errors.New("staging transaction temporary file not found")
		}},
	})
	if !errors.Is(err, errStagingArtifactIdentityChanged) {
		t.Fatalf("StageImports(rebound temporary) error = %v, want identity-change refusal", err)
	}
	if got := readStagingText(t, destination); got != "known-good destination\n" {
		t.Errorf("rebound destination bytes = %q, want preserved", got)
	}
	if got := readStagingText(t, victim); got != "outside victim\n" {
		t.Errorf("outside victim bytes = %q, want untouched", got)
	}
	entries, readErr := os.ReadDir(environmentRoot)
	if readErr != nil {
		t.Fatalf("os.ReadDir(%q) error: %v", environmentRoot, readErr)
	}
	if len(entries) != 1 || entries[0].Name() != filepath.Base(destination) {
		t.Errorf("environment entries after rebind refusal = %v, want only preserved destination", entries)
	}
}

func TestStageImportsFailsWhenNoSelectedArtifactExists(t *testing.T) {
	workspace := t.TempDir()
	_, err := StageImports(StageImportsOptions{
		Deployment: stagingDeployment(workspace), Root: stagingTestRoot(stagingTestResource),
		Selectors: []string{stagingTestResource}, Tenant: "tenant", Workspace: workspace,
	})
	failure := requireStagingFailure(t, err, "NO_IMPORT_ARTIFACTS")
	if failure.Category != procerr.CategoryDomain || !strings.Contains(failure.Message, "transform or make adopt") {
		t.Errorf("StageImports(no artifacts) failure = %#v, want domain transform/adopt message", failure)
	}
}

func TestStageImportsStateAwareRequiresTerraform(t *testing.T) {
	workspace := t.TempDir()
	writeStagingText(
		t,
		filepath.Join(workspace, "imports", "tenant", stagingTestResource+"_imports.tf"),
		stagingImports(t, stagingTestResource, "one"),
	)
	if err := os.MkdirAll(filepath.Join(workspace, "envs", "tenant", stagingTestResource), 0o700); err != nil {
		t.Fatalf("os.MkdirAll(environment root) error: %v", err)
	}
	_, err := StageImports(StageImportsOptions{
		Deployment: stagingDeployment(workspace), Root: stagingTestRoot(stagingTestResource),
		Selectors: []string{stagingTestResource}, StateAware: true, Tenant: "tenant", Workspace: workspace,
	})
	requireStagingFailure(t, err, "TERRAFORM_REQUIRED")
}

func TestStageImportsStagesOnlySelectedSingletonAndOmitsWholeRootDiagnostic(t *testing.T) {
	workspace := t.TempDir()
	first := "zpa_segment_group"
	second := "zpa_server_group"
	dep := stagingDeployment(workspace)
	writeStagingText(t, filepath.Join(workspace, "imports", "tenant", first+"_imports.tf"), stagingImports(t, first, "segment"))
	writeStagingText(t, filepath.Join(workspace, "imports", "tenant", second+"_moves.tf"), "# server group move\n")
	environmentRoot := filepath.Join(workspace, "envs", "tenant", first)
	if err := os.MkdirAll(environmentRoot, 0o700); err != nil {
		t.Fatalf("os.MkdirAll(%q) error: %v", environmentRoot, err)
	}
	var diagnostics []string
	result, err := StageImports(StageImportsOptions{
		Deployment: dep, OnDiagnostic: func(message string) { diagnostics = append(diagnostics, message) },
		Root: stagingTestRoot(first, second), Selectors: []string{first}, Tenant: "tenant", Workspace: workspace,
	})
	if err != nil {
		t.Fatalf("StageImports(singleton) error: %v", err)
	}
	if result != (StageImportsResult{Sources: 1, Staged: 1}) {
		t.Errorf("StageImports(singleton) = %#v, want sources=1 staged=1", result)
	}
	for _, diagnostic := range diagnostics {
		if strings.Contains(diagnostic, "WHOLE_ROOT_SELECTION") || strings.Contains(diagnostic, "selects whole root") {
			t.Errorf("StageImports(singleton) diagnostic = %q, want no whole-root selection diagnostic", diagnostic)
		}
	}
	if _, err := os.Stat(filepath.Join(environmentRoot, second+"_moves.tf")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("StageImports(singleton) staged unselected %s artifact: os.Stat error = %v, want os.ErrNotExist", second, err)
	}
}

func TestStageImportsStateAwareSnapshotsSelectedSingletonOnce(t *testing.T) {
	workspace := t.TempDir()
	first := "zpa_segment_group"
	dep := stagingDeployment(workspace)
	firstSource := filepath.Join(workspace, "imports", "tenant", first+"_imports.tf")
	firstText := stagingImports(t, first, "managed", "new")
	writeStagingText(t, firstSource, firstText)
	environmentRoot := filepath.Join(workspace, "envs", "tenant", first)
	if err := os.MkdirAll(environmentRoot, 0o700); err != nil {
		t.Fatalf("os.MkdirAll(%q) error = %v, want nil", environmentRoot, err)
	}
	fake := &fakeImportStagingTerraform{result: ImportStagingStateResult{
		Success: true,
		Stdout:  stagingImportAddress(t, first, "managed") + "\n",
	}}

	result, err := StageImports(StageImportsOptions{
		Deployment: dep,
		Root:       stagingTestRoot(first),
		Selectors:  []string{first},
		StateAware: true,
		Tenant:     "tenant",
		Terraform:  fake,
		Workspace:  workspace,
	})
	if err != nil {
		t.Fatalf("StageImports(singleton state-aware) error = %v, want nil", err)
	}
	if result != (StageImportsResult{Sources: 1, Staged: 1}) {
		t.Errorf("StageImports(singleton state-aware) = %#v, want sources=1 staged=1", result)
	}
	if len(fake.calls) != 2 || fake.calls[0].Kind != "init" || fake.calls[1].Kind != "list" {
		t.Fatalf("StageImports(singleton state-aware) Terraform calls = %#v, want one init then one list", fake.calls)
	}
	request := fake.calls[0].Request
	if request.Directory != environmentRoot || request.Label != first || request.Tenant != "tenant" {
		t.Errorf("StageImports(singleton state-aware) Terraform request = %#v, want selected singleton directory/label/tenant", request)
	}
	if fake.calls[1].Request != request {
		t.Errorf("StageImports(singleton state-aware) state-list request = %#v, want init request %#v", fake.calls[1].Request, request)
	}
	want, err := FilterGeneratedImports(firstText, []string{stagingImportAddress(t, first, "managed")})
	if err != nil {
		t.Fatalf("FilterGeneratedImports(%s) error = %v, want nil", first, err)
	}
	destination := filepath.Join(environmentRoot, filepath.Base(firstSource))
	if got := readStagingText(t, destination); got != want.Text {
		t.Errorf("StageImports(singleton state-aware) %s bytes = %q, want %q", first, got, want.Text)
	}
}

func TestStageImportsStateAwareBackendPreflightAndExactFiltering(t *testing.T) {
	workspace := t.TempDir()
	dep := stagingDeployment(workspace)
	root := stagingTestRoot(stagingTestResource)
	text := stagingImports(t, stagingTestResource, "managed", "new")
	source := filepath.Join(workspace, "imports", "tenant", stagingTestResource+"_imports.tf")
	movesSource := filepath.Join(workspace, "imports", "tenant", stagingTestResource+"_moves.tf")
	writeStagingText(t, source, text)
	writeStagingText(t, movesSource, "# moves stay independent\n")
	environmentRoot := filepath.Join(workspace, "envs", "tenant", stagingTestResource)
	writeStagingText(t, filepath.Join(environmentRoot, "main.tf"), "terraform {\r  backend \"azurerm\" {}\r}\r")
	fake := &fakeImportStagingTerraform{result: ImportStagingStateResult{
		Success: true,
		Stdout:  stagingImportAddress(t, stagingTestResource, "managed") + "\n",
	}}

	_, err := StageImports(StageImportsOptions{
		Deployment: dep, Root: root, Selectors: []string{stagingTestResource}, StateAware: true,
		Tenant: "tenant", Terraform: fake, Workspace: workspace,
	})
	requireStagingFailure(t, err, "BACKEND_CONFIG_REQUIRED")
	if len(fake.calls) != 0 {
		t.Errorf("StageImports(missing backend config) Terraform calls = %#v, want none", fake.calls)
	}

	backendRelative := "backend.hcl"
	writeStagingText(t, filepath.Join(workspace, backendRelative), "storage_account_name = \"example\"\n")
	var diagnostics []string
	result, err := StageImports(StageImportsOptions{
		BackendConfig: &backendRelative, Deployment: dep,
		OnDiagnostic: func(message string) { diagnostics = append(diagnostics, message) },
		Root:         root, Selectors: []string{stagingTestResource}, StateAware: true,
		Tenant: "tenant", Terraform: fake, Workspace: workspace,
	})
	if err != nil {
		t.Fatalf("StageImports(state-aware) error: %v", err)
	}
	if result != (StageImportsResult{Sources: 2, Staged: 2}) {
		t.Errorf("StageImports(state-aware) = %#v, want sources=2 staged=2", result)
	}
	if len(fake.calls) != 2 || fake.calls[0].Kind != "init" || fake.calls[1].Kind != "list" {
		t.Fatalf("StageImports(state-aware) Terraform calls = %#v, want init then list", fake.calls)
	}
	request := fake.calls[0].Request
	if request.BackendConfig == nil || *request.BackendConfig != filepath.Join(workspace, backendRelative) ||
		request.Directory != environmentRoot || request.Label != stagingTestResource || request.Tenant != "tenant" {
		t.Errorf("StageImports(state-aware) request = %#v, want resolved backend/root/label/tenant", request)
	}
	filtered, err := FilterGeneratedImports(text, []string{stagingImportAddress(t, stagingTestResource, "managed")})
	if err != nil {
		t.Fatalf("FilterGeneratedImports(expected staged bytes) error: %v", err)
	}
	if got := readStagingText(t, filepath.Join(environmentRoot, filepath.Base(source))); got != filtered.Text {
		t.Errorf("state-aware staged imports = %q, want %q", got, filtered.Text)
	}
	if got := readStagingText(t, filepath.Join(environmentRoot, filepath.Base(movesSource))); got != "# moves stay independent\n" {
		t.Errorf("state-aware staged moves = %q, want exact source", got)
	}
	wantDiagnostics := []string{
		"1 import(s) kept, 1 already managed (skipped)",
		"staged " + filepath.Join(environmentRoot, filepath.Base(source)),
		"staged " + filepath.Join(environmentRoot, filepath.Base(movesSource)),
	}
	if len(diagnostics) != len(wantDiagnostics) {
		t.Fatalf("StageImports(state-aware) diagnostics length = %d, want %d (%#v)", len(diagnostics), len(wantDiagnostics), diagnostics)
	}
	for index := range wantDiagnostics {
		if diagnostics[index] != wantDiagnostics[index] {
			t.Errorf("StageImports(state-aware) diagnostic[%d] = %q, want %q", index, diagnostics[index], wantDiagnostics[index])
		}
	}
}

func TestStageImportsStateAwarePublicationDoesNotFollowDestinationSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink publication test is not portable to Windows")
	}
	fixture := newStateAwareStagingFixture(t)
	victim := filepath.Join(fixture.workspace, "outside-victim.tf")
	const victimText = "outside victim must remain unchanged\n"
	writeStagingText(t, victim, victimText)
	if err := os.Symlink(victim, fixture.destination); err != nil {
		t.Fatalf("os.Symlink(%q, %q) error: %v", victim, fixture.destination, err)
	}

	result, err := StageImports(fixture.options())
	if err != nil {
		t.Fatalf("StageImports(state-aware symlink destination) error: %v", err)
	}
	if result != (StageImportsResult{Sources: 1, Staged: 1}) {
		t.Errorf("StageImports(state-aware symlink destination) = %#v, want sources=1 staged=1", result)
	}
	if got := readStagingText(t, victim); got != victimText {
		t.Errorf("outside victim bytes = %q, want untouched %q", got, victimText)
	}
	if got := readStagingText(t, fixture.destination); got != fixture.filteredText {
		t.Errorf("state-aware destination bytes = %q, want filtered %q", got, fixture.filteredText)
	}
	destinationInfo, err := os.Lstat(fixture.destination)
	if err != nil {
		t.Fatalf("os.Lstat(%q) error: %v", fixture.destination, err)
	}
	if destinationInfo.Mode()&os.ModeSymlink != 0 {
		t.Errorf("state-aware destination mode = %v, want symlink replaced by regular file", destinationInfo.Mode())
	}
}

func TestStageImportsStateAwarePublicationBreaksDestinationHardLink(t *testing.T) {
	fixture := newStateAwareStagingFixture(t)
	victim := filepath.Join(fixture.workspace, "outside-hard-link-victim.tf")
	const victimText = "hard-linked victim must remain unchanged\n"
	writeStagingText(t, victim, victimText)
	if err := os.Link(victim, fixture.destination); err != nil {
		t.Fatalf("os.Link(%q, %q) error: %v", victim, fixture.destination, err)
	}

	result, err := StageImports(fixture.options())
	if err != nil {
		t.Fatalf("StageImports(state-aware hard-link destination) error: %v", err)
	}
	if result != (StageImportsResult{Sources: 1, Staged: 1}) {
		t.Errorf("StageImports(state-aware hard-link destination) = %#v, want sources=1 staged=1", result)
	}
	if got := readStagingText(t, victim); got != victimText {
		t.Errorf("hard-linked victim bytes = %q, want untouched %q", got, victimText)
	}
	if got := readStagingText(t, fixture.destination); got != fixture.filteredText {
		t.Errorf("state-aware destination bytes = %q, want filtered %q", got, fixture.filteredText)
	}
	victimInfo, err := os.Stat(victim)
	if err != nil {
		t.Fatalf("os.Stat(%q) error: %v", victim, err)
	}
	destinationInfo, err := os.Stat(fixture.destination)
	if err != nil {
		t.Fatalf("os.Stat(%q) error: %v", fixture.destination, err)
	}
	if os.SameFile(victimInfo, destinationInfo) {
		t.Error("state-aware destination still aliases the outside hard-link victim after publication")
	}
}

func TestStageImportsStateAwarePreRenameFailurePreservesDestination(t *testing.T) {
	fixture := newStateAwareStagingFixture(t)
	const previous = "known-good state-aware destination\n"
	writeStagingText(t, fixture.destination, previous)
	if err := os.Chmod(fixture.destination, 0o640); err != nil {
		t.Fatalf("os.Chmod(%q) error: %v", fixture.destination, err)
	}
	injected := errors.New("injected state-aware pre-rename failure")
	options := fixture.options()
	options.copyHooks = &stagingCopyHooks{afterClose: func() error { return injected }}

	_, err := StageImports(options)
	if !errors.Is(err, injected) {
		t.Fatalf("StageImports(state-aware pre-rename failure) error = %v, want injected failure", err)
	}
	if got := readStagingText(t, fixture.destination); got != previous {
		t.Errorf("state-aware destination bytes = %q, want preserved %q", got, previous)
	}
	destinationInfo, err := os.Stat(fixture.destination)
	if err != nil {
		t.Fatalf("os.Stat(%q) error: %v", fixture.destination, err)
	}
	if got, want := destinationInfo.Mode().Perm(), os.FileMode(0o640); got != want {
		t.Errorf("state-aware destination mode = %04o, want preserved %04o", got, want)
	}
	entries, err := os.ReadDir(fixture.environmentRoot)
	if err != nil {
		t.Fatalf("os.ReadDir(%q) error: %v", fixture.environmentRoot, err)
	}
	if len(entries) != 1 || entries[0].Name() != filepath.Base(fixture.destination) {
		t.Errorf("state-aware environment entries = %v, want only preserved destination and no transaction remnant", entries)
	}
}

func TestStageImportsStateAwarePublicationPreservesSourceMode(t *testing.T) {
	fixture := newStateAwareStagingFixture(t)
	if err := os.Chmod(fixture.source, 0o600); err != nil {
		t.Fatalf("os.Chmod(%q) error: %v", fixture.source, err)
	}
	writeStagingText(t, fixture.destination, "stale destination\n")
	if err := os.Chmod(fixture.destination, 0o644); err != nil {
		t.Fatalf("os.Chmod(%q) error: %v", fixture.destination, err)
	}

	result, err := StageImports(fixture.options())
	if err != nil {
		t.Fatalf("StageImports(state-aware source mode) error: %v", err)
	}
	if result != (StageImportsResult{Sources: 1, Staged: 1}) {
		t.Errorf("StageImports(state-aware source mode) = %#v, want sources=1 staged=1", result)
	}
	destinationInfo, err := os.Stat(fixture.destination)
	if err != nil {
		t.Fatalf("os.Stat(%q) error: %v", fixture.destination, err)
	}
	if got, want := destinationInfo.Mode().Perm(), os.FileMode(0o600); got != want {
		t.Errorf("state-aware destination mode = %04o, want source mode %04o", got, want)
	}
	if got := readStagingText(t, fixture.destination); got != fixture.filteredText {
		t.Errorf("state-aware destination bytes = %q, want filtered %q", got, fixture.filteredText)
	}
}

func TestStageImportsEmptyDeltaAndFailedStateList(t *testing.T) {
	workspace := t.TempDir()
	dep := stagingDeployment(workspace)
	root := stagingTestRoot(stagingTestResource)
	text := stagingImports(t, stagingTestResource, "one")
	source := filepath.Join(workspace, "imports", "tenant", stagingTestResource+"_imports.tf")
	destination := filepath.Join(workspace, "envs", "tenant", stagingTestResource, stagingTestResource+"_imports.tf")
	writeStagingText(t, source, text)
	writeStagingText(t, destination, "stale\n")
	allManaged := &fakeImportStagingTerraform{result: ImportStagingStateResult{
		Success: true, Stdout: stagingImportAddress(t, stagingTestResource, "one") + "\n",
	}}
	var diagnostics []string
	result, err := StageImports(StageImportsOptions{
		Deployment: dep, OnDiagnostic: func(message string) { diagnostics = append(diagnostics, message) },
		Root: root, Selectors: []string{stagingTestResource}, StateAware: true,
		Tenant: "tenant", Terraform: allManaged, Workspace: workspace,
	})
	if err != nil {
		t.Fatalf("StageImports(all managed) error: %v", err)
	}
	if result != (StageImportsResult{Sources: 1, Staged: 0}) {
		t.Errorf("StageImports(all managed) = %#v, want sources=1 staged=0", result)
	}
	if _, err := os.Stat(destination); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("os.Stat(%q) error = %v, want not-exist after empty delta", destination, err)
	}
	if len(diagnostics) != 2 || !strings.Contains(diagnostics[0], "delta is empty") || !strings.HasPrefix(diagnostics[1], "NOTE: 0 staged") {
		t.Errorf("StageImports(all managed) diagnostics = %#v, want empty-delta then zero-staged note", diagnostics)
	}

	noState := &fakeImportStagingTerraform{result: ImportStagingStateResult{Success: false, Stdout: "ignored"}}
	result, err = StageImports(StageImportsOptions{
		Deployment: dep, Root: root, Selectors: []string{stagingTestResource}, StateAware: true,
		Tenant: "tenant", Terraform: noState, Workspace: workspace,
	})
	if err != nil {
		t.Fatalf("StageImports(failed state list) error: %v", err)
	}
	if result != (StageImportsResult{Sources: 1, Staged: 1}) {
		t.Errorf("StageImports(failed state list) = %#v, want sources=1 staged=1", result)
	}
	if got := readStagingText(t, destination); got != text {
		t.Errorf("failed-state staged imports = %q, want full source %q", got, text)
	}
}

func TestStageImportsPythonNewlineAndBOMContracts(t *testing.T) {
	canonical := stagingImports(t, stagingTestResource, "managed", "new")
	filtered, err := FilterGeneratedImports(canonical, []string{stagingImportAddress(t, stagingTestResource, "managed")})
	if err != nil {
		t.Fatalf("FilterGeneratedImports(canonical) error: %v", err)
	}
	tests := []struct {
		name string
		text string
		want string
	}{
		{name: "cr", text: strings.ReplaceAll(canonical, "\n", "\r"), want: filtered.Text},
		{name: "crlf", text: strings.Replace(canonical, "\n", "\r\n", 1), want: filtered.Text},
		{name: "bom", text: "\ufeff" + canonical, want: "\ufeff" + canonical},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			workspace := t.TempDir()
			dep := stagingDeployment(workspace)
			source := filepath.Join(workspace, "imports", "tenant", stagingTestResource+"_imports.tf")
			destination := filepath.Join(workspace, "envs", "tenant", stagingTestResource, filepath.Base(source))
			writeStagingText(t, source, test.text)
			if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
				t.Fatalf("os.MkdirAll(%q) error: %v", filepath.Dir(destination), err)
			}
			fake := &fakeImportStagingTerraform{result: ImportStagingStateResult{
				Success: true, Stdout: stagingImportAddress(t, stagingTestResource, "managed") + "\n",
			}}
			_, err := StageImports(StageImportsOptions{
				Deployment: dep, Root: stagingTestRoot(stagingTestResource), Selectors: []string{stagingTestResource},
				StateAware: true, Tenant: "tenant", Terraform: fake, Workspace: workspace,
			})
			if err != nil {
				t.Fatalf("StageImports(%s source) error: %v", test.name, err)
			}
			if got := readStagingText(t, destination); got != test.want {
				t.Errorf("StageImports(%s source) bytes = %q, want %q", test.name, got, test.want)
			}
			if got := readStagingText(t, source); got != test.text {
				t.Errorf("StageImports(%s source) changed source bytes to %q, want %q", test.name, got, test.text)
			}
		})
	}
}

func TestUnstageImportsRemovesSelectedCopiesOnly(t *testing.T) {
	workspace := t.TempDir()
	first := "zpa_segment_group"
	second := "zpa_server_group"
	dep := stagingDeployment(workspace)
	environmentRoot := filepath.Join(workspace, "envs", "tenant", first)
	secondEnvironmentRoot := filepath.Join(workspace, "envs", "tenant", second)
	source := filepath.Join(workspace, "imports", "tenant", first+"_imports.tf")
	writeStagingText(t, source, "source\n")
	writeStagingText(t, filepath.Join(environmentRoot, first+"_imports.tf"), "staged\n")
	writeStagingText(t, filepath.Join(environmentRoot, first+"_moves.tf"), "staged\n")
	writeStagingText(t, filepath.Join(secondEnvironmentRoot, second+"_imports.tf"), "staged\n")
	writeStagingText(t, filepath.Join(secondEnvironmentRoot, second+"_moves.tf"), "staged\n")
	writeStagingText(t, filepath.Join(environmentRoot, "main.tf"), "keep\n")
	var diagnostics []string
	result, err := UnstageImports(UnstageImportsOptions{
		Deployment: dep, OnDiagnostic: func(message string) { diagnostics = append(diagnostics, message) },
		Root: stagingTestRoot(first, second), Selectors: []string{first}, Tenant: "tenant", Workspace: workspace,
	})
	if err != nil {
		t.Fatalf("UnstageImports(singleton) error: %v", err)
	}
	if result != (UnstageImportsResult{Removed: 2}) {
		t.Errorf("UnstageImports(singleton) = %#v, want removed=2", result)
	}
	for _, diagnostic := range diagnostics {
		if strings.Contains(diagnostic, "WHOLE_ROOT_SELECTION") || strings.Contains(diagnostic, "selects whole root") {
			t.Errorf("UnstageImports(singleton) diagnostic = %q, want no whole-root selection diagnostic", diagnostic)
		}
	}
	if got := readStagingText(t, source); got != "source\n" {
		t.Errorf("UnstageImports source bytes = %q, want preserved source", got)
	}
	if got := readStagingText(t, filepath.Join(environmentRoot, "main.tf")); got != "keep\n" {
		t.Errorf("UnstageImports main.tf bytes = %q, want preserved root file", got)
	}
	for _, name := range []string{second + "_imports.tf", second + "_moves.tf"} {
		if got := readStagingText(t, filepath.Join(secondEnvironmentRoot, name)); got != "staged\n" {
			t.Errorf("UnstageImports selected singleton changed unselected %s bytes = %q, want preserved staged bytes", name, got)
		}
	}
	result, err = UnstageImports(UnstageImportsOptions{
		Deployment: dep, Root: stagingTestRoot(first, second), Selectors: []string{first},
		Tenant: "tenant", Workspace: workspace,
	})
	if err != nil {
		t.Fatalf("UnstageImports(second run) error: %v", err)
	}
	if result != (UnstageImportsResult{Removed: 0}) {
		t.Errorf("UnstageImports(second run) = %#v, want removed=0", result)
	}
}

func TestImportStagingTerraformAdapterUsesExactArgvAndSnapshotsEnvironment(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Terraform execution is intentionally unsupported on Windows")
	}
	workspace := t.TempDir()
	executable := filepath.Join(workspace, "terraform-fake")
	logPath := filepath.Join(workspace, "terraform.log")
	writeStagingText(t, executable, strings.Join([]string{
		"#!/bin/sh",
		"printf '%s|%s|%s\\n' \"$PWD\" \"$*\" \"$SNAPSHOT_VALUE\" >> \"$FAKE_TF_LOG\"",
		"if [ \"$1 $2\" = \"state list\" ]; then exit \"${FAKE_STATE_STATUS:-0}\"; fi",
		"exit 0",
		"",
	}, "\n"))
	if err := os.Chmod(executable, 0o700); err != nil {
		t.Fatalf("os.Chmod(%q) error: %v", executable, err)
	}
	directory := filepath.Join(workspace, "root")
	if err := os.Mkdir(directory, 0o700); err != nil {
		t.Fatalf("os.Mkdir(%q) error: %v", directory, err)
	}
	environment := map[string]string{
		"FAKE_STATE_STATUS": "1",
		"FAKE_TF_LOG":       logPath,
		"SNAPSHOT_VALUE":    "before",
	}
	adapter := CreateImportStagingTerraform(ImportStagingTerraformOptions{
		Environment: environment, TerraformExecutable: executable,
	})
	environment["SNAPSHOT_VALUE"] = "after"
	backend := filepath.Join(workspace, "backend.hcl")
	request := ImportStagingTerraformRequest{
		BackendConfig: &backend, Directory: directory, Label: "grouped", Tenant: "tenant",
	}
	if err := adapter.Initialize(request); err != nil {
		t.Fatalf("ImportStagingTerraform.Initialize(%#v) error: %v", request, err)
	}
	state, err := adapter.ListState(request)
	if err != nil {
		t.Fatalf("ImportStagingTerraform.ListState(%#v) error: %v", request, err)
	}
	if state != (ImportStagingStateResult{Success: false, Stdout: ""}) {
		t.Errorf("ImportStagingTerraform.ListState(%#v) = %#v, want failed empty result", request, state)
	}
	logText := readStagingText(t, logPath)
	if !strings.Contains(logText, "init -input=false -reconfigure") ||
		!strings.Contains(logText, "-backend-config=key=tenant/grouped.tfstate") ||
		!strings.Contains(logText, "state list") || strings.Contains(logText, "|after") || strings.Count(logText, "|before") != 2 {
		t.Errorf("Terraform adapter log = %q, want exact init/state argv and snapshotted environment", logText)
	}
}

func TestDecodeTerraformStateListRejectsInvalidUTF8AndPreservesBOM(t *testing.T) {
	_, err := decodeTerraformStateList([]byte{0xff})
	requireStagingFailure(t, err, "INVALID_TERRAFORM_STATE_LIST")
	got, err := decodeTerraformStateList([]byte("\ufeffaddress\n"))
	if err != nil {
		t.Fatalf("decodeTerraformStateList(BOM) error: %v", err)
	}
	if got != "\ufeffaddress\n" {
		t.Errorf("decodeTerraformStateList(BOM) = %q, want BOM retained", got)
	}
}
