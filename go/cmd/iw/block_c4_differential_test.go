package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

type blockC4Runtime struct {
	repository   string
	node         string
	oracleBundle string
	candidate    string
}

func newBlockC4Runtime(t *testing.T) blockC4Runtime {
	t.Helper()
	repository := repoRoot(t)
	oracleBundle := filepath.Join(repository, "dist", "infrawright-cli.mjs")
	if _, err := os.Stat(oracleBundle); err != nil {
		t.Skipf("Node oracle bundle absent (%s); build it with `npm run build:metadata-cli`", oracleBundle)
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not on PATH; the differential lane needs the pinned Node 24")
	}
	candidateFile, err := os.CreateTemp(filepath.Join(repository, "dist"), "iw-go-diff-block-c4-*")
	if err != nil {
		t.Fatalf("os.CreateTemp(dist/iw-go-diff-block-c4-*) error: %v", err)
	}
	candidate := candidateFile.Name()
	if err := candidateFile.Close(); err != nil {
		t.Fatalf("closing candidate placeholder error: %v", err)
	}
	if err := os.Remove(candidate); err != nil {
		t.Fatalf("removing candidate placeholder error: %v", err)
	}
	build := exec.Command("go", "build", "-o", candidate, ".")
	build.Dir = filepath.Join(repository, "go", "cmd", "iw")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build ./cmd/iw error: %v\n%s", err, output)
	}
	t.Cleanup(func() { _ = os.Remove(candidate) })
	return blockC4Runtime{
		repository: repository, node: node, oracleBundle: oracleBundle, candidate: candidate,
	}
}

type blockC4Fixture struct {
	workspace  string
	packs      string
	profile    string
	deployment string
	terraform  string
	log        string
	envDir     string
	varFile    string
}

func writeBlockC4File(t *testing.T, path string, content []byte, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("os.MkdirAll(%q) error: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, content, mode); err != nil {
		t.Fatalf("os.WriteFile(%q) error: %v", path, err)
	}
}

func writeBlockC4JSON(t *testing.T, path string, value any) {
	t.Helper()
	content, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatalf("json.MarshalIndent(%q) error: %v", path, err)
	}
	writeBlockC4File(t, path, append(content, '\n'), 0o600)
}

func prepareBlockC4Fixture(t *testing.T, workspace string) blockC4Fixture {
	t.Helper()
	if err := os.RemoveAll(workspace); err != nil {
		t.Fatalf("os.RemoveAll(%q) error: %v", workspace, err)
	}
	fixture := blockC4Fixture{
		workspace:  workspace,
		packs:      filepath.Join(workspace, "packs"),
		profile:    filepath.Join(workspace, "packsets", "full.json"),
		deployment: filepath.Join(workspace, "deployment.json"),
		terraform:  filepath.Join(workspace, "terraform-fake"),
		log:        filepath.Join(workspace, "terraform.log"),
		envDir:     filepath.Join(workspace, "envs", "tenant", "sample_root"),
		varFile:    filepath.Join(workspace, "config", "tenant", "sample_resource.auto.tfvars.json"),
	}
	writeBlockC4JSON(t, filepath.Join(fixture.packs, "sample", "pack.json"), map[string]any{
		"pin":               "1.0.0",
		"provider_prefixes": map[string]any{"sample_": "sample"},
		"provider_sources":  map[string]any{"sample": "example/sample"},
		"vendor":            "sample",
	})
	writeBlockC4JSON(t, filepath.Join(fixture.packs, "sample", "registry.json"), map[string]any{
		"sample_resource": map[string]any{"generate": true, "product": "sample"},
	})
	writeBlockC4JSON(t, fixture.profile, map[string]any{
		"kind": "infrawright.pack-set", "version": 1,
		"packs": []any{"sample"}, "shared": []any{},
	})
	writeBlockC4JSON(t, fixture.deployment, map[string]any{
		"overlay": workspace, "module_dir": filepath.Join(workspace, "modules"),
		"roots": map[string]any{"sample": map[string]any{
			"groups": map[string]any{"sample_root": []any{"sample_resource"}},
		}},
	})
	moduleDir := filepath.Join(workspace, "modules", "sample_resource")
	writeBlockC4File(t, filepath.Join(moduleDir, "main.tf"), []byte("# fixture module\n"), 0o600)
	relativeModule, err := filepath.Rel(fixture.envDir, moduleDir)
	if err != nil {
		t.Fatalf("filepath.Rel(%q, %q) error: %v", fixture.envDir, moduleDir, err)
	}
	writeBlockC4File(t, filepath.Join(fixture.envDir, "main.tf"), []byte(strings.Join([]string{
		`module "sample_resource" {`,
		`  source = "` + filepath.ToSlash(relativeModule) + `"`,
		"  items = var.sample_resource_items",
		"}",
		"",
	}, "\n")), 0o600)
	writeBlockC4File(t, fixture.varFile, []byte("{\"sample_resource_items\":{}}\n"), 0o600)
	writeBlockC4File(t, fixture.terraform, []byte(strings.Join([]string{
		"#!/bin/sh",
		`printf 'cwd=%s\n' "$PWD" >> "$FAKE_TF_LOG"`,
		`printf 'arg=%s\n' "$@" >> "$FAKE_TF_LOG"`,
		`if [ "$1" = "plan" ]; then`,
		`  for argument in "$@"; do`,
		`    case "$argument" in -out=*) printf '%s' 'opaque plan bytes' > "${argument#-out=}";; esac`,
		`  done`,
		`elif [ "$1" = "show" ]; then`,
		`  printf '%s' "$FAKE_TF_SHOW_JSON"`,
		`fi`,
		"exit 0",
		"",
	}, "\n")), 0o700)
	if err := os.MkdirAll(filepath.Join(workspace, "tmp"), 0o700); err != nil {
		t.Fatalf("os.MkdirAll(TMPDIR) error: %v", err)
	}
	return fixture
}

