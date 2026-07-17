package tfrender

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"syscall"
	"testing"
)

func requireDarwinLinuxNodeFSErrors(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("native filesystem error translation is supported on the Darwin/Linux release matrix")
	}
}

func filesystemErrorCompiledArtifacts(root string) CompiledTransformArtifacts {
	lookup := "{}\n"
	moves := "moved {}\n"
	return CompiledTransformArtifacts{
		Binding: GeneratedBindingsResult{
			Resources: map[string]any{"sample_resource.example": map[string]any{}},
		},
		ConfigText:    "{}\n",
		LookupText:    &lookup,
		NewImports:    "# imports\n",
		RenderedMoves: &moves,
		ResourceType:  "sample_resource",
		Paths: TransformArtifactPaths{
			Config:            filepath.Join(root, "config", "sample_resource.auto.tfvars.json"),
			GeneratedBindings: filepath.Join(root, "config", "sample_resource.generated.expressions.json"),
			Imports:           filepath.Join(root, "imports", "sample_resource_imports.tf"),
			Lookup:            filepath.Join(root, "config", "sample_resource.lookup.json"),
			Moves:             filepath.Join(root, "imports", "sample_resource_moves.tf"),
			StaleConfig:       filepath.Join(root, "config", "sample_resource.auto.tfvars"),
		},
	}
}

func assertNodePathError(
	t *testing.T,
	err error,
	wantMessage string,
	wantErrno syscall.Errno,
) *fs.PathError {
	t.Helper()
	if err == nil || err.Error() != wantMessage {
		t.Fatalf("filesystem operation error = %v, want %q", err, wantMessage)
	}
	if !errors.Is(err, wantErrno) {
		t.Errorf("errors.Is(%v, %v) = false, want true", err, wantErrno)
	}
	var pathErr *fs.PathError
	if !errors.As(err, &pathErr) {
		t.Fatalf("errors.As(%v, *fs.PathError) = false, want true", err)
	}
	return pathErr
}

func assertNodeLinkError(
	t *testing.T,
	err error,
	wantMessage string,
	wantErrno syscall.Errno,
	wantSource string,
	wantDest string,
) *os.LinkError {
	t.Helper()
	if err == nil || err.Error() != wantMessage {
		t.Fatalf("filesystem operation error = %v, want %q", err, wantMessage)
	}
	if !errors.Is(err, wantErrno) {
		t.Errorf("errors.Is(%v, %v) = false, want true", err, wantErrno)
	}
	var linkErr *os.LinkError
	if !errors.As(err, &linkErr) {
		t.Fatalf("errors.As(%v, *os.LinkError) = false, want true", err)
	}
	if linkErr.Old != wantSource || linkErr.New != wantDest {
		t.Errorf("raw LinkError paths = (%q, %q), want (%q, %q)", linkErr.Old, linkErr.New, wantSource, wantDest)
	}
	return linkErr
}

func TestPublishCompiledTransformArtifactsMkdirErrorsMatchNode(t *testing.T) {
	requireDarwinLinuxNodeFSErrors(t)

	tests := []struct {
		name      string
		configure func(*CompiledTransformArtifacts, string) string
	}{
		{
			name: "config_parent",
			configure: func(compiled *CompiledTransformArtifacts, blocker string) string {
				compiled.Paths.Config = filepath.Join(blocker, "config", "sample.auto.tfvars.json")
				return filepath.Dir(compiled.Paths.Config)
			},
		},
		{
			name: "imports_parent",
			configure: func(compiled *CompiledTransformArtifacts, blocker string) string {
				compiled.Paths.Imports = filepath.Join(blocker, "imports", "sample_imports.tf")
				return filepath.Dir(compiled.Paths.Imports)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			compiled := filesystemErrorCompiledArtifacts(root)
			blocker := filepath.Join(root, "blocker")
			if err := os.WriteFile(blocker, []byte("not a directory"), 0o600); err != nil {
				t.Fatalf("os.WriteFile(%q) error = %v", blocker, err)
			}
			requestedDirectory := test.configure(&compiled, blocker)

			_, err := PublishCompiledTransformArtifacts(compiled)
			want := "ENOTDIR: not a directory, mkdir '" + requestedDirectory + "'"
			assertNodePathError(t, err, want, syscall.ENOTDIR)
		})
	}
}

