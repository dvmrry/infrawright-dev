package artifacts

import (
	"math/big"
	"sync"
)

type readBudgetLimits struct {
	maxFiles            int
	maxDirectories      int
	maxDirectoryEntries int
	maxDepth            int
	maxTotalBytes       big.Int
	maxFileBytes        big.Int
}

type readBudgetState struct {
	limits readBudgetLimits

	mu                       sync.Mutex
	consumedFiles            int
	consumedDirectories      int
	consumedDirectoryEntries int
	consumedBytes            big.Int
}

// ReadBudget atomically accounts for related bounded filesystem observations.
// It is data-race safe for concurrent use, but concurrent charging is not
// deterministic: whichever goroutine reaches a limit first determines which
// input receives the failure. Callers whose diagnostics are observable must
// call Reserve, EnterDirectory, and ReserveDirectoryEntry serially in a
// deterministic caller or traversal order and must not share a charging budget
// across goroutines. Observable counter snapshots must be taken only after that
// serial charging phase completes. The stable read operations are synchronous
// and do not start goroutines. Copies of a ReadBudget share one state pointer
// rather than copying a mutex or counter.
type ReadBudget struct {
	state *readBudgetState
}

// NewReadBudget validates and snapshots limits before any filesystem access.
// It never retains caller-owned big.Int storage.
func NewReadBudget(limits BoundedReadLimits) (*ReadBudget, error) {
	if !validPositiveSafeInteger(limits.MaxFiles) ||
		!validPositiveSafeInteger(limits.MaxDirectories) ||
		!validPositiveSafeInteger(limits.MaxDirectoryEntries) ||
		limits.MaxDepth < 0 || int64(limits.MaxDepth) > maximumJavaScriptSafeInteger ||
		limits.MaxTotalBytes == nil || limits.MaxFileBytes == nil {
		return nil, domainFailure(
			"INVALID_READ_LIMIT",
			"bounded read limits must be positive",
		)
	}
	totalBytes := new(big.Int).Set(limits.MaxTotalBytes)
	fileBytes := new(big.Int).Set(limits.MaxFileBytes)
	if totalBytes.Sign() <= 0 || fileBytes.Sign() <= 0 {
		return nil, domainFailure(
			"INVALID_READ_LIMIT",
			"bounded read limits must be positive",
		)
	}
	limits.MaxTotalBytes = totalBytes
	limits.MaxFileBytes = fileBytes
	return newReadBudget(limits), nil
}

// NewDefaultReadBudget constructs a budget from DefaultBoundedReadLimits.
func NewDefaultReadBudget() *ReadBudget {
	return newReadBudget(DefaultBoundedReadLimits())
}

func (b *ReadBudget) initialized() bool {
	return b != nil && b.state != nil
}

func uninitializedReadBudgetFailure() error {
	return ioFailure("READ_FAILED", "unable to read input file")
}

func newReadBudget(limits BoundedReadLimits) *ReadBudget {
	state := &readBudgetState{
		limits: readBudgetLimits{
			maxFiles:            limits.MaxFiles,
			maxDirectories:      limits.MaxDirectories,
			maxDirectoryEntries: limits.MaxDirectoryEntries,
			maxDepth:            limits.MaxDepth,
		},
	}
	state.limits.maxTotalBytes.Set(limits.MaxTotalBytes)
	state.limits.maxFileBytes.Set(limits.MaxFileBytes)
	return &ReadBudget{state: state}
}

func validPositiveSafeInteger(value int) bool {
	return value > 0 && int64(value) <= maximumJavaScriptSafeInteger
}

// Limits returns a validated deep copy of the limits snapshot.
func (b *ReadBudget) Limits() BoundedReadLimits {
	if !b.initialized() {
		return BoundedReadLimits{}
	}
	b.state.mu.Lock()
	defer b.state.mu.Unlock()
	return BoundedReadLimits{
		MaxFiles:            b.state.limits.maxFiles,
		MaxDirectories:      b.state.limits.maxDirectories,
		MaxDirectoryEntries: b.state.limits.maxDirectoryEntries,
		MaxDepth:            b.state.limits.maxDepth,
		MaxTotalBytes:       new(big.Int).Set(&b.state.limits.maxTotalBytes),
		MaxFileBytes:        new(big.Int).Set(&b.state.limits.maxFileBytes),
	}
}

