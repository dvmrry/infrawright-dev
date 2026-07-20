package zpacorpus

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

const (
	gitOutputLimit  = 64 * 1024
	sourceFileLimit = 4 * 1024 * 1024
)

var errGitOutputLimit = errors.New("Git output exceeded bound")

type gitResult struct {
	stdout   []byte
	exitCode int
}

type gitRunner interface {
	run(context.Context, string, []string) (gitResult, error)
}

type localGitRunner struct{}

func (localGitRunner) run(parent context.Context, directory string, arguments []string) (gitResult, error) {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	command := exec.CommandContext(ctx, "git", fixedGitArguments(directory, arguments)...)
	command.Env = gitEnvironment()
	stdout := &boundedWriter{maximum: gitOutputLimit, cancel: cancel}
	stderr := &boundedWriter{maximum: gitOutputLimit, cancel: cancel}
	command.Stdout = stdout
	command.Stderr = stderr
	err := command.Run()
	if stdout.exceeded || stderr.exceeded {
		return gitResult{}, errGitOutputLimit
	}
	result := gitResult{stdout: stdout.bytes()}
	if exit, ok := err.(*exec.ExitError); ok {
		result.exitCode = exit.ExitCode()
		return result, nil
	}
	if err != nil {
		return gitResult{}, fmt.Errorf("run local Git command: %w", err)
	}
	return result, nil
}

func fixedGitArguments(directory string, arguments []string) []string {
	fixed := []string{
		"--no-pager",
		"-C", directory,
		"--work-tree=" + directory,
		"-c", "core.fsmonitor=false",
		"-c", "core.hooksPath=/dev/null",
		"-c", "core.worktree=" + directory,
		"-c", "core.pager=cat",
	}
	return append(fixed, arguments...)
}

func gitEnvironment() []string {
	return []string{
		"PATH=" + os.Getenv("PATH"),
		"LC_ALL=C",
		"GIT_TERMINAL_PROMPT=0",
		"GIT_ASKPASS=/bin/false",
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_SYSTEM=/dev/null",
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_CONFIG_COUNT=0",
		"GIT_OPTIONAL_LOCKS=0",
		"GIT_PAGER=cat",
		"PAGER=cat",
	}
}

type boundedWriter struct {
	maximum  int
	value    []byte
	exceeded bool
	cancel   context.CancelFunc
}

func (writer *boundedWriter) Write(value []byte) (int, error) {
	if len(writer.value)+len(value) > writer.maximum {
		remaining := writer.maximum - len(writer.value)
		if remaining > 0 {
			writer.value = append(writer.value, value[:remaining]...)
		}
		writer.exceeded = true
		writer.cancel()
		return 0, errGitOutputLimit
	}
	writer.value = append(writer.value, value...)
	return len(value), nil
}

func (writer *boundedWriter) bytes() []byte {
	return append([]byte(nil), writer.value...)
}

func verifyProviderSource(parent context.Context, report corpusReport, providerRoot string, runner gitRunner) error {
	absolute, err := filepath.Abs(providerRoot)
	if err != nil {
		return fmt.Errorf("resolve provider root: %w", err)
	}
	rootInfo, err := os.Lstat(absolute)
	if err != nil || !rootInfo.IsDir() || rootInfo.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("provider root must be an explicit non-symlink directory")
	}
	paths := make([]string, len(report.Provider.SourceFiles))
	for i, source := range report.Provider.SourceFiles {
		paths[i] = source.Path
	}
	if err := verifyGitSnapshot(parent, absolute, paths, runner, true); err != nil {
		return err
	}

	directory, err := os.OpenRoot(absolute)
	if err != nil {
		return fmt.Errorf("open provider root: %w", err)
	}
	defer directory.Close() // The validation result is already decided when close runs.
	openedRootInfo, err := directory.Stat(".")
	if err != nil || !os.SameFile(rootInfo, openedRootInfo) {
		return fmt.Errorf("provider root identity changed before source read")
	}
	contents := make(map[string][]byte, len(paths))
	for _, source := range report.Provider.SourceFiles {
		data, err := readBoundSourceFile(directory, source.Path)
		if err != nil {
			return fmt.Errorf("provider source binding failed for %q: %w", source.Path, err)
		}
		if digest(data) != source.SHA256 {
			return fmt.Errorf("provider source binding failed for %q", source.Path)
		}
		contents[source.Path] = data
	}
	for _, resource := range report.Resources {
		for _, anchor := range claimAnchors(resource) {
			data, ok := contents[anchor.Path]
			if !ok {
				return fmt.Errorf("source range lacks whole-file binding for %q", anchor.Path)
			}
			selected, err := inclusiveLineRange(data, anchor.StartLine, anchor.EndLine)
			if err != nil {
				return fmt.Errorf("provider source range %s:%d-%d: %w", anchor.Path, anchor.StartLine, anchor.EndLine, err)
			}
			if digest(selected) != anchor.SHA256 {
				return fmt.Errorf("provider source range binding failed for %s:%d-%d", anchor.Path, anchor.StartLine, anchor.EndLine)
			}
		}
	}
	currentRootInfo, err := os.Lstat(absolute)
	if err != nil || !os.SameFile(openedRootInfo, currentRootInfo) {
		return fmt.Errorf("provider root pathname changed during source read")
	}
	if err := verifyGitSnapshot(parent, absolute, paths, runner, false); err != nil {
		return fmt.Errorf("post-read provider snapshot: %w", err)
	}
	return nil
}

