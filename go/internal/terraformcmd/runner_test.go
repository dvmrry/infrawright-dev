//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package terraformcmd

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// TestRunTerraformCommandExactProcessBoundary pins
// node-src/io/terraform-command.ts at
// f3a86f2d24dddd4ebf95362d55718a81137800f2:468-688.
func TestRunTerraformCommandExactProcessBoundary(t *testing.T) {
	requirePOSIX(t)
	t.Setenv("PARENT_POISON", "must-not-leak")
	executable := writeExecutable(t, `
if IFS= read -r ignored; then exit 90; fi
printf '%s\n' "$PWD" "$ONLY" "${PARENT_POISON-unset}" "$#" "$1" "$2" "$3"
`)
	options := baseCommandOptions(t, executable)
	options.Argv = []string{"plan", "value with spaces", "literal;$(printf shell-was-used)"}
	options.Environment = map[string]string{"ONLY": "allowlisted"}
	options.Output = TerraformCommandOutputCapture

	result, err := RunTerraformCommand(options)
	if err != nil {
		t.Fatalf("RunTerraformCommand: %v", err)
	}
	if result.Kind != TerraformCommandResultCaptured {
		t.Fatalf("result.Kind = %q, want captured", result.Kind)
	}
	want := strings.Join([]string{
		options.CWD,
		"allowlisted",
		"unset",
		"3",
		"plan",
		"value with spaces",
		"literal;$(printf shell-was-used)",
		"",
	}, "\n")
	if string(result.Stdout) != want {
		t.Errorf("stdout = %q, want %q", result.Stdout, want)
	}
}

func TestRunTerraformCommandAcceptsCISizedEnvironment(t *testing.T) {
	requirePOSIX(t)
	executable := writeExecutable(t, `test "$CI_ENV_499" = value`)
	options := baseCommandOptions(t, executable)
	options.Environment = make(map[string]string, 500)
	for index := 0; index < 500; index++ {
		options.Environment["CI_ENV_"+strconv.Itoa(index)] = "value"
	}
	if _, err := RunTerraformCommand(options); err != nil {
		t.Fatalf("RunTerraformCommand(500 environment entries) error = %v, want nil", err)
	}
}

func TestRunTerraformCommandOutputModes(t *testing.T) {
	requirePOSIX(t)
	executable := writeExecutable(t, `printf '%s' visible-stdout; printf '%s' visible-stderr >&2`)
	var hostStdout synchronizedBuffer
	var hostStderr synchronizedBuffer
	original := terraformCommandHostOutput
	terraformCommandHostOutput.stdout = &hostStdout
	terraformCommandHostOutput.stderr = &hostStderr
	t.Cleanup(func() { terraformCommandHostOutput = original })

	tests := []struct {
		mode       TerraformCommandOutput
		kind       TerraformCommandResultKind
		wantStdout string
		wantStderr string
		wantResult string
	}{
		{
			mode:       TerraformCommandOutputCapture,
			kind:       TerraformCommandResultCaptured,
			wantResult: "visible-stdout",
		},
		{
			mode: TerraformCommandOutputDiscard,
			kind: TerraformCommandResultDiscarded,
		},
		{
			mode:       TerraformCommandOutputInherit,
			kind:       TerraformCommandResultInherited,
			wantStdout: "visible-stdout",
			wantStderr: "visible-stderr",
		},
		{
			mode:       TerraformCommandOutputInheritStderr,
			kind:       TerraformCommandResultInherited,
			wantStderr: "visible-stderr",
		},
	}
	for _, test := range tests {
		t.Run(string(test.mode), func(t *testing.T) {
			hostStdout.Reset()
			hostStderr.Reset()
			options := baseCommandOptions(t, executable)
			options.Output = test.mode
			result, err := RunTerraformCommand(options)
			if err != nil {
				t.Fatal(err)
			}
			if result.Kind != test.kind || string(result.Stdout) != test.wantResult {
				t.Errorf("result = %#v, want kind %q/stdout %q", result, test.kind, test.wantResult)
			}
			if hostStdout.String() != test.wantStdout || hostStderr.String() != test.wantStderr {
				t.Errorf("inherited = (%q, %q), want (%q, %q)", hostStdout.String(), hostStderr.String(), test.wantStdout, test.wantStderr)
			}
		})
	}
}

