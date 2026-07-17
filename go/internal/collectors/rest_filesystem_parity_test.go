package collectors

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
)

type mkdirOracleOutcome struct {
	OK    bool   `json:"ok"`
	Error string `json:"error"`
}

type mkdirTreeEntry struct {
	data string
	kind string
	link string
	mode fs.FileMode
}

type mkdirOutputFixture struct {
	output    string
	requested string
	denied    string
}

type mkdirParityCase struct {
	name                  string
	kind                  string
	suffix                string
	wantNodeCode          string
	wantPathFromOutput    bool
	wantPathOutputSuffix  string
	unsupportedFailClosed bool
}

func setupMkdirOutputCase(t *testing.T, root, kind, suffix string) mkdirOutputFixture {
	t.Helper()
	outputName := `output's-"quoted"\raw`
	if runtime.GOOS == "windows" {
		outputName = `output's-"quoted"-raw`
	}
	output := filepath.Join(root, outputName)
	middle := filepath.Join(root, "middle'link")
	outer := filepath.Join(root, "outer'link")
	missing := filepath.Join(root, "missing'referent")
	regular := filepath.Join(root, "existing'file")
	denied := filepath.Join(root, "denied'directory")
	permissionFixture := false
	switch kind {
	case "file":
		if err := os.WriteFile(output, []byte("existing\n"), 0o644); err != nil {
			t.Fatalf("os.WriteFile(%q) error = %v", output, err)
		}
	case "dangling-symlink":
		if err := os.Symlink(missing, output); err != nil {
			skipOrFailSymlink(t, output, err)
		}
	case "relative-dangling-symlink":
		if err := os.Symlink("missing'referent", output); err != nil {
			skipOrFailSymlink(t, output, err)
		}
	case "nested-dangling-symlink":
		if err := os.Symlink(filepath.Join(missing, "child"), output); err != nil {
			skipOrFailSymlink(t, output, err)
		}
	case "relative-nested-dangling-symlink":
		if err := os.Symlink(filepath.Join("missing'referent", "child"), output); err != nil {
			skipOrFailSymlink(t, output, err)
		}
	case "chained-dangling-symlink":
		if err := os.Symlink(missing, middle); err != nil {
			skipOrFailSymlink(t, middle, err)
		}
		if err := os.Symlink(middle, output); err != nil {
			skipOrFailSymlink(t, output, err)
		}
	case "relative-chained-dangling-symlink":
		if err := os.Symlink("missing'referent", middle); err != nil {
			skipOrFailSymlink(t, middle, err)
		}
		if err := os.Symlink("middle'link", output); err != nil {
			skipOrFailSymlink(t, output, err)
		}
	case "chained-nested-dangling-symlink":
		if err := os.Symlink(filepath.Join(missing, "child"), middle); err != nil {
			skipOrFailSymlink(t, middle, err)
		}
		if err := os.Symlink(middle, output); err != nil {
			skipOrFailSymlink(t, output, err)
		}
	case "relative-chained-nested-dangling-symlink":
		if err := os.Symlink(filepath.Join("missing'referent", "child"), middle); err != nil {
			skipOrFailSymlink(t, middle, err)
		}
		if err := os.Symlink("middle'link", output); err != nil {
			skipOrFailSymlink(t, output, err)
		}
	case "three-link-nested-dangling-symlink":
		if err := os.Symlink(filepath.Join(missing, "child"), outer); err != nil {
			skipOrFailSymlink(t, outer, err)
		}
		if err := os.Symlink(outer, middle); err != nil {
			skipOrFailSymlink(t, middle, err)
		}
		if err := os.Symlink(middle, output); err != nil {
			skipOrFailSymlink(t, output, err)
		}
	case "relative-three-link-nested-dangling-symlink":
		if err := os.Symlink(filepath.Join("missing'referent", "child"), outer); err != nil {
			skipOrFailSymlink(t, outer, err)
		}
		if err := os.Symlink("outer'link", middle); err != nil {
			skipOrFailSymlink(t, middle, err)
		}
		if err := os.Symlink("middle'link", output); err != nil {
			skipOrFailSymlink(t, output, err)
		}
	case "symlink-existing-file":
		if err := os.WriteFile(regular, []byte("existing referent\n"), 0o640); err != nil {
			t.Fatalf("os.WriteFile(%q) error = %v", regular, err)
		}
		if err := os.Symlink(regular, output); err != nil {
			skipOrFailSymlink(t, output, err)
		}
	case "relative-symlink-existing-file":
		if err := os.WriteFile(regular, []byte("existing referent\n"), 0o640); err != nil {
			t.Fatalf("os.WriteFile(%q) error = %v", regular, err)
		}
		if err := os.Symlink("existing'file", output); err != nil {
			skipOrFailSymlink(t, output, err)
		}
	case "chained-symlink-existing-file":
		if err := os.WriteFile(regular, []byte("existing referent\n"), 0o640); err != nil {
			t.Fatalf("os.WriteFile(%q) error = %v", regular, err)
		}
		if err := os.Symlink(regular, middle); err != nil {
			skipOrFailSymlink(t, middle, err)
		}
		if err := os.Symlink(middle, output); err != nil {
			skipOrFailSymlink(t, output, err)
		}
	case "relative-chained-symlink-existing-file":
		if err := os.WriteFile(regular, []byte("existing referent\n"), 0o640); err != nil {
			t.Fatalf("os.WriteFile(%q) error = %v", regular, err)
		}
		if err := os.Symlink("existing'file", middle); err != nil {
			skipOrFailSymlink(t, middle, err)
		}
		if err := os.Symlink("middle'link", output); err != nil {
			skipOrFailSymlink(t, output, err)
		}
	case "symlink-through-regular-file":
		if err := os.WriteFile(regular, []byte("existing referent\n"), 0o640); err != nil {
			t.Fatalf("os.WriteFile(%q) error = %v", regular, err)
		}
		if err := os.Symlink(filepath.Join(regular, "child"), output); err != nil {
			skipOrFailSymlink(t, output, err)
		}
	case "relative-symlink-through-regular-file":
		if err := os.WriteFile(regular, []byte("existing referent\n"), 0o640); err != nil {
			t.Fatalf("os.WriteFile(%q) error = %v", regular, err)
		}
		if err := os.Symlink(filepath.Join("existing'file", "child"), output); err != nil {
			skipOrFailSymlink(t, output, err)
		}
	case "symlink-through-denied-directory":
		permissionFixture = true
		if err := os.Mkdir(denied, 0o700); err != nil {
			t.Fatalf("os.Mkdir(%q) error = %v", denied, err)
		}
		if err := os.Symlink(filepath.Join(denied, "child"), output); err != nil {
			skipOrFailSymlink(t, output, err)
		}
		if err := os.Chmod(denied, 0o000); err != nil {
			t.Fatalf("os.Chmod(%q, 0000) error = %v", denied, err)
		}
	case "relative-symlink-through-denied-directory":
		permissionFixture = true
		if err := os.Mkdir(denied, 0o700); err != nil {
			t.Fatalf("os.Mkdir(%q) error = %v", denied, err)
		}
		if err := os.Symlink(filepath.Join("denied'directory", "child"), output); err != nil {
			skipOrFailSymlink(t, output, err)
		}
		if err := os.Chmod(denied, 0o000); err != nil {
			t.Fatalf("os.Chmod(%q, 0000) error = %v", denied, err)
		}
	case "self-loop":
		if err := os.Symlink(filepath.Base(output), output); err != nil {
			skipOrFailSymlink(t, output, err)
		}
	case "two-link-loop":
		if err := os.Symlink(filepath.Base(middle), output); err != nil {
			skipOrFailSymlink(t, output, err)
		}
		if err := os.Symlink(filepath.Base(output), middle); err != nil {
			skipOrFailSymlink(t, middle, err)
		}
	case "chained-self-loop":
		if err := os.Symlink(filepath.Base(middle), middle); err != nil {
			skipOrFailSymlink(t, middle, err)
		}
		if err := os.Symlink(filepath.Base(middle), output); err != nil {
			skipOrFailSymlink(t, output, err)
		}
	default:
		t.Fatalf("unknown mkdir output fixture kind %q", kind)
	}
	fixture := mkdirOutputFixture{output: output, requested: output + suffix}
	if permissionFixture {
		fixture.denied = denied
		t.Cleanup(func() {
			if err := os.Chmod(denied, 0o700); err != nil && !errors.Is(err, fs.ErrNotExist) {
				t.Errorf("restore os.Chmod(%q) error = %v", denied, err)
			}
		})
		if _, err := os.Stat(output); !errors.Is(err, syscall.EACCES) {
			if errors.Is(err, fs.ErrNotExist) {
				t.Skip("host privileges do not enforce the permission fixture's mode bits")
			}
			t.Fatalf("os.Stat(%q) permission fixture error = %v, want errors.Is(err, syscall.EACCES)", output, err)
		}
	}
	return fixture
}

