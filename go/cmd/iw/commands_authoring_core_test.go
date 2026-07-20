package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dvmrry/infrawright-dev/go/internal/metadata"
)

func coreTestDependencies(stdout, stderr *bytes.Buffer) authoringCoreDependencies {
	deps := defaultAuthoringCoreDependencies()
	deps.stdout = stdout
	deps.stderr = stderr
	return deps
}

func TestAuthoringParseArgumentsRewordsDuplicateAndDisablesHelp(t *testing.T) {
	_, err := authoringParseArguments([]string{"--out", "one", "--out", "two"}, []string{"--out"}, nil, nil)
	if err == nil || err.Error() != "--out may be passed only once" {
		t.Fatalf("authoringParseArguments duplicate error = %v, want reworded usage error", err)
	}
	_, err = authoringParseArguments([]string{"--help"}, nil, nil, nil)
	if err == nil || err.Error() != "unknown argument --help" {
		t.Fatalf("authoringParseArguments(--help) error = %v, want authoring parser rejection", err)
	}
}

func TestAuthoringWriteJSONRendersBeforeDestinationPreparation(t *testing.T) {
	temporary := t.TempDir()
	destination := filepath.Join(temporary, "report.json")
	if err := os.WriteFile(destination, []byte("old\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	deps := coreTestDependencies(&stdout, &stderr)
	if err := authoringWriteJSON(deps, map[string]any{"unsupported": make(chan int)}, &destination); err == nil {
		t.Fatal("authoringWriteJSON unsupported value error = nil")
	}
	got, err := os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "old\n" {
		t.Fatalf("destination after render failure = %q, want existing bytes preserved", got)
	}
	if err := authoringWriteJSON(deps, map[string]any{"coverage_ratio": float64(1)}, nil); err != nil {
		t.Fatalf("authoringWriteJSON(stdout): %v", err)
	}
	if got, want := stdout.String(), "{\n  \"coverage_ratio\": 1.0\n}\n"; got != want {
		t.Fatalf("authoringWriteJSON stdout = %q, want %q", got, want)
	}
}

func TestAuthoringRenderJSONNormalizesGoIntegerFieldsAndRejectsUint64Overflow(t *testing.T) {
	rendered, err := authoringRenderJSON(map[string]any{
		"signed": int(2),
		"nested": []any{int64(-3), uint32(4)},
	})
	if err != nil {
		t.Fatalf("authoringRenderJSON(Go integers): %v", err)
	}
	if got, want := string(rendered), "{\n  \"nested\": [\n    -3,\n    4\n  ],\n  \"signed\": 2\n}\n"; got != want {
		t.Fatalf("authoringRenderJSON(Go integers) = %q, want %q", got, want)
	}
	if _, err := authoringRenderJSON(map[string]any{"overflow": ^uint64(0)}); err == nil || !strings.Contains(err.Error(), "unsigned integer exceeds") {
		t.Fatalf("authoringRenderJSON(uint64 overflow) error = %v, want fail-closed boundary", err)
	}
}

func TestReconcileCommandWritesThenReturnsUnknownExit(t *testing.T) {
	temporary := t.TempDir()
	schema := filepath.Join(temporary, "schema.json")
	api := filepath.Join(temporary, "api.json")
	out := filepath.Join(temporary, "report.json")
	writeCoreTestFile(t, schema, `{"resource_schemas":{"example":{"block":{"attributes":{}}}}}`)
	writeCoreTestFile(t, api, `{"unknown":"value"}`)
	var stdout, stderr bytes.Buffer
	status, err := reconcileCommandWithDependencies([]string{
		"example", "--api", api, "--schema", schema, "--out", out, "--fail-on-unknown",
	}, coreTestDependencies(&stdout, &stderr))
	if err != nil {
		t.Fatalf("reconcileCommand: %v", err)
	}
	if status != 4 {
		t.Fatalf("reconcileCommand status = %d, want 4", status)
	}
	if got, want := stderr.String(), "error: example has unknown API surface; review report\n"; got != want {
		t.Fatalf("reconcileCommand stderr = %q, want %q", got, want)
	}
	report, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read written report: %v", err)
	}
	if !strings.Contains(string(report), `"unknown"`) {
		t.Fatalf("reconcile report = %s, want unknown bucket", report)
	}
}

func TestReconcileCommandUsesNonEmptyPacksEnvironmentBeforePackageDefault(t *testing.T) {
	var stdout, stderr bytes.Buffer
	deps := coreTestDependencies(&stdout, &stderr)
	deps.environment = func(name string) string {
		if name == "INFRAWRIGHT_PACKS" {
			return "/packs-from-environment"
		}
		return ""
	}
	loaded := ""
	deps.loadPackRoot = func(options metadata.LoadPackRootOptions) (metadata.LoadedPackRoot, error) {
		loaded = options.PacksRoot
		return metadata.LoadedPackRoot{}, errors.New("stop after packs-root selection")
	}
	status, err := reconcileCommandWithDependencies([]string{"example", "--api", "ignored.json"}, deps)
	if err == nil || status != 0 {
		t.Fatalf("reconcileCommand packs fallback = (%d, %v), want loader error", status, err)
	}
	if got, want := loaded, "/packs-from-environment"; got != want {
		t.Fatalf("LoadPackRoot PacksRoot = %q, want %q", got, want)
	}
}

func TestOpenAPIMapFailureDoesNotReplaceDestination(t *testing.T) {
	temporary := t.TempDir()
	openAPI := filepath.Join(temporary, "openapi.json")
	schema := filepath.Join(temporary, "schema.json")
	out := filepath.Join(temporary, "report.json")
	writeCoreTestFile(t, openAPI, `{"openapi":"not-a-version","paths":{}}`)
	writeCoreTestFile(t, schema, `{"resource_schemas":{}}`)
	writeCoreTestFile(t, out, "old\n")
	var stdout, stderr bytes.Buffer
	status, err := openAPIMapCommandWithDependencies([]string{"--openapi", openAPI, "--schema", schema, "--out", out}, coreTestDependencies(&stdout, &stderr))
	if err == nil || status != 0 {
		t.Fatalf("openAPIMapCommand invalid OpenAPI = (%d, %v), want error", status, err)
	}
	got, readErr := os.ReadFile(out)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(got) != "old\n" {
		t.Fatalf("openapi-map overwrote destination on preparation error: %q", got)
	}
}

func TestTransformAdoptParityContainsOperationalFailure(t *testing.T) {
	var stdout, stderr bytes.Buffer
	deps := coreTestDependencies(&stdout, &stderr)
	deps.environment = func(string) string { return "/fixture-packs" }
	deps.packageRoot = func() (string, error) { return "/repository", nil }
	deps.loadPackRoot = func(metadata.LoadPackRootOptions) (metadata.LoadedPackRoot, error) {
		return metadata.LoadedPackRoot{}, errors.New("pack loading failed")
	}
	status, err := transformAdoptParityCommandWithDependencies([]string{"fixture.json"}, deps)
	if err != nil {
		t.Fatalf("transformAdoptParityCommand contained error = %v", err)
	}
	if status != 2 {
		t.Fatalf("transformAdoptParityCommand status = %d, want 2", status)
	}
	if got, want := stderr.String(), "error: pack loading failed\n"; got != want {
		t.Fatalf("transformAdoptParityCommand stderr = %q, want %q", got, want)
	}
	_, err = transformAdoptParityCommandWithDependencies(nil, deps)
	if err == nil || err.Error() != "transform-adopt-parity requires at least one fixture path" {
		t.Fatalf("transformAdoptParityCommand missing fixture error = %v", err)
	}
}

func writeCoreTestFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
