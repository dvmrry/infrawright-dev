package sourcebind

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/dvmrry/infrawright-dev/go/internal/artifacts"
	"github.com/dvmrry/infrawright-dev/go/internal/authoring/contracts"
	"golang.org/x/mod/modfile"
)

func TestLoadVerifiedCapturesCanonicalBoundInputs(t *testing.T) {
	fixture := writeVerifiedFixture(t, false)
	got, err := loadVerified(context.Background(), fixture.roots, loadOptions{gitRunner: fixture.git})
	if err != nil {
		t.Fatalf("loadVerified(valid) error = %v, want nil", err)
	}
	snapshot := requireSnapshot(t, got)
	if snapshot.ManifestSHA256 != digest(snapshot.ManifestBytes) {
		t.Errorf("loadVerified(valid).ManifestSHA256 = %q, want manifest-byte digest", snapshot.ManifestSHA256)
	}
	if snapshot.InputProvenanceSHA256 != digest(snapshot.InputProvenanceBytes) {
		t.Errorf("loadVerified(valid).InputProvenanceSHA256 = %q, want input-provenance-byte digest", snapshot.InputProvenanceSHA256)
	}
	if bytes := string(fileFor(snapshot.Provider, "provider.go").Bytes); bytes != "package provider\n" {
		t.Errorf("loadVerified(valid).Provider provider.go = %q, want captured source", bytes)
	}
	if strings.Contains(string(snapshot.InputProvenanceBytes), fixture.directory) {
		t.Errorf("loadVerified(valid).InputProvenanceBytes leaked local root %q", fixture.directory)
	}
	if fixture.git.calls == 0 {
		t.Fatal("loadVerified(valid) Git calls = 0, want revision verification")
	}
}

func TestLoadVerifiedWithLocalGitRepositories(t *testing.T) {
	fixture := writeVerifiedFixture(t, false)
	providerRevision := commitFixtureRepository(t, fixture.roots.ProviderRoot)
	sdkRoot := fixture.roots.SDKRoots["example.test/sdk"]
	sdkRevision := commitFixtureRepository(t, sdkRoot)
	fixture.manifest.Provider.Revision = providerRevision
	fixture.manifest.SDKs[0].Revision = &sdkRevision
	writeManifest(t, fixture.roots.ManifestPath, fixture.manifest)
	t.Setenv("GIT_DIR", filepath.Join(fixture.directory, "foreign-git-dir"))
	got, err := LoadVerified(context.Background(), fixture.roots)
	if err != nil {
		t.Fatalf("LoadVerified(local Git repositories) error = %v, want nil", err)
	}
	requireSnapshot(t, got)
}

func TestLoadVerifiedDisablesCheckoutControlledGitExecution(t *testing.T) {
	fixture := writeVerifiedFixture(t, false)
	prepareRealGitFixture(t, fixture)
	hook := filepath.Join(fixture.roots.ProviderRoot, ".git", "fsmonitor-hook")
	writeFile(t, hook, "#!/bin/sh\ntouch \"${0}.ran\"\nexit 1\n")
	if err := os.Chmod(hook, 0o700); err != nil {
		t.Fatal(err)
	}
	runFixtureGit(t, fixture.roots.ProviderRoot, "config", "core.fsmonitor", hook)
	if _, err := LoadVerified(context.Background(), fixture.roots); err != nil {
		t.Fatalf("LoadVerified(checkout fsmonitor) error = %v, want nil", err)
	}
	if _, err := os.Stat(hook + ".ran"); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("LoadVerified(checkout fsmonitor) sentinel stat error = %v, want not-exist", err)
	}
}

