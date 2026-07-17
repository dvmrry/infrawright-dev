// Package pypath ports node-src/domain/paths.ts: Python `os.path`-flavored
// POSIX path semantics (join/normpath/abspath/realpath) plus the
// containment helpers (pythonPathForms, pythonRelativeUnder,
// sameContractPath) built on top of them. These are the primitives the
// rest of the domain layer -- roots.ts's tenant/env-dir path joins chief
// among them -- uses instead of Node's own path.posix module, precisely
// because Node's normalize/relative do not reproduce CPython's posixpath
// edge cases (the exactly-two-leading-slashes rule, ".." handling at an
// absolute root, realpath(strict=False) semantics). This package is the Go
// analogue of that same deliberate divergence from the host runtime's path
// module.
//
// Every exported function below documents the node-src/domain/paths.ts
// function it ports; node-tests/paths.test.ts is this package's test-vector
// source (see paths_test.go).
package pypath

import (
	"os"
	"strings"

	"github.com/dvmrry/infrawright-dev/go/internal/canonjson"
)

// PythonPosixJoin ports pythonPosixJoin from node-src/domain/paths.ts: a
// plain left-to-right join of parts with "/" (never normalizing "." or
// ".." components, unlike Python's own os.path.join -- which this
// deliberately mirrors byte-for-byte, not the stdlib it's named after). Any
// part that starts with "/" resets the accumulator to that part (POSIX
// join's "absolute part replaces everything before it" rule); otherwise the
// part is appended, with a "/" separator inserted unless the accumulator is
// empty or already ends in "/".
func PythonPosixJoin(parts ...string) string {
	var result strings.Builder
	current := ""
	for _, part := range parts {
		if strings.HasPrefix(part, "/") {
			current = part
		} else if len(current) == 0 || strings.HasSuffix(current, "/") {
			current += part
		} else {
			current += "/" + part
		}
	}
	result.WriteString(current)
	return result.String()
}

// PythonPosixNormPath ports pythonPosixNormPath from
// node-src/domain/paths.ts: CPython's posixpath.normpath, including its
// exactly-two-leading-slashes rule (POSIX reserves "//foo" as
// implementation-defined, distinct from both "/foo" and "///foo", and
// CPython's normpath preserves that distinction; Node's own
// path.posix.normalize collapses all of them to a single leading "/",
// which is why this hand port exists instead of delegating to it).
func PythonPosixNormPath(value string) string {
	if len(value) == 0 {
		return "."
	}
	initialSlashes := 0
	if strings.HasPrefix(value, "/") {
		initialSlashes = 1
	}
	if strings.HasPrefix(value, "//") && !strings.HasPrefix(value, "///") {
		initialSlashes = 2
	}
	var components []string
	for _, component := range strings.Split(value, "/") {
		if len(component) == 0 || component == "." {
			continue
		}
		if component != ".." ||
			(initialSlashes == 0 && (len(components) == 0 || components[len(components)-1] == "..")) {
			components = append(components, component)
		} else if len(components) > 0 {
			components = components[:len(components)-1]
		}
	}
	normalized := strings.Repeat("/", initialSlashes) + strings.Join(components, "/")
	if normalized == "" {
		return "."
	}
	return normalized
}

// PythonPosixAbspath ports pythonPosixAbspath from
// node-src/domain/paths.ts: value normalized, joined onto cwd first if not
// already absolute.
func PythonPosixAbspath(value, cwd string) string {
	if strings.HasPrefix(value, "/") {
		return PythonPosixNormPath(value)
	}
	return PythonPosixNormPath(PythonPosixJoin(cwd, value))
}

// realpathTokenKind distinguishes the two work-queue entry shapes
// pythonPosixRealpath's port of the Node RealpathToken union carries: a
// path component still to be resolved, or a marker that pops one substituted
// symlink target off the in-flight loop-detection set (activeLinks) once
// every token that target expanded to has been consumed.
type realpathTokenKind int

const (
	realpathComponent realpathTokenKind = iota
	realpathLeaveLink
)

// realpathToken is the Go analogue of the RealpathToken union in
// node-src/domain/paths.ts: kind selects which of value (a path component)
// or path (the symlink whose expansion this token closes out) is
// meaningful.
type realpathToken struct {
	kind  realpathTokenKind
	value string
	path  string
}

