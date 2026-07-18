package assessment

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/dvmrry/infrawright-dev/go/internal/plan"
	"github.com/dvmrry/infrawright-dev/go/internal/procerr"
)

func TestMain(m *testing.M) {
	temporaryRoot, err := os.MkdirTemp("", "infrawright-assessment-tests-")
	if err != nil {
		fmt.Fprintf(os.Stderr, "create assessment test temporary root: %v\n", err)
		os.Exit(2)
	}
	previous, hadPrevious := os.LookupEnv("TMPDIR")
	if err := os.Setenv("TMPDIR", temporaryRoot); err != nil {
		fmt.Fprintf(os.Stderr, "set assessment test temporary root: %v\n", err)
		_ = os.RemoveAll(temporaryRoot)
		os.Exit(2)
	}

	code := m.Run()
	if hadPrevious {
		_ = os.Setenv("TMPDIR", previous)
	} else {
		_ = os.Unsetenv("TMPDIR")
	}
	if err := os.RemoveAll(temporaryRoot); err != nil && code == 0 {
		fmt.Fprintf(os.Stderr, "remove assessment test temporary root: %v\n", err)
		code = 2
	}
	os.Exit(code)
}

func requireAssessmentTemporaryUnavailable(t *testing.T, err error) {
	t.Helper()
	var failure *procerr.ProcessFailure
	if !errors.As(err, &failure) {
		t.Fatalf("error = %T(%v), want *procerr.ProcessFailure", err, err)
	}
	if failure.Code != "ASSESSMENT_TEMPORARY_DIRECTORY_UNAVAILABLE" ||
		failure.Category != procerr.CategoryIO ||
		failure.Message != "unable to create private assessment directory" ||
		failure.Retryable || len(failure.Details) != 0 {
		t.Errorf("failure = %+v, want fixed redacted temporary-directory failure", failure)
	}
}

func requirePrivateAssessmentDirectory(t *testing.T, path string) os.FileInfo {
	t.Helper()
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("os.Lstat(%q) error = %v, want nil", path, err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0o077 != 0 {
		t.Errorf("assessment temporary directory %q mode = %v, want non-symlink directory no broader than 0700", path, info.Mode())
	}
	return info
}

func readAssessmentTemporaryEntries(t *testing.T, root string) []os.DirEntry {
	t.Helper()
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("os.ReadDir(%q) error = %v, want nil", root, err)
	}
	return entries
}

func TestMakeAssessmentTemporaryDirectoryCreatesRandomPrivateDirectories(t *testing.T) {
	root := t.TempDir()
	const count = 48
	created := make(map[string]os.FileInfo, count)
	for index := 0; index < count; index++ {
		path, err := makeAssessmentTemporaryDirectory(root)
		if err != nil {
			t.Fatalf("makeAssessmentTemporaryDirectory(run %d) error = %v, want nil", index+1, err)
		}
		if filepath.Dir(path) != root || !strings.HasPrefix(filepath.Base(path), assessmentTemporaryDirectoryPrefix) {
			t.Errorf("makeAssessmentTemporaryDirectory(run %d) path = %q, want randomized child of %q with prefix %q", index+1, path, root, assessmentTemporaryDirectoryPrefix)
		}
		if _, duplicate := created[path]; duplicate {
			t.Errorf("makeAssessmentTemporaryDirectory(run %d) path = %q, want unique name", index+1, path)
		}
		info := requirePrivateAssessmentDirectory(t, path)
		for previousPath, previousInfo := range created {
			if os.SameFile(previousInfo, info) {
				t.Errorf("assessment temporary directories %q and %q share an inode, want distinct claims", previousPath, path)
			}
		}
		created[path] = info
	}
	if len(created) != count {
		t.Errorf("random assessment directory count = %d, want %d", len(created), count)
	}
	for path := range created {
		if err := os.Remove(path); err != nil {
			t.Errorf("os.Remove(test-owned empty directory %q) error = %v, want nil", path, err)
		}
	}
}

