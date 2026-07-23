package main

import (
	"bytes"
	"crypto/sha1" // #nosec G505 -- frozen Oracle instance-name compatibility in a test fixture.
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const blockD5OracleSHA256 = "ce48c2c6a1cc01254866c5a7eb98b3eef1c90e6c45b69aff7df7aed80c822fa2"

// blockD5Runtime is deliberately stricter than the older optional differential
// lanes: D5 is accepted only against this exact frozen Node authority. A
// missing or mismatched bundle fails closed instead of silently skipping.
func newBlockD5Runtime(t *testing.T) blockC4Runtime {
	t.Helper()
	repository := repoRoot(t)
	oracleBundle := frozenNodeOraclePath(t)
	oracleBytes, err := os.ReadFile(oracleBundle)
	if err != nil {
		t.Fatalf("frozen Node oracle unavailable at %s: %v", oracleBundle, err)
	}
	if digest := fmt.Sprintf("%x", sha256.Sum256(oracleBytes)); digest != blockD5OracleSHA256 {
		t.Fatalf("frozen Node oracle digest = %s, want %s", digest, blockD5OracleSHA256)
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatalf("node unavailable for required D5 differential lane: %v", err)
	}
	candidateFile, err := os.CreateTemp(filepath.Join(repository, "dist"), "iw-go-diff-block-d5-*")
	if err != nil {
		t.Fatalf("os.CreateTemp(dist/iw-go-diff-block-d5-*) error: %v", err)
	}
	candidate := candidateFile.Name()
	if err := candidateFile.Close(); err != nil {
		t.Fatalf("closing candidate placeholder: %v", err)
	}
	if err := os.Remove(candidate); err != nil {
		t.Fatalf("removing candidate placeholder: %v", err)
	}
	build := exec.Command("go", "build", "-o", candidate, ".")
	build.Dir = filepath.Join(repository, "go", "cmd", "iw")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build ./cmd/iw error: %v\n%s", err, output)
	}
	t.Cleanup(func() { _ = os.Remove(candidate) })
	return blockC4Runtime{repository: repository, node: node, oracleBundle: oracleBundle, candidate: candidate}
}

