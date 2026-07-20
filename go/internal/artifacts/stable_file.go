package artifacts

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"math/big"
	"os"
	"runtime"
	"unicode/utf8"
)

type scrubKind uint8

const (
	scrubReadBuffer scrubKind = iota + 1
	scrubCollectedChunk
	scrubReturnedBytes
)

type scrubObserver func(kind scrubKind, value []byte)

type consumeStableFileOptions struct {
	filePath      string
	budget        *ReadBudget
	readOptions   StableReadOptions
	collect       bool
	onChunk       func(chunk []byte) error
	scrubObserver scrubObserver
}

type consumedFile struct {
	StableFileDigest
	chunks   [][]byte
	identity StableFileIdentity
}

// SHA256StableFile hashes one bounded file while binding its path and opened
// descriptor to the same stable identity.
func SHA256StableFile(
	filePath string,
	budget *ReadBudget,
	options StableReadOptions,
) (StableFileDigest, error) {
	result, err := consumeStableFile(consumeStableFileOptions{
		filePath:    filePath,
		budget:      budget,
		readOptions: options,
	})
	if err != nil {
		return StableFileDigest{}, err
	}
	return result.StableFileDigest, nil
}

// ReadBoundedFileBytes returns a stable, caller-owned byte snapshot.
func ReadBoundedFileBytes(
	filePath string,
	budget *ReadBudget,
	options StableReadOptions,
) (BoundedFileBytes, error) {
	return readBoundedFileBytes(filePath, budget, options, nil)
}

func readBoundedFileBytes(
	filePath string,
	budget *ReadBudget,
	options StableReadOptions,
	observer scrubObserver,
) (BoundedFileBytes, error) {
	result, err := consumeStableFile(consumeStableFileOptions{
		filePath:      filePath,
		budget:        budget,
		readOptions:   options,
		collect:       true,
		scrubObserver: observer,
	})
	if err != nil {
		return BoundedFileBytes{}, err
	}
	if result.Size > nodeMaximumStringLength {
		scrubChunks(result.chunks, observer)
		return BoundedFileBytes{}, ioFailure(
			"FILE_LIMIT_EXCEEDED",
			"input file exceeds the decoder size limit",
		)
	}

	bytes := make([]byte, int(result.Size))
	offset := 0
	for _, chunk := range result.chunks {
		offset += copy(bytes[offset:], chunk)
	}
	scrubChunks(result.chunks, observer)
	return BoundedFileBytes{
		Bytes:    bytes,
		Digest:   result.StableFileDigest,
		Identity: result.identity,
	}, nil
}

// ReadBoundedUTF8File decodes one stable byte snapshot as strict UTF-8. A
// leading UTF-8 BOM is preserved, matching TextDecoder with ignoreBOM=true in
// the Node source.
func ReadBoundedUTF8File(
	filePath string,
	budget *ReadBudget,
	options StableReadOptions,
) (BoundedUTF8File, error) {
	return readBoundedUTF8File(filePath, budget, options, nil)
}

func readBoundedUTF8File(
	filePath string,
	budget *ReadBudget,
	options StableReadOptions,
	observer scrubObserver,
) (BoundedUTF8File, error) {
	content, err := readBoundedFileBytes(filePath, budget, options, observer)
	if err != nil {
		return BoundedUTF8File{}, err
	}
	defer scrubBytes(content.Bytes, scrubReturnedBytes, observer)

	if !utf8.Valid(content.Bytes) {
		return BoundedUTF8File{}, domainFailure(
			"INVALID_UTF8",
			"input file is not valid UTF-8",
		)
	}
	return BoundedUTF8File{
		Text:     string(content.Bytes),
		Digest:   content.Digest,
		Identity: content.Identity,
	}, nil
}

func consumeStableFile(options consumeStableFileOptions) (consumedFile, error) {
	if !options.budget.initialized() {
		return consumedFile{}, uninitializedReadBudgetFailure()
	}
	if !boundedFilePlatformSupported {
		return consumedFile{}, unsupportedPlatformFailure()
	}

	handle, err := openStableFile(options.filePath, options.readOptions.FollowSymlinks)
	if err != nil {
		return consumedFile{}, err
	}
	result, err := consumeOpenedStableFile(handle, options)
	if err == nil {
		return result, nil
	}
	if _, ok := preserveProcessFailure(err); ok {
		return consumedFile{}, err
	}
	return consumedFile{}, ioFailure("READ_FAILED", "unable to read input file")
}