func TestLoadVerifiedPinsSuppliedGitWorktree(t *testing.T) {
	fixture := writeVerifiedFixture(t, false)
	prepareRealGitFixture(t, fixture)
	foreign := filepath.Join(fixture.directory, "foreign-worktree")
	if err := os.MkdirAll(foreign, 0o700); err != nil {
		t.Fatal(err)
	}
	runFixtureGit(t, fixture.roots.ProviderRoot, "config", "core.worktree", foreign)
	if _, err := LoadVerified(context.Background(), fixture.roots); err != nil {
		t.Fatalf("LoadVerified(checkout core.worktree redirect) error = %v, want nil", err)
	}
}

func TestLoadVerifiedUsesCapturedBytesAfterCheckoutMutation(t *testing.T) {
	fixture := writeVerifiedFixture(t, false)
	providerPath := filepath.Join(fixture.roots.ProviderRoot, "provider.go")
	var reads atomic.Int32
	got, err := loadVerified(context.Background(), fixture.roots, loadOptions{
		gitRunner: fixture.git,
		read: artifacts.StableReadOptions{Hooks: artifacts.StableReadHooks{
			BeforeFinalStat: func() error {
				if reads.Add(1) == 3 { // manifest, provider.go, then provider go.mod.
					return os.WriteFile(providerPath, []byte("package provider\n// changed\n"), 0o600)
				}
				return nil
			},
		}},
	})
	if err != nil {
		t.Fatalf("loadVerified(mutation after provider capture) error = %v, want nil", err)
	}
	snapshot := requireSnapshot(t, got)
	if captured := string(fileFor(snapshot.Provider, "provider.go").Bytes); captured != "package provider\n" {
		t.Errorf("loadVerified(mutation).Provider bytes = %q, want original captured bytes", captured)
	}
	if disk, err := os.ReadFile(providerPath); err != nil || string(disk) == "package provider\n" {
		t.Errorf("mutation fixture disk = %q, %v; want changed path after capture", disk, err)
	}
}

func TestLoadVerifiedOptionalOpenAPIMismatchIsIsolated(t *testing.T) {
	fixture := writeVerifiedFixture(t, true)
	got, err := loadVerified(context.Background(), fixture.roots, loadOptions{gitRunner: fixture.git})
	if err != nil {
		t.Fatalf("loadVerified(OpenAPI mismatch) error = %v, want nil source capture", err)
	}
	snapshot := requireSnapshot(t, got)
	if snapshot.OpenAPI.Available || snapshot.OpenAPI.Err == nil {
		t.Errorf("loadVerified(OpenAPI mismatch).OpenAPI = %#v, want isolated unavailable status", snapshot.OpenAPI)
	}
	if len(snapshot.Provider.Files) == 0 {
		t.Error("loadVerified(OpenAPI mismatch) lost verified core source inputs")
	}
}

func TestLoadVerifiedRejectsBoundMismatchAndSDKRootSet(t *testing.T) {
	fixture := writeVerifiedFixture(t, false)
	fixture.manifest.Provider.Files[0].SHA256 = strings.Repeat("0", 64)
	writeManifest(t, fixture.roots.ManifestPath, fixture.manifest)
	_, err := loadVerified(context.Background(), fixture.roots, loadOptions{gitRunner: fixture.git})
	requireCode(t, err, ErrorBinding)

	fixture = writeVerifiedFixture(t, false)
	delete(fixture.roots.SDKRoots, fixture.manifest.SDKs[0].ModulePath)
	_, err = loadVerified(context.Background(), fixture.roots, loadOptions{gitRunner: fixture.git})
	requireCode(t, err, ErrorInvalidRoots)
}