func (fixture blockC4Fixture) commonArguments() []string {
	return []string{
		"--tenant", "tenant",
		"--resource", "sample_resource",
		"--root", fixture.packs,
		"--profile", fixture.profile,
		"--catalog", fixture.profile,
		"--deployment", fixture.deployment,
	}
}

func runBlockC4Side(
	t *testing.T,
	runtime blockC4Runtime,
	fixture blockC4Fixture,
	oracle bool,
	arguments []string,
	showJSON string,
) runResult {
	t.Helper()
	argv0 := runtime.candidate
	argv := append([]string(nil), arguments...)
	if oracle {
		argv0 = runtime.node
		argv = append([]string{runtime.oracleBundle}, argv...)
	}
	return runBinaryWithEnv(t, fixture.workspace, argv0, argv, []string{
		"TMPDIR=" + filepath.Join(fixture.workspace, "tmp"),
		"FAKE_TF_LOG=" + fixture.log,
		"FAKE_TF_SHOW_JSON=" + showJSON,
		"INFRAWRIGHT_PACKS=",
		"INFRAWRIGHT_PACK_PROFILE=",
		"INFRAWRIGHT_DEPLOYMENT=",
		"TF=",
	})
}

func compareBlockC4RunResult(t *testing.T, operation string, oracle, candidate runResult) {
	t.Helper()
	if candidate.exit != oracle.exit {
		t.Errorf("%s exit = %d, want Node %d\nNode stderr: %s\nGo stderr: %s",
			operation, candidate.exit, oracle.exit, oracle.stderr, candidate.stderr)
	}
	if !equalAfterA6Usage(candidate.stdout, oracle.stdout) {
		t.Errorf("%s stdout = %q, want Node %q", operation, candidate.stdout, oracle.stdout)
	}
	if !equalAfterA6Usage(candidate.stderr, oracle.stderr) {
		t.Errorf("%s stderr = %q, want Node %q", operation, candidate.stderr, oracle.stderr)
	}
}

func readBlockC4File(t *testing.T, path string) []byte {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("os.ReadFile(%q) error: %v", path, err)
	}
	return content
}