func componentTokens(value string) []realpathToken {
	var tokens []realpathToken
	for _, component := range strings.Split(value, "/") {
		if len(component) == 0 {
			continue
		}
		tokens = append(tokens, realpathToken{kind: realpathComponent, value: component})
	}
	return tokens
}

// posixDirname returns the parent of p using the same rule Python's
// posixpath.dirname (and Node's path.posix.dirname) apply: everything
// before the last "/", or "/" itself if there is no parent to strip (p is
// "/" or has no further "/" after the leading one). PythonPosixRealpath is
// this function's only caller, and it only ever calls it on `resolved`,
// which by construction is always either exactly "/" or a single-leading-
// slash absolute path built purely from PythonPosixJoin appends -- so the
// general POSIX double-leading-slash distinction PythonPosixNormPath
// preserves elsewhere in this package never arises here.
func posixDirname(p string) string {
	if p == "/" {
		return "/"
	}
	index := strings.LastIndexByte(p, '/')
	if index <= 0 {
		return "/"
	}
	return p[:index]
}

// PythonPosixRealpath ports pythonPosixRealpath from
// node-src/domain/paths.ts: Python realpath(strict=False) semantics --
// resolve symlinks component by component, in POSIX order (so a symlink's
// own target can itself contain more symlinks, resolved before the
// components that followed the original link), leaving the deepest
// existing ancestor's real path intact and simply reattaching any missing
// leaf rather than requiring the final path to exist. A detected symlink
// loop (a candidate resolved while its own expansion is still in flight)
// is left unresolved at that point, matching CPython's non-strict
// behavior, rather than raising.
//
// Every os.Readlink failure -- ENOENT, EINVAL ("not a symlink"), EACCES,
// ELOOP, or anything else -- is folded into the same "leave this candidate
// as itself and keep going" branch, exactly like the Node source's single
// bare `catch` block: pythonPosixRealpath never distinguishes readlink
// failure causes, by design (a missing or non-link path component is not a
// realpath error, it's simply not expanded further).
func PythonPosixRealpath(absolutePath string) string {
	normalized := PythonPosixNormPath(absolutePath)
	resolved := "/"
	tokens := componentTokens(normalized)
	activeLinks := make(map[string]bool)
	for len(tokens) > 0 {
		token := tokens[0]
		tokens = tokens[1:]
		if token.kind == realpathLeaveLink {
			delete(activeLinks, token.path)
			continue
		}
		if token.value == "." {
			continue
		}
		if token.value == ".." {
			resolved = posixDirname(resolved)
			continue
		}
		candidate := PythonPosixJoin(resolved, token.value)
		target, err := os.Readlink(candidate)
		if err != nil {
			// Missing and non-link components stay in the non-strict
			// result. Keeping the already-resolved prefix also matches
			// Python for ELOOP/EACCES.
			resolved = candidate
			continue
		}
		if activeLinks[candidate] {
			// Python strict=False leaves a detected loop unresolved.
			resolved = candidate
			continue
		}
		activeLinks[candidate] = true
		if strings.HasPrefix(target, "/") {
			resolved = "/"
		}
		targetTokens := componentTokens(target)
		next := make([]realpathToken, 0, len(targetTokens)+1+len(tokens))
		next = append(next, targetTokens...)
		next = append(next, realpathToken{kind: realpathLeaveLink, path: candidate})
		next = append(next, tokens...)
		tokens = next
	}
	return PythonPosixNormPath(resolved)
}

// PhysicalWorkspace ports physicalWorkspace from
// node-src/domain/paths.ts: the fully symlink-resolved form of workspace,
// after normalizing it first.
func PhysicalWorkspace(workspace string) string {
	return PythonPosixRealpath(PythonPosixNormPath(workspace))
}

// PythonPathForms ports pythonPathForms from node-src/domain/paths.ts: the
// sorted, deduplicated set of a path's normalized form, its normalized
// absolute form (resolved against workspace's physical/symlink-free form),
// and that absolute form's own realpath -- the three shapes
// PythonRelativeUnder and SameContractPath both compare a candidate path
// against.
func PythonPathForms(value, workspace string) []string {
	normalized := PythonPosixNormPath(value)
	absolute := PythonPosixAbspath(normalized, PhysicalWorkspace(workspace))
	forms := map[string]struct{}{
		normalized:                    {},
		PythonPosixNormPath(absolute): {},
		PythonPosixRealpath(absolute): {},
	}
	unique := make([]string, 0, len(forms))
	for form := range forms {
		unique = append(unique, form)
	}
	return canonjson.SortedStrings(unique)
}

