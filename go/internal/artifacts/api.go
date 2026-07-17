package artifacts

import (
	"math/big"

	"github.com/dvmrry/infrawright-dev/go/internal/procerr"
)

const (
	readChunkBytes = 1024 * 1024
	// nodeMaximumStringLength freezes buffer.constants.MAX_STRING_LENGTH under
	// the reviewed 64-bit Node v24.15.0 oracle.
	nodeMaximumStringLength      int64 = 536_870_888
	maximumJavaScriptSafeInteger       = 9_007_199_254_740_991
)

// BoundedReadLimits constrains a related set of stable file and directory
// observations. MaxTotalBytes and MaxFileBytes use arbitrary-precision
// integers to preserve the source BigInt domain. NewReadBudget defensively
// copies both values; nil is invalid.
type BoundedReadLimits struct {
	MaxFiles            int
	MaxDirectories      int
	MaxDirectoryEntries int
	MaxDepth            int
	MaxTotalBytes       *big.Int
	MaxFileBytes        *big.Int
}

// DefaultBoundedReadLimits returns a detached copy of the source defaults.
func DefaultBoundedReadLimits() BoundedReadLimits {
	return BoundedReadLimits{
		MaxFiles:            50_000,
		MaxDirectories:      10_000,
		MaxDirectoryEntries: 100_000,
		MaxDepth:            128,
		MaxTotalBytes:       big.NewInt(2 * 1024 * 1024 * 1024),
		MaxFileBytes:        big.NewInt(16 * 1024 * 1024),
	}
}

// StableReadHooks are deterministic race seams for tests. Production callers
// should leave both functions nil. A hook must return rather than panic.
type StableReadHooks struct {
	AfterOpen       func() error
	BeforeFinalStat func() error
}

// StableReadOptions controls whether a final symbolic link may be followed.
// Intermediate path-component handling remains the host filesystem's path
// resolution contract, matching Node's O_NOFOLLOW use on the final component.
type StableReadOptions struct {
	FollowSymlinks bool
	Hooks          StableReadHooks
}

// StableFileDigest binds a SHA-256 digest to the byte count consumed from one
// stable descriptor.
type StableFileDigest struct {
	SHA256 string
	Size   int64
}

// StableFileIdentity identifies one filesystem object.
type StableFileIdentity struct {
	Dev uint64
	Ino uint64
}

// StableFileSnapshot identifies a private saved-plan copy.
type StableFileSnapshot struct {
	Path string
	StableFileDigest
	StableFileIdentity
}

// BoundedFileBytes is a caller-owned byte snapshot and its stable bindings.
// Callers handling sensitive content are responsible for clearing Bytes when
// their processing is complete.
type BoundedFileBytes struct {
	Bytes    []byte
	Digest   StableFileDigest
	Identity StableFileIdentity
}

// BoundedUTF8File is a decoded stable file and its byte-level bindings.
type BoundedUTF8File struct {
	Text     string
	Digest   StableFileDigest
	Identity StableFileIdentity
}

// SnapshotStableFileOptions describes one private saved-plan copy.
type SnapshotStableFileOptions struct {
	SourcePath       string
	PrivateDirectory string
	Budget           *ReadBudget
	ReadOptions      StableReadOptions
}

func failure(code, message string, category procerr.Category) *procerr.ProcessFailure {
	return procerr.NewProcessFailure(procerr.NewProcessFailureOptions{
		Code:     code,
		Category: category,
		Message:  message,
	})
}

func domainFailure(code, message string) *procerr.ProcessFailure {
	return failure(code, message, procerr.CategoryDomain)
}

func ioFailure(code, message string) *procerr.ProcessFailure {
	return failure(code, message, procerr.CategoryIO)
}

func unsupportedPlatformFailure() *procerr.ProcessFailure {
	return ioFailure(
		"UNSUPPORTED_BOUNDED_FILE_PLATFORM",
		"bounded stable file operations are supported only on Linux and macOS amd64/arm64",
	)
}
