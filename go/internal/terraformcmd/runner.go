package terraformcmd

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/dvmrry/infrawright-dev/go/internal/procerr"
)

const (
	commandReadBufferBytes = 64 * 1024
	// commandCaptureInitialBytes is the starting capacity of a captured-output
	// slice. append grows it toward the enforced per-stream ceiling, so tiny
	// captures (show / state list) no longer pre-reserve the full hard limit.
	commandCaptureInitialBytes = 64 * 1024
	maximumTimerChunkMs        = int64(2_147_483_647)
)

var terraformCommandHostOutput = struct {
	stdout io.Writer
	stderr io.Writer
}{
	stdout: os.Stdout,
	stderr: os.Stderr,
}

// terraformCommandStartedHook is nil in production. Tests use the post-Start
// boundary to pin deadline and host-output scheduling races deterministically.
var terraformCommandStartedHook func()

type commandHostOutputPump struct {
	destination io.Writer

	// mu totally orders abort against the queued-to-in-flight transition in
	// nextWrite. A value removed from queue owns the one admitted write transaction;
	// every value still queued when abort acquires mu is cleared and rejected.
	mu                     sync.Mutex
	cond                   *sync.Cond
	queue                  [][]byte
	finished               bool
	aborted                bool
	beforeWriteHandoffHook func()
	done                   chan struct{}
}

type commandStreamResult struct {
	stdout bool
	bytes  []byte
	err    error
}

type commandWaitResult struct {
	err error
}

