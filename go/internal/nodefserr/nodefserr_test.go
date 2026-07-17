package nodefserr

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
)

func TestCallWrapLiteralSystemErrors(t *testing.T) {
	requireDarwinLinuxNativeErrno(t)

	// Node v24.15.0 commit 848430679556aed0bd073f2bc263331ad84fa119
	// pins the generic SystemError constructor in lib/internal/errors.js:637-672
	// and these operation spellings in lib/internal/fs/promises.js:513-560,
	// 782-868, 946-964, 1023-1047, and 1234-1288. The full Cartesian table
	// also pins constructor-format combinations that ordinary call sites do not
	// currently produce.
	tests := []struct {
		name string
		call Call
		err  error
		want string
	}{
		{name: "read_file_ENOENT", call: Call{Operation: ReadFile, Path: "/requested/path"}, err: syscall.ENOENT, want: "ENOENT: no such file or directory, open '/requested/path'"},
		{name: "read_file_EEXIST", call: Call{Operation: ReadFile, Path: "/requested/path"}, err: syscall.EEXIST, want: "EEXIST: file already exists, open '/requested/path'"},
		{name: "read_file_EACCES", call: Call{Operation: ReadFile, Path: "/requested/path"}, err: syscall.EACCES, want: "EACCES: permission denied, open '/requested/path'"},
		{name: "read_file_ENOTDIR", call: Call{Operation: ReadFile, Path: "/requested/path"}, err: syscall.ENOTDIR, want: "ENOTDIR: not a directory, open '/requested/path'"},
		{name: "read_file_EISDIR", call: Call{Operation: ReadFile, Path: "/requested/path"}, err: syscall.EISDIR, want: "EISDIR: illegal operation on a directory, read"},
		{name: "write_file_ENOENT", call: Call{Operation: WriteFile, Path: "/requested/path"}, err: syscall.ENOENT, want: "ENOENT: no such file or directory, open '/requested/path'"},
		{name: "write_file_EEXIST", call: Call{Operation: WriteFile, Path: "/requested/path"}, err: syscall.EEXIST, want: "EEXIST: file already exists, open '/requested/path'"},
		{name: "write_file_EACCES", call: Call{Operation: WriteFile, Path: "/requested/path"}, err: syscall.EACCES, want: "EACCES: permission denied, open '/requested/path'"},
		{name: "write_file_ENOTDIR", call: Call{Operation: WriteFile, Path: "/requested/path"}, err: syscall.ENOTDIR, want: "ENOTDIR: not a directory, open '/requested/path'"},
		{name: "write_file_EISDIR", call: Call{Operation: WriteFile, Path: "/requested/path"}, err: syscall.EISDIR, want: "EISDIR: illegal operation on a directory, open '/requested/path'"},
		{name: "mkdir_all_ENOENT", call: Call{Operation: MkdirAll, Path: "/requested/path"}, err: syscall.ENOENT, want: "ENOENT: no such file or directory, mkdir '/requested/path'"},
		{name: "mkdir_all_EEXIST", call: Call{Operation: MkdirAll, Path: "/requested/path"}, err: syscall.EEXIST, want: "EEXIST: file already exists, mkdir '/requested/path'"},
		{name: "mkdir_all_EACCES", call: Call{Operation: MkdirAll, Path: "/requested/path"}, err: syscall.EACCES, want: "EACCES: permission denied, mkdir '/requested/path'"},
		{name: "mkdir_all_ENOTDIR", call: Call{Operation: MkdirAll, Path: "/requested/path"}, err: syscall.ENOTDIR, want: "ENOTDIR: not a directory, mkdir '/requested/path'"},
		{name: "mkdir_all_EISDIR", call: Call{Operation: MkdirAll, Path: "/requested/path"}, err: syscall.EISDIR, want: "EISDIR: illegal operation on a directory, mkdir '/requested/path'"},
		{name: "read_dir_ENOENT", call: Call{Operation: ReadDir, Path: "/requested/path"}, err: syscall.ENOENT, want: "ENOENT: no such file or directory, scandir '/requested/path'"},
		{name: "read_dir_EEXIST", call: Call{Operation: ReadDir, Path: "/requested/path"}, err: syscall.EEXIST, want: "EEXIST: file already exists, scandir '/requested/path'"},
		{name: "read_dir_EACCES", call: Call{Operation: ReadDir, Path: "/requested/path"}, err: syscall.EACCES, want: "EACCES: permission denied, scandir '/requested/path'"},
		{name: "read_dir_ENOTDIR", call: Call{Operation: ReadDir, Path: "/requested/path"}, err: syscall.ENOTDIR, want: "ENOTDIR: not a directory, scandir '/requested/path'"},
		{name: "read_dir_EISDIR", call: Call{Operation: ReadDir, Path: "/requested/path"}, err: syscall.EISDIR, want: "EISDIR: illegal operation on a directory, scandir '/requested/path'"},
		{name: "stat_ENOENT", call: Call{Operation: Stat, Path: "/requested/path"}, err: syscall.ENOENT, want: "ENOENT: no such file or directory, stat '/requested/path'"},
		{name: "stat_EEXIST", call: Call{Operation: Stat, Path: "/requested/path"}, err: syscall.EEXIST, want: "EEXIST: file already exists, stat '/requested/path'"},
		{name: "stat_EACCES", call: Call{Operation: Stat, Path: "/requested/path"}, err: syscall.EACCES, want: "EACCES: permission denied, stat '/requested/path'"},
		{name: "stat_ENOTDIR", call: Call{Operation: Stat, Path: "/requested/path"}, err: syscall.ENOTDIR, want: "ENOTDIR: not a directory, stat '/requested/path'"},
		{name: "stat_EISDIR", call: Call{Operation: Stat, Path: "/requested/path"}, err: syscall.EISDIR, want: "EISDIR: illegal operation on a directory, stat '/requested/path'"},
		{name: "lstat_ENOENT", call: Call{Operation: Lstat, Path: "/requested/path"}, err: syscall.ENOENT, want: "ENOENT: no such file or directory, lstat '/requested/path'"},
		{name: "lstat_EEXIST", call: Call{Operation: Lstat, Path: "/requested/path"}, err: syscall.EEXIST, want: "EEXIST: file already exists, lstat '/requested/path'"},
		{name: "lstat_EACCES", call: Call{Operation: Lstat, Path: "/requested/path"}, err: syscall.EACCES, want: "EACCES: permission denied, lstat '/requested/path'"},
		{name: "lstat_ENOTDIR", call: Call{Operation: Lstat, Path: "/requested/path"}, err: syscall.ENOTDIR, want: "ENOTDIR: not a directory, lstat '/requested/path'"},
		{name: "lstat_EISDIR", call: Call{Operation: Lstat, Path: "/requested/path"}, err: syscall.EISDIR, want: "EISDIR: illegal operation on a directory, lstat '/requested/path'"},
		{name: "rename_ENOENT", call: Call{Operation: Rename, Path: "/requested/source", Dest: "/requested/dest"}, err: syscall.ENOENT, want: "ENOENT: no such file or directory, rename '/requested/source' -> '/requested/dest'"},
		{name: "rename_EEXIST", call: Call{Operation: Rename, Path: "/requested/source", Dest: "/requested/dest"}, err: syscall.EEXIST, want: "EEXIST: file already exists, rename '/requested/source' -> '/requested/dest'"},
		{name: "rename_EACCES", call: Call{Operation: Rename, Path: "/requested/source", Dest: "/requested/dest"}, err: syscall.EACCES, want: "EACCES: permission denied, rename '/requested/source' -> '/requested/dest'"},
		{name: "rename_ENOTDIR", call: Call{Operation: Rename, Path: "/requested/source", Dest: "/requested/dest"}, err: syscall.ENOTDIR, want: "ENOTDIR: not a directory, rename '/requested/source' -> '/requested/dest'"},
		{name: "rename_EISDIR", call: Call{Operation: Rename, Path: "/requested/source", Dest: "/requested/dest"}, err: syscall.EISDIR, want: "EISDIR: illegal operation on a directory, rename '/requested/source' -> '/requested/dest'"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := test.call.Wrap(test.err)
			if got == nil || got.Error() != test.want {
				t.Errorf("Call%+v.Wrap(%v) = %v, want %q", test.call, test.err, got, test.want)
			}
		})
	}
}

