//go:build (!darwin && !linux) || ios || android || (!amd64 && !arm64)

package plan

import (
	"errors"
	"os"
)

func openEvidenceSnapshotFile(string) (*os.File, error) {
	return nil, errors.New("saved-plan snapshot descriptors are unsupported on this platform")
}
