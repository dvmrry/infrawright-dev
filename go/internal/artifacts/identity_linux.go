//go:build linux && !android && (amd64 || arm64)

package artifacts

import (
	"io/fs"
	"syscall"
)

func platformMetadataIdentity(info fs.FileInfo) (metadataIdentity, bool) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return metadataIdentity{}, false
	}
	return metadataIdentity{
		dev:       uint64(stat.Dev),
		ino:       stat.Ino,
		size:      stat.Size,
		mtimeSec:  stat.Mtim.Sec,
		mtimeNsec: stat.Mtim.Nsec,
		ctimeSec:  stat.Ctim.Sec,
		ctimeNsec: stat.Ctim.Nsec,
	}, true
}

func platformOwnerID(info fs.FileInfo) (uint32, bool) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, false
	}
	return stat.Uid, true
}