// posixRelative computes the POSIX-style relative path from the absolute,
// already-PythonPosixNormPath-normalized path `from` to the equally
// absolute and normalized path `to`, the same result Node's
// path.posix.relative(from, to) produces for two such inputs. It is not a
// general path.posix.relative port: it does not resolve its arguments
// against a working directory (path.posix.relative does, via an implicit
// path.posix.resolve, for any argument that is not already absolute) --
// PythonRelativeUnder's two call sites always pass absolute,
// already-normalized paths, so that resolution step is a no-op there and
// is deliberately not reproduced. It also compares paths by their POSIX
// components with leading empty segments (from a leading "/" or "//")
// discarded uniformly, rather than preserving PythonPosixNormPath's
// distinct one-vs-two-leading-slash forms the way Node's implementation
// technically would; this repository's workspace and artifact paths are
// exclusively single-leading-slash, so that distinction is unreachable at
// PythonRelativeUnder's call sites (see the "reviewer attention" note in
// the port's top-level report for this simplification's scope).
func posixRelative(from, to string) string {
	if from == to {
		return ""
	}
	fromParts := nonEmptySegments(from)
	toParts := nonEmptySegments(to)
	common := 0
	for common < len(fromParts) && common < len(toParts) && fromParts[common] == toParts[common] {
		common++
	}
	parts := make([]string, 0, (len(fromParts)-common)+(len(toParts)-common))
	for range fromParts[common:] {
		parts = append(parts, "..")
	}
	parts = append(parts, toParts[common:]...)
	return strings.Join(parts, "/")
}

func nonEmptySegments(p string) []string {
	raw := strings.Split(p, "/")
	out := make([]string, 0, len(raw))
	for _, segment := range raw {
		if segment != "" {
			out = append(out, segment)
		}
	}
	return out
}

// PythonRelativeUnder ports pythonRelativeUnder from
// node-src/domain/paths.ts: the path components of value relative to root
// (both resolved through workspace), or (nil, false) -- the Go analogue of
// the Node source's `null` return -- if no path-form pairing of value and
// root places value inside root. A value equal to root itself returns an
// empty, non-nil slice with ok=true (the Node source's `return []`), which
// callers must distinguish from the "not contained" (nil, false) case:
// both are falsy-ish in a naive check, but only the former means "value IS
// root".
//
// PhysicalWorkspace(workspace) is computed once up front rather than once
// per pythonPathForms/pythonPosixAbspath call the way the Node source
// literally re-invokes it (a pure function of workspace's on-disk symlink
// structure, evaluated repeatedly there only because nothing hoists it);
// this produces byte-identical results in a single call, assuming -- as
// the ported semantics already assume -- that the filesystem is not
// mutated mid-call.
func PythonRelativeUnder(value, root, workspace string) ([]string, bool) {
	physical := PhysicalWorkspace(workspace)
	for _, valueForm := range PythonPathForms(value, workspace) {
		for _, rootForm := range PythonPathForms(root, workspace) {
			valueAbsolute := strings.HasPrefix(valueForm, "/")
			rootAbsolute := strings.HasPrefix(rootForm, "/")
			if valueAbsolute != rootAbsolute {
				continue
			}
			var relativeBase, relativeValue string
			if valueAbsolute {
				relativeBase = rootForm
				relativeValue = valueForm
			} else {
				relativeBase = PythonPosixAbspath(rootForm, physical)
				relativeValue = PythonPosixAbspath(valueForm, physical)
			}
			relative := posixRelative(relativeBase, relativeValue)
			if relative == "" {
				relative = "."
			}
			if relative == "." {
				return []string{}, true
			}
			if relative == ".." || strings.HasPrefix(relative, "../") {
				continue
			}
			return strings.Split(relative, "/"), true
		}
	}
	return nil, false
}

// SameContractPath ports sameContractPath from node-src/domain/paths.ts:
// whether left and right (both resolved through workspace) share any
// PythonPathForms entry.
func SameContractPath(left, right, workspace string) bool {
	rightForms := PythonPathForms(right, workspace)
	rightSet := make(map[string]struct{}, len(rightForms))
	for _, form := range rightForms {
		rightSet[form] = struct{}{}
	}
	for _, form := range PythonPathForms(left, workspace) {
		if _, ok := rightSet[form]; ok {
			return true
		}
	}
	return false
}
