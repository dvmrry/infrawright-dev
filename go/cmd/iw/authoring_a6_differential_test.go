package main

// A6 keeps the frozen Node v1 authoring surface as the compatibility oracle.
// This corpus intentionally exercises only local fixture files and supplied
// legacy facts: it must never need a provider, Terraform, Git, or a second
// authoring executable.

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const a6FrozenNodeOracleSHA256 = "ce48c2c6a1cc01254866c5a7eb98b3eef1c90e6c45b69aff7df7aed80c822fa2"

type a6Runtime struct {
	repository string
	node       string
	oracle     string
	candidate  string
}

func newA6Runtime(t *testing.T) a6Runtime {
	t.Helper()
	repository := repoRoot(t)
	candidate := buildGoV2AuthorityCLI(t, repository, "iw-go-a6")
	return a6Runtime{repository: repository, candidate: candidate}
}

func newA6DifferentialRuntime(t *testing.T) a6Runtime {
	t.Helper()
	runtime := newA6Runtime(t)
	oracle := frozenNodeOraclePath(t)
	oracleBytes, err := os.ReadFile(oracle)
	if err != nil {
		t.Fatalf("read frozen Node oracle %q: %v", oracle, err)
	}
	if got := fmt.Sprintf("%x", sha256.Sum256(oracleBytes)); got != a6FrozenNodeOracleSHA256 {
		t.Fatalf("frozen Node oracle SHA-256 = %s, want %s", got, a6FrozenNodeOracleSHA256)
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skipf("Node oracle executable unavailable after digest verification: %v", err)
	}
	runtime.node = node
	runtime.oracle = oracle
	return runtime
}

func a6Run(t *testing.T, directory, executable string, arguments, environment []string) runResult {
	t.Helper()
	command := exec.Command(executable, arguments...)
	command.Dir = directory
	command.Env = append([]string{
		"HOME=" + t.TempDir(),
		"INFRAWRIGHT_PACKS=",
		"INFRAWRIGHT_PACK_PROFILE=",
		"INFRAWRIGHT_PERFORMANCE_REPORT=",
		"PATH=" + os.Getenv("PATH"),
		"TMPDIR=" + t.TempDir(),
	}, environment...)
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()
	exit := 0
	if exitError, ok := err.(*exec.ExitError); ok {
		exit = exitError.ExitCode()
	} else if err != nil {
		t.Fatalf("run %q %q: %v", executable, arguments, err)
	}
	return runResult{exit: exit, stdout: stdout.Bytes(), stderr: stderr.Bytes()}
}

// a6Normalize applies the one established authoring normalization: replace the
// fixture-local absolute root. It deliberately does not rewrite diagnostics,
// JSON formatting, ordering, or any other platform detail.
func a6Normalize(content []byte, root string) []byte {
	return bytes.ReplaceAll(content, []byte(root), []byte("<A6_FIXTURE_ROOT>"))
}

func a6CompareRun(t *testing.T, name string, oracle, candidate runResult, oracleRoot, candidateRoot string) {
	t.Helper()
	if oracle.exit != candidate.exit {
		t.Errorf("%s exit = %d, want Node %d; node stderr=%q go stderr=%q", name, candidate.exit, oracle.exit, oracle.stderr, candidate.stderr)
	}
	if got, want := a6Normalize(candidate.stdout, candidateRoot), a6Normalize(oracle.stdout, oracleRoot); !bytes.Equal(got, want) {
		t.Errorf("%s stdout = %q, want Node %q", name, got, want)
	}
	if got, want := a6Normalize(candidate.stderr, candidateRoot), a6Normalize(oracle.stderr, oracleRoot); !bytes.Equal(got, want) {
		t.Errorf("%s stderr = %q, want Node %q", name, got, want)
	}
}

func a6Tree(t *testing.T, root string) map[string][]byte {
	t.Helper()
	files := map[string][]byte{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		files[filepath.ToSlash(relative)] = content
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("walk A6 artifact tree %q: %v", root, err)
	}
	return files
}

