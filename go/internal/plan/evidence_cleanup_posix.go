//go:build (darwin || linux) && !ios && !android && (amd64 || arm64)

package plan

import (
	"errors"
	"os"
	"syscall"
)

func openEvidenceCleanupFile(filePath string) (*os.File, error) {
	for {
		descriptor, err := syscall.Open(
			filePath,
			syscall.O_RDWR|syscall.O_NONBLOCK|syscall.O_NOFOLLOW|syscall.O_CLOEXEC,
			0,
		)
		if errors.Is(err, syscall.EINTR) {
			continue
		}
		if err != nil {
			return nil, err
		}
		file := os.NewFile(uintptr(descriptor), filePath)
		if file == nil {
			// The descriptor cannot be recovered if os.NewFile refuses it.
			_ = syscall.Close(descriptor)
			return nil, errors.New("unable to bind saved-plan snapshot")
		}
		return file, nil
	}
}
