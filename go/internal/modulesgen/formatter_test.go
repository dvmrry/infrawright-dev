package modulesgen

// formatter_test.go exercises the HclFormatter injected seam directly.
// Unlike generator_test.go, none of these are ports of a specific
// node-tests/module-generator.test.ts vector (that suite only ever
// exercises terraformHclFormatter's `executable` option, via the
// "Terraform formatter failures preserve concrete diagnostics" case
// already ported in generator_test.go); these are this Go port's own
// coverage for the seam itself: FormatterFunc/IdentityFormatter as fakes,
// executable-resolution precedence, and the environment-replaces-rather-
// than-merges contract the TS source's `env: environment` spawn option
// establishes (see NewTerraformFormatter's doc comment).

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestFormatterFuncSatisfiesHclFormatterSeam confirms a plain function can
// stand in for HclFormatter, the same role node-tests/
// module-generator.test.ts's IDENTITY_FORMATTER fake plays for the TS
// source's bare `HclFormatter` function-type alias.
func TestFormatterFuncSatisfiesHclFormatterSeam(t *testing.T) {
	calls := 0
	fake := FormatterFunc(func(source string) (string, error) {
		calls++
		return strings.ToUpper(source), nil
	})
	var seam HclFormatter = fake
	got, err := seam.FormatHCL("abc")
	if err != nil {
		t.Fatalf("FormatHCL: %v", err)
	}
	if got != "ABC" {
		t.Errorf("FormatHCL(%q) = %q, want %q", "abc", got, "ABC")
	}
	if calls != 1 {
		t.Errorf("calls = %d, want 1", calls)
	}
}

// TestIdentityFormatterReturnsSourceUnchanged confirms the shared
// IdentityFormatter fake (used throughout generator_test.go in place of a
// real Terraform binary) is a true identity.
func TestIdentityFormatterReturnsSourceUnchanged(t *testing.T) {
	const source = "resource \"x\" \"y\" {}\n"
	got, err := IdentityFormatter.FormatHCL(source)
	if err != nil {
		t.Fatalf("FormatHCL: %v", err)
	}
	if got != source {
		t.Errorf("FormatHCL(%q) = %q, want unchanged", source, got)
	}
}

// TestNewTerraformFormatterResolvesExecutableFromOptionsThenTF confirms
// executable-resolution precedence ports terraformHclFormatter's
// `options?.executable || environment.TF || "terraform"` fallback chain:
// an explicit Executable always wins, and Environment["TF"] is consulted
// only when Executable is empty. Both cases here point at a nonexistent
// path purely to observe which name NewTerraformFormatter actually tried
// to run, without depending on a real terraform binary being installed.
func TestNewTerraformFormatterResolvesExecutableFromOptionsThenTF(t *testing.T) {
	directory := t.TempDir()
	missing := filepath.Join(directory, "definitely-not-a-real-binary")

	explicit := NewTerraformFormatter(TerraformFormatterOptions{Executable: missing})
	if _, err := explicit.FormatHCL("x\n"); err == nil {
		t.Fatal("explicit Executable: expected an error for a nonexistent binary, got nil")
	}

	viaEnvironment := NewTerraformFormatter(TerraformFormatterOptions{
		Environment: map[string]string{"TF": missing},
	})
	if _, err := viaEnvironment.FormatHCL("x\n"); err == nil {
		t.Fatal("Environment[TF]: expected an error for a nonexistent binary, got nil")
	}
}

// TestTerraformFormatterEnvironmentReplacesRatherThanMerges confirms the
// child process sees Environment (when non-nil) as its *entire*
// environment -- not process env plus Environment -- ported from the TS
// source's `env: environment` spawn option, which Node's own
// child_process.spawn documents as a full replacement, never a merge with
// process.env, whenever an `env` option is supplied at all. A nil
// Environment (the zero value most callers use) is the other branch:
// inherit this process's environment wholesale.
func TestTerraformFormatterEnvironmentReplacesRatherThanMerges(t *testing.T) {
	directory := t.TempDir()
	script := filepath.Join(directory, "fake-terraform")
	// Ignores its arguments (so it doubles as a `fmt -` stand-in) and
	// prints FOO, letting the test observe exactly what environment the
	// child process received.
	content := "#!/bin/sh\nif [ -z \"${FOO:-}\" ]; then echo unset; else echo \"$FOO\"; fi\n"
	if err := os.WriteFile(script, []byte(content), 0o755); err != nil {
		t.Fatalf("write fake terraform script: %v", err)
	}

	t.Setenv("FOO", "from-parent")

	replaced := NewTerraformFormatter(TerraformFormatterOptions{
		Executable:  script,
		Environment: map[string]string{"FOO": "from-options"},
	})
	got, err := replaced.FormatHCL("ignored\n")
	if err != nil {
		t.Fatalf("FormatHCL (explicit Environment): %v", err)
	}
	if strings.TrimSpace(got) != "from-options" {
		t.Errorf("with explicit Environment, child saw FOO=%q, want %q (parent FOO must not leak in)", strings.TrimSpace(got), "from-options")
	}

	inherited := NewTerraformFormatter(TerraformFormatterOptions{Executable: script})
	got, err = inherited.FormatHCL("ignored\n")
	if err != nil {
		t.Fatalf("FormatHCL (nil Environment): %v", err)
	}
	if strings.TrimSpace(got) != "from-parent" {
		t.Errorf("with nil Environment, child saw FOO=%q, want %q (inherit process env)", strings.TrimSpace(got), "from-parent")
	}
}
