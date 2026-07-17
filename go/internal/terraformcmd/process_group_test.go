//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package terraformcmd

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

const (
	terraformSignalHarnessEnvironment      = "TERRAFORMCMD_SIGNAL_HARNESS"
	terraformSignalOutputMarkerEnvironment = "TERRAFORMCMD_SIGNAL_OUTPUT_MARKER"
)

// TestTerraformTerminationSignalHarness is both the isolated signal target and
// the parent-side contract test. The subprocess branch is necessary because
// the runner deliberately re-signals its own process with the original signal.
func TestTerraformTerminationSignalHarness(t *testing.T) {
	if os.Getenv(terraformSignalHarnessEnvironment) == "1" {
		runTerraformSignalHarness(t)
		return
	}

	for _, signal := range []syscall.Signal{syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP} {
		t.Run(signal.String(), func(t *testing.T) {
			root, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			cwd := filepath.Join(root, "cwd")
			if err := os.Mkdir(cwd, 0o700); err != nil {
				t.Fatal(err)
			}
			files := make([]string, 0, 4)
			outputMarker := filepath.Join(root, "output-blocked")
			environment := []string{
				terraformSignalHarnessEnvironment + "=1",
				"TERRAFORMCMD_SIGNAL_CWD=" + cwd,
				terraformSignalOutputMarkerEnvironment + "=" + outputMarker,
			}
			for index := 0; index < 2; index++ {
				directFile := filepath.Join(root, fmt.Sprintf("direct-%d.pid", index))
				descendantFile := filepath.Join(root, fmt.Sprintf("descendant-%d.pid", index))
				executable := filepath.Join(root, fmt.Sprintf("terraform-%d", index))
				body := fmt.Sprintf(
					"#!/bin/sh\nprintf '%%s' \"$$\" > %s\nwhile :; do :; done &\nprintf '%%s' \"$!\" > %s\nprintf x\nwait\n",
					shellQuote(directFile),
					shellQuote(descendantFile),
				)
				if err := os.WriteFile(executable, []byte(body), 0o700); err != nil {
					t.Fatal(err)
				}
				files = append(files, directFile, descendantFile)
				environment = append(environment, fmt.Sprintf("TERRAFORMCMD_SIGNAL_EXECUTABLE_%d=%s", index, executable))
			}

			command := exec.Command(os.Args[0], "-test.run=^TestTerraformTerminationSignalHarness$")
			command.Env = environment
			if err := command.Start(); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if command.ProcessState == nil {
					_ = command.Process.Kill()
					_, _ = command.Process.Wait()
				}
			})

			pids := make([]int, 0, len(files))
			for _, file := range files {
				pids = append(pids, waitForPIDFile(t, file))
			}
			waitForFile(t, outputMarker)
			if err := command.Process.Signal(signal); err != nil {
				t.Fatalf("signal harness: %v", err)
			}
			err = command.Wait()
			var exitError *exec.ExitError
			if !errors.As(err, &exitError) {
				t.Fatalf("harness wait = %v, want signal exit", err)
			}
			status, ok := exitError.Sys().(syscall.WaitStatus)
			if !ok || !status.Signaled() || status.Signal() != signal {
				t.Fatalf("harness status = %#v, want signal %s", exitError.Sys(), signal)
			}
			for _, pid := range pids {
				waitForProcessMissing(t, pid)
			}
		})
	}
}

