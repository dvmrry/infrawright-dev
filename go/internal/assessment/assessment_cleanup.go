package assessment

import (
	"errors"
	"os"
	"path/filepath"
	"sort"

	"github.com/dvmrry/infrawright-dev/go/internal/artifacts"
	"github.com/dvmrry/infrawright-dev/go/internal/procerr"
)

type assessmentCleanupIdentity struct {
	dev uint64
	ino uint64
}

type assessmentCleanupSnapshot struct {
	name     string
	identity assessmentCleanupIdentity
}

type assessmentCleanupHooks struct {
	afterDirectoryIdentity func() error
	beforeDirectoryRemoval func() error
	removeSnapshot         func(*os.Root, string) error
	removeDirectory        func(*os.Root, string) error
}

func assessmentCleanupFailure(code, message string) *procerr.ProcessFailure {
	return procerr.NewProcessFailure(procerr.NewProcessFailureOptions{
		Code:     code,
		Category: procerr.CategoryIO,
		Message:  message,
	})
}

func assessmentCleanupFailed() *procerr.ProcessFailure {
	return assessmentCleanupFailure(
		"ASSESSMENT_CLEANUP_FAILED",
		"unable to remove private assessment files",
	)
}

func assessmentCleanupRefused() *procerr.ProcessFailure {
	return assessmentCleanupFailure(
		"ASSESSMENT_CLEANUP_REFUSED",
		"private assessment directory changed before cleanup",
	)
}

func directorySafeIdentity(directory string) (assessmentCleanupIdentity, error) {
	info, err := os.Lstat(directory)
	if err != nil {
		return assessmentCleanupIdentity{}, err
	}
	if info == nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return assessmentCleanupIdentity{}, assessmentDomainFailure(
			"UNSAFE_SNAPSHOT_DIRECTORY",
			"assessment temporary directory is unsafe",
		)
	}
	identity, ok := assessmentCleanupFileIdentity(info)
	if !ok {
		return assessmentCleanupIdentity{}, assessmentCleanupFailed()
	}
	return identity, nil
}

func assessmentSnapshotCleanupBinding(
	directory string,
	snapshot artifacts.StableFileSnapshot,
) (assessmentCleanupSnapshot, error) {
	name := filepath.Base(snapshot.Path)
	if name == "." || name == string(filepath.Separator) ||
		filepath.Clean(filepath.Dir(snapshot.Path)) != filepath.Clean(directory) {
		return assessmentCleanupSnapshot{}, errors.New("assessment snapshot escaped its private directory")
	}
	return assessmentCleanupSnapshot{
		name: name,
		identity: assessmentCleanupIdentity{
			dev: snapshot.Dev,
			ino: snapshot.Ino,
		},
	}, nil
}

func invokeAssessmentCleanupHook(hook func() error) (err error) {
	if hook == nil {
		return nil
	}
	defer func() {
		if recover() != nil {
			err = errors.New("assessment cleanup hook panicked")
		}
	}()
	return hook()
}

func readAssessmentCleanupNames(root *os.Root) ([]string, error) {
	directory, err := root.Open(".")
	if err != nil {
		return nil, err
	}
	names, readErr := directory.Readdirnames(-1)
	closeErr := directory.Close()
	if readErr != nil {
		return nil, readErr
	}
	if closeErr != nil {
		return nil, closeErr
	}
	sort.Strings(names)
	return names, nil
}

func removeAssessmentCleanupName(
	root *os.Root,
	name string,
	hook func(*os.Root, string) error,
) (err error) {
	if hook == nil {
		return root.Remove(name)
	}
	defer func() {
		if recover() != nil {
			err = errors.New("assessment cleanup remove hook panicked")
		}
	}()
	return hook(root, name)
}