func skipOrFailSymlink(t *testing.T, path string, err error) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skipf("os.Symlink(%q) unavailable on this Windows host: %v", path, err)
	}
	t.Fatalf("os.Symlink(%q) error = %v", path, err)
}

func expectedNodeMkdirError(code, requested string) string {
	description := map[string]string{
		"EEXIST":  "file already exists",
		"ENOENT":  "no such file or directory",
		"EACCES":  "permission denied",
		"ENOTDIR": "not a directory",
		"ELOOP":   "too many symbolic links encountered",
	}[code]
	if description == "" {
		return ""
	}
	return code + ": " + description + ", mkdir '" + requested + "'"
}

func makeMkdirFixtureReadableForSnapshot(t *testing.T, fixture mkdirOutputFixture) {
	t.Helper()
	if fixture.denied == "" {
		return
	}
	info, err := os.Lstat(fixture.denied)
	if err != nil {
		t.Fatalf("os.Lstat(%q) before snapshot error = %v", fixture.denied, err)
	}
	if got := info.Mode().Perm(); got != 0 {
		t.Errorf("permission fixture mode after mkdir = %#o, want 0000", got)
	}
	if err := os.Chmod(fixture.denied, 0o700); err != nil {
		t.Fatalf("os.Chmod(%q, 0700) for complete tree snapshot error = %v", fixture.denied, err)
	}
}

