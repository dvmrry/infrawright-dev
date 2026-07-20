//go:build linux && !android && (amd64 || arm64)

package terraformcmd

import (
	"errors"
	"os"
	"syscall"
)

func inheritedPlanFilePath() (string, error) {
	if info, err := os.Stat("/proc/self/fd"); err != nil || !info.IsDir() {
		return "", os.ErrNotExist
	}
	return "/proc/self/fd/3", nil
}

func validateInheritedPlanFile(file *os.File) error {
	if file == nil {
		return errors.New("missing inherited plan file")
	}
	info, err := file.Stat()
	if err != nil || info == nil || !info.Mode().IsRegular() {
		return errors.New("invalid inherited plan file")
	}
	flags, _, errno := syscall.Syscall(syscall.SYS_FCNTL, file.Fd(), syscall.F_GETFL, 0)
	if errno != 0 || flags&syscall.O_ACCMODE != syscall.O_RDONLY {
		return errors.New("inherited plan file is not read-only")
	}
	return nil
}
