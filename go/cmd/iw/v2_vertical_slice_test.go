package main

// This file is the opt-in, hermetic half of the Go runtime contract §5's
// vertical-slice checkpoint. It treats the built Go CLI as a black box and
// deliberately gives it a PATH containing Terraform but no Node runtime.

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/dvmrry/infrawright-dev/go/internal/terraformcmd"
)

const (
	v2CheckpointEnv  = "INFRAWRIGHT_V2_CHECKPOINT"
	v2ResourceType   = "zia_rule_labels"
	v2Tenant         = "demo"
	v2CommandTimeout = 5 * time.Minute
	v2MaxStderrBytes = 1 * 1024 * 1024
	v2MaxStdoutBytes = 4 * 1024 * 1024
)

type v2TerraformChange struct {
	Address string `json:"address"`
	Change  struct {
		Actions []string       `json:"actions"`
		After   map[string]any `json:"after"`
		Before  any            `json:"before"`
	} `json:"change"`
}

type v2TerraformSummary struct {
	Errored int    `json:"errored"`
	Failed  int    `json:"failed"`
	Passed  int    `json:"passed"`
	Skipped int    `json:"skipped"`
	Status  string `json:"status"`
}

type v2TerraformEvent struct {
	Terraform   string `json:"terraform"`
	TestRunName string `json:"@testrun"`
	TestPlan    *struct {
		ResourceChanges []v2TerraformChange `json:"resource_changes"`
	} `json:"test_plan"`
	TestRun *struct {
		Progress string `json:"progress"`
		Run      string `json:"run"`
		Status   string `json:"status"`
	} `json:"test_run"`
	TestSummary *v2TerraformSummary `json:"test_summary"`
	Type        string              `json:"type"`
}

func v2ReadFile(t *testing.T, path string) []byte {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("os.ReadFile(%q) error = %v, want nil", path, err)
	}
	return content
}

func v2RequireFileBytes(t *testing.T, gotPath, wantPath string) {
	t.Helper()
	got := v2ReadFile(t, gotPath)
	want := v2ReadFile(t, wantPath)
	if !bytes.Equal(got, want) {
		t.Errorf("file bytes mismatch for %q against %q\n got: %q\nwant: %q", gotPath, wantPath, got, want)
	}
}

func v2RequireTreeManifest(t *testing.T, label string, tree map[string][]byte, expected []string) {
	t.Helper()
	actual := make([]string, 0, len(tree))
	for path := range tree {
		actual = append(actual, path)
	}
	sort.Strings(actual)
	expected = append([]string(nil), expected...)
	sort.Strings(expected)
	if got, want := strings.Join(actual, "\n"), strings.Join(expected, "\n"); got != want {
		t.Errorf("%s file manifest differs\n got:\n%s\nwant:\n%s", label, got, want)
	}
}

func v2BuildGoBinary(t *testing.T, repositoryRoot string) string {
	t.Helper()
	goExecutable, err := exec.LookPath("go")
	if err != nil {
		t.Fatalf("exec.LookPath(%q) error = %v, want a Go executable", "go", err)
	}
	goExecutable, err = filepath.Abs(goExecutable)
	if err != nil {
		t.Fatalf("filepath.Abs(%q) error = %v, want nil", goExecutable, err)
	}
	home := t.TempDir()
	runtimeRoot := t.TempDir()
	binDirectory := filepath.Join(runtimeRoot, "bin")
	if err := os.Mkdir(binDirectory, 0o700); err != nil {
		t.Fatalf("create checkpoint binary directory: %v", err)
	}
	goBinary := filepath.Join(binDirectory, "iw-go-v2-checkpoint")
	environment := []string{
		"CGO_ENABLED=0",
		"GOCACHE=" + filepath.Join(home, "go-build"),
		"GOENV=off",
		"GOFLAGS=",
		"GOMODCACHE=" + filepath.Join(home, "go-mod"),
		"GOPROXY=off",
		"GOSUMDB=off",
		"GOTOOLCHAIN=local",
		"GOWORK=off",
		"HOME=" + home,
		"PATH=" + filepath.Dir(goExecutable),
		"TMPDIR=" + t.TempDir(),
	}
	v2RunSuccessfully(
		t,
		filepath.Join(repositoryRoot, "go", "cmd", "iw"),
		goExecutable,
		[]string{"build", "-trimpath", "-o", goBinary, "."},
		environment,
	)
	hash := sha256.Sum256(v2ReadFile(t, goBinary))
	t.Logf("Go candidate: sha256=%x", hash)
	return goBinary
}