// cleanupAssessmentTemporaryDirectory binds the actual temporary directory and
// verifies that it contains exactly the zero-length snapshot inodes already
// scrubbed by CleanupSavedPlanEvidence. It then removes those verified names
// through the bound root and removes the empty directory through a bound parent
// root after rechecking its identity. Removal failures are best effort: they
// leave only scrubbed randomized remnants and cannot exhaust later runs.
func cleanupAssessmentTemporaryDirectory(
	directory string,
	expectedDirectory assessmentCleanupIdentity,
	expectedSnapshots []assessmentCleanupSnapshot,
	hooks assessmentCleanupHooks,
) (failure *procerr.ProcessFailure) {
	if !assessmentCleanupPlatformSupported {
		return assessmentCleanupFailed()
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return assessmentCleanupFailed()
	}
	validated := false
	closed := false
	defer func() {
		if closed {
			return
		}
		if err := root.Close(); err != nil && !validated && failure == nil {
			failure = assessmentCleanupFailed()
		}
	}()

	boundInfo, err := root.Stat(".")
	if err != nil || boundInfo == nil || !boundInfo.IsDir() || boundInfo.Mode()&os.ModeSymlink != 0 {
		return assessmentCleanupFailed()
	}
	boundIdentity, ok := assessmentCleanupFileIdentity(boundInfo)
	if !ok {
		return assessmentCleanupFailed()
	}
	if boundIdentity != expectedDirectory {
		return assessmentCleanupRefused()
	}
	if err := invokeAssessmentCleanupHook(hooks.afterDirectoryIdentity); err != nil {
		return assessmentCleanupFailed()
	}

	expected := make(map[string]assessmentCleanupIdentity, len(expectedSnapshots))
	for _, snapshot := range expectedSnapshots {
		if snapshot.name == "" {
			return assessmentCleanupRefused()
		}
		if _, duplicate := expected[snapshot.name]; duplicate {
			return assessmentCleanupRefused()
		}
		expected[snapshot.name] = snapshot.identity
	}
	names, err := readAssessmentCleanupNames(root)
	if err != nil || len(names) != len(expected) {
		return assessmentCleanupRefused()
	}
	for _, name := range names {
		expectedIdentity, exists := expected[name]
		if !exists {
			return assessmentCleanupRefused()
		}
		info, err := root.Lstat(name)
		if err != nil || info == nil || !info.Mode().IsRegular() || info.Size() != 0 {
			return assessmentCleanupRefused()
		}
		identity, ok := assessmentCleanupFileIdentity(info)
		if !ok || identity != expectedIdentity {
			return assessmentCleanupRefused()
		}
	}

	// The descriptor remains bound to the original directory if its pathname
	// was swapped. Observe the public path only to classify that race; no
	// destructive operation is ever issued through the path.
	pathInfo, err := os.Lstat(directory)
	if err != nil {
		return assessmentCleanupFailed()
	}
	pathIdentity, ok := assessmentCleanupFileIdentity(pathInfo)
	if !ok || pathInfo == nil || !pathInfo.IsDir() ||
		pathInfo.Mode()&os.ModeSymlink != 0 || pathIdentity != expectedDirectory {
		return assessmentCleanupRefused()
	}
	validated = true

	for _, name := range names {
		expectedIdentity := expected[name]
		info, err := root.Lstat(name)
		if err != nil || info == nil || !info.Mode().IsRegular() || info.Size() != 0 {
			return assessmentCleanupRefused()
		}
		identity, ok := assessmentCleanupFileIdentity(info)
		if !ok || identity != expectedIdentity {
			return assessmentCleanupRefused()
		}
		if err := removeAssessmentCleanupName(root, name, hooks.removeSnapshot); err != nil {
			return nil
		}
	}
	remaining, err := readAssessmentCleanupNames(root)
	if err != nil {
		return nil
	}
	if len(remaining) != 0 {
		return assessmentCleanupRefused()
	}
	pathInfo, err = os.Lstat(directory)
	if err != nil {
		return assessmentCleanupRefused()
	}
	pathIdentity, ok = assessmentCleanupFileIdentity(pathInfo)
	if !ok || pathInfo == nil || !pathInfo.IsDir() ||
		pathInfo.Mode()&os.ModeSymlink != 0 || pathIdentity != expectedDirectory {
		return assessmentCleanupRefused()
	}
	if err := root.Close(); err != nil {
		closed = true
		return nil
	}
	closed = true

	parent := filepath.Dir(directory)
	name := filepath.Base(directory)
	if name == "." || name == string(filepath.Separator) {
		return assessmentCleanupRefused()
	}
	parentRoot, err := os.OpenRoot(parent)
	if err != nil {
		return nil
	}
	defer func() { _ = parentRoot.Close() }()
	childInfo, err := parentRoot.Lstat(name)
	if err != nil || childInfo == nil || !childInfo.IsDir() || childInfo.Mode()&os.ModeSymlink != 0 {
		return assessmentCleanupRefused()
	}
	childIdentity, ok := assessmentCleanupFileIdentity(childInfo)
	if !ok || childIdentity != expectedDirectory {
		return assessmentCleanupRefused()
	}
	if err := invokeAssessmentCleanupHook(hooks.beforeDirectoryRemoval); err != nil {
		return nil
	}
	childInfo, err = parentRoot.Lstat(name)
	if err != nil || childInfo == nil || !childInfo.IsDir() || childInfo.Mode()&os.ModeSymlink != 0 {
		return assessmentCleanupRefused()
	}
	childIdentity, ok = assessmentCleanupFileIdentity(childInfo)
	if !ok || childIdentity != expectedDirectory {
		return assessmentCleanupRefused()
	}
	if err := removeAssessmentCleanupName(parentRoot, name, hooks.removeDirectory); err != nil {
		return nil
	}
	return nil
}
