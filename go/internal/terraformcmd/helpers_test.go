package terraformcmd

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/dvmrry/infrawright-dev/go/internal/procerr"
)

func requirePOSIX(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("Terraform execution is intentionally unsupported on Windows")
	}
}

func writeExecutable(t *testing.T, body string) string {
	t.Helper()
	requirePOSIX(t)
	file := filepath.Join(t.TempDir(), "terraform")
	if err := os.WriteFile(file, []byte("#!/bin/sh\n"+body+"\n"), 0o700); err != nil {
		t.Fatalf("write executable: %v", err)
	}
	if err := os.Chmod(file, 0o700); err != nil {
		t.Fatalf("chmod executable: %v", err)
	}
	return file
}

func commandTestLimits(timeoutMs int64) *TerraformCommandLimits {
	return &TerraformCommandLimits{
		TimeoutMs:      &timeoutMs,
		MaxStdoutBytes: 64 * 1024,
		MaxStderrBytes: 4 * 1024,
	}
}

func baseCommandOptions(t *testing.T, executable string) TerraformCommandOptions {
	t.Helper()
	cwd, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("canonicalize cwd: %v", err)
	}
	return TerraformCommandOptions{
		TerraformExecutable: executable,
		Argv:                []string{},
		CWD:                 cwd,
		Environment:         map[string]string{},
		Limits:              commandTestLimits(2_000),
		Output:              TerraformCommandOutputDiscard,
	}
}

func requireProcessFailure(t *testing.T, err error, code string) *procerr.ProcessFailure {
	t.Helper()
	if err == nil {
		t.Fatalf("error = nil, want ProcessFailure code %q", code)
	}
	var failure *procerr.ProcessFailure
	if !errors.As(err, &failure) {
		t.Fatalf("error = %v (%T), want *procerr.ProcessFailure", err, err)
	}
	if failure.Code != code {
		t.Fatalf("failure.Code = %q, want %q (message %q)", failure.Code, code, failure.Message)
	}
	return failure
}