func TestBlockC4PlanAndCleanDifferentialAgainstNodeOracle(t *testing.T) {
	runtime := newBlockC4Runtime(t)
	workspace := filepath.Join(t.TempDir(), "workspace")

	fixture := prepareBlockC4Fixture(t, workspace)
	planArguments := append([]string{"plan"}, fixture.commonArguments()...)
	planArguments = append(planArguments, "--save", "--terraform", fixture.terraform)
	oracle := runBlockC4Side(t, runtime, fixture, true, planArguments, "")
	oracleLog := readBlockC4File(t, fixture.log)
	oraclePlan := readBlockC4File(t, filepath.Join(fixture.envDir, "tfplan"))
	oracleSources := readBlockC4File(t, filepath.Join(fixture.envDir, "tfplan.sources"))

	fixture = prepareBlockC4Fixture(t, workspace)
	candidate := runBlockC4Side(t, runtime, fixture, false, planArguments, "")
	compareBlockC4RunResult(t, "plan --save", oracle, candidate)
	if got := readBlockC4File(t, fixture.log); !bytes.Equal(got, oracleLog) {
		t.Errorf("plan --save Terraform argv log = %q, want Node %q", got, oracleLog)
	}
	if got := readBlockC4File(t, filepath.Join(fixture.envDir, "tfplan")); !bytes.Equal(got, oraclePlan) {
		t.Errorf("plan --save tfplan bytes = %q, want Node %q", got, oraclePlan)
	}
	if got := readBlockC4File(t, filepath.Join(fixture.envDir, "tfplan.sources")); !bytes.Equal(got, oracleSources) {
		t.Errorf("plan --save tfplan.sources = %q, want Node %q", got, oracleSources)
	}

	writeBlockC4File(t, filepath.Join(fixture.envDir, "unrelated.txt"), []byte("keep\n"), 0o600)
	cleanArguments := append([]string{"clean-plans"}, fixture.commonArguments()...)
	oracle = runBlockC4Side(t, runtime, fixture, true, cleanArguments, "")
	if _, err := os.Stat(filepath.Join(fixture.envDir, "tfplan")); !os.IsNotExist(err) {
		t.Fatalf("Node clean-plans tfplan stat error = %v, want not-exist", err)
	}

	fixture = prepareBlockC4Fixture(t, workspace)
	writeBlockC4File(t, filepath.Join(fixture.envDir, "tfplan"), oraclePlan, 0o600)
	writeBlockC4File(t, filepath.Join(fixture.envDir, "tfplan.sources"), oracleSources, 0o600)
	writeBlockC4File(t, filepath.Join(fixture.envDir, "unrelated.txt"), []byte("keep\n"), 0o600)
	candidate = runBlockC4Side(t, runtime, fixture, false, cleanArguments, "")
	compareBlockC4RunResult(t, "clean-plans", oracle, candidate)
	for _, name := range []string{"tfplan", "tfplan.sources"} {
		if _, err := os.Stat(filepath.Join(fixture.envDir, name)); !os.IsNotExist(err) {
			t.Errorf("Go clean-plans os.Stat(%q) error = %v, want not-exist", name, err)
		}
	}
	if got := readBlockC4File(t, filepath.Join(fixture.envDir, "unrelated.txt")); string(got) != "keep\n" {
		t.Errorf("Go clean-plans unrelated bytes = %q, want keep\\n", got)
	}
}

func blockC4PlanJSON(t *testing.T, change map[string]any) string {
	t.Helper()
	content, err := json.Marshal(map[string]any{
		"format_version":    "1.2",
		"terraform_version": "1.15.4",
		"complete":          true,
		"errored":           false,
		"resource_changes": []any{map[string]any{
			"address": `sample_resource.this["one"]`,
			"type":    "sample_resource",
			"change":  change,
		}},
		"output_changes": map[string]any{},
	})
	if err != nil {
		t.Fatalf("json.Marshal(Terraform show fixture) error: %v", err)
	}
	return string(content)
}

func resetBlockC4AssessmentOutputs(t *testing.T, fixture blockC4Fixture, report string) {
	t.Helper()
	for _, path := range []string{fixture.log, report, filepath.Join(fixture.workspace, "tmp")} {
		if err := os.RemoveAll(path); err != nil {
			t.Fatalf("os.RemoveAll(%q) error: %v", path, err)
		}
	}
	if err := os.MkdirAll(filepath.Join(fixture.workspace, "tmp"), 0o700); err != nil {
		t.Fatalf("os.MkdirAll(TMPDIR) error: %v", err)
	}
}

