package adopt

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/dvmrry/infrawright-dev/go/internal/procerr"
)

func writeOracleFakeExecutable(t *testing.T, body string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("Terraform execution is intentionally unsupported on Windows")
	}
	path := filepath.Join(t.TempDir(), "terraform-fake")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o700); err != nil {
		t.Fatalf("write fake executable: %v", err)
	}
	if err := os.Chmod(path, 0o700); err != nil {
		t.Fatalf("chmod fake executable: %v", err)
	}
	return path
}

func canonicalTemporaryDirectory(t *testing.T) string {
	t.Helper()
	directory, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("canonicalize temporary directory: %v", err)
	}
	return directory
}

func requireOracleProcessFailure(t *testing.T, err error, code string) *procerr.ProcessFailure {
	t.Helper()
	if err == nil {
		t.Fatalf("error = nil, want %s", code)
	}
	var failure *procerr.ProcessFailure
	if !errors.As(err, &failure) {
		t.Fatalf("error = %v (%T), want *procerr.ProcessFailure", err, err)
	}
	if failure.Code != code {
		t.Fatalf("failure code = %q, want %q (message %q)", failure.Code, code, failure.Message)
	}
	return failure
}

func TestBoundedOracleRunnerForwardsExactArgvEnvironmentAndOutputMode(t *testing.T) {
	executable := writeOracleFakeExecutable(t, `
: > "$ORACLE_LOG"
for argument in "$@"; do
  printf '%s\n' "$argument" >> "$ORACLE_LOG"
done
printf '%s' "$TF_VAR_fixture"
`)
	logPath := filepath.Join(canonicalTemporaryDirectory(t), "argv.log")
	runner := CreateOracleCommandRunner(executable)
	request := OracleCommandRequest{
		Argv:          []string{"plan", "-input=false", "-no-color", "-lock=false", "-out=/tmp/fixture plan"},
		CWD:           canonicalTemporaryDirectory(t),
		DebugName:     "plan-imports",
		Environment:   map[string]string{"ORACLE_LOG": logPath, "TF_VAR_fixture": "exact-environment"},
		CaptureOutput: true,
	}
	result, err := runner.Run(request)
	if err != nil {
		t.Fatalf("BoundedOracleRunner.Run: %v", err)
	}
	if string(result.Stdout) != "exact-environment" {
		t.Fatalf("captured stdout = %q, want exact-environment", result.Stdout)
	}
	logged, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read fake argv log: %v", err)
	}
	want := strings.Join(request.Argv, "\n") + "\n"
	if string(logged) != want {
		t.Fatalf("forwarded argv = %q, want %q", logged, want)
	}

	request.DebugName = "init"
	request.CaptureOutput = false
	result, err = runner.Run(request)
	if err != nil {
		t.Fatalf("BoundedOracleRunner.Run discard: %v", err)
	}
	if len(result.Stdout) != 0 {
		t.Fatalf("discarded stdout = %q, want empty", result.Stdout)
	}
}

func TestBoundedOracleRunnerRejectsRelativeSymlinkAndNonExecutable(t *testing.T) {
	cwd := canonicalTemporaryDirectory(t)
	request := OracleCommandRequest{Argv: []string{"version"}, CWD: cwd, DebugName: "init", Environment: map[string]string{}}

	requestRunner := CreateOracleCommandRunner("terraform")
	_, err := requestRunner.Run(request)
	failure := requireOracleProcessFailure(t, err, "UNRESOLVED_TERRAFORM_COMMAND_PATH")
	if failure.Message != "terraform init failed; provider diagnostics and import IDs were redacted" {
		t.Fatalf("relative path message = %q", failure.Message)
	}

	executable := writeOracleFakeExecutable(t, "exit 0")
	link := filepath.Join(t.TempDir(), "terraform-link")
	if err := os.Symlink(executable, link); err != nil {
		t.Fatalf("create executable symlink: %v", err)
	}
	_, err = CreateOracleCommandRunner(link).Run(request)
	requireOracleProcessFailure(t, err, "UNTRUSTED_TERRAFORM_EXECUTABLE")

	nonExecutable := filepath.Join(t.TempDir(), "terraform-data")
	if err := os.WriteFile(nonExecutable, []byte("opaque"), 0o600); err != nil {
		t.Fatalf("write non-executable: %v", err)
	}
	_, err = CreateOracleCommandRunner(nonExecutable).Run(request)
	requireOracleProcessFailure(t, err, "UNTRUSTED_TERRAFORM_EXECUTABLE")
}

func TestBoundedOracleRunnerCapsAndRedactsOversizedStderr(t *testing.T) {
	chunk := strings.Repeat("provider-secret-", 80)
	body := `
i=0
while [ "$i" -lt 1100 ]; do
  printf '%s' '` + chunk + `' >&2
  i=$((i + 1))
done
exit 7
`
	executable := writeOracleFakeExecutable(t, body)
	_, err := CreateOracleCommandRunner(executable).Run(OracleCommandRequest{
		Argv: []string{"plan"}, CWD: canonicalTemporaryDirectory(t), DebugName: "plan-imports", Environment: map[string]string{},
	})
	failure := requireOracleProcessFailure(t, err, "TERRAFORM_COMMAND_STDERR_LIMIT")
	if failure.Message != "terraform plan-imports failed; provider diagnostics and import IDs were redacted" || strings.Contains(failure.Message, "provider-secret") {
		t.Fatalf("oversized stderr failure leaked diagnostics: %#v", failure)
	}
}

func TestBoundedOracleRunnerTimeoutKillsDescendantProcessGroup(t *testing.T) {
	marker := filepath.Join(canonicalTemporaryDirectory(t), "descendant-survived")
	executable := writeOracleFakeExecutable(t, `
(
  /bin/sleep 1
  printf survived > "$ORACLE_MARKER"
) &
while :; do :; done
`)
	_, err := CreateOracleCommandRunner(executable).Run(OracleCommandRequest{
		Argv: []string{"plan"}, CWD: canonicalTemporaryDirectory(t), DebugName: "plan-imports",
		Environment: map[string]string{"INFRAWRIGHT_ORACLE_TIMEOUT_SECONDS": "0.05", "ORACLE_MARKER": marker},
	})
	requireOracleProcessFailure(t, err, "TERRAFORM_COMMAND_TIMEOUT")
	time.Sleep(1200 * time.Millisecond)
	if _, statErr := os.Stat(marker); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("timed-out descendant wrote marker: %v", statErr)
	}
}