func consumeOpenedStableFile(
	handle *os.File,
	options consumeStableFileOptions,
) (result consumedFile, err error) {
	var (
		readBuffer        []byte
		ownedChunks       [][]byte
		chunksTransferred bool
	)
	defer func() {
		scrubBytes(readBuffer, scrubReadBuffer, options.scrubObserver)
		if !chunksTransferred {
			scrubChunks(ownedChunks, options.scrubObserver)
		}
		// Node deliberately ignores a read descriptor's close failure after the
		// operation has already established its terminal result.
		_ = handle.Close()
	}()

	before, beforeInfo, err := descriptorIdentity(handle)
	if err != nil {
		return consumedFile{}, err
	}
	if !beforeInfo.Mode().IsRegular() {
		return consumedFile{}, ioFailure("NOT_REGULAR_FILE", "input must be a regular file")
	}
	if options.collect && before.size > nodeMaximumStringLength {
		return consumedFile{}, ioFailure(
			"FILE_LIMIT_EXCEEDED",
			"input file exceeds the decoder size limit",
		)
	}
	if err := options.budget.Reserve(big.NewInt(before.size)); err != nil {
		return consumedFile{}, err
	}
	if err := invokeStableReadHook(options.readOptions.Hooks.AfterOpen); err != nil {
		return consumedFile{}, err
	}

	hasher := sha256.New()
	readBuffer = make([]byte, stableReadBufferSize(before.size))
	var consumed int64
	for {
		bytesRead, readErr := handle.Read(readBuffer)
		if bytesRead > 0 {
			consumed += int64(bytesRead)
			if consumed > before.size {
				return consumedFile{}, ioFailure(
					"FILE_CHANGED",
					"input file changed while it was read",
				)
			}
			chunk := readBuffer[:bytesRead]
			// hash.Hash.Write is documented to return a nil error.
			_, _ = hasher.Write(chunk)
			if options.collect {
				ownedChunks = append(ownedChunks, append([]byte(nil), chunk...))
			}
			if options.onChunk != nil {
				if err := options.onChunk(chunk); err != nil {
					return consumedFile{}, err
				}
			}
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				break
			}
			return consumedFile{}, readErr
		}
		if bytesRead == 0 {
			break
		}
	}

	if err := invokeStableReadHook(options.readOptions.Hooks.BeforeFinalStat); err != nil {
		return consumedFile{}, err
	}
	after, _, err := descriptorIdentity(handle)
	if err != nil {
		return consumedFile{}, err
	}
	current, err := pathIdentity(options.filePath, options.readOptions.FollowSymlinks)
	if err != nil {
		return consumedFile{}, err
	}
	if consumed != before.size || !sameIdentity(before, after) || !sameIdentity(before, current) {
		return consumedFile{}, ioFailure(
			"FILE_CHANGED",
			"input file changed while it was read",
		)
	}

	result.StableFileDigest = StableFileDigest{
		SHA256: hex.EncodeToString(hasher.Sum(nil)),
		Size:   consumed,
	}
	result.chunks = ownedChunks
	result.identity = before.stableIdentity()
	chunksTransferred = true
	return result, nil
}

// stableReadBufferSize sizes the read buffer to the file rather than always
// allocating the full readChunkBytes ceiling. It never returns less than one
// byte: the trailing read into a non-empty buffer is what lets the loop observe
// a file that grew past before.size (an empty file with a zero-length buffer
// could never read those bytes), so the one-byte floor preserves the exact
// grow-during-read / EOF-detection behavior of the fixed-size buffer.
func stableReadBufferSize(size int64) int64 {
	if size < 1 {
		return 1
	}
	if size > readChunkBytes {
		return readChunkBytes
	}
	return size
}

func invokeStableReadHook(hook func() error) (err error) {
	if hook == nil {
		return nil
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			if recoveredErr, ok := recovered.(error); ok {
				err = recoveredErr
				return
			}
			err = errors.New("stable read hook panicked")
		}
	}()
	return hook()
}

func scrubChunks(chunks [][]byte, observer scrubObserver) {
	for _, chunk := range chunks {
		scrubBytes(chunk, scrubCollectedChunk, observer)
	}
}

func scrubBytes(value []byte, kind scrubKind, observer scrubObserver) {
	if value == nil {
		return
	}
	clear(value)
	if observer != nil {
		observer(kind, value)
	}
	runtime.KeepAlive(value)
}
