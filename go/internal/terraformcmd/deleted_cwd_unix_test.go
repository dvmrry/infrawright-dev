//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package terraformcmd

import (
	"os"
	"reflect"
	"syscall"
	"testing"
)

func TestTerraformExecutableCandidatesAbsoluteInputsDoNotReadDeletedCWD(t *testing.T) {
	original, err := os.Open(".")
	if err != nil {
		t.Fatal(err)
	}
	restored := true
	t.Cleanup(func() {
		if !restored {
			if err := syscall.Fchdir(int(original.Fd())); err != nil {
				t.Errorf("restore cwd during cleanup: %v", err)
			}
		}
		if err := original.Close(); err != nil {
			t.Errorf("close saved cwd: %v", err)
		}
	})
	root := t.TempDir()
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	restored = false
	if err := os.Remove(root); err != nil {
		t.Fatal(err)
	}
	originalGetwd := terraformExecutableGetwd
	terraformExecutableGetwd = func() (string, error) { return "", syscall.ENOENT }
	t.Cleanup(func() { terraformExecutableGetwd = originalGetwd })

	got, err := TerraformExecutableCandidates(
		"/trusted/terraform",
		map[string]string{},
		&TerraformExecutableCandidateOptions{CWD: "/provided", Platform: "linux"},
	)
	if err != nil {
		t.Fatalf("lexical absolute candidates from deleted cwd: %v", err)
	}
	want := []string{"/trusted/terraform"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("candidates = %#v, want %#v", got, want)
	}

	got, err = TerraformExecutableCandidates(
		"/trusted/terraform",
		map[string]string{},
		&TerraformExecutableCandidateOptions{CWDSet: true, Platform: "linux"},
	)
	if err != nil {
		t.Fatalf("lexical absolute candidates with explicit empty cwd: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("explicit-empty-cwd candidates = %#v, want %#v", got, want)
	}

	if _, err := TerraformExecutableCandidates(
		"/trusted/terraform",
		map[string]string{},
		&TerraformExecutableCandidateOptions{Platform: "linux"},
	); err == nil {
		t.Error("omitted cwd unexpectedly succeeded after the process cwd was deleted")
	}
	if err := syscall.Fchdir(int(original.Fd())); err != nil {
		t.Fatalf("restore cwd: %v", err)
	}
	restored = true
}
