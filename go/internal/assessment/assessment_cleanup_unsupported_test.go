//go:build (!darwin && !linux) || ios || android || (!amd64 && !arm64)

package assessment

import (
	"os"
	"testing"
)

func TestAssessmentTemporaryCleanupFailsClosedOnUnsupportedPlatform(t *testing.T) {
	directory := t.TempDir()
	failure := cleanupAssessmentTemporaryDirectory(
		directory,
		assessmentCleanupIdentity{},
		nil,
		assessmentCleanupHooks{},
	)
	if failure == nil || failure.Code != "ASSESSMENT_CLEANUP_FAILED" {
		t.Fatalf("cleanupAssessmentTemporaryDirectory(unsupported) = %+v, want ASSESSMENT_CLEANUP_FAILED", failure)
	}
	if _, err := os.Lstat(directory); err != nil {
		t.Errorf("os.Lstat(directory after unsupported cleanup) error = %v, want directory untouched", err)
	}
}