func verifyGitSnapshot(parent context.Context, root string, paths []string, runner gitRunner, requireTracked bool) error {
	ctx, cancel := context.WithTimeout(parent, 15*time.Second)
	defer cancel()
	head, err := runner.run(ctx, root, []string{"rev-parse", "--verify", "HEAD"})
	if err != nil || head.exitCode != 0 || strings.TrimSpace(string(head.stdout)) != providerCommit {
		return fmt.Errorf("provider checkout is not the pinned commit")
	}
	tag, err := runner.run(ctx, root, []string{"rev-parse", "--verify", "refs/tags/" + providerRef + "^{commit}"})
	if err != nil || tag.exitCode != 0 || strings.TrimSpace(string(tag.stdout)) != providerCommit {
		return fmt.Errorf("provider tag does not resolve to the pinned commit")
	}
	if requireTracked {
		arguments := append([]string{"ls-files", "--error-unmatch", "--"}, paths...)
		tracked, err := runner.run(ctx, root, arguments)
		if err != nil || tracked.exitCode != 0 {
			return fmt.Errorf("every matrix-bound provider source must be tracked")
		}
	}
	arguments := append([]string{"status", "--porcelain=v1", "--untracked-files=no", "--"}, paths...)
	status, err := runner.run(ctx, root, arguments)
	if err != nil || status.exitCode != 0 || len(status.stdout) != 0 {
		return fmt.Errorf("matrix-bound tracked provider sources are dirty")
	}
	return nil
}

func readBoundSourceFile(root *os.Root, path string) ([]byte, error) {
	if err := validateRelativePath(path); err != nil {
		return nil, err
	}
	pathInfo, err := root.Lstat(path)
	if err != nil || !pathInfo.Mode().IsRegular() {
		return nil, fmt.Errorf("bound source is not a regular file")
	}
	file, err := root.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close() // Reads are complete before the return value is consumed.
	openedInfo, err := file.Stat()
	if err != nil || !openedInfo.Mode().IsRegular() || !os.SameFile(pathInfo, openedInfo) {
		return nil, fmt.Errorf("bound source identity changed before read")
	}
	data, err := io.ReadAll(io.LimitReader(file, sourceFileLimit+1))
	if err != nil {
		return nil, err
	}
	if len(data) > sourceFileLimit {
		return nil, fmt.Errorf("bound source exceeds %d bytes", sourceFileLimit)
	}
	afterRead, err := file.Stat()
	if err != nil || !os.SameFile(openedInfo, afterRead) || openedInfo.Size() != afterRead.Size() || !openedInfo.ModTime().Equal(afterRead.ModTime()) {
		return nil, fmt.Errorf("bound source changed during read")
	}
	currentInfo, err := root.Lstat(path)
	if err != nil || !os.SameFile(openedInfo, currentInfo) {
		return nil, fmt.Errorf("bound source pathname changed during read")
	}
	return data, nil
}

func inclusiveLineRange(data []byte, startLine, endLine int) ([]byte, error) {
	if startLine < 1 || endLine < startLine {
		return nil, fmt.Errorf("invalid inclusive line range")
	}
	lines := splitLinesWithEndings(data)
	if endLine > len(lines) {
		return nil, fmt.Errorf("range exceeds file with %d lines", len(lines))
	}
	var size int
	for _, line := range lines[startLine-1 : endLine] {
		size += len(line)
	}
	selected := make([]byte, 0, size)
	for _, line := range lines[startLine-1 : endLine] {
		selected = append(selected, line...)
	}
	return selected, nil
}

