package main

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/dvmrry/infrawright-dev/go/internal/assessment"
	"github.com/dvmrry/infrawright-dev/go/internal/plan"
	"github.com/dvmrry/infrawright-dev/go/internal/procerr"
)

const assessmentCommandResource = "sample_resource"

type assessmentCommandFixture struct {
	workspace  string
	packs      string
	profile    string
	deployment string
	envDir     string
	terraform  string
	varFile    string
}

func writeAssessmentCommandFile(t *testing.T, path, text string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("os.MkdirAll(%q) error = %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(text), mode); err != nil {
		t.Fatalf("os.WriteFile(%q) error = %v", path, err)
	}
}

func writeAssessmentCommandJSON(t *testing.T, path string, value any) {
	t.Helper()
	encoded, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatalf("json.MarshalIndent(%q) error = %v", path, err)
	}
	writeAssessmentCommandFile(t, path, string(encoded)+"\n", 0o600)
}

func newAssessmentCommandFixture(t *testing.T) assessmentCommandFixture {
	t.Helper()
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("saved-plan assessment is deliberately fail-closed on this platform")
	}
	workspace := t.TempDir()
	fixture := assessmentCommandFixture{
		workspace:  workspace,
		packs:      filepath.Join(workspace, "packs"),
		profile:    filepath.Join(workspace, "packsets", "full.json"),
		deployment: filepath.Join(workspace, "deployment.json"),
		envDir:     filepath.Join(workspace, "envs", "tenant", assessmentCommandResource),
		terraform:  filepath.Join(workspace, "terraform-fake"),
		varFile: filepath.Join(
			workspace,
			"config",
			"tenant",
			assessmentCommandResource+".auto.tfvars.json",
		),
	}
	writeAssessmentCommandJSON(t, filepath.Join(fixture.packs, "sample", "pack.json"), map[string]any{
		"pin":               "1.0.0",
		"provider_prefixes": map[string]any{"sample_": "sample"},
		"provider_sources":  map[string]any{"sample": "example/sample"},
		"vendor":            "sample",
		"provider_config": map[string]any{
			"requirements": []any{map[string]any{
				"id":         "sample_attribution",
				"setting":    "add_attribution",
				"value":      false,
				"reason":     "provider adds attribution",
				"plan_paths": []any{"terraform_labels.attribution"},
				"remediation": map[string]any{
					"kind":     "provider_argument",
					"mode":     "required_external",
					"evidence": "sample.md",
				},
			}},
		},
	})
	writeAssessmentCommandJSON(t, filepath.Join(fixture.packs, "sample", "registry.json"), map[string]any{
		assessmentCommandResource: map[string]any{"generate": true, "product": "sample"},
	})
	writeAssessmentCommandJSON(t, fixture.profile, map[string]any{
		"kind": "infrawright.pack-set", "version": 1,
		"packs": []any{"sample"}, "shared": []any{},
	})
	writeAssessmentCommandJSON(t, fixture.deployment, map[string]any{
		"overlay": workspace,
		"roots":   map[string]any{},
	})
	moduleDir := filepath.Join(workspace, "modules", assessmentCommandResource)
	writeAssessmentCommandFile(t, filepath.Join(moduleDir, "main.tf"), "# module\n", 0o600)
	relativeModule, err := filepath.Rel(fixture.envDir, moduleDir)
	if err != nil {
		t.Fatalf("filepath.Rel() error = %v", err)
	}
	writeAssessmentCommandFile(t, filepath.Join(fixture.envDir, "main.tf"), strings.Join([]string{
		`module "` + assessmentCommandResource + `" {`,
		`  source = "` + filepath.ToSlash(relativeModule) + `"`,
		"  items = var." + assessmentCommandResource + "_items",
		"}",
		"",
	}, "\n"), 0o600)
	writeAssessmentCommandFile(t, fixture.varFile, "{}\n", 0o600)
	writeAssessmentCommandFile(t, filepath.Join(fixture.envDir, "tfplan"), "opaque plan bytes\n", 0o600)
	fingerprint, err := plan.FingerprintPlanV2(plan.PlanFingerprintInput{
		EnvDir: fixture.envDir, VarFiles: []string{fixture.varFile},
		MemberTypes: []string{assessmentCommandResource},
	}, nil)
	if err != nil {
		t.Fatalf("plan.FingerprintPlanV2() error = %v", err)
	}
	writeAssessmentCommandFile(
		t,
		filepath.Join(fixture.envDir, "tfplan.sources"),
		`{"version":2,"sha256":"`+fingerprint.SHA256+`"}`+"\n",
		0o600,
	)
	return fixture
}