func TestMakeAssessmentTemporaryDirectoryIsConcurrent(t *testing.T) {
	root := t.TempDir()
	const workers = 64
	type result struct {
		path string
		err  error
	}
	start := make(chan struct{})
	// One result per bounded worker lets every goroutine terminate before the
	// test goroutine begins validation.
	results := make(chan result, workers)
	var wait sync.WaitGroup
	for worker := 0; worker < workers; worker++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			path, err := makeAssessmentTemporaryDirectory(root)
			results <- result{path: path, err: err}
		}()
	}
	close(start)
	wait.Wait()
	close(results)

	created := make(map[string]struct{}, workers)
	for result := range results {
		if result.err != nil {
			t.Errorf("makeAssessmentTemporaryDirectory(concurrent) error = %v, want nil", result.err)
			continue
		}
		if _, duplicate := created[result.path]; duplicate {
			t.Errorf("makeAssessmentTemporaryDirectory(concurrent) path = %q, want unique name", result.path)
		}
		created[result.path] = struct{}{}
		requirePrivateAssessmentDirectory(t, result.path)
	}
	if len(created) != workers {
		t.Errorf("concurrent assessment directory count = %d, want %d", len(created), workers)
	}
	for path := range created {
		if err := os.Remove(path); err != nil {
			t.Errorf("os.Remove(test-owned concurrent directory %q) error = %v, want nil", path, err)
		}
	}
}

func TestMakeAssessmentTemporaryDirectoryDoesNotReuseRetainedRemnant(t *testing.T) {
	root := t.TempDir()
	retained := filepath.Join(root, assessmentTemporaryDirectoryPrefix+"retained")
	if err := os.Mkdir(retained, 0o700); err != nil {
		t.Fatalf("os.Mkdir(retained remnant) error = %v, want nil", err)
	}
	sentinel := filepath.Join(retained, "sentinel")
	if err := os.WriteFile(sentinel, []byte("keep\n"), 0o600); err != nil {
		t.Fatalf("os.WriteFile(retained sentinel) error = %v, want nil", err)
	}

	created, err := makeAssessmentTemporaryDirectory(root)
	if err != nil {
		t.Fatalf("makeAssessmentTemporaryDirectory(retained remnant) error = %v, want nil", err)
	}
	if created == retained {
		t.Errorf("makeAssessmentTemporaryDirectory(retained remnant) path = %q, want fresh name", created)
	}
	contents, err := os.ReadFile(sentinel)
	if err != nil || string(contents) != "keep\n" {
		t.Errorf("retained remnant sentinel after fresh creation = %q, %v, want untouched", contents, err)
	}
}

func TestMakeAssessmentTemporaryDirectoryRedactsFilesystemErrors(t *testing.T) {
	secret := "secret-temporary-root-61b26"
	root := filepath.Join(t.TempDir(), secret, "missing")
	path, err := makeAssessmentTemporaryDirectory(root)
	if path != "" {
		t.Errorf("makeAssessmentTemporaryDirectory(missing root) path = %q, want empty", path)
	}
	requireAssessmentTemporaryUnavailable(t, err)
	if strings.Contains(err.Error(), secret) || strings.Contains(fmt.Sprintf("%+v", err), secret) {
		t.Errorf("temporary-directory failure = %+v, want root path redacted", err)
	}
}

func runRemnantAssessment(
	fixture assessmentTransactionFixture,
	executable string,
	hooks assessmentHooks,
) (SavedPlanAssessmentCore, error) {
	return runSavedPlanAssessment(
		SavedPlanAssessmentTransactionOptions{Assessment: assessmentOptions(fixture, executable, nil)},
		func(core SavedPlanAssessmentCore, _ []AssessmentGuidanceGroup) (SavedPlanAssessmentCore, error) {
			return core, nil
		},
		nil,
		hooks,
	)
}

