package plan

import (
	"errors"
	"io"
	"os"
	"path"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/dvmrry/infrawright-dev/go/internal/artifacts"
	"github.com/dvmrry/infrawright-dev/go/internal/canonjson"
	"github.com/dvmrry/infrawright-dev/go/internal/posixpath"
	"github.com/dvmrry/infrawright-dev/go/internal/procerr"
)

func fingerprintIOFailure(code, message string) *procerr.ProcessFailure {
	return procerr.NewProcessFailure(procerr.NewProcessFailureOptions{
		Code:     code,
		Category: procerr.CategoryIO,
		Message:  message,
	})
}

func invalidFilenameEncodingFailure() *procerr.ProcessFailure {
	return fingerprintIOFailure(
		"INVALID_FILENAME_ENCODING",
		"fingerprint input name is not valid UTF-8",
	)
}

func directoryReadFailure() *procerr.ProcessFailure {
	return fingerprintIOFailure(
		"DIRECTORY_READ_FAILED",
		"unable to enumerate fingerprint inputs",
	)
}

func isDirectory(filePath string) bool {
	info, err := os.Stat(filePath)
	return err == nil && info.IsDir()
}

func isFile(filePath string) bool {
	info, err := os.Stat(filePath)
	return err == nil && info.Mode().IsRegular()
}

func isSymbolicLink(filePath string) bool {
	info, err := os.Lstat(filePath)
	return err == nil && info.Mode()&os.ModeSymlink != 0
}

func isModuleFingerprintIgnoredDir(name string) bool {
	switch name {
	case ".git", ".mypy_cache", ".pytest_cache", ".ruff_cache", ".terraform", "__pycache__":
		return true
	default:
		return false
	}
}

// fingerprintBudget gives each exported fingerprint operation the source
// function's default ReadBudget behavior while preserving a caller-supplied
// budget across every composed read.
func fingerprintBudget(budget *artifacts.ReadBudget) *artifacts.ReadBudget {
	if budget == nil {
		return artifacts.NewDefaultReadBudget()
	}
	return budget
}

// directoryNames preserves the host directory-stream order while charging
// the shared budget serially. Every caller sorts before an observable emit or
// recursive descent, matching plan-fingerprint.ts.
func directoryNames(directory string, budget *artifacts.ReadBudget, depth int) ([]string, error) {
	if err := budget.EnterDirectory(depth); err != nil {
		return nil, err
	}
	handle, err := os.Open(directory)
	if err != nil {
		return nil, directoryReadFailure()
	}
	defer func() {
		// Directory close failure cannot change a completed enumeration.
		_ = handle.Close()
	}()

	names := make([]string, 0)
	for {
		entries, readErr := handle.ReadDir(1)
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return nil, directoryReadFailure()
		}
		for _, entry := range entries {
			if err := budget.ReserveDirectoryEntry(); err != nil {
				return nil, err
			}
			name := entry.Name()
			if !utf8.ValidString(name) {
				return nil, invalidFilenameEncodingFailure()
			}
			names = append(names, name)
		}
	}
	return names, nil
}

func fileSHA256(filePath string, budget *artifacts.ReadBudget) (string, error) {
	digest, err := artifacts.SHA256StableFile(filePath, budget, artifacts.StableReadOptions{
		FollowSymlinks: true,
	})
	if err != nil {
		return "", err
	}
	return digest.SHA256, nil
}

func isPlanInput(name string) bool {
	return strings.HasSuffix(name, ".tf") ||
		strings.HasSuffix(name, ".tf.json") ||
		name == ".terraform.lock.hcl" ||
		name == "terraform.tfvars" ||
		name == "terraform.tfvars.json" ||
		strings.HasSuffix(name, ".auto.tfvars") ||
		strings.HasSuffix(name, ".auto.tfvars.json")
}

// RootTFFingerprints ports rootTfFingerprints from
// the original implementation. A nil budget selects the source default.
func RootTFFingerprints(envDir string, budget *artifacts.ReadBudget) ([]FileFingerprint, error) {
	budget = fingerprintBudget(budget)
	out := make([]FileFingerprint, 0)
	if !isDirectory(envDir) {
		return out, nil
	}
	names, err := directoryNames(envDir, budget, 0)
	if err != nil {
		return nil, err
	}
	for _, name := range canonjson.SortedStrings(names) {
		if !isPlanInput(name) {
			continue
		}
		filePath := posixpath.Join(envDir, name)
		if !isFile(filePath) {
			continue
		}
		digest, err := fileSHA256(filePath, budget)
		if err != nil {
			return nil, err
		}
		out = append(out, FileFingerprint{name, digest})
	}
	return out, nil
}