// RunTerraformCommand runs one bounded Terraform process without a shell or
// inherited environment. Child output never enters a structured failure.
func RunTerraformCommand(options TerraformCommandOptions) (TerraformCommandResult, error) {
	if err := AssertSupportedTerraformExecutionPlatform(runtime.GOOS); err != nil {
		return TerraformCommandResult{}, err
	}
	if strings.IndexByte(options.TerraformExecutable, 0) >= 0 ||
		strings.IndexByte(options.CWD, 0) >= 0 ||
		!utf8.ValidString(options.TerraformExecutable) || !utf8.ValidString(options.CWD) ||
		!filepath.IsAbs(options.TerraformExecutable) || !filepath.IsAbs(options.CWD) {
		return TerraformCommandResult{}, domainFailure(
			"UNRESOLVED_TERRAFORM_COMMAND_PATH",
			"Terraform command requires resolved absolute paths",
		)
	}
	if !validOutputMode(options.Output) {
		return TerraformCommandResult{}, domainFailure(
			"INVALID_TERRAFORM_COMMAND_OUTPUT",
			"Terraform command output mode is not allowed",
		)
	}
	limitsValue := DefaultTerraformCommandLimits()
	if options.Limits != nil {
		limitsValue = *options.Limits
	}
	limits, err := SnapshotTerraformCommandLimits(limitsValue)
	if err != nil {
		return TerraformCommandResult{}, err
	}
	argv, err := snapshotArgv(options.Argv)
	if err != nil {
		return TerraformCommandResult{}, err
	}
	environment, err := SnapshotTerraformCommandEnvironment(options.Environment)
	if err != nil {
		return TerraformCommandResult{}, err
	}
	startedAt := time.Now()
	if err := requireTrustedExecutable(options.TerraformExecutable); err != nil {
		return TerraformCommandResult{}, err
	}
	if deadlineReached(startedAt, limits.TimeoutMs) {
		return TerraformCommandResult{}, commandTimeoutFailure()
	}

	stdoutReader, stdoutWriter, err := os.Pipe()
	if err != nil {
		return TerraformCommandResult{}, commandSpawnFailure()
	}
	stderrReader, stderrWriter, err := os.Pipe()
	if err != nil {
		_ = stdoutReader.Close()
		_ = stdoutWriter.Close()
		return TerraformCommandResult{}, commandSpawnFailure()
	}

	command := exec.Command(options.TerraformExecutable, argv...)
	command.Dir = options.CWD
	command.Env = sortedEnvironment(environment)
	command.Stdin = nil
	command.Stdout = stdoutWriter
	command.Stderr = stderrWriter
	configureTerraformProcess(command)
	unregister, err := startTerraformProcess(command)
	if err != nil {
		_ = stdoutReader.Close()
		_ = stdoutWriter.Close()
		_ = stderrReader.Close()
		_ = stderrWriter.Close()
		return TerraformCommandResult{}, commandSpawnFailure()
	}
	// The parent must not retain pipe writers: EOF is driven solely by the
	// isolated child process group.
	_ = stdoutWriter.Close()
	_ = stderrWriter.Close()

	pid := command.Process.Pid
	defer unregister()

	streamResults := make(chan commandStreamResult, 2)
	hostOutput := terraformCommandHostOutput
	var stdoutPump *commandHostOutputPump
	if options.Output == TerraformCommandOutputInherit {
		stdoutPump = newCommandHostOutputPump(hostOutput.stdout)
	}
	var stderrPump *commandHostOutputPump
	if options.Output == TerraformCommandOutputInherit || options.Output == TerraformCommandOutputInheritStderr {
		stderrPump = newCommandHostOutputPump(hostOutput.stderr)
	}
	go func() {
		streamResults <- readBoundedCommandStream(
			stdoutReader,
			limits.MaxStdoutBytes,
			options.Output == TerraformCommandOutputCapture,
			stdoutPump,
			true,
		)
	}()
	go func() {
		streamResults <- readBoundedCommandStream(
			stderrReader,
			limits.MaxStderrBytes,
			false,
			stderrPump,
			false,
		)
	}()
	waitResults := make(chan commandWaitResult, 1)
	go func() {
		waitErr := command.Wait()
		// Reap pipe-holding descendants immediately after the direct process
		// exits, including on its successful exit.
		killTerraformProcessGroup(pid, command.Process)
		waitResults <- commandWaitResult{err: waitErr}
	}()
	if terraformCommandStartedHook != nil {
		terraformCommandStartedHook()
	}

	var terminalFailure *procerr.ProcessFailure
	var stdoutBytes []byte
	var waitErr error
	streamsRemaining := 2
	waitRemaining := true
	var stdoutPumpDone <-chan struct{}
	if stdoutPump != nil {
		stdoutPumpDone = stdoutPump.done
	}
	var stderrPumpDone <-chan struct{}
	if stderrPump != nil {
		stderrPumpDone = stderrPump.done
	}
	abortHostOutput := func() {
		if stdoutPump != nil {
			stdoutPump.abort()
		}
		if stderrPump != nil {
			stderrPump.abort()
		}
	}
	// TypeScript's armTimeout checks remaining time synchronously immediately
	// after spawn. Do not let a fast successful child beat an already-expired
	// deadline while a Go timer is still waiting to be scheduled.
	if deadlineReached(startedAt, limits.TimeoutMs) {
		terminalFailure = commandTimeoutFailure()
		abortHostOutput()
		killTerraformProcessGroup(pid, command.Process)
	}
	var timer *time.Timer
	if terminalFailure == nil {
		timer = newCommandTimer(startedAt, limits.TimeoutMs)
	}
	if timer != nil {
		defer timer.Stop()
	}

	for streamsRemaining > 0 || waitRemaining ||
		(terminalFailure == nil && (stdoutPumpDone != nil || stderrPumpDone != nil)) {
		var timeout <-chan time.Time
		if timer != nil {
			timeout = timer.C
		}
		select {
		case stream := <-streamResults:
			streamsRemaining--
			if stream.err != nil && terminalFailure == nil {
				terminalFailure = commandStreamFailure(stream.stdout, stream.err)
				abortHostOutput()
				killTerraformProcessGroup(pid, command.Process)
			}
			if stream.stdout && stream.err == nil {
				stdoutBytes = stream.bytes
			}
		case wait := <-waitResults:
			waitRemaining = false
			// Node's close event follows both stdio streams. Defer exit-status
			// classification so a stream limit/read failure wins even when Go's
			// Wait result is selected first.
			waitErr = wait.err
		case <-stdoutPumpDone:
			stdoutPumpDone = nil
		case <-stderrPumpDone:
			stderrPumpDone = nil
		case <-timeout:
			if terminalFailure == nil && deadlineReached(startedAt, limits.TimeoutMs) {
				terminalFailure = commandTimeoutFailure()
				abortHostOutput()
				killTerraformProcessGroup(pid, command.Process)
			}
			if terminalFailure == nil {
				rearmCommandTimer(timer, startedAt, limits.TimeoutMs)
			} else {
				timer = nil
			}
		}
	}

	killTerraformProcessGroup(pid, command.Process)
	if terminalFailure == nil && waitErr != nil {
		var exitError *exec.ExitError
		if errors.As(waitErr, &exitError) {
			terminalFailure = domainFailure(
				"TERRAFORM_COMMAND_FAILED",
				"Terraform command did not complete successfully",
			)
		} else {
			terminalFailure = commandSpawnFailure()
		}
	}
	if terminalFailure != nil {
		clearBytes(stdoutBytes)
		return TerraformCommandResult{}, terminalFailure
	}
	switch options.Output {
	case TerraformCommandOutputInherit, TerraformCommandOutputInheritStderr:
		clearBytes(stdoutBytes)
		return TerraformCommandResult{Kind: TerraformCommandResultInherited}, nil
	case TerraformCommandOutputDiscard:
		clearBytes(stdoutBytes)
		return TerraformCommandResult{Kind: TerraformCommandResultDiscarded}, nil
	default:
		captured := append([]byte{}, stdoutBytes...)
		clearBytes(stdoutBytes)
		return TerraformCommandResult{Kind: TerraformCommandResultCaptured, Stdout: captured}, nil
	}
}

