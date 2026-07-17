//go:build (darwin || linux) && !ios && !android && (amd64 || arm64)

package artifacts

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"math/big"
	"os"
	"path/filepath"
	"regexp"
	"syscall"
	"testing"

	"github.com/dvmrry/infrawright-dev/go/internal/procerr"
)

func privateTemporaryDirectory(t *testing.T) string {
	t.Helper()
	directory := t.TempDir()
	if err := os.Chmod(directory, 0o700); err != nil {
		t.Fatalf("os.Chmod(%q, 0700) error = %v, want nil", directory, err)
	}
	return directory
}

func writePrivateFile(t *testing.T, filePath string, content []byte) {
	t.Helper()
	if err := os.WriteFile(filePath, content, 0o600); err != nil {
		t.Fatalf("os.WriteFile(%q, %d bytes) error = %v, want nil", filePath, len(content), err)
	}
}

func TestZeroValueReadBudgetFailsBeforeSourceOpen(t *testing.T) {
	directory := privateTemporaryDirectory(t)
	target := filepath.Join(directory, "target")
	link := filepath.Join(directory, "link")
	writePrivateFile(t, target, []byte("must not be opened"))
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("os.Symlink(%q, %q) error = %v, want nil", target, link, err)
	}

	var budget ReadBudget
	_, err := SHA256StableFile(link, &budget, StableReadOptions{})
	// Opening this path would return SYMLINK_NOT_ALLOWED. READ_FAILED therefore
	// proves the uninitialized budget won precedence before source-path access.
	requireFailure(t, err, "READ_FAILED", procerr.CategoryIO)
}

func TestStableHashingAndBoundedUTF8ReadsShareExactBudget(t *testing.T) {
	directory := privateTemporaryDirectory(t)
	first := filepath.Join(directory, "first")
	second := filepath.Join(directory, "second")
	writePrivateFile(t, first, []byte("alpha"))
	writePrivateFile(t, second, []byte("beta"))
	budget := mustReadBudget(t, smallReadLimits())

	digest, err := SHA256StableFile(first, budget, StableReadOptions{})
	if err != nil {
		t.Fatalf("SHA256StableFile(%q) error = %v, want nil", first, err)
	}
	wantHash := sha256.Sum256([]byte("alpha"))
	if digest.SHA256 != hex.EncodeToString(wantHash[:]) || digest.Size != 5 {
		t.Errorf("SHA256StableFile(%q) = %+v, want sha256 %x and size 5", first, digest, wantHash)
	}
	decoded, err := ReadBoundedUTF8File(second, budget, StableReadOptions{})
	if err != nil {
		t.Fatalf("ReadBoundedUTF8File(%q) error = %v, want nil", second, err)
	}
	if decoded.Text != "beta" {
		t.Errorf("ReadBoundedUTF8File(%q).Text = %q, want %q", second, decoded.Text, "beta")
	}
	if budget.Files() != 2 || budget.Bytes().Cmp(big.NewInt(9)) != 0 {
		t.Errorf("shared ReadBudget usage = files %d, bytes %v; want 2 and 9", budget.Files(), budget.Bytes())
	}
}

func TestBoundedUTF8PreservesBOMAndReturnsStableIdentity(t *testing.T) {
	directory := privateTemporaryDirectory(t)
	source := filepath.Join(directory, "bom")
	writePrivateFile(t, source, []byte{0xef, 0xbb, 0xbf, 'x'})

	result, err := ReadBoundedUTF8File(source, mustReadBudget(t, smallReadLimits()), StableReadOptions{})
	if err != nil {
		t.Fatalf("ReadBoundedUTF8File(%q) error = %v, want nil", source, err)
	}
	if result.Text != "\ufeffx" {
		t.Errorf("ReadBoundedUTF8File(%q).Text = %q, want %q", source, result.Text, "\ufeffx")
	}
	info, err := os.Lstat(source)
	if err != nil {
		t.Fatalf("os.Lstat(%q) error = %v, want nil", source, err)
	}
	wantIdentity, ok := platformMetadataIdentity(info)
	if !ok {
		t.Fatalf("platformMetadataIdentity(os.Lstat(%q)) ok = false, want true", source)
	}
	if result.Identity != wantIdentity.stableIdentity() {
		t.Errorf("ReadBoundedUTF8File(%q).Identity = %+v, want %+v", source, result.Identity, wantIdentity.stableIdentity())
	}
}

