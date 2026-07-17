//go:build (darwin || linux) && !ios && !android && (amd64 || arm64)

package artifacts

import (
	"bytes"
	"context"
	cryptorand "crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"syscall"
	"testing"
	"time"

	"github.com/dvmrry/infrawright-dev/go/internal/procerr"
)

type failedEntropyReader struct {
	err error
}

func (r failedEntropyReader) Read([]byte) (int, error) {
	return 0, r.err
}

func TestZeroValueReadBudgetFailsBeforeSnapshotDirectoryAccess(t *testing.T) {
	directory := privateTemporaryDirectory(t)
	snapshots := filepath.Join(directory, "snapshots")
	if err := os.Mkdir(snapshots, 0o700); err != nil {
		t.Fatalf("os.Mkdir(%q, 0700) error = %v, want nil", snapshots, err)
	}
	var budget ReadBudget
	directoryInspected := false
	_, err := snapshotStableFile(SnapshotStableFileOptions{
		SourcePath:       filepath.Join(directory, "source"),
		PrivateDirectory: snapshots,
		Budget:           &budget,
	}, privateDirectoryHooks{
		afterLstat: func(string) error {
			directoryInspected = true
			return nil
		},
	})
	requireFailure(t, err, "READ_FAILED", procerr.CategoryIO)
	if directoryInspected {
		t.Errorf("snapshot directory inspection hook ran for a zero-value ReadBudget")
	}
	requireEmptyDirectory(t, snapshots)
}

func TestSnapshotBindsBytesDigestSizeIdentityAndPrivateMode(t *testing.T) {
	directory := privateTemporaryDirectory(t)
	snapshots := filepath.Join(directory, "snapshots")
	if err := os.Mkdir(snapshots, 0o700); err != nil {
		t.Fatalf("os.Mkdir(%q, 0700) error = %v, want nil", snapshots, err)
	}
	source := filepath.Join(directory, "tfplan")
	content := []byte{0, 1, 2, 3, 255}
	writePrivateFile(t, source, content)

	snapshot, err := SnapshotStableFile(SnapshotStableFileOptions{
		SourcePath:       source,
		PrivateDirectory: snapshots,
		Budget:           mustReadBudget(t, smallReadLimits()),
	})
	if err != nil {
		t.Fatalf("SnapshotStableFile(%q) error = %v, want nil", source, err)
	}
	if !regexp.MustCompile(`^plan-[0-9a-f]{32}$`).MatchString(filepath.Base(snapshot.Path)) {
		t.Errorf("SnapshotStableFile(%q).Path base = %q, want plan- plus 32 lowercase hex digits", source, filepath.Base(snapshot.Path))
	}
	got, err := os.ReadFile(snapshot.Path)
	if err != nil {
		t.Fatalf("os.ReadFile(%q) error = %v, want nil", snapshot.Path, err)
	}
	if !bytes.Equal(got, content) {
		t.Errorf("snapshot bytes = %v, want %v", got, content)
	}
	wantHash := sha256.Sum256(content)
	if snapshot.SHA256 != hex.EncodeToString(wantHash[:]) || snapshot.Size != int64(len(content)) {
		t.Errorf("SnapshotStableFile(%q) digest = %+v, want sha256 %x and size %d", source, snapshot.StableFileDigest, wantHash, len(content))
	}
	info, err := os.Lstat(snapshot.Path)
	if err != nil {
		t.Fatalf("os.Lstat(%q) error = %v, want nil", snapshot.Path, err)
	}
	if gotMode := info.Mode().Perm(); gotMode != 0o600 {
		t.Errorf("snapshot mode = %#o, want 0600", gotMode)
	}
	wantIdentity, ok := platformMetadataIdentity(info)
	if !ok {
		t.Fatalf("platformMetadataIdentity(os.Lstat(%q)) ok = false, want true", snapshot.Path)
	}
	if snapshot.StableFileIdentity != wantIdentity.stableIdentity() {
		t.Errorf("snapshot identity = %+v, want %+v", snapshot.StableFileIdentity, wantIdentity.stableIdentity())
	}
	owner, ownerOK := platformOwnerID(info)
	effectiveUID, effectiveUIDOK := platformEffectiveUID()
	if !ownerOK || !effectiveUIDOK || owner != effectiveUID {
		t.Errorf("snapshot owner = %d (ok %t), effective UID = %d (ok %t); want equal supported identities", owner, ownerOK, effectiveUID, effectiveUIDOK)
	}
}

func TestSnapshotAllowsOwnerWriteSearchDirectoryWithoutReadPermission(t *testing.T) {
	directory := privateTemporaryDirectory(t)
	snapshots := filepath.Join(directory, "snapshots")
	if err := os.Mkdir(snapshots, 0o300); err != nil {
		t.Fatalf("os.Mkdir(%q, 0300) error = %v, want nil", snapshots, err)
	}
	source := filepath.Join(directory, "tfplan")
	content := []byte("snapshot without directory read permission")
	writePrivateFile(t, source, content)

	snapshot, snapshotErr := SnapshotStableFile(SnapshotStableFileOptions{
		SourcePath:       source,
		PrivateDirectory: snapshots,
		Budget:           mustReadBudget(t, smallReadLimits()),
	})
	// Restore read permission before assertions and test cleanup.
	if err := os.Chmod(snapshots, 0o700); err != nil {
		t.Fatalf("os.Chmod(%q, 0700) error = %v, want nil", snapshots, err)
	}
	if snapshotErr != nil {
		t.Fatalf("SnapshotStableFile(privateDirectory mode 0300) error = %v, want nil", snapshotErr)
	}
	got, err := os.ReadFile(snapshot.Path)
	if err != nil {
		t.Fatalf("os.ReadFile(%q) error = %v, want nil", snapshot.Path, err)
	}
	if !bytes.Equal(got, content) {
		t.Errorf("snapshot bytes = %q, want %q", got, content)
	}
}