func TestRunTerraformCommandHostBackpressureCannotDefeatTimeout(t *testing.T) {
	executable := writeExecutable(t, `printf x; while :; do :; done`)
	blocked := &blockingWriter{
		started: make(chan struct{}),
		release: make(chan struct{}),
		done:    make(chan struct{}),
	}
	original := terraformCommandHostOutput
	terraformCommandHostOutput.stdout = blocked
	t.Cleanup(func() { terraformCommandHostOutput = original })
	originalHook := terraformCommandStartedHook
	terraformCommandStartedHook = func() {
		select {
		case <-blocked.started:
		case <-time.After(2 * time.Second):
		}
	}
	t.Cleanup(func() { terraformCommandStartedHook = originalHook })
	released := false
	defer func() {
		if !released {
			close(blocked.release)
		}
	}()

	options := baseCommandOptions(t, executable)
	options.Output = TerraformCommandOutputInherit
	options.Limits = commandTestLimits(5)
	started := time.Now()
	_, err := RunTerraformCommand(options)
	requireProcessFailure(t, err, "TERRAFORM_COMMAND_TIMEOUT")
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("timeout waited on blocked host output for %v", elapsed)
	}
	select {
	case <-blocked.started:
	case <-time.After(2 * time.Second):
		t.Fatal("inherited output was not dispatched")
	}
	close(blocked.release)
	released = true
	select {
	case <-blocked.done:
	case <-time.After(2 * time.Second):
		t.Fatal("blocked host-output worker did not exit after release")
	}
}