func TestStableFileLimitsFailBeforeUnboundedRead(t *testing.T) {
	directory := privateTemporaryDirectory(t)
	first := filepath.Join(directory, "first")
	second := filepath.Join(directory, "second")
	writePrivateFile(t, first, []byte("12345"))
	writePrivateFile(t, second, []byte("67890"))

	fileLimits := smallReadLimits()
	fileLimits.MaxFileBytes.SetInt64(4)
	_, err := SHA256StableFile(first, mustReadBudget(t, fileLimits), StableReadOptions{})
	requireFailure(t, err, "FILE_LIMIT_EXCEEDED", procerr.CategoryIO)

	totalLimits := smallReadLimits()
	totalLimits.MaxTotalBytes.SetInt64(9)
	totalBudget := mustReadBudget(t, totalLimits)
	if _, err := SHA256StableFile(first, totalBudget, StableReadOptions{}); err != nil {
		t.Fatalf("SHA256StableFile(%q) first read error = %v, want nil", first, err)
	}
	_, err = SHA256StableFile(second, totalBudget, StableReadOptions{})
	requireFailure(t, err, "BYTE_BUDGET_EXCEEDED", procerr.CategoryIO)

	countLimits := smallReadLimits()
	countLimits.MaxFiles = 1
	countBudget := mustReadBudget(t, countLimits)
	if _, err := SHA256StableFile(first, countBudget, StableReadOptions{}); err != nil {
		t.Fatalf("SHA256StableFile(%q) first count read error = %v, want nil", first, err)
	}
	_, err = SHA256StableFile(second, countBudget, StableReadOptions{})
	requireFailure(t, err, "FILE_COUNT_EXCEEDED", procerr.CategoryIO)
}

func TestBoundedCollectionRejectsOversizedSparseFileBeforeAllocation(t *testing.T) {
	directory := privateTemporaryDirectory(t)
	source := filepath.Join(directory, "oversized-sparse")
	writePrivateFile(t, source, nil)
	size := nodeMaximumStringLength + 1
	if err := os.Truncate(source, size); err != nil {
		t.Fatalf("os.Truncate(%q, %d) error = %v, want nil", source, size, err)
	}
	limits := smallReadLimits()
	limits.MaxFiles = 1
	limits.MaxTotalBytes.SetInt64(size)
	limits.MaxFileBytes.SetInt64(size)
	budget := mustReadBudget(t, limits)
	if err := budget.Reserve(big.NewInt(0)); err != nil {
		t.Fatalf("ReadBudget.Reserve(0) precharge error = %v, want nil", err)
	}
	afterOpenCalled := false
	_, err := ReadBoundedFileBytes(source, budget, StableReadOptions{
		Hooks: StableReadHooks{
			AfterOpen: func() error {
				afterOpenCalled = true
				return nil
			},
		},
	})
	requireFailure(t, err, "FILE_LIMIT_EXCEEDED", procerr.CategoryIO)
	if afterOpenCalled {
		t.Errorf("oversized sparse file reached AfterOpen hook")
	}
	if budget.Files() != 1 || budget.Bytes().Sign() != 0 {
		t.Errorf("oversized sparse file budget = files %d, bytes %v; want unchanged precharge 1 and 0", budget.Files(), budget.Bytes())
	}
}

