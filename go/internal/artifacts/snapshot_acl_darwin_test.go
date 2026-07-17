//go:build darwin && !ios && (amd64 || arm64)

package artifacts

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/dvmrry/infrawright-dev/go/internal/procerr"
)

const inheritedEveryoneACL = "everyone allow list,search,add_file,delete_child,read,write,file_inherit"
const everyoneReadACL = "everyone allow read"

func addInheritedEveryoneACL(path string) error {
	return exec.Command("/bin/chmod", "+a", inheritedEveryoneACL, path).Run()
}

func addEveryoneReadACL(path string) error {
	return exec.Command("/bin/chmod", "+a", everyoneReadACL, path).Run()
}

func TestSnapshotRejectsPrivateDirectoryWithExtendedACLBeforeCreate(t *testing.T) {
	directory := privateTemporaryDirectory(t)
	snapshots := filepath.Join(directory, "snapshots")
	if err := os.Mkdir(snapshots, 0o700); err != nil {
		t.Fatalf("os.Mkdir(%q, 0700) error = %v, want nil", snapshots, err)
	}
	if err := addInheritedEveryoneACL(snapshots); err != nil {
		t.Fatalf("addInheritedEveryoneACL(%q) error = %v, want nil", snapshots, err)
	}
	source := filepath.Join(directory, "source")
	writePrivateFile(t, source, []byte("sensitive snapshot bytes"))

	_, err := SnapshotStableFile(SnapshotStableFileOptions{
		SourcePath:       source,
		PrivateDirectory: snapshots,
		Budget:           mustReadBudget(t, smallReadLimits()),
	})
	requireFailure(t, err, "UNSAFE_SNAPSHOT_DIRECTORY", procerr.CategoryIO)
	requireEmptyDirectory(t, snapshots)
}

func TestSnapshotRejectsInheritedDestinationACLBeforeCopy(t *testing.T) {
	directory := privateTemporaryDirectory(t)
	snapshots := filepath.Join(directory, "snapshots")
	if err := os.Mkdir(snapshots, 0o700); err != nil {
		t.Fatalf("os.Mkdir(%q, 0700) error = %v, want nil", snapshots, err)
	}
	source := filepath.Join(directory, "source")
	writePrivateFile(t, source, []byte("sensitive snapshot bytes"))
	budget := mustReadBudget(t, smallReadLimits())
	sourceOpened := false

	_, err := snapshotStableFile(SnapshotStableFileOptions{
		SourcePath:       source,
		PrivateDirectory: snapshots,
		Budget:           budget,
		ReadOptions: StableReadOptions{
			Hooks: StableReadHooks{
				AfterOpen: func() error {
					sourceOpened = true
					return nil
				},
			},
		},
	}, privateDirectoryHooks{
		beforeCreate: func(path string, _ *privateDirectoryRoot) error {
			return addInheritedEveryoneACL(path)
		},
	})
	requireFailure(t, err, "SNAPSHOT_PATH_CHANGED", procerr.CategoryIO)
	if sourceOpened || budget.Files() != 0 || budget.Bytes().Sign() != 0 {
		t.Errorf("inherited destination ACL reached source read: opened %t, files %d, bytes %v; want false, 0, 0", sourceOpened, budget.Files(), budget.Bytes())
	}
	entries, err := os.ReadDir(snapshots)
	if err != nil {
		t.Fatalf("os.ReadDir(%q) error = %v, want nil", snapshots, err)
	}
	if len(entries) != 1 {
		t.Fatalf("os.ReadDir(%q) entry count = %d, want one scrubbed destination", snapshots, len(entries))
	}
	destinationPath := filepath.Join(snapshots, entries[0].Name())
	destination, err := os.Open(destinationPath)
	if err != nil {
		t.Fatalf("os.Open(%q) error = %v, want nil", destinationPath, err)
	}
	defer func() { _ = destination.Close() }()
	hasExtendedACL, err := platformDescriptorHasExtendedACL(destination)
	if err != nil {
		t.Fatalf("platformDescriptorHasExtendedACL(%q) error = %v, want nil", destinationPath, err)
	}
	if !hasExtendedACL {
		t.Errorf("inherited destination ACL was not present after descriptor chmod")
	}
	info, err := destination.Stat()
	if err != nil {
		t.Fatalf("(*os.File).Stat(%q) error = %v, want nil", destinationPath, err)
	}
	if info.Size() != 0 {
		t.Errorf("scrubbed inherited-ACL destination size = %d, want 0", info.Size())
	}
}

func TestSnapshotPreservesNodeFailurePrecedenceForACLAndMissingDestination(t *testing.T) {
	directory := privateTemporaryDirectory(t)
	snapshots := filepath.Join(directory, "snapshots")
	if err := os.Mkdir(snapshots, 0o700); err != nil {
		t.Fatalf("os.Mkdir(%q, 0700) error = %v, want nil", snapshots, err)
	}
	source := filepath.Join(directory, "source")
	writePrivateFile(t, source, []byte("sensitive snapshot bytes"))
	var moved string

	_, err := SnapshotStableFile(SnapshotStableFileOptions{
		SourcePath:       source,
		PrivateDirectory: snapshots,
		Budget:           mustReadBudget(t, smallReadLimits()),
		ReadOptions: StableReadOptions{
			Hooks: StableReadHooks{
				BeforeFinalStat: func() error {
					path, err := singleSnapshotPath(snapshots)
					if err != nil {
						return err
					}
					moved = path + ".original"
					if err := os.Rename(path, moved); err != nil {
						return err
					}
					return addEveryoneReadACL(moved)
				},
			},
		},
	})
	requireFailure(t, err, "FILE_CHANGED", procerr.CategoryIO)
	destination, err := os.Open(moved)
	if err != nil {
		t.Fatalf("os.Open(%q) error = %v, want nil", moved, err)
	}
	defer func() { _ = destination.Close() }()
	hasExtendedACL, err := platformDescriptorHasExtendedACL(destination)
	if err != nil {
		t.Fatalf("platformDescriptorHasExtendedACL(%q) error = %v, want nil", moved, err)
	}
	if !hasExtendedACL {
		t.Errorf("compound-race bound destination ACL is absent, want present")
	}
	info, err := destination.Stat()
	if err != nil {
		t.Fatalf("(*os.File).Stat(%q) error = %v, want nil", moved, err)
	}
	if info.Size() != 0 {
		t.Errorf("compound-race bound destination size = %d, want 0", info.Size())
	}
}