func runNodeMkdirOracle(t *testing.T, node, output string) mkdirOracleOutcome {
	t.Helper()
	// Node v24.15.0 commit 848430679556aed0bd073f2bc263331ad84fa119:
	// node:fs/promises.mkdir is the exact operation used by
	// node-src/collectors/rest.ts.
	const script = `
import { mkdir } from "node:fs/promises";

try {
  await mkdir(process.argv[1], { recursive: true });
  process.stdout.write(JSON.stringify({ ok: true, error: "" }));
} catch (error) {
  process.stdout.write(JSON.stringify({
    ok: false,
    error: error instanceof Error ? error.message : String(error),
  }));
}
`
	command := exec.Command(node, "--input-type=module", "--eval", script, output)
	var stderr bytes.Buffer
	command.Stderr = &stderr
	encoded, err := command.Output()
	if err != nil {
		t.Fatalf("Node mkdir oracle error = %v; stderr:\n%s", err, stderr.String())
	}
	var outcome mkdirOracleOutcome
	if err := json.Unmarshal(encoded, &outcome); err != nil {
		t.Fatalf("json.Unmarshal(Node mkdir oracle %q) error = %v", encoded, err)
	}
	return outcome
}

func snapshotMkdirTree(t *testing.T, root string) map[string]mkdirTreeEntry {
	t.Helper()
	tree := make(map[string]mkdirTreeEntry)
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return fmt.Errorf("walk %q: %w", path, walkErr)
		}
		if path == root {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return fmt.Errorf("relative path from %q to %q: %w", root, path, err)
		}
		info, err := entry.Info()
		if err != nil {
			return fmt.Errorf("entry info for %q: %w", path, err)
		}
		snapshot := mkdirTreeEntry{mode: info.Mode()}
		switch {
		case info.Mode()&os.ModeSymlink != 0:
			snapshot.kind = "symlink"
			snapshot.link, err = os.Readlink(path)
			if err != nil {
				return fmt.Errorf("read link %q: %w", path, err)
			}
			snapshot.link = strings.ReplaceAll(snapshot.link, root, "<root>")
		case info.IsDir():
			snapshot.kind = "directory"
		case info.Mode().IsRegular():
			snapshot.kind = "file"
			data, readErr := os.ReadFile(path)
			if readErr != nil {
				return fmt.Errorf("read file %q: %w", path, readErr)
			}
			snapshot.data = string(data)
		default:
			snapshot.kind = info.Mode().String()
		}
		tree[filepath.ToSlash(relative)] = snapshot
		return nil
	})
	if err != nil {
		t.Fatalf("snapshotMkdirTree(%q) error = %v", root, err)
	}
	return tree
}

