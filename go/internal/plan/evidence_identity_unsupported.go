//go:build (!darwin && !linux) || ios || android || (!amd64 && !arm64)

package plan

import (
	"io/fs"

	"github.com/dvmrry/infrawright-dev/go/internal/artifacts"
)

const evidencePlatformSupported = false

func evidenceFileIdentity(fs.FileInfo) (artifacts.StableFileIdentity, bool) {
	return artifacts.StableFileIdentity{}, false
}