func blockC4ShellLiteral(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
}

func writeBlockC4AssessmentTerraform(t *testing.T, fixture blockC4Fixture, showJSON string) {
	t.Helper()
	writeBlockC4File(t, fixture.terraform, []byte(strings.Join([]string{
		"#!/bin/sh",
		"printf 'cwd=%s\\n' \"$PWD\" >> " + blockC4ShellLiteral(fixture.log),
		"printf 'arg=%s\\n' \"$@\" >> " + blockC4ShellLiteral(fixture.log),
		"printf '%s' " + blockC4ShellLiteral(showJSON),
		"exit 0",
		"",
	}, "\n")), 0o700)
}

func normalizeBlockC4AssessmentLog(content []byte) string {
	lines := strings.Split(string(content), "\n")
	for index, line := range lines {
		if strings.HasPrefix(line, "arg=") &&
			strings.Contains(line, "/infrawright-assessment-") &&
			strings.Contains(line, "/plan-") {
			lines[index] = "arg=<assessment-snapshot>"
		}
	}
	return strings.Join(lines, "\n")
}

func TestBlockC4AssessmentReportDifferentialAgainstNodeOracle(t *testing.T) {
	runtime := newBlockC4Runtime(t)
	fixture := prepareBlockC4Fixture(t, filepath.Join(t.TempDir(), "workspace"))
	planArguments := append([]string{"plan"}, fixture.commonArguments()...)
	planArguments = append(planArguments, "--save", "--terraform", fixture.terraform)
	prepared := runBlockC4Side(t, runtime, fixture, false, planArguments, "")
	if prepared.exit != 0 {
		t.Fatalf("preparing saved plan exit = %d, want 0; stderr: %s", prepared.exit, prepared.stderr)
	}

	report := filepath.Join(fixture.workspace, "assessment.json")
	resetBlockC4AssessmentOutputs(t, fixture, report)
	arguments := append([]string{"assert-clean"}, fixture.commonArguments()...)
	arguments = append(arguments, "--report", report, "--terraform", fixture.terraform)
	showJSON := blockC4PlanJSON(t, map[string]any{
		"actions": []any{"no-op"}, "before": map[string]any{}, "after": map[string]any{},
	})
	writeBlockC4AssessmentTerraform(t, fixture, showJSON)
	oracle := runBlockC4Side(t, runtime, fixture, true, arguments, showJSON)
	if oracle.exit != 0 {
		t.Fatalf("Node assert-clean exit = %d, want 0; stdout=%q stderr=%q", oracle.exit, oracle.stdout, oracle.stderr)
	}
	oracleLog := readBlockC4File(t, fixture.log)
	oracleReport := readBlockC4File(t, report)

	resetBlockC4AssessmentOutputs(t, fixture, report)
	candidate := runBlockC4Side(t, runtime, fixture, false, arguments, showJSON)
	compareBlockC4RunResult(t, "assert-clean --report", oracle, candidate)
	if got, want := normalizeBlockC4AssessmentLog(readBlockC4File(t, fixture.log)),
		normalizeBlockC4AssessmentLog(oracleLog); got != want {
		t.Errorf("assert-clean Terraform argv log = %q, want Node %q", got, want)
	}
	if got := readBlockC4File(t, report); !bytes.Equal(got, oracleReport) {
		t.Errorf("assert-clean REPORT bytes differ\nGo:   %s\nNode: %s", got, oracleReport)
	}
}

func TestBlockC4DispatchParseDifferentialAgainstNodeOracle(t *testing.T) {
	runtime := newBlockC4Runtime(t)
	tests := []struct {
		name      string
		arguments []string
	}{
		{name: "plan requires tenant", arguments: []string{"plan"}},
		{name: "clean plans duplicate tenant", arguments: []string{"clean-plans", "--tenant", "one", "--tenant", "two"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			oracle := runBinary(t, runtime.repository, runtime.node,
				append([]string{runtime.oracleBundle}, test.arguments...))
			candidate := runBinary(t, runtime.repository, runtime.candidate, test.arguments)
			compareBlockC4RunResult(t, test.name, oracle, candidate)
		})
	}
}
