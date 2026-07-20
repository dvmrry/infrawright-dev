package artifactpublish

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPublishReplacesWholeSetAndRemovesStaleOptionalArtifact(t *testing.T) {
	parent := t.TempDir()
	destination := filepath.Join(parent, "bundle")
	writeDirectory(t, destination, map[string]string{
		"core.json":     "old core\n",
		"optional.json": "stale optional\n",
		"obsolete.json": "obsolete\n",
	})

	input := []byte("new core\n")
	err := Publish(context.Background(), Options{
		Destination: destination,
		Vocabulary:  Vocabulary{Required: []string{"core.json"}, Optional: []string{"optional.json"}},
		Artifacts:   []Artifact{{Name: "core.json", Bytes: input}},
	})
	if err != nil {
		t.Fatalf("Publish() error = %v, want nil", err)
	}
	input[0] = 'X'
	requireDirectory(t, destination, map[string]string{"core.json": "new core\n"})
	for _, sibling := range []string{destination + ".lock", destination + ".backup"} {
		if _, statErr := os.Lstat(sibling); !errors.Is(statErr, fs.ErrNotExist) {
			t.Errorf("Publish() sibling %q stat error = %v, want not exist", sibling, statErr)
		}
	}
}

func TestPublishRejectsPreflightBeforeMutation(t *testing.T) {
	parent := t.TempDir()
	destination := filepath.Join(parent, "bundle")
	writeDirectory(t, destination, map[string]string{"core.json": "old\n"})
	if err := os.Mkdir(destination+".backup", 0o700); err != nil {
		t.Fatalf("os.Mkdir(backup) error = %v", err)
	}

	err := Publish(context.Background(), Options{
		Destination: destination,
		Vocabulary:  Vocabulary{Required: []string{"core.json"}},
		Artifacts:   []Artifact{{Name: "core.json", Bytes: []byte("new\n")}},
	})
	requireFailureKind(t, err, FailurePreflight)
	requireDirectory(t, destination, map[string]string{"core.json": "old\n"})
	requireDirectory(t, destination+".backup", map[string]string{})
}

func TestPublishRejectsInvalidVocabularyAndArtifactNames(t *testing.T) {
	parent := t.TempDir()
	tests := []struct {
		name       string
		vocabulary Vocabulary
		artifacts  []Artifact
	}{
		{
			name:       "missing required",
			vocabulary: Vocabulary{Required: []string{"core.json"}},
		},
		{
			name:       "duplicate vocabulary",
			vocabulary: Vocabulary{Required: []string{"core.json"}, Optional: []string{"core.json"}},
			artifacts:  []Artifact{{Name: "core.json", Bytes: []byte("x")}},
		},
		{
			name:       "path traversal",
			vocabulary: Vocabulary{Required: []string{"core.json"}},
			artifacts:  []Artifact{{Name: "../core.json", Bytes: []byte("x")}},
		},
		{
			name:       "nil bytes",
			vocabulary: Vocabulary{Required: []string{"core.json"}},
			artifacts:  []Artifact{{Name: "core.json", Bytes: nil}},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			destination := filepath.Join(parent, test.name)
			err := Publish(context.Background(), Options{Destination: destination, Vocabulary: test.vocabulary, Artifacts: test.artifacts})
			requireFailureKind(t, err, FailurePreflight)
			if _, statErr := os.Lstat(destination); !errors.Is(statErr, fs.ErrNotExist) {
				t.Errorf("Publish(%q) destination stat error = %v, want not exist", test.name, statErr)
			}
		})
	}
}

func TestPublishDoesNotStealCooperativeLock(t *testing.T) {
	parent := t.TempDir()
	destination := filepath.Join(parent, "bundle")
	writeDirectory(t, destination, map[string]string{"core.json": "old\n"})
	if err := os.Mkdir(destination+".lock", 0o700); err != nil {
		t.Fatalf("os.Mkdir(lock) error = %v", err)
	}
	err := Publish(context.Background(), publicationOptions(destination))
	requireFailureKind(t, err, FailureLockConflict)
	requireDirectory(t, destination, map[string]string{"core.json": "old\n"})
	if info, statErr := os.Lstat(destination + ".lock"); statErr != nil || !info.IsDir() {
		t.Errorf("Publish() lock after conflict = %v, %v, want existing directory", info, statErr)
	}
}