func TestRunTerraformCommandHostBackpressureCannotDefeatOverflow(t *testing.T) {
	executable := writeExecutable(t, "")
	releaseChild := filepath.Join(filepath.Dir(executable), "release-child")
	body := fmt.Sprintf(
		"#!/bin/sh\nprintf x\nwhile [ ! -f %s ]; do :; done\nwhile :; do printf overflow; done\n",
		shellQuote(releaseChild),
	)
	if err := os.WriteFile(executable, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	blocked := &blockingWriter{
		started: make(chan struct{}),
		release: make(chan struct{}),
		done:    make(chan struct{}),
	}
	original := terraformCommandHostOutput
	terraformCommandHostOutput.stdout = blocked
	t.Cleanup(func() { terraformCommandHostOutput = original })
	originalHook := terraformCommandStartedHook
	terraformCommandStartedHook = func() {
		select {
		case <-blocked.started:
		case <-time.After(2 * time.Second):
		}
	}
	t.Cleanup(func() { terraformCommandStartedHook = originalHook })
	released := false
	defer func() {
		if !released {
			close(blocked.release)
		}
	}()

	options := baseCommandOptions(t, executable)
	options.Output = TerraformCommandOutputInherit
	options.Limits.MaxStdoutBytes = 32
	result := make(chan error, 1)
	go func() {
		_, err := RunTerraformCommand(options)
		result <- err
	}()
	select {
	case <-blocked.started:
	case err := <-result:
		t.Fatalf("overflow run returned before its inherited write started: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("inherited output write did not start")
	}
	if err := os.WriteFile(releaseChild, []byte{}, 0o600); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-result:
		requireProcessFailure(t, err, "TERRAFORM_COMMAND_STDOUT_LIMIT")
	case <-time.After(2 * time.Second):
		t.Fatal("stdout overflow waited on blocked host output")
	}
	close(blocked.release)
	released = true
	select {
	case <-blocked.done:
	case <-time.After(2 * time.Second):
		t.Fatal("overflow host-output worker did not exit after release")
	}
}

func TestRunTerraformCommandInheritStreamsBeforeExit(t *testing.T) {
	executable := writeExecutable(t, "")
	release := filepath.Join(filepath.Dir(executable), "release")
	body := fmt.Sprintf("#!/bin/sh\nprintf live\nwhile [ ! -f %s ]; do :; done\n", shellQuote(release))
	if err := os.WriteFile(executable, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	var hostStdout synchronizedBuffer
	original := terraformCommandHostOutput
	terraformCommandHostOutput.stdout = &hostStdout
	t.Cleanup(func() { terraformCommandHostOutput = original })
	options := baseCommandOptions(t, executable)
	options.Output = TerraformCommandOutputInherit
	result := make(chan error, 1)
	go func() {
		_, err := RunTerraformCommand(options)
		result <- err
	}()
	waitForSynchronizedBuffer(t, &hostStdout, "live")
	if err := os.WriteFile(release, []byte{}, 0o600); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("command did not finish after live output was observed")
	}
}

func TestRunTerraformCommandInheritedOutputDeliveredBeforeNonzeroReturn(t *testing.T) {
	executable := writeExecutable(t, `printf stdout; printf stderr >&2; exit 19`)
	var hostStdout synchronizedBuffer
	var hostStderr synchronizedBuffer
	original := terraformCommandHostOutput
	terraformCommandHostOutput.stdout = &hostStdout
	terraformCommandHostOutput.stderr = &hostStderr
	t.Cleanup(func() { terraformCommandHostOutput = original })
	options := baseCommandOptions(t, executable)
	options.Output = TerraformCommandOutputInherit
	_, err := RunTerraformCommand(options)
	requireProcessFailure(t, err, "TERRAFORM_COMMAND_FAILED")
	if got := hostStdout.String(); got != "stdout" {
		t.Errorf("stdout immediately after failure = %q, want stdout", got)
	}
	if got := hostStderr.String(); got != "stderr" {
		t.Errorf("stderr immediately after failure = %q, want stderr", got)
	}
}

func TestRunTerraformCommandBlockedInheritedRunDoesNotStallNextRun(t *testing.T) {
	executable := writeExecutable(t, "")
	releaseChild := filepath.Join(filepath.Dir(executable), "release-old-output")
	queuedWritten := filepath.Join(filepath.Dir(executable), "old-queued-written")
	body := fmt.Sprintf(`
if [ "$1" = old ]; then
  printf old-blocked
  while [ ! -f %s ]; do :; done
  i=0
  while [ "$i" -lt 16384 ]; do
    printf old-queued
    i=$((i + 1))
  done
  : > %s
  while :; do printf overflow; done
fi
printf new
`, shellQuote(releaseChild), shellQuote(queuedWritten))
	if err := os.WriteFile(executable, []byte("#!/bin/sh\n"+body), 0o700); err != nil {
		t.Fatal(err)
	}
	blocked := &selectivelyBlockingWriter{
		started: make(chan struct{}),
		release: make(chan struct{}),
		done:    make(chan struct{}),
	}
	original := terraformCommandHostOutput
	terraformCommandHostOutput.stdout = blocked
	t.Cleanup(func() { terraformCommandHostOutput = original })
	released := false
	defer func() {
		if !released {
			close(blocked.release)
		}
	}()

	blockedOptions := baseCommandOptions(t, executable)
	blockedOptions.Argv = []string{"old"}
	blockedOptions.Output = TerraformCommandOutputInherit
	blockedOptions.Limits = commandTestLimits(10_000)
	blockedOptions.Limits.MaxStdoutBytes = 192 * 1024
	blockedResult := make(chan error, 1)
	go func() {
		_, err := RunTerraformCommand(blockedOptions)
		blockedResult <- err
	}()
	select {
	case <-blocked.started:
	case err := <-blockedResult:
		t.Fatalf("blocked run returned before its inherited write started: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("first inherited write did not start")
	}
	if err := os.WriteFile(releaseChild, []byte{}, 0o600); err != nil {
		t.Fatal(err)
	}
	waitForFile(t, queuedWritten)
	select {
	case err := <-blockedResult:
		requireProcessFailure(t, err, "TERRAFORM_COMMAND_STDOUT_LIMIT")
	case <-time.After(5 * time.Second):
		t.Fatal("old run did not fail after its queued output exceeded the limit")
	}

	fastOptions := baseCommandOptions(t, executable)
	fastOptions.Argv = []string{"new"}
	fastOptions.Output = TerraformCommandOutputInherit
	if _, err := RunTerraformCommand(fastOptions); err != nil {
		t.Fatalf("next RunTerraformCommand: %v", err)
	}
	if got := blocked.String(); got != "new" {
		t.Fatalf("next inherited output immediately after return = %q, want new", got)
	}
	close(blocked.release)
	released = true
	select {
	case <-blocked.done:
	case <-time.After(2 * time.Second):
		t.Fatal("failed old run's host-output worker did not exit after release")
	}
	if got := blocked.String(); got != "newold-blocked" {
		t.Fatalf("combined output after releasing old in-flight write = %q, want no old queued output", got)
	}
}

func TestCommandHostOutputPumpOrdersAndCancelsQueuedWrites(t *testing.T) {
	t.Run("orders and completes", func(t *testing.T) {
		var destination synchronizedBuffer
		pump := newCommandHostOutputPump(&destination)
		pump.enqueue([]byte("one"))
		pump.enqueue([]byte("-two"))
		pump.enqueue([]byte("-three"))
		pump.finish()
		select {
		case <-pump.done:
		case <-time.After(2 * time.Second):
			t.Fatal("output pump did not finish")
		}
		if got := destination.String(); got != "one-two-three" {
			t.Fatalf("destination = %q, want ordered output", got)
		}
	})

	t.Run("abort wins an observed queued handoff", func(t *testing.T) {
		var destination synchronizedBuffer
		pump := newCommandHostOutputPump(&destination)
		handoffObserved := make(chan struct{})
		allowHandoff := make(chan struct{})
		var releaseOnce sync.Once
		releaseHandoff := func() { releaseOnce.Do(func() { close(allowHandoff) }) }
		pump.mu.Lock()
		pump.beforeWriteHandoffHook = func() {
			close(handoffObserved)
			<-allowHandoff
		}
		pump.mu.Unlock()
		t.Cleanup(func() {
			pump.abort()
			pump.finish()
			releaseHandoff()
			select {
			case <-pump.done:
			case <-time.After(2 * time.Second):
				t.Error("output pump worker did not exit during cleanup")
			}
		})

		pump.enqueue([]byte("must-not-write"))
		select {
		case <-handoffObserved:
		case <-time.After(2 * time.Second):
			t.Fatal("queued output did not reach the pre-handoff boundary")
		}
		pump.abort()
		releaseHandoff()
		pump.finish()
		select {
		case <-pump.done:
		case <-time.After(2 * time.Second):
			t.Fatal("aborted output pump did not finish")
		}
		if got := destination.String(); got != "" {
			t.Fatalf("destination after abort-before-handoff = %q, want empty", got)
		}
	})

	t.Run("repeated abort drops queued writes and releases workers", func(t *testing.T) {
		for iteration := 0; iteration < 32; iteration++ {
			destination := &blockingRecordingWriter{
				started: make(chan struct{}),
				release: make(chan struct{}),
			}
			pump := newCommandHostOutputPump(destination)
			pump.enqueue([]byte("in-flight"))
			select {
			case <-destination.started:
			case <-time.After(2 * time.Second):
				t.Fatalf("iteration %d: output write did not start", iteration)
			}
			pump.enqueue([]byte("queued-one"))
			pump.enqueue([]byte("queued-two"))
			pump.abort()
			pump.finish()
			close(destination.release)
			select {
			case <-pump.done:
			case <-time.After(2 * time.Second):
				t.Fatalf("iteration %d: aborted output pump did not finish", iteration)
			}
			if got := destination.String(); got != "in-flight" {
				t.Fatalf("iteration %d: destination = %q, want only the already in-flight write", iteration, got)
			}
		}
	})
}

func TestRunTerraformCommandDeadlineExpiredDuringStart(t *testing.T) {
	executable := writeExecutable(t, `exit 0`)
	originalHook := terraformCommandStartedHook
	terraformCommandStartedHook = func() { time.Sleep(25 * time.Millisecond) }
	t.Cleanup(func() { terraformCommandStartedHook = originalHook })
	options := baseCommandOptions(t, executable)
	options.Limits = commandTestLimits(5)
	_, err := RunTerraformCommand(options)
	requireProcessFailure(t, err, "TERRAFORM_COMMAND_TIMEOUT")
}

func TestRunTerraformCommandEmptyCaptureIsAllocated(t *testing.T) {
	executable := writeExecutable(t, `exit 0`)
	options := baseCommandOptions(t, executable)
	options.Output = TerraformCommandOutputCapture
	result, err := RunTerraformCommand(options)
	if err != nil {
		t.Fatal(err)
	}
	if result.Stdout == nil || len(result.Stdout) != 0 {
		t.Errorf("stdout = %#v, want allocated empty slice", result.Stdout)
	}
}

func TestRunTerraformCommandFailurePrecedenceAndMessages(t *testing.T) {
	requirePOSIX(t)
	tests := []struct {
		name         string
		body         string
		mutateLimits func(*TerraformCommandLimits)
		code         string
		message      string
		secret       string
	}{
		{
			name:    "nonzero",
			body:    `printf '%s' child-secret; printf '%s' diagnostic-secret >&2; exit 19`,
			code:    "TERRAFORM_COMMAND_FAILED",
			message: "Terraform command did not complete successfully",
			secret:  "secret",
		},
		{
			name: "stdout limit wins over nonzero exit",
			body: `i=0; while [ "$i" -lt 65 ]; do printf x; i=$((i + 1)); done; exit 19`,
			mutateLimits: func(limits *TerraformCommandLimits) {
				limits.MaxStdoutBytes = 32
			},
			code:    "TERRAFORM_COMMAND_STDOUT_LIMIT",
			message: "Terraform command exceeded its output limit",
		},
		{
			name: "stderr limit wins over nonzero exit",
			body: `i=0; while [ "$i" -lt 65 ]; do printf x >&2; i=$((i + 1)); done; exit 19`,
			mutateLimits: func(limits *TerraformCommandLimits) {
				limits.MaxStderrBytes = 32
			},
			code:    "TERRAFORM_COMMAND_STDERR_LIMIT",
			message: "Terraform command exceeded its diagnostic-output limit",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			executable := writeExecutable(t, test.body)
			options := baseCommandOptions(t, executable)
			options.Output = TerraformCommandOutputCapture
			if test.mutateLimits != nil {
				test.mutateLimits(options.Limits)
			}
			_, err := RunTerraformCommand(options)
			failure := requireProcessFailure(t, err, test.code)
			if failure.Message != test.message {
				t.Errorf("message = %q, want %q", failure.Message, test.message)
			}
			if test.secret != "" && strings.Contains(failure.Error(), test.secret) {
				t.Errorf("failure leaked child output: %q", failure.Error())
			}
			if strings.Contains(failure.Error(), filepath.Dir(executable)) {
				t.Errorf("failure leaked executable path: %q", failure.Error())
			}
		})
	}
}

func TestRunTerraformCommandTimeoutAndLongDeadline(t *testing.T) {
	requirePOSIX(t)
	blocked := writeExecutable(t, `while :; do :; done`)
	options := baseCommandOptions(t, blocked)
	options.Limits = commandTestLimits(30)
	started := time.Now()
	_, err := RunTerraformCommand(options)
	requireProcessFailure(t, err, "TERRAFORM_COMMAND_TIMEOUT")
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Errorf("timeout took %v, want under 2s", elapsed)
	}

	immediate := writeExecutable(t, `exit 0`)
	options = baseCommandOptions(t, immediate)
	maximum := int64(maximumJavaScriptSafeInteger)
	options.Limits.TimeoutMs = &maximum
	result, err := RunTerraformCommand(options)
	if err != nil {
		t.Fatalf("maximum safe timeout fired early: %v", err)
	}
	if result.Kind != TerraformCommandResultDiscarded {
		t.Errorf("kind = %q, want discarded", result.Kind)
	}
	options.Limits.TimeoutMs = nil
	if _, err := RunTerraformCommand(options); err != nil {
		t.Fatalf("null/no timeout: %v", err)
	}
}