func TestPublishCompiledTransformArtifactsWriteErrorsMatchNode(t *testing.T) {
	requireDarwinLinuxNodeFSErrors(t)

	tests := []struct {
		name   string
		target func(CompiledTransformArtifacts) string
	}{
		{name: "lookup", target: func(compiled CompiledTransformArtifacts) string { return compiled.Paths.Lookup }},
		{name: "moves", target: func(compiled CompiledTransformArtifacts) string { return compiled.Paths.Moves }},
		{name: "config", target: func(compiled CompiledTransformArtifacts) string { return compiled.Paths.Config }},
		{name: "generated_bindings", target: func(compiled CompiledTransformArtifacts) string { return compiled.Paths.GeneratedBindings }},
		{name: "imports", target: func(compiled CompiledTransformArtifacts) string { return compiled.Paths.Imports }},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			compiled := filesystemErrorCompiledArtifacts(t.TempDir())
			target := test.target(compiled)
			if err := os.MkdirAll(target, 0o700); err != nil {
				t.Fatalf("os.MkdirAll(%q) error = %v", target, err)
			}

			_, err := PublishCompiledTransformArtifacts(compiled)
			want := "EISDIR: illegal operation on a directory, open '" + target + "'"
			pathErr := assertNodePathError(t, err, want, syscall.EISDIR)
			if pathErr.Op != "open" || pathErr.Path != target {
				t.Errorf("raw PathError = {Op:%q Path:%q}, want {Op:%q Path:%q}", pathErr.Op, pathErr.Path, "open", target)
			}
		})
	}
}

func TestAssertRegularBatchArtifactTargetFilesystemErrorsMatchNode(t *testing.T) {
	requireDarwinLinuxNodeFSErrors(t)

	missing := filepath.Join(t.TempDir(), "missing")
	if err := assertRegularBatchArtifactTarget(missing); err != nil {
		t.Errorf("assertRegularBatchArtifactTarget(%q) error = %v, want nil for ENOENT", missing, err)
	}

	root := t.TempDir()
	blocker := filepath.Join(root, "blocker")
	if err := os.WriteFile(blocker, []byte("not a directory"), 0o600); err != nil {
		t.Fatalf("os.WriteFile(%q) error = %v", blocker, err)
	}
	target := filepath.Join(blocker, "child")
	err := assertRegularBatchArtifactTarget(target)
	want := "ENOTDIR: not a directory, lstat '" + target + "'"
	pathErr := assertNodePathError(t, err, want, syscall.ENOTDIR)
	if pathErr.Op != "lstat" {
		t.Errorf("raw PathError.Op = %q, want %q", pathErr.Op, "lstat")
	}
}

func TestPrepareBatchArtifactMutationsFilesystemErrorsMatchNode(t *testing.T) {
	requireDarwinLinuxNodeFSErrors(t)

	t.Run("parent_mkdir", func(t *testing.T) {
		root := t.TempDir()
		blocker := filepath.Join(root, "blocker")
		if err := os.WriteFile(blocker, []byte("not a directory"), 0o600); err != nil {
			t.Fatalf("os.WriteFile(%q) error = %v", blocker, err)
		}
		target := filepath.Join(blocker, "parent", "artifact")
		_, _, err := prepareBatchArtifactMutations([]batchArtifactMutation{{
			kind: mutationRemove, target: target,
		}})
		requestedDirectory := filepath.Dir(target)
		want := "ENOTDIR: not a directory, mkdir '" + requestedDirectory + "'"
		assertNodePathError(t, err, want, syscall.ENOTDIR)
	})

	t.Run("stage_write", func(t *testing.T) {
		root := t.TempDir()
		contents := "staged"
		target := filepath.Join(root, "artifact")
		var stagePath string
		_, _, err := prepareBatchArtifactMutationsWithStageWriter(
			[]batchArtifactMutation{{kind: mutationWrite, target: target, contents: &contents}},
			func(path string, _ []byte, _ os.FileMode) error {
				stagePath = path
				return &fs.PathError{Op: "open", Path: path, Err: syscall.EISDIR}
			},
		)
		if stagePath == "" {
			t.Fatal("injected stage writer was not called")
		}
		want := "EISDIR: illegal operation on a directory, open '" + stagePath + "'"
		pathErr := assertNodePathError(t, err, want, syscall.EISDIR)
		if pathErr.Path != stagePath {
			t.Errorf("raw PathError.Path = %q, want stage path %q", pathErr.Path, stagePath)
		}
	})
}