func a6CompareTree(t *testing.T, name string, oracle, candidate, oracleRoot, candidateRoot string) {
	t.Helper()
	want, got := a6Tree(t, oracle), a6Tree(t, candidate)
	for path, wantBytes := range want {
		gotBytes, found := got[path]
		if !found {
			t.Errorf("%s artifact %q missing from Go output", name, path)
			continue
		}
		if normalizedGot, normalizedWant := a6Normalize(gotBytes, candidateRoot), a6Normalize(wantBytes, oracleRoot); !bytes.Equal(normalizedGot, normalizedWant) {
			t.Errorf("%s artifact %q = %q, want Node %q", name, path, normalizedGot, normalizedWant)
		}
	}
	for path := range got {
		if _, found := want[path]; !found {
			t.Errorf("%s Go output has unexpected artifact %q", name, path)
		}
	}
}

func a6WriteJSON(t *testing.T, path string, value any) {
	t.Helper()
	bytes, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("json.Marshal(%q): %v", path, err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("os.MkdirAll(%q): %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, append(bytes, '\n'), 0o600); err != nil {
		t.Fatalf("os.WriteFile(%q): %v", path, err)
	}
}

type a6Fixture struct {
	api     string
	facts   string
	openAPI string
	recipe  string
	schema  string
	source  string
	work    string
}

func materializeA6Fixture(t *testing.T, root string) a6Fixture {
	t.Helper()
	source := filepath.Join(root, "provider")
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatalf("os.MkdirAll(provider): %v", err)
	}
	if err := os.WriteFile(filepath.Join(source, "resource_folder.go"), []byte("package provider\nfunc read() {}\n"), 0o600); err != nil {
		t.Fatalf("os.WriteFile(provider source): %v", err)
	}
	provider := "registry.terraform.io/example/example"
	schema := filepath.Join(root, "schema.json")
	a6WriteJSON(t, schema, map[string]any{
		"provider_schemas": map[string]any{
			provider: map[string]any{
				"resource_schemas": map[string]any{
					"example_folder": map[string]any{"block": map[string]any{
						"attributes": map[string]any{"name": map[string]any{"required": true, "type": "string"}},
					}},
				},
			},
		},
	})
	openAPI := filepath.Join(root, "openapi.json")
	a6WriteJSON(t, openAPI, map[string]any{
		"info":    map[string]any{"title": "A6 local fixture", "version": "1"},
		"openapi": "3.0.3",
		"paths": map[string]any{
			"/api/folders":       map[string]any{"get": map[string]any{"operationId": "RouteGetFolders", "responses": map[string]any{"200": map[string]any{"description": "ok"}}}},
			"/api/folders/{uid}": map[string]any{"get": map[string]any{"operationId": "RouteGetFolder", "responses": map[string]any{"200": map[string]any{"description": "ok"}}}},
		},
	})
	api := filepath.Join(root, "api.json")
	a6WriteJSON(t, api, []any{map[string]any{"name": "folder"}})
	facts := filepath.Join(root, "source-facts.json")
	a6WriteJSON(t, facts, map[string]any{
		"source_root":            source,
		"files":                  []any{map[string]any{"path": "resource_folder.go", "package": "provider", "imports": []any{}}},
		"functions":              []any{},
		"resource_registrations": []any{},
		"resource_references":    []any{},
		"identifier_references":  []any{},
		"read_callbacks":         []any{},
		"package_calls":          []any{},
		"raw_rest_calls":         []any{},
		"selector_calls": []any{
			map[string]any{"file": "resource_folder.go", "function": "read", "parts": []any{"client", "Provisioning", "GetFolders"}, "symbol": "client.Provisioning.GetFolders"},
			map[string]any{"file": "resource_folder.go", "function": "read", "parts": []any{"client", "Provisioning", "GetFolder"}, "symbol": "client.Provisioning.GetFolder"},
		},
	})
	recipe := filepath.Join(root, "recipe.json")
	a6WriteJSON(t, recipe, map[string]any{
		"api_prefix":       "/api/",
		"name":             "example",
		"openapi":          map[string]any{"format": "json", "path": "openapi.json"},
		"provider_source":  provider,
		"provider_version": "1.2.3",
		"resource_prefix":  "example",
		"source":           map[string]any{"path": "provider"},
		"terraform_schema": map[string]any{"path": "schema.json"},
	})
	return a6Fixture{api: api, facts: facts, openAPI: openAPI, recipe: recipe, schema: schema, source: source, work: filepath.Join(root, "probe-work")}
}

