package main

// The first cut of the Node-oracle differential harness from
// docs/go-runtime-plan.md: run the built Node CLI bundle and this Go binary
// on the same argv/env/cwd and require byte-identical stdout and stderr and
// identical exit codes. The corpus covers every root-catalog surface pinned
// during slice 2. A6 deliberately retires one Node-only authoring command and
// owns the resulting Go-authority help/usage surface separately.
//
// Oracle resolution: <repo>/dist/infrawright-cli.mjs run by `node` from
// PATH. When either is missing the test skips loudly — CI decides where the
// differential lane actually runs, mirroring the Node suite's Python-oracle
// selector pattern.

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const frozenNodeOracleEnvironment = "INFRAWRIGHT_FROZEN_NODE_ORACLE"

func frozenNodeOraclePath(t *testing.T) string {
	t.Helper()
	configured := os.Getenv(frozenNodeOracleEnvironment)
	if configured == "" {
		t.Skipf("archived differential requires %s to name the frozen bundle", frozenNodeOracleEnvironment)
	}
	absolute, err := filepath.Abs(configured)
	if err != nil {
		t.Fatalf("filepath.Abs(%q) error = %v, want nil", configured, err)
	}
	if _, err := os.Stat(absolute); err != nil {
		t.Fatalf("frozen differential bundle %q is unavailable: %v", absolute, err)
	}
	return absolute
}

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

func equalAfterA6Usage(left, right []byte) bool {
	return bytes.Equal(left, right)
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

// buildGoV2AuthorityCLI builds a disposable CLI next to the runtime data so
// packageRoot resolves the checked-in packs without consulting the frozen Node
// runtime. V2 topology is owned by reviewed Go catalog/golden bytes.
func buildGoV2AuthorityCLI(t *testing.T, root, prefix string) string {
	t.Helper()
	dist := filepath.Join(root, "dist")
	distInfo, statErr := os.Stat(dist)
	createdDist := os.IsNotExist(statErr)
	if statErr != nil && !createdDist {
		t.Fatalf("stat %s: %v", dist, statErr)
	}
	if statErr == nil && !distInfo.IsDir() {
		t.Fatalf("runtime directory %s is not a directory", dist)
	}
	if createdDist {
		if err := os.MkdirAll(dist, 0o755); err != nil {
			t.Fatalf("creating %s for disposable Go CLI: %v", dist, err)
		}
		// The test owns this directory only when it was absent on entry. Remove
		// it only if it is still empty after removing our own binary; this never
		// removes a checked-in oracle or an artifact created by another test.
		t.Cleanup(func() { _ = os.Remove(dist) })
	}
	candidateFile, err := os.CreateTemp(dist, prefix+"-*")
	if err != nil {
		t.Fatalf("os.CreateTemp(%s): %v", prefix, err)
	}
	candidate := candidateFile.Name()
	if err := candidateFile.Close(); err != nil {
		t.Fatalf("closing %s: %v", candidate, err)
	}
	if err := os.Remove(candidate); err != nil {
		t.Fatalf("removing build placeholder %s: %v", candidate, err)
	}
	t.Cleanup(func() { _ = os.Remove(candidate) })
	build := exec.Command("go", "build", "-o", candidate, ".")
	build.Dir = filepath.Join(root, "go", "cmd", "iw")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building Go CLI: %v\n%s", err, output)
	}
	return candidate
}