func TestSnapshotNoSearchPrivateDirectoryPreservesNodeCreateFailure(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses owner directory search-permission checks")
	}
	for _, mode := range []os.FileMode{0o600, 0o400, 0o200, 0o000} {
		t.Run(fmt.Sprintf("mode_%04o", mode), func(t *testing.T) {
			directory := privateTemporaryDirectory(t)
			snapshots := filepath.Join(directory, "snapshots")
			if err := os.Mkdir(snapshots, 0o700); err != nil {
				t.Fatalf("os.Mkdir(%q, 0700) error = %v, want nil", snapshots, err)
			}
			if err := os.Chmod(snapshots, mode); err != nil {
				t.Fatalf("os.Chmod(%q, %#o) error = %v, want nil", snapshots, mode, err)
			}
			source := filepath.Join(directory, "tfplan")
			writePrivateFile(t, source, []byte("source must not be consumed"))
			budget := mustReadBudget(t, smallReadLimits())

			_, snapshotErr := SnapshotStableFile(SnapshotStableFileOptions{
				SourcePath:       source,
				PrivateDirectory: snapshots,
				Budget:           budget,
			})
			// Restore search permission before filesystem assertions and cleanup.
			if err := os.Chmod(snapshots, 0o700); err != nil {
				t.Fatalf("os.Chmod(%q, 0700) error = %v, want nil", snapshots, err)
			}
			failure := requireFailure(t, snapshotErr, "SNAPSHOT_FAILED", procerr.CategoryIO)
			if failure.Message != "unable to create plan snapshot" {
				t.Errorf("SNAPSHOT_FAILED message = %q, want %q", failure.Message, "unable to create plan snapshot")
			}
			if budget.Files() != 0 || budget.Bytes().Sign() != 0 {
				t.Errorf("no-search directory failure charged budget: files %d, bytes %v; want 0, 0", budget.Files(), budget.Bytes())
			}
			requireEmptyDirectory(t, snapshots)
		})
	}
}

func TestSnapshotNoSearchUnsafeDirectoryPreservesNodePrivacyFailure(t *testing.T) {
	directory := privateTemporaryDirectory(t)
	snapshots := filepath.Join(directory, "snapshots")
	if err := os.Mkdir(snapshots, 0o700); err != nil {
		t.Fatalf("os.Mkdir(%q, 0700) error = %v, want nil", snapshots, err)
	}
	if err := os.Chmod(snapshots, 0o220); err != nil {
		t.Fatalf("os.Chmod(%q, 0220) error = %v, want nil", snapshots, err)
	}
	source := filepath.Join(directory, "tfplan")
	writePrivateFile(t, source, []byte("source must not be consumed"))
	budget := mustReadBudget(t, smallReadLimits())

	_, snapshotErr := SnapshotStableFile(SnapshotStableFileOptions{
		SourcePath:       source,
		PrivateDirectory: snapshots,
		Budget:           budget,
	})
	if err := os.Chmod(snapshots, 0o700); err != nil {
		t.Fatalf("os.Chmod(%q, 0700) error = %v, want nil", snapshots, err)
	}
	failure := requireFailure(t, snapshotErr, "UNSAFE_SNAPSHOT_DIRECTORY", procerr.CategoryIO)
	if failure.Message != "snapshot directory is not private" {
		t.Errorf("UNSAFE_SNAPSHOT_DIRECTORY message = %q, want %q", failure.Message, "snapshot directory is not private")
	}
	if budget.Files() != 0 || budget.Bytes().Sign() != 0 {
		t.Errorf("unsafe no-search directory failure charged budget: files %d, bytes %v; want 0, 0", budget.Files(), budget.Bytes())
	}
	requireEmptyDirectory(t, snapshots)
}

func TestSnapshotEntropyFailureIsCatchable(t *testing.T) {
	directory := privateTemporaryDirectory(t)
	snapshots := filepath.Join(directory, "snapshots")
	if err := os.Mkdir(snapshots, 0o700); err != nil {
		t.Fatalf("os.Mkdir(%q, 0700) error = %v, want nil", snapshots, err)
	}
	source := filepath.Join(directory, "tfplan")
	writePrivateFile(t, source, []byte("source must not be consumed"))
	budget := mustReadBudget(t, smallReadLimits())

	originalReader := cryptorand.Reader
	cryptorand.Reader = failedEntropyReader{err: errors.New("forced entropy failure")}
	defer func() { cryptorand.Reader = originalReader }()

	_, err := SnapshotStableFile(SnapshotStableFileOptions{
		SourcePath:       source,
		PrivateDirectory: snapshots,
		Budget:           budget,
	})
	failure := requireFailure(t, err, "SNAPSHOT_FAILED", procerr.CategoryIO)
	if failure.Message != "unable to create plan snapshot" {
		t.Errorf("entropy failure message = %q, want %q", failure.Message, "unable to create plan snapshot")
	}
	if budget.Files() != 0 || budget.Bytes().Sign() != 0 {
		t.Errorf("entropy failure charged budget: files %d, bytes %v; want 0, 0", budget.Files(), budget.Bytes())
	}
	requireEmptyDirectory(t, snapshots)
}

func TestSnapshotBindsNormalizedDirectoryAcrossSymlinkDotDot(t *testing.T) {
	for _, test := range []struct {
		name            string
		normalizedMode  os.FileMode
		resolvedMode    os.FileMode
		wantFailureCode string
	}{
		{
			name:            "rejects unsafe normalized destination despite safe raw resolution",
			normalizedMode:  0o755,
			resolvedMode:    0o700,
			wantFailureCode: "UNSAFE_SNAPSHOT_DIRECTORY",
		},
		{
			name:           "accepts safe normalized destination despite unsafe raw resolution",
			normalizedMode: 0o700,
			resolvedMode:   0o755,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			directory := privateTemporaryDirectory(t)
			normalized := filepath.Join(directory, "snapshots")
			alternate := filepath.Join(directory, "alternate")
			resolved := filepath.Join(alternate, "snapshots")
			linkTarget := filepath.Join(alternate, "child")
			for _, specification := range []struct {
				path string
				mode os.FileMode
			}{
				{path: normalized, mode: test.normalizedMode},
				{path: alternate, mode: 0o700},
				{path: resolved, mode: test.resolvedMode},
				{path: linkTarget, mode: 0o700},
			} {
				if err := os.Mkdir(specification.path, specification.mode); err != nil {
					t.Fatalf("os.Mkdir(%q, %#o) error = %v, want nil", specification.path, specification.mode, err)
				}
				if err := os.Chmod(specification.path, specification.mode); err != nil {
					t.Fatalf("os.Chmod(%q, %#o) error = %v, want nil", specification.path, specification.mode, err)
				}
			}
			link := filepath.Join(directory, "link")
			if err := os.Symlink(linkTarget, link); err != nil {
				t.Fatalf("os.Symlink(%q, %q) error = %v, want nil", linkTarget, link, err)
			}
			spelling := link + string(filepath.Separator) + ".." + string(filepath.Separator) + "snapshots"
			if filepath.Clean(spelling) != normalized {
				t.Fatalf("filepath.Clean(%q) = %q, want %q", spelling, filepath.Clean(spelling), normalized)
			}
			source := filepath.Join(directory, "tfplan")
			content := []byte("normalized destination bytes")
			writePrivateFile(t, source, content)

			snapshot, err := SnapshotStableFile(SnapshotStableFileOptions{
				SourcePath:       source,
				PrivateDirectory: spelling,
				Budget:           mustReadBudget(t, smallReadLimits()),
			})
			if test.wantFailureCode != "" {
				requireFailure(t, err, test.wantFailureCode, procerr.CategoryIO)
				requireEmptyDirectory(t, normalized)
				requireEmptyDirectory(t, resolved)
				return
			}
			if err != nil {
				t.Fatalf("SnapshotStableFile(privateDirectory=%q) error = %v, want nil", spelling, err)
			}
			if filepath.Dir(snapshot.Path) != normalized {
				t.Errorf("snapshot directory = %q, want normalized destination %q", filepath.Dir(snapshot.Path), normalized)
			}
			requireEmptyDirectory(t, resolved)
		})
	}
}

