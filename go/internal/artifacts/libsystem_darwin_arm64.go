//go:build darwin && !ios && arm64

package artifacts

import (
	"runtime"
	"syscall"
	"unsafe"
)

func darwinLibSystemFstatat(
	fd int,
	path string,
	stat *syscall.Stat_t,
	flags int,
) error {
	pathPointer, pathErr := syscall.BytePtrFromString(path)
	if pathErr != nil {
		return pathErr
	}
	_, _, errno := darwinLibSystemSyscall6(
		libcArtifactsFstatatTrampolineAddr,
		uintptr(fd),
		uintptr(unsafe.Pointer(pathPointer)),
		uintptr(unsafe.Pointer(stat)),
		uintptr(flags),
		0,
		0,
	)
	runtime.KeepAlive(pathPointer)
	runtime.KeepAlive(stat)
	if errno != 0 {
		return errno
	}
	return nil
}

var libcArtifactsFstatatTrampolineAddr uintptr

//go:cgo_import_dynamic libc_artifacts_fstatat fstatat "/usr/lib/libSystem.B.dylib"
