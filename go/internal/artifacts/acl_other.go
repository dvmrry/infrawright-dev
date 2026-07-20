//go:build !darwin || ios || (!amd64 && !arm64)

package artifacts

import "os"

func platformDescriptorHasExtendedACL(*os.File) (bool, error) {
	return false, nil
}