func splitLinesWithEndings(data []byte) [][]byte {
	var result [][]byte
	start := 0
	for i, value := range data {
		if value == '\n' {
			result = append(result, data[start:i+1])
			start = i + 1
		}
	}
	if start < len(data) {
		result = append(result, data[start:])
	}
	return result
}

type fakeGitRunner struct {
	head     string
	tag      string
	status   string
	listExit int
	calls    [][]string
}

func (runner *fakeGitRunner) run(_ context.Context, _ string, arguments []string) (gitResult, error) {
	runner.calls = append(runner.calls, append([]string(nil), arguments...))
	switch arguments[0] {
	case "rev-parse":
		value := runner.tag
		if arguments[len(arguments)-1] == "HEAD" {
			value = runner.head
		}
		return gitResult{stdout: []byte(value + "\n")}, nil
	case "ls-files":
		return gitResult{exitCode: runner.listExit}, nil
	case "status":
		return gitResult{stdout: []byte(runner.status)}, nil
	default:
		return gitResult{}, fmt.Errorf("unexpected Git command %q", arguments[0])
	}
}

func sourceFixtureReport(path string, data []byte) corpusReport {
	lines := splitLinesWithEndings(data)
	anchor := func(line int) sourceAnchor {
		return sourceAnchor{
			EndLine:   line,
			Function:  fmt.Sprintf("line%d", line),
			Path:      path,
			SHA256:    digest(lines[line-1]),
			StartLine: line,
			URL:       fmt.Sprintf("%s/blob/%s/%s#L%d-L%d", providerRepo, providerRef, path, line, line),
		}
	}
	return corpusReport{
		Provider: providerBinding{SourceFiles: []fileBinding{{Path: path, SHA256: digest(data)}}},
		Resources: []resourceClaim{{SourceEvidence: sourceEvidence{
			Exceptions:   map[string]sourceAnchor{},
			Importer:     anchor(1),
			ReadIdentity: anchor(2),
		}}},
	}
}

func TestProviderSourceBindingUsesPinnedCleanTrackedFilesAndInclusiveRanges(t *testing.T) {
	root := t.TempDir()
	path := "zpa/resource.go"
	data := []byte("line one\nline two\n")
	if err := os.Mkdir(filepath.Join(root, "zpa"), 0o700); err != nil {
		t.Fatalf("os.Mkdir(zpa) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(path)), data, 0o600); err != nil {
		t.Fatalf("os.WriteFile(%q) error = %v", path, err)
	}
	report := sourceFixtureReport(path, data)
	runner := &fakeGitRunner{head: providerCommit, tag: providerCommit}
	if err := verifyProviderSource(context.Background(), report, root, runner); err != nil {
		t.Fatalf("verifyProviderSource(valid fixture) error = %v", err)
	}
	wantStatus := []string{"status", "--porcelain=v1", "--untracked-files=no", "--", path}
	if !containsArguments(runner.calls, wantStatus) {
		t.Errorf("Git calls = %v, want path-scoped tracked-only status %v", runner.calls, wantStatus)
	}

	t.Run("whole_file", func(t *testing.T) {
		if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(path)), []byte("changed\n"), 0o600); err != nil {
			t.Fatalf("os.WriteFile(changed source) error = %v", err)
		}
		if err := verifyProviderSource(context.Background(), report, root, runner); err == nil {
			t.Error("verifyProviderSource(changed whole file) error = nil, want rejection")
		}
		if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(path)), data, 0o600); err != nil {
			t.Fatalf("os.WriteFile(restored source) error = %v", err)
		}
	})

	t.Run("inclusive_range", func(t *testing.T) {
		mutated := report
		mutated.Resources = append([]resourceClaim(nil), report.Resources...)
		mutated.Resources[0].SourceEvidence.Importer.SHA256 = strings.Repeat("0", 64)
		if err := verifyProviderSource(context.Background(), mutated, root, runner); err == nil {
			t.Error("verifyProviderSource(changed range hash) error = nil, want rejection")
		}
	})
}

