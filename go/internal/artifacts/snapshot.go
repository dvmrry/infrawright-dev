package artifacts

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/dvmrry/infrawright-dev/go/internal/procerr"
)

type privateDirectoryHooks struct {
	afterLstat   func(path string) error
	afterBind    func(path string, root *privateDirectoryRoot) error
	beforeCreate func(path string, root *privateDirectoryRoot) error
}

type privateDirectoryBinding struct {
	path     string
	root     *privateDirectoryRoot
	identity StableFileIdentity
}

type privateDirectoryInspection struct {
	path     string
	identity StableFileIdentity
}

// SnapshotStableFile copies one bounded stable source into a new mode-0600
// file inside a caller-owned private directory. The directory is bound through
// a nonblocking, no-follow descriptor after initial path classification, so a
// concurrent path swap cannot redirect or stall it. Creation and path
// inspection remain relative to that descriptor without requiring directory
// read permission.
// The completed destination is reread, hashed, and identity-checked through
// its bound descriptor before success. A failed partial copy is scrubbed
// through that descriptor and is never removed by a path that a concurrent
// parent swap could redirect.
func SnapshotStableFile(options SnapshotStableFileOptions) (StableFileSnapshot, error) {
	return snapshotStableFile(options, privateDirectoryHooks{})
}

func snapshotStableFile(
	options SnapshotStableFileOptions,
	hooks privateDirectoryHooks,
) (StableFileSnapshot, error) {
	if !boundedFilePlatformSupported {
		return StableFileSnapshot{}, unsupportedPlatformFailure()
	}
	if !options.Budget.initialized() {
		return StableFileSnapshot{}, uninitializedReadBudgetFailure()
	}
	inspection, err := inspectPrivateDirectory(options.PrivateDirectory, hooks)
	if err != nil {
		return StableFileSnapshot{}, err
	}
	snapshotName, err := newSnapshotName()
	if err != nil {
		return StableFileSnapshot{}, snapshotCreationFailure()
	}
	binding, err := bindPrivateDirectory(inspection, hooks)
	if err != nil {
		return StableFileSnapshot{}, err
	}
	defer func() {
		// The directory descriptor has no pending data operation. As with the
		// source read descriptor, a close failure does not replace an already
		// established operation result.
		_ = binding.root.Close()
	}()
	if err := binding.revalidate(); err != nil {
		return StableFileSnapshot{}, err
	}
	if hooks.beforeCreate != nil {
		if err := invokeStableReadHook(func() error {
			return hooks.beforeCreate(binding.path, binding.root)
		}); err != nil {
			return StableFileSnapshot{}, normalizeSnapshotDirectoryHookFailure(err)
		}
	}
	destination, err := binding.root.OpenFile(
		snapshotName,
		os.O_RDWR|os.O_CREATE|os.O_EXCL,
		0o600,
	)
	if err != nil {
		return StableFileSnapshot{}, snapshotCreationFailure()
	}

	snapshotPath := filepath.Join(binding.path, snapshotName)
	result, operationErr := createStableSnapshot(
		destination,
		binding,
		snapshotName,
		snapshotPath,
		options,
	)
	if operationErr == nil {
		return result, nil
	}
	return StableFileSnapshot{}, scrubFailedSnapshot(destination, operationErr)
}

func inspectPrivateDirectory(
	directory string,
	hooks privateDirectoryHooks,
) (*privateDirectoryInspection, error) {
	// Preserve Node's initial lstat failure phase on the caller's raw spelling.
	// A second check below deliberately validates the normalized directory that
	// descriptor-relative creation will actually target.
	rawInfo, err := os.Lstat(directory)
	if err != nil {
		return nil, snapshotDirectoryInspectionFailure()
	}
	normalized := directory
	if directory != "" {
		// filepath.Join, like Node path.join, normalizes the snapshot's eventual
		// path. Validate and bind that same directory object. This also exposes a
		// trailing "link/" or "link/." as the final symlink to Lstat.
		normalized = filepath.Clean(directory)
	}
	pathInfo := rawInfo
	if normalized != directory {
		pathInfo, err = os.Lstat(normalized)
		if err != nil {
			return nil, snapshotDirectoryInspectionFailure()
		}
	}
	pathIdentity, err := privateDirectoryIdentity(pathInfo)
	if err != nil {
		return nil, err
	}
	if hooks.afterLstat != nil {
		if err := invokeStableReadHook(func() error { return hooks.afterLstat(normalized) }); err != nil {
			return nil, normalizeSnapshotDirectoryHookFailure(err)
		}
	}
	return &privateDirectoryInspection{path: normalized, identity: pathIdentity}, nil
}

