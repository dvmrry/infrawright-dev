package artifacts

import (
	"errors"
	"math/big"
	"sync"
	"testing"

	"github.com/dvmrry/infrawright-dev/go/internal/procerr"
)

func smallReadLimits() BoundedReadLimits {
	return BoundedReadLimits{
		MaxFiles:            10,
		MaxDirectories:      10,
		MaxDirectoryEntries: 100,
		MaxDepth:            8,
		MaxTotalBytes:       big.NewInt(1024),
		MaxFileBytes:        big.NewInt(1024),
	}
}

func limitsEqual(left, right BoundedReadLimits) bool {
	if left.MaxFiles != right.MaxFiles || left.MaxDirectories != right.MaxDirectories ||
		left.MaxDirectoryEntries != right.MaxDirectoryEntries || left.MaxDepth != right.MaxDepth ||
		left.MaxTotalBytes == nil || right.MaxTotalBytes == nil ||
		left.MaxFileBytes == nil || right.MaxFileBytes == nil {
		return false
	}
	return left.MaxTotalBytes.Cmp(right.MaxTotalBytes) == 0 && left.MaxFileBytes.Cmp(right.MaxFileBytes) == 0
}

func requireFailure(t *testing.T, err error, code string, category procerr.Category) *procerr.ProcessFailure {
	t.Helper()
	if err == nil {
		t.Fatalf("error = nil, want *procerr.ProcessFailure code %q", code)
	}
	var failure *procerr.ProcessFailure
	if !errors.As(err, &failure) {
		t.Fatalf("error = %v (%T), want *procerr.ProcessFailure code %q", err, err, code)
	}
	if failure.Code != code {
		t.Errorf("ProcessFailure.Code = %q, want %q", failure.Code, code)
	}
	if failure.Category != category {
		t.Errorf("ProcessFailure.Category = %q, want %q", failure.Category, category)
	}
	return failure
}

func mustReadBudget(t *testing.T, limits BoundedReadLimits) *ReadBudget {
	t.Helper()
	budget, err := NewReadBudget(limits)
	if err != nil {
		t.Fatalf("NewReadBudget(%+v) error = %v, want nil", limits, err)
	}
	return budget
}

func TestDefaultBoundedReadLimits(t *testing.T) {
	want := BoundedReadLimits{
		MaxFiles:            50_000,
		MaxDirectories:      10_000,
		MaxDirectoryEntries: 100_000,
		MaxDepth:            128,
		MaxTotalBytes:       big.NewInt(2 * 1024 * 1024 * 1024),
		MaxFileBytes:        big.NewInt(16 * 1024 * 1024),
	}
	if got := DefaultBoundedReadLimits(); !limitsEqual(got, want) {
		t.Errorf("DefaultBoundedReadLimits() = %+v, want %+v", got, want)
	}
	first := DefaultBoundedReadLimits()
	first.MaxFiles = 1
	first.MaxTotalBytes.SetInt64(1)
	if got := DefaultBoundedReadLimits().MaxFiles; got != want.MaxFiles {
		t.Errorf("DefaultBoundedReadLimits().MaxFiles after caller mutation = %d, want %d", got, want.MaxFiles)
	}
	if got := DefaultBoundedReadLimits().MaxTotalBytes; got.Cmp(want.MaxTotalBytes) != 0 {
		t.Errorf("DefaultBoundedReadLimits().MaxTotalBytes after caller mutation = %v, want %v", got, want.MaxTotalBytes)
	}
	if got := NewDefaultReadBudget().Limits(); !limitsEqual(got, want) {
		t.Errorf("NewDefaultReadBudget().Limits() = %+v, want %+v", got, want)
	}
}

func TestNodeMaximumStringLengthCompatibilityConstant(t *testing.T) {
	// Node v24.15.0 buffer.constants.MAX_STRING_LENGTH is historical protocol
	// provenance, not a reason to restore a Node runtime to the test suite.
	if got, want := nodeMaximumStringLength, int64(536_870_888); got != want {
		t.Errorf("nodeMaximumStringLength = %d, want frozen Node v24.15.0 value %d", got, want)
	}
}

