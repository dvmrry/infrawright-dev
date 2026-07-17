package terraformcmd

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"unicode/utf8"
)

// ResolveTerraformExecutable resolves the --terraform/TF selection to a
// trusted, real, regular executable. An empty selected value falls back to
// the implicit "terraform" lookup; the flag-then-TF-env precedence above
// that is the caller's responsibility (pass "" through when neither is set).
//
// A requested value containing a path separator names an explicit location
// and is resolved directly with filepath.Abs/filepath.Clean, independent of
// environment's PATH. A bare command name is searched for across each PATH
// directory in order, mirroring exec.LookPath's directory walk; a distinct
// environment map (rather than the process's real environment) is accepted
// so callers can pin a deterministic search path without mutating global
// process state.
func ResolveTerraformExecutable(selected string, environment map[string]string) (string, error) {
	requested := selected
	if requested == "" {
		requested = "terraform"
	}
	if strings.IndexByte(requested, 0) >= 0 || !utf8.ValidString(requested) {
		return "", domainFailure(
			"UNRESOLVED_TERRAFORM_COMMAND_PATH",
			"Terraform executable path contains a null byte or is not valid UTF-8",
		)
	}

	candidates, err := terraformExecutableCandidates(requested, environment)
	if err != nil {
		return "", err
	}
	for _, candidate := range candidates {
		if err := executableAccess(candidate); err != nil {
			continue
		}
		resolved, err := filepath.EvalSymlinks(candidate)
		if err != nil {
			continue
		}
		if !utf8.ValidString(resolved) {
			continue
		}
		metadata, err := os.Lstat(resolved)
		if err != nil || !metadata.Mode().IsRegular() ||
			(runtime.GOOS != "windows" && metadata.Mode().Perm()&0o111 == 0) {
			continue
		}
		absolute, err := filepath.Abs(resolved)
		if err != nil {
			continue
		}
		return absolute, nil
	}
	return "", missingTerraformExecutableError(requested)
}

// terraformExecutableCandidates resolves requested to the ordered candidates
// ResolveTerraformExecutable probes: a single resolved path when requested
// names an explicit location (it contains a path separator), or one
// candidate per PATH directory, in order, when it names a bare command.
func terraformExecutableCandidates(requested string, environment map[string]string) ([]string, error) {
	if strings.ContainsRune(requested, filepath.Separator) {
		absolute, err := filepath.Abs(filepath.Clean(requested))
		if err != nil {
			return nil, ioFailure(
				"UNRESOLVED_TERRAFORM_COMMAND_PATH",
				"unable to resolve the Terraform executable path: "+err.Error(),
			)
		}
		return []string{absolute}, nil
	}
	pathValue, ok := environment["PATH"]
	if !ok {
		return nil, nil
	}
	directories := filepath.SplitList(pathValue)
	candidates := make([]string, 0, len(directories))
	for _, directory := range directories {
		if directory == "" {
			directory = "."
		}
		candidates = append(candidates, filepath.Join(directory, requested))
	}
	return candidates, nil
}

// missingTerraformExecutableError reports resolution exhaustion as a plain,
// actionable Go error. It wraps fs.ErrNotExist so callers can still classify
// the failure with errors.Is without depending on any particular wording.
func missingTerraformExecutableError(requested string) error {
	return fmt.Errorf("terraform executable not found: %q: %w", requested, fs.ErrNotExist)
}

func sortedEnvironment(environment map[string]string) []string {
	keys := make([]string, 0, len(environment))
	for key := range environment {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]string, 0, len(keys))
	for _, key := range keys {
		result = append(result, key+"="+environment[key])
	}
	return result
}
