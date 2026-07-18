package assessment

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
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

func assessmentTemporarySlotPath(root string, slot int) string {
	return filepath.Join(
		root,
		fmt.Sprintf("%s%02d", assessmentTemporaryDirectoryPrefix, slot),
	)
}

func TestMakeAssessmentTemporaryDirectoryHasExactFiniteCapacity(t *testing.T) {
	if retainedSnapshotBound := maxAssessmentTemporaryDirectorySlots * MaxSavedPlanAssessmentRoots; retainedSnapshotBound != 32_000 {
		t.Fatalf("retained snapshot inode bound = %d, want 32,000 plus 32 slot directories", retainedSnapshotBound)
	}
	root := t.TempDir()
	created := make(map[string]os.FileInfo, maxAssessmentTemporaryDirectorySlots)
	for slot := 0; slot < maxAssessmentTemporaryDirectorySlots; slot++ {
		path, err := makeAssessmentTemporaryDirectory(root)
		if err != nil {
			t.Fatalf("makeAssessmentTemporaryDirectory(slot %d) error = %v, want nil", slot, err)
		}
		if path != assessmentTemporarySlotPath(root, slot) {
			t.Errorf("slot %d path = %q, want deterministic slot path", slot, path)
		}
		info, err := os.Lstat(path)
		if err != nil {
			t.Fatalf("os.Lstat(created slot %d) error = %v, want nil", slot, err)
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&^os.FileMode(0o700) != 0 {
			t.Errorf("slot %d mode = %v, want non-symlink directory no broader than requested 0700", slot, info.Mode())
		}
		for previousPath, previousInfo := range created {
			if os.SameFile(previousInfo, info) {
				t.Errorf("slot %q and %q share an inode, want distinct atomic claims", previousPath, path)
			}
		}
		created[path] = info
	}

	path, err := makeAssessmentTemporaryDirectory(root)
	if path != "" {
		t.Errorf("makeAssessmentTemporaryDirectory(exhausted) path = %q, want empty", path)
	}
	requireAssessmentTemporaryUnavailable(t, err)
	entries, readErr := os.ReadDir(root)
	if readErr != nil {
		t.Fatalf("os.ReadDir(exhausted root) error = %v, want nil", readErr)
	}
	if len(entries) != maxAssessmentTemporaryDirectorySlots {
		t.Errorf("exhausted root entry count = %d, want exact cap %d", len(entries), maxAssessmentTemporaryDirectorySlots)
	}
}

func TestMakeAssessmentTemporaryDirectoryClaimsSlotsConcurrently(t *testing.T) {
	root := t.TempDir()
	type result struct {
		path string
		err  error
	}
	start := make(chan struct{})
	results := make(chan result, maxAssessmentTemporaryDirectorySlots*2)
	var workers sync.WaitGroup
	for worker := 0; worker < maxAssessmentTemporaryDirectorySlots*2; worker++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			path, err := makeAssessmentTemporaryDirectory(root)
			results <- result{path: path, err: err}
		}()
	}
	close(start)
	workers.Wait()
	close(results)

	created := make(map[string]struct{}, maxAssessmentTemporaryDirectorySlots)
	failures := 0
	for result := range results {
		if result.err != nil {
			failures++
			if result.path != "" {
				t.Errorf("failed concurrent claim path = %q, want empty", result.path)
			}
			var failure *procerr.ProcessFailure
			if !errors.As(result.err, &failure) ||
				failure.Code != "ASSESSMENT_TEMPORARY_DIRECTORY_UNAVAILABLE" {
				t.Errorf("concurrent claim error = %T(%v), want fixed unavailable failure", result.err, result.err)
			}
			continue
		}
		if _, duplicate := created[result.path]; duplicate {
			t.Errorf("concurrent slot %q claimed more than once", result.path)
		}
		created[result.path] = struct{}{}
	}
	if len(created) != maxAssessmentTemporaryDirectorySlots ||
		failures != maxAssessmentTemporaryDirectorySlots {
		t.Errorf("concurrent claims = %d success/%d failure, want %d/%d", len(created), failures, maxAssessmentTemporaryDirectorySlots, maxAssessmentTemporaryDirectorySlots)
	}
}