func TestPublishStageCreationWriteAndValidationFailuresLeaveOldDestination(t *testing.T) {
	for _, test := range []struct {
		name string
		ops  func(operations, string) operations
	}{
		{
			name: "stage creation",
			ops: func(ops operations, _ string) operations {
				ops.mkdirTemp = func(string, string) (string, error) { return "", errors.New("forced stage creation failure") }
				return ops
			},
		},
		{
			name: "stage write",
			ops: func(ops operations, _ string) operations {
				ops.writeFile = func(string, []byte, fs.FileMode) error { return errors.New("forced stage write failure") }
				return ops
			},
		},
		{
			name: "stage validation",
			ops: func(ops operations, _ string) operations {
				ops.readFile = func(string) ([]byte, error) { return nil, errors.New("forced staged readback failure") }
				return ops
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			parent := t.TempDir()
			destination := filepath.Join(parent, "bundle")
			writeDirectory(t, destination, map[string]string{"core.json": "old\n"})
			err := publish(context.Background(), publicationOptions(destination), test.ops(productionOperations(), destination))
			requireFailureKind(t, err, FailureStage)
			requireDirectory(t, destination, map[string]string{"core.json": "old\n"})
			requireNoTransactionSiblings(t, parent, "bundle")
		})
	}
}

func TestPublishRejectsNonPrivateStageBeforeWritingArtifacts(t *testing.T) {
	parent := t.TempDir()
	destination := filepath.Join(parent, "bundle")
	writeDirectory(t, destination, map[string]string{"core.json": "old\n"})
	stage := filepath.Join(parent, ".bundle.stage-insecure")
	if err := os.Mkdir(stage, 0o755); err != nil {
		t.Fatalf("os.Mkdir(%q) error = %v", stage, err)
	}
	ops := productionOperations()
	ops.mkdirTemp = func(string, string) (string, error) { return stage, nil }
	writeCalls := 0
	ops.writeFile = func(string, []byte, fs.FileMode) error {
		writeCalls++
		return nil
	}
	err := publish(context.Background(), publicationOptions(destination), ops)
	requireFailureKind(t, err, FailureStage)
	if writeCalls != 0 {
		t.Errorf("Publish() writes to non-private stage = %d, want zero", writeCalls)
	}
	requireDirectory(t, destination, map[string]string{"core.json": "old\n"})
	if _, statErr := os.Lstat(stage); !errors.Is(statErr, fs.ErrNotExist) {
		t.Errorf("Publish() insecure stage stat error = %v, want removed", statErr)
	}
}

func TestPublishBackupFailureLeavesOldDestination(t *testing.T) {
	parent := t.TempDir()
	destination := filepath.Join(parent, "bundle")
	writeDirectory(t, destination, map[string]string{"core.json": "old\n"})
	ops := productionOperations()
	productionRename := ops.rename
	ops.rename = func(from, to string) error {
		if from == destination && to == destination+".backup" {
			return errors.New("forced backup rename failure")
		}
		return productionRename(from, to)
	}
	err := publish(context.Background(), publicationOptions(destination), ops)
	requireFailureKind(t, err, FailureBackup)
	requireDirectory(t, destination, map[string]string{"core.json": "old\n"})
	requireNoTransactionSiblings(t, parent, "bundle")
}

func TestPublishPromotionFailureRollsBackOldDestination(t *testing.T) {
	parent := t.TempDir()
	destination := filepath.Join(parent, "bundle")
	writeDirectory(t, destination, map[string]string{"core.json": "old\n"})
	ops := productionOperations()
	productionRename := ops.rename
	ops.rename = func(from, to string) error {
		if to == destination && strings.HasPrefix(filepath.Base(from), ".bundle.stage-") {
			return errors.New("forced promotion failure")
		}
		return productionRename(from, to)
	}
	err := publish(context.Background(), publicationOptions(destination), ops)
	requireFailureKind(t, err, FailurePromote)
	requireDirectory(t, destination, map[string]string{"core.json": "old\n"})
	requireNoTransactionSiblings(t, parent, "bundle")
}

