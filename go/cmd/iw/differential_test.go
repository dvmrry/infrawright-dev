package main

// The first cut of the Node-oracle differential harness from
// docs/go-runtime-plan.md: run the built Node CLI bundle and this Go binary
// on the same argv/env/cwd and require byte-identical stdout and stderr and
// identical exit codes. The corpus covers every root-catalog surface pinned
// during slice 2, plus the shared usage/help/unknown-command shell paths.
//
// Oracle resolution: <repo>/dist/infrawright-cli.mjs run by `node` from
// PATH. When either is missing the test skips loudly — CI decides where the
// differential lane actually runs, mirroring the Node suite's Python-oracle
// selector pattern.

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func repoRoot(t *testing.T) string {
	t.Helper()
	current, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		catalogs, err1 := os.Stat(filepath.Join(current, "catalogs"))
		packs, err2 := os.Stat(filepath.Join(current, "packs"))
		if err1 == nil && err2 == nil && catalogs.IsDir() && packs.IsDir() {
			return current
		}
		parent := filepath.Dir(current)
		if parent == current {
			t.Fatal("unable to locate the repository root (catalogs/ + packs/)")
		}
		current = parent
	}
}

type runResult struct {
	exit   int
	stdout []byte
	stderr []byte
}

func runBinary(t *testing.T, root string, argv0 string, args []string) runResult {
	t.Helper()
	command := exec.Command(argv0, args...)
	command.Dir = root
	// A minimal, deterministic environment: no INFRAWRIGHT_* leakage from
	// the invoking shell may influence either side.
	command.Env = []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + os.Getenv("HOME"),
		"TMPDIR=" + os.Getenv("TMPDIR"),
	}
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

func TestRootCatalogDifferentialAgainstNodeOracle(t *testing.T) {
	root := repoRoot(t)
	oracleBundle := filepath.Join(root, "dist", "infrawright-cli.mjs")
	if _, err := os.Stat(oracleBundle); err != nil {
		t.Skipf("Node oracle bundle absent (%s); build it with `npm run build:metadata-cli`", oracleBundle)
	}
	nodeBinary, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not on PATH; the differential lane needs the pinned Node 24")
	}

	goBinary := filepath.Join(root, "dist", "iw-go-diff")
	build := exec.Command("go", "build", "-o", goBinary, ".")
	build.Dir = filepath.Join(root, "go", "cmd", "iw")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building Go CLI: %v\n%s", err, output)
	}
	t.Cleanup(func() { os.Remove(goBinary) })

	staleFile := filepath.Join(t.TempDir(), "stale.json")
	if err := os.WriteFile(staleFile, []byte("{}\n"), 0o666); err != nil {
		t.Fatal(err)
	}
	nodeOut := filepath.Join(t.TempDir(), "node-out.json")
	goOut := filepath.Join(t.TempDir(), "go-out.json")

	allProviders := "zcc,zia,zpa,ztc"
	freshCatalog := filepath.Join("catalogs", "zscaler-root-catalog.v1.json")

	cases := []struct {
		name string
		args []string
		// outFiles maps per-side --out targets; when set, case args get
		// the side's file appended after "--out" and file bytes compared.
		outFiles bool
	}{
		{name: "render", args: []string{"root-catalog", "--providers", allProviders}},
		{name: "render-subset", args: []string{"root-catalog", "--providers", "zcc,ztc"}},
		{name: "out-file", args: []string{"root-catalog", "--providers", allProviders, "--out"}, outFiles: true},
		{name: "check-fresh", args: []string{"root-catalog", "--providers", allProviders, "--check", freshCatalog}},
		{name: "check-stale", args: []string{"root-catalog", "--providers", allProviders, "--check", staleFile}},
		{name: "out-check-conflict", args: []string{"root-catalog", "--providers", "zcc", "--out", "x.json", "--check", "y.json"}},
		{name: "unknown-provider", args: []string{"root-catalog", "--providers", "nope"}},
		{name: "empty-providers", args: []string{"root-catalog", "--providers", ","}},
		{name: "empty-provider-value", args: []string{"root-catalog", "--providers", ""}},
		{name: "unknown-argument", args: []string{"root-catalog", "--bogus"}},
		{name: "duplicate-forbidden", args: []string{"root-catalog", "--out", "a", "--out", "b", "--check", "c"}},
		{name: "command-help", args: []string{"root-catalog", "-h"}},
		{name: "top-level-help", args: []string{"--help"}},
		{name: "no-arguments", args: []string{}},
		{name: "unknown-command", args: []string{"bogus-command"}},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			nodeArguments := append([]string{oracleBundle}, testCase.args...)
			goArguments := append([]string{}, testCase.args...)
			if testCase.outFiles {
				nodeArguments = append(nodeArguments, nodeOut)
				goArguments = append(goArguments, goOut)
			}
			oracle := runBinary(t, root, nodeBinary, nodeArguments)
			candidate := runBinary(t, root, goBinary, goArguments)

			if oracle.exit != candidate.exit {
				t.Errorf("exit: node=%d go=%d\nnode stderr: %s\ngo stderr: %s",
					oracle.exit, candidate.exit, oracle.stderr, candidate.stderr)
			}
			if !bytes.Equal(oracle.stdout, candidate.stdout) {
				t.Errorf("stdout diverges\nnode (%d bytes): %.400q\ngo (%d bytes): %.400q",
					len(oracle.stdout), oracle.stdout, len(candidate.stdout), candidate.stdout)
			}
			if !bytes.Equal(oracle.stderr, candidate.stderr) {
				t.Errorf("stderr diverges\nnode (%d bytes): %.400q\ngo (%d bytes): %.400q",
					len(oracle.stderr), oracle.stderr, len(candidate.stderr), candidate.stderr)
			}
			if testCase.outFiles {
				nodeBytes, err := os.ReadFile(nodeOut)
				if err != nil {
					t.Fatal(err)
				}
				goBytes, err := os.ReadFile(goOut)
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(nodeBytes, goBytes) {
					t.Errorf("--out artifact diverges: node %d bytes, go %d bytes", len(nodeBytes), len(goBytes))
				}
			}
		})
	}
}
