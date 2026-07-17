package envgen

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

	"github.com/dvmrry/infrawright-dev/go/internal/deployment"
)

type envFilesystemOracleInput struct {
	Bundle      string  `json:"bundle"`
	PacksRoot   string  `json:"packsRoot"`
	ProfilePath string  `json:"profilePath"`
	CatalogPath string  `json:"catalogPath"`
	Workspace   string  `json:"workspace"`
	OutputRoot  string  `json:"outputRoot"`
	Backend     *string `json:"backend,omitempty"`
}

type envFilesystemOracleResult struct {
	Error string `json:"error"`
}

type envFilesystemTreeEntry struct {
	Kind    string
	Content string
	Target  string
}

// TestGenerateEnvironmentFilesystemFailuresMatchNode2415 compiles and runs
// the authoritative TypeScript generator for every raw filesystem boundary in
// GenerateEnvironmentRoots. Each Go run reuses the exact same paths and
// starting tree, then compares both the error bytes and the partially written
// tree. Bare existing-file and dangling-symlink mkdir targets pin the
// promises.mkdir recursive semantics exercised by these joined source paths;
// path.join never supplies a trailing separator here, so that nodefserr case
// is intentionally not manufactured at this call site.
func TestGenerateEnvironmentFilesystemFailuresMatchNode2415(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("envgen deliberately uses POSIX path composition; this live path-byte differential is Unix-only")
	}
	repository := repoRoot(t)
	node, bundle := buildEnvFilesystemOracle(t, repository)
	root := committedRootForTopology(t)

	base := t.TempDir()
	workspace := filepath.Join(base, "workspace'quoted")
	outputRoot := filepath.Join(workspace, "generated'output")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatalf("os.MkdirAll(%q) error = %v, want nil", workspace, err)
	}
	dep := deployment.Deployment{Overlay: workspace, Roots: map[string]deployment.RootProviderConfig{}}
	profilePath := filepath.Join(repository, "packsets", "full.json")
	catalogPath := filepath.Join(repository, "packsets", "full.json")

	tenantDirectory := filepath.Join(outputRoot, "tenant")
	rootDirectory := filepath.Join(tenantDirectory, "zia_url_categories")
	configDirectory := filepath.Join(workspace, "config", "tenant")
	configPath := filepath.Join(configDirectory, "zia_url_categories.auto.tfvars.json")
	bindingsPath := filepath.Join(configDirectory, "zia_url_categories.expressions.json")
	writeBindings := func(t *testing.T) {
		t.Helper()
		writeJSONFile(t, bindingsPath, map[string]any{
			"resources": map[string]any{
				"zia_url_categories.example": map[string]any{
					"configured_name": map[string]any{"expression": "var.replacement"},
				},
			},
		})
	}
	writeConfig := func(t *testing.T) {
		t.Helper()
		writeJSONFile(t, configPath, map[string]any{
			"items": map[string]any{
				"example": map[string]any{"configured_name": "Example"},
			},
		})
	}

	tests := []struct {
		name       string
		backend    *string
		expectPath bool
		setup      func(*testing.T)
	}{
		{
			name: "marker read directory",
			setup: func(t *testing.T) {
				mustMkdirEnvTest(t, filepath.Join(tenantDirectory, ".backend"))
			},
		},
		{
			name:       "tenant mkdir bare file",
			expectPath: true,
			setup: func(t *testing.T) {
				mustWriteEnvTest(t, tenantDirectory, "tenant-file")
			},
		},
		{
			name:       "tenant mkdir dangling symlink",
			expectPath: true,
			setup: func(t *testing.T) {
				if err := os.Symlink("missing-tenant-target", tenantDirectory); err != nil {
					t.Fatalf("os.Symlink(%q) error = %v, want nil", tenantDirectory, err)
				}
			},
		},
		{
			name:       "marker write directory",
			backend:    envStringPointer("azurerm"),
			expectPath: true,
			setup: func(t *testing.T) {
				mustMkdirEnvTest(t, filepath.Join(tenantDirectory, ".backend"))
			},
		},
		{
			name:       "root mkdir bare file",
			expectPath: true,
			setup: func(t *testing.T) {
				mustWriteEnvTest(t, rootDirectory, "root-file")
			},
		},
		{
			name:       "main write directory",
			expectPath: true,
			setup: func(t *testing.T) {
				mustMkdirEnvTest(t, filepath.Join(rootDirectory, "main.tf"))
			},
		},
		{
			name: "config read directory",
			setup: func(t *testing.T) {
				writeBindings(t)
				mustMkdirEnvTest(t, configPath)
			},
		},
		{
			name:       "expression write directory",
			expectPath: true,
			setup: func(t *testing.T) {
				writeBindings(t)
				writeConfig(t)
				mustMkdirEnvTest(t, filepath.Join(rootDirectory, expressionBindingsTF))
			},
		},
		{
			name:       "README write directory",
			expectPath: true,
			setup: func(t *testing.T) {
				mustMkdirEnvTest(t, filepath.Join(rootDirectory, "README.md"))
			},
		},
		{
			name:       "tests mkdir bare file",
			expectPath: true,
			setup: func(t *testing.T) {
				mustWriteEnvTest(t, filepath.Join(rootDirectory, "tests"), "tests-file")
			},
		},
		{
			name:       "smoke write directory",
			expectPath: true,
			setup: func(t *testing.T) {
				mustMkdirEnvTest(t, filepath.Join(rootDirectory, "tests", "smoke.tftest.hcl"))
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			resetEnvFilesystemFixture(t, outputRoot, filepath.Join(workspace, "config"))
			test.setup(t)
			oracleInput := envFilesystemOracleInput{
				Bundle:      bundle,
				PacksRoot:   filepath.Join(repository, "packs"),
				ProfilePath: profilePath,
				CatalogPath: catalogPath,
				Workspace:   workspace,
				OutputRoot:  outputRoot,
				Backend:     test.backend,
			}
			want := runEnvFilesystemOracle(t, node, repository, oracleInput)
			wantTree := snapshotEnvFilesystemTree(t, outputRoot)
			if want.Error == "" {
				t.Fatalf("Node 24.15 %s error = nil, want a filesystem failure", test.name)
			}

			resetEnvFilesystemFixture(t, outputRoot, filepath.Join(workspace, "config"))
			test.setup(t)
			_, gotErr := GenerateEnvironmentRoots(GenerateEnvironmentRootsOptions{
				Backend: test.backend, Deployment: dep, FormatHcl: identityFormatter,
				OutputRoot: &outputRoot, Root: root,
				Selectors: []string{"zia_url_categories"}, Tenant: "tenant",
			})
			gotTree := snapshotEnvFilesystemTree(t, outputRoot)
			if gotErr == nil {
				t.Fatalf("GenerateEnvironmentRoots(%s) error = nil, want Node 24.15 error %q", test.name, want.Error)
			}
			if gotErr.Error() != want.Error {
				t.Errorf("GenerateEnvironmentRoots(%s) error = %q, want Node 24.15 error %q", test.name, gotErr.Error(), want.Error)
			}
			if !reflect.DeepEqual(gotTree, wantTree) {
				t.Errorf("GenerateEnvironmentRoots(%s) partial tree mismatch\ngot:  %#v\nwant: %#v", test.name, gotTree, wantTree)
			}
			if test.expectPath && !strings.Contains(want.Error, "'") {
				t.Errorf("Node 24.15 %s error = %q, want raw single-quoted path evidence", test.name, want.Error)
			}
		})
	}
}