func validOutputMode(output TerraformCommandOutput) bool {
	return output == TerraformCommandOutputCapture ||
		output == TerraformCommandOutputDiscard ||
		output == TerraformCommandOutputInherit ||
		output == TerraformCommandOutputInheritStderr
}

func requireTrustedExecutable(filePath string) error {
	metadata, err := os.Lstat(filePath)
	if err != nil {
		return ioFailure(
			"UNTRUSTED_TERRAFORM_EXECUTABLE",
			"unable to inspect trusted Terraform executable",
		)
	}
	if !metadata.Mode().IsRegular() || runtime.GOOS != "windows" && metadata.Mode().Perm()&0o111 == 0 {
		return ioFailure(
			"UNTRUSTED_TERRAFORM_EXECUTABLE",
			"trusted Terraform executable is not an allowed regular file",
		)
	}
	return nil
}

func readBoundedCommandStream(
	reader *os.File,
	limit int64,
	capture bool,
	pump *commandHostOutputPump,
	stdout bool,
) commandStreamResult {
	defer reader.Close()
	if pump != nil {
		defer pump.finish()
	}
	buffer := make([]byte, commandReadBufferBytes)
	var captured []byte
	if capture {
		// Start small and let append grow toward the ceiling. The hard limit is
		// enforced solely by the count check below, never by this capacity, so an
		// output at limit+1 still fails identically to a full pre-reservation.
		initialCapacity := int64(commandCaptureInitialBytes)
		if limit < initialCapacity {
			initialCapacity = limit
		}
		captured = make([]byte, 0, int(initialCapacity))
	}
	var count int64
	for {
		read, err := reader.Read(buffer)
		if read > 0 {
			chunk := buffer[:read]
			if int64(read) > limit-count {
				clearBytes(captured)
				if stdout {
					return commandStreamResult{stdout: true, err: errCommandStdoutLimit}
				}
				return commandStreamResult{err: errCommandStderrLimit}
			}
			if capture {
				captured = append(captured, chunk...)
			}
			if pump != nil {
				pump.enqueue(append([]byte(nil), chunk...))
			}
			count += int64(read)
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return commandStreamResult{stdout: stdout, bytes: captured}
			}
			clearBytes(captured)
			if stdout {
				return commandStreamResult{stdout: true, err: errCommandStdoutRead}
			}
			return commandStreamResult{err: errCommandStderrRead}
		}
	}
}

func newCommandHostOutputPump(destination io.Writer) *commandHostOutputPump {
	pump := &commandHostOutputPump{
		destination: destination,
		done:        make(chan struct{}),
	}
	pump.cond = sync.NewCond(&pump.mu)
	go pump.write()
	return pump
}

func (p *commandHostOutputPump) enqueue(value []byte) {
	p.mu.Lock()
	if p.aborted || p.finished {
		p.mu.Unlock()
		clearBytes(value)
		return
	}
	p.queue = append(p.queue, value)
	p.cond.Signal()
	p.mu.Unlock()
}

func (p *commandHostOutputPump) finish() {
	p.mu.Lock()
	p.finished = true
	p.cond.Broadcast()
	p.mu.Unlock()
}

func (p *commandHostOutputPump) abort() {
	p.mu.Lock()
	if !p.aborted {
		p.aborted = true
		for _, value := range p.queue {
			clearBytes(value)
		}
		p.queue = nil
		p.cond.Broadcast()
	}
	p.mu.Unlock()
	// Do not wait for done: a Write admitted before this mutex boundary may be
	// permanently blocked, while no queued Write can be admitted afterward.
}

