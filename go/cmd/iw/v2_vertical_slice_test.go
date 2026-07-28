package main

// This file is the opt-in, hermetic half of the Go runtime contract §5's
// vertical-slice checkpoint. It treats the built Go CLI as a black box and
// deliberately gives it a PATH containing only Terraform.

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
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/dvmrry/infrawright-dev/go/internal/metadata"
	"github.com/dvmrry/infrawright-dev/go/internal/modulesgen"
	"github.com/dvmrry/infrawright-dev/go/internal/terraformcmd"
)

const (
	v2CheckpointEnv             = "INFRAWRIGHT_V2_CHECKPOINT"
	v2GoChecksumDB              = "sum.golang.org"
	v2GoModuleProvisioningPhase = "provision candidate Go module cache with go mod download"
	v2GoModuleProxy             = "https://proxy.golang.org"
	v2GoOfflineBuildPhase       = "build candidate Go binary from provisioned module cache with GOPROXY=off"
	v2ResourceType              = "zia_rule_labels"
	v2Tenant                    = "demo"
	v2CommandTimeout            = 5 * time.Minute
	v2MaxStderrBytes            = 1 * 1024 * 1024
	v2MaxStdoutBytes            = 4 * 1024 * 1024
)

// v2CommandRunner lets focused tests observe the two candidate-build phases.
type v2CommandRunner func(directory, executable string, arguments, environment []string) (runResult, error)

// v2GoBuildPhaseError identifies whether cache provisioning or offline build failed.
type v2GoBuildPhaseError struct {
	phase string
	cause error
}

func (e *v2GoBuildPhaseError) Error() string {
	return e.phase + ": " + e.cause.Error()
}

func (e *v2GoBuildPhaseError) Unwrap() error {
	return e.cause
}

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

type v2TerraformTestReport struct {
	completed        map[string]string
	plans            map[string][]v2TerraformChange
	summary          *v2TerraformSummary
	terraformVersion string
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
	goBinary, err := v2BuildGoBinaryWithRunner(t, repositoryRoot, func(
		directory, executable string,
		arguments, environment []string,
	) (runResult, error) {
		return v2RunBoundedCommand(t, directory, executable, arguments, environment)
	})
	if err != nil {
		t.Fatalf("v2BuildGoBinary(%q) error = %v, want nil", repositoryRoot, err)
	}
	return goBinary
}

func v2BuildGoBinaryWithRunner(t *testing.T, repositoryRoot string, run v2CommandRunner) (string, error) {
	t.Helper()
	goExecutable, err := exec.LookPath("go")
	if err != nil {
		return "", fmt.Errorf("locate Go executable: %w", err)
	}
	goExecutable, err = filepath.Abs(goExecutable)
	if err != nil {
		return "", fmt.Errorf("resolve Go executable %q: %w", goExecutable, err)
	}
	home := t.TempDir()
	runtimeRoot := t.TempDir()
	binDirectory := filepath.Join(runtimeRoot, "bin")
	if err := os.Mkdir(binDirectory, 0o700); err != nil {
		return "", fmt.Errorf("create checkpoint binary directory: %w", err)
	}
	goBinary := filepath.Join(binDirectory, "iw-go-v2-checkpoint")
	moduleCache := filepath.Join(home, "go-mod")
	commonEnvironment := []string{
		"CGO_ENABLED=0",
		"GOCACHE=" + filepath.Join(home, "go-build"),
		"GOENV=off",
		"GOMODCACHE=" + moduleCache,
		"GOTOOLCHAIN=local",
		"GOWORK=off",
		"HOME=" + home,
		"PATH=" + filepath.Dir(goExecutable),
		"TMPDIR=" + t.TempDir(),
	}
	moduleRoot := filepath.Join(repositoryRoot, "go")
	provisioningEnvironment := append(append([]string(nil), commonEnvironment...),
		"GOFLAGS=-modcacherw",
		"GOPROXY="+v2GoModuleProxy,
		"GOSUMDB="+v2GoChecksumDB,
	)
	if _, err := run(
		moduleRoot,
		goExecutable,
		[]string{"mod", "download"},
		provisioningEnvironment,
	); err != nil {
		return "", &v2GoBuildPhaseError{phase: v2GoModuleProvisioningPhase, cause: err}
	}
	offlineBuildEnvironment := append(append([]string(nil), commonEnvironment...),
		"GOFLAGS=",
		"GOPROXY=off",
		"GOSUMDB=off",
	)
	if _, err := run(
		filepath.Join(moduleRoot, "cmd", "iw"),
		goExecutable,
		[]string{"build", "-trimpath", "-o", goBinary, "."},
		offlineBuildEnvironment,
	); err != nil {
		return "", &v2GoBuildPhaseError{phase: v2GoOfflineBuildPhase, cause: err}
	}
	content, err := os.ReadFile(goBinary)
	if err != nil {
		return "", fmt.Errorf("read candidate Go binary %q: %w", goBinary, err)
	}
	hash := sha256.Sum256(content)
	t.Logf("Go candidate: sha256=%x", hash)
	return goBinary, nil
}