func TestUnsafeSnapshotDirectoriesFailClosed(t *testing.T) {
	directory := privateTemporaryDirectory(t)
	source := filepath.Join(directory, "source")
	writePrivateFile(t, source, []byte("secret"))
	if err := os.Chmod(directory, 0o755); err != nil {
		t.Fatalf("os.Chmod(%q, 0755) error = %v, want nil", directory, err)
	}
	_, err := SnapshotStableFile(SnapshotStableFileOptions{
		SourcePath:       source,
		PrivateDirectory: directory,
		Budget:           mustReadBudget(t, smallReadLimits()),
	})
	requireFailure(t, err, "UNSAFE_SNAPSHOT_DIRECTORY", procerr.CategoryIO)
	if err := os.Chmod(directory, 0o700); err != nil {
		t.Fatalf("os.Chmod(%q, 0700) error = %v, want nil", directory, err)
	}

	realDirectory := filepath.Join(directory, "real")
	linkDirectory := filepath.Join(directory, "link")
	if err := os.Mkdir(realDirectory, 0o700); err != nil {
		t.Fatalf("os.Mkdir(%q, 0700) error = %v, want nil", realDirectory, err)
	}
	if err := os.Symlink(realDirectory, linkDirectory); err != nil {
		t.Fatalf("os.Symlink(%q, %q) error = %v, want nil", realDirectory, linkDirectory, err)
	}
	_, err = SnapshotStableFile(SnapshotStableFileOptions{
		SourcePath:       source,
		PrivateDirectory: linkDirectory,
		Budget:           mustReadBudget(t, smallReadLimits()),
	})
	requireFailure(t, err, "UNSAFE_SNAPSHOT_DIRECTORY", procerr.CategoryIO)
}

func TestSnapshotDirectoryInspectionPreservesNodeFinalAndAncestorClassification(t *testing.T) {
	directory := privateTemporaryDirectory(t)
	source := filepath.Join(directory, "source")
	writePrivateFile(t, source, []byte("source must not be consumed"))
	regular := filepath.Join(directory, "regular")
	writePrivateFile(t, regular, []byte("not a directory"))
	realDirectory := filepath.Join(directory, "real")
	if err := os.Mkdir(realDirectory, 0o700); err != nil {
		t.Fatalf("os.Mkdir(%q, 0700) error = %v, want nil", realDirectory, err)
	}
	finalSymlink := filepath.Join(directory, "final-link")
	if err := os.Symlink(realDirectory, finalSymlink); err != nil {
		t.Fatalf("os.Symlink(%q, %q) error = %v, want nil", realDirectory, finalSymlink, err)
	}
	loop := filepath.Join(directory, "loop")
	if err := os.Symlink("loop", loop); err != nil {
		t.Fatalf("os.Symlink(loop, %q) error = %v, want nil", loop, err)
	}
	fifo := filepath.Join(directory, "fifo")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatalf("syscall.Mkfifo(%q, 0600) error = %v, want nil", fifo, err)
	}

	for _, test := range []struct {
		name        string
		path        string
		code        string
		wantMessage string
	}{
		{
			name:        "final regular file",
			path:        regular,
			code:        "UNSAFE_SNAPSHOT_DIRECTORY",
			wantMessage: "snapshot directory is not private",
		},
		{
			name:        "final symlink",
			path:        finalSymlink,
			code:        "UNSAFE_SNAPSHOT_DIRECTORY",
			wantMessage: "snapshot directory is not private",
		},
		{
			name:        "final FIFO",
			path:        fifo,
			code:        "UNSAFE_SNAPSHOT_DIRECTORY",
			wantMessage: "snapshot directory is not private",
		},
		{
			name:        "regular file with trailing separator",
			path:        regular + string(filepath.Separator),
			code:        "SNAPSHOT_FAILED",
			wantMessage: "unable to inspect snapshot directory",
		},
		{
			name:        "regular file ancestor",
			path:        filepath.Join(regular, "child"),
			code:        "SNAPSHOT_FAILED",
			wantMessage: "unable to inspect snapshot directory",
		},
		{
			name:        "FIFO ancestor",
			path:        filepath.Join(fifo, "child"),
			code:        "SNAPSHOT_FAILED",
			wantMessage: "unable to inspect snapshot directory",
		},
		{
			name:        "looping symlink ancestor",
			path:        filepath.Join(loop, "child"),
			code:        "SNAPSHOT_FAILED",
			wantMessage: "unable to inspect snapshot directory",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			budget := mustReadBudget(t, smallReadLimits())
			_, err := SnapshotStableFile(SnapshotStableFileOptions{
				SourcePath:       source,
				PrivateDirectory: test.path,
				Budget:           budget,
			})
			failure := requireFailure(t, err, test.code, procerr.CategoryIO)
			if failure.Message != test.wantMessage {
				t.Errorf("failure message = %q, want %q", failure.Message, test.wantMessage)
			}
			if budget.Files() != 0 || budget.Bytes().Sign() != 0 {
				t.Errorf("directory inspection failure charged budget: files %d, bytes %v; want 0, 0", budget.Files(), budget.Bytes())
			}
		})
	}
}