func assessmentCommandShellLiteral(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
}

func (fixture assessmentCommandFixture) writeTerraform(t *testing.T, stdout string) {
	t.Helper()
	writeAssessmentCommandFile(
		t,
		fixture.terraform,
		"#!/bin/sh\nprintf '%s' "+assessmentCommandShellLiteral(stdout)+"\n",
		0o700,
	)
}

func assessmentCommandPlan(change map[string]any) string {
	encoded, err := json.Marshal(map[string]any{
		"format_version":    "1.2",
		"terraform_version": "1.15.4",
		"complete":          true,
		"errored":           false,
		"resource_changes": []any{map[string]any{
			"address": `sample_resource.this["one"]`,
			"type":    assessmentCommandResource,
			"change":  change,
		}},
		"output_changes": map[string]any{},
	})
	if err != nil {
		panic(err)
	}
	return string(encoded)
}

func captureAssessmentCommand(
	t *testing.T,
	operation func() (int, error),
) (int, error, string, string) {
	t.Helper()
	stdoutRead, stdoutWrite, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe(stdout) error = %v", err)
	}
	stderrRead, stderrWrite, err := os.Pipe()
	if err != nil {
		_ = stdoutRead.Close()
		_ = stdoutWrite.Close()
		t.Fatalf("os.Pipe(stderr) error = %v", err)
	}
	originalStdout, originalStderr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = stdoutWrite, stderrWrite
	defer func() {
		os.Stdout, os.Stderr = originalStdout, originalStderr
		_ = stdoutRead.Close()
		_ = stderrRead.Close()
	}()
	status, operationErr := operation()
	if err := stdoutWrite.Close(); err != nil {
		t.Fatalf("stdout close error = %v", err)
	}
	if err := stderrWrite.Close(); err != nil {
		t.Fatalf("stderr close error = %v", err)
	}
	stdout, err := io.ReadAll(stdoutRead)
	if err != nil {
		t.Fatalf("stdout read error = %v", err)
	}
	stderr, err := io.ReadAll(stderrRead)
	if err != nil {
		t.Fatalf("stderr read error = %v", err)
	}
	return status, operationErr, string(stdout), string(stderr)
}

func requireAssessmentCommandFailure(t *testing.T, err error, code string) *procerr.ProcessFailure {
	t.Helper()
	var failure *procerr.ProcessFailure
	if !errors.As(err, &failure) {
		t.Fatalf("error = %T(%v), want *procerr.ProcessFailure code %q", err, err, code)
	}
	if failure.Code != code {
		t.Fatalf("failure.Code = %q, want %q (failure = %+v)", failure.Code, code, failure)
	}
	return failure
}

func runAssessmentCommandFixture(
	t *testing.T,
	fixture assessmentCommandFixture,
	mode assessment.AssessmentMode,
	arguments []string,
) (int, error, string, string) {
	t.Helper()
	options, err := assessmentCLIOptionsFor(arguments, mode, fixture.workspace)
	if err != nil {
		return 0, err, "", ""
	}
	return captureAssessmentCommand(t, func() (int, error) {
		return runAssessmentCommand(options, mode, fixture.workspace)
	})
}

func configureAssessmentCommandFixture(t *testing.T, fixture assessmentCommandFixture) {
	t.Helper()
	t.Chdir(fixture.workspace)
	t.Setenv("TMPDIR", filepath.Join(fixture.workspace, "tmp"))
	if err := os.MkdirAll(os.Getenv("TMPDIR"), 0o700); err != nil {
		t.Fatalf("os.MkdirAll(TMPDIR) error = %v", err)
	}
	t.Setenv("INFRAWRIGHT_PACKS", "")
	t.Setenv("INFRAWRIGHT_PACK_PROFILE", "")
	t.Setenv("INFRAWRIGHT_DEPLOYMENT", "")
}

