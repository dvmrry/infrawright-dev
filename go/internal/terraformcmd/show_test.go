//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package terraformcmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/dvmrry/infrawright-dev/go/internal/procerr"
)

type showFixture struct {
	root         string
	envDir       string
	snapshotPath string
}

func newShowFixture(t *testing.T) showFixture {
	t.Helper()
	requirePOSIX(t)
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("canonicalize temp directory: %v", err)
	}
	envDir := filepath.Join(root, "env")
	if err := os.Mkdir(envDir, 0o700); err != nil {
		t.Fatalf("mkdir env: %v", err)
	}
	snapshotPath := filepath.Join(root, "snapshot")
	if err := os.WriteFile(snapshotPath, []byte("opaque plan bytes\n"), 0o600); err != nil {
		t.Fatalf("write snapshot: %v", err)
	}
	return showFixture{root: root, envDir: envDir, snapshotPath: snapshotPath}
}

func (fixture showFixture) executable(t *testing.T, body string) string {
	t.Helper()
	file := filepath.Join(fixture.root, "terraform-"+strings.ReplaceAll(t.Name(), "/", "-"))
	if err := os.WriteFile(file, []byte("#!/bin/sh\n"+body+"\n"), 0o700); err != nil {
		t.Fatalf("write fake Terraform: %v", err)
	}
	if err := os.Chmod(file, 0o700); err != nil {
		t.Fatalf("chmod fake Terraform: %v", err)
	}
	return file
}

func (fixture showFixture) options(executable string) TerraformShowOptions {
	limits := TerraformShowLimits{
		TimeoutMs:      2_000,
		MaxStdoutBytes: 64 * 1024,
		MaxStderrBytes: 4 * 1024,
	}
	return TerraformShowOptions{
		TerraformExecutable: executable,
		EnvDir:              fixture.envDir,
		SnapshotPath:        fixture.snapshotPath,
		Limits:              &limits,
	}
}

// TestTerraformShowPlanFixedInvocation pins node-src/io/terraform-show.ts at
// 66e9c2d1668b89d772bf6218bbce82172c774a41:297-387.
func TestTerraformShowPlanFixedInvocation(t *testing.T) {
	fixture := newShowFixture(t)
	t.Setenv("TF_CLI_ARGS_show", "malicious-parent-value")
	body := strings.Join([]string{
		`if [ "${TF_CLI_ARGS_show+x}" = x ]; then exit 31; fi`,
		`if [ "$CHECKPOINT_DISABLE" != 1 ]; then exit 35; fi`,
		`if [ "$LANG" != C ] || [ "$LC_ALL" != C ]; then exit 36; fi`,
		fmt.Sprintf(`if [ "$PWD" != %s ]; then exit 37; fi`, shellQuote(fixture.envDir)),
		fmt.Sprintf(`if [ "$1" != %s ]; then exit 32; fi`, shellQuote("-chdir="+fixture.envDir)),
		`if [ "$2" != show ] || [ "$3" != -json ]; then exit 33; fi`,
		fmt.Sprintf(`if [ "$4" != %s ] || [ "$#" != 4 ]; then exit 34; fi`, shellQuote(fixture.snapshotPath)),
		`printf '%s' '{"format_version":"1.2","complete":false,"errored":false,"value":9007199254740993}'`,
	}, "\n")
	executable := fixture.executable(t, body)

	plan, err := TerraformShowPlan(fixture.options(executable))
	if err != nil {
		t.Fatalf("TerraformShowPlan: %v", err)
	}
	object, ok := plan.(map[string]any)
	if !ok {
		t.Fatalf("plan = %#v (%T), want object", plan, plan)
	}
	if object["complete"] != false {
		t.Errorf("complete = %#v, want false accepted at this source boundary", object["complete"])
	}
	if fmt.Sprint(object["value"]) != "9007199254740993" {
		t.Errorf("value = %#v, want lossless integer lexeme", object["value"])
	}
}

