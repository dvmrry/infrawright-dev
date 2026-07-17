// Package nodefserr renders the narrow Node 24 filesystem SystemError surface
// used by the Go runtime port while retaining the original Go error chain.
package nodefserr

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// Operation identifies the filesystem call whose Node 24 error spelling must
// be reproduced.
type Operation uint8

const (
	// ReadFile identifies a Node fs.readFile call.
	ReadFile Operation = iota + 1
	// WriteFile identifies a Node fs.writeFile call.
	WriteFile
	// MkdirAll identifies a Node 24 node:fs/promises.mkdir call with recursive
	// enabled. It does not model the observably different mkdirSync recursion.
	MkdirAll
	// ReadDir identifies a Node fs.readdir call.
	ReadDir
	// Stat identifies a Node fs.stat call.
	Stat
	// Lstat identifies a Node fs.lstat call.
	Lstat
	// Rename identifies a Node fs.rename call.
	Rename
)

// Call records the operation and requested paths at the filesystem call
// boundary. Dest is used only by Rename.
type Call struct {
	Operation Operation
	Path      string
	Dest      string
}

// Wrap translates a supported direct filesystem-call error to its fixed Node
// 24 English spelling. It returns nil for a nil error and returns unsupported
// or already-wrapped errors unchanged. The returned compatibility error
// unwraps to err. For source-shaped direct MkdirAll errors, Wrap performs
// scoped read-only inspections where Go's userspace recursion loses the
// promises.mkdir-visible failure path or code. Unsupported or observably
// changing probe results fail closed by returning err unchanged. Classification
// necessarily assumes the relevant tree stays stable from the raw failure
// through those probes; the original filesystem call does not expose a state
// snapshot that a later Stat can bind to.
func (call Call) Wrap(err error) error {
	if err == nil {
		return nil
	}

	code := classify(err)
	if code == codeUnknown {
		return err
	}
	path := call.Path
	if call.Operation == MkdirAll {
		var supported bool
		code, path, supported = call.adjustMkdirAll(err, code)
		if !supported {
			return err
		}
	}

	systemCall, ok := call.Operation.systemCall(code)
	if !ok {
		return err
	}

	message := string(code) + ": " + code.description() + ", " + systemCall
	if call.Operation == ReadFile && code == codeEISDIR {
		return systemError{message: message, cause: err}
	}
	if call.Operation == Rename {
		message += " '" + call.Path + "' -> '" + call.Dest + "'"
	} else {
		message += " '" + path + "'"
	}
	return systemError{message: message, cause: err}
}

type systemError struct {
	message string
	cause   error
}

func (err systemError) Error() string {
	return err.message
}

func (err systemError) Unwrap() error {
	return err.cause
}

type errorCode string

const (
	codeUnknown errorCode = ""
	codeENOENT  errorCode = "ENOENT"
	codeEEXIST  errorCode = "EEXIST"
	codeEACCES  errorCode = "EACCES"
	codeENOTDIR errorCode = "ENOTDIR"
	codeEISDIR  errorCode = "EISDIR"
)

func classify(err error) errorCode {
	switch err := err.(type) {
	case *fs.PathError:
		if err == nil {
			return codeUnknown
		}
		return classifyDirect(err.Err)
	case *os.LinkError:
		if err == nil {
			return codeUnknown
		}
		return classifyDirect(err.Err)
	default:
		return classifyDirect(err)
	}
}

func classifyDirect(err error) errorCode {
	// Do not replace these exact checks with errors.Is or errors.As: Call.Wrap
	// belongs at the raw filesystem boundary and must not translate a domain
	// wrapper that happens to retain a filesystem cause. Native errno mapping
	// is release-platform-specific; unsupported platforms fail closed.
	if errno, ok := err.(syscall.Errno); ok {
		return classifyNativeErrno(errno)
	}

	switch err {
	case fs.ErrNotExist:
		return codeENOENT
	case fs.ErrExist:
		return codeEEXIST
	case fs.ErrPermission:
		return codeEACCES
	default:
		return codeUnknown
	}
}