func TestV2BuildGoBinaryDownloadsModulesBeforeOfflineBuild(t *testing.T) {
	repositoryRoot := repoRoot(t)
	ambientModuleCache := filepath.Join(t.TempDir(), "ambient-module-cache")
	t.Setenv("GOMODCACHE", ambientModuleCache)
	t.Setenv("GOPROXY", "https://ambient.invalid")
	t.Setenv("GOSUMDB", "ambient.invalid")

	type invocation struct {
		directory   string
		executable  string
		arguments   []string
		environment []string
	}
	var invocations []invocation
	var provisionedMarker string
	run := func(directory, executable string, arguments, environment []string) (runResult, error) {
		call := invocation{
			directory:   directory,
			executable:  executable,
			arguments:   append([]string(nil), arguments...),
			environment: append([]string(nil), environment...),
		}
		callIndex := len(invocations)
		invocations = append(invocations, call)
		environmentMap := v2EnvironmentMap(t, environment)
		switch callIndex {
		case 0:
			provisionedMarker = filepath.Join(environmentMap["GOMODCACHE"], "provisioned")
			if err := os.MkdirAll(filepath.Dir(provisionedMarker), 0o700); err != nil {
				return runResult{}, fmt.Errorf("create fake provisioned module cache: %w", err)
			}
			if err := os.WriteFile(provisionedMarker, []byte("downloaded\n"), 0o600); err != nil {
				return runResult{}, fmt.Errorf("mark fake provisioned module cache: %w", err)
			}
		case 1:
			if _, err := os.Stat(provisionedMarker); err != nil {
				return runResult{}, fmt.Errorf("offline build observe provisioned module cache: %w", err)
			}
			if len(arguments) < 4 {
				return runResult{}, fmt.Errorf("offline build arguments = %q, want an output path", arguments)
			}
			if err := os.WriteFile(arguments[3], []byte("fake candidate\n"), 0o700); err != nil {
				return runResult{}, fmt.Errorf("write fake candidate Go binary: %w", err)
			}
		default:
			return runResult{}, fmt.Errorf("command invocation count = %d, want exactly two", callIndex+1)
		}
		return runResult{}, nil
	}

	goBinary, err := v2BuildGoBinaryWithRunner(t, repositoryRoot, run)
	if err != nil {
		t.Fatalf("v2BuildGoBinaryWithRunner(%q) error = %v, want nil", repositoryRoot, err)
	}
	if got, want := len(invocations), 2; got != want {
		t.Fatalf("v2BuildGoBinaryWithRunner(%q) invocation count = %d, want %d", repositoryRoot, got, want)
	}
	download, offlineBuild := invocations[0], invocations[1]
	if got, want := download.directory, filepath.Join(repositoryRoot, "go"); got != want {
		t.Errorf("go mod download directory = %q, want repository module root %q", got, want)
	}
	if got, want := strings.Join(download.arguments, " "), "mod download"; got != want {
		t.Errorf("provisioning arguments = %q, want %q", got, want)
	}
	if got, want := offlineBuild.directory, filepath.Join(repositoryRoot, "go", "cmd", "iw"); got != want {
		t.Errorf("offline go build directory = %q, want command package %q", got, want)
	}
	if got, want := strings.Join(offlineBuild.arguments, " "), "build -trimpath -o "+goBinary+" ."; got != want {
		t.Errorf("offline build arguments = %q, want %q", got, want)
	}
	if got, want := offlineBuild.executable, download.executable; got != want {
		t.Errorf("offline build Go executable = %q, want provisioning executable %q", got, want)
	}

	downloadEnvironment := v2EnvironmentMap(t, download.environment)
	offlineBuildEnvironment := v2EnvironmentMap(t, offlineBuild.environment)
	if got, want := downloadEnvironment["GOPROXY"], v2GoModuleProxy; got != want {
		t.Errorf("go mod download GOPROXY = %q, want %q", got, want)
	}
	if got, want := downloadEnvironment["GOSUMDB"], v2GoChecksumDB; got != want {
		t.Errorf("go mod download GOSUMDB = %q, want %q", got, want)
	}
	if got, want := downloadEnvironment["GOFLAGS"], "-modcacherw"; got != want {
		t.Errorf("go mod download GOFLAGS = %q, want cleanable module cache flag %q", got, want)
	}
	if got, want := offlineBuildEnvironment["GOPROXY"], "off"; got != want {
		t.Errorf("offline go build GOPROXY = %q, want %q", got, want)
	}
	if got, want := offlineBuildEnvironment["GOSUMDB"], "off"; got != want {
		t.Errorf("offline go build GOSUMDB = %q, want %q", got, want)
	}
	if got, want := offlineBuildEnvironment["GOFLAGS"], ""; got != want {
		t.Errorf("offline go build GOFLAGS = %q, want %q", got, want)
	}
	if got, want := offlineBuildEnvironment["GOMODCACHE"], downloadEnvironment["GOMODCACHE"]; got != want {
		t.Errorf("offline go build GOMODCACHE = %q, want provisioned cache %q", got, want)
	}
	if got := downloadEnvironment["GOMODCACHE"]; got == ambientModuleCache {
		t.Errorf("go mod download GOMODCACHE = ambient cache %q, want a fresh test cache", got)
	}
	if got, want := string(v2ReadFile(t, goBinary)), "fake candidate\n"; got != want {
		t.Errorf("fake candidate bytes = %q, want %q", got, want)
	}
}

func TestV2BuildGoBinaryDistinguishesProvisioningAndOfflineBuildFailures(t *testing.T) {
	repositoryRoot := repoRoot(t)
	tests := []struct {
		name      string
		failAt    int
		wantPhase string
	}{
		{
			name:      "provisioning",
			failAt:    0,
			wantPhase: v2GoModuleProvisioningPhase,
		},
		{
			name:      "offline build",
			failAt:    1,
			wantPhase: v2GoOfflineBuildPhase,
		},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			wantCause := errors.New("fake command failure")
			calls := 0
			_, err := v2BuildGoBinaryWithRunner(
				t,
				repositoryRoot,
				func(_, _ string, _, _ []string) (runResult, error) {
					callIndex := calls
					calls++
					if callIndex == testCase.failAt {
						return runResult{}, wantCause
					}
					return runResult{}, nil
				},
			)
			if !errors.Is(err, wantCause) {
				t.Fatalf("v2BuildGoBinaryWithRunner(%q) error = %v, want wrapped cause %v", repositoryRoot, err, wantCause)
			}
			var phaseError *v2GoBuildPhaseError
			if !errors.As(err, &phaseError) {
				t.Fatalf("v2BuildGoBinaryWithRunner(%q) error type = %T, want *v2GoBuildPhaseError", repositoryRoot, err)
			}
			if got, want := phaseError.phase, testCase.wantPhase; got != want {
				t.Errorf("v2BuildGoBinaryWithRunner(%q) failure phase = %q, want %q", repositoryRoot, got, want)
			}
			if got, want := calls, testCase.failAt+1; got != want {
				t.Errorf("v2BuildGoBinaryWithRunner(%q) command calls = %d, want %d", repositoryRoot, got, want)
			}
		})
	}
}

