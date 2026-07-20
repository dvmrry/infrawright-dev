//go:build (darwin || linux) && !ios && !android && (amd64 || arm64)

package plan

import (
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"testing"

	"github.com/dvmrry/infrawright-dev/go/internal/artifacts"
	"github.com/dvmrry/infrawright-dev/go/internal/procerr"
)

func TestSavedPlanEvidenceCleanupRefusesDirectorySymlinkSwap(t *testing.T) {
	fixture := newEvidenceFixture(t)
	evidence := prepareEvidence(t, fixture)
	movedDirectory := fixture.snapshotDirectory + "-moved"
	victimDirectory := filepath.Join(fixture.root, "victim-directory")
	if err := os.Mkdir(victimDirectory, 0o700); err != nil {
		t.Fatalf("os.Mkdir(%q, 0700) error = %v, want nil", victimDirectory, err)
	}
	victim := filepath.Join(victimDirectory, filepath.Base(evidence.Snapshot.Path))
	writeEvidenceFile(t, victim, []byte("must survive\n"), 0o600)
	if err := os.Rename(fixture.snapshotDirectory, movedDirectory); err != nil {
		t.Fatalf("os.Rename(%q, %q) error = %v, want nil", fixture.snapshotDirectory, movedDirectory, err)
	}
	if err := os.Symlink(victimDirectory, fixture.snapshotDirectory); err != nil {
		t.Fatalf("os.Symlink(%q, %q) error = %v, want nil", victimDirectory, fixture.snapshotDirectory, err)
	}

	requireEvidenceFailure(
		t,
		CleanupSavedPlanEvidence(evidence),
		"SNAPSHOT_CLEANUP_REFUSED",
		procerr.CategoryDomain,
		"private snapshot directory changed before cleanup",
	)
	if content, err := os.ReadFile(victim); err != nil || string(content) != "must survive\n" {
		t.Errorf("victim after directory symlink attack = %q, %v, want unchanged bytes", content, err)
	}

	if err := os.Remove(fixture.snapshotDirectory); err != nil {
		t.Fatalf("os.Remove(%q) error = %v, want nil", fixture.snapshotDirectory, err)
	}
	if err := os.Rename(movedDirectory, fixture.snapshotDirectory); err != nil {
		t.Fatalf("os.Rename(%q, %q) error = %v, want nil", movedDirectory, fixture.snapshotDirectory, err)
	}
	if err := CleanupSavedPlanEvidence(evidence); err != nil {
		t.Errorf("CleanupSavedPlanEvidence(restored directory) error = %v, want nil", err)
	}
}

func TestSavedPlanEvidenceCleanupRefusesSnapshotReplacement(t *testing.T) {
	fixture := newEvidenceFixture(t)
	evidence := prepareEvidence(t, fixture)
	original := evidence.Snapshot.Path + ".original"
	if err := os.Rename(evidence.Snapshot.Path, original); err != nil {
		t.Fatalf("os.Rename(%q, %q) error = %v, want nil", evidence.Snapshot.Path, original, err)
	}
	writeEvidenceFile(t, evidence.Snapshot.Path, []byte("replacement must survive\n"), 0o600)

	requireEvidenceFailure(
		t,
		CleanupSavedPlanEvidence(evidence),
		"SNAPSHOT_CLEANUP_REFUSED",
		procerr.CategoryDomain,
		"saved-plan snapshot changed before cleanup",
	)
	for _, filePath := range []string{original, evidence.Snapshot.Path} {
		if _, err := os.Stat(filePath); err != nil {
			t.Errorf("os.Stat(%q) after replacement cleanup refusal error = %v, want nil", filePath, err)
		}
	}
	content, err := os.ReadFile(evidence.Snapshot.Path)
	if err != nil || string(content) != "replacement must survive\n" {
		t.Errorf("replacement after cleanup refusal = %q, %v, want unchanged bytes", content, err)
	}

	if err := os.Remove(evidence.Snapshot.Path); err != nil {
		t.Fatalf("os.Remove(%q) error = %v, want nil", evidence.Snapshot.Path, err)
	}
	if err := os.Rename(original, evidence.Snapshot.Path); err != nil {
		t.Fatalf("os.Rename(%q, %q) error = %v, want nil", original, evidence.Snapshot.Path, err)
	}
	if err := CleanupSavedPlanEvidence(evidence); err != nil {
		t.Errorf("CleanupSavedPlanEvidence(restored snapshot) error = %v, want nil", err)
	}
}