func TestLoadVerifiedRejectsModuleReplaceTreeAndRevisionDrift(t *testing.T) {
	for _, mutation := range []struct {
		name   string
		mutate func(*verifiedFixture)
		want   ErrorCode
	}{
		{
			name: "module", want: ErrorModule,
			mutate: func(fixture *verifiedFixture) {
				content := "module example.test/wrong\n\ngo 1.26\n\nrequire example.test/sdk v1.2.3\n\nreplace example.test/sdk => ../sdk\n"
				writeFile(t, filepath.Join(fixture.roots.ProviderRoot, "go.mod"), content)
				fixture.manifest.ProviderModule.GoMod.SHA256 = shaText([]byte(content))
				fixture.manifest.Provider.TreeSHA256 = treeDigest([]CapturedFile{{Path: "provider.go", SHA256: shaText([]byte("package provider\n"))}, {Path: "go.mod", SHA256: fixture.manifest.ProviderModule.GoMod.SHA256}})
				writeManifest(t, fixture.roots.ManifestPath, fixture.manifest)
			},
		},
		{
			name: "replace", want: ErrorModule,
			mutate: func(fixture *verifiedFixture) {
				writeFile(t, filepath.Join(fixture.roots.ProviderRoot, "go.mod"), "module example.test/provider\n\ngo 1.26\n\nrequire example.test/sdk v1.2.3\n\nreplace example.test/sdk => ../wrong\n")
				fixture.manifest.ProviderModule.GoMod.SHA256 = shaText(readFile(t, filepath.Join(fixture.roots.ProviderRoot, "go.mod")))
				fixture.manifest.Provider.TreeSHA256 = treeDigest([]CapturedFile{{Path: "provider.go", SHA256: shaText([]byte("package provider\n"))}, {Path: "go.mod", SHA256: fixture.manifest.ProviderModule.GoMod.SHA256}})
				writeManifest(t, fixture.roots.ManifestPath, fixture.manifest)
			},
		},
		{
			name: "tree", want: ErrorBinding,
			mutate: func(fixture *verifiedFixture) {
				fixture.manifest.Provider.TreeSHA256 = strings.Repeat("0", 64)
				writeManifest(t, fixture.roots.ManifestPath, fixture.manifest)
			},
		},
		{
			name: "revision", want: ErrorRevision,
			mutate: func(fixture *verifiedFixture) { fixture.git.revision = "different" },
		},
	} {
		t.Run(mutation.name, func(t *testing.T) {
			fixture := writeVerifiedFixture(t, false)
			mutation.mutate(fixture)
			_, err := loadVerified(context.Background(), fixture.roots, loadOptions{gitRunner: fixture.git})
			requireCode(t, err, mutation.want)
		})
	}
}

func TestVerifyLocalReplacesNormalizesOrderAndRejectsRemote(t *testing.T) {
	expected := []contracts.LocalModuleReplaceBinding{
		{ModulePath: "example.test/alpha", LocalPath: "../alpha"},
		{ModulePath: "example.test/zeta", LocalPath: "../zeta"},
	}
	parsed, err := modfile.Parse("go.mod", []byte("module example.test/provider\n\nreplace example.test/zeta => ../zeta\nreplace example.test/alpha => ../alpha\n"), nil)
	if err != nil {
		t.Fatalf("modfile.Parse(out-of-order local replaces) error = %v", err)
	}
	if err := verifyLocalReplaces(expected, parsed.Replace); err != nil {
		t.Errorf("verifyLocalReplaces(out-of-order local replaces) error = %v, want nil", err)
	}

	remote, err := modfile.Parse("go.mod", []byte("module example.test/provider\n\nreplace example.test/alpha => example.test/fork v1.2.3\n"), nil)
	if err != nil {
		t.Fatalf("modfile.Parse(remote replace) error = %v", err)
	}
	err = verifyLocalReplaces(nil, remote.Replace)
	requireCode(t, err, ErrorModule)
	if !strings.Contains(err.Error(), "only local replacement directories") {
		t.Errorf("verifyLocalReplaces(remote).Error() = %q, want v1 local-only diagnostic", err)
	}
}

