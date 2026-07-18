//go:build (!darwin && !linux) || ios || android || (!amd64 && !arm64)

package controlevidence

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/dvmrry/infrawright-dev/go/internal/procerr"
)

func TestUnsupportedRecheckFailsClosedForAbsentBinding(t *testing.T) {
	filePath := filepath.Join(t.TempDir(), "missing-control.json")
	err := RecheckAssessmentControlFiles([]BoundAssessmentControlFile{{Path: filePath}})
	var failure *procerr.ProcessFailure
	if !errors.As(err, &failure) {
		t.Fatalf("RecheckAssessmentControlFiles(absent unsupported binding) error = %v (%T), want *procerr.ProcessFailure", err, err)
	}
	if got, want := failure.Code, "ASSESSMENT_CONTROL_CHANGED"; got != want {
		t.Errorf("RecheckAssessmentControlFiles(absent unsupported binding) code = %q, want %q", got, want)
	}
	if got, want := failure.Category, procerr.CategoryDomain; got != want {
		t.Errorf("RecheckAssessmentControlFiles(absent unsupported binding) category = %q, want %q", got, want)
	}
	if got, want := failure.Message, "saved-plan assessment control input changed during assessment"; got != want {
		t.Errorf("RecheckAssessmentControlFiles(absent unsupported binding) message = %q, want %q", got, want)
	}
}

func TestUnsupportedRecheckAcceptsEmptySet(t *testing.T) {
	if err := RecheckAssessmentControlFiles(nil); err != nil {
		t.Errorf("RecheckAssessmentControlFiles(nil) error = %v, want nil", err)
	}
	if err := RecheckAssessmentControlFiles([]BoundAssessmentControlFile{}); err != nil {
		t.Errorf("RecheckAssessmentControlFiles(empty) error = %v, want nil", err)
	}
}