// TestTerraformShowPlanDoesNotApplyPlanContract explicitly pins the current
// source boundary: terraform-show.ts returns parsed JSON, while complete ===
// true and object-shape enforcement remain in the downstream plan contract.
func TestTerraformShowPlanDoesNotApplyPlanContract(t *testing.T) {
	fixture := newShowFixture(t)
	executable := fixture.executable(t, `printf '%s' '42'`)
	plan, err := TerraformShowPlan(fixture.options(executable))
	if err != nil {
		t.Fatalf("TerraformShowPlan scalar: %v", err)
	}
	if fmt.Sprint(plan) != "42" {
		t.Errorf("plan = %#v, want scalar 42", plan)
	}
}

func TestOperationalTerraformShowEnvironment(t *testing.T) {
	input := map[string]string{
		"HOME":                     "/home/test",
		"TEMP":                     "/temp",
		"TMP":                      "/tmp-short",
		"TMPDIR":                   "/tmp",
		"XDG_CONFIG_HOME":          "/xdg/config",
		"XDG_DATA_HOME":            "/xdg/data",
		"TERRAFORM_CONFIG":         "/terraform-config.rc",
		"TF_CLI_CONFIG_FILE":       "/terraform.rc",
		"TF_DATA_DIR":              "/terraform-data",
		"TF_PLUGIN_CACHE_DIR":      "/plugin-cache",
		"TF_CLI_ARGS_show":         "-destroy",
		"TF_LOG":                   "TRACE",
		"TF_TOKEN_private_example": "secret",
		"HTTPS_PROXY":              "https://proxy.invalid",
	}
	want := map[string]string{
		"HOME":                "/home/test",
		"TEMP":                "/temp",
		"TMP":                 "/tmp-short",
		"TMPDIR":              "/tmp",
		"XDG_CONFIG_HOME":     "/xdg/config",
		"XDG_DATA_HOME":       "/xdg/data",
		"TERRAFORM_CONFIG":    "/terraform-config.rc",
		"TF_CLI_CONFIG_FILE":  "/terraform.rc",
		"TF_DATA_DIR":         "/terraform-data",
		"TF_PLUGIN_CACHE_DIR": "/plugin-cache",
		"CHECKPOINT_DISABLE":  "1",
		"LANG":                "C",
		"LC_ALL":              "C",
	}
	got, err := OperationalTerraformShowEnvironment(input)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("environment = %#v, want %#v", got, want)
	}
	input["HOME"] = "/mutated"
	if got["HOME"] != "/home/test" {
		t.Errorf("environment was not detached: HOME = %q", got["HOME"])
	}
}

func TestTerraformShowPlanSuppliedEnvironmentIsComplete(t *testing.T) {
	fixture := newShowFixture(t)
	body := strings.Join([]string{
		`if [ "$SHOW_MARKER" != original ]; then exit 41; fi`,
		`if [ "${CHECKPOINT_DISABLE+x}" = x ]; then exit 42; fi`,
		`if [ "${LANG+x}" = x ] || [ "${LC_ALL+x}" = x ]; then exit 43; fi`,
		`if [ "${HOME+x}" = x ] || [ "${TF_LOG+x}" = x ]; then exit 44; fi`,
		`printf '%s' '{"ok":true}'`,
	}, "\n")
	executable := fixture.executable(t, body)
	options := fixture.options(executable)
	options.Environment = map[string]string{"SHOW_MARKER": "original"}
	plan, err := TerraformShowPlan(options)
	if err != nil {
		t.Fatalf("TerraformShowPlan: %v", err)
	}
	if plan.(map[string]any)["ok"] != true {
		t.Errorf("plan = %#v", plan)
	}
}

