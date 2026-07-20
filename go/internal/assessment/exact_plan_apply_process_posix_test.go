//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package assessment

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestGitApplyBranchReapsDescendantAfterOverflow(t *testing.T) {
	root := t.TempDir()
	pidFile := filepath.Join(root, "descendant.pid")
	executable := assessmentExecutable(t, root, strings.Join([]string{
		"/bin/sleep 30 &",
		"printf '%s' \"$!\" > " + assessmentShellLiteral(pidFile),
		"printf '%131073s' x >&2",
		"wait",
	}, "\n"))
	if got := runGitApplyBranch(root, executable); got != "unknown" {
		t.Fatalf("runGitApplyBranch(descendant overflow) = %q, want unknown", got)
	}
	pid := readApplyGitDescendantPID(t, pidFile)
	waitForApplyGitDescendantExit(t, pid)
}

func TestGitApplyBranchReapsDescendantAfterSuccessfulParentExit(t *testing.T) {
	root := t.TempDir()
	pidFile := filepath.Join(root, "descendant.pid")
	executable := assessmentExecutable(t, root, strings.Join([]string{
		"/bin/sleep 30 </dev/null >/dev/null 2>&1 &",
		"printf '%s' \"$!\" > " + assessmentShellLiteral(pidFile),
		"printf 'main\\n'",
	}, "\n"))
	if got := runGitApplyBranch(root, executable); got != "main" {
		t.Fatalf("runGitApplyBranch(successful parent) = %q, want main", got)
	}
	pid := readApplyGitDescendantPID(t, pidFile)
	waitForApplyGitDescendantExit(t, pid)
}

func TestGitApplyBranchFailsClosedWhenFinalCleanupFails(t *testing.T) {
	root := t.TempDir()
	executable := assessmentExecutable(t, root, "printf 'main\\n'")
	wantErr := errors.New("injected process-group cleanup failure")
	cleanup := func(_ *exec.Cmd) error { return wantErr }
	if got := runGitApplyBranchWithCleanup(root, executable, cleanup); got != "unknown" {
		t.Fatalf("runGitApplyBranchWithCleanup(cleanup failure) = %q, want unknown", got)
	}
}

func readApplyGitDescendantPID(t *testing.T, path string) int {
	t.Helper()
	pidBytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("os.ReadFile(%q) error = %v, want nil", path, err)
	}
	pid, err := strconv.Atoi(string(pidBytes))
	if err != nil {
		t.Fatalf("strconv.Atoi(%q) error = %v, want nil", pidBytes, err)
	}
	t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })
	return pid
}

func waitForApplyGitDescendantExit(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		err := syscall.Kill(pid, 0)
		if errors.Is(err, syscall.ESRCH) {
			return
		}
		if err != nil {
			t.Fatalf("syscall.Kill(%d, 0) error = %v, want nil or ESRCH", pid, err)
		}
		if time.Now().After(deadline) {
			t.Fatalf("Git branch probe descendant %d still exists after process-group cancellation", pid)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
