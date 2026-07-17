//go:build windows

package terraformcmd

import "os"

func executableAccess(path string) error {
	_, err := os.Stat(path)
	return err
}