func TestWatcherResendsSignalReceivedBeforeLastUnregister(t *testing.T) {
	receivedByWatcher := make(chan struct{})
	allowRegistryLock := make(chan struct{})
	resent := make(chan syscall.Signal, 1)
	originalHook := terraformSignalReceivedHook
	originalResignal := terraformResignalProcess
	terraformSignalReceivedHook = func() {
		close(receivedByWatcher)
		<-allowRegistryLock
	}
	terraformResignalProcess = func(received syscall.Signal) error {
		resent <- received
		return nil
	}
	t.Cleanup(func() {
		terraformSignalReceivedHook = originalHook
		terraformResignalProcess = originalResignal
	})

	signals := make(chan os.Signal, 1)
	stop := make(chan struct{})
	done := make(chan struct{})
	activeTerraformGroups.mu.Lock()
	if len(activeTerraformGroups.pids) != 0 || activeTerraformGroups.signals != nil || activeTerraformGroups.terminating {
		activeTerraformGroups.mu.Unlock()
		t.Fatal("process-group registry was not idle before deterministic race test")
	}
	activeTerraformGroups.signals = signals
	activeTerraformGroups.stop = stop
	activeTerraformGroups.done = done
	activeTerraformGroups.mu.Unlock()
	go watchTerraformTerminationSignals(signals, stop, done)

	signals <- syscall.SIGTERM
	select {
	case <-receivedByWatcher:
	case <-time.After(2 * time.Second):
		t.Fatal("watcher did not receive the test signal")
	}
	// Reproduce last-unregister teardown while the watcher is between channel
	// receipt and registry locking.
	activeTerraformGroups.mu.Lock()
	activeTerraformGroups.signals = nil
	activeTerraformGroups.stop = nil
	activeTerraformGroups.done = nil
	signal.Stop(signals)
	close(stop)
	activeTerraformGroups.mu.Unlock()
	close(allowRegistryLock)

	select {
	case got := <-resent:
		if got != syscall.SIGTERM {
			t.Fatalf("re-sent signal = %s, want SIGTERM", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("signal received before teardown was swallowed")
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("watcher did not finish")
	}
}

func TestResignalFailureReleasesTerminatingGate(t *testing.T) {
	originalResignal := terraformResignalProcess
	terraformResignalProcess = func(syscall.Signal) error { return syscall.EPERM }
	t.Cleanup(func() { terraformResignalProcess = originalResignal })

	signals := make(chan os.Signal, 1)
	stop := make(chan struct{})
	activeTerraformGroups.mu.Lock()
	if len(activeTerraformGroups.pids) != 0 || activeTerraformGroups.signals != nil || activeTerraformGroups.terminating {
		activeTerraformGroups.mu.Unlock()
		t.Fatal("process-group registry was not idle before resignal failure test")
	}
	activeTerraformGroups.signals = signals
	activeTerraformGroups.stop = stop
	activeTerraformGroups.done = make(chan struct{})
	activeTerraformGroups.mu.Unlock()

	handleTerraformTerminationSignal(syscall.SIGTERM, stop)
	activeTerraformGroups.mu.Lock()
	defer activeTerraformGroups.mu.Unlock()
	if activeTerraformGroups.terminating || activeTerraformGroups.terminationRelease != nil {
		t.Fatal("failed self-signal left the process-group registry permanently terminating")
	}
}

func runTerraformSignalHarness(t *testing.T) {
	t.Helper()
	cwd := os.Getenv("TERRAFORMCMD_SIGNAL_CWD")
	terraformCommandHostOutput.stdout = &signalHarnessBlockingWriter{
		marker: os.Getenv(terraformSignalOutputMarkerEnvironment),
		block:  make(chan struct{}),
	}
	errResults := make(chan error, 2)
	for index := 0; index < 2; index++ {
		executable := os.Getenv(fmt.Sprintf("TERRAFORMCMD_SIGNAL_EXECUTABLE_%d", index))
		go func() {
			_, err := RunTerraformCommand(TerraformCommandOptions{
				TerraformExecutable: executable,
				Argv:                []string{},
				CWD:                 cwd,
				Environment:         map[string]string{},
				Limits: &TerraformCommandLimits{
					TimeoutMs:      nil,
					MaxStdoutBytes: 64 * 1024,
					MaxStderrBytes: 4 * 1024,
				},
				Output: TerraformCommandOutputInherit,
			})
			errResults <- err
		}()
	}
	if err := <-errResults; err != nil {
		t.Fatalf("runner returned before signal: %v", err)
	}
	t.Fatal("runner returned before signal without an error")
}

type signalHarnessBlockingWriter struct {
	once   sync.Once
	marker string
	block  chan struct{}
}

func (w *signalHarnessBlockingWriter) Write(value []byte) (int, error) {
	w.once.Do(func() { _ = os.WriteFile(w.marker, []byte{}, 0o600) })
	<-w.block
	return len(value), nil
}

func waitForPIDFile(t *testing.T, file string) int {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		value, err := os.ReadFile(file)
		if err == nil {
			pid, parseErr := strconv.Atoi(strings.TrimSpace(string(value)))
			if parseErr == nil && pid > 0 {
				return pid
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for pid file %s", file)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func waitForFile(t *testing.T, file string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(file); err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for file %s", file)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