func TestMapTerraformCommandFailure(t *testing.T) {
	tests := []struct {
		inputCode   string
		input       error
		wantCode    string
		wantMessage string
		category    procerr.Category
	}{
		{inputCode: "UNSUPPORTED_TERRAFORM_EXECUTION_PLATFORM", wantCode: "UNSUPPORTED_TERRAFORM_EXECUTION_PLATFORM", wantMessage: UnsupportedTerraformExecutionPlatformMessage, category: procerr.CategoryDomain},
		{inputCode: "TERRAFORM_COMMAND_TIMEOUT", wantCode: "TERRAFORM_SHOW_TIMEOUT", wantMessage: "Terraform show exceeded its execution deadline", category: procerr.CategoryIO},
		{inputCode: "TERRAFORM_COMMAND_STDOUT_LIMIT", wantCode: "TERRAFORM_SHOW_STDOUT_LIMIT", wantMessage: "Terraform show exceeded its output limit", category: procerr.CategoryIO},
		{inputCode: "TERRAFORM_COMMAND_STDERR_LIMIT", wantCode: "TERRAFORM_SHOW_STDERR_LIMIT", wantMessage: "Terraform show exceeded its diagnostic-output limit", category: procerr.CategoryIO},
		{inputCode: "TERRAFORM_COMMAND_STDOUT_FAILED", wantCode: "TERRAFORM_SHOW_STDOUT_FAILED", wantMessage: "unable to read Terraform show output", category: procerr.CategoryIO},
		{inputCode: "TERRAFORM_COMMAND_STDERR_FAILED", wantCode: "TERRAFORM_SHOW_STDERR_FAILED", wantMessage: "unable to read Terraform show diagnostic output", category: procerr.CategoryIO},
		{inputCode: "UNTRUSTED_TERRAFORM_EXECUTABLE", wantCode: "UNTRUSTED_TERRAFORM_EXECUTABLE", wantMessage: "trusted Terraform input is not an allowed regular file", category: procerr.CategoryIO},
		{inputCode: "UNRESOLVED_TERRAFORM_COMMAND_PATH", wantCode: "UNRESOLVED_TERRAFORM_SHOW_PATH", wantMessage: "Terraform show requires resolved absolute paths", category: procerr.CategoryDomain},
		{inputCode: "INVALID_TERRAFORM_COMMAND_LIMIT", wantCode: "INVALID_TERRAFORM_SHOW_LIMIT", wantMessage: "Terraform show limits must be positive", category: procerr.CategoryDomain},
		{inputCode: "INVALID_TERRAFORM_COMMAND_ENVIRONMENT", wantCode: "INVALID_TERRAFORM_SHOW_ENVIRONMENT", wantMessage: "Terraform show environment is not allowed", category: procerr.CategoryDomain},
		{inputCode: "TERRAFORM_COMMAND_FAILED", wantCode: "TERRAFORM_SHOW_FAILED", wantMessage: "Terraform could not render the saved plan", category: procerr.CategoryDomain},
		{inputCode: "TERRAFORM_COMMAND_SPAWN_FAILED", wantCode: "TERRAFORM_SHOW_SPAWN_FAILED", wantMessage: "unable to start Terraform show", category: procerr.CategoryIO},
		{inputCode: "INVALID_TERRAFORM_COMMAND_ARGUMENTS", wantCode: "TERRAFORM_SHOW_SPAWN_FAILED", wantMessage: "unable to start Terraform show", category: procerr.CategoryIO},
		{input: errors.New("ordinary error"), wantCode: "TERRAFORM_SHOW_SPAWN_FAILED", wantMessage: "unable to start Terraform show", category: procerr.CategoryIO},
	}
	for _, test := range tests {
		name := test.inputCode
		if name == "" {
			name = "ordinary"
		}
		t.Run(name, func(t *testing.T) {
			input := test.input
			if input == nil {
				input = domainFailure(test.inputCode, "source-secret")
			}
			failure := requireProcessFailure(t, mapTerraformCommandFailure(input), test.wantCode)
			if failure.Message != test.wantMessage || failure.Category != test.category {
				t.Errorf("failure = %#v, want message %q/category %q", failure, test.wantMessage, test.category)
			}
		})
	}
}