func a6AuthoringArguments(command string, fixture a6Fixture, root string) ([]string, string) {
	switch command {
	case "reconcile":
		return []string{"reconcile", "example_folder", "--api", fixture.api, "--schema", fixture.schema, "--out", filepath.Join(root, "reconcile.json")}, filepath.Join(root, "reconcile.json")
	case "openapi-map":
		return []string{"openapi-map", "--schema", fixture.schema, "--openapi", fixture.openAPI, "--provider-source", "registry.terraform.io/example/example", "--resource-prefix", "example", "--api-prefix", "/api/", "--out", filepath.Join(root, "openapi-map.json")}, filepath.Join(root, "openapi-map.json")
	case "source-operation-map":
		out := filepath.Join(root, "source-operation")
		return []string{"source-operation-map", "--schema", fixture.schema, "--openapi", fixture.openAPI, "--source-root", fixture.source, "--provider-source", "registry.terraform.io/example/example", "--resource-prefix", "example", "--source-facts", fixture.facts, "--out", filepath.Join(out, "source-registry.json"), "--diagnostics", filepath.Join(out, "source-diagnostics.json"), "--source-facts-compare", filepath.Join(out, "source-facts-compare.json")}, out
	case "source-evidence-eval":
		out := filepath.Join(root, "source-evidence")
		return []string{"source-evidence-eval", "--schema", fixture.schema, "--openapi", fixture.openAPI, "--source-root", fixture.source, "--provider-source", "registry.terraform.io/example/example", "--resource-prefix", "example", "--source-facts", fixture.facts, "--out-dir", out}, out
	case "provider-probe":
		copies := filepath.Join(root, "probe-copies")
		return []string{"provider-probe", fixture.recipe, "--work-dir", fixture.work, "--out", filepath.Join(copies, "summary.json"), "--markdown", filepath.Join(copies, "summary.md")}, filepath.Join(fixture.work, "artifacts")
	default:
		panic("unsupported A6 command " + command)
	}
}