func TestCallWrapUsesRequestedPathsWithoutEscaping(t *testing.T) {
	cause := &fs.PathError{Op: "open", Path: "/kernel/reported/component", Err: fs.ErrNotExist}
	call := Call{
		Operation: Rename,
		Path:      `/requested/team's/"source"\\raw`,
		Dest:      `/requested/team's/"dest"\\raw`,
	}
	want := `ENOENT: no such file or directory, rename '/requested/team's/"source"\\raw' -> '/requested/team's/"dest"\\raw'`

	if got := call.Wrap(cause).Error(); got != want {
		t.Errorf("Call%+v.Wrap(%v).Error() = %q, want %q", call, cause, got, want)
	}
}

func TestCallWrapMkdirAllTargetReclassification(t *testing.T) {
	requireDarwinLinuxNativeErrno(t)

	target := filepath.Join(t.TempDir(), "existing-file")
	if err := os.WriteFile(target, []byte("existing"), 0o600); err != nil {
		t.Fatalf("os.WriteFile(%q) error = %v", target, err)
	}

	cause := os.MkdirAll(target, 0o700)
	if !errors.Is(cause, syscall.ENOTDIR) {
		t.Fatalf("os.MkdirAll(%q) error = %v, want errors.Is(err, syscall.ENOTDIR)", target, cause)
	}
	want := "EEXIST: file already exists, mkdir '" + target + "'"
	got := (Call{Operation: MkdirAll, Path: target}).Wrap(cause)
	if got.Error() != want {
		t.Errorf("Call{Operation: MkdirAll, Path: %q}.Wrap(%v).Error() = %q, want %q", target, cause, got.Error(), want)
	}
	if !errors.Is(got, syscall.ENOTDIR) {
		t.Errorf("errors.Is(Call{Operation: MkdirAll, Path: %q}.Wrap(%v), syscall.ENOTDIR) = false, want true", target, cause)
	}
}

func TestCallWrapMkdirAllExistingFileWithTrailingSeparator(t *testing.T) {
	requireDarwinLinuxNativeErrno(t)

	// Node v24.15.0 on Darwin reports this exact ENOTDIR shape. The trailing
	// separator makes this distinct from the bare existing-file target, which
	// reports EEXIST.
	call := Call{Operation: MkdirAll, Path: "/requested/existing-file/"}
	cause := &fs.PathError{Op: "mkdir", Path: call.Path, Err: syscall.ENOTDIR}
	want := "ENOTDIR: not a directory, mkdir '/requested/existing-file/'"

	got := call.Wrap(cause)
	if got == nil || got.Error() != want {
		t.Errorf("Call%+v.Wrap(%v) = %v, want %q", call, cause, got, want)
	}
	if !errors.Is(got, syscall.ENOTDIR) {
		t.Errorf("errors.Is(Call%+v.Wrap(%v), syscall.ENOTDIR) = false, want true", call, cause)
	}
}

func TestCallWrapMkdirAllPermissionPaths(t *testing.T) {
	requireDarwinLinuxNativeErrno(t)

	// Hermetic Node v24.15.0 commit 848430679556aed0bd073f2bc263331ad84fa119
	// darwin/arm64 probes (uid/euid 501) used a fresh temporary root for every
	// mode and node:fs/promises.mkdir(target, {recursive:true}). The promises API
	// is the production compatibility oracle; mkdirSync differs by retaining the
	// full target for all five modes. With a missing parent below the locked
	// directory, promises.mkdir reports that active parent when the directory
	// remains searchable and the full target when traversal itself is denied.
	// Go 1.26.3 os.MkdirAll reports the active parent in both classes.
	tests := []struct {
		name         string
		mode         fs.FileMode
		wantStatCode errorCode
		wantFullPath bool
	}{
		{name: "searchable_0555", mode: 0o555, wantStatCode: codeENOENT},
		{name: "searchable_0500", mode: 0o500, wantStatCode: codeENOENT},
		{name: "nonsearchable_0444", mode: 0o444, wantStatCode: codeEACCES, wantFullPath: true},
		{name: "nonsearchable_0400", mode: 0o400, wantStatCode: codeEACCES, wantFullPath: true},
		{name: "nonsearchable_0000", mode: 0o000, wantStatCode: codeEACCES, wantFullPath: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			locked := filepath.Join(root, "locked")
			if err := os.Mkdir(locked, 0o700); err != nil {
				t.Fatalf("os.Mkdir(%q) error = %v", locked, err)
			}
			activeParent := filepath.Join(locked, "parent")
			target := filepath.Join(activeParent, "target")
			if _, err := os.Lstat(activeParent); !errors.Is(err, fs.ErrNotExist) {
				t.Fatalf("precondition os.Lstat(%q) error = %v, want fs.ErrNotExist", activeParent, err)
			}
			if err := os.Chmod(locked, test.mode); err != nil {
				t.Fatalf("os.Chmod(%q, %#o) error = %v", locked, test.mode, err)
			}
			t.Cleanup(func() {
				if err := os.Chmod(locked, 0o700); err != nil && !errors.Is(err, fs.ErrNotExist) {
					t.Errorf("restore os.Chmod(%q) error = %v", locked, err)
				}
			})

			cause := os.MkdirAll(target, 0o700)
			if cause == nil {
				t.Skip("host privileges do not enforce the test directory's permission bits")
			}
			if !errors.Is(cause, syscall.EACCES) {
				t.Fatalf("os.MkdirAll(%q) error = %v, want errors.Is(err, syscall.EACCES)", target, cause)
			}
			pathErr, ok := cause.(*fs.PathError)
			if !ok || pathErr == nil || pathErr.Op != "mkdir" || pathErr.Path != activeParent {
				t.Fatalf("os.MkdirAll(%q) error = %#v, want direct *fs.PathError{Op: %q, Path: %q}", target, cause, "mkdir", activeParent)
			}

			_, statErr := os.Stat(target)
			if gotCode := classify(statErr); gotCode != test.wantStatCode {
				t.Fatalf("classify(os.Stat(%q) error %v) = %q, want %q", target, statErr, gotCode, test.wantStatCode)
			}

			wantPath := activeParent
			if test.wantFullPath {
				wantPath = target
			}
			call := Call{Operation: MkdirAll, Path: target}
			want := "EACCES: permission denied, mkdir '" + wantPath + "'"
			got := call.Wrap(cause)
			if got == nil || got.Error() != want {
				t.Errorf("Call%+v.Wrap(%v) = %v, want %q", call, cause, got, want)
			}
			if !errors.Is(got, syscall.EACCES) {
				t.Errorf("errors.Is(Call%+v.Wrap(%v), syscall.EACCES) = false, want true", call, cause)
			}
			var preserved *fs.PathError
			if !errors.As(got, &preserved) || preserved != pathErr {
				t.Errorf("errors.As(Call%+v.Wrap(%v), *fs.PathError) = %p, want original %p", call, cause, preserved, pathErr)
			}
		})
	}
}

func TestCallWrapMkdirAllAmbiguousPermissionPathFailsClosed(t *testing.T) {
	root := t.TempDir()
	absentTarget := filepath.Join(root, "missing", "target")
	existingTarget := filepath.Join(root, "existing")
	if err := os.Mkdir(existingTarget, 0o700); err != nil {
		t.Fatalf("os.Mkdir(%q) error = %v", existingTarget, err)
	}

	tests := []struct {
		name  string
		call  Call
		cause *fs.PathError
	}{
		{
			name:  "reported_path_is_not_an_ancestor",
			call:  Call{Operation: MkdirAll, Path: absentTarget},
			cause: &fs.PathError{Op: "mkdir", Path: filepath.Join(root, "sibling"), Err: syscall.EACCES},
		},
		{
			name:  "reported_path_is_only_a_string_prefix",
			call:  Call{Operation: MkdirAll, Path: filepath.Join(root, "missing", "target")},
			cause: &fs.PathError{Op: "mkdir", Path: filepath.Join(root, "miss"), Err: syscall.EACCES},
		},
		{
			name:  "reported_path_is_volume_root",
			call:  Call{Operation: MkdirAll, Path: absentTarget},
			cause: &fs.PathError{Op: "mkdir", Path: string(filepath.Separator), Err: syscall.EACCES},
		},
		{
			name:  "target_now_exists",
			call:  Call{Operation: MkdirAll, Path: existingTarget},
			cause: &fs.PathError{Op: "mkdir", Path: root, Err: syscall.EACCES},
		},
		{
			name:  "wrong_source_operation",
			call:  Call{Operation: MkdirAll, Path: absentTarget},
			cause: &fs.PathError{Op: "stat", Path: absentTarget, Err: syscall.EACCES},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := test.call.Wrap(test.cause); got != test.cause {
				t.Errorf("Call%+v.Wrap(%v) = %v, want original ambiguous error %v", test.call, test.cause, got, test.cause)
			}
		})
	}
}