func TestTerraformShowPlanOutputAndDecodeFailures(t *testing.T) {
	fixture := newShowFixture(t)
	tests := []struct {
		name    string
		body    string
		limits  *TerraformShowLimits
		code    string
		message string
	}{
		{
			name: "nonzero",
			body: `printf '%s' diagnostic-secret >&2; exit 19`,
			code: "TERRAFORM_SHOW_FAILED",
		},
		{
			name:   "stdout limit",
			body:   `i=0; while [ "$i" -lt 65 ]; do printf x; i=$((i + 1)); done`,
			limits: &TerraformShowLimits{TimeoutMs: 2_000, MaxStdoutBytes: 32, MaxStderrBytes: 4 * 1024},
			code:   "TERRAFORM_SHOW_STDOUT_LIMIT",
		},
		{
			name:   "stderr limit",
			body:   `i=0; while [ "$i" -lt 65 ]; do printf x >&2; i=$((i + 1)); done`,
			limits: &TerraformShowLimits{TimeoutMs: 2_000, MaxStdoutBytes: 64 * 1024, MaxStderrBytes: 32},
			code:   "TERRAFORM_SHOW_STDERR_LIMIT",
		},
		{
			name: "invalid utf8",
			body: `printf '\377'`,
			code: "INVALID_TERRAFORM_SHOW_UTF8",
		},
		{
			name: "truncated utf8",
			body: `printf '\342\202'`,
			code: "INVALID_TERRAFORM_SHOW_UTF8",
		},
		{
			name:    "python json decode message",
			body:    `printf '%s' 'not-json-secret'`,
			code:    "INVALID_TERRAFORM_SHOW_JSON",
			message: "Expecting value: line 1 column 1 (char 0)",
		},
		{
			name:    "duplicate key generic json message",
			body:    `printf '%s' '{"x":1,"x":2}'`,
			code:    "INVALID_TERRAFORM_SHOW_JSON",
			message: "Terraform show did not emit valid plan JSON",
		},
		{
			name:    "bom preserved into parser",
			body:    `printf '\357\273\277{}'`,
			code:    "INVALID_TERRAFORM_SHOW_JSON",
			message: "Terraform show did not emit valid plan JSON",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			executable := fixture.executable(t, test.body)
			options := fixture.options(executable)
			if test.limits != nil {
				options.Limits = test.limits
			}
			_, err := TerraformShowPlan(options)
			failure := requireProcessFailure(t, err, test.code)
			if test.message != "" && failure.Message != test.message {
				t.Errorf("message = %q, want %q", failure.Message, test.message)
			}
			encoded, jsonErr := json.Marshal(failure)
			if jsonErr != nil {
				t.Fatal(jsonErr)
			}
			if strings.Contains(string(encoded), "secret") || strings.Contains(string(encoded), fixture.root) {
				t.Errorf("failure leaked child data or path: %s", encoded)
			}
		})
	}
}

func TestTerraformShowPlanValidMultibyteAcrossWrites(t *testing.T) {
	fixture := newShowFixture(t)
	executable := fixture.executable(t, `printf '%s' '{"value":"'; printf '\303'; printf '\251'; printf '\360\237'; printf '\230\200"}'`)
	plan, err := TerraformShowPlan(fixture.options(executable))
	if err != nil {
		t.Fatalf("TerraformShowPlan: %v", err)
	}
	if got := plan.(map[string]any)["value"]; got != "é😀" {
		t.Errorf("value = %#v, want é😀", got)
	}
}

func TestTerraformShowPlanTimeout(t *testing.T) {
	fixture := newShowFixture(t)
	executable := fixture.executable(t, `while :; do :; done`)
	options := fixture.options(executable)
	options.Limits.TimeoutMs = 25
	started := time.Now()
	_, err := TerraformShowPlan(options)
	requireProcessFailure(t, err, "TERRAFORM_SHOW_TIMEOUT")
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Errorf("timeout took %v, want under 2s", elapsed)
	}
}

