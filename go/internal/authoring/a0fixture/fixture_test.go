package a0fixture

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/dvmrry/infrawright-dev/go/internal/authoring/contracts"
	"github.com/dvmrry/infrawright-dev/go/internal/authoring/sourcebind"
)

const (
	fixtureSDKModule        = "example.invalid/sourcefirst-sdk"
	fixtureProviderRevision = "c37dc3c4bdd98adf61862e76d67803469bd5b35d"
	commandTimeout          = 30 * time.Second
)

type materializedFixture struct {
	checkedRoot   string
	temporaryRoot string
	providerRoot  string
	sdkRoot       string
}

type namedArtifact struct {
	name  string
	bytes []byte
}

func TestVerifiedSourceFirstFixtureBinding(t *testing.T) {
	fixture := materializeFixture(t)
	commitProviderFixture(t, fixture.providerRoot)

	manifestBytes := mustReadFile(t, filepath.Join(fixture.checkedRoot, "source-provenance-v1.json"))
	expectedInputBytes := mustReadFile(t, filepath.Join(fixture.checkedRoot, "expected", "input-provenance.json"))
	expectedInput := mustDecodeInputProvenance(t, expectedInputBytes)

	loaded, err := sourcebind.LoadVerified(context.Background(), sourcebind.LocalRoots{
		ManifestPath: filepath.Join(fixture.checkedRoot, "source-provenance-v1.json"),
		ProviderRoot: fixture.providerRoot,
		SDKRoots: map[string]string{
			fixtureSDKModule: fixture.sdkRoot,
		},
		SchemaRoot: fixture.checkedRoot,
	})
	if err != nil {
		t.Fatalf("sourcebind.LoadVerified(source-first-v2) error = %v, want nil", err)
	}
	qualified, err := sourcebind.RequireQualification(loaded)
	if err != nil {
		t.Fatalf("sourcebind.RequireQualification(loader result) error = %v, want nil", err)
	}
	got, err := qualified.Snapshot()
	if err != nil {
		t.Fatalf("sourcebind.QualifiedInputs.Snapshot() error = %v, want nil", err)
	}
	assertExactBytes(t, "LoadVerified.ManifestBytes", got.ManifestBytes, manifestBytes)
	assertExactDigest(t, "LoadVerified.ManifestSHA256", got.ManifestSHA256, manifestBytes)
	if expectedInput.SourceManifestSHA256 == nil {
		t.Fatal("contracts.DecodeInputProvenance(expected input).SourceManifestSHA256 = nil, want manifest digest")
	}
	if got.ManifestSHA256 != *expectedInput.SourceManifestSHA256 {
		t.Errorf(
			"sourcebind.LoadVerified(source-first-v2).ManifestSHA256 = %q, want expected input binding %q",
			got.ManifestSHA256,
			*expectedInput.SourceManifestSHA256,
		)
	}
	assertExactBytes(t, "LoadVerified.InputProvenanceBytes", got.InputProvenanceBytes, expectedInputBytes)
	assertExactDigest(t, "LoadVerified.InputProvenanceSHA256", got.InputProvenanceSHA256, expectedInputBytes)

	artifacts := validateExpectedReportChain(t, fixture.checkedRoot, got.InputProvenance, got.InputProvenanceSHA256)
	artifacts = append(artifacts,
		namedArtifact{name: "source-provenance-v1.json", bytes: got.ManifestBytes},
		namedArtifact{name: "input-provenance.json", bytes: got.InputProvenanceBytes},
	)
	assertNoAbsolutePathLeak(t, artifacts, []string{
		fixture.checkedRoot,
		fixture.temporaryRoot,
		fixture.providerRoot,
		fixture.sdkRoot,
	})
}

