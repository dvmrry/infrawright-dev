//go:build (!darwin && !linux) || ios || android || (!amd64 && !arm64)

package plan

import (
	"errors"
	"os"
)

func openEvidenceCleanupFile(string) (*os.File, error) {
	return nil, errors.New("saved-plan evidence cleanup is unsupported on this platform")
}
