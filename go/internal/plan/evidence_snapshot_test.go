//go:build (darwin || linux) && !ios && !android && (amd64 || arm64)

package plan

import (
	"math/big"
	"os"
	"testing"
)

func TestSavedPlanSnapshotDescriptorBindsPreparedBytes(t *testing.T) {
	fixture := newEvidenceFixture(t)
	evidence := prepareEvidence(t, fixture)
	t.Cleanup(func() { _ = CleanupSavedPlanEvidence(evidence) })
	file, err := OpenSavedPlanSnapshot(evidence, newEvidenceBudget(t, evidencePlanLimits()))
	if err != nil {
		t.Fatalf("OpenSavedPlanSnapshot() error = %v", err)
	}
	defer file.Close()
	if err := os.Rename(evidence.Snapshot.Path, evidence.Snapshot.Path+".rebound"); err != nil {
		t.Fatal(err)
	}
	writeEvidenceFile(t, evidence.Snapshot.Path, []byte("replacement"), 0o600)
	if err := RecheckSavedPlanSnapshot(evidence, file, newEvidenceBudget(t, evidencePlanLimits())); err != nil {
		t.Errorf("RecheckSavedPlanSnapshot(open original) error = %v, want nil", err)
	}
	if _, err := OpenSavedPlanSnapshot(evidence, newEvidenceBudget(t, evidencePlanLimits())); err == nil {
		t.Error("OpenSavedPlanSnapshot(rebound path) error = nil, want changed-snapshot rejection")
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if err := RecheckSavedPlanSnapshot(evidence, file, newEvidenceBudget(t, evidencePlanLimits())); err == nil {
		t.Error("RecheckSavedPlanSnapshot(closed) error = nil, want changed-snapshot rejection")
	} else {
		requireEvidenceCode(t, err, "PLAN_SNAPSHOT_CHANGED")
	}
}

func TestOpenSavedPlanSnapshotChargesBoundedReadBudget(t *testing.T) {
	fixture := newEvidenceFixture(t)
	evidence := prepareEvidence(t, fixture)
	t.Cleanup(func() { _ = CleanupSavedPlanEvidence(evidence) })
	limits := evidencePlanLimits()
	limits.MaxFileBytes = big.NewInt(1)
	limits.MaxTotalBytes = big.NewInt(1)
	_, err := OpenSavedPlanSnapshot(evidence, newEvidenceBudget(t, limits))
	requireEvidenceCode(t, err, "FILE_LIMIT_EXCEEDED")
}