func TestLoadVerifiedBindsLocalReplaceToExplicitSDKRoot(t *testing.T) {
	t.Run("mismatched sibling", func(t *testing.T) {
		fixture := writeVerifiedFixture(t, false)
		otherSDK := filepath.Join(fixture.directory, "other-sdk")
		writeFile(t, filepath.Join(otherSDK, "go.mod"), "module example.test/sdk\n\ngo 1.26\n")
		writeFile(t, filepath.Join(otherSDK, "client.go"), "package sdk\n")
		fixture.roots.SDKRoots["example.test/sdk"] = otherSDK
		_, err := loadVerified(context.Background(), fixture.roots, loadOptions{gitRunner: fixture.git})
		requireCode(t, err, ErrorModule)
	})
	t.Run("symlink target", func(t *testing.T) {
		fixture := writeVerifiedFixture(t, false)
		link := filepath.Join(fixture.directory, "sdk-link")
		if err := os.Symlink(fixture.roots.SDKRoots["example.test/sdk"], link); err != nil {
			t.Fatal(err)
		}
		fixture.manifest.ProviderModule.LocalReplaces[0].LocalPath = "../sdk-link"
		writeManifest(t, fixture.roots.ManifestPath, fixture.manifest)
		_, err := loadVerified(context.Background(), fixture.roots, loadOptions{gitRunner: fixture.git})
		requireCode(t, err, ErrorModule)
	})
}

func TestLoadVerifiedRefusesBoundSymlink(t *testing.T) {
	fixture := writeVerifiedFixture(t, false)
	target := filepath.Join(fixture.directory, "outside.go")
	writeFile(t, target, "package provider\n")
	if err := os.Remove(filepath.Join(fixture.roots.ProviderRoot, "provider.go")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(fixture.roots.ProviderRoot, "provider.go")); err != nil {
		t.Fatal(err)
	}
	_, err := loadVerified(context.Background(), fixture.roots, loadOptions{gitRunner: fixture.git})
	requireCode(t, err, ErrorBinding)
}

func TestLoadVerifiedRejectsUntrackedBoundFileAndPostCaptureRevisionChange(t *testing.T) {
	t.Run("untracked", func(t *testing.T) {
		fixture := writeVerifiedFixture(t, false)
		fixture.git.untracked = true
		_, err := loadVerified(context.Background(), fixture.roots, loadOptions{gitRunner: fixture.git})
		requireCode(t, err, ErrorRevision)
	})
	t.Run("post_capture_revision", func(t *testing.T) {
		fixture := writeVerifiedFixture(t, false)
		fixture.git.revisionChangesAfter = 2 // provider+SDK pre-capture rev-parse calls.
		_, err := loadVerified(context.Background(), fixture.roots, loadOptions{gitRunner: fixture.git})
		requireCode(t, err, ErrorRevision)
	})
}

func TestRunGitRejectsOversizedResultAndCancelledContext(t *testing.T) {
	_, err := runGit(context.Background(), "/not-used", []string{"rev-parse"}, loadOptions{
		gitRunner: oversizedGit{}, timeout: defaultGitTimeout,
	})
	requireCode(t, err, ErrorRevision)

	fixture := writeVerifiedFixture(t, false)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = LoadVerified(ctx, fixture.roots)
	requireCode(t, err, ErrorRead)
}