func bindPrivateDirectory(
	inspection *privateDirectoryInspection,
	hooks privateDirectoryHooks,
) (_ *privateDirectoryBinding, err error) {
	if inspection == nil {
		return nil, unsafeSnapshotDirectoryFailure()
	}
	directoryDescriptor, err := openPrivateDirectoryDescriptor(inspection.path)
	if err != nil {
		return nil, normalizePrivateDirectoryBindFailure(inspection)
	}
	root := &privateDirectoryRoot{descriptor: directoryDescriptor}
	keepRoot := false
	defer func() {
		if !keepRoot {
			_ = root.Close()
		}
	}()
	descriptorIdentity, err := privateDirectoryDescriptorIdentity(directoryDescriptor)
	if err != nil {
		return nil, err
	}
	if inspection.identity != descriptorIdentity {
		return nil, unsafeSnapshotDirectoryFailure()
	}

	binding := &privateDirectoryBinding{
		path:     inspection.path,
		root:     root,
		identity: descriptorIdentity,
	}
	if err := binding.revalidate(); err != nil {
		return nil, err
	}
	if hooks.afterBind != nil {
		if err := invokeStableReadHook(func() error { return hooks.afterBind(inspection.path, root) }); err != nil {
			return nil, normalizeSnapshotDirectoryHookFailure(err)
		}
	}
	if err := binding.revalidate(); err != nil {
		return nil, err
	}
	keepRoot = true
	return binding, nil
}

func normalizePrivateDirectoryBindFailure(inspection *privateDirectoryInspection) error {
	pathInfo, err := os.Lstat(inspection.path)
	if err != nil {
		return snapshotCreationFailure()
	}
	pathIdentity, identityErr := privateDirectoryIdentity(pathInfo)
	if identityErr != nil {
		return identityErr
	}
	if pathIdentity != inspection.identity {
		return unsafeSnapshotDirectoryFailure()
	}
	return snapshotCreationFailure()
}

func (b *privateDirectoryBinding) revalidate() error {
	rootIdentity, identityErr := privateRootDirectoryIdentity(b.root)
	if identityErr != nil {
		// Preserve the package's structured public failure unchanged.
		return identityErr
	}
	pathInfo, err := os.Lstat(b.path)
	if err != nil {
		return unsafeSnapshotDirectoryFailure()
	}
	pathIdentity, identityErr := privateDirectoryIdentity(pathInfo)
	if identityErr != nil {
		// Preserve the package's structured public failure unchanged.
		return identityErr
	}
	if rootIdentity != b.identity || pathIdentity != b.identity {
		return unsafeSnapshotDirectoryFailure()
	}
	return nil
}

func privateRootDirectoryIdentity(root *privateDirectoryRoot) (StableFileIdentity, error) {
	if root == nil || root.descriptor == nil {
		return StableFileIdentity{}, unsafeSnapshotDirectoryFailure()
	}
	return privateDirectoryDescriptorIdentity(root.descriptor)
}

func privateDirectoryDescriptorIdentity(file *os.File) (StableFileIdentity, error) {
	info, err := file.Stat()
	if err != nil {
		return StableFileIdentity{}, snapshotDirectoryInspectionFailure()
	}
	identity, err := privateDirectoryIdentity(info)
	if err != nil {
		return StableFileIdentity{}, err
	}
	hasExtendedACL, err := platformDescriptorHasExtendedACL(file)
	if err != nil {
		return StableFileIdentity{}, snapshotDirectoryInspectionFailure()
	}
	if hasExtendedACL {
		return StableFileIdentity{}, unsafeSnapshotDirectoryFailure()
	}
	return identity, nil
}

func privateDirectoryIdentity(info fs.FileInfo) (StableFileIdentity, error) {
	if !info.IsDir() || info.Mode()&fs.ModeSymlink != 0 || info.Mode().Perm()&0o077 != 0 {
		return StableFileIdentity{}, unsafeSnapshotDirectoryFailure()
	}
	owner, ownerOK := platformOwnerID(info)
	effectiveUID, effectiveUIDOK := platformEffectiveUID()
	if !ownerOK || !effectiveUIDOK {
		return StableFileIdentity{}, unsupportedPlatformFailure()
	}
	if owner != effectiveUID {
		return StableFileIdentity{}, unsafeSnapshotDirectoryFailure()
	}
	identity, ok := platformMetadataIdentity(info)
	if !ok {
		return StableFileIdentity{}, unsupportedPlatformFailure()
	}
	return identity.stableIdentity(), nil
}