func TestSavedPlanAssessmentRandomizedTemporaryLifecycle(t *testing.T) {
	t.Run("post_claim_symlink_swap_is_refused_without_target_mutation", func(t *testing.T) {
		fixture := newAssessmentTransactionFixture(t)
		executable := assessmentExecutable(t, fixture.root, "printf '%s' "+assessmentShellLiteral(cleanAssessmentPlanJSON(t)))
		temporary := filepath.Join(fixture.root, "private-assessment")
		claimed := temporary + ".claimed"
		target := filepath.Join(fixture.root, "replacement-target")
		if err := os.Mkdir(target, 0o755); err != nil {
			t.Fatalf("os.Mkdir(replacement target) error = %v, want nil", err)
		}
		if err := os.Chmod(target, 0o755); err != nil {
			t.Fatalf("os.Chmod(replacement target fixture) error = %v, want nil", err)
		}
		hooks := productionAssessmentHooks()
		hooks.makeTemporary = func() (string, error) {
			if err := os.Mkdir(temporary, 0o700); err != nil {
				return "", err
			}
			if err := os.Rename(temporary, claimed); err != nil {
				return "", err
			}
			if err := os.Symlink(target, temporary); err != nil {
				return "", err
			}
			return temporary, nil
		}
		prepareCalls := 0
		hooks.prepareEvidence = func(plan.PrepareSavedPlanEvidenceOptions) (*plan.SavedPlanEvidence, error) {
			prepareCalls++
			return nil, errors.New("prepare must not run after a temporary-directory swap")
		}
		core, err := runRemnantAssessment(fixture, executable, hooks)
		failure := requireSavedPlanAssessmentFailure(t, err, "UNSAFE_SNAPSHOT_DIRECTORY")
		if prepareCalls != 0 {
			t.Errorf("prepareEvidence call count = %d, want zero after temporary-directory swap", prepareCalls)
		}
		if core.Checked != 0 || len(core.Roots) != 0 ||
			failure.Partial.Checked != 0 || len(failure.Partial.Roots) != 0 {
			t.Errorf("swapped temporary result/partial = %+v/%+v, want zero values", core, failure.Partial)
		}
		info, statErr := os.Lstat(target)
		if statErr != nil {
			t.Fatalf("os.Lstat(replacement target) error = %v, want nil", statErr)
		}
		if info.Mode().Perm() != 0o755 {
			t.Errorf("replacement target mode = %#o, want unchanged 0755", info.Mode().Perm())
		}
		linkTarget, readlinkErr := os.Readlink(temporary)
		if readlinkErr != nil || linkTarget != target {
			t.Errorf("replacement symlink after assessment = %q, %v, want untouched target %q", linkTarget, readlinkErr, target)
		}
	})

	t.Run("forty_eight_sequential_assessments_self_clean", func(t *testing.T) {
		temporaryRoot := t.TempDir()
		t.Setenv("TMPDIR", temporaryRoot)
		fixture := newAssessmentTransactionFixture(t)
		executable := assessmentExecutable(t, fixture.root, "printf '%s' "+assessmentShellLiteral(cleanAssessmentPlanJSON(t)))
		for transaction := 0; transaction < 48; transaction++ {
			core, err := AssessSavedPlans(assessmentOptions(fixture, executable, nil))
			if err != nil {
				t.Fatalf("AssessSavedPlans(sequential run %d) error = %v, want nil", transaction+1, err)
			}
			if core.Checked != 1 || core.Clean != 1 {
				t.Errorf("AssessSavedPlans(sequential run %d) counts = %+v, want one clean root", transaction+1, core)
			}
			if entries := readAssessmentTemporaryEntries(t, temporaryRoot); len(entries) != 0 {
				t.Fatalf("assessment temp entries after sequential run %d = %d, want zero", transaction+1, len(entries))
			}
		}
	})

	t.Run("forced_snapshot_removal_failure_does_not_restore_ceiling", func(t *testing.T) {
		temporaryRoot := t.TempDir()
		t.Setenv("TMPDIR", temporaryRoot)
		fixture := newAssessmentTransactionFixture(t)
		executable := assessmentExecutable(t, fixture.root, "printf '%s' "+assessmentShellLiteral(cleanAssessmentPlanJSON(t)))
		hooks := productionAssessmentHooks()
		removalCalls := 0
		hooks.cleanupHooks.removeSnapshot = func(*os.Root, string) error {
			removalCalls++
			return errors.New("forced descriptor-relative snapshot removal failure")
		}
		for transaction := 0; transaction < 40; transaction++ {
			core, err := runRemnantAssessment(fixture, executable, hooks)
			if err != nil {
				t.Fatalf("runSavedPlanAssessment(forced cleanup run %d) error = %v, want nil", transaction+1, err)
			}
			if core.Checked != 1 || core.Clean != 1 {
				t.Errorf("runSavedPlanAssessment(forced cleanup run %d) counts = %+v, want one clean root", transaction+1, core)
			}
		}
		entries := readAssessmentTemporaryEntries(t, temporaryRoot)
		if len(entries) != 40 || removalCalls != 40 {
			t.Errorf("forced cleanup after 40 runs = %d remnants/%d removal calls, want 40/40", len(entries), removalCalls)
		}
		for _, entry := range entries {
			path := filepath.Join(temporaryRoot, entry.Name())
			requirePrivateAssessmentDirectory(t, path)
			children := readAssessmentTemporaryEntries(t, path)
			if len(children) != 1 {
				t.Errorf("forced cleanup remnant %q entry count = %d, want one scrubbed snapshot", path, len(children))
				continue
			}
			info, err := children[0].Info()
			if err != nil || !info.Mode().IsRegular() || info.Size() != 0 {
				t.Errorf("forced cleanup remnant %q snapshot = {info:%v error:%v}, want zero-length regular inode", path, info, err)
			}
		}

		core, err := AssessSavedPlans(assessmentOptions(fixture, executable, nil))
		if err != nil {
			t.Fatalf("AssessSavedPlans(run 41 after forced cleanup failures) error = %v, want nil", err)
		}
		if core.Checked != 1 || core.Clean != 1 {
			t.Errorf("AssessSavedPlans(run 41 after forced cleanup failures) counts = %+v, want one clean root", core)
		}
		if got := len(readAssessmentTemporaryEntries(t, temporaryRoot)); got != 40 {
			t.Errorf("assessment remnants after successful run 41 = %d, want retained 40 only", got)
		}
	})

	t.Run("parallel_assessments_do_not_collide", func(t *testing.T) {
		temporaryRoot := t.TempDir()
		t.Setenv("TMPDIR", temporaryRoot)
		fixture := newAssessmentTransactionFixture(t)
		executable := assessmentExecutable(t, fixture.root, "printf '%s' "+assessmentShellLiteral(cleanAssessmentPlanJSON(t)))
		const workers = 16
		type result struct {
			core SavedPlanAssessmentCore
			err  error
		}
		start := make(chan struct{})
		// One result per bounded assessment worker ensures all goroutines can
		// terminate before the test goroutine validates outcomes.
		results := make(chan result, workers)
		var wait sync.WaitGroup
		for worker := 0; worker < workers; worker++ {
			wait.Add(1)
			go func() {
				defer wait.Done()
				<-start
				core, err := AssessSavedPlans(assessmentOptions(fixture, executable, nil))
				results <- result{core: core, err: err}
			}()
		}
		close(start)
		wait.Wait()
		close(results)

		completed := 0
		for result := range results {
			if result.err != nil {
				t.Errorf("AssessSavedPlans(parallel) error = %v, want nil", result.err)
				continue
			}
			if result.core.Checked != 1 || result.core.Clean != 1 {
				t.Errorf("AssessSavedPlans(parallel) counts = %+v, want one clean root", result.core)
				continue
			}
			completed++
		}
		if completed != workers {
			t.Errorf("parallel successful assessments = %d, want %d", completed, workers)
		}
		if entries := readAssessmentTemporaryEntries(t, temporaryRoot); len(entries) != 0 {
			t.Errorf("assessment temp entries after parallel runs = %d, want zero", len(entries))
		}
	})
}
