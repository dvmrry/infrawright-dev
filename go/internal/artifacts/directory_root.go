package artifacts

import (
	"errors"
	"fmt"
	"os"
)

// privateDirectoryRoot owns the descriptor that binds a private snapshot
// directory. Its operations are descriptor-relative and accept only names
// generated inside this package.
type privateDirectoryRoot struct {
	descriptor *os.File
}

func (r *privateDirectoryRoot) Close() error {
	if r == nil || r.descriptor == nil {
		return nil
	}
	return r.descriptor.Close()
}

func (r *privateDirectoryRoot) OpenFile(
	name string,
	flag int,
	perm os.FileMode,
) (*os.File, error) {
	if r == nil || r.descriptor == nil {
		return nil, errors.New("snapshot directory root is closed")
	}
	return platformRootOpenFile(r.descriptor, name, flag, perm)
}

func (r *privateDirectoryRoot) pathIdentity(name string) (metadataIdentity, error) {
	if r == nil || r.descriptor == nil {
		return metadataIdentity{}, fileChangedFailure()
	}
	return platformRootPathIdentity(r.descriptor, name)
}

func withFileDescriptor(file *os.File, operation func(fd int) error) error {
	if file == nil {
		return errors.New("cannot use a nil file descriptor")
	}
	raw, err := file.SyscallConn()
	if err != nil {
		return fmt.Errorf("access raw file descriptor: %w", err)
	}
	var operationErr error
	controlErr := raw.Control(func(fd uintptr) {
		operationErr = operation(int(fd))
	})
	if controlErr != nil {
		return fmt.Errorf("control raw file descriptor: %w", controlErr)
	}
	if operationErr != nil {
		return fmt.Errorf("perform descriptor-relative operation: %w", operationErr)
	}
	return nil
}