func v2IsolatedPath(t *testing.T, terraform string) string {
	t.Helper()
	directory := t.TempDir()
	terraformLink := filepath.Join(directory, "terraform")
	if err := os.Symlink(terraform, terraformLink); err != nil {
		t.Fatalf("os.Symlink(%q, %q) error = %v, want nil", terraform, terraformLink, err)
	}
	if _, err := os.Stat(filepath.Join(directory, "node")); !os.IsNotExist(err) {
		t.Fatalf("os.Stat(%q) error = %v, want not-exist (retired runtime must be absent from checkpoint PATH)", filepath.Join(directory, "node"), err)
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

func v2FocusedZPAPackRoot(t *testing.T, repositoryRoot string) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "packs")
	shared := filepath.Join(root, "_shared")
	if err := os.MkdirAll(shared, 0o755); err != nil {
		t.Fatalf("os.MkdirAll(%q) error = %v, want nil", shared, err)
	}
	links := map[string]string{
		filepath.Join(root, "zpa"):       filepath.Join(repositoryRoot, "packs", "zpa"),
		filepath.Join(shared, "zscaler"): filepath.Join(repositoryRoot, "packs", "_shared", "zscaler"),
	}
	for link, target := range links {
		if err := os.Symlink(target, link); err != nil {
			t.Fatalf("os.Symlink(%q, %q) error = %v, want the focused ZPA pack", target, link, err)
		}
	}
	return root
}

func v2PluginCacheDirectory(t *testing.T, home string) string {
	t.Helper()
	pluginCache := os.Getenv("TF_PLUGIN_CACHE_DIR")
	if pluginCache == "" {
		pluginCache = filepath.Join(home, "plugin-cache")
	}
	pluginCache, err := filepath.Abs(pluginCache)
	if err != nil {
		t.Fatalf("filepath.Abs(%q) error = %v, want nil", pluginCache, err)
	}
	if err := os.MkdirAll(pluginCache, 0o700); err != nil {
		t.Fatalf("os.MkdirAll(%q) error = %v, want nil", pluginCache, err)
	}
	return pluginCache
}

func v2RequiredTerraformExecutable(t *testing.T) string {
	t.Helper()
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
	return terraform
}

// v2FocusedTerraformEnvironment gives one-resource Terraform contracts only
// the runtime state they need. It deliberately does not require the vertical
// checkpoint's CLI build, fetch fixture, deployment, or opt-in environment.
func v2FocusedTerraformEnvironment(t *testing.T, terraform string) []string {
	t.Helper()
	home := t.TempDir()
	temporaryBase := os.TempDir()
	if runtime.GOOS != "windows" {
		temporaryBase = "/tmp"
	}
	temporary, err := os.MkdirTemp(temporaryBase, "iw-tf-contract-")
	if err != nil {
		t.Fatalf("os.MkdirTemp(%q, %q) error = %v, want nil", temporaryBase, "iw-tf-contract-", err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(temporary); err != nil {
			t.Errorf("os.RemoveAll(%q) error = %v, want nil", temporary, err)
		}
	})
	return []string{
		"CHECKPOINT_DISABLE=1",
		"HOME=" + home,
		"PATH=" + filepath.Dir(terraform),
		"TF_IN_AUTOMATION=1",
		"TF_INPUT=0",
		"TF_PLUGIN_CACHE_DIR=" + v2PluginCacheDirectory(t, home),
		"TMPDIR=" + temporary,
	}
}

func TestV2PluginCacheDirectoryUsesExplicitCacheWithHermeticFallback(t *testing.T) {
	t.Run("hermetic fallback", func(t *testing.T) {
		t.Setenv("TF_PLUGIN_CACHE_DIR", "")
		home := t.TempDir()
		got := v2PluginCacheDirectory(t, home)
		want := filepath.Join(home, "plugin-cache")
		if got != want {
			t.Fatalf("v2PluginCacheDirectory() = %q, want %q", got, want)
		}
	})

	t.Run("explicit reusable cache", func(t *testing.T) {
		cache := filepath.Join(t.TempDir(), "shared-plugin-cache")
		t.Setenv("TF_PLUGIN_CACHE_DIR", cache)
		got := v2PluginCacheDirectory(t, t.TempDir())
		if got != cache {
			t.Fatalf("v2PluginCacheDirectory() = %q, want explicit cache %q", got, cache)
		}
		if info, err := os.Stat(got); err != nil || !info.IsDir() {
			t.Fatalf("os.Stat(%q) = (%v, %v), want an existing directory", got, info, err)
		}
	})
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
	pluginCache := v2PluginCacheDirectory(t, home)
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

func v2RunBoundedCommand(t *testing.T, directory, executable string, arguments, environment []string) (runResult, error) {
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
		return runResult{}, err
	}
	return runResult{exit: 0, stdout: result.Stdout}, nil
}

func v2RunSuccessfully(t *testing.T, directory, executable string, arguments, environment []string) runResult {
	t.Helper()
	result, err := v2RunBoundedCommand(t, directory, executable, arguments, environment)
	if err != nil {
		t.Fatalf("bounded command %s %s failed: %v", executable, strings.Join(arguments, " "), err)
	}
	return result
}