func TestBlockD5DispatchAndExitDifferentialAgainstFrozenNodeOracle(t *testing.T) {
	runtime := newBlockD5Runtime(t)
	tests := []struct {
		name      string
		arguments []string
	}{
		{name: "adopt requires input and tenant", arguments: []string{"adopt", "--in", "input"}},
		{name: "stage requires tenant", arguments: []string{"stage-imports"}},
		{name: "apply invalid tenant uses legacy usage exit", arguments: []string{"apply", "--tenant", "INVALID"}},
		{name: "apply branch refusal before inputs or Terraform", arguments: []string{"apply", "--main-branch", "__block_d5_never__"}},
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

func prepareBlockD5AdoptionClassificationFixture(t *testing.T, workspace string) blockC4Fixture {
	t.Helper()
	fixture := prepareBlockC4Fixture(t, workspace)
	writeBlockC4JSON(t, filepath.Join(fixture.packs, "sample", "registry.json"), map[string]any{
		"sample_resource": map[string]any{
			"generate": true,
			"product":  "sample",
			"adopt": map[string]any{
				"key_field": "name",
				"skip_if":   []any{map[string]any{"system": true}},
				"unsupported_if": []any{map[string]any{
					"evidence": []any{"https://example.invalid/?a=1&b=2"},
					"match":    map[string]any{"mode": "blocked"},
					"provider": map[string]any{"source": "example/sample", "version": "1.0.0"},
					"reason":   "fixture is not round-trippable",
				}},
			},
		},
	})
	input := filepath.Join(workspace, "input")
	writeBlockC4JSON(t, filepath.Join(input, "sample_resource.json"), []any{
		map[string]any{"system": true},
		map[string]any{"name": "line\u2028paragraph\u2029", "system": true},
		map[string]any{"name": json.Number("9007199254740993"), "system": true},
		map[string]any{"id": json.Number("1.25e+7"), "system": true},
		map[string]any{"id": "blocked", "name": "<blocked & item>", "mode": "blocked"},
	})
	return fixture
}

func TestBlockD5AdoptionClassificationDifferentialAgainstFrozenNodeOracle(t *testing.T) {
	runtime := newBlockD5Runtime(t)
	workspace := filepath.Join(t.TempDir(), "workspace")
	fixture := prepareBlockD5AdoptionClassificationFixture(t, workspace)
	arguments := append([]string{"adopt", "--in", filepath.Join(workspace, "input")}, fixture.commonArguments()...)
	arguments = append(arguments, "--terraform", fixture.terraform)
	oracle := runBlockC4Side(t, runtime, fixture, true, arguments, "")
	if oracle.exit != 1 {
		t.Fatalf("Node adoption classification exit = %d, want 1; stdout=%q stderr=%q", oracle.exit, oracle.stdout, oracle.stderr)
	}
	if _, err := os.Stat(fixture.log); !os.IsNotExist(err) {
		t.Fatalf("Node unsupported preflight invoked fake Terraform; log stat error = %v", err)
	}
	candidate := runBlockC4Side(t, runtime, fixture, false, arguments, "")
	compareBlockC4RunResult(t, "adopt unsupported classification", oracle, candidate)
	if _, err := os.Stat(fixture.log); !os.IsNotExist(err) {
		t.Fatalf("Go unsupported preflight invoked fake Terraform; log stat error = %v", err)
	}
}

func blockD5OracleAddress(key string) string {
	digest := fmt.Sprintf("%x", sha1.Sum([]byte(key)))
	return `sample_resource.iw_` + digest[:16]
}

func prepareBlockD5SuccessfulAdoptFixture(t *testing.T, workspace string) (blockC4Fixture, string) {
	t.Helper()
	fixture := prepareBlockC4Fixture(t, workspace)
	writeBlockC4JSON(t, filepath.Join(fixture.packs, "sample", "registry.json"), map[string]any{
		"sample_resource": map[string]any{
			"generate": true, "product": "sample",
			"adopt": map[string]any{"import_id": "{id}", "key_field": "name"},
		},
	})
	writeBlockC4JSON(t, filepath.Join(fixture.packs, "sample", "schemas", "provider", "sample.json"), map[string]any{
		"provider":            map[string]any{"block": map[string]any{}},
		"data_source_schemas": map[string]any{},
		"resource_schemas": map[string]any{
			"sample_resource": map[string]any{"block": map[string]any{
				"attributes": map[string]any{
					"id":   map[string]any{"computed": true, "type": "string"},
					"name": map[string]any{"optional": true, "type": "string"},
				},
				"block_types": map[string]any{},
			}},
		},
	})
	input := filepath.Join(workspace, "input")
	writeBlockC4JSON(t, filepath.Join(input, "sample_resource.json"), []any{
		map[string]any{"id": "one", "name": "fixture"},
	})
	address := blockD5OracleAddress("fixture")
	resource := map[string]any{
		"address": address, "mode": "managed", "type": "sample_resource",
		"provider_name":    "registry.terraform.io/example/sample",
		"values":           map[string]any{"id": "one", "name": "fixture"},
		"sensitive_values": map[string]any{},
	}
	change := map[string]any{
		"address": address, "mode": "managed", "type": "sample_resource",
		"provider_name": "registry.terraform.io/example/sample",
		"change": map[string]any{
			"actions": []any{"no-op"}, "importing": map[string]any{"id": "one"},
			"before": resource["values"], "after": resource["values"], "after_unknown": false,
			"before_sensitive": map[string]any{}, "after_sensitive": map[string]any{},
		},
	}
	plan := map[string]any{
		"format_version": "1.2", "terraform_version": "1.15.4", "complete": true,
		"errored": false, "applyable": true, "resource_changes": []any{change},
		"planned_values": map[string]any{"root_module": map[string]any{"resources": []any{resource}}},
		"prior_state": map[string]any{
			"format_version": "1.2", "terraform_version": "1.15.4",
			"values": map[string]any{"root_module": map[string]any{"resources": []any{resource}}},
		},
	}
	planJSON, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	writeBlockC4File(t, fixture.terraform, []byte(strings.Join([]string{
		"#!/bin/sh",
		`printf 'cwd=%s\n' "$PWD" >> "$FAKE_TF_LOG"`,
		`printf 'arg=%s\n' "$@" >> "$FAKE_TF_LOG"`,
		`if [ "$1" = "apply" ]; then printf '%s\n' forbidden-apply >> "$FAKE_TF_LOG"; exit 97; fi`,
		`if [ "$1" = "plan" ]; then`,
		`  for argument in "$@"; do`,
		`    case "$argument" in`,
		`      -generate-config-out=*) printf '%s\n' 'resource "sample_resource" "generated" {' '  name = "fixture"' '}' > "${argument#-generate-config-out=}";;`,
		`      -out=*) printf '%s' opaque-plan > "${argument#-out=}";;`,
		`    esac`,
		`  done`,
		`elif [ "$1" = "show" ]; then printf '%s' "$FAKE_TF_SHOW_JSON"; fi`,
		"exit 0", "",
	}, "\n")), 0o700)
	return fixture, string(planJSON)
}

func runBlockD5AdoptSide(t *testing.T, runtime blockC4Runtime, fixture blockC4Fixture, oracle bool, arguments []string, planJSON string) runResult {
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
		"FAKE_TF_SHOW_JSON=" + planJSON,
		"INFRAWRIGHT_ORACLE_STATE_SOURCE=accepted-plan",
		"INFRAWRIGHT_PACKS=", "INFRAWRIGHT_PACK_PROFILE=", "INFRAWRIGHT_DEPLOYMENT=", "TF=",
	})
}