func TestProviderSourceBindingRejectsGitAuthorityDrift(t *testing.T) {
	root := t.TempDir()
	path := "source.go"
	data := []byte("one\ntwo\n")
	if err := os.WriteFile(filepath.Join(root, path), data, 0o600); err != nil {
		t.Fatalf("os.WriteFile(source) error = %v", err)
	}
	report := sourceFixtureReport(path, data)
	tests := []struct {
		name   string
		runner fakeGitRunner
	}{
		{name: "head", runner: fakeGitRunner{head: strings.Repeat("0", 40), tag: providerCommit}},
		{name: "tag", runner: fakeGitRunner{head: providerCommit, tag: strings.Repeat("1", 40)}},
		{name: "untracked", runner: fakeGitRunner{head: providerCommit, tag: providerCommit, listExit: 1}},
		{name: "dirty_bound_file", runner: fakeGitRunner{head: providerCommit, tag: providerCommit, status: " M source.go\x00"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runner := test.runner
			if err := verifyProviderSource(context.Background(), report, root, &runner); err == nil {
				t.Errorf("verifyProviderSource(%s Git drift) error = nil, want rejection", test.name)
			}
		})
	}
}

func containsArguments(calls [][]string, want []string) bool {
	for _, call := range calls {
		if reflect.DeepEqual(call, want) {
			return true
		}
	}
	return false
}

func TestLocalGitRunnerPinsWorktreeAndDisablesCheckoutExecution(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("checkout execution sentinel uses a POSIX test script")
	}
	root := t.TempDir()
	runTestGit(t, "init", "--quiet", root)
	if err := os.WriteFile(filepath.Join(root, "bound.go"), []byte("package bound\n"), 0o600); err != nil {
		t.Fatalf("os.WriteFile(bound.go) error = %v", err)
	}
	runTestGit(t, "-C", root, "add", "--", "bound.go")

	hook := filepath.Join(root, ".git", "fsmonitor-hook")
	if err := os.WriteFile(hook, []byte("#!/bin/sh\ntouch \"${0}.ran\"\nexit 1\n"), 0o700); err != nil {
		t.Fatalf("os.WriteFile(fsmonitor hook) error = %v", err)
	}
	runTestGit(t, "-C", root, "config", "core.fsmonitor", hook)
	foreign := filepath.Join(t.TempDir(), "foreign-worktree")
	if err := os.Mkdir(foreign, 0o700); err != nil {
		t.Fatalf("os.Mkdir(foreign worktree) error = %v", err)
	}
	runTestGit(t, "-C", root, "config", "core.worktree", foreign)
	t.Setenv("GIT_DIR", filepath.Join(t.TempDir(), "foreign-git-dir"))

	result, err := (localGitRunner{}).run(context.Background(), root, []string{
		"status", "--porcelain=v1", "--untracked-files=no", "--", "bound.go",
	})
	if err != nil || result.exitCode != 0 {
		t.Fatalf("localGitRunner.status() = %+v, %v; want successful pinned-worktree read", result, err)
	}
	if _, err := os.Stat(hook + ".ran"); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("checkout fsmonitor sentinel stat error = %v, want not-exist", err)
	}

	wantPrefix := []string{
		"--no-pager",
		"-C", root,
		"--work-tree=" + root,
		"-c", "core.fsmonitor=false",
		"-c", "core.hooksPath=/dev/null",
		"-c", "core.worktree=" + root,
		"-c", "core.pager=cat",
	}
	got := fixedGitArguments(root, []string{"status"})
	if !reflect.DeepEqual(got[:len(wantPrefix)], wantPrefix) {
		t.Errorf("fixedGitArguments(%q) prefix = %v, want %v", root, got[:len(wantPrefix)], wantPrefix)
	}
}

func runTestGit(t *testing.T, arguments ...string) {
	t.Helper()
	command := exec.Command("git", arguments...)
	command.Env = gitEnvironment()
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %v error = %v; output = %s", arguments, err, output)
	}
}

func TestOptionalExternalZPAProviderCheckout(t *testing.T) {
	providerRoot, present := os.LookupEnv("ZPA_PROVIDER_SOURCE")
	if !present || strings.TrimSpace(providerRoot) == "" {
		t.Skip("set ZPA_PROVIDER_SOURCE to audit the external pinned checkout")
	}
	matrixBytes, report := readMatrix(t)
	if got := digest(matrixBytes); got != matrixSHA256 {
		t.Fatalf("digest(ZPA matrix) = %q, want frozen %q", got, matrixSHA256)
	}
	repository := repositoryRoot(t)
	if err := validateLocalCorpus(report, repository, filepath.Join(repository, "packs")); err != nil {
		t.Fatalf("validateLocalCorpus(committed matrix) error = %v", err)
	}
	if err := verifyProviderSource(context.Background(), report, providerRoot, localGitRunner{}); err != nil {
		t.Fatalf("verifyProviderSource(ZPA_PROVIDER_SOURCE) error = %v", err)
	}
}