func TestStableStatErrorCodeRejectsChangingObservation(t *testing.T) {
	requireDarwinLinuxNativeErrno(t)

	errorsByCall := []error{
		&fs.PathError{Op: "stat", Path: "/requested/path", Err: syscall.ENOENT},
		&fs.PathError{Op: "stat", Path: "/requested/path", Err: syscall.EACCES},
	}
	calls := 0
	stat := func(string) (fs.FileInfo, error) {
		err := errorsByCall[calls]
		calls++
		return nil, err
	}

	if code, stable := stableStatErrorCode("/requested/path", stat); stable || code != codeENOENT {
		t.Errorf("stableStatErrorCode() = (%q, %t), want (%q, false)", code, stable, codeENOENT)
	}
	if calls != 2 {
		t.Errorf("stableStatErrorCode() probe calls = %d, want 2", calls)
	}
}

func TestMkdirSymlinkSnapshotPathStripsOnlyTrailingSeparators(t *testing.T) {
	separator := string(filepath.Separator)
	tests := []struct {
		name   string
		path   string
		want   string
		wantOK bool
	}{
		{name: "relative", path: "parent" + separator + "link", want: "parent" + separator + "link", wantOK: true},
		{name: "one_trailing", path: "parent" + separator + "link" + separator, want: "parent" + separator + "link", wantOK: true},
		{name: "many_trailing", path: "parent" + separator + "link" + separator + separator, want: "parent" + separator + "link", wantOK: true},
		{name: "empty"},
		{name: "root", path: separator},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, ok := mkdirSymlinkSnapshotPath(test.path)
			if got != test.want || ok != test.wantOK {
				t.Errorf("mkdirSymlinkSnapshotPath(%q) = (%q, %t), want (%q, %t)", test.path, got, ok, test.want, test.wantOK)
			}
		})
	}
}

func TestIsStrictMkdirAllAncestor(t *testing.T) {
	separator := string(filepath.Separator)
	output := filepath.Join(t.TempDir(), "output")
	tests := []struct {
		name     string
		ancestor string
		path     string
		want     bool
	}{
		{name: "ordinary_component", ancestor: output, path: output + separator + "child", want: true},
		{name: "recursive_repeated_separator", ancestor: output + separator, path: output + separator + separator + "child", want: true},
		{name: "single_separator_not_recursive_spelling", ancestor: output + separator, path: output + separator + "child"},
		{name: "equal", ancestor: output, path: output},
		{name: "string_prefix_only", ancestor: output, path: output + "-sibling"},
		{name: "volume_root", ancestor: separator, path: separator + "child"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := isStrictMkdirAllAncestor(test.ancestor, test.path); got != test.want {
				t.Errorf("isStrictMkdirAllAncestor(%q, %q) = %t, want %t", test.ancestor, test.path, got, test.want)
			}
		})
	}
}

func TestMatchingMkdirSymlinkFollowFailsClosed(t *testing.T) {
	directory := t.TempDir()
	regular := filepath.Join(directory, "regular")
	if err := os.WriteFile(regular, []byte("existing"), 0o600); err != nil {
		t.Fatalf("os.WriteFile(%q) error = %v", regular, err)
	}
	regularFirst, err := os.Stat(regular)
	if err != nil {
		t.Fatalf("first os.Stat(%q) error = %v", regular, err)
	}
	regularSecond, err := os.Stat(regular)
	if err != nil {
		t.Fatalf("second os.Stat(%q) error = %v", regular, err)
	}
	directoryInfo, err := os.Stat(directory)
	if err != nil {
		t.Fatalf("os.Stat(%q) error = %v", directory, err)
	}
	missing := &fs.PathError{Op: "stat", Path: filepath.Join(directory, "missing"), Err: syscall.ENOENT}
	denied := &fs.PathError{Op: "stat", Path: filepath.Join(directory, "denied"), Err: syscall.EACCES}
	loop := &fs.PathError{Op: "stat", Path: filepath.Join(directory, "loop"), Err: syscall.ELOOP}

	tests := []struct {
		name                     string
		firstInfo                fs.FileInfo
		firstErr                 error
		secondInfo               fs.FileInfo
		secondErr                error
		wantCode                 errorCode
		wantExistingNonDirectory bool
		wantStable               bool
	}{
		{name: "stable_existing_non_directory", firstInfo: regularFirst, secondInfo: regularSecond, wantExistingNonDirectory: true, wantStable: true},
		{name: "directory_is_not_non_directory", firstInfo: directoryInfo, secondInfo: directoryInfo},
		{name: "matching_supported_error", firstErr: missing, secondErr: missing, wantCode: codeENOENT, wantStable: true},
		{name: "changing_error", firstErr: missing, secondErr: denied},
		{name: "success_then_error", firstInfo: regularFirst, secondErr: missing},
		{name: "nil_info_success", secondInfo: regularSecond},
		{name: "unsupported_loop", firstErr: loop, secondErr: loop},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, stable := matchingMkdirSymlinkFollow(test.firstInfo, test.firstErr, test.secondInfo, test.secondErr)
			if got.code != test.wantCode || got.existingNonDirectory != test.wantExistingNonDirectory || stable != test.wantStable {
				t.Errorf("matchingMkdirSymlinkFollow() = (%+v, %t), want ({code:%q existingNonDirectory:%t}, %t)", got, stable, test.wantCode, test.wantExistingNonDirectory, test.wantStable)
			}
		})
	}
}

func TestCallWrapMkdirAllBareDanglingSymlink(t *testing.T) {
	requireDarwinLinuxNativeErrno(t)

	// Node v24.15.0 commit 848430679556aed0bd073f2bc263331ad84fa119
	// node:fs/promises.mkdir reports ENOENT for a bare dangling-symlink target.
	// Go 1.26.3 reports a direct mkdir PathError carrying EEXIST for the same
	// target.
	directory := t.TempDir()
	target := filepath.Join(directory, "dangling")
	if err := os.Symlink(filepath.Join(directory, "missing-referent"), target); err != nil {
		if runtime.GOOS == "windows" {
			t.Skipf("os.Symlink() error = %v; Windows test host does not permit symlink creation", err)
		}
		t.Fatalf("os.Symlink() error = %v", err)
	}

	cause := os.MkdirAll(target, 0o700)
	if !errors.Is(cause, syscall.EEXIST) {
		t.Fatalf("os.MkdirAll(%q) error = %v, want errors.Is(err, syscall.EEXIST)", target, cause)
	}
	pathErr, ok := cause.(*fs.PathError)
	if !ok || pathErr == nil || pathErr.Op != "mkdir" || pathErr.Path != target {
		t.Fatalf("os.MkdirAll(%q) error = %#v, want direct *fs.PathError{Op: %q, Path: %q}", target, cause, "mkdir", target)
	}
	call := Call{Operation: MkdirAll, Path: target}
	want := "ENOENT: no such file or directory, mkdir '" + target + "'"

	got := call.Wrap(cause)
	if got == nil || got.Error() != want {
		t.Errorf("Call%+v.Wrap(%v) = %v, want %q", call, cause, got, want)
	}
	if !errors.Is(got, syscall.EEXIST) {
		t.Errorf("errors.Is(Call%+v.Wrap(%v), syscall.EEXIST) = false, want true", call, cause)
	}
	var preserved *fs.PathError
	if !errors.As(got, &preserved) || preserved != pathErr {
		t.Errorf("errors.As(Call%+v.Wrap(%v), *fs.PathError) = %p, want original %p", call, cause, preserved, pathErr)
	}
}