func envStringPointer(value string) *string { return &value }

func mustMkdirEnvTest(t *testing.T, directory string) {
	t.Helper()
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatalf("os.MkdirAll(%q) error = %v, want nil", directory, err)
	}
}

func mustWriteEnvTest(t *testing.T, file, content string) {
	t.Helper()
	mustMkdirEnvTest(t, filepath.Dir(file))
	if err := os.WriteFile(file, []byte(content), 0o644); err != nil {
		t.Fatalf("os.WriteFile(%q) error = %v, want nil", file, err)
	}
}

func resetEnvFilesystemFixture(t *testing.T, outputRoot, configRoot string) {
	t.Helper()
	if err := os.RemoveAll(outputRoot); err != nil {
		t.Fatalf("os.RemoveAll(%q) error = %v, want nil", outputRoot, err)
	}
	if err := os.RemoveAll(configRoot); err != nil {
		t.Fatalf("os.RemoveAll(%q) error = %v, want nil", configRoot, err)
	}
	mustMkdirEnvTest(t, outputRoot)
}

func snapshotEnvFilesystemTree(t *testing.T, root string) map[string]envFilesystemTreeEntry {
	t.Helper()
	tree := map[string]envFilesystemTreeEntry{}
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
			tree[relative] = envFilesystemTreeEntry{Kind: "symlink", Target: target}
		case info.IsDir():
			tree[relative] = envFilesystemTreeEntry{Kind: "directory"}
		case info.Mode().IsRegular():
			content, err := os.ReadFile(file)
			if err != nil {
				return err
			}
			tree[relative] = envFilesystemTreeEntry{Kind: "file", Content: string(content)}
		default:
			tree[relative] = envFilesystemTreeEntry{Kind: info.Mode().String()}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("snapshot filesystem tree %q error = %v, want nil", root, err)
	}
	return tree
}