func blockD5GeneratedArtifactTree(tree map[string][]byte) map[string][]byte {
	artifacts := make(map[string][]byte)
	for path, content := range tree {
		if strings.HasPrefix(path, "config/") || strings.HasPrefix(path, "imports/") {
			artifacts[path] = content
		}
	}
	return artifacts
}

func TestBlockD5AdoptGeneratedArtifactBytesDifferentialAgainstFrozenNodeOracle(t *testing.T) {
	runtime := newBlockD5Runtime(t)
	fixture, planJSON := prepareBlockD5SuccessfulAdoptFixture(t, filepath.Join(t.TempDir(), "workspace"))
	arguments := append([]string{"adopt", "--in", filepath.Join(fixture.workspace, "input")}, fixture.commonArguments()...)
	arguments = append(arguments, "--terraform", fixture.terraform)
	oracle := runBlockD5AdoptSide(t, runtime, fixture, true, arguments, planJSON)
	if oracle.exit != 0 {
		t.Fatalf("Node accepted-plan adopt exit = %d; stdout=%q stderr=%q", oracle.exit, oracle.stdout, oracle.stderr)
	}
	oracleTree := treeBytes(t, fixture.workspace)
	oracleLog := string(oracleTree["terraform.log"])
	if strings.Contains(oracleLog, "forbidden-apply") || strings.Contains(oracleLog, "arg=apply\n") {
		t.Fatalf("Node accepted-plan fixture reached Apply:\n%s", oracleLog)
	}
	for _, path := range []string{
		filepath.Join(fixture.workspace, "config"), filepath.Join(fixture.workspace, "imports"), fixture.log,
	} {
		if err := os.RemoveAll(path); err != nil {
			t.Fatal(err)
		}
	}
	candidate := runBlockD5AdoptSide(t, runtime, fixture, false, arguments, planJSON)
	compareBlockC4RunResult(t, "adopt accepted-plan", oracle, candidate)
	candidateTree := treeBytes(t, fixture.workspace)
	oracleArtifacts := blockD5GeneratedArtifactTree(oracleTree)
	candidateArtifacts := blockD5GeneratedArtifactTree(candidateTree)
	if !reflect.DeepEqual(candidateArtifacts, oracleArtifacts) {
		t.Errorf("adopt generated/import artifact tree = %#v, want frozen Node %#v", candidateArtifacts, oracleArtifacts)
	}
	candidateLog := string(candidateTree["terraform.log"])
	if strings.Contains(candidateLog, "forbidden-apply") || strings.Contains(candidateLog, "arg=apply\n") {
		t.Fatalf("Go accepted-plan fixture reached Apply:\n%s", candidateLog)
	}
}

func blockD5StagingPaths(fixture blockC4Fixture) (sourceImports, sourceMoves, destinationImports, destinationMoves string) {
	return filepath.Join(fixture.workspace, "imports", "tenant", "sample_resource_imports.tf"),
		filepath.Join(fixture.workspace, "imports", "tenant", "sample_resource_moves.tf"),
		filepath.Join(fixture.envDir, "sample_resource_imports.tf"),
		filepath.Join(fixture.envDir, "sample_resource_moves.tf")
}