func TestCallWrapMkdirAllTrailingChainedDanglingSymlink(t *testing.T) {
	requireDarwinLinuxNativeErrno(t)

	// Node v24.15.0 reports requested-path ENOENT for both absolute and
	// relative two-link chains with a trailing separator. Go 1.26.3 reports a
	// direct requested-path EEXIST. Classification must remain read-only: the
	// missing referent stays absent and both link snapshots stay unchanged.
	tests := []struct {
		name     string
		relative bool
	}{
		{name: "absolute"},
		{name: "relative", relative: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			directory := t.TempDir()
			middle := filepath.Join(directory, "middle")
			missing := filepath.Join(directory, "missing")
			target := filepath.Join(directory, "target")
			middleLink, targetLink := missing, middle
			if test.relative {
				middleLink, targetLink = "missing", "middle"
			}
			if err := os.Symlink(middleLink, middle); err != nil {
				t.Fatalf("os.Symlink(%q, %q) error = %v", middleLink, middle, err)
			}
			if err := os.Symlink(targetLink, target); err != nil {
				t.Fatalf("os.Symlink(%q, %q) error = %v", targetLink, target, err)
			}

			requested := target + string(filepath.Separator)
			cause := os.MkdirAll(requested, 0o700)
			pathErr := requireDirectMkdirExist(t, requested, cause)
			call := Call{Operation: MkdirAll, Path: requested}
			want := "ENOENT: no such file or directory, mkdir '" + requested + "'"

			got := call.Wrap(cause)
			if got == nil || got.Error() != want {
				t.Errorf("Call%+v.Wrap(%v) = %v, want %q", call, cause, got, want)
			}
			assertPreservedMkdirExist(t, call, cause, got, pathErr)
			if _, err := os.Lstat(missing); !errors.Is(err, fs.ErrNotExist) {
				t.Errorf("os.Lstat(%q) error after Call.Wrap = %v, want fs.ErrNotExist", missing, err)
			}
			if gotLink, err := os.Readlink(middle); err != nil || gotLink != middleLink {
				t.Errorf("os.Readlink(%q) after Call.Wrap = (%q, %v), want (%q, nil)", middle, gotLink, err, middleLink)
			}
			if gotLink, err := os.Readlink(target); err != nil || gotLink != targetLink {
				t.Errorf("os.Readlink(%q) after Call.Wrap = (%q, %v), want (%q, nil)", target, gotLink, err, targetLink)
			}
		})
	}
}

func TestCallWrapMkdirAllTrailingDirectNestedDanglingSymlink(t *testing.T) {
	requireDarwinLinuxNativeErrno(t)

	// Node v24.15.0 reports ENOTDIR at the bare link for a final link whose
	// direct target has a missing parent. Go 1.26.3 reports ENOENT at the exact
	// trailing-separator spelling. The observation must not create any part of
	// the missing referent.
	tests := []struct {
		name     string
		relative bool
		suffix   string
	}{
		{name: "absolute_one_separator", suffix: string(filepath.Separator)},
		{name: "absolute_multiple_separators", suffix: strings.Repeat(string(filepath.Separator), 2)},
		{name: "relative_one_separator", relative: true, suffix: string(filepath.Separator)},
		{name: "relative_multiple_separators", relative: true, suffix: strings.Repeat(string(filepath.Separator), 2)},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			directory := t.TempDir()
			missing := filepath.Join(directory, "missing")
			target := filepath.Join(directory, "target")
			linkTarget := filepath.Join(missing, "child")
			if test.relative {
				linkTarget = filepath.Join("missing", "child")
			}
			if err := os.Symlink(linkTarget, target); err != nil {
				t.Fatalf("os.Symlink(%q, %q) error = %v", linkTarget, target, err)
			}

			requested := target + test.suffix
			cause := os.MkdirAll(requested, 0o700)
			pathErr := requireDirectMkdirError(t, requested, requested, cause, syscall.ENOENT)
			call := Call{Operation: MkdirAll, Path: requested}
			want := "ENOTDIR: not a directory, mkdir '" + target + "'"

			got := call.Wrap(cause)
			if got == nil || got.Error() != want {
				t.Errorf("Call%+v.Wrap(%v) = %v, want %q", call, cause, got, want)
			}
			assertPreservedMkdirCause(t, call, cause, got, pathErr, syscall.ENOENT)
			if gotLink, err := os.Readlink(target); err != nil || gotLink != linkTarget {
				t.Errorf("os.Readlink(%q) after Call.Wrap = (%q, %v), want (%q, nil)", target, gotLink, err, linkTarget)
			}
			if _, err := os.Lstat(missing); !errors.Is(err, fs.ErrNotExist) {
				t.Errorf("os.Lstat(%q) error after Call.Wrap = %v, want fs.ErrNotExist", missing, err)
			}
		})
	}
}

func TestCallWrapMkdirAllIntermediateDanglingSymlink(t *testing.T) {
	requireDarwinLinuxNativeErrno(t)

	// os.MkdirAll recursively reports EEXIST at the dangling link ancestor;
	// Node v24.15.0 reports ENOTDIR at that exact ancestor instead. A repeated
	// separator leaves one separator on the recursive ancestor spelling.
	separator := string(filepath.Separator)
	tests := []struct {
		name         string
		relative     bool
		chain        bool
		suffix       string
		reportedPath func(string) string
	}{
		{name: "absolute_direct_child", suffix: separator + "child", reportedPath: func(target string) string { return target }},
		{name: "relative_direct_child", relative: true, suffix: separator + "child", reportedPath: func(target string) string { return target }},
		{name: "absolute_chain_child", chain: true, suffix: separator + "child", reportedPath: func(target string) string { return target }},
		{name: "relative_chain_child", relative: true, chain: true, suffix: separator + "child", reportedPath: func(target string) string { return target }},
		{name: "absolute_chain_repeated_separator_child", chain: true, suffix: separator + separator + "child", reportedPath: func(target string) string { return target + separator }},
		{name: "relative_chain_repeated_separator_child", relative: true, chain: true, suffix: separator + separator + "child", reportedPath: func(target string) string { return target + separator }},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			directory := t.TempDir()
			missing := filepath.Join(directory, "missing")
			middle := filepath.Join(directory, "middle")
			target := filepath.Join(directory, "target")
			linkTarget := missing
			if test.relative {
				linkTarget = "missing"
			}
			if test.chain {
				if err := os.Symlink(linkTarget, middle); err != nil {
					t.Fatalf("os.Symlink(%q, %q) error = %v", linkTarget, middle, err)
				}
				linkTarget = middle
				if test.relative {
					linkTarget = "middle"
				}
			}
			if err := os.Symlink(linkTarget, target); err != nil {
				t.Fatalf("os.Symlink(%q, %q) error = %v", linkTarget, target, err)
			}

			requested := target + test.suffix
			reported := test.reportedPath(target)
			cause := os.MkdirAll(requested, 0o700)
			pathErr := requireDirectMkdirError(t, requested, reported, cause, syscall.EEXIST)
			call := Call{Operation: MkdirAll, Path: requested}
			want := "ENOTDIR: not a directory, mkdir '" + reported + "'"

			got := call.Wrap(cause)
			if got == nil || got.Error() != want {
				t.Errorf("Call%+v.Wrap(%v) = %v, want %q", call, cause, got, want)
			}
			assertPreservedMkdirCause(t, call, cause, got, pathErr, syscall.EEXIST)
			if gotLink, err := os.Readlink(target); err != nil || gotLink != linkTarget {
				t.Errorf("os.Readlink(%q) after Call.Wrap = (%q, %v), want (%q, nil)", target, gotLink, err, linkTarget)
			}
			if test.chain {
				wantMiddle := missing
				if test.relative {
					wantMiddle = "missing"
				}
				if gotLink, err := os.Readlink(middle); err != nil || gotLink != wantMiddle {
					t.Errorf("os.Readlink(%q) after Call.Wrap = (%q, %v), want (%q, nil)", middle, gotLink, err, wantMiddle)
				}
			}
			if _, err := os.Lstat(missing); !errors.Is(err, fs.ErrNotExist) {
				t.Errorf("os.Lstat(%q) error after Call.Wrap = %v, want fs.ErrNotExist", missing, err)
			}
		})
	}
}