func TestLoadUnverifiedHasNoGitCapabilityOrRootLeak(t *testing.T) {
	directory := t.TempDir()
	providerRoot := filepath.Join(directory, "provider")
	schemaRoot := filepath.Join(directory, "schema")
	sdkRoot := filepath.Join(directory, "sdk")
	writeFile(t, filepath.Join(providerRoot, "provider.go"), "package provider\n")
	writeFile(t, filepath.Join(schemaRoot, "schema.json"), "{}\n")
	writeFile(t, filepath.Join(sdkRoot, "client.go"), "package sdk\n")
	got, err := LoadUnverified(context.Background(), UnverifiedRoots{
		ProviderRoot: providerRoot, ProviderModulePath: "example.test/provider", ProviderFiles: []string{"provider.go"},
		SchemaRoot: schemaRoot, TerraformSchema: "schema.json",
		SDKRoots: map[string]string{"example.test/sdk": sdkRoot}, SDKFiles: map[string][]string{"example.test/sdk": {"client.go"}}, SDKVersions: map[string]string{"example.test/sdk": "v1.2.3"},
		Selection: contracts.SelectionBinding{ResourceTypes: []string{"example_resource"}, Filters: []contracts.SelectionFilterBinding{}},
	})
	if err != nil {
		t.Fatalf("LoadUnverified(valid) error = %v, want nil", err)
	}
	if got.InputProvenance.SourceManifest != nil || got.InputProvenance.SourceManifestSHA256 != nil {
		t.Errorf("LoadUnverified(valid) claimed verified manifest: %#v", got.InputProvenance)
	}
	if strings.Contains(string(got.InputProvenanceBytes), directory) {
		t.Errorf("LoadUnverified(valid).InputProvenanceBytes leaked local root %q", directory)
	}
	if got.InputProvenanceSHA256 != digest(got.InputProvenanceBytes) {
		t.Errorf("LoadUnverified(valid).InputProvenanceSHA256 = %q, want input-provenance-byte digest", got.InputProvenanceSHA256)
	}
}

func TestLoadUnverifiedRedactsRejectedAbsolutePath(t *testing.T) {
	directory := t.TempDir()
	providerRoot := filepath.Join(directory, "provider")
	schemaRoot := filepath.Join(directory, "schema")
	writeFile(t, filepath.Join(providerRoot, "provider.go"), "package provider\n")
	writeFile(t, filepath.Join(schemaRoot, "schema.json"), "{}\n")
	secretPath := filepath.Join(directory, "secret-provider.go")
	_, err := LoadUnverified(context.Background(), UnverifiedRoots{
		ProviderRoot: providerRoot, ProviderModulePath: "example.test/provider", ProviderFiles: []string{secretPath},
		SchemaRoot: schemaRoot, TerraformSchema: "schema.json",
		SDKRoots: map[string]string{}, SDKFiles: map[string][]string{}, SDKVersions: map[string]string{},
		Selection: contracts.SelectionBinding{ResourceTypes: []string{}, Filters: []contracts.SelectionFilterBinding{}},
	})
	requireCode(t, err, ErrorInvalidRoots)
	if strings.Contains(err.Error(), secretPath) || strings.Contains(err.Error(), directory) {
		t.Errorf("LoadUnverified(absolute path).Error() leaked rejected path: %v", err)
	}
}

func TestRequireQualificationAcceptsOnlyLoaderMintedInputs(t *testing.T) {
	if _, err := RequireQualification(VerifiedInputs{}); err == nil {
		t.Fatal("RequireQualification(zero VerifiedInputs) error = nil, want qualification error")
	} else {
		requireCode(t, err, ErrorQualification)
	}
	manual := VerifiedInputs{}
	if _, err := RequireQualification(manual); err == nil {
		t.Fatal("RequireQualification(manual VerifiedInputs) error = nil, want qualification error")
	} else {
		requireCode(t, err, ErrorQualification)
	}
	fixture := writeVerifiedFixture(t, false)
	loaded, err := loadVerified(context.Background(), fixture.roots, loadOptions{gitRunner: fixture.git})
	if err != nil {
		t.Fatalf("loadVerified(valid) error = %v", err)
	}
	qualified, err := RequireQualification(loaded)
	snapshot, snapshotErr := qualified.Snapshot()
	if err != nil || snapshotErr != nil || snapshot.ManifestSHA256 == "" {
		t.Errorf("RequireQualification(loaded) = (%#v, %v), want qualified snapshot and nil", qualified, err)
	}
}

func requireSnapshot(t *testing.T, inputs VerifiedInputs) VerifiedSnapshot {
	t.Helper()
	qualified, err := RequireQualification(inputs)
	if err != nil {
		t.Fatalf("RequireQualification(loader result) error = %v, want nil", err)
	}
	snapshot, err := qualified.Snapshot()
	if err != nil {
		t.Fatalf("QualifiedInputs.Snapshot() error = %v, want nil", err)
	}
	return snapshot
}

