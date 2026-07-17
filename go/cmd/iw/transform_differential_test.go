package main

// Transform differential corpus: run the Node oracle and the Go binary on
// identical inputs under separate temp overlay deployments and require
// byte-identical stdout/stderr, equal exit codes, and byte-identical full
// output trees. The demo inputs are the committed
// packs/_shared/zscaler/demo pulls — the same corpus `make demo` and
// check-demo gate against.

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func writeTransformDeployment(t *testing.T, dir, overlay string) string {
	t.Helper()
	payload, err := json.Marshal(map[string]string{
		"overlay":    overlay,
		"module_dir": filepath.Join(overlay, "modules"),
	})
	if err != nil {
		t.Fatal(err)
	}
	deploymentPath := filepath.Join(dir, "deployment.json")
	if err := os.WriteFile(deploymentPath, append(payload, '\n'), 0o666); err != nil {
		t.Fatal(err)
	}
	return deploymentPath
}

func treeBytes(t *testing.T, root string) map[string][]byte {
	t.Helper()
	output := map[string][]byte{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		content, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		relative, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		output[filepath.ToSlash(relative)] = content
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return output
}

// copyDemoInputs copies the demo pull corpus into a temp dir, optionally
// transforming one file's bytes.
func copyDemoInputs(
	t *testing.T,
	source string,
	replace map[string]func([]byte) []byte,
) string {
	t.Helper()
	target := t.TempDir()
	entries, err := os.ReadDir(source)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		content, err := os.ReadFile(filepath.Join(source, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if mutate, ok := replace[entry.Name()]; ok {
			content = mutate(content)
		}
		if err := os.WriteFile(filepath.Join(target, entry.Name()), content, 0o666); err != nil {
			t.Fatal(err)
		}
	}
	return target
}

// injectField adds one unacknowledged field to every item of a pull list,
// producing `dropped` diagnostics downstream.
func injectField(name string) func([]byte) []byte {
	return func(content []byte) []byte {
		var items []map[string]any
		if err := json.Unmarshal(content, &items); err != nil {
			panic(err)
		}
		for _, item := range items {
			item[name] = "differential-probe"
		}
		mutated, err := json.Marshal(items)
		if err != nil {
			panic(err)
		}
		return mutated
	}
}

func runBinaryWithEnv(
	t *testing.T,
	dir, argv0 string,
	args []string,
	extraEnv []string,
) runResult {
	t.Helper()
	command := exec.Command(argv0, args...)
	command.Dir = dir
	command.Env = append([]string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + os.Getenv("HOME"),
		"TMPDIR=" + os.Getenv("TMPDIR"),
	}, extraEnv...)
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()
	exit := 0
	if exitError, ok := err.(*exec.ExitError); ok {
		exit = exitError.ExitCode()
	} else if err != nil {
		t.Fatalf("running %s %v: %v", argv0, args, err)
	}
	return runResult{exit: exit, stdout: stdout.Bytes(), stderr: stderr.Bytes()}
}

func TestTransformDifferentialAgainstNodeOracle(t *testing.T) {
	root := repoRoot(t)
	oracleBundle := filepath.Join(root, "dist", "infrawright-cli.mjs")
	if _, err := os.Stat(oracleBundle); err != nil {
		t.Skipf("Node oracle bundle absent (%s); build it with `npm run build:metadata-cli`", oracleBundle)
	}
	nodeBinary, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not on PATH; the differential lane needs the pinned Node 24")
	}
	goBinary := filepath.Join(root, "dist", "iw-go-diff-transform")
	build := exec.Command("go", "build", "-o", goBinary, ".")
	build.Dir = filepath.Join(root, "go", "cmd", "iw")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building Go CLI: %v\n%s", err, output)
	}
	t.Cleanup(func() { os.Remove(goBinary) })

	demoInput := filepath.Join(root, "packs", "_shared", "zscaler", "demo")

	cases := []struct {
		name      string
		selectors []string
		extraEnv  []string
		input     func(t *testing.T) string
	}{
		{name: "demo-full"},
		{name: "demo-subset", selectors: []string{"zcc_web_privacy", "zia_rule_labels"}},
		{name: "demo-provider-selector", selectors: []string{"zcc"}},
		{
			name: "drops-diagnostics",
			input: func(t *testing.T) string {
				return copyDemoInputs(t, demoInput, map[string]func([]byte) []byte{
					"zcc_web_privacy.json": injectField("reviewUnexpectedField"),
				})
			},
		},
		{
			name:     "drops-check-exit",
			extraEnv: []string{"DROPS_CHECK=1"},
			input: func(t *testing.T) string {
				return copyDemoInputs(t, demoInput, map[string]func([]byte) []byte{
					"zcc_web_privacy.json": injectField("reviewUnexpectedField"),
				})
			},
		},
		{name: "missing-input-dir", input: func(t *testing.T) string { return t.TempDir() }},
		{
			name: "invalid-input-json",
			input: func(t *testing.T) string {
				return copyDemoInputs(t, demoInput, map[string]func([]byte) []byte{
					"zcc_web_privacy.json": func([]byte) []byte { return []byte("{ not json\n") },
				})
			},
		},
		{
			name: "envelope-not-list",
			input: func(t *testing.T) string {
				return copyDemoInputs(t, demoInput, map[string]func([]byte) []byte{
					"zcc_web_privacy.json": func([]byte) []byte { return []byte("{\"items\": []}\n") },
				})
			},
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			input := demoInput
			if testCase.input != nil {
				input = testCase.input(t)
			}
			nodeDir, goDir := t.TempDir(), t.TempDir()
			nodeOverlay := filepath.Join(nodeDir, "out")
			goOverlay := filepath.Join(goDir, "out")
			nodeDeployment := writeTransformDeployment(t, nodeDir, nodeOverlay)
			goDeployment := writeTransformDeployment(t, goDir, goOverlay)

			arguments := []string{
				"transform", "--in", input, "--tenant", "demo",
				"--profile", "packsets/full.json", "--catalog", "packsets/full.json",
			}
			for _, selector := range testCase.selectors {
				arguments = append(arguments, "--resource", selector)
			}

			oracle := runBinaryWithEnv(t, root, nodeBinary,
				append([]string{oracleBundle}, arguments...),
				append([]string{"INFRAWRIGHT_DEPLOYMENT=" + nodeDeployment}, testCase.extraEnv...))
			candidate := runBinaryWithEnv(t, root, goBinary, arguments,
				append([]string{"INFRAWRIGHT_DEPLOYMENT=" + goDeployment}, testCase.extraEnv...))

			if oracle.exit != candidate.exit {
				t.Errorf("exit: node=%d go=%d\nnode stderr:\n%s\ngo stderr:\n%s",
					oracle.exit, candidate.exit, oracle.stderr, candidate.stderr)
			}
			if !bytes.Equal(oracle.stdout, candidate.stdout) {
				t.Errorf("stdout diverges\nnode: %q\ngo: %q", oracle.stdout, candidate.stdout)
			}
			nodeStderr := strings.ReplaceAll(string(oracle.stderr), nodeDeployment, "<deployment>")
			nodeStderr = strings.ReplaceAll(nodeStderr, nodeOverlay, "<overlay>")
			goStderr := strings.ReplaceAll(string(candidate.stderr), goDeployment, "<deployment>")
			goStderr = strings.ReplaceAll(goStderr, goOverlay, "<overlay>")
			if nodeStderr != goStderr {
				t.Errorf("stderr diverges\nnode:\n%s\ngo:\n%s", nodeStderr, goStderr)
			}

			nodeTree := treeBytes(t, nodeOverlay)
			goTree := treeBytes(t, goOverlay)
			for relative, nodeContent := range nodeTree {
				goContent, ok := goTree[relative]
				if !ok {
					t.Errorf("go output missing %s", relative)
					continue
				}
				if !bytes.Equal(nodeContent, goContent) {
					t.Errorf("artifact %s diverges (node %d bytes, go %d bytes)",
						relative, len(nodeContent), len(goContent))
				}
			}
			for relative := range goTree {
				if _, ok := nodeTree[relative]; !ok {
					t.Errorf("go output has extra file %s", relative)
				}
			}
		})
	}
}