func TestBoundedCollectionAllowsExactNodeMaximumBeforeRead(t *testing.T) {
	directory := privateTemporaryDirectory(t)
	source := filepath.Join(directory, "maximum-sparse")
	writePrivateFile(t, source, nil)
	if err := os.Truncate(source, nodeMaximumStringLength); err != nil {
		t.Fatalf("os.Truncate(%q, %d) error = %v, want nil", source, nodeMaximumStringLength, err)
	}
	limits := smallReadLimits()
	limits.MaxTotalBytes.SetInt64(nodeMaximumStringLength)
	limits.MaxFileBytes.SetInt64(nodeMaximumStringLength)
	budget := mustReadBudget(t, limits)
	afterOpenCalled := false
	_, err := ReadBoundedFileBytes(source, budget, StableReadOptions{
		Hooks: StableReadHooks{
			AfterOpen: func() error {
				afterOpenCalled = true
				return errors.New("stop before reading maximum sparse file")
			},
		},
	})
	requireFailure(t, err, "READ_FAILED", procerr.CategoryIO)
	if !afterOpenCalled {
		t.Errorf("exact MAX_STRING_LENGTH sparse file did not reach AfterOpen hook")
	}
	if budget.Files() != 1 || budget.Bytes().Cmp(big.NewInt(nodeMaximumStringLength)) != 0 {
		t.Errorf("exact MAX_STRING_LENGTH budget = files %d, bytes %v; want 1 and %d", budget.Files(), budget.Bytes(), nodeMaximumStringLength)
	}
}

func TestDirectoriesSymlinksAndFIFOsCannotMasqueradeAsFiles(t *testing.T) {
	directory := privateTemporaryDirectory(t)
	source := filepath.Join(directory, "source")
	alias := filepath.Join(directory, "alias")
	writePrivateFile(t, source, []byte("plan"))
	if err := os.Symlink(source, alias); err != nil {
		t.Fatalf("os.Symlink(%q, %q) error = %v, want nil", source, alias, err)
	}

	_, err := SHA256StableFile(directory, mustReadBudget(t, smallReadLimits()), StableReadOptions{})
	requireFailure(t, err, "NOT_REGULAR_FILE", procerr.CategoryIO)
	_, err = SHA256StableFile(alias, mustReadBudget(t, smallReadLimits()), StableReadOptions{})
	requireFailure(t, err, "SYMLINK_NOT_ALLOWED", procerr.CategoryIO)
	followed, err := SHA256StableFile(alias, mustReadBudget(t, smallReadLimits()), StableReadOptions{FollowSymlinks: true})
	if err != nil {
		t.Fatalf("SHA256StableFile(%q, FollowSymlinks) error = %v, want nil", alias, err)
	}
	if followed.Size != 4 {
		t.Errorf("SHA256StableFile(%q, FollowSymlinks).Size = %d, want 4", alias, followed.Size)
	}

	fifo := filepath.Join(directory, "fifo")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatalf("syscall.Mkfifo(%q) error = %v, want nil", fifo, err)
	}
	_, err = SHA256StableFile(fifo, mustReadBudget(t, smallReadLimits()), StableReadOptions{})
	requireFailure(t, err, "NOT_REGULAR_FILE", procerr.CategoryIO)
}

func TestSameSizeMutationIsDetectedThroughOpenedDescriptor(t *testing.T) {
	directory := privateTemporaryDirectory(t)
	source := filepath.Join(directory, "source")
	writePrivateFile(t, source, []byte("before"))
	_, err := SHA256StableFile(source, mustReadBudget(t, smallReadLimits()), StableReadOptions{
		Hooks: StableReadHooks{
			AfterOpen: func() error {
				return os.WriteFile(source, []byte("after!"), 0o600)
			},
		},
	})
	requireFailure(t, err, "FILE_CHANGED", procerr.CategoryIO)
}

func TestPathReplacementIsDetectedWithStableOpenedBytes(t *testing.T) {
	directory := privateTemporaryDirectory(t)
	source := filepath.Join(directory, "source")
	replacement := filepath.Join(directory, "replacement")
	writePrivateFile(t, source, []byte("stable"))
	writePrivateFile(t, replacement, []byte("stable"))
	_, err := SHA256StableFile(source, mustReadBudget(t, smallReadLimits()), StableReadOptions{
		Hooks: StableReadHooks{
			BeforeFinalStat: func() error {
				return os.Rename(replacement, source)
			},
		},
	})
	requireFailure(t, err, "FILE_CHANGED", procerr.CategoryIO)
}