func v2ParseTerraformTestReport(output []byte) (v2TerraformTestReport, error) {
	decoder := json.NewDecoder(bytes.NewReader(output))
	report := v2TerraformTestReport{
		completed: map[string]string{},
		plans:     map[string][]v2TerraformChange{},
	}
	for {
		var event v2TerraformEvent
		err := decoder.Decode(&event)
		if err == io.EOF {
			break
		}
		if err != nil {
			return v2TerraformTestReport{}, fmt.Errorf("decode terraform test JSON event: %w", err)
		}
		switch event.Type {
		case "version":
			report.terraformVersion = event.Terraform
		case "test_run":
			if event.TestRun != nil && event.TestRun.Progress == "complete" {
				if _, duplicate := report.completed[event.TestRun.Run]; duplicate {
					return v2TerraformTestReport{}, fmt.Errorf("terraform emitted multiple completed events for run %q", event.TestRun.Run)
				}
				report.completed[event.TestRun.Run] = event.TestRun.Status
			}
		case "test_plan":
			if event.TestPlan == nil || event.TestRunName == "" {
				return v2TerraformTestReport{}, errors.New("terraform test_plan event lacks a named run")
			}
			if _, duplicate := report.plans[event.TestRunName]; duplicate {
				return v2TerraformTestReport{}, fmt.Errorf("terraform emitted multiple plans for run %q", event.TestRunName)
			}
			report.plans[event.TestRunName] = event.TestPlan.ResourceChanges
		case "test_summary":
			if event.TestSummary != nil {
				if report.summary != nil {
					return v2TerraformTestReport{}, errors.New("terraform emitted multiple test summaries")
				}
				report.summary = event.TestSummary
			}
		}
	}
	return report, nil
}

func v2RequirePassedTerraformRuns(report v2TerraformTestReport, expected []string) error {
	if len(expected) == 0 {
		return errors.New("terraform expected run set is empty")
	}
	if report.terraformVersion == "" {
		return errors.New("terraform test JSON omitted its version event")
	}
	expected = append([]string(nil), expected...)
	sort.Strings(expected)
	completedNames := make([]string, 0, len(report.completed))
	for name, status := range report.completed {
		if status != "pass" {
			return fmt.Errorf("terraform completed run %q status = %q, want pass", name, status)
		}
		completedNames = append(completedNames, name)
	}
	sort.Strings(completedNames)
	if got, want := strings.Join(completedNames, ","), strings.Join(expected, ","); got != want {
		return fmt.Errorf("terraform completed runs = %q, want exactly %q", got, want)
	}
	wantPassed := len(expected)
	if report.summary == nil || report.summary.Status != "pass" || report.summary.Passed != wantPassed ||
		report.summary.Failed != 0 || report.summary.Errored != 0 || report.summary.Skipped != 0 {
		return fmt.Errorf(
			"terraform test summary = %+v, want pass with %d passed and no failed/errored/skipped",
			report.summary,
			wantPassed,
		)
	}
	return nil
}

func v2TerraformRunEvidence(output []byte, expected []string) (string, error) {
	report, err := v2ParseTerraformTestReport(output)
	if err != nil {
		return "", err
	}
	if err := v2RequirePassedTerraformRuns(report, expected); err != nil {
		return "", err
	}
	return fmt.Sprintf("Terraform %s; runs %s passed", report.terraformVersion, strings.Join(expected, ", ")), nil
}

func v2TerraformTestEvidence(output []byte) (string, error) {
	return v2TerraformRuleLabelEvidence(output, []string{"empty_plan", "config_plan"})
}

func v2TerraformRuleLabelEvidence(output []byte, expectedRuns []string) (string, error) {
	report, err := v2ParseTerraformTestReport(output)
	if err != nil {
		return "", err
	}
	if err := v2RequirePassedTerraformRuns(report, expectedRuns); err != nil {
		return "", err
	}
	if len(report.plans) != len(expectedRuns) {
		return "", fmt.Errorf("terraform plan events = %v, want exactly %s", report.plans, strings.Join(expectedRuns, " and "))
	}
	for _, run := range expectedRuns {
		if _, present := report.plans[run]; !present {
			return "", fmt.Errorf("terraform plan events = %v, want plan for run %q", report.plans, run)
		}
	}
	if changes, hasEmptyPlan := report.plans["empty_plan"]; hasEmptyPlan && len(changes) != 0 {
		return "", fmt.Errorf("empty_plan resource changes = %+v, want none", changes)
	}
	configChanges := report.plans["config_plan"]
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
	lines := []string{fmt.Sprintf("Terraform %s", report.terraformVersion)}
	if _, hasEmptyPlan := report.plans["empty_plan"]; hasEmptyPlan {
		lines = append(lines, "empty_plan: pass; 0 resource changes")
	}
	lines = append(lines,
		fmt.Sprintf("config_plan: pass; create %s; name=%q; description=%q", change.Address, change.Change.After["name"], change.Change.After["description"]),
		fmt.Sprintf("summary: %d passed, 0 failed, 0 errored, 0 skipped", len(expectedRuns)),
	)
	return strings.Join(lines, "\n"), nil
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
	return v2TerraformEventStream(t, events)
}

func v2TerraformEventStream(t *testing.T, events []map[string]any) []byte {
	t.Helper()
	var output bytes.Buffer
	encoder := json.NewEncoder(&output)
	for _, event := range events {
		if err := encoder.Encode(event); err != nil {
			t.Fatalf("encode synthetic terraform event: %v", err)
		}
	}
	return output.Bytes()
}

