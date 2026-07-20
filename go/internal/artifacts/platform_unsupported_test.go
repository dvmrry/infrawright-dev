//go:build (!darwin && !linux) || ios || android || (!amd64 && !arm64)

package artifacts

import (
	"testing"

	"github.com/dvmrry/infrawright-dev/go/internal/procerr"
)

func TestStableFilesystemOperationsFailClosedOnUnsupportedPlatform(t *testing.T) {
	budget := NewDefaultReadBudget()
	_, err := SHA256StableFile("missing", budget, StableReadOptions{})
	requireFailure(t, err, "UNSUPPORTED_BOUNDED_FILE_PLATFORM", procerr.CategoryIO)
	_, err = ReadBoundedFileBytes("missing", budget, StableReadOptions{})
	requireFailure(t, err, "UNSUPPORTED_BOUNDED_FILE_PLATFORM", procerr.CategoryIO)
	_, err = ReadBoundedUTF8File("missing", budget, StableReadOptions{})
	requireFailure(t, err, "UNSUPPORTED_BOUNDED_FILE_PLATFORM", procerr.CategoryIO)
	_, err = SnapshotStableFile(SnapshotStableFileOptions{
		SourcePath:       "missing",
		PrivateDirectory: "missing",
		Budget:           budget,
	})
	requireFailure(t, err, "UNSUPPORTED_BOUNDED_FILE_PLATFORM", procerr.CategoryIO)
}