func TestA6AuthoringDifferentialAgainstFrozenNodeOracle(t *testing.T) {
	runtime := newA6DifferentialRuntime(t)
	for _, command := range []string{"reconcile", "openapi-map", "source-operation-map", "source-evidence-eval", "provider-probe"} {
		t.Run(command, func(t *testing.T) {
			oracleRoot, candidateRoot := filepath.Join(t.TempDir(), "node"), filepath.Join(t.TempDir(), "go")
			oracleFixture, candidateFixture := materializeA6Fixture(t, oracleRoot), materializeA6Fixture(t, candidateRoot)
			oracleArguments, oracleArtifacts := a6AuthoringArguments(command, oracleFixture, oracleRoot)
			candidateArguments, candidateArtifacts := a6AuthoringArguments(command, candidateFixture, candidateRoot)
			oracle := a6Run(t, runtime.repository, runtime.node, append([]string{runtime.oracle}, oracleArguments...), nil)
			candidate := a6Run(t, runtime.repository, runtime.candidate, candidateArguments, nil)
			a6CompareRun(t, command, oracle, candidate, oracleRoot, candidateRoot)
			if command == "reconcile" || command == "openapi-map" {
				want, wantErr := os.ReadFile(oracleArtifacts)
				got, gotErr := os.ReadFile(candidateArtifacts)
				if wantErr != nil || gotErr != nil {
					t.Errorf("%s artifact read errors: Node=%v Go=%v", command, wantErr, gotErr)
				} else if normalizedGot, normalizedWant := a6Normalize(got, candidateRoot), a6Normalize(want, oracleRoot); !bytes.Equal(normalizedGot, normalizedWant) {
					t.Errorf("%s artifact = %q, want Node %q", command, normalizedGot, normalizedWant)
				}
				return
			}
			a6CompareTree(t, command, oracleArtifacts, candidateArtifacts, oracleRoot, candidateRoot)
			if command == "provider-probe" {
				a6CompareTree(t, command+" copies", filepath.Join(oracleRoot, "probe-copies"), filepath.Join(candidateRoot, "probe-copies"), oracleRoot, candidateRoot)
			}
		})
	}

	t.Run("transform-adopt-parity", func(t *testing.T) {
		fixture := filepath.Join(runtime.repository, "tests", "fixtures", "parity", "zcc_failopen_policy_inversion.json")
		arguments := []string{"transform-adopt-parity", fixture}
		oracle := a6Run(t, runtime.repository, runtime.node, append([]string{runtime.oracle}, arguments...), nil)
		candidate := a6Run(t, runtime.repository, runtime.candidate, arguments, nil)
		a6CompareRun(t, "transform-adopt-parity", oracle, candidate, runtime.repository, runtime.repository)
	})
}

func TestA6LegacySourceUsagePriorityAgainstFrozenNodeOracle(t *testing.T) {
	runtime := newA6DifferentialRuntime(t)
	for _, test := range []struct {
		name      string
		arguments func(string) []string
	}{
		{
			name: "source operation required input precedes facts relationship",
			arguments: func(root string) []string {
				return []string{"source-operation-map", "--source-facts-compare", filepath.Join(root, "compare.json")}
			},
		},
		{
			name: "source evaluation source root precedes file reads",
			arguments: func(root string) []string {
				return []string{
					"source-evidence-eval", "--out-dir", filepath.Join(root, "out"),
					"--source-facts", filepath.Join(root, "facts.json"),
					"--openapi", filepath.Join(root, "openapi.json"),
				}
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			oracleRoot, candidateRoot := filepath.Join(t.TempDir(), "node"), filepath.Join(t.TempDir(), "go")
			oracle := a6Run(t, runtime.repository, runtime.node, append([]string{runtime.oracle}, test.arguments(oracleRoot)...), nil)
			candidate := a6Run(t, runtime.repository, runtime.candidate, test.arguments(candidateRoot), nil)
			a6CompareRun(t, test.name, oracle, candidate, oracleRoot, candidateRoot)
		})
	}
}

func TestA6GoHelpListsOnlyRetainedAuthoringCommands(t *testing.T) {
	runtime := newA6Runtime(t)
	result := a6Run(t, runtime.repository, runtime.candidate, []string{"--help"}, nil)
	if result.exit != 0 {
		t.Fatalf("iw --help exit = %d, want 0; stderr=%q", result.exit, result.stderr)
	}
	help := string(result.stdout)
	for _, command := range []string{"reconcile", "openapi-map", "source-operation-map", "source-evidence-eval", "provider-probe", "transform-adopt-parity"} {
		line := "\n  " + command + " "
		if got := strings.Count(help, line); got != 1 {
			t.Errorf("iw --help count(%q) = %d, want 1; help=%q", line, got, help)
		}
	}
	if strings.Contains(help, "zpa-provider-evidence") {
		t.Errorf("iw --help contains retired zpa-provider-evidence: %q", help)
	}
}

