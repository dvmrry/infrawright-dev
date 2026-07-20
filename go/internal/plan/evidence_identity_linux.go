//go:build linux && !android && (amd64 || arm64)

package plan

import (
	"io/fs"
	"syscall"

	"github.com/dvmrry/infrawright-dev/go/internal/artifacts"
)

const evidencePlatformSupported = true

func evidenceFileIdentity(info fs.FileInfo) (artifacts.StableFileIdentity, bool) {
	if info == nil {
		return artifacts.StableFileIdentity{}, false
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return artifacts.StableFileIdentity{}, false
	}
	return artifacts.StableFileIdentity{Dev: uint64(stat.Dev), Ino: stat.Ino}, true
}
