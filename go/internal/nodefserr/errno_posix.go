//go:build darwin || linux

package nodefserr

import "syscall"

// classifyNativeErrno maps the exact native errno values on the release
// platforms. Exact matching avoids collapsing EPERM or ENOTEMPTY through
// broader portable error classes.
func classifyNativeErrno(errno syscall.Errno) errorCode {
	switch errno {
	case syscall.ENOENT:
		return codeENOENT
	case syscall.EEXIST:
		return codeEEXIST
	case syscall.EACCES:
		return codeEACCES
	case syscall.ENOTDIR:
		return codeENOTDIR
	case syscall.EISDIR:
		return codeEISDIR
	default:
		return codeUnknown
	}
}