func v2IsolatedPath(t *testing.T, terraform string) string {
	t.Helper()
	directory := t.TempDir()
	terraformLink := filepath.Join(directory, "terraform")
	if err := os.Symlink(terraform, terraformLink); err != nil {
		t.Fatalf("os.Symlink(%q, %q) error = %v, want nil", terraform, terraformLink, err)
	}
	if _, err := os.Stat(filepath.Join(directory, "node")); !os.IsNotExist(err) {
		t.Fatalf("os.Stat(%q) error = %v, want not-exist (Node must be absent from checkpoint PATH)", filepath.Join(directory, "node"), err)
	}
	return directory
}

func v2FullZIAPackRoot(t *testing.T, repositoryRoot string) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "packs")
	shared := filepath.Join(root, "_shared")
	if err := os.MkdirAll(shared, 0o755); err != nil {
		t.Fatalf("os.MkdirAll(%q) error = %v, want nil", shared, err)
	}
	links := map[string]string{
		filepath.Join(root, "zia"):       filepath.Join(repositoryRoot, "packs", "zia"),
		filepath.Join(shared, "zscaler"): filepath.Join(repositoryRoot, "packs", "_shared", "zscaler"),
	}
	for link, target := range links {
		if err := os.Symlink(target, link); err != nil {
			t.Fatalf("os.Symlink(%q, %q) error = %v, want the complete production ZIA pack", target, link, err)
		}
	}
	return root
}

func v2Environment(t *testing.T, repositoryRoot, isolatedPath, deploymentPath string, server *recordedFetchFixture) []string {
	t.Helper()
	home := t.TempDir()
	temporaryBase := os.TempDir()
	if runtime.GOOS != "windows" {
		// Terraform provider plugins communicate over Unix sockets, whose path
		// limit is shorter than macOS's ordinary per-test temporary path.
		temporaryBase = "/tmp"
	}
	temporary, err := os.MkdirTemp(temporaryBase, "iw-v2-")
	if err != nil {
		t.Fatalf("os.MkdirTemp(%q, %q) error = %v, want nil", temporaryBase, "iw-v2-", err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(temporary); err != nil {
			t.Errorf("os.RemoveAll(%q) error = %v, want nil", temporary, err)
		}
	})
	pluginCache := filepath.Join(home, "plugin-cache")
	if err := os.Mkdir(pluginCache, 0o700); err != nil {
		t.Fatalf("os.Mkdir(%q) error = %v, want nil", pluginCache, err)
	}
	return append(recordedFetchEnvironment(server),
		"CHECKPOINT_DISABLE=1",
		"HOME="+home,
		"INFRAWRIGHT_DEPLOYMENT="+deploymentPath,
		"INFRAWRIGHT_PACKAGE_ROOT="+repositoryRoot,
		"PATH="+isolatedPath,
		"TF_IN_AUTOMATION=1",
		"TF_INPUT=0",
		"TF_PLUGIN_CACHE_DIR="+pluginCache,
		"TMPDIR="+temporary,
	)
}

func v2TerraformEnvironment(t *testing.T, environment []string) []string {
	t.Helper()
	filtered := make([]string, 0, len(environment))
	for _, entry := range environment {
		name, _, found := strings.Cut(entry, "=")
		if !found {
			t.Fatalf("checkpoint environment entry %q has no equals sign", entry)
		}
		if strings.HasPrefix(name, "ZIA_") || strings.HasPrefix(name, "ZSCALER_") {
			continue
		}
		filtered = append(filtered, entry)
	}
	return filtered
}

func v2EnvironmentMap(t *testing.T, environment []string) map[string]string {
	t.Helper()
	result := make(map[string]string, len(environment))
	for _, entry := range environment {
		name, value, found := strings.Cut(entry, "=")
		if !found || name == "" {
			t.Fatalf("checkpoint environment entry %q is invalid", entry)
		}
		result[name] = value
	}
	return result
}