func TestAssessmentCLIOptionGrammarAndLastWins(t *testing.T) {
	root := t.TempDir()
	for _, test := range []struct {
		name      string
		mode      assessment.AssessmentMode
		arguments []string
		want      string
	}{
		{name: "duplicate tenant", mode: assessment.AssertClean, arguments: []string{"--tenant", "one", "--tenant", "two"}, want: "--tenant may be specified only once"},
		{name: "duplicate report", mode: assessment.AssertClean, arguments: []string{"--report", "one", "--report", "two"}, want: "--report may be specified only once"},
		{name: "clean rejects policy", mode: assessment.AssertClean, arguments: []string{"--policy", "policy.json"}, want: "assert-clean does not accept --policy"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := assessmentCLIOptionsFor(test.arguments, test.mode, root)
			var exit *cliExit
			if !errors.As(err, &exit) || exit.status != 2 || exit.message != test.want {
				t.Errorf("error = %T(%v), want usage error %q", err, err, test.want)
			}
		})
	}

	options, err := assessmentCLIOptionsFor([]string{
		"--tenant", "", "--resource", "first", "--resource", "second",
		"--policy", "first.json", "--policy", "second.json",
	}, assessment.AssertAdoptable, root)
	if err != nil {
		t.Fatalf("assessmentCLIOptionsFor(last wins) error = %v", err)
	}
	if options.tenant == nil || *options.tenant != "" {
		t.Errorf("tenant = %#v, want present empty string", options.tenant)
	}
	if got := strings.Join(options.resources, ","); got != "first,second" {
		t.Errorf("resources = %q, want first,second", got)
	}
	if options.policy == nil || *options.policy != "second.json" {
		t.Errorf("policy = %#v, want last occurrence", options.policy)
	}
}

func TestAssessmentTerraformResolverIsLazyAndPreservesPrecedence(t *testing.T) {
	directory := t.TempDir()
	first := filepath.Join(directory, "terraform-first")
	second := filepath.Join(directory, "terraform-second")
	for _, path := range []string{first, second} {
		writeAssessmentCommandFile(t, path, "#!/bin/sh\nexit 0\n", 0o700)
	}
	t.Setenv("PATH", directory)
	t.Setenv("TF", first)
	fromEnvironment := assessmentTerraformResolver(nil)
	t.Setenv("TF", second)
	resolved, err := fromEnvironment()
	if err != nil {
		t.Fatalf("lazy TF resolver error = %v", err)
	}
	wantSecond, _ := filepath.EvalSymlinks(second)
	if resolved != wantSecond {
		t.Errorf("lazy TF resolver = %q, want post-construction TF %q", resolved, wantSecond)
	}

	explicit := first
	fromFlag := assessmentTerraformResolver(&explicit)
	t.Setenv("TF", second)
	resolved, err = fromFlag()
	if err != nil {
		t.Fatalf("explicit TF resolver error = %v", err)
	}
	wantFirst, _ := filepath.EvalSymlinks(first)
	if resolved != wantFirst {
		t.Errorf("explicit TF resolver = %q, want flag %q", resolved, wantFirst)
	}
}

