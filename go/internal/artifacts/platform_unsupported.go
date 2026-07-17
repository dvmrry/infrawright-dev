//go:build (!darwin && !linux) || ios || android || (!amd64 && !arm64)

package artifacts

import "os"

const boundedFilePlatformSupported = false

func openPrivateDirectoryDescriptor(string) (*os.File, error) {
	return nil, unsupportedPlatformFailure()
}

func platformRootOpenFile(*os.File, string, int, os.FileMode) (*os.File, error) {
	return nil, unsupportedPlatformFailure()
}

func platformRootPathIdentity(*os.File, string) (metadataIdentity, error) {
	return metadataIdentity{}, unsupportedPlatformFailure()
}

func openStableFile(string, bool) (*os.File, error) {
	return nil, unsupportedPlatformFailure()
}

func platformEffectiveUID() (uint32, bool) {
	return 0, false
}
