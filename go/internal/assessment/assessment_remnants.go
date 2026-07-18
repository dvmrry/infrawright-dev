package assessment

import (
	"os"

	"github.com/dvmrry/infrawright-dev/go/internal/procerr"
)

const assessmentTemporaryDirectoryPrefix = "infrawright-assessment-"

func assessmentTemporaryDirectoryUnavailable() *procerr.ProcessFailure {
	return procerr.NewProcessFailure(procerr.NewProcessFailureOptions{
		Code:     "ASSESSMENT_TEMPORARY_DIRECTORY_UNAVAILABLE",
		Category: procerr.CategoryIO,
		Message:  "unable to create private assessment directory",
	})
}

// makeAssessmentTemporaryDirectory creates a fresh private directory.
// MkdirTemp requests 0700; the process umask may make that stricter. Callers
// must not widen the resulting permissions through the returned pathname.
func makeAssessmentTemporaryDirectory(temporaryRoot string) (string, error) {
	directory, err := os.MkdirTemp(temporaryRoot, assessmentTemporaryDirectoryPrefix)
	if err != nil {
		return "", assessmentTemporaryDirectoryUnavailable()
	}
	return directory, nil
}
