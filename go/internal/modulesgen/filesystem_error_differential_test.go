package modulesgen

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

type moduleFilesystemOracleInput struct {
	Bundle      string `json:"bundle"`
	PacksRoot   string `json:"packsRoot"`
	ProfilePath string `json:"profilePath"`
	CatalogPath string `json:"catalogPath"`
	OutputRoot  string `json:"outputRoot"`
	Mode        string `json:"mode"`
}

type moduleFilesystemOracleResult struct {
	Error string `json:"error"`
}

type moduleFilesystemTreeEntry struct {
	Kind    string
	Content string
	Target  string
}

// TestModuleFilesystemFailuresMatchNode2415 executes the authoritative
// TypeScript generator/validator and Go against identical paths and trees.
// Every generated destination is faulted in sorted write order, and the
// partial trees are compared byte-for-byte. The mkdir cases cover the bare
// existing-file and dangling-symlink promises.mkdir shapes reachable after
// path.join; that join normalizes away trailing separators, so the separate
// nodefserr trailing-separator case is not applicable to this call site.
func TestModuleFilesystemFailuresMatchNode2415(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("live Node/Go path-byte differential is Unix-only")
	}
	repository := repoRoot(t)
	node, bundle := buildModuleFilesystemOracle(t, repository)
	root := committedRoot(t)

	base := t.TempDir()
	outputRoot := filepath.Join(base, "modules'output")
	resourceType := "zia_url_categories"
	moduleBase := filepath.Join(outputRoot, resourceType)
	profilePath := filepath.Join(repository, "packsets", "full.json")
	catalogPath := filepath.Join(repository, "packsets", "full.json")

	tests := []struct {
		name       string
		mode       string
		expectPath bool
		setup      func(*testing.T)
	}{
		{
			name:       "tests mkdir bare file",
			mode:       "generate",
			expectPath: true,
			setup: func(t *testing.T) {
				mustWriteModuleTest(t, filepath.Join(moduleBase, "tests"), "tests-file")
			},
		},
		{
			name:       "tests mkdir dangling symlink",
			mode:       "generate",
			expectPath: true,
			setup: func(t *testing.T) {
				mustMkdirModuleTest(t, moduleBase)
				link := filepath.Join(moduleBase, "tests")
				if err := os.Symlink("missing-tests-target", link); err != nil {
					t.Fatalf("os.Symlink(%q) error = %v, want nil", link, err)
				}
			},
		},
		{
			name:       "validation stat non-ENOENT",
			mode:       "validate",
			expectPath: true,
			setup: func(t *testing.T) {
				mustWriteModuleTest(t, moduleBase, "resource-file")
			},
		},
		{
			name: "validation stat ENOENT branch",
			mode: "validate",
			setup: func(*testing.T) {
			},
		},
	}
	for _, file := range ExpectedModuleFiles {
		relative := string(file)
		tests = append(tests, struct {
			name       string
			mode       string
			expectPath bool
			setup      func(*testing.T)
		}{
			name:       "write " + relative + " as directory",
			mode:       "generate",
			expectPath: true,
			setup: func(t *testing.T) {
				mustMkdirModuleTest(t, filepath.Join(moduleBase, filepath.FromSlash(relative)))
			},
		})
	}

	for _, test := range tests {
		t.Run(strings.ReplaceAll(test.name, "/", "_"), func(t *testing.T) {
			resetModuleFilesystemFixture(t, outputRoot)
			test.setup(t)
			input := moduleFilesystemOracleInput{
				Bundle:      bundle,
				PacksRoot:   filepath.Join(repository, "packs"),
				ProfilePath: profilePath,
				CatalogPath: catalogPath,
				OutputRoot:  outputRoot,
				Mode:        test.mode,
			}
			want := runModuleFilesystemOracle(t, node, repository, input)
			wantTree := snapshotModuleFilesystemTree(t, outputRoot)
			if want.Error == "" {
				t.Fatalf("Node 24.15 %s error = nil, want a filesystem or validation failure", test.name)
			}

			resetModuleFilesystemFixture(t, outputRoot)
			test.setup(t)
			var gotErr error
			switch test.mode {
			case "generate":
				_, gotErr = GenerateModule(root, resourceType, GenerateModuleOptions{
					OutputRoot: outputRoot,
					FormatHCL:  IdentityFormatter,
				})
			case "validate":
				_, gotErr = ValidateGeneratedModuleTree(outputRoot, []string{resourceType})
			default:
				t.Fatalf("unknown test mode %q", test.mode)
			}
			gotTree := snapshotModuleFilesystemTree(t, outputRoot)
			if gotErr == nil {
				t.Fatalf("%s(%s) error = nil, want Node 24.15 error %q", test.mode, test.name, want.Error)
			}
			if gotErr.Error() != want.Error {
				t.Errorf("%s(%s) error = %q, want Node 24.15 error %q", test.mode, test.name, gotErr.Error(), want.Error)
			}
			if !reflect.DeepEqual(gotTree, wantTree) {
				t.Errorf("%s(%s) partial tree mismatch\ngot:  %#v\nwant: %#v", test.mode, test.name, gotTree, wantTree)
			}
			if test.expectPath && !strings.Contains(want.Error, "'") {
				t.Errorf("Node 24.15 %s error = %q, want raw single-quoted path evidence", test.name, want.Error)
			}
		})
	}
}

func mustMkdirModuleTest(t *testing.T, directory string) {
	t.Helper()
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatalf("os.MkdirAll(%q) error = %v, want nil", directory, err)
	}
}

