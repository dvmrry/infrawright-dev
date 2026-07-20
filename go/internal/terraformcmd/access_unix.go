//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package terraformcmd

import "syscall"

func executableAccess(path string) error {
	// POSIX X_OK is 1; syscall does not export the name on every supported
	// target (notably Darwin).
	return syscall.Access(path, 1)
}