func buildEnvFilesystemOracle(t *testing.T, repository string) (string, string) {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skipf("Node 24.15 envgen differential unavailable: %v", err)
	}
	version, err := exec.Command(node, "--version").Output()
	if err != nil {
		t.Skipf("Node 24.15 envgen differential unavailable: node --version: %v", err)
	}
	if got := strings.TrimSpace(string(version)); !strings.HasPrefix(got, "v24.15.") {
		t.Skipf("Node 24.15 envgen differential requires v24.15.x, got %s", got)
	}
	esbuild := filepath.Join(repository, "node_modules", ".bin", "esbuild")
	if _, err := os.Stat(esbuild); err != nil {
		t.Skipf("Node envgen differential requires the pinned esbuild install: %v", err)
	}

	temporary := t.TempDir()
	entryPath := filepath.Join(temporary, "envgen-filesystem-oracle.ts")
	bundlePath := filepath.Join(temporary, "envgen-filesystem-oracle.mjs")
	domainSource := filepath.ToSlash(filepath.Join(repository, "node-src", "domain"))
	metadataSource := filepath.ToSlash(filepath.Join(repository, "node-src", "metadata"))
	entry := fmt.Sprintf(
		"export { generateEnvironmentRoots } from %q;\n"+
			"export { loadPackRoot } from %q;\n",
		domainSource+"/environment-generator.ts",
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
		t.Fatalf("esbuild envgen filesystem oracle error = %v, want nil; output:\n%s", err, output)
	}
	return node, bundlePath
}

func runEnvFilesystemOracle(t *testing.T, node, repository string, input envFilesystemOracleInput) envFilesystemOracleResult {
	t.Helper()
	encoded, err := json.Marshal(input)
	if err != nil {
		t.Fatalf("json.Marshal(envFilesystemOracleInput) error = %v, want nil", err)
	}
	const script = `
import { pathToFileURL } from "node:url";

let encodedInput = "";
for await (const chunk of process.stdin) encodedInput += chunk;
const input = JSON.parse(encodedInput);
const oracle = await import(pathToFileURL(input.bundle).href);
let message = "";
try {
  const root = await oracle.loadPackRoot({
    packsRoot: input.packsRoot,
    profilePath: input.profilePath,
    catalogPath: input.catalogPath,
  });
  await oracle.generateEnvironmentRoots({
    ...(typeof input.backend === "string" ? { backend: input.backend } : {}),
    deployment: { overlay: input.workspace, roots: {} },
    formatHcl: async (source) => source,
    outputRoot: input.outputRoot,
    root,
    selectors: ["zia_url_categories"],
    tenant: "tenant",
  });
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
		t.Fatalf("Node envgen filesystem oracle error = %v, want nil; stderr:\n%s", err, stderr.String())
	}
	var result envFilesystemOracleResult
	if err := json.Unmarshal(output, &result); err != nil {
		t.Fatalf("json.Unmarshal(Node envgen filesystem oracle %q) error = %v, want nil", output, err)
	}
	return result
}