func TestCallWrapMkdirAllNestedDanglingIntermediateSeparators(t *testing.T) {
	requireDarwinLinuxNativeErrno(t)

	// Node v24.15.0 exposes the active link component for nested dangling
	// targets. A direct link always names the bare link; two- and three-link
	// chains retain the recursive ancestor's separator spelling. Go reports
	// ancestor ENOENT only for the direct link with repeated separators; chains
	// retain ancestor EEXIST. The 42-case product pins every source shape.
	separator := string(filepath.Separator)
	families := []struct {
		name     string
		depth    int
		relative bool
	}{
		{name: "direct_absolute", depth: 1},
		{name: "direct_relative", depth: 1, relative: true},
		{name: "two_link_absolute", depth: 2},
		{name: "two_link_relative", depth: 2, relative: true},
		{name: "three_link_absolute", depth: 3},
		{name: "three_link_relative", depth: 3, relative: true},
	}
	requests := []struct {
		name       string
		separators int
		trailing   bool
	}{
		{name: "one_separator", separators: 1},
		{name: "one_separator_trailing", separators: 1, trailing: true},
		{name: "two_separators", separators: 2},
		{name: "two_separators_trailing", separators: 2, trailing: true},
		{name: "three_separators", separators: 3},
		{name: "three_separators_trailing", separators: 3, trailing: true},
		{name: "four_separators", separators: 4},
	}

	for _, family := range families {
		for _, request := range requests {
			t.Run(family.name+"_"+request.name, func(t *testing.T) {
				directory := t.TempDir()
				missing := filepath.Join(directory, "missing")
				middle := filepath.Join(directory, "middle")
				outer := filepath.Join(directory, "outer")
				target := filepath.Join(directory, "target")
				type linkSnapshot struct {
					path   string
					target string
				}
				var links []linkSnapshot
				linkTarget := filepath.Join(missing, "child")
				if family.relative {
					linkTarget = filepath.Join("missing", "child")
				}
				if family.depth == 3 {
					if err := os.Symlink(linkTarget, outer); err != nil {
						t.Fatalf("os.Symlink(%q, %q) error = %v", linkTarget, outer, err)
					}
					links = append(links, linkSnapshot{path: outer, target: linkTarget})
					linkTarget = outer
					if family.relative {
						linkTarget = "outer"
					}
				}
				if family.depth >= 2 {
					if err := os.Symlink(linkTarget, middle); err != nil {
						t.Fatalf("os.Symlink(%q, %q) error = %v", linkTarget, middle, err)
					}
					links = append(links, linkSnapshot{path: middle, target: linkTarget})
					linkTarget = middle
					if family.relative {
						linkTarget = "middle"
					}
				}
				if err := os.Symlink(linkTarget, target); err != nil {
					t.Fatalf("os.Symlink(%q, %q) error = %v", linkTarget, target, err)
				}
				links = append(links, linkSnapshot{path: target, target: linkTarget})

				suffix := strings.Repeat(separator, request.separators) + "grand"
				if request.trailing {
					suffix += separator
				}
				requested := target + suffix
				reported := target + strings.Repeat(separator, request.separators-1)
				wantRawCode := error(syscall.EEXIST)
				if family.depth == 1 && request.separators > 1 {
					wantRawCode = syscall.ENOENT
				}
				cause := os.MkdirAll(requested, 0o700)
				pathErr := requireDirectMkdirError(t, requested, reported, cause, wantRawCode)
				call := Call{Operation: MkdirAll, Path: requested}
				wantPath := target
				if family.depth > 1 {
					wantPath = reported
				}
				want := "ENOTDIR: not a directory, mkdir '" + wantPath + "'"

				got := call.Wrap(cause)
				if got == nil || got.Error() != want {
					t.Errorf("Call%+v.Wrap(%v) = %v, want %q", call, cause, got, want)
				}
				assertPreservedMkdirCause(t, call, cause, got, pathErr, wantRawCode)
				for _, link := range links {
					if gotLink, err := os.Readlink(link.path); err != nil || gotLink != link.target {
						t.Errorf("os.Readlink(%q) after Call.Wrap = (%q, %v), want (%q, nil)", link.path, gotLink, err, link.target)
					}
				}
				if _, err := os.Lstat(missing); !errors.Is(err, fs.ErrNotExist) {
					t.Errorf("os.Lstat(%q) error after Call.Wrap = %v, want fs.ErrNotExist", missing, err)
				}
			})
		}
	}
}

func TestCallWrapMkdirAllIntermediateSymlinkThroughDeniedDirectory(t *testing.T) {
	requireDarwinLinuxNativeErrno(t)

	// Node v24.15.0 exposes EACCES at the full requested path when an
	// intermediate link resolves through a non-searchable directory. Go
	// 1.26.3 either reports EEXIST at the bare link ancestor or, with a
	// repeated separator, EACCES at the recursive link/ spelling.
	separator := string(filepath.Separator)
	tests := []struct {
		name           string
		relative       bool
		suffix         string
		wantRawCode    error
		reportedSuffix string
	}{
		{name: "absolute_child", suffix: separator + "grand", wantRawCode: syscall.EEXIST},
		{name: "absolute_trailing_child", suffix: separator + "grand" + separator, wantRawCode: syscall.EEXIST},
		{name: "absolute_repeated_separator_child", suffix: separator + separator + "grand", wantRawCode: syscall.EACCES, reportedSuffix: separator},
		{name: "relative_child", relative: true, suffix: separator + "grand", wantRawCode: syscall.EEXIST},
		{name: "relative_trailing_child", relative: true, suffix: separator + "grand" + separator, wantRawCode: syscall.EEXIST},
		{name: "relative_repeated_separator_child", relative: true, suffix: separator + separator + "grand", wantRawCode: syscall.EACCES, reportedSuffix: separator},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			directory := t.TempDir()
			denied := filepath.Join(directory, "denied")
			if err := os.Mkdir(denied, 0o700); err != nil {
				t.Fatalf("os.Mkdir(%q) error = %v", denied, err)
			}
			child := filepath.Join(denied, "child")
			if _, err := os.Lstat(child); !errors.Is(err, fs.ErrNotExist) {
				t.Fatalf("precondition os.Lstat(%q) error = %v, want fs.ErrNotExist", child, err)
			}
			target := filepath.Join(directory, "target")
			linkTarget := child
			if test.relative {
				linkTarget = filepath.Join("denied", "child")
			}
			if err := os.Symlink(linkTarget, target); err != nil {
				t.Fatalf("os.Symlink(%q, %q) error = %v", linkTarget, target, err)
			}
			if err := os.Chmod(denied, 0o000); err != nil {
				t.Fatalf("os.Chmod(%q, 0000) error = %v", denied, err)
			}
			restored := false
			t.Cleanup(func() {
				if !restored {
					if err := os.Chmod(denied, 0o700); err != nil && !errors.Is(err, fs.ErrNotExist) {
						t.Errorf("restore os.Chmod(%q) error = %v", denied, err)
					}
				}
			})

			if _, err := os.Stat(target); !errors.Is(err, syscall.EACCES) {
				if errors.Is(err, fs.ErrNotExist) {
					t.Skip("host privileges do not enforce the test directory's permission bits")
				}
				t.Fatalf("os.Stat(%q) error = %v, want errors.Is(err, syscall.EACCES)", target, err)
			}

			requested := target + test.suffix
			reported := target + test.reportedSuffix
			cause := os.MkdirAll(requested, 0o700)
			pathErr := requireDirectMkdirError(t, requested, reported, cause, test.wantRawCode)
			call := Call{Operation: MkdirAll, Path: requested}
			want := "EACCES: permission denied, mkdir '" + requested + "'"

			got := call.Wrap(cause)
			if got == nil || got.Error() != want {
				t.Errorf("Call%+v.Wrap(%v) = %v, want %q", call, cause, got, want)
			}
			assertPreservedMkdirCause(t, call, cause, got, pathErr, test.wantRawCode)
			if gotLink, err := os.Readlink(target); err != nil || gotLink != linkTarget {
				t.Errorf("os.Readlink(%q) after Call.Wrap = (%q, %v), want (%q, nil)", target, gotLink, err, linkTarget)
			}
			if info, err := os.Lstat(denied); err != nil || info.Mode().Perm() != 0 {
				t.Errorf("os.Lstat(%q) after Call.Wrap = (%v, %v), want mode 0000", denied, info, err)
			}

			if err := os.Chmod(denied, 0o700); err != nil {
				t.Fatalf("restore os.Chmod(%q) error = %v", denied, err)
			}
			restored = true
			if _, err := os.Lstat(child); !errors.Is(err, fs.ErrNotExist) {
				t.Errorf("os.Lstat(%q) error after Call.Wrap = %v, want fs.ErrNotExist", child, err)
			}
		})
	}
}