func v2RunSuccessfully(t *testing.T, directory, executable string, arguments, environment []string) runResult {
	t.Helper()
	timeoutMilliseconds := v2CommandTimeout.Milliseconds()
	result, err := terraformcmd.RunTerraformCommand(terraformcmd.TerraformCommandOptions{
		TerraformExecutable: executable,
		Argv:                arguments,
		CWD:                 directory,
		Environment:         v2EnvironmentMap(t, environment),
		Limits: &terraformcmd.TerraformCommandLimits{
			TimeoutMs:      &timeoutMilliseconds,
			MaxStdoutBytes: v2MaxStdoutBytes,
			MaxStderrBytes: v2MaxStderrBytes,
		},
		Output: terraformcmd.TerraformCommandOutputCapture,
	})
	if err != nil {
		t.Fatalf("bounded command %s %s failed: %v", executable, strings.Join(arguments, " "), err)
	}
	return runResult{exit: 0, stdout: result.Stdout}
}

func v2TerraformTestEvidence(output []byte) (string, error) {
	decoder := json.NewDecoder(bytes.NewReader(output))
	completed := map[string]string{}
	plans := map[string][]v2TerraformChange{}
	terraformVersion := ""
	var summary *v2TerraformSummary
	for {
		var event v2TerraformEvent
		err := decoder.Decode(&event)
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", fmt.Errorf("decode terraform test JSON event: %w", err)
		}
		switch event.Type {
		case "version":
			terraformVersion = event.Terraform
		case "test_run":
			if event.TestRun != nil && event.TestRun.Progress == "complete" {
				completed[event.TestRun.Run] = event.TestRun.Status
			}
		case "test_plan":
			if event.TestPlan == nil || event.TestRunName == "" {
				return "", errors.New("terraform test_plan event lacks a named run")
			}
			if _, duplicate := plans[event.TestRunName]; duplicate {
				return "", fmt.Errorf("terraform emitted multiple plans for run %q", event.TestRunName)
			}
			plans[event.TestRunName] = event.TestPlan.ResourceChanges
		case "test_summary":
			if event.TestSummary != nil {
				summary = event.TestSummary
			}
		}
	}

	if terraformVersion == "" {
		return "", errors.New("terraform test JSON omitted its version event")
	}
	if len(completed) != 2 || completed["empty_plan"] != "pass" || completed["config_plan"] != "pass" {
		return "", fmt.Errorf("terraform completed runs = %v, want exactly empty_plan=pass and config_plan=pass", completed)
	}
	if len(plans) != 2 {
		return "", fmt.Errorf("terraform plan events = %v, want exactly empty_plan and config_plan", plans)
	}
	if changes := plans["empty_plan"]; len(changes) != 0 {
		return "", fmt.Errorf("empty_plan resource changes = %+v, want none", changes)
	}
	configChanges := plans["config_plan"]
	if len(configChanges) != 1 {
		return "", fmt.Errorf("config_plan resource changes = %+v, want exactly one", configChanges)
	}
	change := configChanges[0]
	wantAddress := `module.zia_rule_labels.zia_rule_labels.this["testlabel_vcr_integration"]`
	if change.Address != wantAddress {
		return "", fmt.Errorf("config_plan address = %q, want %q", change.Address, wantAddress)
	}
	if got, want := strings.Join(change.Change.Actions, ","), "create"; got != want {
		return "", fmt.Errorf("config_plan actions = %q, want %q", got, want)
	}
	if change.Change.Before != nil {
		return "", fmt.Errorf("config_plan before = %#v, want nil for a create", change.Change.Before)
	}
	for attribute, want := range map[string]string{
		"description": "Test Description for VCR",
		"name":        "TestLabel_VCR_Integration",
	} {
		if got, ok := change.Change.After[attribute].(string); !ok || got != want {
			return "", fmt.Errorf("config_plan after[%q] = %#v, want %q", attribute, change.Change.After[attribute], want)
		}
	}
	if summary == nil || summary.Status != "pass" || summary.Passed != 2 ||
		summary.Failed != 0 || summary.Errored != 0 || summary.Skipped != 0 {
		return "", fmt.Errorf("terraform test summary = %+v, want pass with 2 passed and no failed/errored/skipped", summary)
	}
	return fmt.Sprintf(
		"Terraform %s\nempty_plan: pass; 0 resource changes\nconfig_plan: pass; create %s; name=%q; description=%q\nsummary: 2 passed, 0 failed, 0 errored, 0 skipped",
		terraformVersion,
		change.Address,
		change.Change.After["name"],
		change.Change.After["description"],
	), nil
}