func removeBlockD5StagedCopies(t *testing.T, paths ...string) {
	t.Helper()
	for _, path := range paths {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			t.Fatalf("os.Remove(%q): %v", path, err)
		}
	}
}

func TestBlockD5StagingArtifactBytesAndExitDifferentialAgainstFrozenNodeOracle(t *testing.T) {
	runtime := newBlockD5Runtime(t)
	fixture := prepareBlockC4Fixture(t, filepath.Join(t.TempDir(), "workspace"))
	sourceImports, sourceMoves, destinationImports, destinationMoves := blockD5StagingPaths(fixture)
	importsBytes := []byte(strings.Join([]string{
		"import {", `  to = sample_resource.this["one"]`, `  id = "tenant:one"`, "}", "",
	}, "\n"))
	movesBytes := []byte(strings.Join([]string{
		"moved {", `  from = sample_resource.this["old"]`, `  to = sample_resource.this["one"]`, "}", "",
	}, "\n"))
	writeBlockC4File(t, sourceImports, importsBytes, 0o600)
	writeBlockC4File(t, sourceMoves, movesBytes, 0o600)
	arguments := append([]string{"stage-imports"}, fixture.commonArguments()...)

	oracle := runBlockC4Side(t, runtime, fixture, true, arguments, "")
	if oracle.exit != 0 {
		t.Fatalf("Node stage-imports exit = %d; stderr=%q", oracle.exit, oracle.stderr)
	}
	oracleImports := readBlockC4File(t, destinationImports)
	oracleMoves := readBlockC4File(t, destinationMoves)
	removeBlockD5StagedCopies(t, destinationImports, destinationMoves)

	candidate := runBlockC4Side(t, runtime, fixture, false, arguments, "")
	compareBlockC4RunResult(t, "stage-imports", oracle, candidate)
	if got := readBlockC4File(t, destinationImports); !bytes.Equal(got, oracleImports) || !bytes.Equal(got, importsBytes) {
		t.Errorf("staged import bytes = %q, want frozen Node/source %q", got, oracleImports)
	}
	if got := readBlockC4File(t, destinationMoves); !bytes.Equal(got, oracleMoves) || !bytes.Equal(got, movesBytes) {
		t.Errorf("staged move bytes = %q, want frozen Node/source %q", got, oracleMoves)
	}

	unstageArguments := append([]string{"unstage-imports"}, fixture.commonArguments()...)
	oracle = runBlockC4Side(t, runtime, fixture, true, unstageArguments, "")
	if oracle.exit != 0 {
		t.Fatalf("Node unstage-imports exit = %d; stderr=%q", oracle.exit, oracle.stderr)
	}
	writeBlockC4File(t, destinationImports, importsBytes, 0o600)
	writeBlockC4File(t, destinationMoves, movesBytes, 0o600)
	candidate = runBlockC4Side(t, runtime, fixture, false, unstageArguments, "")
	compareBlockC4RunResult(t, "unstage-imports", oracle, candidate)
	for _, path := range []string{destinationImports, destinationMoves} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("unstage left %s: stat error = %v", path, err)
		}
	}

	// The state-aware CLI path is qualified only through an explicit local fake
	// executable. Its empty state-list result keeps the import, and the exact
	// init/state-list argv transcript must match the frozen Node command.
	stateAwareArguments := append([]string{"stage-imports"}, fixture.commonArguments()...)
	stateAwareArguments = append(stateAwareArguments,
		"--state-aware", "--backend-config", filepath.Join(fixture.workspace, "backend.hcl"),
		"--terraform", fixture.terraform,
	)
	oracle = runBlockC4Side(t, runtime, fixture, true, stateAwareArguments, "")
	if oracle.exit != 0 {
		t.Fatalf("Node state-aware stage exit = %d; stderr=%q", oracle.exit, oracle.stderr)
	}
	oracleLog := readBlockC4File(t, fixture.log)
	oracleImports = readBlockC4File(t, destinationImports)
	removeBlockD5StagedCopies(t, destinationImports, destinationMoves, fixture.log)
	candidate = runBlockC4Side(t, runtime, fixture, false, stateAwareArguments, "")
	compareBlockC4RunResult(t, "stage-imports --state-aware", oracle, candidate)
	if got := readBlockC4File(t, fixture.log); !bytes.Equal(got, oracleLog) {
		t.Errorf("state-aware Terraform argv = %q, want frozen Node %q", got, oracleLog)
	}
	if got := readBlockC4File(t, destinationImports); !bytes.Equal(got, oracleImports) {
		t.Errorf("state-aware staged import bytes = %q, want frozen Node %q", got, oracleImports)
	}
}

