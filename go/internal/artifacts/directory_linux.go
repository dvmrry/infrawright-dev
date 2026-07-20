//go:build linux && !android && (amd64 || arm64)

package artifacts

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
)

// O_PATH is stable across supported Linux architectures but is not exported
// by syscall on every one of them. It binds a directory without requiring the
// read permission that Node's snapshot contract does not require.
const linuxOpenPath = 0x00200000

func openPrivateDirectoryDescriptor(path string) (*os.File, error) {
	fd, err := retryLinuxOpen(path,
		linuxOpenPath|syscall.O_DIRECTORY|syscall.O_NONBLOCK|syscall.O_NOFOLLOW|syscall.O_CLOEXEC,
		0,
	)
	if err != nil {
		return nil, err
	}
	descriptor := os.NewFile(uintptr(fd), path)
	if descriptor == nil {
		_ = syscall.Close(fd)
		return nil, errors.New("unable to bind private snapshot directory")
	}
	return descriptor, nil
}

func platformRootOpenFile(
	directory *os.File,
	name string,
	flag int,
	perm os.FileMode,
) (*os.File, error) {
	return linuxOpenAtFile(
		directory,
		name,
		flag|syscall.O_NOFOLLOW|syscall.O_CLOEXEC,
		uint32(perm.Perm()),
	)
}

func platformRootPathIdentity(
	directory *os.File,
	name string,
) (metadataIdentity, error) {
	file, err := linuxOpenAtFile(
		directory,
		name,
		linuxOpenPath|syscall.O_NONBLOCK|syscall.O_NOFOLLOW|syscall.O_CLOEXEC,
		0,
	)
	if err != nil {
		return metadataIdentity{}, fileChangedFailure()
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return metadataIdentity{}, fileChangedFailure()
	}
	identity, ok := platformMetadataIdentity(info)
	if !ok {
		return metadataIdentity{}, unsupportedPlatformFailure()
	}
	return identity, nil
}

func linuxOpenAtFile(
	directory *os.File,
	name string,
	flag int,
	perm uint32,
) (*os.File, error) {
	childFD := -1
	err := withFileDescriptor(directory, func(directoryFD int) error {
		var openErr error
		for {
			childFD, openErr = syscall.Openat(directoryFD, name, flag, perm)
			if !errors.Is(openErr, syscall.EINTR) {
				return openErr
			}
		}
	})
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(childFD), filepath.Join(directory.Name(), name))
	if file == nil {
		_ = syscall.Close(childFD)
		return nil, errors.New("unable to bind descriptor-relative file")
	}
	return file, nil
}

func retryLinuxOpen(path string, flag int, perm uint32) (int, error) {
	for {
		fd, err := syscall.Open(path, flag, perm)
		if !errors.Is(err, syscall.EINTR) {
			return fd, err
		}
	}
}
