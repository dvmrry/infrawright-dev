package pypath

// paths_test.go ports node-tests/paths.test.ts's three test cases
// verbatim: POSIX normalization's Python edge cases, non-strict realpath's
// symlink-loop canonicalization (cross-checked against the same Python
// oracle the Node test spawns), and relative-containment's workspace-scoped
// behavior.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestPythonPosixNormPathMatchesPythonEdgeCases(t *testing.T) {
	cases := []struct {
		input, expected string
	}{
		{"", "."},
		{"./", "."},
		{"a/", "a"},
		{"a//b/../c", "a/c"},
		{"../a/../../b", "../../b"},
		{"/../../a", "/a"},
		{"//server/share/../x", "//server/x"},
		{"///server/share", "/server/share"},
	}
	for _, c := range cases {
		if got := PythonPosixNormPath(c.input); got != c.expected {
			t.Errorf("PythonPosixNormPath(%q) = %q, want %q", c.input, got, c.expected)
		}
	}
}

// pythonOracle resolves a Python 3 interpreter the way
// node-tests/python-oracle.ts's resolvePythonOracle does (PYTHON env var
// first, then python3/python on PATH), skipping the test if none is found
// rather than failing the whole gate on an environment without Python --
// this test's only role is cross-checking PythonPosixRealpath against a
// real symlink tree, not asserting Python's presence.
func pythonOracle(t *testing.T) string {
	t.Helper()
	if configured := strings.TrimSpace(os.Getenv("PYTHON")); configured != "" {
		return configured
	}
	for _, candidate := range []string{"python3", "python"} {
		if path, err := exec.LookPath(candidate); err == nil {
			return path
		}
	}
	t.Skip("no python3/python interpreter on PATH; set PYTHON to enable this cross-check")
	return ""
}

func TestPythonPosixRealpathCanonicalizesPrefixesBeforeSymlinkLoops(t *testing.T) {
	python := pythonOracle(t)
	directory := t.TempDir()

	realParent := filepath.Join(directory, "real-parent")
	if err := os.Mkdir(realParent, 0o755); err != nil {
		t.Fatal(err)
	}
	aliasParent := filepath.Join(directory, "alias-parent")
	if err := os.Symlink(realParent, aliasParent); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("b", filepath.Join(realParent, "a")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("a", filepath.Join(realParent, "b")); err != nil {
		t.Fatal(err)
	}
	candidate := filepath.Join(aliasParent, "a", "deleted-child")

	out, err := exec.Command(python, "-c",
		"import os,sys; print(os.path.realpath(sys.argv[1]))", candidate).Output()
	if err != nil {
		t.Fatalf("python oracle: %v", err)
	}
	want := strings.TrimRight(string(out), "\n")

	if got := PythonPosixRealpath(candidate); got != want {
		t.Errorf("PythonPosixRealpath(%q) = %q, want %q (python oracle)", candidate, got, want)
	}
}

func TestPythonRelativeUnderUsesSuppliedWorkspace(t *testing.T) {
	got, ok := PythonRelativeUnder(
		"artifacts/config/prod/x.auto.tfvars.json",
		"artifacts/config",
		"/tmp/workspace",
	)
	if !ok {
		t.Fatalf("PythonRelativeUnder: expected containment, got (nil, false)")
	}
	want := []string{"prod", "x.auto.tfvars.json"}
	if len(got) != len(want) {
		t.Fatalf("PythonRelativeUnder = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("PythonRelativeUnder = %v, want %v", got, want)
		}
	}

	if _, ok := PythonRelativeUnder("../outside", "artifacts/config", "/tmp/workspace"); ok {
		t.Fatalf("PythonRelativeUnder(../outside, ...) expected (nil, false)")
	}
}

func TestPythonRelativeUnderAtRootReturnsEmptyNonNilSlice(t *testing.T) {
	got, ok := PythonRelativeUnder("artifacts/config", "artifacts/config", "/tmp/workspace")
	if !ok {
		t.Fatalf("PythonRelativeUnder: expected containment for value == root")
	}
	if got == nil || len(got) != 0 {
		t.Fatalf("PythonRelativeUnder(value == root) = %#v, want empty non-nil slice", got)
	}
}

func TestPythonPosixJoin(t *testing.T) {
	cases := []struct {
		parts    []string
		expected string
	}{
		{[]string{}, ""},
		{[]string{"a", "b"}, "a/b"},
		{[]string{"a/", "b"}, "a/b"},
		{[]string{"a", "/b"}, "/b"},
		{[]string{"", "b"}, "b"},
	}
	for _, c := range cases {
		if got := PythonPosixJoin(c.parts...); got != c.expected {
			t.Errorf("PythonPosixJoin(%v) = %q, want %q", c.parts, got, c.expected)
		}
	}
}

func TestSameContractPath(t *testing.T) {
	workspace := t.TempDir()
	if !SameContractPath("a/b", "a/b", workspace) {
		t.Errorf("SameContractPath(a/b, a/b) = false, want true")
	}
	if SameContractPath("a/b", "a/c", workspace) {
		t.Errorf("SameContractPath(a/b, a/c) = true, want false")
	}
}