func (code errorCode) description() string {
	switch code {
	case codeENOENT:
		return "no such file or directory"
	case codeEEXIST:
		return "file already exists"
	case codeEACCES:
		return "permission denied"
	case codeENOTDIR:
		return "not a directory"
	case codeEISDIR:
		return "illegal operation on a directory"
	default:
		return ""
	}
}

func (operation Operation) systemCall(code errorCode) (string, bool) {
	switch operation {
	case ReadFile:
		if code == codeEISDIR {
			return "read", true
		}
		return "open", true
	case WriteFile:
		return "open", true
	case MkdirAll:
		return "mkdir", true
	case ReadDir:
		return "scandir", true
	case Stat:
		return "stat", true
	case Lstat:
		return "lstat", true
	case Rename:
		return "rename", true
	default:
		return "", false
	}
}

// adjustMkdirAll handles source-pinned mismatches between Go's userspace
// os.MkdirAll recursion and Node's promises.mkdir recursion. It deliberately
// requires direct PathError shapes and returns false when read-only inspection
// cannot prove the equivalent Node code and path.
func (call Call) adjustMkdirAll(err error, code errorCode) (errorCode, string, bool) {
	pathErr, ok := err.(*fs.PathError)
	if !ok || pathErr == nil {
		return code, call.Path, true
	}

	if code == codeEEXIST {
		if pathErr.Op != "mkdir" {
			return code, call.Path, false
		}
		if pathErr.Path == call.Path {
			snapshotPath, snapshotOK := mkdirSymlinkSnapshotPath(call.Path)
			if snapshotOK {
				if followed, proved := observeStableMkdirSymlink(snapshotPath, call.Path); proved && followed.code != codeUnknown {
					return followed.code, call.Path, true
				}
			}
			return code, call.Path, false
		}

		// MkdirAll recurses through the requested spelling. A symlink used as an
		// intermediate component can make Go report EEXIST at that recursive
		// ancestor while promises.mkdir exposes the followed failure: ENOENT as
		// ENOTDIR at the ancestor, or ENOTDIR/EACCES at the full request. Bind
		// each adjustment to the exact ancestor and a stable read-only link
		// snapshot.
		if !isStrictMkdirAllAncestor(pathErr.Path, call.Path) {
			return code, call.Path, false
		}
		snapshotPath, snapshotOK := mkdirSymlinkSnapshotPath(pathErr.Path)
		if snapshotOK {
			if followed, proved := observeStableMkdirSymlink(snapshotPath, pathErr.Path); proved {
				switch followed.code {
				case codeENOENT:
					return codeENOTDIR, pathErr.Path, true
				case codeENOTDIR:
					return codeENOTDIR, call.Path, true
				case codeEACCES:
					return codeEACCES, call.Path, true
				}
			}
		}
		return code, call.Path, false
	}

	if code == codeENOENT && pathErr.Op == "mkdir" {
		// For a final link whose direct referent has a missing parent, Go reports
		// ENOENT at the requested trailing-separator spelling. promises.mkdir
		// instead reports ENOTDIR at the bare link component. A two-link dangling
		// chain has a different Go source shape (EEXIST), so it is handled above.
		if pathErr.Path == call.Path && hasTrailingPathSeparator(call.Path) {
			snapshotPath, snapshotOK := mkdirSymlinkSnapshotPath(call.Path)
			if snapshotOK {
				if followed, proved := observeStableMkdirSymlink(snapshotPath, call.Path); proved && followed.code == codeENOENT {
					return codeENOTDIR, snapshotPath, true
				}
			}
		}

		// With repeated separators after a direct nested dangling link,
		// os.MkdirAll can report ENOENT at a trailing-separator recursive
		// ancestor. Node reports ENOTDIR at the bare active link. Require the
		// exact recursive spelling and a stable read-only link observation; an
		// ambiguous source-shaped failure must remain untouched.
		if pathErr.Path != call.Path && hasTrailingPathSeparator(pathErr.Path) && isStrictMkdirAllAncestor(pathErr.Path, call.Path) {
			snapshotPath, snapshotOK := mkdirSymlinkSnapshotPath(pathErr.Path)
			if snapshotOK {
				if followed, proved := observeStableMkdirSymlink(snapshotPath, pathErr.Path); proved && followed.code == codeENOENT {
					return codeENOTDIR, snapshotPath, true
				}
			}
			return code, call.Path, false
		}
	}

	if code == codeEACCES {
		if pathErr.Op != "mkdir" {
			return code, call.Path, false
		}
		if pathErr.Path == call.Path {
			return code, call.Path, true
		}
		if !isStrictPathAncestor(pathErr.Path, call.Path) {
			// A repeated separator can leave a trailing separator on the
			// recursive ancestor reported by os.MkdirAll. Admit that spelling
			// only when it names a stable symlink and two full-request Stat
			// observations independently prove EACCES.
			if !isStrictMkdirAllAncestor(pathErr.Path, call.Path) {
				return code, call.Path, false
			}
			snapshotPath, snapshotOK := mkdirSymlinkSnapshotPath(pathErr.Path)
			if !snapshotOK {
				return code, call.Path, false
			}
			followed, proved := observeStableMkdirSymlink(snapshotPath, call.Path)
			if !proved || followed.code != codeEACCES {
				return code, call.Path, false
			}
			return code, call.Path, true
		}

		// A searchable but non-writable ancestor lets traversal reach the
		// missing component: Stat reports ENOENT and Node names that active
		// component. A non-searchable ancestor rejects the original full-path
		// traversal: Stat reports EACCES and Node keeps the requested target.
		// Require two matching probes so a changing tree fails closed.
		statCode, stable := stableStatErrorCode(call.Path, os.Stat)
		switch {
		case stable && statCode == codeENOENT:
			return code, pathErr.Path, true
		case stable && statCode == codeEACCES:
			return code, call.Path, true
		default:
			return code, call.Path, false
		}
	}

	if pathErr.Op != "mkdir" || pathErr.Path != call.Path {
		return code, call.Path, true
	}

	// When a final symlink resolves directly to an existing non-directory,
	// Stat of the bare link succeeds even though the trailing-separator Mkdir
	// call reports ENOTDIR. promises.mkdir renders EEXIST at the exact requested
	// spelling.
	// A link whose target traverses through a non-directory instead yields a
	// stable ENOTDIR Stat and keeps the ordinary ENOTDIR result below.
	if code == codeENOTDIR && hasTrailingPathSeparator(call.Path) {
		snapshotPath, snapshotOK := mkdirSymlinkSnapshotPath(call.Path)
		if snapshotOK {
			if followed, proved := observeStableMkdirSymlink(snapshotPath, snapshotPath); proved && followed.existingNonDirectory {
				return codeEEXIST, call.Path, true
			}
		}
	}

	// A trailing separator asks for a directory traversal. Node 24.15 and the
	// source-pinned Go error shape report ENOTDIR for an existing regular-file
	// target, so it is not the bare-target ENOTDIR that Node spells EEXIST.
	if hasTrailingPathSeparator(call.Path) {
		return code, call.Path, true
	}

	if code == codeENOTDIR {
		return codeEEXIST, call.Path, true
	}
	return code, call.Path, true
}

