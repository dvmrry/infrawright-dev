//go:build darwin && !ios && (amd64 || arm64)

package artifacts

import (
	"encoding/binary"
	"errors"
	"os"
	"runtime"
	"syscall"
)

const (
	darwinAttributeBitmapCount      = 5
	darwinAttributeExtendedSecurity = 0x00400000
	darwinReportFullAttributeSize   = 0x00000004
	darwinACLBufferSize             = 64 << 10
)

type darwinAttributeList struct {
	bitmapCount uint16
	reserved    uint16
	common      uint32
	volume      uint32
	directory   uint32
	file        uint32
	fork        uint32
}

// platformDescriptorHasExtendedACL uses libSystem fgetattrlist rather than a
// path or xattr API. macOS hides com.apple.system.Security from ordinary xattr
// reads; ATTR_CMN_EXTENDED_SECURITY is the descriptor-bound interface that
// reports the access-control list.
func platformDescriptorHasExtendedACL(file *os.File) (bool, error) {
	if file == nil {
		return false, errors.New("cannot inspect extended ACL on nil file")
	}
	raw, err := file.SyscallConn()
	if err != nil {
		return false, err
	}
	var (
		hasACL     bool
		inspectErr error
	)
	controlErr := raw.Control(func(fd uintptr) {
		hasACL, inspectErr = darwinDescriptorHasExtendedACL(fd)
	})
	if controlErr != nil {
		return false, controlErr
	}
	return hasACL, inspectErr
}

func darwinDescriptorHasExtendedACL(fd uintptr) (bool, error) {
	attributes := darwinAttributeList{
		bitmapCount: darwinAttributeBitmapCount,
		common:      darwinAttributeExtendedSecurity,
	}
	buffer := make([]byte, darwinACLBufferSize)
	defer func() {
		clear(buffer)
		runtime.KeepAlive(buffer)
	}()
	for {
		err := darwinLibSystemFgetattrlist(
			int(fd),
			&attributes,
			buffer,
			darwinReportFullAttributeSize,
		)
		if errors.Is(err, syscall.EINTR) {
			continue
		}
		if err != nil {
			return false, err
		}
		break
	}

	total := int(binary.LittleEndian.Uint32(buffer[0:4]))
	if total < 12 || total > len(buffer) {
		return false, errors.New("malformed Darwin extended ACL response length")
	}
	dataOffset := int(int32(binary.LittleEndian.Uint32(buffer[4:8])))
	dataLength := int(binary.LittleEndian.Uint32(buffer[8:12]))
	dataStart := 4 + dataOffset
	if dataOffset < 8 || dataStart > total || dataLength > total-dataStart {
		return false, errors.New("malformed Darwin extended ACL reference")
	}
	return dataLength != 0, nil
}