// RootConfigFingerprints ports rootConfigFingerprints from
// the original implementation. A nil budget selects the source default.
func RootConfigFingerprints(envDir string, budget *artifacts.ReadBudget) ([]FileFingerprint, error) {
	budget = fingerprintBudget(budget)
	fingerprints, err := RootTFFingerprints(envDir, budget)
	if err != nil {
		return nil, err
	}
	out := make([]FileFingerprint, 0, len(fingerprints))
	for _, fingerprint := range fingerprints {
		name := fingerprint[0]
		if strings.HasSuffix(name, ".tf") || strings.HasSuffix(name, ".tf.json") {
			out = append(out, fingerprint)
		}
	}
	return out, nil
}

func walkTree(
	root string,
	current string,
	out *[]FileFingerprint,
	budget *artifacts.ReadBudget,
	depth int,
) error {
	names, err := directoryNames(current, budget, depth)
	if err != nil {
		return err
	}

	directories := make([]string, 0)
	files := make([]string, 0)
	for _, name := range names {
		filePath := posixpath.Join(current, name)
		if isDirectory(filePath) {
			if !isModuleFingerprintIgnoredDir(name) {
				directories = append(directories, name)
			}
		} else {
			files = append(files, name)
		}
	}

	for _, name := range canonjson.SortedStrings(files) {
		filePath := posixpath.Join(current, name)
		if !isFile(filePath) {
			continue
		}
		relativePath := strings.TrimPrefix(filePath, strings.TrimSuffix(root, "/")+"/")
		digest, err := fileSHA256(filePath, budget)
		if err != nil {
			return err
		}
		*out = append(*out, FileFingerprint{relativePath, digest})
	}

	for _, name := range canonjson.SortedStrings(directories) {
		directoryPath := posixpath.Join(current, name)
		// Python os.walk(..., followlinks=False) lists directory symlinks but
		// does not recurse into them. A symlink supplied as root is traversed.
		if !isSymbolicLink(directoryPath) {
			if err := walkTree(root, directoryPath, out, budget, depth+1); err != nil {
				return err
			}
		}
	}
	return nil
}

// TreeFingerprints ports treeFingerprints from
// the original implementation. A nil budget selects the source default.
func TreeFingerprints(root string, budget *artifacts.ReadBudget) ([]FileFingerprint, error) {
	budget = fingerprintBudget(budget)
	out := make([]FileFingerprint, 0)
	if !isDirectory(root) {
		return out, nil
	}
	if err := walkTree(root, root, &out, budget, 0); err != nil {
		return nil, err
	}
	return out, nil
}

// LocalModulePath ports localModulePath from
// the original implementation. The bool reports whether source names a
// local module.
func LocalModulePath(envDir, source string) (string, bool) {
	if source == "" {
		return "", false
	}
	if source[0] == '/' {
		return posixpath.Normalize(source), true
	}
	if strings.HasPrefix(source, "./") || strings.HasPrefix(source, "../") {
		return posixpath.Normalize(posixpath.Join(envDir, source)), true
	}
	return "", false
}

// BackendConfigFingerprint ports backendFingerprint from
// the original implementation. A nil budget selects the source default.
func BackendConfigFingerprint(
	backendConfig *string,
	backendKey *string,
	budget *artifacts.ReadBudget,
) (*BackendFingerprint, error) {
	budget = fingerprintBudget(budget)
	if backendConfig == nil || *backendConfig == "" {
		return nil, nil
	}
	cwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	filePath := posixpath.Absolute(*backendConfig, cwd)
	present := isFile(filePath)
	result := &BackendFingerprint{
		Key:     cloneStringPointer(backendKey),
		Present: present,
	}
	if !present {
		return result, nil
	}
	digest, err := fileSHA256(filePath, budget)
	if err != nil {
		return nil, err
	}
	result.SHA256 = &digest
	return result, nil
}

// VarFileFingerprints ports varFileFingerprints from
// the original implementation. A nil budget selects the source default.
func VarFileFingerprints(varFiles []string, budget *artifacts.ReadBudget) ([]FileFingerprint, error) {
	budget = fingerprintBudget(budget)
	sorted := append([]string(nil), varFiles...)
	sort.SliceStable(sorted, func(left, right int) bool {
		return canonjson.ComparePythonStrings(path.Base(sorted[left]), path.Base(sorted[right])) < 0
	})
	out := make([]FileFingerprint, 0, len(sorted))
	for _, filePath := range sorted {
		if !isFile(filePath) {
			continue
		}
		digest, err := fileSHA256(filePath, budget)
		if err != nil {
			return nil, err
		}
		out = append(out, FileFingerprint{path.Base(filePath), digest})
	}
	return out, nil
}

func cloneStringPointer(value *string) *string {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}