func compareMkdirTrees(t *testing.T, want, got map[string]mkdirTreeEntry) {
	t.Helper()
	for path, wantEntry := range want {
		gotEntry, ok := got[path]
		if !ok {
			t.Errorf("mkdir output tree is missing %q", path)
			continue
		}
		if gotEntry != wantEntry {
			t.Errorf("mkdir output tree %q = %+v, want Node %+v", path, gotEntry, wantEntry)
		}
	}
	for path := range got {
		if _, ok := want[path]; !ok {
			t.Errorf("mkdir output tree has unexpected %q", path)
		}
	}
}

func TestMkdirFetchOutputDirectoryAgainstNode2415(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skipf("Node 24.15 mkdir differential unavailable: %v", err)
	}
	version, err := exec.Command(node, "--version").Output()
	if err != nil {
		t.Skipf("Node 24.15 mkdir differential unavailable: node --version: %v", err)
	}
	if got := strings.TrimSpace(string(version)); got != "v24.15.0" {
		t.Skipf("Node mkdir differential requires v24.15.0, got %s", got)
	}

	separator := string(filepath.Separator)
	tests := []mkdirParityCase{
		{name: "bare_existing_file", kind: "file", wantNodeCode: "EEXIST"},
		{name: "one_separator_existing_file", kind: "file", suffix: separator, wantNodeCode: "ENOTDIR"},
		{name: "multiple_separators_existing_file", kind: "file", suffix: separator + separator, wantNodeCode: "ENOTDIR"},

		{name: "bare_absolute_dangling_symlink", kind: "dangling-symlink", wantNodeCode: "ENOENT"},
		{name: "one_separator_absolute_dangling_symlink", kind: "dangling-symlink", suffix: separator},
		{name: "multiple_separators_absolute_dangling_symlink", kind: "dangling-symlink", suffix: separator + separator},
		{name: "bare_relative_dangling_symlink", kind: "relative-dangling-symlink", wantNodeCode: "ENOENT"},
		{name: "one_separator_relative_dangling_symlink", kind: "relative-dangling-symlink", suffix: separator},
		{name: "multiple_separators_relative_dangling_symlink", kind: "relative-dangling-symlink", suffix: separator + separator},

		{name: "one_separator_absolute_nested_dangling_symlink", kind: "nested-dangling-symlink", suffix: separator, wantNodeCode: "ENOTDIR", wantPathFromOutput: true},
		{name: "multiple_separators_absolute_nested_dangling_symlink", kind: "nested-dangling-symlink", suffix: separator + separator, wantNodeCode: "ENOTDIR", wantPathFromOutput: true},
		{name: "one_separator_relative_nested_dangling_symlink", kind: "relative-nested-dangling-symlink", suffix: separator, wantNodeCode: "ENOTDIR", wantPathFromOutput: true},
		{name: "multiple_separators_relative_nested_dangling_symlink", kind: "relative-nested-dangling-symlink", suffix: separator + separator, wantNodeCode: "ENOTDIR", wantPathFromOutput: true},

		{name: "bare_absolute_chained_dangling_symlink", kind: "chained-dangling-symlink", wantNodeCode: "ENOENT"},
		{name: "one_separator_absolute_chained_dangling_symlink", kind: "chained-dangling-symlink", suffix: separator, wantNodeCode: "ENOENT"},
		{name: "multiple_separators_absolute_chained_dangling_symlink", kind: "chained-dangling-symlink", suffix: separator + separator, wantNodeCode: "ENOENT"},
		{name: "bare_relative_chained_dangling_symlink", kind: "relative-chained-dangling-symlink", wantNodeCode: "ENOENT"},
		{name: "one_separator_relative_chained_dangling_symlink", kind: "relative-chained-dangling-symlink", suffix: separator, wantNodeCode: "ENOENT"},
		{name: "multiple_separators_relative_chained_dangling_symlink", kind: "relative-chained-dangling-symlink", suffix: separator + separator, wantNodeCode: "ENOENT"},

		{name: "one_separator_absolute_chained_nested_dangling_symlink", kind: "chained-nested-dangling-symlink", suffix: separator, wantNodeCode: "ENOENT"},
		{name: "multiple_separators_absolute_chained_nested_dangling_symlink", kind: "chained-nested-dangling-symlink", suffix: separator + separator, wantNodeCode: "ENOENT"},
		{name: "one_separator_relative_chained_nested_dangling_symlink", kind: "relative-chained-nested-dangling-symlink", suffix: separator, wantNodeCode: "ENOENT"},
		{name: "multiple_separators_relative_chained_nested_dangling_symlink", kind: "relative-chained-nested-dangling-symlink", suffix: separator + separator, wantNodeCode: "ENOENT"},

		{name: "one_separator_absolute_symlink_existing_file", kind: "symlink-existing-file", suffix: separator, wantNodeCode: "EEXIST"},
		{name: "multiple_separators_absolute_symlink_existing_file", kind: "symlink-existing-file", suffix: separator + separator, wantNodeCode: "EEXIST"},
		{name: "one_separator_relative_symlink_existing_file", kind: "relative-symlink-existing-file", suffix: separator, wantNodeCode: "EEXIST"},
		{name: "multiple_separators_relative_symlink_existing_file", kind: "relative-symlink-existing-file", suffix: separator + separator, wantNodeCode: "EEXIST"},
		{name: "one_separator_absolute_chained_symlink_existing_file", kind: "chained-symlink-existing-file", suffix: separator, wantNodeCode: "EEXIST"},
		{name: "multiple_separators_absolute_chained_symlink_existing_file", kind: "chained-symlink-existing-file", suffix: separator + separator, wantNodeCode: "EEXIST"},
		{name: "one_separator_relative_chained_symlink_existing_file", kind: "relative-chained-symlink-existing-file", suffix: separator, wantNodeCode: "EEXIST"},
		{name: "multiple_separators_relative_chained_symlink_existing_file", kind: "relative-chained-symlink-existing-file", suffix: separator + separator, wantNodeCode: "EEXIST"},

		{name: "absolute_dangling_intermediate_child", kind: "dangling-symlink", suffix: separator + "child", wantNodeCode: "ENOTDIR", wantPathFromOutput: true},
		{name: "absolute_dangling_intermediate_repeated_separator_child", kind: "dangling-symlink", suffix: separator + separator + "child"},
		{name: "relative_dangling_intermediate_child", kind: "relative-dangling-symlink", suffix: separator + "child", wantNodeCode: "ENOTDIR", wantPathFromOutput: true},
		{name: "relative_dangling_intermediate_repeated_separator_child", kind: "relative-dangling-symlink", suffix: separator + separator + "child"},
		{name: "absolute_chained_dangling_intermediate_child", kind: "chained-dangling-symlink", suffix: separator + "child", wantNodeCode: "ENOTDIR", wantPathFromOutput: true},
		{name: "absolute_chained_dangling_intermediate_repeated_separator_child", kind: "chained-dangling-symlink", suffix: separator + separator + "child", wantNodeCode: "ENOTDIR", wantPathFromOutput: true, wantPathOutputSuffix: separator},
		{name: "relative_chained_dangling_intermediate_child", kind: "relative-chained-dangling-symlink", suffix: separator + "child", wantNodeCode: "ENOTDIR", wantPathFromOutput: true},
		{name: "relative_chained_dangling_intermediate_repeated_separator_child", kind: "relative-chained-dangling-symlink", suffix: separator + separator + "child", wantNodeCode: "ENOTDIR", wantPathFromOutput: true, wantPathOutputSuffix: separator},

		{name: "absolute_regular_file_intermediate_child", kind: "symlink-through-regular-file", suffix: separator + "grandchild", wantNodeCode: "ENOTDIR"},
		{name: "absolute_regular_file_intermediate_trailing_child", kind: "symlink-through-regular-file", suffix: separator + "grandchild" + separator, wantNodeCode: "ENOTDIR"},
		{name: "absolute_regular_file_intermediate_repeated_separator_child", kind: "symlink-through-regular-file", suffix: separator + separator + "grandchild", wantNodeCode: "ENOTDIR"},
		{name: "relative_regular_file_intermediate_child", kind: "relative-symlink-through-regular-file", suffix: separator + "grandchild", wantNodeCode: "ENOTDIR"},
		{name: "relative_regular_file_intermediate_trailing_child", kind: "relative-symlink-through-regular-file", suffix: separator + "grandchild" + separator, wantNodeCode: "ENOTDIR"},
		{name: "relative_regular_file_intermediate_repeated_separator_child", kind: "relative-symlink-through-regular-file", suffix: separator + separator + "grandchild", wantNodeCode: "ENOTDIR"},

		{name: "absolute_denied_intermediate_child", kind: "symlink-through-denied-directory", suffix: separator + "grand", wantNodeCode: "EACCES"},
		{name: "absolute_denied_intermediate_trailing_child", kind: "symlink-through-denied-directory", suffix: separator + "grand" + separator, wantNodeCode: "EACCES"},
		{name: "absolute_denied_intermediate_repeated_separator_child", kind: "symlink-through-denied-directory", suffix: separator + separator + "grand", wantNodeCode: "EACCES"},
		{name: "relative_denied_intermediate_child", kind: "relative-symlink-through-denied-directory", suffix: separator + "grand", wantNodeCode: "EACCES"},
		{name: "relative_denied_intermediate_trailing_child", kind: "relative-symlink-through-denied-directory", suffix: separator + "grand" + separator, wantNodeCode: "EACCES"},
		{name: "relative_denied_intermediate_repeated_separator_child", kind: "relative-symlink-through-denied-directory", suffix: separator + separator + "grand", wantNodeCode: "EACCES"},

		{name: "bare_self_loop", kind: "self-loop", wantNodeCode: "ELOOP", unsupportedFailClosed: true},
		{name: "one_separator_self_loop", kind: "self-loop", suffix: separator, wantNodeCode: "ELOOP", unsupportedFailClosed: true},
		{name: "multiple_separators_self_loop", kind: "self-loop", suffix: separator + separator, wantNodeCode: "ELOOP", unsupportedFailClosed: true},
		{name: "intermediate_self_loop", kind: "self-loop", suffix: separator + "child", wantNodeCode: "ELOOP", unsupportedFailClosed: true},
		{name: "one_separator_two_link_loop", kind: "two-link-loop", suffix: separator, wantNodeCode: "ELOOP", unsupportedFailClosed: true},
		{name: "multiple_separators_two_link_loop", kind: "two-link-loop", suffix: separator + separator, wantNodeCode: "ELOOP", unsupportedFailClosed: true},
		{name: "intermediate_two_link_loop", kind: "two-link-loop", suffix: separator + "child", wantNodeCode: "ELOOP", unsupportedFailClosed: true},
		{name: "one_separator_chained_self_loop", kind: "chained-self-loop", suffix: separator, wantNodeCode: "ELOOP", unsupportedFailClosed: true},
		{name: "multiple_separators_chained_self_loop", kind: "chained-self-loop", suffix: separator + separator, wantNodeCode: "ELOOP", unsupportedFailClosed: true},
		{name: "intermediate_chained_self_loop", kind: "chained-self-loop", suffix: separator + "child", wantNodeCode: "ELOOP", unsupportedFailClosed: true},
	}
	nestedFamilies := []struct {
		name       string
		kind       string
		chainDepth int
	}{
		{name: "direct_absolute", kind: "nested-dangling-symlink", chainDepth: 1},
		{name: "direct_relative", kind: "relative-nested-dangling-symlink", chainDepth: 1},
		{name: "two_link_absolute", kind: "chained-nested-dangling-symlink", chainDepth: 2},
		{name: "two_link_relative", kind: "relative-chained-nested-dangling-symlink", chainDepth: 2},
		{name: "three_link_absolute", kind: "three-link-nested-dangling-symlink", chainDepth: 3},
		{name: "three_link_relative", kind: "relative-three-link-nested-dangling-symlink", chainDepth: 3},
	}
	nestedRequests := []struct {
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
	for _, family := range nestedFamilies {
		for _, request := range nestedRequests {
			suffix := strings.Repeat(separator, request.separators) + "grand"
			if request.trailing {
				suffix += separator
			}
			wantOutputSuffix := ""
			if family.chainDepth > 1 {
				wantOutputSuffix = strings.Repeat(separator, request.separators-1)
			}
			tests = append(tests, mkdirParityCase{
				name:                 "nested_intermediate_" + family.name + "_" + request.name,
				kind:                 family.kind,
				suffix:               suffix,
				wantNodeCode:         "ENOTDIR",
				wantPathFromOutput:   true,
				wantPathOutputSuffix: wantOutputSuffix,
			})
		}
	}
	if got, want := len(tests), 103; got != want {
		t.Fatalf("mkdir differential case count = %d, want %d", got, want)
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			nodeRoot := t.TempDir()
			goRoot := t.TempDir()
			nodeFixture := setupMkdirOutputCase(t, nodeRoot, test.kind, test.suffix)
			goFixture := setupMkdirOutputCase(t, goRoot, test.kind, test.suffix)

			oracle := runNodeMkdirOracle(t, node, nodeFixture.requested)
			oracleError := strings.ReplaceAll(oracle.Error, nodeRoot, "<root>")
			wantNodePath := nodeFixture.requested
			if test.wantPathFromOutput {
				wantNodePath = nodeFixture.output + test.wantPathOutputSuffix
			}
			wantNodeError := expectedNodeMkdirError(
				test.wantNodeCode,
				strings.ReplaceAll(wantNodePath, nodeRoot, "<root>"),
			)
			if wantNodeOK := test.wantNodeCode == ""; oracle.OK != wantNodeOK {
				t.Fatalf("Node mkdir(%q) success = %t, want pinned Node 24.15 success %t", nodeFixture.requested, oracle.OK, wantNodeOK)
			}
			if oracleError != wantNodeError {
				t.Fatalf("Node mkdir(%q) error = %q, want pinned Node 24.15 error %q", nodeFixture.requested, oracleError, wantNodeError)
			}

			candidateErr := mkdirFetchOutputDirectory(goFixture.requested)
			candidateError := ""
			if candidateErr != nil {
				candidateError = strings.ReplaceAll(candidateErr.Error(), goRoot, "<root>")
			}
			if gotOK := candidateErr == nil; gotOK != oracle.OK {
				t.Errorf("mkdirFetchOutputDirectory(%q) success = %t, want Node %t", goFixture.requested, gotOK, oracle.OK)
			}
			if !test.unsupportedFailClosed && candidateError != oracleError {
				t.Errorf("mkdirFetchOutputDirectory(%q) error = %q, want Node %q", goFixture.requested, candidateError, oracleError)
			}
			if test.unsupportedFailClosed {
				if candidateErr == nil {
					t.Errorf("mkdirFetchOutputDirectory(%q) error = nil, want unsupported ELOOP to fail closed", goFixture.requested)
				} else if errors.Is(candidateErr, fs.ErrNotExist) {
					t.Errorf("mkdirFetchOutputDirectory(%q) error = %v, must not coerce unsupported ELOOP to ENOENT", goFixture.requested, candidateErr)
				}
				rawRoot := t.TempDir()
				rawFixture := setupMkdirOutputCase(t, rawRoot, test.kind, test.suffix)
				rawErr := os.MkdirAll(rawFixture.requested, 0o777)
				rawError := ""
				if rawErr != nil {
					rawError = strings.ReplaceAll(rawErr.Error(), rawRoot, "<root>")
				}
				if candidateError != rawError {
					t.Errorf("mkdirFetchOutputDirectory(%q) unsupported error = %q, want unchanged raw os.MkdirAll error %q", goFixture.requested, candidateError, rawError)
				}
				compareMkdirTrees(t, snapshotMkdirTree(t, rawRoot), snapshotMkdirTree(t, goRoot))
			}

			makeMkdirFixtureReadableForSnapshot(t, nodeFixture)
			makeMkdirFixtureReadableForSnapshot(t, goFixture)
			nodeTree := snapshotMkdirTree(t, nodeRoot)
			goTree := snapshotMkdirTree(t, goRoot)
			compareMkdirTrees(t, nodeTree, goTree)
		})
	}
}
