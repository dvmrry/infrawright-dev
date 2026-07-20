//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package assessment

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

// configureApplyGitProcess isolates the branch probe so cancellation cannot
// leave pipe-holding descendants behind after the direct Git process exits.
func configureApplyGitProcess(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	command.Cancel = func() error {
		return killApplyGitProcessGroup(command)
	}
}

func killApplyGitProcessGroup(command *exec.Cmd) error {
	if command.Process == nil {
		return os.ErrProcessDone
	}
	if err := syscall.Kill(-command.Process.Pid, syscall.SIGKILL); err != nil {
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		directErr := command.Process.Kill()
		if directErr == nil || errors.Is(directErr, os.ErrProcessDone) {
			return err
		}
		return errors.Join(err, directErr)
	}
	return nil
}

func cleanupApplyGitProcessGroup(command *exec.Cmd) error {
	// Command.Wait reaps the direct process. This final group kill handles a
	// descendant that survived its parent. An already-empty group is clean;
	// every other error must keep exact Apply fail-closed.
	err := killApplyGitProcessGroup(command)
	if errors.Is(err, os.ErrProcessDone) {
		return nil
	}
	return err
}