func TestCallWrapMkdirAllIntermediateSymlinkThroughRegularFile(t *testing.T) {
	requireDarwinLinuxNativeErrno(t)

	// Node v24.15.0 exposes ENOTDIR at the full request when an intermediate
	// link target traverses through a regular file. Go reports EEXIST at the
	// link ancestor, except that a repeated separator retains ENOTDIR at link/.
	separator := string(filepath.Separator)
	tests := []struct {
		name           string
		relative       bool
		suffix         string
		wantRawCode    error
		reportedSuffix string
	}{
		{name: "absolute_child", suffix: separator + "grandchild", wantRawCode: syscall.EEXIST},
		{name: "absolute_trailing_child", suffix: separator + "grandchild" + separator, wantRawCode: syscall.EEXIST},
		{name: "absolute_repeated_separator_child", suffix: separator + separator + "grandchild", wantRawCode: syscall.ENOTDIR, reportedSuffix: separator},
		{name: "relative_child", relative: true, suffix: separator + "grandchild", wantRawCode: syscall.EEXIST},
		{name: "relative_trailing_child", relative: true, suffix: separator + "grandchild" + separator, wantRawCode: syscall.EEXIST},
		{name: "relative_repeated_separator_child", relative: true, suffix: separator + separator + "grandchild", wantRawCode: syscall.ENOTDIR, reportedSuffix: separator},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			directory := t.TempDir()
			regular := filepath.Join(directory, "regular")
			const contents = "unchanged\n"
			if err := os.WriteFile(regular, []byte(contents), 0o600); err != nil {
				t.Fatalf("os.WriteFile(%q) error = %v", regular, err)
			}
			target := filepath.Join(directory, "target")
			linkTarget := filepath.Join(regular, "child")
			if test.relative {
				linkTarget = filepath.Join("regular", "child")
			}
			if err := os.Symlink(linkTarget, target); err != nil {
				t.Fatalf("os.Symlink(%q, %q) error = %v", linkTarget, target, err)
			}

			requested := target + test.suffix
			reported := target + test.reportedSuffix
			cause := os.MkdirAll(requested, 0o700)
			pathErr := requireDirectMkdirError(t, requested, reported, cause, test.wantRawCode)
			call := Call{Operation: MkdirAll, Path: requested}
			want := "ENOTDIR: not a directory, mkdir '" + requested + "'"

			got := call.Wrap(cause)
			if got == nil || got.Error() != want {
				t.Errorf("Call%+v.Wrap(%v) = %v, want %q", call, cause, got, want)
			}
			assertPreservedMkdirCause(t, call, cause, got, pathErr, test.wantRawCode)
			if gotLink, err := os.Readlink(target); err != nil || gotLink != linkTarget {
				t.Errorf("os.Readlink(%q) after Call.Wrap = (%q, %v), want (%q, nil)", target, gotLink, err, linkTarget)
			}
			if gotContents, err := os.ReadFile(regular); err != nil || string(gotContents) != contents {
				t.Errorf("os.ReadFile(%q) after Call.Wrap = (%q, %v), want (%q, nil)", regular, gotContents, err, contents)
			}
		})
	}
}

func TestCallWrapMkdirAllTrailingSymlinkToExistingNonDirectory(t *testing.T) {
	requireDarwinLinuxNativeErrno(t)

	// Node v24.15.0 renders EEXIST at the exact requested spelling when the
	// final symlink resolves to an existing non-directory. Go 1.26.3 returns a
	// direct ENOTDIR PathError. Both the file and link must remain unchanged.
	separator := string(filepath.Separator)
	tests := []struct {
		name     string
		relative bool
		suffix   string
	}{
		{name: "absolute_one_separator", suffix: separator},
		{name: "absolute_multiple_separators", suffix: separator + separator},
		{name: "relative_one_separator", relative: true, suffix: separator},
		{name: "relative_multiple_separators", relative: true, suffix: separator + separator},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			directory := t.TempDir()
			regular := filepath.Join(directory, "regular")
			const contents = "unchanged\n"
			if err := os.WriteFile(regular, []byte(contents), 0o600); err != nil {
				t.Fatalf("os.WriteFile(%q) error = %v", regular, err)
			}
			target := filepath.Join(directory, "target")
			linkTarget := regular
			if test.relative {
				linkTarget = "regular"
			}
			if err := os.Symlink(linkTarget, target); err != nil {
				t.Fatalf("os.Symlink(%q, %q) error = %v", linkTarget, target, err)
			}

			requested := target + test.suffix
			cause := os.MkdirAll(requested, 0o700)
			pathErr := requireDirectMkdirError(t, requested, requested, cause, syscall.ENOTDIR)
			call := Call{Operation: MkdirAll, Path: requested}
			want := "EEXIST: file already exists, mkdir '" + requested + "'"

			got := call.Wrap(cause)
			if got == nil || got.Error() != want {
				t.Errorf("Call%+v.Wrap(%v) = %v, want %q", call, cause, got, want)
			}
			assertPreservedMkdirCause(t, call, cause, got, pathErr, syscall.ENOTDIR)
			if gotLink, err := os.Readlink(target); err != nil || gotLink != linkTarget {
				t.Errorf("os.Readlink(%q) after Call.Wrap = (%q, %v), want (%q, nil)", target, gotLink, err, linkTarget)
			}
			if gotContents, err := os.ReadFile(regular); err != nil || string(gotContents) != contents {
				t.Errorf("os.ReadFile(%q) after Call.Wrap = (%q, %v), want (%q, nil)", regular, gotContents, err, contents)
			}
		})
	}
}

func TestCallWrapMkdirAllBareSymlinkThroughRegularFile(t *testing.T) {
	requireDarwinLinuxNativeErrno(t)

	// A hermetic Node v24.15.0 node:fs/promises.mkdir probe reports ENOTDIR
	// against the bare link path when following its referent encounters a
	// regular file. Go 1.26.3 reports target PathError/EEXIST instead.
	directory := t.TempDir()
	regular := filepath.Join(directory, "regular")
	if err := os.WriteFile(regular, []byte("existing"), 0o600); err != nil {
		t.Fatalf("os.WriteFile(%q) error = %v", regular, err)
	}
	target := filepath.Join(directory, "link")
	if err := os.Symlink(filepath.Join(regular, "child"), target); err != nil {
		if runtime.GOOS == "windows" {
			t.Skipf("os.Symlink() error = %v; Windows test host does not permit symlink creation", err)
		}
		t.Fatalf("os.Symlink() error = %v", err)
	}

	cause := os.MkdirAll(target, 0o700)
	pathErr := requireDirectMkdirExist(t, target, cause)
	if _, err := os.Stat(target); classify(err) != codeENOTDIR {
		t.Fatalf("classify(os.Stat(%q) error %v) = %q, want %q", target, err, classify(err), codeENOTDIR)
	}
	call := Call{Operation: MkdirAll, Path: target}
	want := "ENOTDIR: not a directory, mkdir '" + target + "'"

	got := call.Wrap(cause)
	if got == nil || got.Error() != want {
		t.Errorf("Call%+v.Wrap(%v) = %v, want %q", call, cause, got, want)
	}
	assertPreservedMkdirExist(t, call, cause, got, pathErr)
}

func TestCallWrapMkdirAllBareSymlinkThroughDeniedDirectory(t *testing.T) {
	requireDarwinLinuxNativeErrno(t)

	// A hermetic Node v24.15.0 node:fs/promises.mkdir probe reports EACCES
	// against the bare link path when following its referent enters a mode-0000
	// directory. Go 1.26.3 reports target PathError/EEXIST instead.
	directory := t.TempDir()
	denied := filepath.Join(directory, "denied")
	if err := os.Mkdir(denied, 0o700); err != nil {
		t.Fatalf("os.Mkdir(%q) error = %v", denied, err)
	}
	target := filepath.Join(directory, "link")
	if err := os.Symlink(filepath.Join(denied, "child"), target); err != nil {
		t.Fatalf("os.Symlink() error = %v", err)
	}
	if err := os.Chmod(denied, 0o000); err != nil {
		t.Fatalf("os.Chmod(%q, 0000) error = %v", denied, err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(denied, 0o700); err != nil && !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("restore os.Chmod(%q) error = %v", denied, err)
		}
	})

	_, statErr := os.Stat(target)
	if classify(statErr) != codeEACCES {
		if errors.Is(statErr, fs.ErrNotExist) {
			t.Skip("host privileges do not enforce the test directory's permission bits")
		}
		t.Fatalf("classify(os.Stat(%q) error %v) = %q, want %q", target, statErr, classify(statErr), codeEACCES)
	}
	cause := os.MkdirAll(target, 0o700)
	pathErr := requireDirectMkdirExist(t, target, cause)
	call := Call{Operation: MkdirAll, Path: target}
	want := "EACCES: permission denied, mkdir '" + target + "'"

	got := call.Wrap(cause)
	if got == nil || got.Error() != want {
		t.Errorf("Call%+v.Wrap(%v) = %v, want %q", call, cause, got, want)
	}
	assertPreservedMkdirExist(t, call, cause, got, pathErr)
}