// Reserve charges one file and its pre-read size to the budget. Size is copied
// before it is inspected or retained.
func (b *ReadBudget) Reserve(size *big.Int) error {
	if !b.initialized() {
		return uninitializedReadBudgetFailure()
	}
	if size == nil {
		return ioFailure(
			"FILE_LIMIT_EXCEEDED",
			"input file exceeds the configured size limit",
		)
	}
	sizeSnapshot := new(big.Int).Set(size)

	b.state.mu.Lock()
	defer b.state.mu.Unlock()
	if sizeSnapshot.Sign() < 0 || sizeSnapshot.Cmp(&b.state.limits.maxFileBytes) > 0 {
		return ioFailure(
			"FILE_LIMIT_EXCEEDED",
			"input file exceeds the configured size limit",
		)
	}
	if b.state.consumedFiles >= b.state.limits.maxFiles {
		return ioFailure(
			"FILE_COUNT_EXCEEDED",
			"input exceeds the configured file-count limit",
		)
	}
	var nextBytes big.Int
	nextBytes.Add(&b.state.consumedBytes, sizeSnapshot)
	if nextBytes.Cmp(&b.state.limits.maxTotalBytes) > 0 {
		return ioFailure(
			"BYTE_BUDGET_EXCEEDED",
			"input exceeds the configured byte limit",
		)
	}
	b.state.consumedFiles++
	b.state.consumedBytes.Set(&nextBytes)
	return nil
}

// EnterDirectory charges one directory at depth to the budget.
func (b *ReadBudget) EnterDirectory(depth int) error {
	if !b.initialized() {
		return uninitializedReadBudgetFailure()
	}
	b.state.mu.Lock()
	defer b.state.mu.Unlock()

	if depth < 0 || int64(depth) > maximumJavaScriptSafeInteger || depth > b.state.limits.maxDepth {
		return ioFailure(
			"DIRECTORY_DEPTH_EXCEEDED",
			"input exceeds the directory-depth limit",
		)
	}
	if b.state.consumedDirectories >= b.state.limits.maxDirectories {
		return ioFailure(
			"DIRECTORY_COUNT_EXCEEDED",
			"input exceeds the directory-count limit",
		)
	}
	b.state.consumedDirectories++
	return nil
}

// ReserveDirectoryEntry charges one directory entry to the budget.
func (b *ReadBudget) ReserveDirectoryEntry() error {
	if !b.initialized() {
		return uninitializedReadBudgetFailure()
	}
	b.state.mu.Lock()
	defer b.state.mu.Unlock()

	if b.state.consumedDirectoryEntries >= b.state.limits.maxDirectoryEntries {
		return ioFailure(
			"DIRECTORY_ENTRY_LIMIT_EXCEEDED",
			"input exceeds the directory-entry limit",
		)
	}
	b.state.consumedDirectoryEntries++
	return nil
}

// Files returns the number of successfully reserved files.
func (b *ReadBudget) Files() int {
	if !b.initialized() {
		return 0
	}
	b.state.mu.Lock()
	defer b.state.mu.Unlock()
	return b.state.consumedFiles
}

// Bytes returns a detached arbitrary-precision copy of the bytes charged by
// successfully reserved files.
func (b *ReadBudget) Bytes() *big.Int {
	if !b.initialized() {
		return new(big.Int)
	}
	b.state.mu.Lock()
	defer b.state.mu.Unlock()
	return new(big.Int).Set(&b.state.consumedBytes)
}

// Directories returns the number of successfully entered directories.
func (b *ReadBudget) Directories() int {
	if !b.initialized() {
		return 0
	}
	b.state.mu.Lock()
	defer b.state.mu.Unlock()
	return b.state.consumedDirectories
}

// DirectoryEntries returns the number of successfully reserved entries.
func (b *ReadBudget) DirectoryEntries() int {
	if !b.initialized() {
		return 0
	}
	b.state.mu.Lock()
	defer b.state.mu.Unlock()
	return b.state.consumedDirectoryEntries
}