func TestRootCatalogV2GoldenAuthority(t *testing.T) {
	root := repoRoot(t)
	goBinary := buildGoV2AuthorityCLI(t, root, "iw-go-v2-root-catalog")
	goldenPath := filepath.Join(root, "catalogs", "zscaler-root-catalog.v2.json")
	golden, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("reading v2 root-catalog golden: %v", err)
	}

	allProviders := "zcc,zia,zpa,ztc"
	t.Run("render", func(t *testing.T) {
		result := runBinary(t, root, goBinary, []string{"root-catalog", "--providers", allProviders})
		if result.exit != 0 {
			t.Fatalf("root-catalog exit = %d; stderr=%s", result.exit, result.stderr)
		}
		if !bytes.Equal(result.stdout, golden) {
			t.Errorf("root-catalog v2 bytes differ from reviewed golden %s", goldenPath)
		}
		if len(result.stderr) != 0 {
			t.Errorf("root-catalog stderr = %q, want empty", result.stderr)
		}
	})

	t.Run("subset", func(t *testing.T) {
		result := runBinary(t, root, goBinary, []string{"root-catalog", "--providers", "zcc,ztc"})
		if result.exit != 0 {
			t.Fatalf("subset exit = %d; stderr=%s", result.exit, result.stderr)
		}
		var catalog struct {
			SchemaVersion     int      `json:"schema_version"`
			DeclaredProviders []string `json:"declared_providers"`
			Resources         []struct {
				Provider string `json:"provider"`
			} `json:"resources"`
		}
		if err := json.Unmarshal(result.stdout, &catalog); err != nil {
			t.Fatalf("decoding subset catalog: %v", err)
		}
		if catalog.SchemaVersion != 2 || strings.Join(catalog.DeclaredProviders, ",") != "zcc,ztc" {
			t.Errorf("subset catalog header = version %d providers %v, want version 2 providers [zcc ztc]", catalog.SchemaVersion, catalog.DeclaredProviders)
		}
		if len(catalog.Resources) == 0 {
			t.Fatal("subset catalog contains no resources")
		}
		for _, resource := range catalog.Resources {
			if resource.Provider != "zcc" && resource.Provider != "ztc" {
				t.Errorf("subset catalog included provider %q", resource.Provider)
			}
		}
	})

	t.Run("out-and-check", func(t *testing.T) {
		out := filepath.Join(t.TempDir(), "catalog.json")
		written := runBinary(t, root, goBinary, []string{"root-catalog", "--providers", allProviders, "--out", out})
		if written.exit != 0 || len(written.stdout) != 0 || len(written.stderr) != 0 {
			t.Fatalf("--out result = exit %d stdout %q stderr %q", written.exit, written.stdout, written.stderr)
		}
		actual, err := os.ReadFile(out)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(actual, golden) {
			t.Error("--out bytes differ from reviewed v2 golden")
		}
		fresh := runBinary(t, root, goBinary, []string{"root-catalog", "--providers", allProviders, "--check", goldenPath})
		if fresh.exit != 0 || len(fresh.stdout) != 0 || len(fresh.stderr) != 0 {
			t.Errorf("fresh --check result = exit %d stdout %q stderr %q", fresh.exit, fresh.stdout, fresh.stderr)
		}
	})

	t.Run("stale-and-argument-failures", func(t *testing.T) {
		stale := filepath.Join(t.TempDir(), "stale.json")
		if err := os.WriteFile(stale, []byte("{}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		cases := []struct {
			name     string
			args     []string
			contains string
		}{
			{"stale", []string{"root-catalog", "--providers", allProviders, "--check", stale}, "STALE_ROOT_CATALOG"},
			{"out-check-conflict", []string{"root-catalog", "--out", "x.json", "--check", "y.json"}, "only one of --out or --check"},
			{"unknown-provider", []string{"root-catalog", "--providers", "nope"}, "unknown provider"},
			{"empty-providers", []string{"root-catalog", "--providers", ","}, "requires at least one provider"},
		}
		for _, testCase := range cases {
			t.Run(testCase.name, func(t *testing.T) {
				result := runBinary(t, root, goBinary, testCase.args)
				if result.exit == 0 {
					t.Fatalf("%v unexpectedly succeeded", testCase.args)
				}
				if !strings.Contains(string(result.stderr), testCase.contains) {
					t.Errorf("stderr = %q, want %q", result.stderr, testCase.contains)
				}
			})
		}
	})
}

// resources is independent of state-unit topology, so it remains a frozen-v1
// Node differential after the v2 catalog/topology differentials retire.
func TestResourcesDifferentialAgainstFrozenNodeOracle(t *testing.T) {
	runtime := newBlockD5Runtime(t)
	cases := []struct {
		name string
		args []string
	}{
		{"all", []string{"resources"}},
		{"reference-order", []string{"resources", "--order=references"}},
		{"provider-selector", []string{"resources", "--resource", "zcc"}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			oracle := runBinary(t, runtime.repository, runtime.node,
				append([]string{runtime.oracleBundle}, testCase.args...))
			candidate := runBinary(t, runtime.repository, runtime.candidate, testCase.args)
			compareBlockC4RunResult(t, testCase.name, oracle, candidate)
		})
	}
}