func hasTrailingPathSeparator(path string) bool {
	return len(path) > 0 && os.IsPathSeparator(path[len(path)-1])
}

// isStrictPathAncestor accepts only a component-boundary lexical ancestor.
// Roots and volume-only paths are deliberately ambiguous and fail closed.
func isStrictPathAncestor(ancestor, path string) bool {
	if ancestor == "" || ancestor == path {
		return false
	}
	if hasTrailingPathSeparator(ancestor) || filepath.VolumeName(ancestor) == ancestor {
		return false
	}
	if !strings.HasPrefix(path, ancestor) || len(path) <= len(ancestor) {
		return false
	}
	return os.IsPathSeparator(path[len(ancestor)])
}

// isStrictMkdirAllAncestor also accepts the repeated-separator spelling that
// os.MkdirAll's recursive parent scan can report (for example, "link/" for
// "link//child"). The exact reported bytes must remain a strict prefix and
// the next requested byte must still be a separator.
func isStrictMkdirAllAncestor(ancestor, path string) bool {
	if isStrictPathAncestor(ancestor, path) {
		return true
	}
	if ancestor == "" || ancestor == path || !hasTrailingPathSeparator(ancestor) {
		return false
	}
	trimmed, ok := mkdirSymlinkSnapshotPath(ancestor)
	if !ok || trimmed == ancestor || !strings.HasPrefix(path, ancestor) || len(path) <= len(ancestor) {
		return false
	}
	return os.IsPathSeparator(path[len(ancestor)])
}