func TestSnapshotRejectsTrailingSymlinkDirectorySpellings(t *testing.T) {
	directory := privateTemporaryDirectory(t)
	realDirectory := filepath.Join(directory, "real")
	linkDirectory := filepath.Join(directory, "link")
	if err := os.Mkdir(realDirectory, 0o700); err != nil {
		t.Fatalf("os.Mkdir(%q, 0700) error = %v, want nil", realDirectory, err)
	}
	if err := os.Symlink(realDirectory, linkDirectory); err != nil {
		t.Fatalf("os.Symlink(%q, %q) error = %v, want nil", realDirectory, linkDirectory, err)
	}
	source := filepath.Join(directory, "source")
	writePrivateFile(t, source, []byte("snapshot bytes"))

	for _, spelling := range []string{
		linkDirectory + string(filepath.Separator),
		filepath.Join(linkDirectory, ".") + string(filepath.Separator) + ".",
	} {
		_, err := SnapshotStableFile(SnapshotStableFileOptions{
			SourcePath:       source,
			PrivateDirectory: spelling,
			Budget:           mustReadBudget(t, smallReadLimits()),
		})
		requireFailure(t, err, "UNSAFE_SNAPSHOT_DIRECTORY", procerr.CategoryIO)
	}
	requireEmptyDirectory(t, realDirectory)
}

func TestSnapshotAllowsDotSpellingForRealPrivateDirectory(t *testing.T) {
	directory := privateTemporaryDirectory(t)
	snapshots := filepath.Join(directory, "snapshots")
	if err := os.Mkdir(snapshots, 0o700); err != nil {
		t.Fatalf("os.Mkdir(%q, 0700) error = %v, want nil", snapshots, err)
	}
	source := filepath.Join(directory, "source")
	writePrivateFile(t, source, []byte("snapshot bytes"))
	snapshot, err := SnapshotStableFile(SnapshotStableFileOptions{
		SourcePath:       source,
		PrivateDirectory: snapshots + string(filepath.Separator) + ".",
		Budget:           mustReadBudget(t, smallReadLimits()),
	})
	if err != nil {
		t.Fatalf("SnapshotStableFile(privateDirectory=%q) error = %v, want nil", snapshots+string(filepath.Separator)+".", err)
	}
	if filepath.Dir(snapshot.Path) != snapshots {
		t.Errorf("SnapshotStableFile(...).Path directory = %q, want normalized %q", filepath.Dir(snapshot.Path), snapshots)
	}
}

func TestSnapshotRejectsDirectorySwapAfterInitialIdentityCheckWithoutCreatingFile(t *testing.T) {
	directory := privateTemporaryDirectory(t)
	snapshots := filepath.Join(directory, "snapshots")
	moved := filepath.Join(directory, "snapshots-moved")
	victim := filepath.Join(directory, "victim")
	if err := os.Mkdir(snapshots, 0o700); err != nil {
		t.Fatalf("os.Mkdir(%q, 0700) error = %v, want nil", snapshots, err)
	}
	if err := os.Mkdir(victim, 0o700); err != nil {
		t.Fatalf("os.Mkdir(%q, 0700) error = %v, want nil", victim, err)
	}
	source := filepath.Join(directory, "source")
	writePrivateFile(t, source, []byte("snapshot bytes"))

	_, err := snapshotStableFile(SnapshotStableFileOptions{
		SourcePath:       source,
		PrivateDirectory: snapshots,
		Budget:           mustReadBudget(t, smallReadLimits()),
	}, privateDirectoryHooks{
		afterLstat: func(string) error {
			if err := os.Rename(snapshots, moved); err != nil {
				return err
			}
			return os.Symlink(victim, snapshots)
		},
	})
	requireFailure(t, err, "UNSAFE_SNAPSHOT_DIRECTORY", procerr.CategoryIO)
	requireEmptyDirectory(t, moved)
	requireEmptyDirectory(t, victim)
}

func TestSnapshotDirectoryFIFOReplacementDoesNotBlock(t *testing.T) {
	const childEnvironment = "INFRAWRIGHT_TEST_SNAPSHOT_FIFO_CHILD"
	if os.Getenv(childEnvironment) != "1" {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		command := exec.CommandContext(
			ctx,
			os.Args[0],
			"-test.run=^TestSnapshotDirectoryFIFOReplacementDoesNotBlock$",
			"-test.count=1",
		)
		command.Env = append(os.Environ(), childEnvironment+"=1")
		output, err := command.CombinedOutput()
		if ctx.Err() != nil {
			t.Fatalf("snapshot directory FIFO replacement child timed out: %v\n%s", ctx.Err(), output)
		}
		if err != nil {
			t.Fatalf("snapshot directory FIFO replacement child error = %v, want nil\n%s", err, output)
		}
		return
	}

	directory := privateTemporaryDirectory(t)
	snapshots := filepath.Join(directory, "snapshots")
	moved := filepath.Join(directory, "snapshots-moved")
	if err := os.Mkdir(snapshots, 0o700); err != nil {
		t.Fatalf("os.Mkdir(%q, 0700) error = %v, want nil", snapshots, err)
	}
	source := filepath.Join(directory, "source")
	writePrivateFile(t, source, []byte("snapshot bytes"))

	_, err := snapshotStableFile(SnapshotStableFileOptions{
		SourcePath:       source,
		PrivateDirectory: snapshots,
		Budget:           mustReadBudget(t, smallReadLimits()),
	}, privateDirectoryHooks{
		afterLstat: func(string) error {
			if err := os.Rename(snapshots, moved); err != nil {
				return err
			}
			return syscall.Mkfifo(snapshots, 0o600)
		},
	})
	requireFailure(t, err, "UNSAFE_SNAPSHOT_DIRECTORY", procerr.CategoryIO)
	requireEmptyDirectory(t, moved)
}

func TestSnapshotRejectsDirectorySwapAfterDescriptorBindingWithoutCreatingFile(t *testing.T) {
	directory := privateTemporaryDirectory(t)
	snapshots := filepath.Join(directory, "snapshots")
	moved := filepath.Join(directory, "snapshots-moved")
	victim := filepath.Join(directory, "victim")
	if err := os.Mkdir(snapshots, 0o700); err != nil {
		t.Fatalf("os.Mkdir(%q, 0700) error = %v, want nil", snapshots, err)
	}
	if err := os.Mkdir(victim, 0o700); err != nil {
		t.Fatalf("os.Mkdir(%q, 0700) error = %v, want nil", victim, err)
	}
	source := filepath.Join(directory, "source")
	writePrivateFile(t, source, []byte("snapshot bytes"))

	_, err := snapshotStableFile(SnapshotStableFileOptions{
		SourcePath:       source,
		PrivateDirectory: snapshots,
		Budget:           mustReadBudget(t, smallReadLimits()),
	}, privateDirectoryHooks{
		afterBind: func(string, *privateDirectoryRoot) error {
			if err := os.Rename(snapshots, moved); err != nil {
				return err
			}
			return os.Symlink(victim, snapshots)
		},
	})
	requireFailure(t, err, "UNSAFE_SNAPSHOT_DIRECTORY", procerr.CategoryIO)
	requireEmptyDirectory(t, moved)
	requireEmptyDirectory(t, victim)
}

