//go:build (darwin || linux) && !ios && !android && (amd64 || arm64)

package artifacts

import (
	"errors"
	"os"
	"syscall"
)

const boundedFilePlatformSupported = true

func openStableFile(filePath string, followSymlinks bool) (*os.File, error) {
	noFollow := syscall.O_NOFOLLOW
	if followSymlinks {
		noFollow = 0
	}
	fd, err := syscall.Open(
		filePath,
		syscall.O_RDONLY|syscall.O_NONBLOCK|syscall.O_CLOEXEC|noFollow,
		0,
	)
	if err != nil {
		if !followSymlinks && errors.Is(err, syscall.ELOOP) {
			return nil, ioFailure(
				"SYMLINK_NOT_ALLOWED",
				"input file must not be a symbolic link",
			)
		}
		return nil, ioFailure("READ_FAILED", "unable to open input file")
	}
	return os.NewFile(uintptr(fd), filePath), nil
}

func platformEffectiveUID() (uint32, bool) {
	return uint32(os.Geteuid()), true
}