func TestPrepareBatchArtifactMutationsRetainsTranslatedErrorBeforeCleanupFailure(t *testing.T) {
	requireDarwinLinuxNodeFSErrors(t)

	root := t.TempDir()
	contents := "staged"
	target := filepath.Join(root, "artifact")
	var stagePath string
	var transactionDirectories []string
	var cleanupFailure error
	_, _, err := prepareBatchArtifactMutationsWithFilesystem(
		[]batchArtifactMutation{{kind: mutationWrite, target: target, contents: &contents}},
		func(path string, _ []byte, _ os.FileMode) error {
			stagePath = path
			return &fs.PathError{Op: "open", Path: path, Err: syscall.EISDIR}
		},
		func(directories []string) []error {
			if len(directories) != 1 {
				t.Fatalf("cleanup transaction directories = %v, want one staged directory", directories)
			}
			transactionDirectories = append([]string(nil), directories...)
			cleanupFailure = &fs.PathError{
				Op:   "scandir",
				Path: directories[0],
				Err:  syscall.EACCES,
			}
			return []error{cleanupFailure}
		},
	)
	for _, directory := range transactionDirectories {
		directory := directory
		t.Cleanup(func() { _ = os.RemoveAll(directory) })
	}

	var aggregate *multiError
	if !errors.As(err, &aggregate) {
		t.Fatalf("errors.As(%v, *multiError) = false, want deterministic aggregate", err)
	}
	if aggregate.Error() != "transform artifact batch staging and cleanup both failed" {
		t.Errorf("multiError.Error() = %q, want staging-and-cleanup message", aggregate.Error())
	}
	if len(aggregate.errs) != 2 {
		t.Fatalf("multiError.errs length = %d, want 2", len(aggregate.errs))
	}
	wantStage := "EISDIR: illegal operation on a directory, open '" + stagePath + "'"
	assertNodePathError(t, aggregate.errs[0], wantStage, syscall.EISDIR)
	if aggregate.errs[1] != cleanupFailure {
		t.Errorf("multiError.errs[1] = %v, want raw cleanup failure %v", aggregate.errs[1], cleanupFailure)
	}
	if !errors.Is(err, syscall.EISDIR) || !errors.Is(err, syscall.EACCES) {
		t.Errorf(
			"aggregate chain errors.Is = (stage EISDIR:%t, cleanup EACCES:%t), want both true",
			errors.Is(err, syscall.EISDIR),
			errors.Is(err, syscall.EACCES),
		)
	}
}

func TestApplyBatchArtifactMutationsRenameErrorsMatchNode(t *testing.T) {
	requireDarwinLinuxNodeFSErrors(t)

	t.Run("target_to_backup", func(t *testing.T) {
		root := t.TempDir()
		target := filepath.Join(root, "target")
		if err := os.WriteFile(target, []byte("original"), 0o600); err != nil {
			t.Fatalf("os.WriteFile(%q) error = %v", target, err)
		}
		blocker := filepath.Join(root, "blocker")
		if err := os.WriteFile(blocker, []byte("not a directory"), 0o600); err != nil {
			t.Fatalf("os.WriteFile(%q) error = %v", blocker, err)
		}
		backup := filepath.Join(blocker, "backup")
		_, err := applyBatchArtifactMutations([]preparedBatchArtifactMutation{{
			batchArtifactMutation: batchArtifactMutation{kind: mutationRemove, target: target},
			backupPath:            backup,
		}})
		want := "ENOTDIR: not a directory, rename '" + target + "' -> '" + backup + "'"
		assertNodeLinkError(t, err, want, syscall.ENOTDIR, target, backup)
	})

	t.Run("stage_to_target", func(t *testing.T) {
		root := t.TempDir()
		transactionDirectory := filepath.Join(root, "transaction")
		if err := os.Mkdir(transactionDirectory, 0o700); err != nil {
			t.Fatalf("os.Mkdir(%q) error = %v", transactionDirectory, err)
		}
		blocker := filepath.Join(root, "blocker")
		if err := os.WriteFile(blocker, []byte("not a directory"), 0o600); err != nil {
			t.Fatalf("os.WriteFile(%q) error = %v", blocker, err)
		}
		stage := filepath.Join(blocker, "stage")
		target := filepath.Join(root, "target")
		backup := filepath.Join(transactionDirectory, "backup")
		_, err := applyBatchArtifactMutations([]preparedBatchArtifactMutation{{
			batchArtifactMutation: batchArtifactMutation{kind: mutationWrite, target: target},
			backupPath:            backup,
			stagePath:             &stage,
		}})
		want := "ENOTDIR: not a directory, rename '" + stage + "' -> '" + target + "'"
		assertNodeLinkError(t, err, want, syscall.ENOTDIR, stage, target)
	})
}