func TestVerifiedSourceBindingSnapshotsAreDefensive(t *testing.T) {
	fixture := materializeFixture(t)
	commitProviderFixture(t, fixture.providerRoot)
	loaded, err := sourcebind.LoadVerified(context.Background(), sourcebind.LocalRoots{
		ManifestPath: filepath.Join(fixture.checkedRoot, "source-provenance-v1.json"),
		ProviderRoot: fixture.providerRoot,
		SDKRoots: map[string]string{
			fixtureSDKModule: fixture.sdkRoot,
		},
		SchemaRoot: fixture.checkedRoot,
	})
	if err != nil {
		t.Fatalf("sourcebind.LoadVerified(defensive-copy fixture) error = %v, want nil", err)
	}
	qualified, err := sourcebind.RequireQualification(loaded)
	if err != nil {
		t.Fatalf("sourcebind.RequireQualification(loader result) error = %v, want nil", err)
	}
	want, err := qualified.Snapshot()
	if err != nil {
		t.Fatalf("sourcebind.QualifiedInputs.Snapshot() error = %v, want nil", err)
	}
	mutated, err := qualified.Snapshot()
	if err != nil {
		t.Fatalf("sourcebind.QualifiedInputs.Snapshot() error = %v, want nil", err)
	}
	mutateVerifiedSnapshot(t, &mutated)
	got, err := qualified.Snapshot()
	if err != nil {
		t.Fatalf("sourcebind.QualifiedInputs.Snapshot() error = %v, want nil", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Error("QualifiedInputs.Snapshot() changed after mutating a prior detached snapshot")
	}
	if _, err := sourcebind.RequireQualification(sourcebind.VerifiedInputs{}); err == nil {
		t.Error("sourcebind.RequireQualification(manually constructed zero input) error = nil, want rejection")
	}
	if _, err := (sourcebind.QualifiedInputs{}).Snapshot(); err == nil {
		t.Error("manually constructed QualifiedInputs.Snapshot() error = nil, want rejection")
	}
}

func mutateVerifiedSnapshot(t *testing.T, snapshot *sourcebind.VerifiedSnapshot) {
	t.Helper()
	snapshot.Manifest.Kind = "mutated"
	snapshot.Manifest.Provider.Repository = "mutated"
	snapshot.Manifest.Provider.Files[0].Path = "mutated.go"
	snapshot.Manifest.ProviderModule.LocalReplaces[0].LocalPath = "../mutated"
	snapshot.Manifest.SDKs[0].Files[0].SHA256 = strings.Repeat("0", 64)
	if snapshot.Manifest.SDKs[0].TreeSHA256 != nil {
		*snapshot.Manifest.SDKs[0].TreeSHA256 = strings.Repeat("0", 64)
	}
	snapshot.Manifest.Selection.ResourceTypes[0] = "mutated_resource"
	snapshot.ManifestBytes[0] ^= 0xff
	snapshot.ManifestSHA256 = strings.Repeat("0", 64)
	snapshot.Provider.ModulePath = "example.invalid/mutated"
	snapshot.Provider.Files[0].Bytes[0] ^= 0xff
	snapshot.ProviderModule[0].Bytes[0] ^= 0xff
	for modulePath, tree := range snapshot.SDKs {
		tree.Files[0].Bytes[0] ^= 0xff
		snapshot.SDKs[modulePath] = tree
		delete(snapshot.SDKs, modulePath)
		break
	}
	snapshot.TerraformSchema.Bytes[0] ^= 0xff
	snapshot.OpenAPI.Available = !snapshot.OpenAPI.Available
	snapshot.OpenAPI.Files = append(snapshot.OpenAPI.Files, sourcebind.CapturedFile{Path: "mutated", Bytes: []byte("mutated")})
	snapshot.InputProvenance.Kind = "mutated"
	if snapshot.InputProvenance.SourceManifestSHA256 != nil {
		*snapshot.InputProvenance.SourceManifestSHA256 = strings.Repeat("0", 64)
	}
	if snapshot.InputProvenance.SourceManifest != nil {
		snapshot.InputProvenance.SourceManifest.Provider.Files[0].Path = "mutated.go"
	}
	snapshot.InputProvenanceBytes[0] ^= 0xff
	snapshot.InputProvenanceSHA256 = strings.Repeat("0", 64)
}

func TestSourceFirstFixtureModulesCompileOffline(t *testing.T) {
	fixture := materializeFixture(t)
	environment := offlineGoEnvironment(t, filepath.Join(fixture.temporaryRoot, "offline-go"))
	for _, module := range []struct {
		name string
		root string
	}{
		{name: "sdk", root: fixture.sdkRoot},
		{name: "provider", root: fixture.providerRoot},
	} {
		t.Run(module.name, func(t *testing.T) {
			runCommand(t, module.root, environment, "go", "test", "-count=1", "./...")
		})
	}
}

func validateExpectedReportChain(
	t *testing.T,
	checkedRoot string,
	input contracts.InputProvenance,
	inputSHA256 string,
) []namedArtifact {
	t.Helper()
	expectedRoot := filepath.Join(checkedRoot, "expected")
	sourceBytes := mustReadFile(t, filepath.Join(expectedRoot, "source-evidence-report-v1.json"))
	source, err := contracts.DecodeSourceEvidenceReport(sourceBytes)
	if err != nil {
		t.Fatalf("contracts.DecodeSourceEvidenceReport(checked expected) error = %v, want nil", err)
	}
	if err := contracts.ValidateSourceEvidenceReportAgainstInput(source, input); err != nil {
		t.Fatalf("contracts.ValidateSourceEvidenceReportAgainstInput(checked expected) error = %v, want nil", err)
	}
	if source.InputProvenanceSHA256 != inputSHA256 {
		t.Errorf(
			"checked source report input_provenance_sha256 = %q, want captured input digest %q",
			source.InputProvenanceSHA256,
			inputSHA256,
		)
	}
	canonicalSource, err := contracts.RenderSourceEvidenceReport(source)
	if err != nil {
		t.Fatalf("contracts.RenderSourceEvidenceReport(checked expected) error = %v, want nil", err)
	}
	assertExactBytes(t, "RenderSourceEvidenceReport", []byte(canonicalSource), sourceBytes)

	openAPIBytes := mustReadFile(t, filepath.Join(expectedRoot, "openapi-diagnostics-v1.json"))
	openAPI, err := contracts.DecodeOpenAPIDiagnosticsReport(openAPIBytes, source)
	if err != nil {
		t.Fatalf("contracts.DecodeOpenAPIDiagnosticsReport(checked expected) error = %v, want nil", err)
	}
	assertExactDigest(t, "checked OpenAPI diagnostics source_report_sha256", openAPI.SourceReportSHA256, sourceBytes)
	canonicalOpenAPI, err := contracts.RenderOpenAPIDiagnosticsReport(openAPI, source)
	if err != nil {
		t.Fatalf("contracts.RenderOpenAPIDiagnosticsReport(checked expected) error = %v, want nil", err)
	}
	assertExactBytes(t, "RenderOpenAPIDiagnosticsReport", []byte(canonicalOpenAPI), openAPIBytes)

	return []namedArtifact{
		{name: "source-evidence-report-v1.json", bytes: sourceBytes},
		{name: "openapi-diagnostics-v1.json", bytes: openAPIBytes},
	}
}

func mustDecodeInputProvenance(t *testing.T, data []byte) contracts.InputProvenance {
	t.Helper()
	input, err := contracts.DecodeInputProvenance(data)
	if err != nil {
		t.Fatalf("contracts.DecodeInputProvenance(checked expected) error = %v, want nil", err)
	}
	canonical, err := contracts.RenderInputProvenance(input)
	if err != nil {
		t.Fatalf("contracts.RenderInputProvenance(checked expected) error = %v, want nil", err)
	}
	assertExactBytes(t, "RenderInputProvenance", []byte(canonical), data)
	return input
}

func materializeFixture(t *testing.T) materializedFixture {
	t.Helper()
	checkedRoot := checkedFixtureRoot(t)
	temporaryRoot := t.TempDir()
	providerRoot := filepath.Join(temporaryRoot, "provider")
	sdkRoot := filepath.Join(temporaryRoot, "sdk")
	copyNormalizedTree(t, filepath.Join(checkedRoot, "provider"), providerRoot)
	copyNormalizedTree(t, filepath.Join(checkedRoot, "sdk"), sdkRoot)
	return materializedFixture{
		checkedRoot:   checkedRoot,
		temporaryRoot: temporaryRoot,
		providerRoot:  providerRoot,
		sdkRoot:       sdkRoot,
	}
}

func checkedFixtureRoot(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller(0) failed while locating source-first-v2 fixture")
	}
	root := filepath.Clean(filepath.Join(
		filepath.Dir(filename),
		"..", "..", "..", "..",
		"tests", "fixtures", "authoring", "source-first-v2",
	))
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		t.Fatalf("source-first-v2 fixture root %q is not a directory: %v", root, err)
	}
	return root
}

