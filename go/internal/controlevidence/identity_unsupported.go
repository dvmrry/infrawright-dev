//go:build (!darwin && !linux) || ios || android || (!amd64 && !arm64)

package controlevidence

import (
	"io/fs"

	"github.com/dvmrry/infrawright-dev/go/internal/artifacts"
)

func stableIdentityPlatformSupported() bool {
	return false
}

func stableIdentity(fs.FileInfo) (artifacts.StableFileIdentity, bool) {
	return artifacts.StableFileIdentity{}, false
}