func TestSavedPlanEvidenceCleanupDoesNotFollowSnapshotSymlink(t *testing.T) {
	fixture := newEvidenceFixture(t)
	evidence := prepareEvidence(t, fixture)
	original := evidence.Snapshot.Path + ".original"
	victim := filepath.Join(fixture.root, "must-not-truncate")
	writeEvidenceFile(t, victim, []byte("must survive\n"), 0o600)
	if err := os.Rename(evidence.Snapshot.Path, original); err != nil {
		t.Fatalf("os.Rename(%q, %q) error = %v, want nil", evidence.Snapshot.Path, original, err)
	}
	if err := os.Symlink(victim, evidence.Snapshot.Path); err != nil {
		t.Fatalf("os.Symlink(%q, %q) error = %v, want nil", victim, evidence.Snapshot.Path, err)
	}

	requireEvidenceFailure(
		t,
		CleanupSavedPlanEvidence(evidence),
		"SNAPSHOT_CLEANUP_FAILED",
		procerr.CategoryDomain,
		"unable to scrub saved-plan snapshot",
	)
	content, err := os.ReadFile(victim)
	if err != nil || string(content) != "must survive\n" {
		t.Errorf("symlink victim after cleanup = %q, %v, want unchanged bytes", content, err)
	}

	if err := os.Remove(evidence.Snapshot.Path); err != nil {
		t.Fatalf("os.Remove(%q) error = %v, want nil", evidence.Snapshot.Path, err)
	}
	if err := os.Rename(original, evidence.Snapshot.Path); err != nil {
		t.Fatalf("os.Rename(%q, %q) error = %v, want nil", original, evidence.Snapshot.Path, err)
	}
	if err := CleanupSavedPlanEvidence(evidence); err != nil {
		t.Errorf("CleanupSavedPlanEvidence(restored snapshot) error = %v, want nil", err)
	}
}

func TestSavedPlanEvidenceCleanupOpensSpecialFileNonblocking(t *testing.T) {
	fixture := newEvidenceFixture(t)
	evidence := prepareEvidence(t, fixture)
	original := evidence.Snapshot.Path + ".original"
	if err := os.Rename(evidence.Snapshot.Path, original); err != nil {
		t.Fatalf("os.Rename(%q, %q) error = %v, want nil", evidence.Snapshot.Path, original, err)
	}
	if err := syscall.Mkfifo(evidence.Snapshot.Path, 0o600); err != nil {
		t.Fatalf("syscall.Mkfifo(%q, 0600) error = %v, want nil", evidence.Snapshot.Path, err)
	}

	requireEvidenceFailure(
		t,
		CleanupSavedPlanEvidence(evidence),
		"SNAPSHOT_CLEANUP_REFUSED",
		procerr.CategoryDomain,
		"saved-plan snapshot changed before cleanup",
	)
	if err := os.Remove(evidence.Snapshot.Path); err != nil {
		t.Fatalf("os.Remove(%q) error = %v, want nil", evidence.Snapshot.Path, err)
	}
	if err := os.Rename(original, evidence.Snapshot.Path); err != nil {
		t.Fatalf("os.Rename(%q, %q) error = %v, want nil", original, evidence.Snapshot.Path, err)
	}
	if err := CleanupSavedPlanEvidence(evidence); err != nil {
		t.Errorf("CleanupSavedPlanEvidence(restored snapshot) error = %v, want nil", err)
	}
}

func TestSavedPlanEvidenceCleanupIsConcurrentAndIdempotent(t *testing.T) {
	fixture := newEvidenceFixture(t)
	evidence := prepareEvidence(t, fixture)
	const callers = 16
	errorsByCaller := make([]error, callers)
	var wait sync.WaitGroup
	for caller := 0; caller < callers; caller++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			errorsByCaller[index] = CleanupSavedPlanEvidence(evidence)
		}(caller)
	}
	wait.Wait()
	for caller, err := range errorsByCaller {
		if err != nil {
			t.Errorf("CleanupSavedPlanEvidence concurrent caller %d error = %v, want nil", caller, err)
		}
	}
	info, err := os.Stat(evidence.Snapshot.Path)
	if err != nil {
		t.Fatalf("os.Stat(%q) after concurrent cleanup error = %v, want nil", evidence.Snapshot.Path, err)
	}
	if info.Size() != 0 {
		t.Errorf("snapshot size after concurrent cleanup = %d, want 0", info.Size())
	}
}

