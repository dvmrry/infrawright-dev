package assessment

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/dvmrry/infrawright-dev/go/internal/procerr"
)

const (
	// maxAssessmentTemporaryDirectorySlots bounds the private assessment
	// directories that one temporary root can ever accumulate. Slots are
	// claimed atomically and are never inspected, removed, or reused here. The
	// 33rd transaction fails closed until an operator removes old slots. At the
	// root-count ceiling, the retained bound is 32,000 scrubbed snapshot inodes
	// plus the 32 slot-directory inodes.
	maxAssessmentTemporaryDirectorySlots = 32
	assessmentTemporaryDirectoryPrefix   = "infrawright-assessment-slot-"
)

func assessmentTemporaryDirectoryUnavailable() *procerr.ProcessFailure {
	return procerr.NewProcessFailure(procerr.NewProcessFailureOptions{
		Code:     "ASSESSMENT_TEMPORARY_DIRECTORY_UNAVAILABLE",
		Category: procerr.CategoryIO,
		Message:  "unable to create private assessment directory",
	})
}

// makeAssessmentTemporaryDirectory atomically claims one of a finite set of
// private directory slots. An existing entry, regardless of its type or
// ownership, is skipped without inspection and is never deleted or reused.
// Mkdir requests 0700; the process umask may make that stricter. Callers must
// not widen the resulting permissions through the returned pathname.
func makeAssessmentTemporaryDirectory(temporaryRoot string) (string, error) {
	for slot := 0; slot < maxAssessmentTemporaryDirectorySlots; slot++ {
		candidate := filepath.Join(
			temporaryRoot,
			fmt.Sprintf("%s%02d", assessmentTemporaryDirectoryPrefix, slot),
		)
		err := os.Mkdir(candidate, 0o700)
		if err == nil {
			return candidate, nil
		}
		if errors.Is(err, os.ErrExist) {
			continue
		}
		return "", assessmentTemporaryDirectoryUnavailable()
	}
	return "", assessmentTemporaryDirectoryUnavailable()
}