func (p *commandHostOutputPump) write() {
	defer close(p.done)
	for {
		value, ok := p.nextWrite()
		if !ok {
			return
		}
		writeCommandOutput(p.destination, value)
		clearBytes(value)
	}
}

func (p *commandHostOutputPump) nextWrite() ([]byte, bool) {
	p.mu.Lock()
	for {
		for len(p.queue) == 0 && !p.finished && !p.aborted {
			p.cond.Wait()
		}
		if p.aborted || (len(p.queue) == 0 && p.finished) {
			p.mu.Unlock()
			return nil, false
		}
		if hook := p.beforeWriteHandoffHook; hook != nil {
			// Tests use this one-shot point after work is observable but before
			// the mutex-linearized queued-to-in-flight transition.
			p.beforeWriteHandoffHook = nil
			p.mu.Unlock()
			hook()
			p.mu.Lock()
			continue
		}
		value := p.queue[0]
		p.queue[0] = nil
		p.queue = p.queue[1:]
		if len(p.queue) == 0 {
			p.queue = nil
		}
		p.mu.Unlock()
		return value, true
	}
}

func writeCommandOutput(destination io.Writer, value []byte) {
	for len(value) > 0 {
		written, err := destination.Write(value)
		if err != nil || written <= 0 {
			return
		}
		value = value[written:]
	}
}

type commandStreamError string

func (e commandStreamError) Error() string { return string(e) }

const (
	errCommandStdoutLimit commandStreamError = "stdout-limit"
	errCommandStderrLimit commandStreamError = "stderr-limit"
	errCommandStdoutRead  commandStreamError = "stdout-read"
	errCommandStderrRead  commandStreamError = "stderr-read"
)

func commandStreamFailure(stdout bool, err error) *procerr.ProcessFailure {
	switch err {
	case errCommandStdoutLimit:
		return ioFailure("TERRAFORM_COMMAND_STDOUT_LIMIT", "Terraform command exceeded its output limit")
	case errCommandStderrLimit:
		return ioFailure("TERRAFORM_COMMAND_STDERR_LIMIT", "Terraform command exceeded its diagnostic-output limit")
	case errCommandStdoutRead:
		return ioFailure("TERRAFORM_COMMAND_STDOUT_FAILED", "unable to read Terraform command output")
	case errCommandStderrRead:
		return ioFailure("TERRAFORM_COMMAND_STDERR_FAILED", "unable to read Terraform command diagnostic output")
	default:
		if stdout {
			return ioFailure("TERRAFORM_COMMAND_STDOUT_FAILED", "unable to read Terraform command output")
		}
		return ioFailure("TERRAFORM_COMMAND_STDERR_FAILED", "unable to read Terraform command diagnostic output")
	}
}

func commandTimeoutFailure() *procerr.ProcessFailure {
	return ioFailure("TERRAFORM_COMMAND_TIMEOUT", "Terraform command exceeded its execution deadline")
}

func commandSpawnFailure() *procerr.ProcessFailure {
	return ioFailure("TERRAFORM_COMMAND_SPAWN_FAILED", "unable to start Terraform command")
}

func deadlineReached(startedAt time.Time, timeoutMs *int64) bool {
	return timeoutMs != nil && elapsedMilliseconds(startedAt) >= *timeoutMs
}

func elapsedMilliseconds(startedAt time.Time) int64 {
	return int64(time.Since(startedAt) / time.Millisecond)
}

func newCommandTimer(startedAt time.Time, timeoutMs *int64) *time.Timer {
	if timeoutMs == nil {
		return nil
	}
	return time.NewTimer(commandTimerDuration(startedAt, timeoutMs))
}

func rearmCommandTimer(timer *time.Timer, startedAt time.Time, timeoutMs *int64) {
	timer.Reset(commandTimerDuration(startedAt, timeoutMs))
}

func commandTimerDuration(startedAt time.Time, timeoutMs *int64) time.Duration {
	remaining := *timeoutMs - elapsedMilliseconds(startedAt)
	if remaining < 1 {
		remaining = 1
	}
	if remaining > maximumTimerChunkMs {
		remaining = maximumTimerChunkMs
	}
	return time.Duration(remaining) * time.Millisecond
}

func clearBytes(value []byte) {
	for index := range value {
		value[index] = 0
	}
}
