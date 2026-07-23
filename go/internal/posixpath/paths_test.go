package posixpath

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNormalizeEdgeCases(t *testing.T) {
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
		if got := Normalize(c.input); got != c.expected {
			t.Errorf("Normalize(%q) = %q, want %q", c.input, got, c.expected)
		}
	}
}

func TestRealpathCanonicalizesPrefixesBeforeSymlinkLoops(t *testing.T) {
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
	canonicalParent, err := filepath.EvalSymlinks(realParent)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(canonicalParent, "a", "deleted-child")

	if got := Realpath(candidate); got != want {
		t.Errorf("Realpath(%q) = %q, want %q", candidate, got, want)
	}
}

func TestRelativeUnderUsesSuppliedWorkspace(t *testing.T) {
	got, ok := RelativeUnder(
		"artifacts/config/prod/x.auto.tfvars.json",
		"artifacts/config",
		"/tmp/workspace",
	)
	if !ok {
		t.Fatalf("RelativeUnder: expected containment, got (nil, false)")
	}
	want := []string{"prod", "x.auto.tfvars.json"}
	if len(got) != len(want) {
		t.Fatalf("RelativeUnder = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("RelativeUnder = %v, want %v", got, want)
		}
	}

	if _, ok := RelativeUnder("../outside", "artifacts/config", "/tmp/workspace"); ok {
		t.Fatalf("RelativeUnder(../outside, ...) expected (nil, false)")
	}
}

func TestRelativeUnderAtRootReturnsEmptyNonNilSlice(t *testing.T) {
	got, ok := RelativeUnder("artifacts/config", "artifacts/config", "/tmp/workspace")
	if !ok {
		t.Fatalf("RelativeUnder: expected containment for value == root")
	}
	if got == nil || len(got) != 0 {
		t.Fatalf("RelativeUnder(value == root) = %#v, want empty non-nil slice", got)
	}
}

func TestJoin(t *testing.T) {
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
		if got := Join(c.parts...); got != c.expected {
			t.Errorf("Join(%v) = %q, want %q", c.parts, got, c.expected)
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