func mustWriteModuleTest(t *testing.T, file, content string) {
	t.Helper()
	mustMkdirModuleTest(t, filepath.Dir(file))
	if err := os.WriteFile(file, []byte(content), 0o644); err != nil {
		t.Fatalf("os.WriteFile(%q) error = %v, want nil", file, err)
	}
}

func resetModuleFilesystemFixture(t *testing.T, outputRoot string) {
	t.Helper()
	if err := os.RemoveAll(outputRoot); err != nil {
		t.Fatalf("os.RemoveAll(%q) error = %v, want nil", outputRoot, err)
	}
	mustMkdirModuleTest(t, outputRoot)
}

func snapshotModuleFilesystemTree(t *testing.T, root string) map[string]moduleFilesystemTreeEntry {
	t.Helper()
	tree := map[string]moduleFilesystemTreeEntry{}
	err := filepath.WalkDir(root, func(file string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(root, file)
		if err != nil {
			return err
		}
		if relative == "." {
			return nil
		}
		relative = filepath.ToSlash(relative)
		info, err := entry.Info()
		if err != nil {
			return err
		}
		switch {
		case info.Mode()&os.ModeSymlink != 0:
			target, err := os.Readlink(file)
			if err != nil {
				return err
			}
			tree[relative] = moduleFilesystemTreeEntry{Kind: "symlink", Target: target}
		case info.IsDir():
			tree[relative] = moduleFilesystemTreeEntry{Kind: "directory"}
		case info.Mode().IsRegular():
			content, err := os.ReadFile(file)
			if err != nil {
				return err
			}
			tree[relative] = moduleFilesystemTreeEntry{Kind: "file", Content: string(content)}
		default:
			tree[relative] = moduleFilesystemTreeEntry{Kind: info.Mode().String()}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("snapshot module filesystem tree %q error = %v, want nil", root, err)
	}
	return tree
}

func buildModuleFilesystemOracle(t *testing.T, repository string) (string, string) {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skipf("Node 24.15 modules differential unavailable: %v", err)
	}
	version, err := exec.Command(node, "--version").Output()
	if err != nil {
		t.Skipf("Node 24.15 modules differential unavailable: node --version: %v", err)
	}
	if got := strings.TrimSpace(string(version)); !strings.HasPrefix(got, "v24.15.") {
		t.Skipf("Node 24.15 modules differential requires v24.15.x, got %s", got)
	}
	esbuild := filepath.Join(repository, "node_modules", ".bin", "esbuild")
	if _, err := os.Stat(esbuild); err != nil {
		t.Skipf("Node modules differential requires the pinned esbuild install: %v", err)
	}

	temporary := t.TempDir()
	entryPath := filepath.Join(temporary, "modules-filesystem-oracle.ts")
	bundlePath := filepath.Join(temporary, "modules-filesystem-oracle.mjs")
	modulesSource := filepath.ToSlash(filepath.Join(repository, "node-src", "modules"))
	metadataSource := filepath.ToSlash(filepath.Join(repository, "node-src", "metadata"))
	entry := fmt.Sprintf(
		"export { generateModule, validateGeneratedModuleTree } from %q;\n"+
			"export { loadPackRoot } from %q;\n",
		modulesSource+"/generator.ts",
		metadataSource+"/loader.ts",
	)
	if err := os.WriteFile(entryPath, []byte(entry), 0o600); err != nil {
		t.Fatalf("os.WriteFile(%q) error = %v, want nil", entryPath, err)
	}
	command := exec.Command(
		esbuild, entryPath, "--bundle", "--platform=node", "--format=esm",
		"--outfile="+bundlePath,
	)
	command.Dir = repository
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("esbuild modules filesystem oracle error = %v, want nil; output:\n%s", err, output)
	}
	return node, bundlePath
}

func runModuleFilesystemOracle(t *testing.T, node, repository string, input moduleFilesystemOracleInput) moduleFilesystemOracleResult {
	t.Helper()
	encoded, err := json.Marshal(input)
	if err != nil {
		t.Fatalf("json.Marshal(moduleFilesystemOracleInput) error = %v, want nil", err)
	}
	const script = `
import { pathToFileURL } from "node:url";

let encodedInput = "";
for await (const chunk of process.stdin) encodedInput += chunk;
const input = JSON.parse(encodedInput);
const oracle = await import(pathToFileURL(input.bundle).href);
let message = "";
try {
  if (input.mode === "generate") {
    const root = await oracle.loadPackRoot({
      packsRoot: input.packsRoot,
      profilePath: input.profilePath,
      catalogPath: input.catalogPath,
    });
    await oracle.generateModule(root, "zia_url_categories", {
      outputRoot: input.outputRoot,
      formatHcl: async (source) => source,
    });
  } else if (input.mode === "validate") {
    await oracle.validateGeneratedModuleTree(input.outputRoot, ["zia_url_categories"]);
  } else {
    throw new Error("unknown mode " + input.mode);
  }
} catch (error) {
  message = error instanceof Error ? error.message : String(error);
}
process.stdout.write(JSON.stringify({ error: message }));
`
	command := exec.Command(node, "--input-type=module", "--eval", script)
	command.Dir = repository
	command.Stdin = bytes.NewReader(encoded)
	var stderr bytes.Buffer
	command.Stderr = &stderr
	output, err := command.Output()
	if err != nil {
		t.Fatalf("Node modules filesystem oracle error = %v, want nil; stderr:\n%s", err, stderr.String())
	}
	var result moduleFilesystemOracleResult
	if err := json.Unmarshal(output, &result); err != nil {
		t.Fatalf("json.Unmarshal(Node modules filesystem oracle %q) error = %v, want nil", output, err)
	}
	return result
}
