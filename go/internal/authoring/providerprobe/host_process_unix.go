//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package providerprobe

import (
	"context"
	"errors"
	"os/exec"
	"sync"
	"syscall"
	"time"
)

func legacyGitExecutableName() string { return "git" }
func legacyGitAskPass() string        { return "/bin/false" }
func legacyGitNullDevice() string     { return "/dev/null" }

func runLegacyGitProcess(
	parent context.Context,
	executable string,
	arguments []string,
	environment []string,
	timeout time.Duration,
	streamLimit int64,
) error {
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return err
	}
	command := exec.Command(executable, arguments...)
	command.Env = environment
	command.Stdin = nil
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	var stopOnce sync.Once
	stop := func() {
		stopOnce.Do(func() {
			if command.Process != nil {
				_ = syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
			}
		})
	}
	stdout := &boundedGitOutput{limit: streamLimit, stop: stop}
	stderr := &boundedGitOutput{limit: streamLimit, stop: stop}
	command.Stdout = stdout
	command.Stderr = stderr
	if err := command.Start(); err != nil {
		return err
	}
	wait := make(chan error, 1)
	go func() { wait <- command.Wait() }()
	select {
	case err := <-wait:
		if stdout.Exceeded() || stderr.Exceeded() {
			return errors.New("legacy Git output exceeded the stream limit")
		}
		return err
	case <-ctx.Done():
		stop()
		<-wait
		return ctx.Err()
	}
}