func TestMakeAssessmentTemporaryDirectorySkipsExistingEntriesWithoutChangingThem(t *testing.T) {
	root := t.TempDir()
	filePath := assessmentTemporarySlotPath(root, 0)
	if err := os.WriteFile(filePath, []byte("keep file\n"), 0o600); err != nil {
		t.Fatalf("os.WriteFile(preexisting slot) error = %v, want nil", err)
	}
	target := filepath.Join(root, "symlink-target")
	if err := os.WriteFile(target, []byte("keep target\n"), 0o600); err != nil {
		t.Fatalf("os.WriteFile(symlink target) error = %v, want nil", err)
	}
	symlinkPath := assessmentTemporarySlotPath(root, 1)
	if err := os.Symlink(target, symlinkPath); err != nil {
		t.Fatalf("os.Symlink(preexisting slot) error = %v, want nil", err)
	}
	directoryPath := assessmentTemporarySlotPath(root, 2)
	if err := os.Mkdir(directoryPath, 0o755); err != nil {
		t.Fatalf("os.Mkdir(preexisting slot) error = %v, want nil", err)
	}
	sentinelPath := filepath.Join(directoryPath, "sentinel")
	if err := os.WriteFile(sentinelPath, []byte("keep directory\n"), 0o600); err != nil {
		t.Fatalf("os.WriteFile(preexisting directory sentinel) error = %v, want nil", err)
	}

	claimed, err := makeAssessmentTemporaryDirectory(root)
	if err != nil {
		t.Fatalf("makeAssessmentTemporaryDirectory(preexisting entries) error = %v, want nil", err)
	}
	if claimed != assessmentTemporarySlotPath(root, 3) {
		t.Errorf("claimed path = %q, want first untouched free slot", claimed)
	}
	if contents, err := os.ReadFile(filePath); err != nil || string(contents) != "keep file\n" {
		t.Errorf("preexisting file after claim = %q, %v, want untouched", contents, err)
	}
	if got, err := os.Readlink(symlinkPath); err != nil || got != target {
		t.Errorf("preexisting symlink after claim = %q, %v, want target %q", got, err, target)
	}
	if contents, err := os.ReadFile(target); err != nil || string(contents) != "keep target\n" {
		t.Errorf("symlink target after claim = %q, %v, want untouched", contents, err)
	}
	if contents, err := os.ReadFile(sentinelPath); err != nil || string(contents) != "keep directory\n" {
		t.Errorf("preexisting directory after claim = %q, %v, want untouched", contents, err)
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

func TestSavedPlanAssessmentUsesBoundedTemporaryDirectoryPolicy(t *testing.T) {
	t.Run("post_claim_symlink_swap_does_not_chmod_target", func(t *testing.T) {
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
		core, err := runSavedPlanAssessment(
			SavedPlanAssessmentTransactionOptions{Assessment: assessmentOptions(fixture, executable, nil)},
			func(core SavedPlanAssessmentCore, _ []AssessmentGuidanceGroup) (SavedPlanAssessmentCore, error) {
				return core, nil
			},
			nil,
			hooks,
		)
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
			t.Errorf("replacement target mode = %#o, want unchanged 0755 (no pathname chmod)", info.Mode().Perm())
		}
		linkTarget, readlinkErr := os.Readlink(temporary)
		if readlinkErr != nil || linkTarget != target {
			t.Errorf("replacement symlink after assessment = %q, %v, want untouched target %q", linkTarget, readlinkErr, target)
		}
	})

	t.Run("success_leaves_one_bounded_remnant", func(t *testing.T) {
		temporaryRoot := t.TempDir()
		t.Setenv("TMPDIR", temporaryRoot)
		fixture := newAssessmentTransactionFixture(t)
		executable := assessmentExecutable(t, fixture.root, "printf '%s' "+assessmentShellLiteral(cleanAssessmentPlanJSON(t)))
		core, err := AssessSavedPlans(assessmentOptions(fixture, executable, nil))
		if err != nil {
			t.Fatalf("AssessSavedPlans(bounded temporary success) error = %v, want nil", err)
		}
		if core.Checked != 1 {
			t.Errorf("AssessSavedPlans(bounded temporary success).Checked = %d, want 1", core.Checked)
		}
		entries, err := os.ReadDir(assessmentTemporarySlotPath(temporaryRoot, 0))
		if err != nil {
			t.Fatalf("os.ReadDir(bounded remnant) error = %v, want nil", err)
		}
		if len(entries) != 1 {
			t.Fatalf("bounded remnant entry count = %d, want one scrubbed snapshot", len(entries))
		}
		info, err := entries[0].Info()
		if err != nil {
			t.Fatalf("bounded remnant snapshot info error = %v, want nil", err)
		}
		if !info.Mode().IsRegular() || info.Size() != 0 {
			t.Errorf("bounded remnant snapshot = {mode:%v size:%d}, want zero-length regular inode", info.Mode(), info.Size())
		}
	})

	t.Run("thirty_third_transaction_fails_before_snapshot_creation", func(t *testing.T) {
		temporaryRoot := t.TempDir()
		t.Setenv("TMPDIR", temporaryRoot)
		fixture := newAssessmentTransactionFixture(t)
		executable := assessmentExecutable(t, fixture.root, "printf '%s' "+assessmentShellLiteral(cleanAssessmentPlanJSON(t)))
		hooks := productionAssessmentHooks()
		prepareCalls := 0
		hooks.prepareEvidence = func(plan.PrepareSavedPlanEvidenceOptions) (*plan.SavedPlanEvidence, error) {
			prepareCalls++
			return nil, assessmentDomainFailure("STOP_AFTER_TEMPORARY_CLAIM", "stop after temporary claim")
		}
		for transaction := 0; transaction < maxAssessmentTemporaryDirectorySlots; transaction++ {
			core, err := runSavedPlanAssessment(
				SavedPlanAssessmentTransactionOptions{Assessment: assessmentOptions(fixture, executable, nil)},
				func(core SavedPlanAssessmentCore, _ []AssessmentGuidanceGroup) (SavedPlanAssessmentCore, error) {
					return core, nil
				},
				nil,
				hooks,
			)
			failure := requireSavedPlanAssessmentFailure(t, err, "STOP_AFTER_TEMPORARY_CLAIM")
			if core.Checked != 0 || len(core.Roots) != 0 ||
				failure.Partial.Checked != 0 || len(failure.Partial.Roots) != 0 {
				t.Errorf("transaction %d result/partial = %+v/%+v, want zero values", transaction+1, core, failure.Partial)
			}
		}

		core, err := runSavedPlanAssessment(
			SavedPlanAssessmentTransactionOptions{Assessment: assessmentOptions(fixture, executable, nil)},
			func(core SavedPlanAssessmentCore, _ []AssessmentGuidanceGroup) (SavedPlanAssessmentCore, error) {
				return core, nil
			},
			nil,
			hooks,
		)
		failure := requireSavedPlanAssessmentFailure(t, err, "ASSESSMENT_TEMPORARY_DIRECTORY_UNAVAILABLE")
		if failure.Category != procerr.CategoryIO ||
			failure.Message != "unable to create private assessment directory" {
			t.Errorf("assessment exhaustion failure = %+v, want fixed redacted IO failure", failure.ProcessFailure)
		}
		if core.Checked != 0 || len(core.Roots) != 0 ||
			failure.Partial.Checked != 0 || len(failure.Partial.Roots) != 0 {
			t.Errorf("assessment exhaustion result/partial = %+v/%+v, want zero values", core, failure.Partial)
		}
		entries, readErr := os.ReadDir(temporaryRoot)
		if readErr != nil {
			t.Fatalf("os.ReadDir(exhausted assessment root) error = %v, want nil", readErr)
		}
		names := make([]string, 0, maxAssessmentTemporaryDirectorySlots)
		for _, entry := range entries {
			if strings.HasPrefix(entry.Name(), assessmentTemporaryDirectoryPrefix) {
				names = append(names, entry.Name())
			}
		}
		sort.Strings(names)
		if len(names) != maxAssessmentTemporaryDirectorySlots {
			t.Errorf("assessment exhaustion slot count = %d, want %d", len(names), maxAssessmentTemporaryDirectorySlots)
		}
		if prepareCalls != maxAssessmentTemporaryDirectorySlots {
			t.Errorf("prepareEvidence call count = %d, want %d (33rd transaction must fail before snapshot preparation)", prepareCalls, maxAssessmentTemporaryDirectorySlots)
		}
		for slot := 0; slot < maxAssessmentTemporaryDirectorySlots; slot++ {
			entries, readErr := os.ReadDir(assessmentTemporarySlotPath(temporaryRoot, slot))
			if readErr != nil || len(entries) != 0 {
				t.Errorf("retained slot %d = %d entries, %v, want untouched empty remnant", slot, len(entries), readErr)
			}
		}
	})
}