func TestSavedPlanEvidenceCleanupRejectsDirectorySwapAfterIdentityCheck(t *testing.T) {
	fixture := newEvidenceFixture(t)
	evidence := prepareEvidence(t, fixture)
	movedDirectory := fixture.snapshotDirectory + "-moved"
	victim := filepath.Join(fixture.snapshotDirectory, filepath.Base(evidence.Snapshot.Path))
	err := cleanupSavedPlanEvidence(evidence, evidenceCleanupHooks{
		afterDirectoryIdentity: func() error {
			if err := os.Rename(fixture.snapshotDirectory, movedDirectory); err != nil {
				return err
			}
			if err := os.Mkdir(fixture.snapshotDirectory, 0o700); err != nil {
				return err
			}
			return os.WriteFile(victim, []byte("must survive\n"), 0o600)
		},
	})
	requireEvidenceFailure(
		t,
		err,
		"SNAPSHOT_CLEANUP_REFUSED",
		procerr.CategoryDomain,
		"saved-plan snapshot changed before cleanup",
	)
	content, readErr := os.ReadFile(victim)
	if readErr != nil || string(content) != "must survive\n" {
		t.Errorf("victim after post-check directory swap = %q, %v, want unchanged bytes", content, readErr)
	}
	if err := os.Remove(victim); err != nil {
		t.Fatalf("os.Remove(%q) error = %v, want nil", victim, err)
	}
	if err := os.Remove(fixture.snapshotDirectory); err != nil {
		t.Fatalf("os.Remove(%q) error = %v, want nil", fixture.snapshotDirectory, err)
	}
	if err := os.Rename(movedDirectory, fixture.snapshotDirectory); err != nil {
		t.Fatalf("os.Rename(%q, %q) error = %v, want nil", movedDirectory, fixture.snapshotDirectory, err)
	}
	if err := CleanupSavedPlanEvidence(evidence); err != nil {
		t.Errorf("CleanupSavedPlanEvidence(restored directory) error = %v, want nil", err)
	}
}

func TestSavedPlanEvidenceCleanupKeepsOpenedIdentityAcrossPathSwap(t *testing.T) {
	fixture := newEvidenceFixture(t)
	evidence := prepareEvidence(t, fixture)
	movedSnapshot := evidence.Snapshot.Path + ".opened"
	err := cleanupSavedPlanEvidence(evidence, evidenceCleanupHooks{
		afterOpen: func() error {
			if err := os.Rename(evidence.Snapshot.Path, movedSnapshot); err != nil {
				return err
			}
			return os.WriteFile(evidence.Snapshot.Path, []byte("replacement must survive\n"), 0o600)
		},
	})
	if err != nil {
		t.Fatalf("cleanupSavedPlanEvidence(post-open path swap) error = %v, want nil", err)
	}
	movedInfo, err := os.Stat(movedSnapshot)
	if err != nil {
		t.Fatalf("os.Stat(%q) error = %v, want nil", movedSnapshot, err)
	}
	if movedInfo.Size() != 0 {
		t.Errorf("opened bound snapshot size after cleanup = %d, want 0", movedInfo.Size())
	}
	content, err := os.ReadFile(evidence.Snapshot.Path)
	if err != nil || string(content) != "replacement must survive\n" {
		t.Errorf("post-open replacement after cleanup = %q, %v, want unchanged bytes", content, err)
	}
}

func TestSavedPlanEvidencePrepareFailureScrubsWithoutUnlinking(t *testing.T) {
	fixture := newEvidenceFixture(t)
	options := evidencePrepareOptions(t, fixture)
	var snapshotPath string
	_, err := prepareSavedPlanEvidence(options, evidenceHooks{
		afterSnapshotIdentity: func(snapshot artifacts.StableFileSnapshot) error {
			snapshotPath = snapshot.Path
			return os.WriteFile(snapshot.Path, []byte("mutated snapshot\n"), 0o600)
		},
	})
	requireEvidenceFailure(
		t,
		err,
		"PLAN_SNAPSHOT_CHANGED",
		procerr.CategoryDomain,
		"saved-plan snapshot changed while evidence was prepared",
	)
	info, statErr := os.Stat(snapshotPath)
	if statErr != nil {
		t.Fatalf("os.Stat(%q) after preparation cleanup error = %v, want nil", snapshotPath, statErr)
	}
	if info.Size() != 0 {
		t.Errorf("snapshot size after preparation cleanup = %d, want 0", info.Size())
	}
}

func TestReadSavedPlanFingerprintDoesNotFollowSymlink(t *testing.T) {
	fixture := newEvidenceFixture(t)
	original := fixture.fingerprintPath + ".original"
	if err := os.Rename(fixture.fingerprintPath, original); err != nil {
		t.Fatalf("os.Rename(%q, %q) error = %v, want nil", fixture.fingerprintPath, original, err)
	}
	if err := os.Symlink(original, fixture.fingerprintPath); err != nil {
		t.Fatalf("os.Symlink(%q, %q) error = %v, want nil", original, fixture.fingerprintPath, err)
	}
	_, err := ReadSavedPlanFingerprint(
		fixture.fingerprintPath,
		newEvidenceBudget(t, evidenceSourceLimits()),
	)
	requireEvidenceFailure(
		t,
		err,
		"SYMLINK_NOT_ALLOWED",
		procerr.CategoryIO,
		"input file must not be a symbolic link",
	)
}