func runBlockD5ApplySide(t *testing.T, runtime blockC4Runtime, fixture blockC4Fixture, oracle bool, arguments []string, showJSON string) runResult {
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
		"BUILD_SOURCEBRANCH=refs/heads/main",
		"INFRAWRIGHT_PACKS=", "INFRAWRIGHT_PACK_PROFILE=", "INFRAWRIGHT_DEPLOYMENT=", "TF=",
	})
}

func normalizeBlockD5ApplyLog(content []byte) string {
	lines := strings.Split(string(content), "\n")
	for index, line := range lines {
		if strings.HasPrefix(line, "arg=") && (strings.Contains(line, "/plan-") ||
			line == "arg=/dev/fd/3" || line == "arg=/proc/self/fd/3") {
			lines[index] = "arg=<assessment-snapshot>"
		}
	}
	return strings.Join(lines, "\n")
}

func TestBlockD5ApplyIncompletePlanFailsBeforeFakeApplyDifferential(t *testing.T) {
	runtime := newBlockD5Runtime(t)
	fixture := prepareBlockC4Fixture(t, filepath.Join(t.TempDir(), "workspace"))
	planArguments := append([]string{"plan"}, fixture.commonArguments()...)
	planArguments = append(planArguments, "--save", "--terraform", fixture.terraform)
	prepared := runBlockC4Side(t, runtime, fixture, false, planArguments, "")
	if prepared.exit != 0 {
		t.Fatalf("preparing saved plan exit = %d; stderr=%q", prepared.exit, prepared.stderr)
	}
	incomplete, err := json.Marshal(map[string]any{
		"format_version": "1.2", "terraform_version": "1.15.4",
		"complete": false, "errored": false, "resource_changes": []any{}, "output_changes": map[string]any{},
	})
	if err != nil {
		t.Fatal(err)
	}
	writeBlockC4AssessmentTerraform(t, fixture, string(incomplete))
	if err := os.Remove(fixture.log); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	arguments := append([]string{"apply"}, fixture.commonArguments()...)
	arguments = append(arguments,
		"--allow-destroy", "--allow-plan-changes",
		"--main-branch", "main", "--terraform", fixture.terraform,
	)
	oracle := runBlockD5ApplySide(t, runtime, fixture, true, arguments, string(incomplete))
	if oracle.exit != 1 || !bytes.Contains(oracle.stderr, []byte("plan must be complete before assessment")) {
		t.Fatalf("Node incomplete Apply = exit %d stderr %q", oracle.exit, oracle.stderr)
	}
	oracleLog := readBlockC4File(t, fixture.log)
	if bytes.Contains(oracleLog, []byte("arg=apply\n")) {
		t.Fatalf("Node incomplete-plan fixture reached Apply:\n%s", oracleLog)
	}
	if err := os.Remove(fixture.log); err != nil {
		t.Fatal(err)
	}
	candidate := runBlockD5ApplySide(t, runtime, fixture, false, arguments, string(incomplete))
	compareBlockC4RunResult(t, "apply incomplete plan", oracle, candidate)
	candidateLog := readBlockC4File(t, fixture.log)
	if bytes.Contains(candidateLog, []byte("arg=apply\n")) {
		t.Fatalf("Go incomplete-plan fixture reached Apply:\n%s", candidateLog)
	}
	if got, want := normalizeBlockD5ApplyLog(candidateLog), normalizeBlockD5ApplyLog(oracleLog); got != want {
		t.Errorf("incomplete Apply fake Terraform transcript = %q, want frozen Node %q", got, want)
	}
}

func TestBlockD5DifferentialFixturesContainNoCredentialOrLiveApplySurface(t *testing.T) {
	source, err := os.ReadFile("block_d5_differential_test.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"ZIA_" + "USERNAME", "ZPA_" + "CLIENT_ID", "terraform " + "apply", "https://" + "api."} {
		if bytes.Contains(source, []byte(forbidden)) {
			t.Fatalf("D5 differential source contains forbidden live surface %q", forbidden)
		}
	}
}