func TestAssessmentCommandsCleanBlockedAndTolerated(t *testing.T) {
	for _, test := range []struct {
		name       string
		mode       assessment.AssessmentMode
		change     map[string]any
		policy     any
		wantCode   string
		wantStderr []string
	}{
		{
			name: "clean", mode: assessment.AssertClean,
			change:     map[string]any{"actions": []any{"no-op"}, "before": map[string]any{}, "after": map[string]any{}},
			wantStderr: []string{"all 1 saved plan(s) clean (no-op/imports only)\n"},
		},
		{
			name: "blocked guidance", mode: assessment.AssertAdoptable,
			change: map[string]any{
				"actions": []any{"update"},
				"before":  map[string]any{"terraform_labels": map[string]any{}},
				"after":   map[string]any{"terraform_labels": map[string]any{"attribution": "true"}},
			},
			wantCode: "PLAN_NOT_ADOPTABLE",
			wantStderr: []string{
				"BLOCKED: tenant/sample_resource\n",
				"  Provider configuration guidance:\n",
				"      setting: add_attribution\n",
			},
		},
		{
			name: "tolerated", mode: assessment.AssertAdoptable,
			change: map[string]any{
				"actions": []any{"update"},
				"before":  map[string]any{"status": "old"},
				"after":   map[string]any{"status": "new"},
			},
			policy: map[string]any{
				"version": 1,
				"resource_types": map[string]any{assessmentCommandResource: map[string]any{
					"plan_tolerate": []any{map[string]any{
						"path": "status", "reason": "consumer accepts status", "approved_by": "unit",
					}},
				}},
			},
			wantStderr: []string{
				"TOLERATED: tenant/sample_resource\n",
				"1 saved plan(s) adoptable with consumer-tolerated drift\n",
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newAssessmentCommandFixture(t)
			configureAssessmentCommandFixture(t, fixture)
			fixture.writeTerraform(t, assessmentCommandPlan(test.change))
			report := filepath.Join(fixture.workspace, test.name+".report.json")
			arguments := []string{
				"--tenant", "tenant", "--report", report, "--terraform", fixture.terraform,
			}
			if test.policy != nil {
				policyPath := filepath.Join(fixture.workspace, test.name+".policy.json")
				writeAssessmentCommandJSON(t, policyPath, test.policy)
				arguments = append(arguments, "--policy", policyPath)
			}
			status, err, stdout, stderr := runAssessmentCommandFixture(t, fixture, test.mode, arguments)
			if status != 0 || stdout != "" {
				t.Errorf("command = {status:%d stdout:%q}, want {0 empty}", status, stdout)
			}
			if test.wantCode == "" {
				if err != nil {
					t.Fatalf("command error = %v, want nil", err)
				}
			} else {
				requireAssessmentCommandFailure(t, err, test.wantCode)
			}
			for _, fragment := range test.wantStderr {
				if !strings.Contains(stderr, fragment) {
					t.Errorf("stderr = %q, want fragment %q", stderr, fragment)
				}
			}
			reportBytes, readErr := os.ReadFile(report)
			if readErr != nil {
				t.Fatalf("os.ReadFile(report) error = %v", readErr)
			}
			var decoded map[string]any
			if err := json.Unmarshal(reportBytes, &decoded); err != nil {
				t.Fatalf("report is invalid JSON: %v\n%s", err, reportBytes)
			}
			if decoded["mode"] != string(test.mode) {
				t.Errorf("report mode = %#v, want %q", decoded["mode"], test.mode)
			}
		})
	}
}

func TestAssessmentCommandFailuresAndLazyOrdering(t *testing.T) {
	t.Run("deployment control file is bound", func(t *testing.T) {
		fixture := newAssessmentCommandFixture(t)
		configureAssessmentCommandFixture(t, fixture)
		planJSON := assessmentCommandPlan(map[string]any{
			"actions": []any{"no-op"}, "before": map[string]any{}, "after": map[string]any{},
		})
		writeAssessmentCommandFile(t, fixture.terraform, strings.Join([]string{
			"#!/bin/sh",
			"printf '%s' '{}\\n' > " + assessmentCommandShellLiteral(fixture.deployment),
			"printf '%s' " + assessmentCommandShellLiteral(planJSON),
			"",
		}, "\n"), 0o700)
		_, err, _, _ := runAssessmentCommandFixture(t, fixture, assessment.AssertClean, []string{
			"--tenant", "tenant", "--terraform", fixture.terraform,
		})
		requireAssessmentCommandFailure(t, err, "ASSESSMENT_CONTROL_CHANGED")
	})

	t.Run("no plans skips missing Terraform", func(t *testing.T) {
		fixture := newAssessmentCommandFixture(t)
		configureAssessmentCommandFixture(t, fixture)
		if err := os.Remove(filepath.Join(fixture.envDir, "tfplan")); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(filepath.Join(fixture.envDir, "tfplan.sources")); err != nil {
			t.Fatal(err)
		}
		report := filepath.Join(fixture.workspace, "no-plans.json")
		_, err, _, stderr := runAssessmentCommandFixture(t, fixture, assessment.AssertClean, []string{
			"--tenant", "tenant", "--report", report, "--terraform", "/definitely/missing/terraform",
		})
		requireAssessmentCommandFailure(t, err, "NO_SAVED_PLANS")
		if strings.Contains(stderr, "terraform executable") {
			t.Errorf("stderr = %q, want zero-root Terraform suppression", stderr)
		}
	})

	t.Run("selector failure", func(t *testing.T) {
		fixture := newAssessmentCommandFixture(t)
		configureAssessmentCommandFixture(t, fixture)
		report := filepath.Join(fixture.workspace, "selector.json")
		_, err, _, _ := runAssessmentCommandFixture(t, fixture, assessment.AssertClean, []string{
			"--tenant", "tenant", "--resource", "missing_resource", "--report", report,
		})
		requireAssessmentCommandFailure(t, err, "UNKNOWN_RESOURCE_SELECTOR")
	})

	t.Run("invalid show JSON", func(t *testing.T) {
		fixture := newAssessmentCommandFixture(t)
		configureAssessmentCommandFixture(t, fixture)
		fixture.writeTerraform(t, "invalid-json")
		report := filepath.Join(fixture.workspace, "invalid-show.json")
		_, err, _, _ := runAssessmentCommandFixture(t, fixture, assessment.AssertClean, []string{
			"--tenant", "tenant", "--report", report, "--terraform", fixture.terraform,
		})
		requireAssessmentCommandFailure(t, err, "INVALID_TERRAFORM_SHOW_JSON")
	})

	t.Run("invalid policy before lazy inputs and Terraform", func(t *testing.T) {
		fixture := newAssessmentCommandFixture(t)
		configureAssessmentCommandFixture(t, fixture)
		policy := filepath.Join(fixture.workspace, "invalid-policy.json")
		writeAssessmentCommandFile(t, policy, `{"version":1,"resource_types":oops}`+"\n", 0o600)
		options, parseErr := assessmentCLIOptionsFor([]string{
			"--tenant", "tenant", "--policy", policy,
			"--root", filepath.Join(fixture.workspace, "missing-packs"),
			"--deployment", filepath.Join(fixture.workspace, "missing-deployment"),
			"--terraform", filepath.Join(fixture.workspace, "missing-terraform"),
		}, assessment.AssertAdoptable, fixture.workspace)
		if parseErr != nil {
			t.Fatalf("assessmentCLIOptionsFor() error = %v", parseErr)
		}
		_, err, _, _ := captureAssessmentCommand(t, func() (int, error) {
			return runAssessmentCommand(options, assessment.AssertAdoptable, fixture.workspace)
		})
		requireAssessmentCommandFailure(t, err, "INVALID_DRIFT_POLICY")
	})

	t.Run("missing Terraform after selected roots", func(t *testing.T) {
		fixture := newAssessmentCommandFixture(t)
		configureAssessmentCommandFixture(t, fixture)
		report := filepath.Join(fixture.workspace, "missing-terraform.json")
		_, err, _, _ := runAssessmentCommandFixture(t, fixture, assessment.AssertClean, []string{
			"--tenant", "tenant", "--report", report,
			"--terraform", filepath.Join(fixture.workspace, "missing-terraform"),
		})
		failure := requireAssessmentCommandFailure(t, err, "ASSESSMENT_FAILED")
		if failure.Category != procerr.CategoryInternal {
			t.Errorf("missing Terraform category = %q, want internal", failure.Category)
		}
	})
}