func TestApplyBatchArtifactMutationsBackupLstatFailureRestoresOriginal(t *testing.T) {
	requireDarwinLinuxNodeFSErrors(t)

	root := t.TempDir()
	transactionDirectory := filepath.Join(root, "transaction")
	if err := os.Mkdir(transactionDirectory, 0o700); err != nil {
		t.Fatalf("os.Mkdir(%q) error = %v", transactionDirectory, err)
	}
	target := filepath.Join(root, "target")
	backup := filepath.Join(transactionDirectory, "backup")
	stage := filepath.Join(transactionDirectory, "stage")
	original := []byte("original")
	replacement := []byte("replacement")
	if err := os.WriteFile(target, original, 0o600); err != nil {
		t.Fatalf("os.WriteFile(%q) error = %v", target, err)
	}
	if err := os.WriteFile(stage, replacement, 0o600); err != nil {
		t.Fatalf("os.WriteFile(%q) error = %v", stage, err)
	}

	rawErrorPath := filepath.Join(root, "raw-path-must-not-render")
	lstatCalls := 0
	_, err := applyBatchArtifactMutationsWithLstat(
		[]preparedBatchArtifactMutation{{
			batchArtifactMutation: batchArtifactMutation{kind: mutationWrite, target: target},
			backupPath:            backup,
			stagePath:             &stage,
		}},
		func(path string) (os.FileInfo, error) {
			lstatCalls++
			if path != backup {
				t.Fatalf("post-backup lstat path = %q, want %q", path, backup)
			}
			return nil, &fs.PathError{Op: "lstat", Path: rawErrorPath, Err: syscall.ENOENT}
		},
	)
	if lstatCalls != 1 {
		t.Errorf("post-backup lstat calls = %d, want 1", lstatCalls)
	}
	want := "ENOENT: no such file or directory, lstat '" + backup + "'"
	pathErr := assertNodePathError(t, err, want, syscall.ENOENT)
	if pathErr.Op != "lstat" || pathErr.Path != rawErrorPath {
		t.Errorf(
			"raw PathError = {Op:%q Path:%q}, want retained raw cause {Op:%q Path:%q}",
			pathErr.Op,
			pathErr.Path,
			"lstat",
			rawErrorPath,
		)
	}
	gotOriginal, readErr := os.ReadFile(target)
	if readErr != nil {
		t.Fatalf("os.ReadFile(restored target %q) error = %v", target, readErr)
	}
	if string(gotOriginal) != string(original) {
		t.Errorf("restored target bytes = %q, want original %q", gotOriginal, original)
	}
	gotStage, readErr := os.ReadFile(stage)
	if readErr != nil {
		t.Fatalf("os.ReadFile(preserved stage %q) error = %v", stage, readErr)
	}
	if string(gotStage) != string(replacement) {
		t.Errorf("preserved stage bytes = %q, want replacement %q", gotStage, replacement)
	}
	if _, statErr := os.Lstat(backup); !errors.Is(statErr, os.ErrNotExist) {
		t.Errorf("os.Lstat(restored backup %q) error = %v, want ENOENT after rollback", backup, statErr)
	}
}