func snapshotDirectoryInspectionFailure() error {
	return ioFailure(
		"SNAPSHOT_FAILED",
		"unable to inspect snapshot directory",
	)
}

func snapshotCreationFailure() error {
	return ioFailure(
		"SNAPSHOT_FAILED",
		"unable to create plan snapshot",
	)
}

func unsafeSnapshotDirectoryFailure() error {
	return ioFailure(
		"UNSAFE_SNAPSHOT_DIRECTORY",
		"snapshot directory is not private",
	)
}

func normalizeSnapshotDirectoryHookFailure(err error) error {
	if failure, ok := preserveProcessFailure(err); ok {
		return failure
	}
	return snapshotDirectoryInspectionFailure()
}

func createStableSnapshot(
	destination *os.File,
	binding *privateDirectoryBinding,
	snapshotName string,
	snapshotPath string,
	options SnapshotStableFileOptions,
) (StableFileSnapshot, error) {
	if err := destination.Chmod(0o600); err != nil {
		return StableFileSnapshot{}, err
	}
	initialIdentity, initialInfo, err := descriptorIdentity(destination)
	if err != nil {
		return StableFileSnapshot{}, err
	}
	initialPrivate, err := validPrivateSnapshotFile(
		destination,
		initialIdentity,
		initialInfo,
		0,
	)
	if err != nil {
		return StableFileSnapshot{}, err
	}
	if !initialPrivate {
		return StableFileSnapshot{}, snapshotPathChangedFailure()
	}
	result, err := consumeStableFile(consumeStableFileOptions{
		filePath:    options.SourcePath,
		budget:      options.Budget,
		readOptions: options.ReadOptions,
		onChunk: func(chunk []byte) error {
			return writeAll(destination, chunk)
		},
	})
	if err != nil {
		return StableFileSnapshot{}, err
	}
	if err := destination.Sync(); err != nil {
		return StableFileSnapshot{}, err
	}
	// Freeze Node's observable precedence before the Go-only reread hardening.
	// With a stable parent, missing, symlink, and non-regular path replacements
	// remain FILE_CHANGED even when the bound inode was also mutated. Go first
	// revalidates its bound parent; a compound parent replacement therefore
	// remains the intentional UNSAFE_SNAPSHOT_DIRECTORY divergence.
	observedIdentity, _, err := descriptorIdentity(destination)
	if err != nil {
		return StableFileSnapshot{}, err
	}
	if err := binding.revalidate(); err != nil {
		return StableFileSnapshot{}, err
	}
	observedPathIdentity, err := rootPathIdentity(binding.root, snapshotName)
	if err != nil {
		return StableFileSnapshot{}, err
	}
	if !sameIdentity(observedIdentity, observedPathIdentity) {
		return StableFileSnapshot{}, snapshotPathChangedFailure()
	}
	verifiedIdentity, err := verifyStableSnapshotDestination(
		destination,
		result.StableFileDigest,
	)
	if err != nil {
		return StableFileSnapshot{}, err
	}
	if err := binding.revalidate(); err != nil {
		return StableFileSnapshot{}, err
	}
	currentIdentity, err := rootPathIdentity(binding.root, snapshotName)
	if err != nil {
		return StableFileSnapshot{}, err
	}
	finalIdentity, finalInfo, err := descriptorIdentity(destination)
	if err != nil {
		return StableFileSnapshot{}, err
	}
	finalPrivate, err := validPrivateSnapshotFile(
		destination,
		finalIdentity,
		finalInfo,
		result.Size,
	)
	if err != nil {
		return StableFileSnapshot{}, err
	}
	if !finalPrivate || !sameIdentity(verifiedIdentity, finalIdentity) ||
		!sameIdentity(verifiedIdentity, currentIdentity) {
		return StableFileSnapshot{}, snapshotPathChangedFailure()
	}
	if err := destination.Close(); err != nil {
		return StableFileSnapshot{}, err
	}
	return StableFileSnapshot{
		Path:               snapshotPath,
		StableFileDigest:   result.StableFileDigest,
		StableFileIdentity: finalIdentity.stableIdentity(),
	}, nil
}

