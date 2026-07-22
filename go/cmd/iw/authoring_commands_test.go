package main

// Authoring commands use only local fixture files and supplied source facts.

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

type a6Runtime struct {
	repository string
	candidate  string
}

func newA6Runtime(t *testing.T) a6Runtime {
	t.Helper()
	repository := repoRoot(t)
	candidate := buildGoV2AuthorityCLI(t, repository, "iw-go-a6")
	return a6Runtime{repository: repository, candidate: candidate}
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