func copyNormalizedTree(t *testing.T, source, destination string) {
	t.Helper()
	err := fs.WalkDir(os.DirFS(source), ".", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return fmt.Errorf("walk %q: %w", path, walkErr)
		}
		if !fs.ValidPath(path) {
			return fmt.Errorf("fixture path %q is not portable", path)
		}
		target := destination
		if path != "." {
			target = filepath.Join(destination, filepath.FromSlash(path))
		}
		if entry.IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return fmt.Errorf("create directory %q: %w", path, err)
			}
			if err := os.Chmod(target, 0o755); err != nil {
				return fmt.Errorf("normalize directory mode %q: %w", path, err)
			}
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("fixture path %q is a symbolic link", path)
		}
		info, err := entry.Info()
		if err != nil {
			return fmt.Errorf("inspect fixture path %q: %w", path, err)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("fixture path %q is not a regular file", path)
		}
		content, err := os.ReadFile(filepath.Join(source, filepath.FromSlash(path)))
		if err != nil {
			return fmt.Errorf("read fixture path %q: %w", path, err)
		}
		if err := os.WriteFile(target, content, 0o600); err != nil {
			return fmt.Errorf("copy fixture path %q: %w", path, err)
		}
		if err := os.Chmod(target, 0o644); err != nil {
			return fmt.Errorf("normalize file mode %q: %w", path, err)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("copyNormalizedTree(%q) error = %v, want nil", source, err)
	}
}