func TestApplyBatchArtifactMutationsRollbackRenameRetainsAggregateOrderAndChains(t *testing.T) {
	requireDarwinLinuxNodeFSErrors(t)

	root := t.TempDir()
	publishedDirectory := filepath.Join(root, "published")
	transactionDirectory := filepath.Join(root, "transaction")
	if err := os.MkdirAll(publishedDirectory, 0o700); err != nil {
		t.Fatalf("os.MkdirAll(%q) error = %v", publishedDirectory, err)
	}
	if err := os.Mkdir(transactionDirectory, 0o700); err != nil {
		t.Fatalf("os.Mkdir(%q) error = %v", transactionDirectory, err)
	}
	target := filepath.Join(publishedDirectory, "target")
	backup := filepath.Join(transactionDirectory, "backup")
	stage := filepath.Join(transactionDirectory, "stage")
	if err := os.WriteFile(target, []byte("original"), 0o600); err != nil {
		t.Fatalf("os.WriteFile(%q) error = %v", target, err)
	}
	if err := os.WriteFile(stage, []byte("replacement"), 0o600); err != nil {
		t.Fatalf("os.WriteFile(%q) error = %v", stage, err)
	}
	blocker := filepath.Join(root, "blocker")
	if err := os.WriteFile(blocker, []byte("not a directory"), 0o600); err != nil {
		t.Fatalf("os.WriteFile(%q) error = %v", blocker, err)
	}
	failingTarget := filepath.Join(blocker, "child")
	failingBackup := filepath.Join(transactionDirectory, "failing-backup")
	cleanup, err := InstallTransformArtifactBatchCommitHookForTests(func(mutation BatchArtifactMutationRef, phase string) error {
		if phase != "commit" || mutation.Target != failingTarget {
			return nil
		}
		if err := os.RemoveAll(publishedDirectory); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		t.Fatalf("InstallTransformArtifactBatchCommitHookForTests() error = %v", err)
	}
	t.Cleanup(cleanup)

	_, err = applyBatchArtifactMutations([]preparedBatchArtifactMutation{
		{
			batchArtifactMutation: batchArtifactMutation{kind: mutationWrite, target: target},
			backupPath:            backup,
			stagePath:             &stage,
		},
		{
			batchArtifactMutation: batchArtifactMutation{kind: mutationRemove, target: failingTarget},
			backupPath:            failingBackup,
		},
	})
	var rollbackErr *BatchArtifactRollbackError
	if !errors.As(err, &rollbackErr) {
		t.Fatalf("errors.As(%v, *BatchArtifactRollbackError) = false, want true", err)
	}
	if len(rollbackErr.Errors) != 2 {
		t.Fatalf("BatchArtifactRollbackError.Errors length = %d, want 2", len(rollbackErr.Errors))
	}
	wantApply := "ENOTDIR: not a directory, rename '" + failingTarget + "' -> '" + failingBackup + "'"
	assertNodeLinkError(t, rollbackErr.Errors[0], wantApply, syscall.ENOTDIR, failingTarget, failingBackup)
	wantRollback := "ENOENT: no such file or directory, rename '" + backup + "' -> '" + target + "'"
	assertNodeLinkError(t, rollbackErr.Errors[1], wantRollback, syscall.ENOENT, backup, target)
	if !errors.Is(err, syscall.ENOTDIR) || !errors.Is(err, syscall.ENOENT) {
		t.Errorf(
			"aggregate chain errors.Is = (apply ENOTDIR:%t, rollback ENOENT:%t), want both true",
			errors.Is(err, syscall.ENOTDIR),
			errors.Is(err, syscall.ENOENT),
		)
	}
	if len(rollbackErr.TransactionDirectories) != 1 || rollbackErr.TransactionDirectories[0] != transactionDirectory {
		t.Errorf("BatchArtifactRollbackError.TransactionDirectories = %v, want [%q]", rollbackErr.TransactionDirectories, transactionDirectory)
	}
}

func TestWriteDerivedTransformArtifactFilesystemErrorsMatchNode(t *testing.T) {
	requireDarwinLinuxNodeFSErrors(t)

	items := map[string]map[string]any{"derived": {"name": "Derived"}}
	references := map[string]TransformReferenceSpec{}

	t.Run("config_parent_mkdir", func(t *testing.T) {
		blocker := filepath.Join(t.TempDir(), "blocker")
		if err := os.WriteFile(blocker, []byte("not a directory"), 0o600); err != nil {
			t.Fatalf("os.WriteFile(%q) error = %v", blocker, err)
		}
		dep := testDeployment(blocker, false)
		paths, err := ComputeTransformArtifactPaths(dep, "sample_derived", "tenant")
		if err != nil {
			t.Fatalf("ComputeTransformArtifactPaths() error = %v", err)
		}
		_, err = WriteDerivedTransformArtifact(
			dep, items, references, "sample_derived", "sample_source", "tenant", "items", nil,
		)
		requestedDirectory := filepath.Dir(paths.Config)
		want := "ENOTDIR: not a directory, mkdir '" + requestedDirectory + "'"
		assertNodePathError(t, err, want, syscall.ENOTDIR)
	})

	t.Run("config_write", func(t *testing.T) {
		dep := testDeployment(t.TempDir(), false)
		paths, err := ComputeTransformArtifactPaths(dep, "sample_derived", "tenant")
		if err != nil {
			t.Fatalf("ComputeTransformArtifactPaths() error = %v", err)
		}
		if err := os.MkdirAll(paths.Config, 0o700); err != nil {
			t.Fatalf("os.MkdirAll(%q) error = %v", paths.Config, err)
		}
		_, err = WriteDerivedTransformArtifact(
			dep, items, references, "sample_derived", "sample_source", "tenant", "items", nil,
		)
		want := "EISDIR: illegal operation on a directory, open '" + paths.Config + "'"
		assertNodePathError(t, err, want, syscall.EISDIR)
	})
}

type node24TransformFilesystemErrorVector struct {
	ID       string `json:"id"`
	Mapped   bool   `json:"mapped"`
	Code     string `json:"code"`
	Errno    int    `json:"errno"`
	Syscall  string `json:"syscall"`
	Path     string `json:"path"`
	Dest     string `json:"dest"`
	Message  string `json:"message"`
	Deferral string `json:"deferral"`
}

type node24TransformFilesystemErrorOracle struct {
	SchemaVersion  int                                    `json:"schema_version"`
	CapturedAt     string                                 `json:"captured_at"`
	NodeVersion    string                                 `json:"node_version"`
	NodeCommit     string                                 `json:"node_commit"`
	Platform       string                                 `json:"platform"`
	API            string                                 `json:"api"`
	Normalization  string                                 `json:"normalization"`
	CaptureCommand string                                 `json:"capture_command"`
	Vectors        []node24TransformFilesystemErrorVector `json:"vectors"`
}

var wantNode24TransformFilesystemErrorOracle = node24TransformFilesystemErrorOracle{
	SchemaVersion:  1,
	CapturedAt:     "2026-07-17",
	NodeVersion:    "v24.15.0",
	NodeCommit:     "848430679556aed0bd073f2bc263331ad84fa119",
	Platform:       "darwin/arm64 (Darwin 24.6.0, uid/euid 501)",
	API:            "node:fs/promises",
	Normalization:  "fresh temporary root -> $ROOT; mkdtemp's six generated characters -> $SUFFIX",
	CaptureCommand: "node internal/tfrender/testdata/capture-node24-transform-filesystem-errors.mjs",
	Vectors: []node24TransformFilesystemErrorVector{
		{
			ID:      "mkdir_through_file",
			Mapped:  true,
			Code:    "ENOTDIR",
			Errno:   -20,
			Syscall: "mkdir",
			Path:    "$ROOT/blocker/parent",
			Message: "ENOTDIR: not a directory, mkdir '$ROOT/blocker/parent'",
		},
		{
			ID:      "write_file_directory",
			Mapped:  true,
			Code:    "EISDIR",
			Errno:   -21,
			Syscall: "open",
			Path:    "$ROOT/directory",
			Message: "EISDIR: illegal operation on a directory, open '$ROOT/directory'",
		},
		{
			ID:      "lstat_through_file",
			Mapped:  true,
			Code:    "ENOTDIR",
			Errno:   -20,
			Syscall: "lstat",
			Path:    "$ROOT/blocker/child",
			Message: "ENOTDIR: not a directory, lstat '$ROOT/blocker/child'",
		},
		{
			ID:      "rename_through_file",
			Mapped:  true,
			Code:    "ENOTDIR",
			Errno:   -20,
			Syscall: "rename",
			Path:    "$ROOT/blocker/child",
			Dest:    "$ROOT/dest",
			Message: "ENOTDIR: not a directory, rename '$ROOT/blocker/child' -> '$ROOT/dest'",
		},
		{
			ID:      "rename_dest_through_file",
			Mapped:  true,
			Code:    "ENOTDIR",
			Errno:   -20,
			Syscall: "rename",
			Path:    "$ROOT/source-dest-file",
			Dest:    "$ROOT/blocker/backup",
			Message: "ENOTDIR: not a directory, rename '$ROOT/source-dest-file' -> '$ROOT/blocker/backup'",
		},
		{
			ID:      "rename_dest_missing_parent",
			Mapped:  true,
			Code:    "ENOENT",
			Errno:   -2,
			Syscall: "rename",
			Path:    "$ROOT/source-missing-parent",
			Dest:    "$ROOT/missing/target",
			Message: "ENOENT: no such file or directory, rename '$ROOT/source-missing-parent' -> '$ROOT/missing/target'",
		},
		{
			ID:       "unlink_missing",
			Code:     "ENOENT",
			Errno:    -2,
			Syscall:  "unlink",
			Path:     "$ROOT/missing-unlink",
			Message:  "ENOENT: no such file or directory, unlink '$ROOT/missing-unlink'",
			Deferral: "nodefserr has no unlink operation; raw ENOENT remains a control-flow success and other unlink errors remain untranslated",
		},
		{
			ID:       "mkdtemp_missing_parent",
			Code:     "ENOENT",
			Errno:    -2,
			Syscall:  "mkdtemp",
			Path:     "$ROOT/missing-parent/.batch-$SUFFIX",
			Message:  "ENOENT: no such file or directory, mkdtemp '$ROOT/missing-parent/.batch-$SUFFIX'",
			Deferral: "nodefserr has no mkdtemp operation and the observable error path includes the generated suffix",
		},
		{
			ID:       "chmod_missing",
			Code:     "ENOENT",
			Errno:    -2,
			Syscall:  "chmod",
			Path:     "$ROOT/missing-chmod",
			Message:  "ENOENT: no such file or directory, chmod '$ROOT/missing-chmod'",
			Deferral: "nodefserr has no chmod operation",
		},
		{
			ID:       "rm_locked_parent",
			Code:     "EACCES",
			Errno:    -13,
			Syscall:  "rmdir",
			Path:     "$ROOT/locked/victim",
			Message:  "EACCES: permission denied, rmdir '$ROOT/locked/victim'",
			Deferral: "recursive rm can surface internal traversal operations; nodefserr has no rm/rmdir operation",
		},
		{
			ID:       "rm_unreadable_tree",
			Code:     "EACCES",
			Errno:    -13,
			Syscall:  "scandir",
			Path:     "$ROOT/unreadable",
			Message:  "EACCES: permission denied, scandir '$ROOT/unreadable'",
			Deferral: "the same recursive rm API can instead surface scandir, so cleanup errors remain untranslated pending an operation-aware contract",
		},
	},
}

func TestNode24TransformFilesystemErrorOraclePayload(t *testing.T) {
	content, err := os.ReadFile("testdata/node24-transform-filesystem-errors.json")
	if err != nil {
		t.Fatalf("os.ReadFile(node24 transform filesystem oracle) error = %v", err)
	}
	var fixture node24TransformFilesystemErrorOracle
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&fixture); err != nil {
		t.Fatalf("decode node24 transform filesystem oracle error = %v", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		t.Fatalf("node24 transform filesystem oracle has trailing JSON: %v", err)
	}
	if !reflect.DeepEqual(fixture, wantNode24TransformFilesystemErrorOracle) {
		t.Fatalf(
			"node24 transform filesystem oracle payload mismatch\n got: %#v\nwant: %#v",
			fixture,
			wantNode24TransformFilesystemErrorOracle,
		)
	}
}

func TestNode24TransformFilesystemErrorCaptureReproducesFixture(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("pinned Node filesystem oracle was captured on Darwin/arm64")
	}
	nodePath, err := exec.LookPath("node")
	if err != nil {
		t.Skipf("Node v24.15.0 capture runtime unavailable: %v", err)
	}
	version, err := exec.Command(nodePath, "--version").Output()
	if err != nil {
		t.Fatalf("node --version error = %v", err)
	}
	if got := string(bytes.TrimSpace(version)); got != "v24.15.0" {
		t.Skipf("Node capture runtime version = %q, want v24.15.0", got)
	}

	command := exec.Command(nodePath, "testdata/capture-node24-transform-filesystem-errors.mjs")
	captured, err := command.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 77 {
			t.Skipf("pinned capture environment unavailable: %s", bytes.TrimSpace(exitErr.Stderr))
		}
		t.Fatalf("Node filesystem oracle capture error = %v", err)
	}
	fixture, err := os.ReadFile("testdata/node24-transform-filesystem-errors.json")
	if err != nil {
		t.Fatalf("os.ReadFile(node24 transform filesystem oracle) error = %v", err)
	}
	if !bytes.Equal(captured, fixture) {
		t.Fatalf("Node filesystem oracle recapture differs from fixture\n--- captured\n%s--- fixture\n%s", captured, fixture)
	}
}