func TestV2TerraformRunEvidenceRejectsMissingFailedAndSkippedRuns(t *testing.T) {
	tests := []struct {
		name      string
		events    []map[string]any
		expected  []string
		wantError string
	}{
		{
			name: "exact run passes",
			events: []map[string]any{
				{"type": "version", "terraform": "test-version"},
				{"type": "test_run", "test_run": map[string]any{"run": "defaults_plan", "progress": "complete", "status": "pass"}},
				{"type": "test_summary", "test_summary": map[string]any{"status": "pass", "passed": 1, "failed": 0, "errored": 0, "skipped": 0}},
			},
			expected: []string{"defaults_plan"},
		},
		{
			name: "missing run",
			events: []map[string]any{
				{"type": "version", "terraform": "test-version"},
				{"type": "test_summary", "test_summary": map[string]any{"status": "pass", "passed": 0, "failed": 0, "errored": 0, "skipped": 0}},
			},
			expected:  []string{"defaults_plan"},
			wantError: "want exactly",
		},
		{
			name: "failed run",
			events: []map[string]any{
				{"type": "version", "terraform": "test-version"},
				{"type": "test_run", "test_run": map[string]any{"run": "defaults_plan", "progress": "complete", "status": "fail"}},
				{"type": "test_summary", "test_summary": map[string]any{"status": "fail", "passed": 0, "failed": 1, "errored": 0, "skipped": 0}},
			},
			expected:  []string{"defaults_plan"},
			wantError: `status = "fail"`,
		},
		{
			name: "skipped run",
			events: []map[string]any{
				{"type": "version", "terraform": "test-version"},
				{"type": "test_run", "test_run": map[string]any{"run": "defaults_plan", "progress": "complete", "status": "skip"}},
				{"type": "test_summary", "test_summary": map[string]any{"status": "pass", "passed": 0, "failed": 0, "errored": 0, "skipped": 1}},
			},
			expected:  []string{"defaults_plan"},
			wantError: `status = "skip"`,
		},
		{
			name:      "empty expected set",
			events:    []map[string]any{{"type": "version", "terraform": "test-version"}},
			expected:  nil,
			wantError: "expected run set is empty",
		},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := v2TerraformRunEvidence(v2TerraformEventStream(t, testCase.events), testCase.expected)
			if testCase.wantError == "" {
				if err != nil {
					t.Fatalf("v2TerraformRunEvidence() error = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), testCase.wantError) {
				t.Fatalf("v2TerraformRunEvidence() error = %v, want error containing %q", err, testCase.wantError)
			}
		})
	}
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

func v2WriteCheckpointDeployment(t *testing.T, directory, overlay, moduleDirectory, tfvarsFormat string) string {
	t.Helper()
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatalf("os.MkdirAll(%q) error = %v, want nil", directory, err)
	}
	payload := map[string]any{
		"module_dir": moduleDirectory,
		"overlay":    overlay,
	}
	if tfvarsFormat != "" {
		payload["tfvars_format"] = tfvarsFormat
	}
	content, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		t.Fatalf("json.Marshal(%#v) error = %v, want nil", payload, err)
	}
	path := filepath.Join(directory, "deployment.json")
	if err := os.WriteFile(path, append(content, '\n'), 0o600); err != nil {
		t.Fatalf("os.WriteFile(%q) error = %v, want nil", path, err)
	}
	return path
}

func v2ResourceTypesFromConfig(t *testing.T, directory, suffix string) []string {
	t.Helper()
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatalf("os.ReadDir(%q) error = %v, want nil", directory, err)
	}
	var resourceTypes []string
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), suffix) {
			continue
		}
		resourceType := strings.TrimSuffix(entry.Name(), suffix)
		if resourceType == "" {
			t.Fatalf("config file %q has an empty resource type before suffix %q", entry.Name(), suffix)
		}
		resourceTypes = append(resourceTypes, resourceType)
	}
	sort.Strings(resourceTypes)
	return resourceTypes
}

func v2ConfigItemKeys(content []byte) ([]string, error) {
	var config struct {
		Items map[string]json.RawMessage `json:"items"`
	}
	if err := json.Unmarshal(content, &config); err != nil {
		return nil, fmt.Errorf("decode tfvars document: %w", err)
	}
	if config.Items == nil {
		return nil, errors.New(`tfvars "items" must be a JSON object`)
	}
	keys := make([]string, 0, len(config.Items))
	for key := range config.Items {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys, nil
}

func v2ReadConfigItemKeys(t *testing.T, path string) []string {
	t.Helper()
	keys, err := v2ConfigItemKeys(v2ReadFile(t, path))
	if err != nil {
		t.Fatalf("v2ConfigItemKeys(%q) error = %v, want nil", path, err)
	}
	return keys
}

func v2VerifyDemoConfigItemParity(t *testing.T, gotDirectory, wantDirectory string, resourceTypes []string) {
	t.Helper()
	matched := 0
	for _, resourceType := range resourceTypes {
		resourceType := resourceType
		if t.Run("demo_config_items_"+resourceType, func(t *testing.T) {
			filename := resourceType + ".auto.tfvars.json"
			got := v2ReadConfigItemKeys(t, filepath.Join(gotDirectory, filename))
			want := v2ReadConfigItemKeys(t, filepath.Join(wantDirectory, filename))
			if !slices.Equal(got, want) {
				t.Errorf(
					"candidate transformed demo item keys for %q = %v (count %d), want committed keys %v (count %d)",
					resourceType,
					got,
					len(got),
					want,
					len(want),
				)
			}
		}) {
			matched++
		}
	}
	if matched != len(resourceTypes) {
		t.Errorf("candidate transformed demo item-key parity: %d/%d matched, want all resources", matched, len(resourceTypes))
		return
	}
	t.Logf("candidate transformed demo item-key parity: %d/%d matched", matched, len(resourceTypes))
}

func TestV2ConfigItemKeysAcceptsEmptyObjectsAndRejectsMissingItems(t *testing.T) {
	tests := []struct {
		name      string
		content   string
		want      []string
		wantError string
	}{
		{
			name:    "sorted keys",
			content: `{"items":{"second":{},"first":{}}}`,
			want:    []string{"first", "second"},
		},
		{
			name:    "empty items object",
			content: `{"items":{}}`,
			want:    []string{},
		},
		{
			name:      "missing items",
			content:   `{}`,
			wantError: `tfvars "items" must be a JSON object`,
		},
		{
			name:      "null items",
			content:   `{"items":null}`,
			wantError: `tfvars "items" must be a JSON object`,
		},
		{
			name:      "non-object items",
			content:   `{"items":[]}`,
			wantError: "cannot unmarshal array",
		},
		{
			name:      "invalid JSON",
			content:   `{"items":`,
			wantError: "decode tfvars document",
		},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			got, err := v2ConfigItemKeys([]byte(testCase.content))
			if testCase.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), testCase.wantError) {
					t.Fatalf("v2ConfigItemKeys(%q) error = %v, want error containing %q", testCase.content, err, testCase.wantError)
				}
				return
			}
			if err != nil {
				t.Fatalf("v2ConfigItemKeys(%q) error = %v, want nil", testCase.content, err)
			}
			if !slices.Equal(got, testCase.want) {
				t.Errorf("v2ConfigItemKeys(%q) = %v, want %v", testCase.content, got, testCase.want)
			}
		})
	}
}