func commitProviderFixture(t *testing.T, providerRoot string) {
	t.Helper()
	environment := fixtureGitEnvironment()
	runCommand(t, providerRoot, environment, "git", "init", "--quiet")

	entries, err := os.ReadDir(providerRoot)
	if err != nil {
		t.Fatalf("os.ReadDir(provider fixture) error = %v, want nil", err)
	}
	addArguments := []string{"add", "--", "go.mod"}
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".go") {
			addArguments = append(addArguments, entry.Name())
		}
	}
	addArguments = append(addArguments, "internal")
	runCommand(t, providerRoot, environment, "git", addArguments...)
	runCommand(
		t,
		providerRoot,
		environment,
		"git",
		"-c", "core.hooksPath=/dev/null",
		"-c", "commit.gpgsign=false",
		"-c", "user.name=Infrawright Fixture",
		"-c", "user.email=fixtures@infrawright.invalid",
		"commit", "--quiet", "-m", "source-first fixture provider",
	)
	output := runCommand(t, providerRoot, environment, "git", "rev-parse", "HEAD")
	if got := strings.TrimSpace(output); got != fixtureProviderRevision {
		t.Fatalf("deterministic provider fixture revision = %q, want %q", got, fixtureProviderRevision)
	}
}

func fixtureGitEnvironment() []string {
	return []string{
		"PATH=" + os.Getenv("PATH"),
		"LC_ALL=C",
		"TZ=UTC",
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_SYSTEM=/dev/null",
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_CONFIG_COUNT=0",
		"GIT_TERMINAL_PROMPT=0",
		"GIT_ASKPASS=/bin/false",
		"GIT_AUTHOR_NAME=Infrawright Fixture",
		"GIT_AUTHOR_EMAIL=fixtures@infrawright.invalid",
		"GIT_AUTHOR_DATE=2000-01-01T00:00:00 +0000",
		"GIT_COMMITTER_NAME=Infrawright Fixture",
		"GIT_COMMITTER_EMAIL=fixtures@infrawright.invalid",
		"GIT_COMMITTER_DATE=2000-01-01T00:00:00 +0000",
	}
}