func TestNewReadBudgetRejectsInvalidLimits(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*BoundedReadLimits)
	}{
		{name: "zero_files", mutate: func(value *BoundedReadLimits) { value.MaxFiles = 0 }},
		{name: "negative_files", mutate: func(value *BoundedReadLimits) { value.MaxFiles = -1 }},
		{name: "zero_directories", mutate: func(value *BoundedReadLimits) { value.MaxDirectories = 0 }},
		{name: "zero_entries", mutate: func(value *BoundedReadLimits) { value.MaxDirectoryEntries = 0 }},
		{name: "negative_depth", mutate: func(value *BoundedReadLimits) { value.MaxDepth = -1 }},
		{name: "nil_total", mutate: func(value *BoundedReadLimits) { value.MaxTotalBytes = nil }},
		{name: "zero_total", mutate: func(value *BoundedReadLimits) { value.MaxTotalBytes = big.NewInt(0) }},
		{name: "negative_total", mutate: func(value *BoundedReadLimits) { value.MaxTotalBytes = big.NewInt(-1) }},
		{name: "nil_file", mutate: func(value *BoundedReadLimits) { value.MaxFileBytes = nil }},
		{name: "zero_file", mutate: func(value *BoundedReadLimits) { value.MaxFileBytes = big.NewInt(0) }},
		{name: "negative_file", mutate: func(value *BoundedReadLimits) { value.MaxFileBytes = big.NewInt(-1) }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			limits := smallReadLimits()
			test.mutate(&limits)
			budget, err := NewReadBudget(limits)
			if budget != nil {
				t.Errorf("NewReadBudget(%+v) budget = %+v, want nil", limits, budget)
			}
			failure := requireFailure(t, err, "INVALID_READ_LIMIT", procerr.CategoryDomain)
			if failure.Message != "bounded read limits must be positive" {
				t.Errorf("NewReadBudget(%+v) message = %q, want %q", limits, failure.Message, "bounded read limits must be positive")
			}
		})
	}
}

func TestReadBudgetSnapshotsLimitsAndUsesSourceFailurePrecedence(t *testing.T) {
	limits := smallReadLimits()
	limits.MaxFiles = 1
	limits.MaxTotalBytes.SetInt64(6)
	limits.MaxFileBytes.SetInt64(5)
	budget := mustReadBudget(t, limits)
	limits.MaxFiles = 100
	limits.MaxTotalBytes.SetInt64(100)
	limits.MaxFileBytes.SetInt64(100)

	requireFailure(t, budget.Reserve(big.NewInt(6)), "FILE_LIMIT_EXCEEDED", procerr.CategoryIO)
	if err := budget.Reserve(big.NewInt(5)); err != nil {
		t.Fatalf("ReadBudget.Reserve(5) error = %v, want nil", err)
	}
	requireFailure(t, budget.Reserve(big.NewInt(2)), "FILE_COUNT_EXCEEDED", procerr.CategoryIO)
	if got := budget.Files(); got != 1 {
		t.Errorf("ReadBudget.Files() = %d, want 1", got)
	}
	if got := budget.Bytes(); got.Cmp(big.NewInt(5)) != 0 {
		t.Errorf("ReadBudget.Bytes() = %v, want 5", got)
	}
	if got := budget.Limits().MaxFiles; got != 1 {
		t.Errorf("ReadBudget.Limits().MaxFiles = %d, want snapshotted value 1", got)
	}
}

