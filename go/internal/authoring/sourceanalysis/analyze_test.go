package sourceanalysis

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/dvmrry/infrawright-dev/go/internal/authoring/contracts"
	"github.com/dvmrry/infrawright-dev/go/internal/authoring/sourcebind"
)

const fixtureSDKModule = "example.invalid/sourcefirst-sdk"

func TestAnalyzeSyntheticFixtureMatchesReviewedAuthority(t *testing.T) {
	checked := fixtureRoot(t)
	temporary := t.TempDir()
	provider := filepath.Join(temporary, "provider")
	sdk := filepath.Join(temporary, "sdk")
	copyTree(t, filepath.Join(checked, "provider"), provider)
	copyTree(t, filepath.Join(checked, "sdk"), sdk)
	run(t, provider, "git", "init", "-q")
	run(t, provider, "git", "add", ".")
	run(t, provider, "git", "-c", "core.hooksPath=/dev/null", "-c", "commit.gpgsign=false", "-c", "user.name=Infrawright Fixture", "-c", "user.email=fixtures@infrawright.invalid", "commit", "-qm", "source-first fixture provider")

	loaded, err := sourcebind.LoadVerified(context.Background(), sourcebind.LocalRoots{
		ManifestPath: filepath.Join(checked, "source-provenance-v1.json"),
		ProviderRoot: provider,
		SDKRoots:     map[string]string{fixtureSDKModule: sdk},
		SchemaRoot:   checked,
	})
	if err != nil {
		t.Fatalf("sourcebind.LoadVerified() error = %v, want nil", err)
	}
	inputs, err := sourcebind.RequireQualification(loaded)
	if err != nil {
		t.Fatalf("sourcebind.RequireQualification() error = %v, want nil", err)
	}
	got, err := Analyze(context.Background(), inputs)
	if err != nil {
		t.Fatalf("Analyze() error = %v, want nil", err)
	}
	report, err := got.Snapshot()
	if err != nil {
		t.Fatalf("Analyze().Snapshot() error = %v, want nil", err)
	}
	if row := report.Resources["sourcefirst_dynamic"]; row.Classification != contracts.SourceDynamic || len(row.Chains) != 1 {
		t.Errorf("sourcefirst_dynamic = %#v, want one dynamic raw-request chain without an independent local helper", row)
	}
	if row := report.Resources["sourcefirst_sdk_http"]; row.Classification != contracts.SourceObservedHTTP || len(row.Chains) != 1 {
		t.Errorf("sourcefirst_sdk_http = %#v, want one observed HTTP chain without an independent SDK constructor", row)
	}
	gotBytes, err := got.CanonicalBytes()
	if err != nil {
		t.Fatalf("QualifiedEvidence.CanonicalBytes() error = %v, want nil", err)
	}
	want := mustRead(t, filepath.Join(checked, "expected", "source-evidence-report-v1.json"))
	if !bytes.Equal(gotBytes, want) {
		t.Errorf("Analyze() bytes differ from independently reviewed authority\n got: %s\nwant: %s", gotBytes, want)
	}
	if _, err := got.Snapshot(); err != nil {
		t.Errorf("QualifiedEvidence.Snapshot() error = %v, want nil", err)
	}
}

func TestQualifiedEvidenceRejectsZeroValue(t *testing.T) {
	var zero QualifiedEvidence
	if _, err := zero.CanonicalBytes(); err == nil {
		t.Error("QualifiedEvidence{}.CanonicalBytes() error = nil, want rejection")
	}
	if _, err := zero.SHA256(); err == nil {
		t.Error("QualifiedEvidence{}.SHA256() error = nil, want rejection")
	}
	if _, err := zero.Snapshot(); err == nil {
		t.Error("QualifiedEvidence{}.Snapshot() error = nil, want rejection")
	}
}

func TestAnalyzeRejectsZeroQualifiedInputs(t *testing.T) {
	if _, err := Analyze(context.Background(), sourcebind.QualifiedInputs{}); err == nil {
		t.Error("Analyze(zero QualifiedInputs) error = nil, want rejection")
	}
}

func TestAnalyzeUsesOnlyCapturedBytesAfterRootsDisappear(t *testing.T) {
	checked := fixtureRoot(t)
	temporary := t.TempDir()
	provider := filepath.Join(temporary, "provider")
	sdk := filepath.Join(temporary, "sdk")
	copyTree(t, filepath.Join(checked, "provider"), provider)
	copyTree(t, filepath.Join(checked, "sdk"), sdk)
	run(t, provider, "git", "init", "-q")
	run(t, provider, "git", "add", ".")
	run(t, provider, "git", "-c", "core.hooksPath=/dev/null", "-c", "commit.gpgsign=false", "-c", "user.name=Infrawright Fixture", "-c", "user.email=fixtures@infrawright.invalid", "commit", "-qm", "source-first fixture provider")
	loaded, err := sourcebind.LoadVerified(context.Background(), sourcebind.LocalRoots{ManifestPath: filepath.Join(checked, "source-provenance-v1.json"), ProviderRoot: provider, SDKRoots: map[string]string{fixtureSDKModule: sdk}, SchemaRoot: checked})
	if err != nil {
		t.Fatalf("sourcebind.LoadVerified() error = %v, want nil", err)
	}
	inputs, err := sourcebind.RequireQualification(loaded)
	if err != nil {
		t.Fatalf("sourcebind.RequireQualification() error = %v, want nil", err)
	}
	if err := os.RemoveAll(provider); err != nil {
		t.Fatalf("os.RemoveAll(provider test root) error = %v", err)
	}
	if err := os.RemoveAll(sdk); err != nil {
		t.Fatalf("os.RemoveAll(sdk test root) error = %v", err)
	}
	if _, err := Analyze(context.Background(), inputs); err != nil {
		t.Errorf("Analyze(captured inputs after roots removed) error = %v, want nil", err)
	}
}

func TestAnalyzeHonorsCancelledContext(t *testing.T) {
	context, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Analyze(context, sourcebind.QualifiedInputs{}); err == nil {
		t.Error("Analyze(cancelled context) error = nil, want cancellation")
	}
}

func fixtureRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller(0) failed")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", "..", "..", "tests", "fixtures", "authoring", "source-first-v2"))
}

func copyTree(t *testing.T, source, target string) {
	t.Helper()
	if err := filepath.WalkDir(source, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		destination := filepath.Join(target, relative)
		if entry.IsDir() {
			return os.MkdirAll(destination, 0o755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(destination, data, 0o644)
	}); err != nil {
		t.Fatalf("copyTree(%q) error = %v", source, err)
	}
}

func run(t *testing.T, directory, name string, args ...string) {
	t.Helper()
	command := exec.Command(name, args...)
	command.Dir = directory
	command.Env = append(os.Environ(),
		"GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_SYSTEM=/dev/null", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_COUNT=0",
		"GIT_TERMINAL_PROMPT=0", "GIT_ASKPASS=/bin/false",
		"GIT_AUTHOR_NAME=Infrawright Fixture", "GIT_AUTHOR_EMAIL=fixtures@infrawright.invalid", "GIT_AUTHOR_DATE=2000-01-01T00:00:00 +0000",
		"GIT_COMMITTER_NAME=Infrawright Fixture", "GIT_COMMITTER_EMAIL=fixtures@infrawright.invalid", "GIT_COMMITTER_DATE=2000-01-01T00:00:00 +0000")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("%s %v error = %v output=%s", name, args, err, output)
	}
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("os.ReadFile(%q) error = %v", path, err)
	}
	return data
}

var _ = contracts.SourceEvidenceReport{}
