//go:build darwin && !ios && (amd64 || arm64)

package artifacts

import (
	"errors"
	"runtime"
	"syscall"
	"unsafe"
)

// darwinLibSystemSyscall6 invokes a libSystem function address. This is the
// same runtime entry point used by Go's generated Darwin syscall wrappers.
//
//go:linkname darwinLibSystemSyscall6 syscall.syscall6
func darwinLibSystemSyscall6(
	function, argument1, argument2, argument3, argument4, argument5, argument6 uintptr,
) (result1, result2 uintptr, errno syscall.Errno)

func darwinLibSystemOpenat(
	directoryFD int,
	path string,
	flags int,
	perm uint32,
) (int, error) {
	pathPointer, err := syscall.BytePtrFromString(path)
	if err != nil {
		return -1, err
	}
	result, _, errno := darwinLibSystemSyscall6(
		libcArtifactsOpenatTrampolineAddr,
		uintptr(directoryFD),
		uintptr(unsafe.Pointer(pathPointer)),
		uintptr(flags),
		uintptr(perm),
		0,
		0,
	)
	runtime.KeepAlive(pathPointer)
	if errno != 0 {
		return -1, errno
	}
	return int(result), nil
}

func darwinLibSystemFgetattrlist(
	fd int,
	attributes *darwinAttributeList,
	buffer []byte,
	options uint32,
) error {
	if attributes == nil || len(buffer) == 0 {
		return errors.New("invalid Darwin attribute-list buffer")
	}
	_, _, errno := darwinLibSystemSyscall6(
		libcArtifactsFgetattrlistTrampolineAddr,
		uintptr(fd),
		uintptr(unsafe.Pointer(attributes)),
		uintptr(unsafe.Pointer(&buffer[0])),
		uintptr(len(buffer)),
		uintptr(options),
		0,
	)
	runtime.KeepAlive(attributes)
	runtime.KeepAlive(buffer)
	if errno != 0 {
		return errno
	}
	return nil
}

var libcArtifactsOpenatTrampolineAddr uintptr
var libcArtifactsFgetattrlistTrampolineAddr uintptr

//go:cgo_import_dynamic libc_artifacts_openat openat "/usr/lib/libSystem.B.dylib"
//go:cgo_import_dynamic libc_artifacts_fgetattrlist fgetattrlist "/usr/lib/libSystem.B.dylib"