func TestSnapshotDirectorySwapImmediatelyBeforeCreateCannotRedirectCreation(t *testing.T) {
	directory := privateTemporaryDirectory(t)
	snapshots := filepath.Join(directory, "snapshots")
	moved := filepath.Join(directory, "snapshots-moved")
	victim := filepath.Join(directory, "victim")
	if err := os.Mkdir(snapshots, 0o700); err != nil {
		t.Fatalf("os.Mkdir(%q, 0700) error = %v, want nil", snapshots, err)
	}
	if err := os.Mkdir(victim, 0o700); err != nil {
		t.Fatalf("os.Mkdir(%q, 0700) error = %v, want nil", victim, err)
	}
	source := filepath.Join(directory, "source")
	writePrivateFile(t, source, []byte("sensitive snapshot bytes"))

	_, err := snapshotStableFile(SnapshotStableFileOptions{
		SourcePath:       source,
		PrivateDirectory: snapshots,
		Budget:           mustReadBudget(t, smallReadLimits()),
	}, privateDirectoryHooks{
		beforeCreate: func(string, *privateDirectoryRoot) error {
			if err := os.Rename(snapshots, moved); err != nil {
				return err
			}
			return os.Symlink(victim, snapshots)
		},
	})
	requireFailure(t, err, "UNSAFE_SNAPSHOT_DIRECTORY", procerr.CategoryIO)
	requireEmptyDirectory(t, victim)
	entries, err := os.ReadDir(moved)
	if err != nil {
		t.Fatalf("os.ReadDir(%q) error = %v, want nil", moved, err)
	}
	if len(entries) != 1 {
		t.Fatalf("os.ReadDir(%q) entry count = %d, want 1 scrubbed descriptor-bound snapshot", moved, len(entries))
	}
	info, err := os.Stat(filepath.Join(moved, entries[0].Name()))
	if err != nil {
		t.Fatalf("os.Stat(bound snapshot %q) error = %v, want nil", entries[0].Name(), err)
	}
	if info.Size() != 0 {
		t.Errorf("scrubbed descriptor-bound snapshot size = %d, want 0", info.Size())
	}
}

func TestSnapshotRejectsDirectoryIdentityReplacementAfterDescriptorBinding(t *testing.T) {
	directory := privateTemporaryDirectory(t)
	snapshots := filepath.Join(directory, "snapshots")
	moved := filepath.Join(directory, "snapshots-moved")
	if err := os.Mkdir(snapshots, 0o700); err != nil {
		t.Fatalf("os.Mkdir(%q, 0700) error = %v, want nil", snapshots, err)
	}
	source := filepath.Join(directory, "source")
	writePrivateFile(t, source, []byte("snapshot bytes"))

	_, err := snapshotStableFile(SnapshotStableFileOptions{
		SourcePath:       source,
		PrivateDirectory: snapshots,
		Budget:           mustReadBudget(t, smallReadLimits()),
	}, privateDirectoryHooks{
		afterBind: func(string, *privateDirectoryRoot) error {
			if err := os.Rename(snapshots, moved); err != nil {
				return err
			}
			return os.Mkdir(snapshots, 0o700)
		},
	})
	requireFailure(t, err, "UNSAFE_SNAPSHOT_DIRECTORY", procerr.CategoryIO)
	requireEmptyDirectory(t, moved)
	requireEmptyDirectory(t, snapshots)
}

func TestSnapshotParentReplacementIntentionallyPrecedesNodeChildClassification(t *testing.T) {
	for _, kind := range []string{"missing", "symlink", "fifo", "directory", "regular"} {
		t.Run(kind, func(t *testing.T) {
			directory := privateTemporaryDirectory(t)
			snapshots := filepath.Join(directory, "snapshots")
			moved := filepath.Join(directory, "snapshots-moved")
			victim := filepath.Join(directory, "victim")
			target := filepath.Join(directory, "target")
			if err := os.Mkdir(snapshots, 0o700); err != nil {
				t.Fatalf("os.Mkdir(%q, 0700) error = %v, want nil", snapshots, err)
			}
			if err := os.Mkdir(victim, 0o700); err != nil {
				t.Fatalf("os.Mkdir(%q, 0700) error = %v, want nil", victim, err)
			}
			writePrivateFile(t, target, []byte("target survives"))
			source := filepath.Join(directory, "source")
			writePrivateFile(t, source, []byte("sensitive snapshot bytes"))
			var snapshotName string

			// The exact Node v24.15 source-bound oracle returns FILE_CHANGED for
			// missing/symlink/FIFO/directory children and SNAPSHOT_PATH_CHANGED for
			// a regular replacement. Go intentionally returns
			// UNSAFE_SNAPSHOT_DIRECTORY first in every row because its additional
			// bound-parent revalidation detects the compound parent swap.
			_, err := SnapshotStableFile(SnapshotStableFileOptions{
				SourcePath:       source,
				PrivateDirectory: snapshots,
				Budget:           mustReadBudget(t, smallReadLimits()),
				ReadOptions: StableReadOptions{
					Hooks: StableReadHooks{
						BeforeFinalStat: func() error {
							entries, err := os.ReadDir(snapshots)
							if err != nil {
								return err
							}
							if len(entries) != 1 {
								return fmt.Errorf("snapshot entry count = %d, want 1", len(entries))
							}
							snapshotName = entries[0].Name()
							if err := os.Rename(snapshots, moved); err != nil {
								return err
							}
							if err := os.Symlink(victim, snapshots); err != nil {
								return err
							}
							replacement := filepath.Join(victim, snapshotName)
							switch kind {
							case "missing":
								return nil
							case "symlink":
								return os.Symlink(target, replacement)
							case "fifo":
								return syscall.Mkfifo(replacement, 0o600)
							case "directory":
								return os.Mkdir(replacement, 0o700)
							case "regular":
								return os.WriteFile(replacement, []byte("replacement survives"), 0o600)
							default:
								return fmt.Errorf("unknown replacement kind %q", kind)
							}
						},
					},
				},
			})
			requireFailure(t, err, "UNSAFE_SNAPSHOT_DIRECTORY", procerr.CategoryIO)
			if snapshotName == "" {
				t.Fatal("snapshot name is empty after parent-replacement hook")
			}
			info, err := os.Stat(filepath.Join(moved, snapshotName))
			if err != nil {
				t.Fatalf("os.Stat(scrubbed snapshot %q) error = %v, want nil", snapshotName, err)
			}
			if info.Size() != 0 {
				t.Errorf("scrubbed bound snapshot size = %d, want 0", info.Size())
			}
			targetBytes, err := os.ReadFile(target)
			if err != nil {
				t.Fatalf("os.ReadFile(%q) error = %v, want nil", target, err)
			}
			if string(targetBytes) != "target survives" {
				t.Errorf("symlink target bytes = %q, want %q", targetBytes, "target survives")
			}
			replacement := filepath.Join(victim, snapshotName)
			switch kind {
			case "missing":
				if _, err := os.Lstat(replacement); !errors.Is(err, os.ErrNotExist) {
					t.Errorf("os.Lstat(%q) error = %v, want os.ErrNotExist", replacement, err)
				}
			case "symlink":
				info, err := os.Lstat(replacement)
				if err != nil || info.Mode()&os.ModeSymlink == 0 {
					t.Errorf("os.Lstat(%q) = (%v, %v), want preserved symlink", replacement, info, err)
				}
			case "fifo":
				info, err := os.Lstat(replacement)
				if err != nil || info.Mode()&os.ModeNamedPipe == 0 {
					t.Errorf("os.Lstat(%q) = (%v, %v), want preserved FIFO", replacement, info, err)
				}
			case "directory":
				info, err := os.Lstat(replacement)
				if err != nil || !info.IsDir() {
					t.Errorf("os.Lstat(%q) = (%v, %v), want preserved directory", replacement, info, err)
				}
			case "regular":
				replacementBytes, err := os.ReadFile(replacement)
				if err != nil {
					t.Fatalf("os.ReadFile(%q) error = %v, want nil", replacement, err)
				}
				if string(replacementBytes) != "replacement survives" {
					t.Errorf("replacement bytes = %q, want %q", replacementBytes, "replacement survives")
				}
			}
		})
	}
}