func TestA6AuthoringMakeTargetsUseSoleGoLane(t *testing.T) {
	repository := repoRoot(t)
	data, err := os.ReadFile(filepath.Join(repository, "Makefile"))
	if err != nil {
		t.Fatalf("read Makefile: %v", err)
	}
	makefile := string(data)
	if !strings.Contains(makefile, "IW ?= dist/iw\n") {
		t.Fatal("Makefile does not default IW to the Go binary")
	}
	for _, command := range []string{"reconcile", "openapi-map", "source-operation-map", "source-evidence-eval", "provider-probe", "transform-adopt-parity"} {
		if !strings.Contains(makefile, "\n"+command+": dist/iw ") {
			t.Errorf("Makefile target %q does not depend on dist/iw", command)
		}
		if !strings.Contains(makefile, "\n\t$(IW) "+command) {
			t.Errorf("Makefile target %q does not invoke the sole Go lane", command)
		}
	}
	if strings.Contains(makefile, "\nzpa-provider-evidence:") {
		t.Error("Makefile retains active zpa-provider-evidence target")
	}
}

func TestA6AuthoringCommandsRunWithoutExternalExecutables(t *testing.T) {
	runtime := newA6Runtime(t)
	root := t.TempDir()
	fixture := materializeA6Fixture(t, root)
	tripwire := filepath.Join(root, "tripwire-bin")
	if err := os.MkdirAll(tripwire, 0o755); err != nil {
		t.Fatalf("os.MkdirAll(tripwire): %v", err)
	}
	log := filepath.Join(root, "external-invocations.log")
	for _, name := range []string{"node", "npm", "npx", "go", "terraform", "git"} {
		program := filepath.Join(tripwire, name)
		content := "#!/bin/sh\nprintf '%s\\n' \"$0\" >> " + shellQuote(log) + "\nexit 97\n"
		if err := os.WriteFile(program, []byte(content), 0o700); err != nil {
			t.Fatalf("os.WriteFile(%q): %v", program, err)
		}
	}
	environment := []string{"PATH=" + tripwire}
	commands := [][]string{
		{"reconcile", "example_folder", "--api", fixture.api, "--schema", fixture.schema, "--out", filepath.Join(root, "smoke-reconcile.json")},
		{"openapi-map", "--schema", fixture.schema, "--openapi", fixture.openAPI, "--provider-source", "registry.terraform.io/example/example", "--resource-prefix", "example", "--out", filepath.Join(root, "smoke-openapi.json")},
		{"source-operation-map", "--schema", fixture.schema, "--openapi", fixture.openAPI, "--source-root", fixture.source, "--provider-source", "registry.terraform.io/example/example", "--resource-prefix", "example", "--source-facts", fixture.facts, "--out", filepath.Join(root, "smoke-source-registry.json"), "--diagnostics", filepath.Join(root, "smoke-source-diagnostics.json"), "--source-facts-compare", filepath.Join(root, "smoke-source-compare.json")},
		{"source-evidence-eval", "--schema", fixture.schema, "--openapi", fixture.openAPI, "--source-root", fixture.source, "--provider-source", "registry.terraform.io/example/example", "--resource-prefix", "example", "--source-facts", fixture.facts, "--out-dir", filepath.Join(root, "smoke-evidence")},
		{"provider-probe", fixture.recipe, "--work-dir", filepath.Join(root, "smoke-probe")},
		{"transform-adopt-parity", filepath.Join(runtime.repository, "tests", "fixtures", "parity", "zcc_failopen_policy_inversion.json")},
	}
	for _, arguments := range commands {
		result := a6Run(t, runtime.repository, runtime.candidate, arguments, environment)
		if result.exit != 0 {
			t.Errorf("iw %s exit = %d, want 0; stdout=%q stderr=%q", arguments[0], result.exit, result.stdout, result.stderr)
		}
	}
	if bytes, err := os.ReadFile(log); err == nil {
		t.Errorf("A6 Node-free smoke invoked forbidden executable(s): %q", bytes)
	} else if !os.IsNotExist(err) {
		t.Fatalf("os.ReadFile(%q): %v", log, err)
	}
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\\"'\\\"'") + "'"
}
