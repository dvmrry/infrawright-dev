package sourcebind

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"sort"
	"strings"

	"github.com/dvmrry/infrawright-dev/go/internal/authoring/contracts"
)

// localGitRunner is deliberately narrow: it runs only the fixed local commands
// assembled below. It never opens a shell and no accepted command can contact a
// remote, install hooks, fetch, clone, or prompt. The parent process's PATH is
// trusted to select the Git executable; checkout-controlled configuration is
// disabled or overridden for every invocation.
type localGitRunner struct{}

func (localGitRunner) Run(ctx context.Context, directory string, arguments []string) (GitResult, error) {
	child, cancel := context.WithCancel(ctx)
	defer cancel()
	fixedArguments := []string{
		"--no-pager",
		"-C", directory,
		"--work-tree=" + directory,
		"-c", "core.fsmonitor=false",
		"-c", "core.hooksPath=/dev/null",
		"-c", "core.worktree=" + directory,
		"-c", "core.pager=cat",
	}
	command := exec.CommandContext(child, "git", append(fixedArguments, arguments...)...)
	command.Env = gitEnvironment()
	stdout := &boundedOutput{maximum: 64 * 1024, cancel: cancel}
	stderr := &boundedOutput{maximum: 64 * 1024, cancel: cancel}
	command.Stdout = stdout
	command.Stderr = stderr
	err := command.Run()
	result := GitResult{Stdout: stdout.Bytes(), Stderr: stderr.Bytes()}
	if stdout.exceeded || stderr.exceeded {
		return GitResult{}, errGitOutputLimit
	}
	if exit, ok := err.(*exec.ExitError); ok {
		result.ExitCode = exit.ExitCode()
		return result, nil
	}
	if err != nil {
		return GitResult{}, err
	}
	return result, nil
}

func gitEnvironment() []string {
	// Deliberately do not inherit GIT_*, proxy, credential, or HOME state from
	// the parent process. PATH is the only host lookup needed for the local git
	// executable selected by exec.CommandContext.
	return []string{
		"PATH=" + os.Getenv("PATH"),
		"LC_ALL=C",
		"GIT_TERMINAL_PROMPT=0",
		"GIT_ASKPASS=/bin/false",
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_SYSTEM=/dev/null",
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_CONFIG_COUNT=0",
		"GIT_PAGER=cat",
		"PAGER=cat",
	}
}

var errGitOutputLimit = errors.New("Git output exceeded bound")

type boundedOutput struct {
	maximum  int
	value    []byte
	exceeded bool
	cancel   context.CancelFunc
}

func (output *boundedOutput) Write(value []byte) (int, error) {
	if len(output.value)+len(value) > output.maximum {
		remaining := output.maximum - len(output.value)
		if remaining > 0 {
			output.value = append(output.value, value[:remaining]...)
		}
		output.exceeded = true
		output.cancel()
		return 0, errGitOutputLimit
	}
	output.value = append(output.value, value...)
	return len(value), nil
}

func (output *boundedOutput) Bytes() []byte { return append([]byte(nil), output.value...) }

func verifyGitSnapshot(ctx context.Context, roots LocalRoots, manifest contracts.SourceProvenance, options loadOptions) error {
	providerPaths := make([]string, 0, len(manifest.Provider.Files)+2)
	for _, file := range manifest.Provider.Files {
		providerPaths = append(providerPaths, file.Path)
	}
	providerPaths = append(providerPaths, manifest.ProviderModule.GoMod.Path)
	if manifest.ProviderModule.GoSum != nil {
		providerPaths = append(providerPaths, manifest.ProviderModule.GoSum.Path)
	}
	if err := verifyGitTree(ctx, roots.ProviderRoot, manifest.Provider.Revision, sortedUnique(providerPaths), options); err != nil {
		return failure(ErrorRevision, "provider", "local provider revision or bound-file cleanliness does not match manifest")
	}
	for _, binding := range manifest.SDKs {
		if binding.Revision == nil {
			continue
		}
		paths := make([]string, 0, len(binding.Files))
		for _, file := range binding.Files {
			paths = append(paths, file.Path)
		}
		if err := verifyGitTree(ctx, roots.SDKRoots[binding.ModulePath], *binding.Revision, sortedUnique(paths), options); err != nil {
			return failure(ErrorRevision, "sdks."+binding.ModulePath, "local SDK revision or bound-file cleanliness does not match manifest")
		}
	}
	return nil
}

func verifyGitTree(ctx context.Context, root, revision string, paths []string, options loadOptions) error {
	if err := verifyGitRevision(ctx, root, revision, options); err != nil {
		return err
	}
	tracked, err := runGit(ctx, root, append([]string{"ls-files", "--error-unmatch", "--"}, paths...), options)
	if err != nil || tracked.ExitCode != 0 {
		return failure(ErrorRevision, "git", "every bound path must be tracked")
	}
	arguments := append([]string{"status", "--porcelain=v1", "--untracked-files=no", "--"}, paths...)
	result, err := runGit(ctx, root, arguments, options)
	if err != nil || result.ExitCode != 0 || len(strings.TrimSpace(string(result.Stdout))) != 0 {
		return failure(ErrorRevision, "git", "bound tracked files are dirty")
	}
	return nil
}

func verifyGitRevision(ctx context.Context, root, revision string, options loadOptions) error {
	result, err := runGit(ctx, root, []string{"rev-parse", "--verify", "HEAD"}, options)
	if err != nil || result.ExitCode != 0 || strings.TrimSpace(string(result.Stdout)) != revision {
		return failure(ErrorRevision, "git", "HEAD does not match reviewed revision")
	}
	return nil
}

func runGit(parent context.Context, root string, arguments []string, options loadOptions) (GitResult, error) {
	ctx, cancel := context.WithTimeout(parent, options.timeout)
	defer cancel()
	result, err := options.gitRunner.Run(ctx, root, arguments)
	if err != nil || ctx.Err() != nil {
		return GitResult{}, failure(ErrorRevision, "git", "local Git verification did not complete")
	}
	if len(result.Stdout) > 64*1024 || len(result.Stderr) > 64*1024 {
		return GitResult{}, failure(ErrorRevision, "git", "local Git output exceeded the verification limit")
	}
	return result, nil
}

func sortedUnique(values []string) []string {
	// Manifest contracts already make paths unique, but module inputs join a
	// second set. The local Git argument list remains deterministic either way.
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}
