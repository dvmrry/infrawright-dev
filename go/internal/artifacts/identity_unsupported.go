//go:build (!darwin && !linux) || ios || android || (!amd64 && !arm64)

package artifacts

import "io/fs"

func platformMetadataIdentity(fs.FileInfo) (metadataIdentity, bool) {
	return metadataIdentity{}, false
}

func platformOwnerID(fs.FileInfo) (uint32, bool) {
	return 0, false
}