func v2DirectoryNames(t *testing.T, directory string) []string {
	t.Helper()
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatalf("os.ReadDir(%q) error = %v, want nil", directory, err)
	}
	var names []string
	for _, entry := range entries {
		if entry.IsDir() {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	return names
}

func v2RequireStrings(t *testing.T, label string, got, want []string) {
	t.Helper()
	got = append([]string(nil), got...)
	want = append([]string(nil), want...)
	sort.Strings(got)
	sort.Strings(want)
	if gotText, wantText := strings.Join(got, "\n"), strings.Join(want, "\n"); gotText != wantText {
		t.Fatalf("%s differs\n got:\n%s\nwant:\n%s", label, gotText, wantText)
	}
}

func v2WithResourceSelectors(arguments, resourceTypes []string) []string {
	result := append([]string(nil), arguments...)
	for _, resourceType := range resourceTypes {
		result = append(result, "--resource", resourceType)
	}
	return result
}

func v2InitializeTerraformRoot(t *testing.T, label, terraform, directory string, environment []string) {
	t.Helper()
	if _, err := v2RunBoundedCommand(
		t,
		directory,
		terraform,
		[]string{"init", "-backend=false", "-input=false", "-no-color"},
		environment,
	); err != nil {
		t.Fatalf("%s terraform init in %q failed: %v", label, directory, err)
	}
}

func v2RunTerraformTestRuns(
	t *testing.T,
	label, terraform, directory string,
	testArguments, environment, expectedRuns []string,
) {
	t.Helper()
	v2InitializeTerraformRoot(t, label, terraform, directory, environment)
	result, err := v2RunBoundedCommand(t, directory, terraform, testArguments, environment)
	if err != nil {
		t.Fatalf("%s terraform test in %q failed: %v", label, directory, err)
	}
	if _, err := v2TerraformRunEvidence(result.stdout, expectedRuns); err != nil {
		t.Fatalf("%s terraform test evidence invalid: %v", label, err)
	}
}

func v2VerifyGeneratedModuleSemantics(
	t *testing.T,
	workspace, goBinary, terraform, deploymentPath, moduleDirectory string,
	metadataArguments, environment, expectedResourceTypes []string,
) {
	t.Helper()
	generateArguments := append([]string{
		"modules", "generate", "--out", moduleDirectory, "--deployment", deploymentPath,
	}, metadataArguments...)
	v2RunSuccessfully(t, workspace, goBinary, generateArguments, environment)
	validateArguments := append([]string{
		"modules", "validate", "--out", moduleDirectory, "--deployment", deploymentPath,
	}, metadataArguments...)
	v2RunSuccessfully(t, workspace, goBinary, validateArguments, environment)

	resourceTypes := v2DirectoryNames(t, moduleDirectory)
	v2RequireStrings(t, "generated module directories versus selected profile", resourceTypes, expectedResourceTypes)
	passed := 0
	for index, resourceType := range resourceTypes {
		resourceType := resourceType
		if t.Run("generated_module_"+resourceType, func(t *testing.T) {
			moduleRoot := filepath.Join(moduleDirectory, resourceType)
			for _, required := range []string{
				filepath.Join("tests", "defaults.tftest.hcl"),
				filepath.Join("tests", "sample.auto.tfvars.json"),
			} {
				if _, err := os.Stat(filepath.Join(moduleRoot, required)); err != nil {
					t.Fatalf("os.Stat(%q) error = %v, want generated module test artifact", required, err)
				}
			}
			v2RunTerraformTestRuns(
				t,
				"generated module "+resourceType,
				terraform,
				moduleRoot,
				[]string{"test", "-no-color", "-json"},
				environment,
				[]string{"defaults_plan"},
			)
		}) {
			passed++
		}
		if (index+1)%25 == 0 {
			t.Logf("generated module Terraform semantics: %d/%d complete", index+1, len(resourceTypes))
		}
	}
	if passed != len(resourceTypes) {
		t.Fatalf("generated module Terraform semantics: %d/%d passed", passed, len(resourceTypes))
	}
	t.Logf("generated module Terraform semantics: %d/%d passed", passed, len(resourceTypes))
}

func v2VerifyZPAPortalCapabilityCardinality(
	t *testing.T,
	repositoryRoot, terraform string,
	environment []string,
) {
	t.Helper()
	profile := filepath.Join(repositoryRoot, "packs", "zpa.packset.json")
	packRoot, err := metadata.LoadPackRoot(metadata.LoadPackRootOptions{
		PacksRoot:   v2FocusedZPAPackRoot(t, repositoryRoot),
		ProfilePath: &profile,
	})
	if err != nil {
		t.Fatalf("metadata.LoadPackRoot(ZPA portal contract) error = %v, want nil", err)
	}
	moduleDirectory := filepath.Join(t.TempDir(), "modules")
	if _, err := modulesgen.GenerateModule(
		packRoot,
		"zpa_policy_portal_access_rule",
		modulesgen.GenerateModuleOptions{
			OutputRoot: moduleDirectory,
			FormatHCL:  modulesgen.NewHCLFormatter(),
		},
	); err != nil {
		t.Fatalf("modulesgen.GenerateModule(zpa_policy_portal_access_rule) error = %v, want nil", err)
	}

	root := t.TempDir()
	moduleSource := filepath.Join(moduleDirectory, "zpa_policy_portal_access_rule")
	providerRequirements, err := os.ReadFile(filepath.Join(moduleSource, "versions.tf"))
	if err != nil {
		t.Fatalf("os.ReadFile(portal module versions.tf) error = %v, want nil", err)
	}
	if err := os.WriteFile(filepath.Join(root, "versions.tf"), providerRequirements, 0o600); err != nil {
		t.Fatalf("os.WriteFile(portal cardinality versions.tf) error = %v, want nil", err)
	}
	testDirectory := filepath.Join(root, "tests")
	if err := os.MkdirAll(testDirectory, 0o700); err != nil {
		t.Fatalf("os.MkdirAll(portal cardinality tests) error = %v, want nil", err)
	}
	testSource := `mock_provider "zpa" {}

run "one_capability_plan" {
  command = plan
}
`
	if err := os.WriteFile(filepath.Join(testDirectory, "cardinality.tftest.hcl"), []byte(testSource), 0o600); err != nil {
		t.Fatalf("os.WriteFile(portal cardinality test) error = %v, want nil", err)
	}
	writeConfiguration := func(capabilities string) {
		t.Helper()
		configuration := fmt.Sprintf(`module "portal" {
  source = %q
  items = {
    example = {
      name                           = "example"
      privileged_portal_capabilities = %s
    }
  }
}
`, moduleSource, capabilities)
		if err := os.WriteFile(filepath.Join(root, "main.tf"), []byte(configuration), 0o600); err != nil {
			t.Fatalf("os.WriteFile(portal cardinality main.tf) error = %v, want nil", err)
		}
	}

	writeConfiguration(`[
        { delete_file = true },
      ]`)
	v2InitializeTerraformRoot(t, "ZPA portal singleton capability", terraform, root, environment)
	result := v2RunSuccessfully(t, root, terraform, []string{
		"test", "-test-directory=tests", "-no-color", "-verbose", "-json",
	}, environment)
	report, err := v2ParseTerraformTestReport(result.stdout)
	if err != nil {
		t.Fatalf("parse ZPA portal capability plan: %v", err)
	}
	if err := v2RequirePassedTerraformRuns(report, []string{"one_capability_plan"}); err != nil {
		t.Fatalf("verify ZPA portal capability plan: %v", err)
	}
	changes := report.plans["one_capability_plan"]
	if len(changes) != 1 {
		t.Fatalf("ZPA portal capability plan resource changes = %+v, want exactly one", changes)
	}
	change := changes[0]
	wantAddress := `module.portal.zpa_policy_portal_access_rule.this["example"]`
	if change.Address != wantAddress {
		t.Fatalf("ZPA portal capability plan address = %q, want %q", change.Address, wantAddress)
	}
	capabilities, ok := change.Change.After["privileged_portal_capabilities"].([]any)
	if !ok || len(capabilities) != 1 {
		t.Fatalf("ZPA portal capability plan value = %#v, want one block", change.Change.After["privileged_portal_capabilities"])
	}
	capability, ok := capabilities[0].(map[string]any)
	if !ok {
		t.Fatalf("ZPA portal capability plan block = %#v, want object", capabilities[0])
	}
	if deleteFile, ok := capability["delete_file"].(bool); !ok || !deleteFile {
		t.Fatalf("ZPA portal capability plan delete_file = %#v, want true", capability["delete_file"])
	}

	requireRejected := func(label, value string) {
		t.Helper()
		writeConfiguration(value)
		if _, err := v2RunBoundedCommand(t, root, terraform, []string{"validate", "-no-color"}, environment); err == nil {
			t.Fatalf("ZPA portal module accepted %s; want strict tuple rejection before provider execution", label)
		}
	}
	requireRejected("two capability elements", `[
        { delete_file = true },
        { request_approvals = true },
      ]`)
	requireRejected("keyed two-object bypass", `{
        first  = { delete_file = true }
        second = { request_approvals = true }
      }`)
	t.Log("ZPA portal capability cardinality: one tuple element preserved in plan; two elements and keyed-object bypass rejected before provider execution")
}

func v2VerifyDemoEnvironmentSemantics(
	t *testing.T,
	repositoryRoot, workspace, goBinary, terraform, deploymentPath, overlay string,
	metadataArguments, environment []string,
) {
	t.Helper()
	demoInput := filepath.Join(repositoryRoot, "packs", "_shared", "zscaler", "demo")
	transformArguments := append([]string{
		"transform", "--in", demoInput, "--tenant", v2Tenant, "--deployment", deploymentPath,
	}, metadataArguments...)
	v2RunSuccessfully(t, workspace, goBinary, transformArguments, environment)

	wantResourceTypes := v2ResourceTypesFromConfig(
		t,
		filepath.Join(repositoryRoot, "demo", "config", v2Tenant),
		".auto.tfvars.json",
	)
	if len(wantResourceTypes) == 0 {
		t.Fatal("committed demo config corpus has no JSON resource fixtures")
	}
	configDirectory := filepath.Join(overlay, "config", v2Tenant)
	gotResourceTypes := v2ResourceTypesFromConfig(t, configDirectory, ".auto.tfvars.json")
	v2RequireStrings(t, "candidate transformed demo resource types", gotResourceTypes, wantResourceTypes)
	v2VerifyDemoConfigItemParity(
		t,
		configDirectory,
		filepath.Join(repositoryRoot, "demo", "config", v2Tenant),
		wantResourceTypes,
	)

	genEnvArguments := append([]string{
		"gen-env", "--tenant", v2Tenant, "--deployment", deploymentPath,
	}, metadataArguments...)
	genEnvArguments = v2WithResourceSelectors(genEnvArguments, wantResourceTypes)
	v2RunSuccessfully(t, workspace, goBinary, genEnvArguments, environment)

	environmentDirectory := filepath.Join(overlay, "envs", v2Tenant)
	rootTypes := v2DirectoryNames(t, environmentDirectory)
	v2RequireStrings(t, "generated demo environment roots", rootTypes, wantResourceTypes)
	passed := 0
	for _, resourceType := range rootTypes {
		resourceType := resourceType
		if t.Run("demo_environment_"+resourceType, func(t *testing.T) {
			environmentRoot := filepath.Join(environmentDirectory, resourceType)
			expectedRuns := []string{"empty_plan", "config_plan"}
			if _, err := os.Stat(filepath.Join(environmentRoot, "expression_bindings.tf")); err == nil {
				expectedRuns = []string{"config_plan"}
			} else if !os.IsNotExist(err) {
				t.Fatalf("os.Stat(expression_bindings.tf) error = %v, want nil or not-exist", err)
			}
			v2RunTerraformTestRuns(
				t,
				"demo environment "+resourceType,
				terraform,
				environmentRoot,
				[]string{"test", "-no-color", "-json"},
				environment,
				expectedRuns,
			)
		}) {
			passed++
		}
	}
	if passed != len(rootTypes) {
		t.Fatalf("demo environment Terraform semantics: %d/%d passed", passed, len(rootTypes))
	}
	t.Logf("demo environment Terraform semantics: %d/%d passed", passed, len(wantResourceTypes))
}

func v2VerifyHCLTfvarsSemantics(
	t *testing.T,
	repositoryRoot, goBinary, terraform, moduleDirectory string,
	metadataArguments, environment []string,
) {
	t.Helper()
	workspace := t.TempDir()
	overlay := filepath.Join(workspace, "overlay")
	deploymentPath := v2WriteCheckpointDeployment(t, workspace, overlay, moduleDirectory, "hcl")
	demoInput := filepath.Join(repositoryRoot, "packs", "_shared", "zscaler", "demo")
	transformArguments := append([]string{
		"transform", "--in", demoInput, "--tenant", v2Tenant, "--deployment", deploymentPath,
		"--resource", v2ResourceType,
	}, metadataArguments...)
	v2RunSuccessfully(t, workspace, goBinary, transformArguments, environment)
	genEnvArguments := append([]string{
		"gen-env", "--tenant", v2Tenant, "--deployment", deploymentPath, "--resource", v2ResourceType,
	}, metadataArguments...)
	v2RunSuccessfully(t, workspace, goBinary, genEnvArguments, environment)

	configPath := filepath.Join(overlay, "config", v2Tenant, v2ResourceType+".auto.tfvars")
	if _, err := os.Stat(configPath); err != nil {
		t.Fatalf("os.Stat(%q) error = %v, want generated HCL tfvars", configPath, err)
	}
	if _, err := os.Stat(configPath + ".json"); !os.IsNotExist(err) {
		t.Fatalf("os.Stat(%q) error = %v, want not-exist for HCL deployment", configPath+".json", err)
	}
	environmentRoot := filepath.Join(overlay, "envs", v2Tenant, v2ResourceType)
	testDirectory := filepath.Join(environmentRoot, "hcl-tests")
	if err := os.MkdirAll(testDirectory, 0o700); err != nil {
		t.Fatalf("os.MkdirAll(%q) error = %v, want nil", testDirectory, err)
	}
	testPath := filepath.Join(testDirectory, "config.tftest.hcl")
	testSource := "# Check the generated native-HCL tfvars through the real provider schema.\n" +
		"mock_provider \"zia\" {}\n\n" +
		"run \"config_plan\" {\n  command = plan\n}\n"
	if err := os.WriteFile(testPath, []byte(testSource), 0o600); err != nil {
		t.Fatalf("os.WriteFile(%q) error = %v, want nil", testPath, err)
	}

	v2InitializeTerraformRoot(t, "HCL tfvars environment", terraform, environmentRoot, environment)
	result, err := v2RunBoundedCommand(t, environmentRoot, terraform, []string{
		"test",
		"-test-directory=hcl-tests",
		"-var-file=" + configPath,
		"-no-color",
		"-verbose",
		"-json",
	}, environment)
	if err != nil {
		t.Fatalf("HCL tfvars terraform test in %q failed: %v", environmentRoot, err)
	}
	evidence, err := v2TerraformRuleLabelEvidence(result.stdout, []string{"config_plan"})
	if err != nil {
		t.Fatalf("HCL tfvars terraform test evidence invalid: %v", err)
	}
	t.Log("HCL tfvars semantic evidence:\n" + evidence)
}

func TestV2VerticalSliceCheckpoint(t *testing.T) {
	if os.Getenv(v2CheckpointEnv) != "1" {
		t.Skipf("set %s=1 to run the Go runtime v2 vertical-slice checkpoint", v2CheckpointEnv)
	}

	root := repoRoot(t)
	terraform := v2RequiredTerraformExecutable(t)
	v2VerifyZPAPortalCapabilityCardinality(
		t,
		root,
		terraform,
		v2FocusedTerraformEnvironment(t, terraform),
	)
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

	semanticWorkspace := t.TempDir()
	semanticOverlay := filepath.Join(semanticWorkspace, "overlay")
	semanticModuleDirectory := filepath.Join(semanticOverlay, "modules")
	semanticDeploymentPath := v2WriteCheckpointDeployment(
		t,
		semanticWorkspace,
		semanticOverlay,
		semanticModuleDirectory,
		"json",
	)
	fullMetadataArguments := []string{
		"--root", filepath.Join(root, "packs"),
		"--profile", filepath.Join(root, "packs", "full.packset.json"),
	}
	fullProfilePath := filepath.Join(root, "packs", "full.packset.json")
	fullPackRoot, err := metadata.LoadPackRoot(metadata.LoadPackRootOptions{
		PacksRoot:   filepath.Join(root, "packs"),
		ProfilePath: &fullProfilePath,
	})
	if err != nil {
		t.Fatalf("load selected full profile for generated module semantics: %v", err)
	}
	fullResourceTypes := modulesgen.ActiveGeneratedResourceTypes(fullPackRoot)
	v2VerifyGeneratedModuleSemantics(
		t,
		semanticWorkspace,
		goBinary,
		terraform,
		semanticDeploymentPath,
		semanticModuleDirectory,
		fullMetadataArguments,
		terraformEnvironment,
		fullResourceTypes,
	)
	v2VerifyDemoEnvironmentSemantics(
		t,
		root,
		semanticWorkspace,
		goBinary,
		terraform,
		semanticDeploymentPath,
		semanticOverlay,
		fullMetadataArguments,
		terraformEnvironment,
	)
	v2VerifyHCLTfvarsSemantics(
		t,
		root,
		goBinary,
		terraform,
		semanticModuleDirectory,
		fullMetadataArguments,
		terraformEnvironment,
	)

	postFetchRequests := takeRecordedFetchTranscript(t, server, "v2 checkpoint post-fetch")
	requireRecordedFetchTranscript(t, "v2 checkpoint post-fetch", postFetchRequests, nil)
}
