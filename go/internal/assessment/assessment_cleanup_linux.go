//go:build linux && !android && (amd64 || arm64)

package assessment

import (
	"os"
	"syscall"
)

const assessmentCleanupPlatformSupported = true

func assessmentCleanupFileIdentity(info os.FileInfo) (assessmentCleanupIdentity, bool) {
	if info == nil {
		return assessmentCleanupIdentity{}, false
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return assessmentCleanupIdentity{}, false
	}
	return assessmentCleanupIdentity{dev: uint64(stat.Dev), ino: stat.Ino}, true
}