func v2TerraformTestStream(t *testing.T, emptyChanges, configChanges []map[string]any) []byte {
	t.Helper()
	events := []map[string]any{
		{"type": "version", "terraform": "test-version"},
		{"type": "test_run", "test_run": map[string]any{"run": "empty_plan", "progress": "complete", "status": "pass"}},
		{"type": "test_plan", "@testrun": "empty_plan", "test_plan": map[string]any{"resource_changes": emptyChanges}},
		{"type": "test_run", "test_run": map[string]any{"run": "config_plan", "progress": "complete", "status": "pass"}},
		{"type": "test_plan", "@testrun": "config_plan", "test_plan": map[string]any{"resource_changes": configChanges}},
		{"type": "test_summary", "test_summary": map[string]any{"status": "pass", "passed": 2, "failed": 0, "errored": 0, "skipped": 0}},
	}
	var output bytes.Buffer
	encoder := json.NewEncoder(&output)
	for _, event := range events {
		if err := encoder.Encode(event); err != nil {
			t.Fatalf("encode synthetic terraform event: %v", err)
		}
	}
	return output.Bytes()
}

func TestV2TerraformTestEvidenceRejectsMisScopedPlans(t *testing.T) {
	expected := map[string]any{
		"address": `module.zia_rule_labels.zia_rule_labels.this["testlabel_vcr_integration"]`,
		"change": map[string]any{
			"actions": []string{"create"},
			"before":  nil,
			"after": map[string]any{
				"description": "Test Description for VCR",
				"name":        "TestLabel_VCR_Integration",
			},
		},
	}
	tests := []struct {
		name          string
		emptyChanges  []map[string]any
		configChanges []map[string]any
		wantError     string
	}{
		{
			name:          "config action attributed to empty plan",
			emptyChanges:  []map[string]any{expected},
			configChanges: []map[string]any{},
			wantError:     "empty_plan resource changes",
		},
		{
			name:          "config plan has an extra action",
			emptyChanges:  []map[string]any{},
			configChanges: []map[string]any{expected, expected},
			wantError:     "want exactly one",
		},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := v2TerraformTestEvidence(v2TerraformTestStream(t, testCase.emptyChanges, testCase.configChanges))
			if err == nil || !strings.Contains(err.Error(), testCase.wantError) {
				t.Fatalf("v2TerraformTestEvidence() error = %v, want error containing %q", err, testCase.wantError)
			}
		})
	}
}

func v2VerifyProviderLock(t *testing.T, repositoryRoot, environmentRoot string) string {
	t.Helper()
	var pack struct {
		Pin string `json:"pin"`
	}
	packPath := filepath.Join(repositoryRoot, "packs", "zia", "pack.json")
	if err := json.Unmarshal(v2ReadFile(t, packPath), &pack); err != nil {
		t.Fatalf("decode provider pin from %q: %v", packPath, err)
	}
	if pack.Pin == "" {
		t.Fatalf("provider pin in %q is empty", packPath)
	}
	lockPath := filepath.Join(environmentRoot, ".terraform.lock.hcl")
	lock := v2ReadFile(t, lockPath)
	for _, required := range []string{
		`provider "registry.terraform.io/zscaler/zia" {`,
		`version     = "` + pack.Pin + `"`,
		`constraints = "` + pack.Pin + `"`,
	} {
		if !bytes.Contains(lock, []byte(required)) {
			t.Fatalf("provider lock %q contains %q = false, want true", lockPath, required)
		}
	}
	if got := bytes.Count(lock, []byte("provider \"")); got != 1 {
		t.Fatalf("provider lock %q has %d provider blocks, want exactly one", lockPath, got)
	}
	hash := sha256.Sum256(lock)
	return fmt.Sprintf("provider registry.terraform.io/zscaler/zia %s; lock_sha256=%x", pack.Pin, hash)
}