func TestRunTerraformCommandReapsDescendantsOnSuccessAndTimeout(t *testing.T) {
	requirePOSIX(t)
	for _, test := range []struct {
		name    string
		tail    string
		timeout int64
		code    string
	}{
		{name: "success", tail: "exit 0", timeout: 2_000},
		{name: "timeout", tail: "wait", timeout: 500, code: "TERRAFORM_COMMAND_TIMEOUT"},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			pidFile := filepath.Join(root, "descendant.pid")
			descendantPID := 0
			var pidWaitErr error
			executable := filepath.Join(root, "terraform")
			body := fmt.Sprintf("#!/bin/sh\nwhile :; do :; done &\nprintf '%%s' \"$!\" > %s\n%s\n", shellQuote(pidFile), test.tail)
			if err := os.WriteFile(executable, []byte(body), 0o700); err != nil {
				t.Fatal(err)
			}
			options := baseCommandOptions(t, executable)
			options.Limits = commandTestLimits(test.timeout)
			if test.code != "" {
				originalHook := terraformCommandStartedHook
				terraformCommandStartedHook = func() {
					descendantPID, pidWaitErr = waitForPositivePIDFile(pidFile, 5*time.Second)
				}
				t.Cleanup(func() { terraformCommandStartedHook = originalHook })
			}
			if test.code == "" {
				if _, err := RunTerraformCommand(options); err != nil {
					t.Fatalf("RunTerraformCommand: %v", err)
				}
			} else {
				_, err := RunTerraformCommand(options)
				requireProcessFailure(t, err, test.code)
			}
			if pidWaitErr != nil {
				t.Fatalf("wait for positive descendant pid before releasing start hook: %v", pidWaitErr)
			}
			if descendantPID == 0 {
				var err error
				descendantPID, err = waitForPositivePIDFile(pidFile, 2*time.Second)
				if err != nil {
					t.Fatalf("wait for positive descendant pid after command: %v", err)
				}
			}
			waitForProcessMissing(t, descendantPID)
		})
	}
}

