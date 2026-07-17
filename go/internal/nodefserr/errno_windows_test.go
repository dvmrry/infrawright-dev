//go:build windows

package nodefserr

import (
	"io/fs"
	"syscall"
	"testing"
)

func TestWindowsNativeErrnoFailsClosed(t *testing.T) {
	tests := []struct {
		name  string
		errno syscall.Errno
	}{
		{name: "file_not_found", errno: syscall.ERROR_FILE_NOT_FOUND},
		{name: "path_not_found", errno: syscall.ERROR_PATH_NOT_FOUND},
		{name: "access_denied", errno: syscall.ERROR_ACCESS_DENIED},
		{name: "already_exists", errno: syscall.ERROR_ALREADY_EXISTS},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := classifyDirect(test.errno); got != codeUnknown {
				t.Errorf("classifyDirect(%d) = %q, want %q", test.errno, got, codeUnknown)
			}

			cause := &fs.PathError{Op: "mkdir", Path: `C:\requested\path`, Err: test.errno}
			call := Call{Operation: MkdirAll, Path: cause.Path}
			if got := call.Wrap(cause); got != cause {
				t.Errorf("Call%+v.Wrap(%v) = %v, want original unsupported Windows error %v", call, cause, got, cause)
			}
		})
	}
}