func offlineGoEnvironment(t *testing.T, root string) []string {
	t.Helper()
	for _, path := range []string{
		filepath.Join(root, "cache"),
		filepath.Join(root, "module-cache"),
		filepath.Join(root, "gopath"),
		filepath.Join(root, "tmp"),
	} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatalf("os.MkdirAll(offline Go directory) error = %v, want nil", err)
		}
	}
	return []string{
		"PATH=" + os.Getenv("PATH"),
		"LC_ALL=C",
		"TZ=UTC",
		"CGO_ENABLED=0",
		"GOENV=off",
		"GOCACHE=" + filepath.Join(root, "cache"),
		"GOMODCACHE=" + filepath.Join(root, "module-cache"),
		"GOPATH=" + filepath.Join(root, "gopath"),
		"GOPROXY=off",
		"GOSUMDB=off",
		"GOTELEMETRY=off",
		"GOTOOLCHAIN=local",
		"GOTMPDIR=" + filepath.Join(root, "tmp"),
		"GOWORK=off",
	}
}

func runCommand(t *testing.T, directory string, environment []string, name string, arguments ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
	defer cancel()
	command := exec.CommandContext(ctx, name, arguments...)
	command.Dir = directory
	command.Env = append([]string(nil), environment...)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %s in fixture error = %v, output = %q", name, strings.Join(arguments, " "), err, output)
	}
	return string(output)
}

func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("os.ReadFile(%q) error = %v, want nil", path, err)
	}
	return data
}

func assertExactBytes(t *testing.T, label string, got, want []byte) {
	t.Helper()
	if bytes.Equal(got, want) {
		return
	}
	t.Errorf(
		"%s bytes differ: got len=%d sha256=%s, want len=%d sha256=%s",
		label,
		len(got),
		sha256Text(got),
		len(want),
		sha256Text(want),
	)
}

func assertExactDigest(t *testing.T, label, got string, content []byte) {
	t.Helper()
	want := sha256Text(content)
	if got != want {
		t.Errorf("%s = %q, want SHA-256 %q", label, got, want)
	}
}

func assertNoAbsolutePathLeak(t *testing.T, artifacts []namedArtifact, roots []string) {
	t.Helper()
	rootSet := make(map[string]struct{}, len(roots)*2)
	for _, root := range roots {
		for _, candidate := range []string{filepath.Clean(root), filepath.ToSlash(filepath.Clean(root))} {
			if candidate != "." && candidate != string(filepath.Separator) {
				rootSet[candidate] = struct{}{}
			}
		}
	}
	sortedRoots := make([]string, 0, len(rootSet))
	for root := range rootSet {
		sortedRoots = append(sortedRoots, root)
	}
	sort.Strings(sortedRoots)
	for _, artifact := range artifacts {
		for _, root := range sortedRoots {
			if bytes.Contains(artifact.bytes, []byte(root)) {
				t.Errorf("%s contains local absolute root %q, want portable paths only", artifact.name, root)
			}
		}
	}
}

func sha256Text(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