func TestRunTerraformCommandRejectsUntrustedPathsAndInputs(t *testing.T) {
	requirePOSIX(t)
	executable := writeExecutable(t, `exit 0`)
	options := baseCommandOptions(t, executable)

	relative := options
	relative.TerraformExecutable = "terraform"
	_, err := RunTerraformCommand(relative)
	requireProcessFailure(t, err, "UNRESOLVED_TERRAFORM_COMMAND_PATH")

	nonExecutable := filepath.Join(t.TempDir(), "terraform")
	if err := os.WriteFile(nonExecutable, []byte("opaque"), 0o600); err != nil {
		t.Fatal(err)
	}
	untrusted := options
	untrusted.TerraformExecutable = nonExecutable
	_, err = RunTerraformCommand(untrusted)
	failure := requireProcessFailure(t, err, "UNTRUSTED_TERRAFORM_EXECUTABLE")
	if failure.Message != "trusted Terraform executable is not an allowed regular file" {
		t.Errorf("message = %q", failure.Message)
	}

	link := filepath.Join(t.TempDir(), "terraform-link")
	if err := os.Symlink(executable, link); err != nil {
		t.Fatal(err)
	}
	untrusted.TerraformExecutable = link
	_, err = RunTerraformCommand(untrusted)
	requireProcessFailure(t, err, "UNTRUSTED_TERRAFORM_EXECUTABLE")

	badArgv := options
	badArgv.Argv = nil
	_, err = RunTerraformCommand(badArgv)
	requireProcessFailure(t, err, "INVALID_TERRAFORM_COMMAND_ARGUMENTS")

	badEnvironment := options
	badEnvironment.Environment = nil
	_, err = RunTerraformCommand(badEnvironment)
	requireProcessFailure(t, err, "INVALID_TERRAFORM_COMMAND_ENVIRONMENT")

	badOutput := options
	badOutput.Output = "unknown"
	_, err = RunTerraformCommand(badOutput)
	requireProcessFailure(t, err, "INVALID_TERRAFORM_COMMAND_OUTPUT")

	badUTF8Path := options
	badUTF8Path.CWD += "\xff"
	_, err = RunTerraformCommand(badUTF8Path)
	requireProcessFailure(t, err, "UNRESOLVED_TERRAFORM_COMMAND_PATH")

	badFormat := filepath.Join(t.TempDir(), "terraform")
	if err := os.WriteFile(badFormat, []byte("not an executable image\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	spawnFailure := options
	spawnFailure.TerraformExecutable = badFormat
	_, err = RunTerraformCommand(spawnFailure)
	requireProcessFailure(t, err, "TERRAFORM_COMMAND_SPAWN_FAILED")
}

func TestReadBoundedCommandStreamFailures(t *testing.T) {
	for _, test := range []struct {
		name   string
		stdout bool
		code   string
	}{
		{name: "stdout", stdout: true, code: "TERRAFORM_COMMAND_STDOUT_FAILED"},
		{name: "stderr", stdout: false, code: "TERRAFORM_COMMAND_STDERR_FAILED"},
	} {
		t.Run(test.name, func(t *testing.T) {
			reader, writer, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			if err := reader.Close(); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = writer.Close() })
			result := readBoundedCommandStream(reader, 32, false, nil, test.stdout)
			failure := commandStreamFailure(test.stdout, result.err)
			requireProcessFailure(t, failure, test.code)
		})
	}
}