func TestCollectedChunksAreScrubbedWhenFinalHookFails(t *testing.T) {
	directory := privateTemporaryDirectory(t)
	source := filepath.Join(directory, "source")
	content := bytes.Repeat([]byte{0x53}, 4097)
	writePrivateFile(t, source, content)
	limits := smallReadLimits()
	limits.MaxFileBytes.SetInt64(8192)
	limits.MaxTotalBytes.SetInt64(8192)
	collectedScrubs := 0
	readBufferScrubs := 0
	_, err := readBoundedFileBytes(source, mustReadBudget(t, limits), StableReadOptions{
		Hooks: StableReadHooks{
			BeforeFinalStat: func() error {
				return errors.New("forced final-stat failure")
			},
		},
	}, func(kind scrubKind, value []byte) {
		if !bytes.Equal(value, make([]byte, len(value))) {
			t.Errorf("scrub observer kind %d saw non-zero bytes", kind)
		}
		switch kind {
		case scrubCollectedChunk:
			if len(value) == len(content) {
				collectedScrubs++
			}
		case scrubReadBuffer:
			if len(value) == readChunkBytes {
				readBufferScrubs++
			}
		}
	})
	requireFailure(t, err, "READ_FAILED", procerr.CategoryIO)
	if collectedScrubs != 1 || readBufferScrubs != 1 {
		t.Errorf("failed read scrub counts = collected %d, read buffer %d; want 1 and 1", collectedScrubs, readBufferScrubs)
	}
}

func TestUTF8HelperScrubsInternallyOwnedBytesOnDecodeFailure(t *testing.T) {
	directory := privateTemporaryDirectory(t)
	source := filepath.Join(directory, "invalid-utf8")
	content := []byte{0xff, 0x53, 0x45, 0x43, 0x52, 0x45, 0x54, 0x00, 0x01}
	writePrivateFile(t, source, content)
	collectedScrubs := 0
	returnedScrubs := 0
	_, err := readBoundedUTF8File(
		source,
		mustReadBudget(t, smallReadLimits()),
		StableReadOptions{},
		func(kind scrubKind, value []byte) {
			if len(value) != len(content) {
				return
			}
			if !bytes.Equal(value, make([]byte, len(value))) {
				t.Errorf("scrub observer kind %d saw non-zero bytes", kind)
			}
			switch kind {
			case scrubCollectedChunk:
				collectedScrubs++
			case scrubReturnedBytes:
				returnedScrubs++
			}
		},
	)
	failure := requireFailure(t, err, "INVALID_UTF8", procerr.CategoryDomain)
	if regexp.MustCompile(`SECRET`).MatchString(failure.Message) {
		t.Errorf("INVALID_UTF8 message = %q, must not contain file content", failure.Message)
	}
	if collectedScrubs != 1 || returnedScrubs != 1 {
		t.Errorf("invalid UTF-8 scrub counts = collected %d, returned %d; want 1 and 1", collectedScrubs, returnedScrubs)
	}
}

func TestBoundedFileBytesAreCallerOwned(t *testing.T) {
	directory := privateTemporaryDirectory(t)
	source := filepath.Join(directory, "source")
	writePrivateFile(t, source, []byte("original"))
	result, err := ReadBoundedFileBytes(source, mustReadBudget(t, smallReadLimits()), StableReadOptions{})
	if err != nil {
		t.Fatalf("ReadBoundedFileBytes(%q) error = %v, want nil", source, err)
	}
	writePrivateFile(t, source, []byte("replaced"))
	if string(result.Bytes) != "original" {
		t.Errorf("ReadBoundedFileBytes(%q).Bytes after source mutation = %q, want %q", source, result.Bytes, "original")
	}
	clear(result.Bytes)
}

func TestStableReadHookPanicIsMappedAndBuffersAreScrubbed(t *testing.T) {
	directory := privateTemporaryDirectory(t)
	source := filepath.Join(directory, "source")
	writePrivateFile(t, source, []byte("secret"))
	readBufferScrubs := 0
	_, err := readBoundedFileBytes(
		source,
		mustReadBudget(t, smallReadLimits()),
		StableReadOptions{
			Hooks: StableReadHooks{
				BeforeFinalStat: func() error {
					panic("forced hook panic")
				},
			},
		},
		func(kind scrubKind, value []byte) {
			if kind == scrubReadBuffer && len(value) == readChunkBytes {
				readBufferScrubs++
			}
		},
	)
	requireFailure(t, err, "READ_FAILED", procerr.CategoryIO)
	if readBufferScrubs != 1 {
		t.Errorf("panic read-buffer scrub count = %d, want 1", readBufferScrubs)
	}
}