func TestReadBudgetAggregateAndDirectoryLimits(t *testing.T) {
	totalLimits := smallReadLimits()
	totalLimits.MaxTotalBytes.SetInt64(9)
	total := mustReadBudget(t, totalLimits)
	if err := total.Reserve(big.NewInt(5)); err != nil {
		t.Fatalf("ReadBudget.Reserve(5) error = %v, want nil", err)
	}
	requireFailure(t, total.Reserve(big.NewInt(5)), "BYTE_BUDGET_EXCEEDED", procerr.CategoryIO)

	countLimits := smallReadLimits()
	countLimits.MaxDirectories = 1
	count := mustReadBudget(t, countLimits)
	if err := count.EnterDirectory(0); err != nil {
		t.Fatalf("ReadBudget.EnterDirectory(0) error = %v, want nil", err)
	}
	requireFailure(t, count.EnterDirectory(0), "DIRECTORY_COUNT_EXCEEDED", procerr.CategoryIO)

	entryLimits := smallReadLimits()
	entryLimits.MaxDirectoryEntries = 1
	entries := mustReadBudget(t, entryLimits)
	if err := entries.ReserveDirectoryEntry(); err != nil {
		t.Fatalf("ReadBudget.ReserveDirectoryEntry() error = %v, want nil", err)
	}
	requireFailure(t, entries.ReserveDirectoryEntry(), "DIRECTORY_ENTRY_LIMIT_EXCEEDED", procerr.CategoryIO)

	depthLimits := smallReadLimits()
	depthLimits.MaxDepth = 1
	depth := mustReadBudget(t, depthLimits)
	if err := depth.EnterDirectory(1); err != nil {
		t.Fatalf("ReadBudget.EnterDirectory(1) error = %v, want nil", err)
	}
	requireFailure(t, depth.EnterDirectory(2), "DIRECTORY_DEPTH_EXCEEDED", procerr.CategoryIO)

	if got := count.Directories(); got != 1 {
		t.Errorf("ReadBudget.Directories() = %d, want 1", got)
	}
	if got := entries.DirectoryEntries(); got != 1 {
		t.Errorf("ReadBudget.DirectoryEntries() = %d, want 1", got)
	}
}

func TestReadBudgetConcurrentReserveIsAtomic(t *testing.T) {
	// This proves data-race safety and atomic accounting only. ReadBudget's
	// documented single-threaded charging convention is what keeps failure
	// attribution deterministic for observable diagnostics.
	limits := smallReadLimits()
	limits.MaxFiles = 1
	limits.MaxTotalBytes.SetInt64(1000)
	budget := mustReadBudget(t, limits)
	copied := *budget

	const callers = 32
	start := make(chan struct{})
	// The result channel is sized to the fixed worker count so every worker can
	// terminate before this test begins collecting results.
	errorsByCaller := make(chan error, callers)
	var workers sync.WaitGroup
	for caller := range callers {
		target := budget
		if caller%2 != 0 {
			target = &copied
		}
		workers.Add(1)
		go func(target *ReadBudget) {
			defer workers.Done()
			<-start
			errorsByCaller <- target.Reserve(big.NewInt(1))
		}(target)
	}
	close(start)
	workers.Wait()
	close(errorsByCaller)

	successes := 0
	countFailures := 0
	for err := range errorsByCaller {
		if err == nil {
			successes++
			continue
		}
		failure := requireFailure(t, err, "FILE_COUNT_EXCEEDED", procerr.CategoryIO)
		if failure.Code == "FILE_COUNT_EXCEEDED" {
			countFailures++
		}
	}
	if successes != 1 || countFailures != callers-1 {
		t.Errorf("concurrent ReadBudget.Reserve results = successes %d, count failures %d; want 1 and %d", successes, countFailures, callers-1)
	}
}

func TestNilReadBudgetFailsClosed(t *testing.T) {
	var budget *ReadBudget
	requireFailure(t, budget.Reserve(big.NewInt(0)), "READ_FAILED", procerr.CategoryIO)
	requireFailure(t, budget.EnterDirectory(0), "READ_FAILED", procerr.CategoryIO)
	requireFailure(t, budget.ReserveDirectoryEntry(), "READ_FAILED", procerr.CategoryIO)
	limits := budget.Limits()
	if limits.MaxTotalBytes != nil || limits.MaxFileBytes != nil || budget.Files() != 0 || budget.Bytes().Sign() != 0 ||
		budget.Directories() != 0 || budget.DirectoryEntries() != 0 {
		t.Errorf("nil ReadBudget accessors returned non-zero state")
	}
}