// stableStatErrorCode requires two direct, matching filesystem observations.
func stableStatErrorCode(path string, stat func(string) (fs.FileInfo, error)) (errorCode, bool) {
	_, firstErr := stat(path)
	first := classify(firstErr)
	if first == codeUnknown {
		return codeUnknown, false
	}

	_, secondErr := stat(path)
	second := classify(secondErr)
	return first, first == second
}

type mkdirSymlinkFollow struct {
	code                 errorCode
	existingNonDirectory bool
}

type mkdirSymlinkSnapshot struct {
	info   fs.FileInfo
	target string
}

// observeStableMkdirSymlink brackets two matching Stat observations with
// matching Lstat and Readlink snapshots. snapshotPath names the symlink itself.
// statPath is either the exact source-reported spelling whose failed traversal
// Node exposes or that same bare link spelling when proving an existing final
// referent. No referent path is constructed and no mutation or retry of the
// failed MkdirAll is performed. Unsupported, replaced, or observably changing
// links fail closed.
func observeStableMkdirSymlink(snapshotPath, statPath string) (mkdirSymlinkFollow, bool) {
	before, ok := readMkdirSymlinkSnapshot(snapshotPath)
	if !ok {
		return mkdirSymlinkFollow{}, false
	}

	firstInfo, firstErr := os.Stat(statPath)
	secondInfo, secondErr := os.Stat(statPath)
	follow, stable := matchingMkdirSymlinkFollow(firstInfo, firstErr, secondInfo, secondErr)
	if !stable {
		return mkdirSymlinkFollow{}, false
	}

	after, ok := readMkdirSymlinkSnapshot(snapshotPath)
	if !ok || !os.SameFile(before.info, after.info) || before.target != after.target {
		return mkdirSymlinkFollow{}, false
	}
	return follow, true
}

func readMkdirSymlinkSnapshot(path string) (mkdirSymlinkSnapshot, bool) {
	info, err := os.Lstat(path)
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		return mkdirSymlinkSnapshot{}, false
	}
	target, err := os.Readlink(path)
	if err != nil {
		return mkdirSymlinkSnapshot{}, false
	}
	return mkdirSymlinkSnapshot{info: info, target: target}, true
}

func matchingMkdirSymlinkFollow(firstInfo fs.FileInfo, firstErr error, secondInfo fs.FileInfo, secondErr error) (mkdirSymlinkFollow, bool) {
	if firstErr == nil || secondErr == nil {
		if firstErr != nil || secondErr != nil || firstInfo == nil || secondInfo == nil {
			return mkdirSymlinkFollow{}, false
		}
		if firstInfo.IsDir() || secondInfo.IsDir() || !os.SameFile(firstInfo, secondInfo) {
			return mkdirSymlinkFollow{}, false
		}
		return mkdirSymlinkFollow{existingNonDirectory: true}, true
	}

	firstCode := classify(firstErr)
	secondCode := classify(secondErr)
	if firstCode != secondCode {
		return mkdirSymlinkFollow{}, false
	}
	switch firstCode {
	case codeENOENT, codeENOTDIR, codeEACCES:
		return mkdirSymlinkFollow{code: firstCode}, true
	default:
		return mkdirSymlinkFollow{}, false
	}
}

// mkdirSymlinkSnapshotPath removes trailing separators to name the link itself,
// never its referent. A root or volume-only result is not a final symlink
// component and fails closed.
func mkdirSymlinkSnapshotPath(path string) (string, bool) {
	end := len(path)
	volumeLength := len(filepath.VolumeName(path))
	for end > volumeLength && os.IsPathSeparator(path[end-1]) {
		end--
	}
	if end == 0 || end == volumeLength {
		return "", false
	}
	return path[:end], true
}
