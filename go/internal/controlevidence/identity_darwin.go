//go:build darwin && !ios && (amd64 || arm64)

package controlevidence

import (
	"io/fs"
	"syscall"

	"github.com/dvmrry/infrawright-dev/go/internal/artifacts"
)

func stableIdentityPlatformSupported() bool {
	return true
}

func stableIdentity(info fs.FileInfo) (artifacts.StableFileIdentity, bool) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return artifacts.StableFileIdentity{}, false
	}
	return artifacts.StableFileIdentity{
		Dev: uint64(stat.Dev),
		Ino: stat.Ino,
	}, true
}