type verifiedFixture struct {
	directory string
	roots     LocalRoots
	manifest  contracts.SourceProvenance
	git       *fakeGit
}

func writeVerifiedFixture(t *testing.T, openAPIMismatch bool) *verifiedFixture {
	t.Helper()
	directory := t.TempDir()
	providerRoot := filepath.Join(directory, "provider")
	sdkRoot := filepath.Join(directory, "sdk")
	schemaRoot := filepath.Join(directory, "schema")
	openAPIRoot := filepath.Join(directory, "openapi")
	providerGo := []byte("package provider\n")
	providerMod := []byte("module example.test/provider\n\ngo 1.26\n\nrequire example.test/sdk v1.2.3\n\nreplace example.test/sdk => ../sdk\n")
	sdkMod := []byte("module example.test/sdk\n\ngo 1.26\n")
	sdkGo := []byte("package sdk\n")
	writeFile(t, filepath.Join(providerRoot, "provider.go"), string(providerGo))
	writeFile(t, filepath.Join(providerRoot, "go.mod"), string(providerMod))
	writeFile(t, filepath.Join(sdkRoot, "go.mod"), string(sdkMod))
	writeFile(t, filepath.Join(sdkRoot, "client.go"), string(sdkGo))
	writeFile(t, filepath.Join(schemaRoot, "provider.json"), "{}\n")
	writeFile(t, filepath.Join(openAPIRoot, "openapi.json"), "{}\n")
	revision := "provider-revision"
	sdkRevision := "sdk-revision"
	providerFiles := []contracts.FileBinding{{Path: "provider.go", SHA256: shaText(providerGo)}}
	sdkFiles := []contracts.FileBinding{{Path: "client.go", SHA256: shaText(sdkGo)}, {Path: "go.mod", SHA256: shaText(sdkMod)}}
	providerTree := treeDigest([]CapturedFile{{Path: "provider.go", SHA256: shaText(providerGo)}, {Path: "go.mod", SHA256: shaText(providerMod)}})
	sdkTree := treeDigest([]CapturedFile{{Path: "client.go", SHA256: shaText(sdkGo)}, {Path: "go.mod", SHA256: shaText(sdkMod)}})
	manifest := contracts.SourceProvenance{
		Kind: "infrawright.source_provenance", SchemaVersion: 1,
		Provider:        contracts.ProviderSourceBinding{Repository: "example/provider", ModulePath: "example.test/provider", Revision: revision, TreeSHA256: providerTree, Files: providerFiles},
		ProviderModule:  contracts.ProviderModuleBinding{GoMod: contracts.FileBinding{Path: "go.mod", SHA256: shaText(providerMod)}, LocalReplaces: []contracts.LocalModuleReplaceBinding{{ModulePath: "example.test/sdk", LocalPath: "../sdk"}}},
		TerraformSchema: contracts.FileBinding{Path: "provider.json", SHA256: shaText([]byte("{}\n"))},
		SDKs:            []contracts.SDKSourceBinding{{ModulePath: "example.test/sdk", ModuleVersion: "v1.2.3", Repository: "example/sdk", Revision: &sdkRevision, TreeSHA256: &sdkTree, Files: sdkFiles}},
		Selection:       contracts.SelectionBinding{ResourceTypes: []string{"example_resource"}, Filters: []contracts.SelectionFilterBinding{}},
	}
	if openAPIMismatch {
		manifest.OpenAPI = &contracts.OpenAPIInputBinding{Document: contracts.FileBinding{Path: "openapi.json", SHA256: strings.Repeat("0", 64)}, LocalRefs: []contracts.FileBinding{}}
	}
	manifestPath := filepath.Join(directory, "source-provenance.json")
	writeManifest(t, manifestPath, manifest)
	return &verifiedFixture{
		directory: directory,
		roots:     LocalRoots{ManifestPath: manifestPath, ProviderRoot: providerRoot, SDKRoots: map[string]string{"example.test/sdk": sdkRoot}, SchemaRoot: schemaRoot, OpenAPIRoot: openAPIRoot},
		manifest:  manifest,
		git:       &fakeGit{revision: revision, sdkRevision: sdkRevision},
	}
}