func TestCallWrapMkdirAllTrailingSymlinkSupportedClassifications(t *testing.T) {
	requireDarwinLinuxNativeErrno(t)

	t.Run("ENOTDIR", func(t *testing.T) {
		directory := t.TempDir()
		regular := filepath.Join(directory, "regular")
		if err := os.WriteFile(regular, []byte("existing"), 0o600); err != nil {
			t.Fatalf("os.WriteFile(%q) error = %v", regular, err)
		}
		target := filepath.Join(directory, "link")
		if err := os.Symlink(filepath.Join(regular, "child"), target); err != nil {
			t.Fatalf("os.Symlink() error = %v", err)
		}
		requested := target + string(filepath.Separator)
		cause := &fs.PathError{Op: "mkdir", Path: requested, Err: syscall.EEXIST}
		pathErr := requireDirectMkdirExist(t, requested, cause)
		call := Call{Operation: MkdirAll, Path: requested}
		want := "ENOTDIR: not a directory, mkdir '" + requested + "'"

		got := call.Wrap(cause)
		if got == nil || got.Error() != want {
			t.Errorf("Call%+v.Wrap(%v) = %v, want %q", call, cause, got, want)
		}
		assertPreservedMkdirExist(t, call, cause, got, pathErr)
	})

	t.Run("EACCES", func(t *testing.T) {
		directory := t.TempDir()
		denied := filepath.Join(directory, "denied")
		if err := os.Mkdir(denied, 0o700); err != nil {
			t.Fatalf("os.Mkdir(%q) error = %v", denied, err)
		}
		target := filepath.Join(directory, "link")
		if err := os.Symlink(filepath.Join(denied, "child"), target); err != nil {
			t.Fatalf("os.Symlink() error = %v", err)
		}
		if err := os.Chmod(denied, 0o000); err != nil {
			t.Fatalf("os.Chmod(%q, 0000) error = %v", denied, err)
		}
		t.Cleanup(func() {
			if err := os.Chmod(denied, 0o700); err != nil && !errors.Is(err, fs.ErrNotExist) {
				t.Errorf("restore os.Chmod(%q) error = %v", denied, err)
			}
		})

		requested := target + string(filepath.Separator)
		_, statErr := os.Stat(requested)
		if !errors.Is(statErr, syscall.EACCES) {
			if errors.Is(statErr, fs.ErrNotExist) {
				t.Skip("host privileges do not enforce the test directory's permission bits")
			}
			t.Fatalf("os.Stat(%q) error = %v, want errors.Is(err, syscall.EACCES)", requested, statErr)
		}
		cause := &fs.PathError{Op: "mkdir", Path: requested, Err: syscall.EEXIST}
		pathErr := requireDirectMkdirExist(t, requested, cause)
		call := Call{Operation: MkdirAll, Path: requested}
		want := "EACCES: permission denied, mkdir '" + requested + "'"

		got := call.Wrap(cause)
		if got == nil || got.Error() != want {
			t.Errorf("Call%+v.Wrap(%v) = %v, want %q", call, cause, got, want)
		}
		assertPreservedMkdirExist(t, call, cause, got, pathErr)
	})
}

func TestCallWrapMkdirAllBareSymlinkUnsupportedFollowErrorFailsClosed(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("native Win32 symlink errno behavior is outside the Darwin/Linux parity matrix")
	}
	directory := t.TempDir()
	target := filepath.Join(directory, "loop")
	if err := os.Symlink(target, target); err != nil {
		if runtime.GOOS == "windows" {
			t.Skipf("os.Symlink() error = %v; Windows test host does not permit symlink creation", err)
		}
		t.Fatalf("os.Symlink() error = %v", err)
	}

	cause := os.MkdirAll(target, 0o700)
	requireDirectMkdirExist(t, target, cause)
	if _, err := os.Stat(target); !errors.Is(err, syscall.ELOOP) {
		t.Fatalf("os.Stat(%q) error = %v, want errors.Is(err, syscall.ELOOP)", target, err)
	}
	call := Call{Operation: MkdirAll, Path: target}
	if got := call.Wrap(cause); got != cause {
		t.Errorf("Call%+v.Wrap(%v) = %v, want original unsupported error %v", call, cause, got, cause)
	}
}

func TestCallWrapMkdirAllTrailingSelfLoopFailsClosed(t *testing.T) {
	requireDarwinLinuxNativeErrno(t)
	directory := t.TempDir()
	target := filepath.Join(directory, "loop")
	if err := os.Symlink(filepath.Base(target), target); err != nil {
		t.Fatalf("os.Symlink() error = %v", err)
	}
	requested := target + string(filepath.Separator)
	cause := os.MkdirAll(requested, 0o700)
	requireDirectMkdirExist(t, requested, cause)
	if _, err := os.Stat(requested); !errors.Is(err, syscall.ELOOP) {
		t.Fatalf("os.Stat(%q) error = %v, want errors.Is(err, syscall.ELOOP)", requested, err)
	}
	call := Call{Operation: MkdirAll, Path: requested}
	if got := call.Wrap(cause); got != cause {
		t.Errorf("Call%+v.Wrap(%v) = %v, want original unsupported error %v", call, cause, got, cause)
	}
}

func requireDirectMkdirExist(t *testing.T, target string, cause error) *fs.PathError {
	t.Helper()
	return requireDirectMkdirError(t, target, target, cause, syscall.EEXIST)
}

func requireDirectMkdirError(t *testing.T, requested, reported string, cause error, want error) *fs.PathError {
	t.Helper()
	if !errors.Is(cause, want) {
		t.Fatalf("os.MkdirAll(%q) error = %v, want errors.Is(err, %v)", requested, cause, want)
	}
	pathErr, ok := cause.(*fs.PathError)
	if !ok || pathErr == nil || pathErr.Op != "mkdir" || pathErr.Path != reported {
		t.Fatalf("os.MkdirAll(%q) error = %#v, want direct *fs.PathError{Op: %q, Path: %q}", requested, cause, "mkdir", reported)
	}
	return pathErr
}

func assertPreservedMkdirExist(t *testing.T, call Call, cause, got error, pathErr *fs.PathError) {
	t.Helper()
	assertPreservedMkdirCause(t, call, cause, got, pathErr, syscall.EEXIST)
}

func assertPreservedMkdirCause(t *testing.T, call Call, cause, got error, pathErr *fs.PathError, want error) {
	t.Helper()
	if !errors.Is(got, want) {
		t.Errorf("errors.Is(Call%+v.Wrap(%v), %v) = false, want true", call, cause, want)
	}
	var preserved *fs.PathError
	if !errors.As(got, &preserved) || preserved != pathErr {
		t.Errorf("errors.As(Call%+v.Wrap(%v), *fs.PathError) = %p, want original %p", call, cause, preserved, pathErr)
	}
}

func TestCallWrapMkdirAllTrailingEEXISTWithoutSymlinkEvidenceFailsClosed(t *testing.T) {
	// A direct EEXIST plus trailing path text is insufficient evidence by
	// itself. With no stable final symlink snapshot, the original error must
	// remain unchanged rather than being guessed into a referent error.
	target := "/requested/dangling/"
	call := Call{Operation: MkdirAll, Path: target}
	cause := &fs.PathError{Op: "mkdir", Path: target, Err: syscall.EEXIST}

	if got := call.Wrap(cause); got != cause {
		t.Errorf("Call%+v.Wrap(%v) = %v, want original ambiguous error %v", call, cause, got, cause)
	}
}

func TestCallWrapMkdirAllAmbiguousEEXISTFailsClosed(t *testing.T) {
	target := filepath.Join(t.TempDir(), "absent-after-error")
	call := Call{Operation: MkdirAll, Path: target}
	cause := &fs.PathError{Op: "mkdir", Path: target, Err: syscall.EEXIST}

	if got := call.Wrap(cause); got != cause {
		t.Errorf("Call%+v.Wrap(%v) = %v, want original ambiguous error %v", call, cause, got, cause)
	}
}