func TestPartialSnapshotFailureScrubsDescriptorWithoutFollowingSwappedParent(t *testing.T) {
	directory := privateTemporaryDirectory(t)
	snapshots := filepath.Join(directory, "snapshots")
	moved := filepath.Join(directory, "snapshots-moved")
	victimDirectory := filepath.Join(directory, "victim")
	if err := os.Mkdir(snapshots, 0o700); err != nil {
		t.Fatalf("os.Mkdir(%q, 0700) error = %v, want nil", snapshots, err)
	}
	if err := os.Mkdir(victimDirectory, 0o700); err != nil {
		t.Fatalf("os.Mkdir(%q, 0700) error = %v, want nil", victimDirectory, err)
	}
	source := filepath.Join(directory, "tfplan")
	writePrivateFile(t, source, []byte("secret partial snapshot bytes"))
	var snapshotName string
	_, err := SnapshotStableFile(SnapshotStableFileOptions{
		SourcePath:       source,
		PrivateDirectory: snapshots,
		Budget:           mustReadBudget(t, smallReadLimits()),
		ReadOptions: StableReadOptions{
			Hooks: StableReadHooks{
				AfterOpen: func() error {
					names, readErr := os.ReadDir(snapshots)
					if readErr != nil {
						return readErr
					}
					if len(names) != 1 {
						return errors.New("snapshot directory did not contain exactly one partial file")
					}
					snapshotName = names[0].Name()
					if err := os.Rename(snapshots, moved); err != nil {
						return err
					}
					if err := os.Symlink(victimDirectory, snapshots); err != nil {
						return err
					}
					if err := os.WriteFile(
						filepath.Join(victimDirectory, snapshotName),
						[]byte("victim must survive"),
						0o600,
					); err != nil {
						return err
					}
					return errors.New("force snapshot copy failure")
				},
			},
		},
	})
	requireFailure(t, err, "READ_FAILED", procerr.CategoryIO)
	victim, err := os.ReadFile(filepath.Join(victimDirectory, snapshotName))
	if err != nil {
		t.Fatalf("os.ReadFile(victim %q) error = %v, want nil", snapshotName, err)
	}
	if string(victim) != "victim must survive" {
		t.Errorf("victim bytes = %q, want %q", victim, "victim must survive")
	}
	partialInfo, err := os.Stat(filepath.Join(moved, snapshotName))
	if err != nil {
		t.Fatalf("os.Stat(moved partial %q) error = %v, want nil", snapshotName, err)
	}
	if partialInfo.Size() != 0 {
		t.Errorf("moved partial snapshot size = %d, want 0", partialInfo.Size())
	}
}

func TestSnapshotRejectsIdenticalBytePathReplacementBeforeReturn(t *testing.T) {
	directory := privateTemporaryDirectory(t)
	snapshots := filepath.Join(directory, "snapshots")
	if err := os.Mkdir(snapshots, 0o700); err != nil {
		t.Fatalf("os.Mkdir(%q, 0700) error = %v, want nil", snapshots, err)
	}
	source := filepath.Join(directory, "tfplan")
	content := []byte("secret snapshot bytes")
	writePrivateFile(t, source, content)
	var moved string
	var replacement string
	_, err := SnapshotStableFile(SnapshotStableFileOptions{
		SourcePath:       source,
		PrivateDirectory: snapshots,
		Budget:           mustReadBudget(t, smallReadLimits()),
		ReadOptions: StableReadOptions{
			Hooks: StableReadHooks{
				BeforeFinalStat: func() error {
					names, readErr := os.ReadDir(snapshots)
					if readErr != nil {
						return readErr
					}
					if len(names) != 1 {
						return errors.New("snapshot directory did not contain exactly one partial file")
					}
					replacement = filepath.Join(snapshots, names[0].Name())
					moved = replacement + ".original"
					if err := os.Rename(replacement, moved); err != nil {
						return err
					}
					return copyFile(replacement, moved)
				},
			},
		},
	})
	requireFailure(t, err, "SNAPSHOT_PATH_CHANGED", procerr.CategoryIO)
	movedInfo, err := os.Stat(moved)
	if err != nil {
		t.Fatalf("os.Stat(%q) error = %v, want nil", moved, err)
	}
	if movedInfo.Size() != 0 {
		t.Errorf("original bound snapshot size after cleanup = %d, want 0", movedInfo.Size())
	}
	replacementBytes, err := os.ReadFile(replacement)
	if err != nil {
		t.Fatalf("os.ReadFile(%q) error = %v, want nil", replacement, err)
	}
	if !bytes.Equal(replacementBytes, content) {
		t.Errorf("replacement snapshot bytes = %q, want %q", replacementBytes, content)
	}
}

