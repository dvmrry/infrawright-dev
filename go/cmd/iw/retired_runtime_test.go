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
		"pyproject.toml", "requirements.txt", "setup.py", "setup.cfg", "tox.ini",
		"tsconfig.json", ".node-version", "__main__.py":
		return true
	default:
		return strings.HasSuffix(name, ".test.ts")
	}
}

func findRetiredRuntimeFiles(root string) ([]string, error) {
	var violations []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() && (entry.Name() == ".git" || entry.Name() == ".claude") {
			return filepath.SkipDir
		}
		if entry.IsDir() || !isRetiredRuntimeFile(entry.Name()) {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		violations = append(violations, filepath.ToSlash(relative))
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
	violations := []string{
		"engine/__main__.py",
		"engine/requirements.txt",
		"engine/tox.ini",
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
	}
	for _, path := range violations {
		writeRetiredRuntimeFixtureFile(t, filepath.Join(root, filepath.FromSlash(path)))
	}
	for _, path := range []string{
		".claude/scratch/engine/__main__.py",
		".git/worktrees/node-src/package.json",
		"safe/package.json.md",
		"engine/main.py",
		"tests/command.test.ts.golden",
	} {
		writeRetiredRuntimeFixtureFile(t, filepath.Join(root, filepath.FromSlash(path)))
	}

	want := append([]string(nil), violations...)
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