type fakeGit struct {
	revision             string
	sdkRevision          string
	calls                int
	revParses            int
	untracked            bool
	revisionChangesAfter int
}

func (runner *fakeGit) Run(_ context.Context, directory string, arguments []string) (GitResult, error) {
	runner.calls++
	if len(arguments) == 0 {
		return GitResult{}, errors.New("missing Git arguments")
	}
	if arguments[0] == "rev-parse" {
		runner.revParses++
		if runner.revisionChangesAfter > 0 && runner.revParses > runner.revisionChangesAfter {
			return GitResult{Stdout: []byte("changed-revision\n")}, nil
		}
		revision := runner.revision
		if strings.HasSuffix(directory, "sdk") {
			revision = runner.sdkRevision
		}
		return GitResult{Stdout: []byte(revision + "\n")}, nil
	}
	if arguments[0] == "ls-files" && runner.untracked {
		return GitResult{ExitCode: 1}, nil
	}
	if arguments[0] == "status" || arguments[0] == "ls-files" {
		return GitResult{}, nil
	}
	return GitResult{}, errors.New("unexpected Git command")
}

type oversizedGit struct{}

func (oversizedGit) Run(context.Context, string, []string) (GitResult, error) {
	return GitResult{Stdout: make([]byte, 64*1024+1)}, nil
}

func writeManifest(t *testing.T, path string, manifest contracts.SourceProvenance) {
	t.Helper()
	rendered, err := contracts.RenderSourceProvenance(manifest)
	if err != nil {
		t.Fatalf("RenderSourceProvenance(fixture) error = %v", err)
	}
	writeFile(t, path, rendered)
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func commitFixtureRepository(t *testing.T, directory string) string {
	t.Helper()
	for _, arguments := range [][]string{
		{"init", "-q"},
		{"add", "."},
		{"-c", "user.name=sourcebind test", "-c", "user.email=sourcebind@example.test", "commit", "-q", "-m", "fixture"},
	} {
		command := exec.Command("git", arguments...)
		command.Dir = directory
		command.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null")
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git %v error = %v, output = %s", arguments, err, output)
		}
	}
	command := exec.Command("git", "rev-parse", "HEAD")
	command.Dir = directory
	output, err := command.Output()
	if err != nil {
		t.Fatalf("git rev-parse HEAD error = %v", err)
	}
	return strings.TrimSpace(string(output))
}

func prepareRealGitFixture(t *testing.T, fixture *verifiedFixture) {
	t.Helper()
	providerRevision := commitFixtureRepository(t, fixture.roots.ProviderRoot)
	sdkRevision := commitFixtureRepository(t, fixture.roots.SDKRoots["example.test/sdk"])
	fixture.manifest.Provider.Revision = providerRevision
	fixture.manifest.SDKs[0].Revision = &sdkRevision
	writeManifest(t, fixture.roots.ManifestPath, fixture.manifest)
}

func runFixtureGit(t *testing.T, directory string, arguments ...string) {
	t.Helper()
	command := exec.Command("git", arguments...)
	command.Dir = directory
	command.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %v error = %v, output = %s", arguments, err, output)
	}
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	value, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func shaText(value []byte) string {
	sum := sha256.Sum256(value)
	return hex.EncodeToString(sum[:])
}

func requireCode(t *testing.T, err error, want ErrorCode) {
	t.Helper()
	var sourceErr *Error
	if !errors.As(err, &sourceErr) {
		t.Fatalf("error = %T %v, want sourcebind error code %q", err, err, want)
	}
	if sourceErr.Code != want {
		t.Errorf("error code = %q, want %q (%v)", sourceErr.Code, want, err)
	}
}