func waitForPositivePIDFile(filePath string, timeout time.Duration) (int, error) {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for {
		value, err := os.ReadFile(filePath)
		if err == nil {
			pid, parseErr := strconv.Atoi(strings.TrimSpace(string(value)))
			switch {
			case parseErr != nil:
				lastErr = fmt.Errorf("parse %q: %w", value, parseErr)
			case pid <= 0:
				lastErr = fmt.Errorf("parsed non-positive pid %d", pid)
			default:
				return pid, nil
			}
		} else {
			lastErr = err
		}
		if time.Now().After(deadline) {
			return 0, fmt.Errorf("read %q: %w", filePath, lastErr)
		}
		time.Sleep(time.Millisecond)
	}
}

func waitForProcessMissing(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		err := syscall.Kill(pid, 0)
		if err != nil {
			return
		}
		if runtime.GOOS == "linux" {
			status, readErr := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
			if readErr != nil || strings.Contains(string(status), ") Z ") {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("process %d survived Terraform process-group cleanup", pid)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

type synchronizedBuffer struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (b *synchronizedBuffer) Write(value []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.Write(value)
}

func (b *synchronizedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.String()
}

func (b *synchronizedBuffer) Reset() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.buffer.Reset()
}

func waitForSynchronizedBuffer(t *testing.T, buffer *synchronizedBuffer, want string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for buffer.String() != want {
		if time.Now().After(deadline) {
			t.Fatalf("host output = %q, want %q", buffer.String(), want)
		}
		time.Sleep(time.Millisecond)
	}
}

type blockingWriter struct {
	once       sync.Once
	finishOnce sync.Once
	started    chan struct{}
	release    chan struct{}
	done       chan struct{}
}

func (w *blockingWriter) Write(value []byte) (int, error) {
	w.once.Do(func() { close(w.started) })
	<-w.release
	if w.done != nil {
		w.finishOnce.Do(func() { close(w.done) })
	}
	return len(value), nil
}

type selectivelyBlockingWriter struct {
	once       sync.Once
	finishOnce sync.Once
	started    chan struct{}
	release    chan struct{}
	done       chan struct{}
	mu         sync.Mutex
	buffer     bytes.Buffer
}

func (w *selectivelyBlockingWriter) Write(value []byte) (int, error) {
	wasBlocked := bytes.Contains(value, []byte("blocked"))
	if wasBlocked {
		w.once.Do(func() { close(w.started) })
		<-w.release
	}
	w.mu.Lock()
	written, err := w.buffer.Write(value)
	w.mu.Unlock()
	if wasBlocked {
		w.finishOnce.Do(func() { close(w.done) })
	}
	return written, err
}

func (w *selectivelyBlockingWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buffer.String()
}

type blockingRecordingWriter struct {
	once    sync.Once
	started chan struct{}
	release chan struct{}
	mu      sync.Mutex
	buffer  bytes.Buffer
}

func (w *blockingRecordingWriter) Write(value []byte) (int, error) {
	w.once.Do(func() { close(w.started) })
	<-w.release
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buffer.Write(value)
}

func (w *blockingRecordingWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buffer.String()
}
