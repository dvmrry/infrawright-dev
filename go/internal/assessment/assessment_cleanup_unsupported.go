//go:build (!darwin && !linux) || ios || android || (!amd64 && !arm64)

package assessment

import "os"

const assessmentCleanupPlatformSupported = false

func assessmentCleanupFileIdentity(os.FileInfo) (assessmentCleanupIdentity, bool) {
	return assessmentCleanupIdentity{}, false
}