func TestPublishRollbackFailurePreservesRecoveryEvidence(t *testing.T) {
	parent := t.TempDir()
	destination := filepath.Join(parent, "bundle")
	writeDirectory(t, destination, map[string]string{"core.json": "old\n"})
	ops := productionOperations()
	productionRename := ops.rename
	ops.rename = func(from, to string) error {
		if to == destination && strings.HasPrefix(filepath.Base(from), ".bundle.stage-") {
			return errors.New("forced promotion failure")
		}
		if from == destination+".backup" && to == destination {
			return errors.New("forced rollback failure")
		}
		return productionRename(from, to)
	}
	err := publish(context.Background(), publicationOptions(destination), ops)
	requireFailureKind(t, err, FailurePromote)
	requireFailureKind(t, err, FailureRollback)
	if _, statErr := os.Lstat(destination); !errors.Is(statErr, fs.ErrNotExist) {
		t.Errorf("Publish() destination after rollback failure stat error = %v, want not exist", statErr)
	}
	requireDirectory(t, destination+".backup", map[string]string{"core.json": "old\n"})
	if stages := transactionStages(t, parent, "bundle"); len(stages) != 1 {
		t.Errorf("Publish() retained stage count = %d, want one recovery stage", len(stages))
	} else {
		requireDirectory(t, stages[0], map[string]string{"core.json": "new\n"})
	}
}

func TestPublishCommittedCleanupFailureDoesNotRollbackNewDestination(t *testing.T) {
	parent := t.TempDir()
	destination := filepath.Join(parent, "bundle")
	writeDirectory(t, destination, map[string]string{"core.json": "old\n"})
	ops := productionOperations()
	productionRemoveAll := ops.removeAll
	ops.removeAll = func(path string) error {
		if path == destination+".backup" {
			return errors.New("forced backup cleanup failure")
		}
		return productionRemoveAll(path)
	}
	err := publish(context.Background(), publicationOptions(destination), ops)
	requireFailureKind(t, err, FailureCommittedCleanup)
	var failure *Error
	if !errors.As(err, &failure) || !failure.Committed {
		t.Errorf("Publish() committed cleanup failure = %#v, want committed Error", failure)
	}
	requireDirectory(t, destination, map[string]string{"core.json": "new\n"})
	requireDirectory(t, destination+".backup", map[string]string{"core.json": "old\n"})
	if _, statErr := os.Lstat(destination + ".lock"); !errors.Is(statErr, fs.ErrNotExist) {
		t.Errorf("Publish() lock after committed cleanup stat error = %v, want not exist", statErr)
	}
}

func TestPublishUncommittedCleanupFailureRetainsStageEvidence(t *testing.T) {
	parent := t.TempDir()
	destination := filepath.Join(parent, "bundle")
	writeDirectory(t, destination, map[string]string{"core.json": "old\n"})
	ops := productionOperations()
	ops.writeFile = func(string, []byte, fs.FileMode) error {
		return errors.New("forced stage write failure")
	}
	ops.removeAll = func(string) error {
		return errors.New("forced stage cleanup failure")
	}
	err := publish(context.Background(), publicationOptions(destination), ops)
	requireFailureKind(t, err, FailureStage)
	requireFailureKind(t, err, FailureCleanup)
	requireDirectory(t, destination, map[string]string{"core.json": "old\n"})
	stages := transactionStages(t, parent, "bundle")
	if len(stages) != 1 {
		t.Errorf("Publish() retained stage count = %d, want one cleanup remnant", len(stages))
	}
}

func TestPublishCancellationBeforeBackupLeavesOldDestination(t *testing.T) {
	parent := t.TempDir()
	destination := filepath.Join(parent, "bundle")
	writeDirectory(t, destination, map[string]string{"core.json": "old\n"})
	ctx, cancel := context.WithCancel(context.Background())
	ops := productionOperations()
	productionReadFile := ops.readFile
	ops.readFile = func(path string) ([]byte, error) {
		contents, err := productionReadFile(path)
		cancel()
		return contents, err
	}
	err := publish(ctx, publicationOptions(destination), ops)
	requireFailureKind(t, err, FailureCancelled)
	requireDirectory(t, destination, map[string]string{"core.json": "old\n"})
	requireNoTransactionSiblings(t, parent, "bundle")
}

