//go:build (!darwin && !linux) || ios || android || (!amd64 && !arm64)

package plan

import (
	"errors"
	"math/big"
	"os"
	"path/filepath"
	"testing"

	"github.com/dvmrry/infrawright-dev/go/internal/artifacts"
	"github.com/dvmrry/infrawright-dev/go/internal/procerr"
)

func unsupportedEvidenceBudget(t *testing.T) *artifacts.ReadBudget {
	t.Helper()
	budget, err := artifacts.NewReadBudget(artifacts.BoundedReadLimits{
		MaxFiles:            8,
		MaxDirectories:      8,
		MaxDirectoryEntries: 8,
		MaxDepth:            8,
		MaxTotalBytes:       big.NewInt(1024),
		MaxFileBytes:        big.NewInt(1024),
	})
	if err != nil {
		t.Fatalf("artifacts.NewReadBudget(unsupported evidence limits) error = %v, want nil", err)
	}
	return budget
}

func requireUnsupportedEvidenceFailure(t *testing.T, err error) {
	t.Helper()
	var failure *procerr.ProcessFailure
	if !errors.As(err, &failure) {
		t.Fatalf("error = %T %v, want *procerr.ProcessFailure", err, err)
	}
	if failure.Code != "UNSUPPORTED_BOUNDED_FILE_PLATFORM" ||
		failure.Category != procerr.CategoryIO ||
		failure.Message != "bounded stable file operations are supported only on Linux and macOS amd64/arm64" {
		t.Errorf(
			"unsupported ProcessFailure = {Code:%q Category:%q Message:%q}, want exact bounded-file platform failure",
			failure.Code,
			failure.Category,
			failure.Message,
		)
	}
	if failure.Retryable || len(failure.Details) != 0 {
		t.Errorf("unsupported ProcessFailure = %+v, want retryable false and no details", failure)
	}
}

func requireUnchargedEvidenceBudget(t *testing.T, name string, budget *artifacts.ReadBudget) {
	t.Helper()
	if got := budget.Files(); got != 0 {
		t.Errorf("%s budget files = %d, want 0", name, got)
	}
	if got := budget.Directories(); got != 0 {
		t.Errorf("%s budget directories = %d, want 0", name, got)
	}
	if got := budget.DirectoryEntries(); got != 0 {
		t.Errorf("%s budget directory entries = %d, want 0", name, got)
	}
	if got := budget.Bytes(); got.Sign() != 0 {
		t.Errorf("%s budget bytes = %s, want 0", name, got)
	}
}

func TestEvidenceUnsupportedPlatformBoundary(t *testing.T) {
	root := t.TempDir()
	missingDirectory := filepath.Join(root, "must-not-be-inspected")
	missingPlan := filepath.Join(root, "missing.tfplan")
	missingFingerprint := filepath.Join(root, "missing.tfplan.sources")
	missingEnvironment := filepath.Join(root, "missing-environment")

	t.Run("read_fingerprint", func(t *testing.T) {
		budget := unsupportedEvidenceBudget(t)
		_, err := ReadSavedPlanFingerprint(missingFingerprint, budget)
		requireUnsupportedEvidenceFailure(t, err)
		requireUnchargedEvidenceBudget(t, "fingerprint", budget)
	})

	t.Run("prepare", func(t *testing.T) {
		fingerprintBudget := unsupportedEvidenceBudget(t)
		savedPlanBudget := unsupportedEvidenceBudget(t)
		_, err := PrepareSavedPlanEvidence(PrepareSavedPlanEvidenceOptions{
			SavedPlanPath:     missingPlan,
			FingerprintPath:   missingFingerprint,
			FingerprintInput:  PlanFingerprintInput{EnvDir: missingEnvironment},
			SnapshotDirectory: missingDirectory,
			FingerprintBudget: fingerprintBudget,
			SavedPlanBudget:   savedPlanBudget,
		})
		requireUnsupportedEvidenceFailure(t, err)
		requireUnchargedEvidenceBudget(t, "prepare fingerprint", fingerprintBudget)
		requireUnchargedEvidenceBudget(t, "prepare saved plan", savedPlanBudget)
		if _, statErr := os.Lstat(missingDirectory); !errors.Is(statErr, os.ErrNotExist) {
			t.Errorf("os.Lstat(%q) after unsupported prepare error = %v, want os.ErrNotExist", missingDirectory, statErr)
		}
	})

	t.Run("unprepared_bindings_fail_first", func(t *testing.T) {
		fingerprintBudget := unsupportedEvidenceBudget(t)
		savedPlanBudget := unsupportedEvidenceBudget(t)
		err := RecheckSavedPlanEvidence(RecheckSavedPlanEvidenceOptions{
			Evidence:          &SavedPlanEvidence{},
			FingerprintBudget: fingerprintBudget,
			SavedPlanBudget:   savedPlanBudget,
		})
		var failure *procerr.ProcessFailure
		if !errors.As(err, &failure) ||
			failure.Code != "INVALID_EVIDENCE_BINDING" ||
			failure.Category != procerr.CategoryDomain ||
			failure.Message != "saved-plan evidence is not active" {
			t.Errorf("RecheckSavedPlanEvidence(unprepared) error = %T %+v, want exact INVALID_EVIDENCE_BINDING", err, failure)
		}
		err = CleanupSavedPlanEvidence(&SavedPlanEvidence{})
		if !errors.As(err, &failure) ||
			failure.Code != "INVALID_SNAPSHOT_BINDING" ||
			failure.Category != procerr.CategoryDomain ||
			failure.Message != "saved-plan snapshot has no active cleanup binding" {
			t.Errorf("CleanupSavedPlanEvidence(unprepared) error = %T %+v, want exact INVALID_SNAPSHOT_BINDING", err, failure)
		}
		requireUnchargedEvidenceBudget(t, "unprepared fingerprint", fingerprintBudget)
		requireUnchargedEvidenceBudget(t, "unprepared saved plan", savedPlanBudget)
	})

	t.Run("active_binding_gates_before_filesystem", func(t *testing.T) {
		fingerprintBudget := unsupportedEvidenceBudget(t)
		savedPlanBudget := unsupportedEvidenceBudget(t)
		evidence := &SavedPlanEvidence{}
		evidence.binding = &savedPlanEvidenceBinding{
			owner: evidence,
			state: savedPlanEvidenceState{
				fingerprintPath:   missingFingerprint,
				originalPlan:      BoundFileDigest{Path: missingPlan},
				snapshotDirectory: missingDirectory,
				snapshot:          artifacts.StableFileSnapshot{Path: filepath.Join(missingDirectory, "snapshot")},
			},
		}
		err := RecheckSavedPlanEvidence(RecheckSavedPlanEvidenceOptions{
			Evidence:          evidence,
			FingerprintBudget: fingerprintBudget,
			SavedPlanBudget:   savedPlanBudget,
		})
		requireUnsupportedEvidenceFailure(t, err)
		requireUnsupportedEvidenceFailure(t, CleanupSavedPlanEvidence(evidence))
		requireUnchargedEvidenceBudget(t, "active fingerprint", fingerprintBudget)
		requireUnchargedEvidenceBudget(t, "active saved plan", savedPlanBudget)
	})
}
