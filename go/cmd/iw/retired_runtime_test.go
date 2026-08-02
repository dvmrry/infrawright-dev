package main

import (
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"
)

func isRetiredRuntimeFile(name string) bool {
	switch name {
	case "package.json", "package-lock.json", "pnpm-lock.yaml", "yarn.lock",
		"pyproject.toml", "requirements.txt", "setup.cfg", "tox.ini",
		"tsconfig.json", ".node-version":
		return true
	}
	// The repository tracks zero TypeScript, JavaScript, or Python source
	// files: the retired runtimes were removed wholesale, so ANY file with
	// these extensions is retired surface returning -- including the 21
	// historical node-tests/ files (JSON fixtures aside, caught by the
	// directory rule below) that a basename-only rule missed.
	for _, suffix := range []string{".ts", ".js", ".mjs", ".cjs", ".py"} {
		if strings.HasSuffix(name, suffix) {
			return true
		}
	}
	return false
}

// isRetiredRuntimeRoot reports directory names whose entire trees are
// retired surface: the historical runtime roots. node-tests, node-src, and
// .node-test have no legitimate future use at any depth; "engine" is only
// the retired Python engine when it sits at the repository root, since a
// future Go package could legitimately use the name deeper in the tree.
func isRetiredRuntimeRoot(relative, name string) bool {
	switch name {
	case "node-tests", "node-src", ".node-test":
		return true
	case "engine":
		return relative == "engine"
	default:
		return false
	}
}

func findRetiredRuntimeFiles(root string) ([]string, error) {
	var violations []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		if entry.IsDir() && (entry.Name() == ".git" || entry.Name() == ".claude") {
			return filepath.SkipDir
		}
		// Root names are violations whatever the entry type: WalkDir does
		// not follow symlinks, so a symlink named node-tests is not a
		// directory here, and a symlink (or plain file) wearing a retired
		// root's name restores the retired path surface just the same.
		if isRetiredRuntimeRoot(relative, entry.Name()) {
			violations = append(violations, relative)
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() || !isRetiredRuntimeFile(entry.Name()) {
			return nil
		}
		violations = append(violations, relative)
		return nil
	})
	sort.Strings(violations)
	return violations, err
}

func TestRetiredRuntimeStaysRetired(t *testing.T) {
	root := repoRoot(t)
	violations, err := findRetiredRuntimeFiles(root)
	if err != nil {
		t.Fatalf("findRetiredRuntimeFiles(%q) error = %v, want nil", root, err)
	}
	for _, path := range violations {
		t.Errorf("retired runtime surface reintroduced: %s", path)
	}
}

func TestRetiredRuntimeWalkerReportsNestedViolations(t *testing.T) {
	root := t.TempDir()
	for _, path := range []string{
		"node-src/__main__.py",
		"node-src/requirements.txt",
		"nested/manifests/package-lock.json",
		"nested/manifests/pnpm-lock.yaml",
		"nested/manifests/yarn.lock",
		"nested/runtime/.node-version",
		"nested/runtime/command.test.ts",
		"nested/runtime/pyproject.toml",
		"nested/runtime/setup.cfg",
		"nested/runtime/setup.py",
		"nested/runtime/tsconfig.json",
		"node-src/package.json",
		"tools/viewer.js",
		"tools/loader.mjs",
		"tools/shim.cjs",
		"vendor/.node-test",
	} {
		writeRetiredRuntimeFixtureFile(t, filepath.Join(root, filepath.FromSlash(path)))
	}
	// A symlink wearing a retired root's name restores the path surface
	// without ever being a directory in the walk.
	if err := os.Symlink(filepath.Join(root, "safe"), filepath.Join(root, "node-tests")); err != nil {
		t.Fatalf("os.Symlink(node-tests) error = %v, want nil", err)
	}
	if err := os.Symlink(filepath.Join(root, "safe"), filepath.Join(root, "engine")); err != nil {
		t.Fatalf("os.Symlink(engine) error = %v, want nil", err)
	}
	for _, path := range []string{
		".claude/scratch/engine/__main__.py",
		"internal/engine/config.json",
		".git/worktrees/node-src/package.json",
		"safe/package.json.md",
		"internal/engine/kernel.go",
		"tests/command.test.ts.golden",
	} {
		writeRetiredRuntimeFixtureFile(t, filepath.Join(root, filepath.FromSlash(path)))
	}

	// The historical roots report as single tree-level violations (their
	// contents are skipped once the root is flagged); loose files report
	// individually, including extension-rule catches with no manifest name.
	want := []string{
		"engine",
		"node-tests",
		"vendor/.node-test",
		"nested/manifests/package-lock.json",
		"nested/manifests/pnpm-lock.yaml",
		"nested/manifests/yarn.lock",
		"nested/runtime/.node-version",
		"nested/runtime/command.test.ts",
		"nested/runtime/pyproject.toml",
		"nested/runtime/setup.cfg",
		"nested/runtime/setup.py",
		"nested/runtime/tsconfig.json",
		"node-src",
		"tools/loader.mjs",
		"tools/shim.cjs",
		"tools/viewer.js",
	}
	sort.Strings(want)
	got, err := findRetiredRuntimeFiles(root)
	if err != nil {
		t.Fatalf("findRetiredRuntimeFiles(%q) error = %v, want nil", root, err)
	}
	if !slices.Equal(got, want) {
		t.Errorf("findRetiredRuntimeFiles(%q) = %q, want %q", root, got, want)
	}
}

func writeRetiredRuntimeFixtureFile(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("os.MkdirAll(%q) error = %v, want nil", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatalf("os.WriteFile(%q) error = %v, want nil", path, err)
	}
}
