package stateprobe

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeTerraform writes an executable stand-in that logs each invocation's
// argv to argvLog, exits initCode for `init`, and emits pullStdout with
// pullCode for everything else (`state pull`). Driving the probe through a
// real child process keeps the terraformcmd runner in the path, so these
// tests exercise the same execution seam production uses rather than a
// stubbed function.
func fakeTerraform(t *testing.T, argvLog string, initCode int, pullStdout string, pullCode int) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "terraform")
	script := "#!/bin/sh\n" +
		"printf '%s\\n' \"$*\" >> " + shellQuote(argvLog) + "\n" +
		"if [ \"$1\" = init ]; then exit " + itoa(initCode) + "; fi\n" +
		"printf '%s' " + shellQuote(pullStdout) + "\nexit " + itoa(pullCode) + "\n"
	if err := os.WriteFile(path, []byte(script), 0o777); err != nil {
		t.Fatalf("write fake terraform: %v", err)
	}
	return path
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}

func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	digits := ""
	for value > 0 {
		digits = string(rune('0'+value%10)) + digits
		value /= 10
	}
	return digits
}

func probeFor(t *testing.T, initCode int, pullStdout string, pullCode int) (bool, error, string) {
	t.Helper()
	argvLog := filepath.Join(t.TempDir(), "argv.log")
	probe := New(Options{
		BackendConfig:       "/config/backend.azurerm.json",
		Environment:         map[string]string{},
		Tenant:              "demo",
		TerraformExecutable: fakeTerraform(t, argvLog, initCode, pullStdout, pullCode),
	})
	result, err := probe("referent_root", "example_type")
	raw, _ := os.ReadFile(argvLog)
	return result.Usable, err, string(raw)
}

const appliedState = `{"version":4,"terraform_version":"1.15.4","serial":1,"lineage":"probe-fixture",` +
	`"outputs":{"infrawright_reference_ids":{"value":{"example_type":{"item_one":"id-1"}},` +
	`"type":["object",{"example_type":["object",{"item_one":"string"}]}]}},"resources":[]}`

// TestProbeAnswersWithoutAnyWorkspace pins the defect this rewrite exists
// for: a fresh CI workspace has no generated roots and no .terraform
// anywhere, and the probe must still reach the backend and answer usable.
// The previous implementation answered absent for every referent in exactly
// this situation, so state-aware generation silently rewrote every
// cross-state reference to a literal on every pipeline run.
func TestProbeAnswersWithoutAnyWorkspace(t *testing.T) {
	usable, err, _ := probeFor(t, 0, appliedState, 0)
	if err != nil {
		t.Fatalf("probe error = %v, want nil", err)
	}
	if !usable {
		t.Errorf("usable = false, want true with no workspace present")
	}
}

// TestProbeInitArgvMatchesPlanConvention pins that the scratch init consumes
// the same BACKEND_CONFIG file and derives the same per-root key iw plan
// uses (plan/lifecycle.go, adopt/import_staging.go), so the probe and plan
// can never look at different state addresses.
func TestProbeInitArgvMatchesPlanConvention(t *testing.T) {
	_, err, argv := probeFor(t, 0, appliedState, 0)
	if err != nil {
		t.Fatalf("probe error = %v, want nil", err)
	}
	for _, want := range []string{
		"init -input=false -reconfigure -backend-config=/config/backend.azurerm.json -backend-config=key=demo/referent_root.tfstate",
		"state pull",
	} {
		if !strings.Contains(argv, want) {
			t.Errorf("argv log %q does not contain %q", argv, want)
		}
	}
}

// TestProbeReportsAbsentForEmptyStatePull pins the never-applied referent at
// the correct layer: the backend answers, and the answer is empty. A local
// backend that was never applied answers exit 0 with empty output; a remote
// backend holding no state yet answers with a synthesized empty state. Both
// are absent, and both fall back to the tfvars literal.
func TestProbeReportsAbsentForEmptyStatePull(t *testing.T) {
	for _, testCase := range []struct{ name, stdout string }{
		{name: "empty output", stdout: ""},
		{name: "whitespace only", stdout: "\n"},
		{name: "synthesized empty state", stdout: `{"version":4,"serial":0,"lineage":"x","outputs":{},"resources":[]}`},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			usable, err, _ := probeFor(t, 0, testCase.stdout, 0)
			if err != nil {
				t.Fatalf("probe error = %v, want nil", err)
			}
			if usable {
				t.Errorf("usable = true, want false")
			}
		})
	}
}

// TestProbeReportsUsableForAppliedState is the positive case, without which
// every other test here is satisfied by a probe that reports absent always.
func TestProbeReportsUsableForAppliedState(t *testing.T) {
	usable, err, _ := probeFor(t, 0, appliedState, 0)
	if err != nil {
		t.Fatalf("probe error = %v, want nil", err)
	}
	if !usable {
		t.Errorf("usable = false, want true for a root publishing reference identifiers")
	}
}

// TestProbeFailsClosedWhenInitFails pins that a backend the probe cannot
// even configure (missing container, refused credentials — both surface as
// init failures) is an error, never absence: folding it into absence would
// silently rewrite every reference in the run to a literal.
func TestProbeFailsClosedWhenInitFails(t *testing.T) {
	_, err, _ := probeFor(t, 1, appliedState, 0)
	if err == nil {
		t.Fatalf("probe error = nil, want a refusal when terraform init fails")
	}
	if !strings.Contains(err.Error(), "referent_root") {
		t.Errorf("error = %q, want it to name the root that could not be probed", err)
	}
}

// TestProbeFailsClosedWhenPullFails pins the same distinction one step
// later: a non-zero `state pull` means the backend was unreachable or
// refused, which is not the same as a root awaiting apply.
func TestProbeFailsClosedWhenPullFails(t *testing.T) {
	_, err, _ := probeFor(t, 0, "", 1)
	if err == nil {
		t.Fatalf("probe error = nil, want a refusal when terraform state pull fails")
	}
	if !strings.Contains(err.Error(), "referent_root") {
		t.Errorf("error = %q, want it to name the root that could not be probed", err)
	}
}

// TestProbeFailsClosedForMalformedState pins that the probe reuses envgen's
// parser rather than reimplementing state semantics: a document Terraform
// would not accept must fail closed here exactly as it does there.
func TestProbeFailsClosedForMalformedState(t *testing.T) {
	if _, err, _ := probeFor(t, 0, `{"outputs":{}}`, 0); err == nil {
		t.Errorf("probe error = nil, want a refusal for a document carrying no state version")
	}
}

// TestProbePullsOncePerRoot pins the per-root cache: two referent types on
// one root must not re-init or re-pull, both for speed and so one root
// cannot answer two identical probes differently within a run.
func TestProbePullsOncePerRoot(t *testing.T) {
	argvLog := filepath.Join(t.TempDir(), "argv.log")
	probe := New(Options{
		BackendConfig:       "/config/backend.azurerm.json",
		Environment:         map[string]string{},
		Tenant:              "demo",
		TerraformExecutable: fakeTerraform(t, argvLog, 0, appliedState, 0),
	})
	if _, err := probe("referent_root", "example_type"); err != nil {
		t.Fatalf("first probe: %v", err)
	}
	if _, err := probe("referent_root", "other_type"); err != nil {
		t.Fatalf("second probe: %v", err)
	}
	raw, err := os.ReadFile(argvLog)
	if err != nil {
		t.Fatalf("read argv log: %v", err)
	}
	if got := strings.Count(string(raw), "state pull"); got != 1 {
		t.Errorf("state pull ran %d times, want 1", got)
	}
}