func TestZeroValueReadBudgetFailsClosed(t *testing.T) {
	var budget ReadBudget
	requireFailure(t, budget.Reserve(big.NewInt(0)), "READ_FAILED", procerr.CategoryIO)
	requireFailure(t, budget.EnterDirectory(0), "READ_FAILED", procerr.CategoryIO)
	requireFailure(t, budget.ReserveDirectoryEntry(), "READ_FAILED", procerr.CategoryIO)
	limits := budget.Limits()
	if limits.MaxTotalBytes != nil || limits.MaxFileBytes != nil || budget.Files() != 0 || budget.Bytes().Sign() != 0 ||
		budget.Directories() != 0 || budget.DirectoryEntries() != 0 {
		t.Errorf("zero-value ReadBudget accessors returned non-zero state")
	}
}

func TestReadBudgetArbitraryPrecisionAndBoundaryCopies(t *testing.T) {
	chunk := new(big.Int).Lsh(big.NewInt(1), 62)
	limit := new(big.Int).Lsh(big.NewInt(1), 63)
	limits := smallReadLimits()
	limits.MaxFiles = 3
	limits.MaxFileBytes = new(big.Int).Set(chunk)
	limits.MaxTotalBytes = new(big.Int).Set(limit)
	budget := mustReadBudget(t, limits)

	// The source BigInt values are immutable. Mutating Go caller-owned inputs
	// after construction must not alter the budget's snapshot.
	limits.MaxFileBytes.SetInt64(1)
	limits.MaxTotalBytes.SetInt64(1)
	if err := budget.Reserve(chunk); err != nil {
		t.Fatalf("ReadBudget.Reserve(2^62) first error = %v, want nil", err)
	}
	if err := budget.Reserve(chunk); err != nil {
		t.Fatalf("ReadBudget.Reserve(2^62) second error = %v, want nil", err)
	}
	if got := budget.Bytes(); got.Cmp(limit) != 0 {
		t.Errorf("ReadBudget.Bytes() after 2^62 + 2^62 = %v, want %v", got, limit)
	}

	returnedBytes := budget.Bytes()
	returnedBytes.SetInt64(0)
	returnedLimits := budget.Limits()
	returnedLimits.MaxFileBytes.SetInt64(0)
	returnedLimits.MaxTotalBytes.SetInt64(0)
	if got := budget.Bytes(); got.Cmp(limit) != 0 {
		t.Errorf("ReadBudget.Bytes() after returned-copy mutation = %v, want %v", got, limit)
	}
	internalLimits := budget.Limits()
	if internalLimits.MaxFileBytes.Cmp(chunk) != 0 || internalLimits.MaxTotalBytes.Cmp(limit) != 0 {
		t.Errorf("ReadBudget.Limits() after returned-copy mutation = file %v total %v, want %v and %v", internalLimits.MaxFileBytes, internalLimits.MaxTotalBytes, chunk, limit)
	}

	// A value copy shares the state pointer, so it neither copies a live mutex
	// nor forks accounting.
	copied := *budget
	requireFailure(t, copied.Reserve(big.NewInt(1)), "BYTE_BUDGET_EXCEEDED", procerr.CategoryIO)
	if got := budget.Files(); got != 2 {
		t.Errorf("original ReadBudget.Files() after copied-budget failure = %d, want 2", got)
	}

	chunk.SetInt64(0)
	limit.SetInt64(0)
	if got := budget.Bytes(); got.BitLen() != 64 || got.Bit(63) != 1 {
		t.Errorf("ReadBudget.Bytes() after reserve-input mutation = %v, want 2^63", got)
	}
}
