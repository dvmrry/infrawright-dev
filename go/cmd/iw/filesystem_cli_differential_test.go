package main

// Direct CLI filesystem-argument differential corpus. Node v24.15.0 is the
// byte oracle for the raw readFile/writeFile boundaries owned by root-catalog
// and scope-paths. Every case runs in mirrored sandboxes so relative argv text
// and the complete post-run filesystem tree can be compared without scrubbing.

import (
	"bytes"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const filesystemCLIOracleVersion = "v24.15.0"

type filesystemCLIPaths struct {
	directory        string
	existingFile     string
	missingFile      string
	missingOutput    string
	successfulOutput string
	trailingFile     string
}

func prepareFilesystemCLISandbox(t *testing.T, root string) filesystemCLIPaths {
	t.Helper()
	separator := string(filepath.Separator)
	oddComponent := `team's-"quoted"\raw`
	relativeDirectory := "." + separator + "literal" + separator + oddComponent
	absoluteDirectory := filepath.Join(root, "literal", oddComponent)
	if err := os.MkdirAll(absoluteDirectory, 0o755); err != nil {
		t.Fatalf("os.MkdirAll(%q) error = %v", absoluteDirectory, err)
	}
	if err := os.WriteFile(filepath.Join(absoluteDirectory, "existing.json"), []byte("fixture\n"), 0o644); err != nil {
		t.Fatalf("os.WriteFile(%q) error = %v", filepath.Join(absoluteDirectory, "existing.json"), err)
	}
	existingFile := relativeDirectory + separator + "existing.json"
	return filesystemCLIPaths{
		directory:        relativeDirectory,
		existingFile:     existingFile,
		missingFile:      relativeDirectory + separator + `missing's-"quoted"\raw.json`,
		missingOutput:    relativeDirectory + separator + "missing-parent" + separator + "out.json",
		successfulOutput: relativeDirectory + separator + `catalog-out's-"quoted"\raw.json`,
		trailingFile:     existingFile + separator,
	}
}

type filesystemCLITreeEntry struct {
	mode fs.FileMode
	data []byte
}

func snapshotFilesystemCLITree(t *testing.T, root string) map[string]filesystemCLITreeEntry {
	t.Helper()
	tree := make(map[string]filesystemCLITreeEntry)
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == root {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		treeEntry := filesystemCLITreeEntry{mode: info.Mode()}
		if info.Mode().IsRegular() {
			treeEntry.data, err = os.ReadFile(path)
			if err != nil {
				return err
			}
		}
		tree[filepath.ToSlash(relative)] = treeEntry
		return nil
	})
	if err != nil {
		t.Fatalf("snapshotFilesystemCLITree(%q) error = %v", root, err)
	}
	return tree
}

func compareFilesystemCLITrees(
	t *testing.T,
	arguments []string,
	want, got map[string]filesystemCLITreeEntry,
) {
	t.Helper()
	for path, wantEntry := range want {
		gotEntry, ok := got[path]
		if !ok {
			t.Errorf("filesystem CLI %v output tree is missing %q", arguments, path)
			continue
		}
		if gotEntry.mode != wantEntry.mode {
			t.Errorf("filesystem CLI %v output tree %q mode = %v, want Node %v", arguments, path, gotEntry.mode, wantEntry.mode)
		}
		if !bytes.Equal(gotEntry.data, wantEntry.data) {
			t.Errorf("filesystem CLI %v output tree %q bytes differ: Go length=%d Node length=%d", arguments, path, len(gotEntry.data), len(wantEntry.data))
		}
	}
	for path := range got {
		if _, ok := want[path]; !ok {
			t.Errorf("filesystem CLI %v output tree has unexpected %q", arguments, path)
		}
	}
}

