//go:build darwin && !ios && (amd64 || arm64)

package artifacts

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
)

const (
	// Darwin's O_SEARCH opens a directory for descriptor-relative lookup
	// without imposing an O_RDONLY permission that Node does not require.
	darwinOpenSearch = 0x40100000

	darwinAtSymlinkNoFollow = 0x0020
)

func openPrivateDirectoryDescriptor(path string) (*os.File, error) {
	fd, err := retryDarwinOpen(
		path,
		darwinOpenSearch|syscall.O_NONBLOCK|syscall.O_NOFOLLOW|syscall.O_CLOEXEC,
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
	return darwinOpenAtFile(
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
	var stat syscall.Stat_t
	err := withFileDescriptor(directory, func(directoryFD int) error {
		for {
			statErr := darwinLibSystemFstatat(
				directoryFD,
				name,
				&stat,
				darwinAtSymlinkNoFollow,
			)
			if !errors.Is(statErr, syscall.EINTR) {
				return statErr
			}
		}
	})
	if err != nil || uint32(stat.Mode)&syscall.S_IFMT != syscall.S_IFREG {
		return metadataIdentity{}, fileChangedFailure()
	}
	return metadataIdentity{
		dev:       uint64(stat.Dev),
		ino:       stat.Ino,
		size:      stat.Size,
		mtimeSec:  stat.Mtimespec.Sec,
		mtimeNsec: stat.Mtimespec.Nsec,
		ctimeSec:  stat.Ctimespec.Sec,
		ctimeNsec: stat.Ctimespec.Nsec,
	}, nil
}

func darwinOpenAtFile(
	directory *os.File,
	name string,
	flag int,
	perm uint32,
) (*os.File, error) {
	childFD := -1
	err := withFileDescriptor(directory, func(directoryFD int) error {
		for {
			var openErr error
			childFD, openErr = darwinLibSystemOpenat(directoryFD, name, flag, perm)
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

func retryDarwinOpen(path string, flag int, perm uint32) (int, error) {
	for {
		fd, err := syscall.Open(path, flag, perm)
		if !errors.Is(err, syscall.EINTR) {
			return fd, err
		}
	}
}