func TestSnapshotPreservesNodeFailureForMissingOrNonRegularDestinationPath(t *testing.T) {
	tests := []string{"missing", "symlink", "fifo", "directory"}
	for _, name := range tests {
		t.Run(name, func(t *testing.T) {
			directory := privateTemporaryDirectory(t)
			snapshots := filepath.Join(directory, "snapshots")
			if err := os.Mkdir(snapshots, 0o700); err != nil {
				t.Fatalf("os.Mkdir(%q, 0700) error = %v, want nil", snapshots, err)
			}
			source := filepath.Join(directory, "tfplan")
			content := []byte("secret snapshot bytes")
			writePrivateFile(t, source, content)
			victim := filepath.Join(directory, "victim")
			writePrivateFile(t, victim, []byte("victim must survive"))
			var (
				snapshotPath string
				moved        string
			)

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
							snapshotPath = path
							moved = snapshotPath + ".original"
							if err := os.Rename(snapshotPath, moved); err != nil {
								return err
							}
							switch name {
							case "missing":
								return nil
							case "symlink":
								return os.Symlink(victim, snapshotPath)
							case "fifo":
								return syscall.Mkfifo(snapshotPath, 0o600)
							case "directory":
								return os.Mkdir(snapshotPath, 0o700)
							default:
								return errors.New("unknown destination replacement kind")
							}
						},
					},
				},
			})
			// Node's generic pathIdentity helper owns this exact classification:
			// missing, symlink, and non-regular destination paths are FILE_CHANGED;
			// a regular different identity reaches SNAPSHOT_PATH_CHANGED instead.
			requireFailure(t, err, "FILE_CHANGED", procerr.CategoryIO)
			movedInfo, err := os.Stat(moved)
			if err != nil {
				t.Fatalf("os.Stat(%q) error = %v, want nil", moved, err)
			}
			if movedInfo.Size() != 0 {
				t.Errorf("bound original size after %s replacement cleanup = %d, want 0", name, movedInfo.Size())
			}
			switch name {
			case "missing":
				if _, err := os.Lstat(snapshotPath); !errors.Is(err, os.ErrNotExist) {
					t.Errorf("os.Lstat(missing replacement %q) error = %v, want os.ErrNotExist", snapshotPath, err)
				}
			case "symlink":
				victimBytes, err := os.ReadFile(victim)
				if err != nil {
					t.Fatalf("os.ReadFile(%q) error = %v, want nil", victim, err)
				}
				if string(victimBytes) != "victim must survive" {
					t.Errorf("symlink victim bytes = %q, want untouched", victimBytes)
				}
			case "fifo":
				info, err := os.Lstat(snapshotPath)
				if err != nil {
					t.Fatalf("os.Lstat(%q) error = %v, want nil", snapshotPath, err)
				}
				if info.Mode()&os.ModeNamedPipe == 0 {
					t.Errorf("replacement mode = %v, want named pipe", info.Mode())
				}
			case "directory":
				requireEmptyDirectory(t, snapshotPath)
			}
		})
	}
}

func TestSnapshotPreservesNodeFailurePrecedenceForStableParentCompoundDestinationRaces(t *testing.T) {
	mutations := []struct {
		name   string
		mutate func(path string, size int) error
	}{
		{
			name: "same_size_overwrite",
			mutate: func(path string, size int) error {
				return os.WriteFile(path, bytes.Repeat([]byte{'x'}, size), 0o600)
			},
		},
		{
			name: "truncate",
			mutate: func(path string, _ int) error {
				return os.Truncate(path, 0)
			},
		},
		{
			name: "chmod",
			mutate: func(path string, _ int) error {
				return os.Chmod(path, 0o400)
			},
		},
	}
	replacements := []struct {
		name     string
		wantCode string
	}{
		{name: "missing", wantCode: "FILE_CHANGED"},
		{name: "symlink", wantCode: "FILE_CHANGED"},
		{name: "fifo", wantCode: "FILE_CHANGED"},
		{name: "directory", wantCode: "FILE_CHANGED"},
		{name: "regular", wantCode: "SNAPSHOT_PATH_CHANGED"},
	}
	for _, mutation := range mutations {
		for _, replacement := range replacements {
			t.Run(mutation.name+"/"+replacement.name, func(t *testing.T) {
				directory := privateTemporaryDirectory(t)
				snapshots := filepath.Join(directory, "snapshots")
				if err := os.Mkdir(snapshots, 0o700); err != nil {
					t.Fatalf("os.Mkdir(%q, 0700) error = %v, want nil", snapshots, err)
				}
				source := filepath.Join(directory, "tfplan")
				content := []byte("secret snapshot bytes")
				writePrivateFile(t, source, content)
				victim := filepath.Join(directory, "victim")
				writePrivateFile(t, victim, []byte("victim must survive"))
				var (
					snapshotPath string
					moved        string
				)

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
								snapshotPath = path
								moved = snapshotPath + ".original"
								if err := os.Rename(snapshotPath, moved); err != nil {
									return err
								}
								if err := mutation.mutate(moved, len(content)); err != nil {
									return err
								}
								switch replacement.name {
								case "missing":
									return nil
								case "symlink":
									return os.Symlink(victim, snapshotPath)
								case "fifo":
									return syscall.Mkfifo(snapshotPath, 0o600)
								case "directory":
									return os.Mkdir(snapshotPath, 0o700)
								case "regular":
									return os.WriteFile(snapshotPath, content, 0o600)
								default:
									return errors.New("unknown compound destination replacement kind")
								}
							},
						},
					},
				})
				requireFailure(t, err, replacement.wantCode, procerr.CategoryIO)
				movedInfo, err := os.Stat(moved)
				if err != nil {
					t.Fatalf("os.Stat(%q) error = %v, want nil", moved, err)
				}
				if movedInfo.Size() != 0 {
					t.Errorf("compound-race bound original size = %d, want 0", movedInfo.Size())
				}
				victimBytes, err := os.ReadFile(victim)
				if err != nil {
					t.Fatalf("os.ReadFile(%q) error = %v, want nil", victim, err)
				}
				if string(victimBytes) != "victim must survive" {
					t.Errorf("symlink victim bytes = %q, want untouched", victimBytes)
				}
				if replacement.name == "regular" {
					replacementBytes, err := os.ReadFile(snapshotPath)
					if err != nil {
						t.Fatalf("os.ReadFile(%q) error = %v, want nil", snapshotPath, err)
					}
					if !bytes.Equal(replacementBytes, content) {
						t.Errorf("regular replacement bytes = %q, want %q", replacementBytes, content)
					}
				}
			})
		}
	}
}

