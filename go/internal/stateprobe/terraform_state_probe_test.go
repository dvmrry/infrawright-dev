package stateprobe

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeTerraform writes an executable stand-in that emits stdout verbatim and
// exits with code. Driving the probe through a real child process keeps the
// terraformcmd runner in the path, so these tests exercise the same execution
// seam production uses rather than a stubbed function.
func fakeTerraform(t *testing.T, stdout string, code int) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "terraform")
	script := "#!/bin/sh\nprintf '%s' " + shellQuote(stdout) + "\nexit " + itoa(code) + "\n"
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

// initializedRoot creates a root directory that looks initialized, so the
// probe proceeds to Terraform instead of short-circuiting.
func initializedRoot(t *testing.T) string {
	t.Helper()
	directory := t.TempDir()
	if err := os.MkdirAll(filepath.Join(directory, ".terraform"), 0o777); err != nil {
		t.Fatalf("create .terraform: %v", err)
	}
	return directory
}

func probeFor(t *testing.T, directory, stdout string, code int) (bool, error) {
	t.Helper()
	probe := New(Options{
		Environment:          map[string]string{},
		TerraformExecutable:  fakeTerraform(t, stdout, code),
		ResolveRootDirectory: func(string) (string, error) { return directory, nil },
	})
	result, err := probe("referent_root", "example_type")
	return result.Usable, err
}

const appliedState = `{"version":4,"terraform_version":"1.15.4","serial":1,"lineage":"probe-fixture",` +
	`"outputs":{"infrawright_reference_ids":{"value":{"example_type":{"item_one":"id-1"}},` +
	`"type":["object",{"example_type":["object",{"item_one":"string"}]}]}},"resources":[]}`

// TestProbeReportsAbsentForRootThatWasNeverGenerated pins the case adoption
// hits first: root A references root B before B exists on disk at all.
// Terraform cannot start in a directory that is not there, so without this the
// ordinary incremental case fails closed instead of falling back.
func TestProbeReportsAbsentForRootThatWasNeverGenerated(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "never-generated")
	usable, err := probeFor(t, missing, appliedState, 0)
	if err != nil {
		t.Fatalf("probe error = %v, want nil", err)
	}
	if usable {
		t.Errorf("usable = true, want false for a root with no directory")
	}
}

// TestProbeReportsAbsentForUninitializedRoot pins the second adoption case: the
// referent has been generated but never applied, so its modules are not
// installed and Terraform refuses `state pull` outright. An uninitialized root
// cannot have state Terraform could read under any backend, so it is absent.
func TestProbeReportsAbsentForUninitializedRoot(t *testing.T) {
	generated := t.TempDir()
	if err := os.WriteFile(filepath.Join(generated, "main.tf"), []byte("terraform {}\n"), 0o666); err != nil {
		t.Fatalf("write main.tf: %v", err)
	}
	// The fake would report a fully applied root; the probe must never reach
	// it, because the absence of .terraform settles the question first.
	usable, err := probeFor(t, generated, appliedState, 0)
	if err != nil {
		t.Fatalf("probe error = %v, want nil", err)
	}
	if usable {
		t.Errorf("usable = true, want false for a generated but uninitialized root")
	}
}

// TestProbeReportsAbsentForEmptyStatePull pins the local-vs-remote asymmetry:
// an initialized root that has never been applied answers exit 0 with empty
// output, while a remote backend holding no state yet answers with a
// synthesized empty state. Both are absent.
func TestProbeReportsAbsentForEmptyStatePull(t *testing.T) {
	for _, testCase := range []struct{ name, stdout string }{
		{name: "empty output", stdout: ""},
		{name: "whitespace only", stdout: "\n"},
		{name: "synthesized empty state", stdout: `{"version":4,"serial":0,"lineage":"x","outputs":{},"resources":[]}`},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			usable, err := probeFor(t, initializedRoot(t), testCase.stdout, 0)
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
	usable, err := probeFor(t, initializedRoot(t), appliedState, 0)
	if err != nil {
		t.Fatalf("probe error = %v, want nil", err)
	}
	if !usable {
		t.Errorf("usable = false, want true for a root publishing reference identifiers")
	}
}

// TestProbeFailsClosedWhenTerraformExits pins the distinction the whole probe
// exists to preserve. A non-zero exit means the backend was unreachable or
// refused, which is not the same as a root awaiting apply; folding it into
// absence would silently rewrite every reference in the run to a literal.
func TestProbeFailsClosedWhenTerraformExits(t *testing.T) {
	_, err := probeFor(t, initializedRoot(t), "", 1)
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
	if _, err := probeFor(t, initializedRoot(t), `{"outputs":{}}`, 0); err == nil {
		t.Errorf("probe error = nil, want a refusal for a document carrying no state version")
	}
}