func TestAssessmentCommandReportStdoutAndFileOrdering(t *testing.T) {
	for _, destination := range []string{"stdout", "file"} {
		t.Run(destination, func(t *testing.T) {
			fixture := newAssessmentCommandFixture(t)
			configureAssessmentCommandFixture(t, fixture)
			fixture.writeTerraform(t, assessmentCommandPlan(map[string]any{
				"actions": []any{"no-op"}, "before": map[string]any{}, "after": map[string]any{},
			}))
			report := "-"
			if destination == "file" {
				report = filepath.Join(fixture.workspace, "report.json")
			}
			_, err, stdout, stderr := runAssessmentCommandFixture(t, fixture, assessment.AssertClean, []string{
				"--tenant", "tenant", "--report", report, "--terraform", fixture.terraform,
			})
			if err != nil {
				t.Fatalf("assessment command error = %v", err)
			}
			if stderr != "all 1 saved plan(s) clean (no-op/imports only)\n" {
				t.Errorf("stderr = %q, want exact success diagnostic", stderr)
			}
			var reportBytes []byte
			if destination == "stdout" {
				reportBytes = []byte(stdout)
			} else {
				if stdout != "" {
					t.Errorf("file report stdout = %q, want empty", stdout)
				}
				reportBytes, err = os.ReadFile(report)
				if err != nil {
					t.Fatalf("os.ReadFile(report) error = %v", err)
				}
			}
			if !strings.HasSuffix(string(reportBytes), "\n") {
				t.Errorf("report lacks exact trailing newline: %q", reportBytes)
			}
			var decoded map[string]any
			if err := json.Unmarshal(reportBytes, &decoded); err != nil {
				t.Fatalf("report JSON error = %v", err)
			}
			summary, _ := decoded["summary"].(map[string]any)
			if summary["status"] != "clean" || summary["checked"] != float64(1) {
				t.Errorf("report summary = %#v, want clean checked=1", summary)
			}
		})
	}
}