func publicationOptions(destination string) Options {
	return Options{
		Destination: destination,
		Vocabulary:  Vocabulary{Required: []string{"core.json"}, Optional: []string{"optional.json"}},
		Artifacts:   []Artifact{{Name: "core.json", Bytes: []byte("new\n")}},
	}
}

func writeDirectory(t *testing.T, directory string, files map[string]string) {
	t.Helper()
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatalf("os.MkdirAll(%q) error = %v", directory, err)
	}
	for name, contents := range files {
		if err := os.WriteFile(filepath.Join(directory, name), []byte(contents), 0o600); err != nil {
			t.Fatalf("os.WriteFile(%q) error = %v", filepath.Join(directory, name), err)
		}
	}
}

func requireDirectory(t *testing.T, directory string, want map[string]string) {
	t.Helper()
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatalf("os.ReadDir(%q) error = %v, want nil", directory, err)
	}
	if len(entries) != len(want) {
		t.Fatalf("directory %q entries = %d, want %d (%v)", directory, len(entries), len(want), want)
	}
	for _, entry := range entries {
		if !entry.Type().IsRegular() {
			t.Errorf("directory %q entry %q mode = %v, want regular file", directory, entry.Name(), entry.Type())
			continue
		}
		contents, readErr := os.ReadFile(filepath.Join(directory, entry.Name()))
		if readErr != nil {
			t.Errorf("os.ReadFile(%q) error = %v, want nil", entry.Name(), readErr)
			continue
		}
		if got, ok := want[entry.Name()]; !ok || string(contents) != got {
			t.Errorf("directory %q entry %q = %q, want %q", directory, entry.Name(), contents, got)
		}
	}
}

func requireFailureKind(t *testing.T, err error, want FailureKind) {
	t.Helper()
	if !hasFailureKind(err, want) {
		t.Errorf("publication failure kinds = %v, want %q", failureKinds(err), want)
	}
}

func hasFailureKind(err error, want FailureKind) bool {
	for _, kind := range failureKinds(err) {
		if kind == want {
			return true
		}
	}
	return false
}

func failureKinds(err error) []FailureKind {
	if err == nil {
		return nil
	}
	var kinds []FailureKind
	var walk func(error)
	walk = func(current error) {
		if current == nil {
			return
		}
		var failure *Error
		if errors.As(current, &failure) {
			kinds = append(kinds, failure.Kind)
		}
		if joined, ok := current.(interface{ Unwrap() []error }); ok {
			for _, nested := range joined.Unwrap() {
				walk(nested)
			}
			return
		}
		if nested := errors.Unwrap(current); nested != nil {
			walk(nested)
		}
	}
	walk(err)
	return kinds
}

func requireNoTransactionSiblings(t *testing.T, parent, base string) {
	t.Helper()
	entries, err := os.ReadDir(parent)
	if err != nil {
		t.Fatalf("os.ReadDir(%q) error = %v", parent, err)
	}
	for _, entry := range entries {
		if entry.Name() == base {
			continue
		}
		if !strings.HasPrefix(entry.Name(), "."+base+".stage-") &&
			entry.Name() != base+".backup" && entry.Name() != base+".lock" {
			continue
		}
		t.Errorf("transaction sibling %q remains in %q", entry.Name(), parent)
	}
}

func transactionStages(t *testing.T, parent, base string) []string {
	t.Helper()
	entries, err := os.ReadDir(parent)
	if err != nil {
		t.Fatalf("os.ReadDir(%q) error = %v", parent, err)
	}
	var stages []string
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "."+base+".stage-") {
			stages = append(stages, filepath.Join(parent, entry.Name()))
		}
	}
	return stages
}

func ExamplePublish() {
	root, err := os.MkdirTemp("", "artifactpublish-example-")
	if err != nil {
		panic(err)
	}
	defer func() { _ = os.RemoveAll(root) }()
	if err := Publish(context.Background(), Options{
		Destination: filepath.Join(root, "bundle"),
		Vocabulary:  Vocabulary{Required: []string{"summary.json"}},
		Artifacts:   []Artifact{{Name: "summary.json", Bytes: []byte("{}\n")}},
	}); err != nil {
		panic(err)
	}
	contents, err := os.ReadFile(filepath.Join(root, "bundle", "summary.json"))
	if err != nil {
		panic(err)
	}
	fmt.Print(string(contents))
	// Output:
	// {}
}
