//go:build !darwin && !linux

package nodefserr

import "syscall"

// classifyNativeErrno fails closed outside the Darwin/Linux release matrix.
// Native Node parity on those platforms requires a platform-specific oracle;
// direct portable io/fs sentinels remain supported by classifyDirect.
func classifyNativeErrno(syscall.Errno) errorCode {
	return codeUnknown
}