func verifyStableSnapshotDestination(
	destination *os.File,
	expected StableFileDigest,
) (metadataIdentity, error) {
	before, beforeInfo, err := descriptorIdentity(destination)
	if err != nil {
		return metadataIdentity{}, err
	}
	beforePrivate, err := validPrivateSnapshotFile(
		destination,
		before,
		beforeInfo,
		expected.Size,
	)
	if err != nil {
		return metadataIdentity{}, err
	}
	if !beforePrivate {
		return metadataIdentity{}, snapshotPathChangedFailure()
	}

	hasher := sha256.New()
	buffer := make([]byte, readChunkBytes)
	defer scrubBytes(buffer, scrubReadBuffer, nil)
	consumed, err := io.CopyBuffer(
		hasher,
		io.NewSectionReader(destination, 0, expected.Size),
		buffer,
	)
	if err != nil {
		return metadataIdentity{}, err
	}
	after, afterInfo, err := descriptorIdentity(destination)
	if err != nil {
		return metadataIdentity{}, err
	}
	afterPrivate, err := validPrivateSnapshotFile(
		destination,
		after,
		afterInfo,
		expected.Size,
	)
	if err != nil {
		return metadataIdentity{}, err
	}
	if consumed != expected.Size ||
		hex.EncodeToString(hasher.Sum(nil)) != expected.SHA256 ||
		!sameIdentity(before, after) ||
		!afterPrivate {
		return metadataIdentity{}, snapshotPathChangedFailure()
	}
	return after, nil
}

func validPrivateSnapshotFile(
	file *os.File,
	identity metadataIdentity,
	info fs.FileInfo,
	expectedSize int64,
) (bool, error) {
	owner, ownerOK := platformOwnerID(info)
	effectiveUID, effectiveUIDOK := platformEffectiveUID()
	if !info.Mode().IsRegular() ||
		info.Mode() != 0o600 ||
		identity.size != expectedSize ||
		!ownerOK || !effectiveUIDOK || owner != effectiveUID {
		return false, nil
	}
	hasExtendedACL, err := platformDescriptorHasExtendedACL(file)
	if err != nil {
		return false, err
	}
	return !hasExtendedACL, nil
}

func snapshotPathChangedFailure() error {
	return ioFailure(
		"SNAPSHOT_PATH_CHANGED",
		"saved-plan snapshot path changed while it was created",
	)
}

type snapshotWriter interface {
	Write(value []byte) (int, error)
}

func writeAll(destination snapshotWriter, chunk []byte) error {
	for offset := 0; offset < len(chunk); {
		written, err := destination.Write(chunk[offset:])
		if written > 0 {
			offset += written
		}
		if err != nil {
			if errors.Is(err, io.ErrShortWrite) && written > 0 {
				continue
			}
			return err
		}
		if written <= 0 {
			return ioFailure(
				"SNAPSHOT_FAILED",
				"unable to write plan snapshot",
			)
		}
	}
	return nil
}

func scrubFailedSnapshot(destination *os.File, operationErr error) error {
	cleanupFailed := false
	if err := destination.Truncate(0); err != nil {
		cleanupFailed = true
	} else if err := destination.Sync(); err != nil {
		cleanupFailed = true
	}
	if err := destination.Close(); err != nil {
		cleanupFailed = true
	}

	if cleanupFailed {
		if failure, ok := preserveProcessFailure(operationErr); ok {
			details := append([]procerr.ErrorDetail(nil), failure.Details...)
			details = append(details, procerr.ErrorDetail{
				Path:    "$",
				Code:    "SNAPSHOT_CLEANUP_FAILED",
				Message: "partial saved-plan snapshot cleanup also failed",
			})
			return procerr.NewProcessFailure(procerr.NewProcessFailureOptions{
				Code:      failure.Code,
				Category:  failure.Category,
				Message:   failure.Message,
				Retryable: failure.Retryable,
				Details:   details,
			})
		}
		return ioFailure(
			"SNAPSHOT_AND_CLEANUP_FAILED",
			"unable to create or scrub the saved-plan snapshot",
		)
	}
	if failure, ok := preserveProcessFailure(operationErr); ok {
		return failure
	}
	return ioFailure("SNAPSHOT_FAILED", "unable to create plan snapshot")
}

func newSnapshotName() (string, error) {
	var random [16]byte
	// crypto/rand.Read fatals on Reader failure in Go 1.26. Reading Reader
	// directly keeps failure catchable; the public API intentionally normalizes
	// the raw entropy error to SNAPSHOT_FAILED.
	if _, err := io.ReadFull(rand.Reader, random[:]); err != nil {
		return "", err
	}
	return "plan-" + hex.EncodeToString(random[:]), nil
}