func TestSnapshotRejectsInPlaceDestinationMutationBeforeReturn(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(path string, size int) error
	}{
		{
			name: "same_size_overwrite",
			mutate: func(path string, size int) error {
				return os.WriteFile(path, bytes.Repeat([]byte{'x'}, size), 0o600)
			},
		},
		{
			name: "truncate",
			mutate: func(path string, _ int) error {
				return os.Truncate(path, 0)
			},
		},
		{
			name: "chmod",
			mutate: func(path string, _ int) error {
				return os.Chmod(path, 0o400)
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			directory := privateTemporaryDirectory(t)
			snapshots := filepath.Join(directory, "snapshots")
			if err := os.Mkdir(snapshots, 0o700); err != nil {
				t.Fatalf("os.Mkdir(%q, 0700) error = %v, want nil", snapshots, err)
			}
			source := filepath.Join(directory, "tfplan")
			content := []byte("secret snapshot bytes")
			writePrivateFile(t, source, content)
			var (
				snapshotPath      string
				identityPreserved bool
			)

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
							snapshotPath = path
							beforeInfo, err := os.Lstat(snapshotPath)
							if err != nil {
								return err
							}
							before, ok := platformMetadataIdentity(beforeInfo)
							if !ok {
								return errors.New("snapshot identity unavailable before mutation")
							}
							if err := test.mutate(snapshotPath, len(content)); err != nil {
								return err
							}
							afterInfo, err := os.Lstat(snapshotPath)
							if err != nil {
								return err
							}
							after, ok := platformMetadataIdentity(afterInfo)
							if !ok {
								return errors.New("snapshot identity unavailable after mutation")
							}
							identityPreserved = before.stableIdentity() == after.stableIdentity()
							if !identityPreserved {
								return errors.New("in-place mutation replaced snapshot identity")
							}
							return nil
						},
					},
				},
			})
			requireFailure(t, err, "SNAPSHOT_PATH_CHANGED", procerr.CategoryIO)
			if !identityPreserved {
				t.Errorf("%s did not exercise a same-identity mutation", test.name)
			}
			info, err := os.Stat(snapshotPath)
			if err != nil {
				t.Fatalf("os.Stat(%q) error = %v, want nil", snapshotPath, err)
			}
			if info.Size() != 0 {
				t.Errorf("bound snapshot size after %s cleanup = %d, want 0", test.name, info.Size())
			}
		})
	}
}

func TestSnapshotCleanupFailurePreservesProcessFailureAndAddsDetail(t *testing.T) {
	directory := privateTemporaryDirectory(t)
	filePath := filepath.Join(directory, "closed-destination")
	file, err := os.OpenFile(filePath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		t.Fatalf("os.OpenFile(%q) error = %v, want nil", filePath, err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("(*os.File).Close(%q) error = %v, want nil", filePath, err)
	}
	original := procerr.NewProcessFailure(procerr.NewProcessFailureOptions{
		Code:      "FILE_CHANGED",
		Category:  procerr.CategoryIO,
		Message:   "input file changed while it was read",
		Retryable: true,
		Details: []procerr.ErrorDetail{{
			Path:    "$.source",
			Code:    "SOURCE_DETAIL",
			Message: "source detail",
		}},
	})
	failure := requireFailure(
		t,
		scrubFailedSnapshot(file, original),
		"FILE_CHANGED",
		procerr.CategoryIO,
	)
	if !failure.Retryable {
		t.Errorf("cleanup-augmented ProcessFailure.Retryable = false, want true")
	}
	if len(failure.Details) != 2 {
		t.Fatalf("cleanup-augmented ProcessFailure.Details length = %d, want 2", len(failure.Details))
	}
	wantCleanup := procerr.ErrorDetail{
		Path:    "$",
		Code:    "SNAPSHOT_CLEANUP_FAILED",
		Message: "partial saved-plan snapshot cleanup also failed",
	}
	if failure.Details[1] != wantCleanup {
		t.Errorf("cleanup detail = %+v, want %+v", failure.Details[1], wantCleanup)
	}
	if len(original.Details) != 1 {
		t.Errorf("original ProcessFailure.Details length after augmentation = %d, want 1", len(original.Details))
	}
}

func TestSnapshotCleanupFailureMapsUnstructuredError(t *testing.T) {
	directory := privateTemporaryDirectory(t)
	filePath := filepath.Join(directory, "closed-destination")
	file, err := os.OpenFile(filePath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		t.Fatalf("os.OpenFile(%q) error = %v, want nil", filePath, err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("(*os.File).Close(%q) error = %v, want nil", filePath, err)
	}
	failure := requireFailure(
		t,
		scrubFailedSnapshot(file, errors.New("unstructured operation failure")),
		"SNAPSHOT_AND_CLEANUP_FAILED",
		procerr.CategoryIO,
	)
	if failure.Message != "unable to create or scrub the saved-plan snapshot" {
		t.Errorf("SNAPSHOT_AND_CLEANUP_FAILED message = %q, want %q", failure.Message, "unable to create or scrub the saved-plan snapshot")
	}
}

func copyFile(destination, source string) error {
	content, err := os.ReadFile(source)
	if err != nil {
		return err
	}
	defer clear(content)
	return os.WriteFile(destination, content, 0o600)
}

func singleSnapshotPath(directory string) (string, error) {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return "", err
	}
	if len(entries) != 1 {
		return "", errors.New("snapshot directory did not contain exactly one partial file")
	}
	return filepath.Join(directory, entries[0].Name()), nil
}

func requireEmptyDirectory(t *testing.T, directory string) {
	t.Helper()
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatalf("os.ReadDir(%q) error = %v, want nil", directory, err)
	}
	if len(entries) == 0 {
		return
	}
	names := make([]string, len(entries))
	for index, entry := range entries {
		names[index] = entry.Name()
	}
	t.Fatalf("os.ReadDir(%q) entries = %q, want empty directory", directory, names)
}