func TestV2VerticalSliceCheckpoint(t *testing.T) {
	if os.Getenv(v2CheckpointEnv) != "1" {
		t.Skipf("set %s=1 to run the Go runtime v2 vertical-slice checkpoint", v2CheckpointEnv)
	}

	root := repoRoot(t)
	terraform, err := exec.LookPath("terraform")
	if err != nil {
		t.Fatalf("exec.LookPath(%q) error = %v, want a real Terraform executable", "terraform", err)
	}
	terraform, err = filepath.Abs(terraform)
	if err != nil {
		t.Fatalf("filepath.Abs(%q) error = %v, want nil", terraform, err)
	}
	terraform, err = filepath.EvalSymlinks(terraform)
	if err != nil {
		t.Fatalf("filepath.EvalSymlinks(%q) error = %v, want a regular Terraform executable", terraform, err)
	}
	goBinary := v2BuildGoBinary(t, root)

	wantPullPath := filepath.Join(root, "packs", "_shared", "zscaler", "demo", v2ResourceType+".json")
	wantPull := v2ReadFile(t, wantPullPath)
	server := newRecordedFetchFixtureForResource(
		t,
		"/api/v1/ruleLabels?page=1&pageSize=1000",
		wantPull,
	)

	workspace := t.TempDir()
	overlay := filepath.Join(workspace, "overlay")
	moduleDirectory := filepath.Join(overlay, "modules")
	deploymentPath := writeTransformDeployment(t, workspace, overlay, nil)
	isolatedPath := v2IsolatedPath(t, terraform)
	environment := v2Environment(t, root, isolatedPath, deploymentPath, server)

	profile := filepath.Join(root, "packs", "zia.packset.json")
	metadataArguments := []string{
		"--root", v2FullZIAPackRoot(t, root),
		"--profile", profile,
	}
	pulls := filepath.Join(workspace, "pulls", v2Tenant)
	fetchArguments := append([]string{
		"fetch", "--tenant", v2Tenant, "--out", pulls, "--resource", v2ResourceType,
	}, metadataArguments...)
	v2RunSuccessfully(t, workspace, goBinary, fetchArguments, environment)

	requests := takeRecordedFetchTranscript(t, server, "v2 checkpoint fetch")
	wantRequests := []recordedFetchRequest{
		{contract: "legacy-zia-auth", method: http.MethodPost, uri: "/api/v1/authenticatedSession"},
		{contract: "resource", method: http.MethodGet, uri: "/api/v1/ruleLabels?page=1&pageSize=1000"},
	}
	requireRecordedFetchTranscript(t, "v2 checkpoint fetch", requests, wantRequests)
	requireRecordedFetchTree(t, "v2 checkpoint fetch", treeBytes(t, pulls), map[string][]byte{
		v2ResourceType + ".json": wantPull,
	})

	transformArguments := append([]string{
		"transform", "--in", pulls, "--tenant", v2Tenant,
		"--deployment", deploymentPath, "--resource", v2ResourceType,
	}, metadataArguments...)
	v2RunSuccessfully(t, workspace, goBinary, transformArguments, environment)
	wantConfigPath := filepath.Join(root, "demo", "config", v2Tenant, v2ResourceType+".auto.tfvars.json")
	wantImportsPath := filepath.Join(root, "demo", "imports", v2Tenant, v2ResourceType+"_imports.tf")
	requireRecordedFetchTree(t, "v2 checkpoint transform", treeBytes(t, overlay), map[string][]byte{
		filepath.ToSlash(filepath.Join("config", v2Tenant, v2ResourceType+".auto.tfvars.json")): v2ReadFile(t, wantConfigPath),
		filepath.ToSlash(filepath.Join("imports", v2Tenant, v2ResourceType+"_imports.tf")):      v2ReadFile(t, wantImportsPath),
	})

	moduleGenerateArguments := append([]string{
		"modules", "generate", "--out", moduleDirectory,
		"--deployment", deploymentPath,
		"--resource", v2ResourceType,
	}, metadataArguments...)
	v2RunSuccessfully(t, workspace, goBinary, moduleGenerateArguments, environment)
	moduleValidateArguments := append([]string{
		"modules", "validate", "--out", moduleDirectory,
		"--deployment", deploymentPath, "--resource", v2ResourceType,
	}, metadataArguments...)
	v2RunSuccessfully(t, workspace, goBinary, moduleValidateArguments, environment)

	genEnvArguments := append([]string{
		"gen-env", "--tenant", v2Tenant, "--deployment", deploymentPath,
		"--resource", v2ResourceType,
	}, metadataArguments...)
	v2RunSuccessfully(t, workspace, goBinary, genEnvArguments, environment)

	generatedManifest := []string{
		filepath.ToSlash(filepath.Join("config", v2Tenant, v2ResourceType+".auto.tfvars.json")),
		filepath.ToSlash(filepath.Join("envs", v2Tenant, v2ResourceType, "README.md")),
		filepath.ToSlash(filepath.Join("envs", v2Tenant, v2ResourceType, "main.tf")),
		filepath.ToSlash(filepath.Join("envs", v2Tenant, v2ResourceType, "tests", "smoke.tftest.hcl")),
		filepath.ToSlash(filepath.Join("imports", v2Tenant, v2ResourceType+"_imports.tf")),
		filepath.ToSlash(filepath.Join("modules", v2ResourceType, "README.md")),
		filepath.ToSlash(filepath.Join("modules", v2ResourceType, "main.tf")),
		filepath.ToSlash(filepath.Join("modules", v2ResourceType, "outputs.tf")),
		filepath.ToSlash(filepath.Join("modules", v2ResourceType, "tests", "defaults.tftest.hcl")),
		filepath.ToSlash(filepath.Join("modules", v2ResourceType, "tests", "sample.auto.tfvars.json")),
		filepath.ToSlash(filepath.Join("modules", v2ResourceType, "variables.tf")),
		filepath.ToSlash(filepath.Join("modules", v2ResourceType, "versions.tf")),
	}
	v2RequireTreeManifest(t, "v2 checkpoint generated overlay", treeBytes(t, overlay), generatedManifest)
	environmentRoot := filepath.Join(overlay, "envs", v2Tenant, v2ResourceType)
	smokeTestPath := filepath.Join(environmentRoot, "tests", "smoke.tftest.hcl")
	smokeTest := string(v2ReadFile(t, smokeTestPath))
	for _, required := range []string{
		`mock_provider "zia" {}`,
		`run "empty_plan"`,
		`run "config_plan"`,
		v2ResourceType + ".auto.tfvars.json",
	} {
		if !strings.Contains(smokeTest, required) {
			t.Errorf("generated smoke test %q contains %q = false, want true\nsmoke test:\n%s", smokeTestPath, required, smokeTest)
		}
	}

	terraformEnvironment := v2TerraformEnvironment(t, environment)
	initResult := v2RunSuccessfully(t, environmentRoot, terraform, []string{"init", "-backend=false", "-input=false", "-no-color"}, terraformEnvironment)
	t.Logf("terraform init:\n%s", strings.TrimSpace(string(initResult.stdout)))
	t.Log(v2VerifyProviderLock(t, root, environmentRoot))
	validateResult := v2RunSuccessfully(t, environmentRoot, terraform, []string{"validate", "-no-color"}, terraformEnvironment)
	t.Logf("terraform validate:\n%s", strings.TrimSpace(string(validateResult.stdout)))
	testResult := v2RunSuccessfully(t, environmentRoot, terraform, []string{"test", "-no-color", "-verbose", "-json"}, terraformEnvironment)
	testEvidence, err := v2TerraformTestEvidence(testResult.stdout)
	if err != nil {
		t.Fatalf("verify terraform test evidence: %v", err)
	}
	t.Log(testEvidence)

	postFetchRequests := takeRecordedFetchTranscript(t, server, "v2 checkpoint post-fetch")
	requireRecordedFetchTranscript(t, "v2 checkpoint post-fetch", postFetchRequests, nil)
}