func TestTerraformShowPlanInputAndLimitFailures(t *testing.T) {
	fixture := newShowFixture(t)
	executable := fixture.executable(t, `printf '%s' '{}'`)

	invalidPath := fixture.options(executable)
	invalidPath.EnvDir = "relative"
	_, err := TerraformShowPlan(invalidPath)
	requireProcessFailure(t, err, "UNRESOLVED_TERRAFORM_SHOW_PATH")

	nulPath := fixture.options(executable)
	nulPath.SnapshotPath += "\x00secret"
	_, err = TerraformShowPlan(nulPath)
	requireProcessFailure(t, err, "UNRESOLVED_TERRAFORM_SHOW_PATH")

	malformedPath := fixture.options(executable)
	malformedPath.EnvDir += "\xff"
	_, err = TerraformShowPlan(malformedPath)
	requireProcessFailure(t, err, "UNRESOLVED_TERRAFORM_SHOW_PATH")

	link := filepath.Join(fixture.root, "snapshot-link")
	if err := os.Symlink(fixture.snapshotPath, link); err != nil {
		t.Fatal(err)
	}
	unsafeSnapshot := fixture.options(executable)
	unsafeSnapshot.SnapshotPath = link
	_, err = TerraformShowPlan(unsafeSnapshot)
	failure := requireProcessFailure(t, err, "INVALID_PLAN_SNAPSHOT")
	if failure.Message != "trusted Terraform input is not an allowed regular file" || failure.Category != procerr.CategoryIO {
		t.Errorf("failure = %#v", failure)
	}

	missingSnapshot := fixture.options(executable)
	missingSnapshot.SnapshotPath = filepath.Join(fixture.root, "missing")
	_, err = TerraformShowPlan(missingSnapshot)
	failure = requireProcessFailure(t, err, "INVALID_PLAN_SNAPSHOT")
	if failure.Message != "unable to inspect trusted Terraform input" || failure.Category != procerr.CategoryIO {
		t.Errorf("failure = %#v", failure)
	}

	executableLink := filepath.Join(fixture.root, "terraform-link")
	if err := os.Symlink(executable, executableLink); err != nil {
		t.Fatal(err)
	}
	unsafeExecutable := fixture.options(executableLink)
	_, err = TerraformShowPlan(unsafeExecutable)
	requireProcessFailure(t, err, "UNTRUSTED_TERRAFORM_EXECUTABLE")

	invalidEnvironment := fixture.options(executable)
	invalidEnvironment.Environment = map[string]string{"BAD=KEY": "value"}
	_, err = TerraformShowPlan(invalidEnvironment)
	requireProcessFailure(t, err, "INVALID_TERRAFORM_SHOW_ENVIRONMENT")

	invalidLimits := fixture.options(executable)
	invalidLimits.Limits = &TerraformShowLimits{TimeoutMs: 0, MaxStdoutBytes: 1, MaxStderrBytes: 1}
	_, err = TerraformShowPlan(invalidLimits)
	requireProcessFailure(t, err, "INVALID_TERRAFORM_SHOW_LIMIT")
}

func TestPreflightTerraformJSONComplexity(t *testing.T) {
	deadline := time.Now().Add(30 * time.Second).UnixMilli()
	tests := []struct {
		name    string
		text    string
		wantErr bool
		message string
	}{
		{
			name: "structural exact",
			text: strings.Repeat("[", maxTerraformJSONStructureTokens),
		},
		{
			name:    "structural overflow",
			text:    strings.Repeat("[", maxTerraformJSONStructureTokens+1),
			wantErr: true,
			message: "Terraform show JSON exceeds its structural limit",
		},
		{
			name: "scalar exact",
			text: strings.Repeat("0", maxTerraformJSONScalarCharacters),
		},
		{
			name:    "scalar overflow",
			text:    strings.Repeat("0", maxTerraformJSONScalarCharacters+1),
			wantErr: true,
			message: "Terraform show JSON exceeds its scalar-token limit",
		},
		{
			name: "string exact including closing quote",
			text: `"` + strings.Repeat("x", maxTerraformJSONStringCharacters-1) + `"`,
		},
		{
			name:    "cumulative string overflow",
			text:    `"` + strings.Repeat("x", maxTerraformJSONStringCharacters/2) + `","` + strings.Repeat("y", maxTerraformJSONStringCharacters/2) + `"`,
			wantErr: true,
			message: "Terraform show JSON exceeds its string-content limit",
		},
		{
			name:    "astral characters count two utf16 units",
			text:    `"` + strings.Repeat("😀", maxTerraformJSONStringCharacters/2) + `"`,
			wantErr: true,
			message: "Terraform show JSON exceeds its string-content limit",
		},
		{
			name: "structural characters inside string do not count",
			text: `"{[,:]}"`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := preflightTerraformJSON(test.text, deadline)
			if !test.wantErr {
				if err != nil {
					t.Fatalf("preflightTerraformJSON: %v", err)
				}
				return
			}
			failure := requireProcessFailure(t, err, "TERRAFORM_SHOW_COMPLEXITY_LIMIT")
			if failure.Message != test.message {
				t.Errorf("message = %q, want %q", failure.Message, test.message)
			}
		})
	}

	err := preflightTerraformJSON("{}", time.Now().Add(-time.Second).UnixMilli())
	requireProcessFailure(t, err, "TERRAFORM_SHOW_TIMEOUT")
}