func TestCallWrapMkdirAllAmbiguousIntermediateFollowFailsClosed(t *testing.T) {
	requireDarwinLinuxNativeErrno(t)

	directory := t.TempDir()
	ordinary := filepath.Join(directory, "ordinary")
	if err := os.Mkdir(ordinary, 0o700); err != nil {
		t.Fatalf("os.Mkdir(%q) error = %v", ordinary, err)
	}
	loop := filepath.Join(directory, "loop")
	if err := os.Symlink(filepath.Base(loop), loop); err != nil {
		t.Fatalf("os.Symlink(%q, %q) error = %v", filepath.Base(loop), loop, err)
	}
	separator := string(filepath.Separator)
	tests := []struct {
		name  string
		call  Call
		cause *fs.PathError
	}{
		{
			name:  "EEXIST_ancestor_is_not_a_symlink",
			call:  Call{Operation: MkdirAll, Path: ordinary + separator + "child"},
			cause: &fs.PathError{Op: "mkdir", Path: ordinary, Err: syscall.EEXIST},
		},
		{
			name:  "EEXIST_symlink_follow_is_unsupported_ELOOP",
			call:  Call{Operation: MkdirAll, Path: loop + separator + "child"},
			cause: &fs.PathError{Op: "mkdir", Path: loop, Err: syscall.EEXIST},
		},
		{
			name:  "repeated_separator_EACCES_ancestor_is_not_a_symlink",
			call:  Call{Operation: MkdirAll, Path: ordinary + separator + separator + "child"},
			cause: &fs.PathError{Op: "mkdir", Path: ordinary + separator, Err: syscall.EACCES},
		},
		{
			name:  "repeated_separator_ENOENT_ancestor_is_not_a_symlink",
			call:  Call{Operation: MkdirAll, Path: ordinary + separator + separator + "child"},
			cause: &fs.PathError{Op: "mkdir", Path: ordinary + separator, Err: syscall.ENOENT},
		},
		{
			name:  "repeated_separator_ENOENT_symlink_follow_is_unsupported_ELOOP",
			call:  Call{Operation: MkdirAll, Path: loop + separator + separator + "child"},
			cause: &fs.PathError{Op: "mkdir", Path: loop + separator, Err: syscall.ENOENT},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := test.call.Wrap(test.cause); got != test.cause {
				t.Errorf("Call%+v.Wrap(%v) = %v, want original ambiguous error %v", test.call, test.cause, got, test.cause)
			}
		})
	}
	if gotLink, err := os.Readlink(loop); err != nil || gotLink != filepath.Base(loop) {
		t.Errorf("os.Readlink(%q) after ambiguous classifications = (%q, %v), want (%q, nil)", loop, gotLink, err, filepath.Base(loop))
	}
}

func TestCallWrapMkdirAllParentRetainsENOTDIRAndFullRequest(t *testing.T) {
	requireDarwinLinuxNativeErrno(t)

	parent := filepath.Join(t.TempDir(), "existing-file")
	if err := os.WriteFile(parent, []byte("existing"), 0o600); err != nil {
		t.Fatalf("os.WriteFile(%q) error = %v", parent, err)
	}
	target := filepath.Join(parent, "child", "target")

	cause := os.MkdirAll(target, 0o700)
	if !errors.Is(cause, syscall.ENOTDIR) {
		t.Fatalf("os.MkdirAll(%q) error = %v, want errors.Is(err, syscall.ENOTDIR)", target, cause)
	}
	want := "ENOTDIR: not a directory, mkdir '" + target + "'"
	if got := (Call{Operation: MkdirAll, Path: target}).Wrap(cause).Error(); got != want {
		t.Errorf("Call{Operation: MkdirAll, Path: %q}.Wrap(%v).Error() = %q, want %q", target, cause, got, want)
	}
}

func TestCallWrapPreservesDirectRawCause(t *testing.T) {
	cause := &fs.PathError{Op: "open", Path: "/kernel/path", Err: fs.ErrNotExist}
	got := (Call{Operation: ReadFile, Path: "/requested/path"}).Wrap(cause)

	if !errors.Is(got, fs.ErrNotExist) {
		t.Errorf("errors.Is(Call.Wrap(%v), fs.ErrNotExist) = false, want true", cause)
	}
	var pathErr *fs.PathError
	if !errors.As(got, &pathErr) {
		t.Fatalf("errors.As(Call.Wrap(%v), *fs.PathError) = false, want true", cause)
	}
	if pathErr != cause {
		t.Errorf("errors.As(Call.Wrap(%v), *fs.PathError) = %p, want %p", cause, pathErr, cause)
	}
}

func TestCallWrapDirectLinkError(t *testing.T) {
	cause := &os.LinkError{
		Op:  "rename",
		Old: "/kernel/source",
		New: "/kernel/dest",
		Err: fs.ErrNotExist,
	}
	call := Call{Operation: Rename, Path: "/requested/source", Dest: "/requested/dest"}
	want := "ENOENT: no such file or directory, rename '/requested/source' -> '/requested/dest'"
	got := call.Wrap(cause)

	if got == nil || got.Error() != want {
		t.Errorf("Call%+v.Wrap(%v) = %v, want %q", call, cause, got, want)
	}
	var linkErr *os.LinkError
	if !errors.As(got, &linkErr) {
		t.Fatalf("errors.As(Call.Wrap(%v), *os.LinkError) = false, want true", cause)
	}
	if linkErr != cause {
		t.Errorf("errors.As(Call.Wrap(%v), *os.LinkError) = %p, want %p", cause, linkErr, cause)
	}
}

func TestCallWrapLeavesWrappedFilesystemErrorsUntouched(t *testing.T) {
	tests := []struct {
		name    string
		call    Call
		wrapped error
	}{
		{
			name:    "path_error",
			call:    Call{Operation: ReadFile, Path: "/requested/path"},
			wrapped: fmt.Errorf("domain boundary: %w", &fs.PathError{Op: "open", Path: "/kernel/path", Err: syscall.ENOENT}),
		},
		{
			name:    "direct_errno",
			call:    Call{Operation: ReadFile, Path: "/requested/path"},
			wrapped: fmt.Errorf("domain boundary: %w", syscall.ENOENT),
		},
		{
			name:    "mkdir_target_reclassification",
			call:    Call{Operation: MkdirAll, Path: "/requested/path"},
			wrapped: fmt.Errorf("domain boundary: %w", &fs.PathError{Op: "mkdir", Path: "/requested/path", Err: syscall.ENOTDIR}),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := test.call.Wrap(test.wrapped); got != test.wrapped {
				t.Errorf("Call%+v.Wrap(%v) = %v, want original wrapped error %v", test.call, test.wrapped, got, test.wrapped)
			}
		})
	}
}

func TestCallWrapPortableFilesystemSentinels(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{name: "not_exist", err: fs.ErrNotExist, want: "ENOENT: no such file or directory, stat '/requested/path'"},
		{name: "exist", err: fs.ErrExist, want: "EEXIST: file already exists, stat '/requested/path'"},
		{name: "permission", err: fs.ErrPermission, want: "EACCES: permission denied, stat '/requested/path'"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := (Call{Operation: Stat, Path: "/requested/path"}).Wrap(test.err)
			if got == nil || got.Error() != test.want {
				t.Errorf("Call{Operation: Stat}.Wrap(%v) = %v, want %q", test.err, got, test.want)
			}
			if !errors.Is(got, test.err) {
				t.Errorf("errors.Is(Call{Operation: Stat}.Wrap(%v), cause) = false, want true", test.err)
			}
		})
	}
}

func TestCallWrapLeavesUnsupportedErrorsUntouched(t *testing.T) {
	nonFilesystem := errors.New("domain failure")
	tests := []struct {
		name string
		call Call
		err  error
	}{
		{name: "nil", call: Call{Operation: ReadFile, Path: "/requested/path"}, err: nil},
		{name: "non_filesystem", call: Call{Operation: ReadFile, Path: "/requested/path"}, err: nonFilesystem},
		{name: "unmapped_errno", call: Call{Operation: ReadFile, Path: "/requested/path"}, err: syscall.EIO},
		{name: "unmapped_errno_does_not_collapse_to_portable_class", call: Call{Operation: ReadFile, Path: "/requested/path"}, err: syscall.EPERM},
		{name: "ENOTEMPTY_does_not_collapse_to_EEXIST", call: Call{Operation: ReadFile, Path: "/requested/path"}, err: syscall.ENOTEMPTY},
		{name: "unknown_operation", call: Call{Operation: 255, Path: "/requested/path"}, err: syscall.ENOENT},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := test.call.Wrap(test.err); got != test.err {
				t.Errorf("Call%+v.Wrap(%v) = %v, want original error %v", test.call, test.err, got, test.err)
			}
		})
	}
}

func requireDarwinLinuxNativeErrno(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("native errno translation is supported only on the Darwin/Linux release matrix")
	}
}
