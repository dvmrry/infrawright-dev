package artifacts

import (
	"errors"
	"io/fs"
	"os"

	"github.com/dvmrry/infrawright-dev/go/internal/procerr"
)

type metadataIdentity struct {
	dev       uint64
	ino       uint64
	size      int64
	mtimeSec  int64
	mtimeNsec int64
	ctimeSec  int64
	ctimeNsec int64
}

func (i metadataIdentity) stableIdentity() StableFileIdentity {
	return StableFileIdentity{Dev: i.dev, Ino: i.ino}
}

func sameIdentity(left, right metadataIdentity) bool {
	return left == right
}

func descriptorIdentity(file *os.File) (metadataIdentity, fs.FileInfo, error) {
	info, err := file.Stat()
	if err != nil {
		return metadataIdentity{}, nil, err
	}
	identity, ok := platformMetadataIdentity(info)
	if !ok {
		return metadataIdentity{}, nil, unsupportedPlatformFailure()
	}
	return identity, info, nil
}

func pathIdentity(filePath string, followSymlinks bool) (metadataIdentity, error) {
	var (
		info fs.FileInfo
		err  error
	)
	if followSymlinks {
		info, err = os.Stat(filePath)
	} else {
		info, err = os.Lstat(filePath)
	}
	if err != nil {
		return metadataIdentity{}, fileChangedFailure()
	}
	if !info.Mode().IsRegular() || (!followSymlinks && info.Mode()&fs.ModeSymlink != 0) {
		return metadataIdentity{}, fileChangedFailure()
	}
	identity, ok := platformMetadataIdentity(info)
	if !ok {
		return metadataIdentity{}, unsupportedPlatformFailure()
	}
	return identity, nil
}

func rootPathIdentity(root *privateDirectoryRoot, name string) (metadataIdentity, error) {
	return root.pathIdentity(name)
}

func fileChangedFailure() error {
	return ioFailure(
		"FILE_CHANGED",
		"input file changed while it was read",
	)
}

func preserveProcessFailure(err error) (*procerr.ProcessFailure, bool) {
	var failure *procerr.ProcessFailure
	if !errors.As(err, &failure) {
		return nil, false
	}
	return failure, true
}
