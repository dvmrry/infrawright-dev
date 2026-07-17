//go:build !aix && !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd && !solaris && !windows

package terraformcmd

import "os"

func executableAccess(path string) error {
	_, err := os.Stat(path)
	return err
}