func TestFilesystemCLIFileArgumentsDifferentialAgainstNode24(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("literal backslash filename oracle is Unix-specific")
	}
	root := repoRoot(t)
	oracleBundle := filepath.Join(root, "dist", "infrawright-cli.mjs")
	if _, err := os.Stat(oracleBundle); err != nil {
		t.Skipf("Node oracle bundle absent (%s); build it with `npm run build:metadata-cli`", oracleBundle)
	}
	nodeBinary, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not on PATH; the differential lane needs Node v24.15.0")
	}
	versionOutput, err := exec.Command(nodeBinary, "--version").Output()
	if err != nil {
		t.Fatalf("%s --version error = %v", nodeBinary, err)
	}
	if version := strings.TrimSpace(string(versionOutput)); version != filesystemCLIOracleVersion {
		t.Skipf("node version = %q, want byte oracle %q", version, filesystemCLIOracleVersion)
	}

	binaryPlaceholder, err := os.CreateTemp(filepath.Join(root, "dist"), "iw-go-diff-filesystem-cli-*")
	if err != nil {
		t.Fatalf("os.CreateTemp(%q) error = %v", filepath.Join(root, "dist"), err)
	}
	goBinary := binaryPlaceholder.Name()
	t.Cleanup(func() {
		if err := os.Remove(goBinary); err != nil && !os.IsNotExist(err) {
			t.Errorf("os.Remove(%q) error = %v", goBinary, err)
		}
	})
	if err := binaryPlaceholder.Close(); err != nil {
		t.Fatalf("closing Go CLI placeholder %q: %v", goBinary, err)
	}
	if err := os.Remove(goBinary); err != nil {
		t.Fatalf("os.Remove(%q) error = %v", goBinary, err)
	}
	build := exec.Command("go", "build", "-o", goBinary, ".")
	build.Dir = filepath.Join(root, "go", "cmd", "iw")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building Go CLI: %v\n%s", err, output)
	}

	tests := []struct {
		name           string
		arguments      func(filesystemCLIPaths) []string
		pathMustRender func(filesystemCLIPaths) string
	}{
		{
			name: "root_catalog_check_missing_literal_path",
			arguments: func(paths filesystemCLIPaths) []string {
				return []string{"root-catalog", "--providers", "zcc", "--check", paths.missingFile}
			},
			pathMustRender: func(paths filesystemCLIPaths) string { return paths.missingFile },
		},
		{
			name: "root_catalog_check_directory",
			arguments: func(paths filesystemCLIPaths) []string {
				return []string{"root-catalog", "--providers", "zcc", "--check", paths.directory}
			},
		},
		{
			name: "root_catalog_check_trailing_separator",
			arguments: func(paths filesystemCLIPaths) []string {
				return []string{"root-catalog", "--providers", "zcc", "--check", paths.trailingFile}
			},
			pathMustRender: func(paths filesystemCLIPaths) string { return paths.trailingFile },
		},
		{
			name: "root_catalog_out_missing_parent",
			arguments: func(paths filesystemCLIPaths) []string {
				return []string{"root-catalog", "--providers", "zcc", "--out", paths.missingOutput}
			},
			pathMustRender: func(paths filesystemCLIPaths) string { return paths.missingOutput },
		},
		{
			name: "root_catalog_out_directory",
			arguments: func(paths filesystemCLIPaths) []string {
				return []string{"root-catalog", "--providers", "zcc", "--out", paths.directory}
			},
			pathMustRender: func(paths filesystemCLIPaths) string { return paths.directory },
		},
		{
			name: "root_catalog_out_trailing_separator",
			arguments: func(paths filesystemCLIPaths) []string {
				return []string{"root-catalog", "--providers", "zcc", "--out", paths.trailingFile}
			},
			pathMustRender: func(paths filesystemCLIPaths) string { return paths.trailingFile },
		},
		{
			name: "root_catalog_out_literal_success",
			arguments: func(paths filesystemCLIPaths) []string {
				return []string{"root-catalog", "--providers", "zcc", "--out", paths.successfulOutput}
			},
		},
		{
			name: "scope_paths_missing_literal_path",
			arguments: func(paths filesystemCLIPaths) []string {
				return []string{"scope-paths", "--paths-json", paths.missingFile}
			},
			pathMustRender: func(paths filesystemCLIPaths) string { return paths.missingFile },
		},
		{
			name: "scope_paths_directory",
			arguments: func(paths filesystemCLIPaths) []string {
				return []string{"scope-paths", "--paths-json", paths.directory}
			},
		},
		{
			name: "scope_paths_trailing_separator",
			arguments: func(paths filesystemCLIPaths) []string {
				return []string{"scope-paths", "--paths-json", paths.trailingFile}
			},
			pathMustRender: func(paths filesystemCLIPaths) string { return paths.trailingFile },
		},
	}

	environment := []string{"INFRAWRIGHT_DEPLOYMENT=" + filepath.Join(root, "demo", "deployment.json")}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			nodeDirectory := t.TempDir()
			goDirectory := t.TempDir()
			nodePaths := prepareFilesystemCLISandbox(t, nodeDirectory)
			goPaths := prepareFilesystemCLISandbox(t, goDirectory)
			if nodePaths != goPaths {
				t.Fatalf("prepareFilesystemCLISandbox() paths differ: node=%+v go=%+v", nodePaths, goPaths)
			}
			arguments := test.arguments(nodePaths)
			oracle := runBinaryWithEnv(
				t,
				nodeDirectory,
				nodeBinary,
				append([]string{oracleBundle}, arguments...),
				environment,
			)
			candidate := runBinaryWithEnv(t, goDirectory, goBinary, arguments, environment)

			if oracle.exit != candidate.exit {
				t.Errorf("filesystem CLI %v exit = %d, want Node %d\nGo stderr: %q\nNode stderr: %q", arguments, candidate.exit, oracle.exit, candidate.stderr, oracle.stderr)
			}
			if !bytes.Equal(oracle.stdout, candidate.stdout) {
				t.Errorf("filesystem CLI %v stdout = %q, want Node %q", arguments, candidate.stdout, oracle.stdout)
			}
			if !bytes.Equal(oracle.stderr, candidate.stderr) {
				t.Errorf("filesystem CLI %v stderr = %q, want Node %q", arguments, candidate.stderr, oracle.stderr)
			}
			if test.pathMustRender != nil {
				path := test.pathMustRender(nodePaths)
				if !bytes.Contains(oracle.stderr, []byte(path)) {
					t.Errorf("Node filesystem CLI %v stderr = %q, want exact argv path %q", arguments, oracle.stderr, path)
				}
			}
			nodeTree := snapshotFilesystemCLITree(t, nodeDirectory)
			goTree := snapshotFilesystemCLITree(t, goDirectory)
			compareFilesystemCLITrees(t, arguments, nodeTree, goTree)
		})
	}
}
