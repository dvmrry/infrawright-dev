package terraformcmd

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFakeTerraform(t *testing.T, dir, name string, mode os.FileMode) string {
	t.Helper()
	requirePOSIX(t)
	target := filepath.Join(dir, name)
	if err := os.WriteFile(target, []byte("#!/bin/sh\nexit 0\n"), mode); err != nil {
		t.Fatalf("write fake terraform: %v", err)
	}
	return target
}

func TestResolveTerraformExecutableDefaultsToImplicitTerraform(t *testing.T) {
	root := t.TempDir()
	target := writeFakeTerraform(t, root, "terraform", 0o700)

	resolved, err := ResolveTerraformExecutable("", map[string]string{"PATH": root})
	if err != nil {
		t.Fatalf("ResolveTerraformExecutable: %v", err)
	}
	want, err := filepath.EvalSymlinks(target)
	if err != nil {
		t.Fatal(err)
	}
	if resolved != want {
		t.Errorf("resolved = %q, want %q", resolved, want)
	}
}

func TestResolveTerraformExecutableExplicitPathSkipsPATH(t *testing.T) {
	root := t.TempDir()
	target := writeFakeTerraform(t, root, "custom-terraform", 0o700)

	resolved, err := ResolveTerraformExecutable(target, map[string]string{"PATH": "/does/not/exist"})
	if err != nil {
		t.Fatalf("ResolveTerraformExecutable: %v", err)
	}
	want, err := filepath.EvalSymlinks(target)
	if err != nil {
		t.Fatal(err)
	}
	if resolved != want {
		t.Errorf("resolved = %q, want %q", resolved, want)
	}
}

func TestResolveTerraformExecutableExplicitRelativePathUsesCWD(t *testing.T) {
	requirePOSIX(t)
	root := t.TempDir()
	writeFakeTerraform(t, root, "terraform", 0o700)
	original, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(original) })

	resolved, err := ResolveTerraformExecutable("./terraform", map[string]string{})
	if err != nil {
		t.Fatalf("ResolveTerraformExecutable: %v", err)
	}
	want, err := filepath.EvalSymlinks(filepath.Join(root, "terraform"))
	if err != nil {
		t.Fatal(err)
	}
	if resolved != want {
		t.Errorf("resolved = %q, want %q", resolved, want)
	}
}

func TestResolveTerraformExecutableExplicitAbsolutePathIgnoresDeletedCWD(t *testing.T) {
	requirePOSIX(t)
	root := t.TempDir()
	target := writeFakeTerraform(t, root, "terraform", 0o700)

	deletedCWD := t.TempDir()
	original, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(deletedCWD); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(original) })
	if err := os.Remove(deletedCWD); err != nil {
		t.Fatal(err)
	}

	resolved, err := ResolveTerraformExecutable(target, map[string]string{})
	if err != nil {
		t.Fatalf("ResolveTerraformExecutable with a deleted process cwd: %v", err)
	}
	want, err := filepath.EvalSymlinks(target)
	if err != nil {
		t.Fatal(err)
	}
	if resolved != want {
		t.Errorf("resolved = %q, want %q", resolved, want)
	}
}

func TestResolveTerraformExecutableFollowsSymlink(t *testing.T) {
	requirePOSIX(t)
	root := t.TempDir()
	target := writeFakeTerraform(t, root, "terraform-from-path", 0o700)
	link := filepath.Join(root, "terraform-link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}

	resolved, err := ResolveTerraformExecutable("terraform-link", map[string]string{"PATH": root})
	if err != nil {
		t.Fatalf("ResolveTerraformExecutable: %v", err)
	}
	realTarget, err := filepath.EvalSymlinks(target)
	if err != nil {
		t.Fatal(err)
	}
	if resolved != realTarget {
		t.Errorf("resolved = %q, want %q", resolved, realTarget)
	}
}

func TestResolveTerraformExecutableSearchesEachPATHDirectoryInOrder(t *testing.T) {
	root := t.TempDir()
	first := filepath.Join(root, "first")
	second := filepath.Join(root, "second")
	if err := os.Mkdir(first, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(second, 0o700); err != nil {
		t.Fatal(err)
	}
	target := writeFakeTerraform(t, second, "terraform", 0o700)

	pathValue := first + string(filepath.ListSeparator) + second
	resolved, err := ResolveTerraformExecutable("terraform", map[string]string{"PATH": pathValue})
	if err != nil {
		t.Fatalf("ResolveTerraformExecutable: %v", err)
	}
	want, err := filepath.EvalSymlinks(target)
	if err != nil {
		t.Fatal(err)
	}
	if resolved != want {
		t.Errorf("resolved = %q, want %q", resolved, want)
	}
}

func TestResolveTerraformExecutableSkipsNonExecutableCandidate(t *testing.T) {
	root := t.TempDir()
	writeFakeTerraform(t, root, "terraform", 0o600)

	_, err := ResolveTerraformExecutable("terraform", map[string]string{"PATH": root})
	if err == nil {
		t.Fatal("error = nil, want a not-found failure for a non-executable candidate")
	}
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("errors.Is(err, fs.ErrNotExist) = false, want true (err = %v)", err)
	}
}

func TestResolveTerraformExecutableMissingWithoutPATH(t *testing.T) {
	_, err := ResolveTerraformExecutable("terraform", map[string]string{})
	if err == nil {
		t.Fatal("error = nil")
	}
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("errors.Is(err, fs.ErrNotExist) = false, want true")
	}
}

func TestResolveTerraformExecutableMissingUsesPlainGoError(t *testing.T) {
	_, err := ResolveTerraformExecutable("definitely-not-a-real-terraform-binary", map[string]string{"PATH": t.TempDir()})
	if err == nil {
		t.Fatal("error = nil")
	}
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("errors.Is(err, fs.ErrNotExist) = false, want true")
	}
	const want = `terraform executable not found: "definitely-not-a-real-terraform-binary": file does not exist`
	if err.Error() != want {
		t.Errorf("error = %q, want %q", err.Error(), want)
	}
}

func TestResolveTerraformExecutableRejectsEmbeddedNUL(t *testing.T) {
	_, err := ResolveTerraformExecutable("terraform\x00secret", nil)
	failure := requireProcessFailure(t, err, "UNRESOLVED_TERRAFORM_COMMAND_PATH")
	if !strings.Contains(failure.Message, "null byte") {
		t.Errorf("message = %q, want it to mention the null byte", failure.Message)
	}
}

func TestResolveTerraformExecutableRejectsMalformedUTF8(t *testing.T) {
	_, err := ResolveTerraformExecutable("terraform\xff", nil)
	requireProcessFailure(t, err, "UNRESOLVED_TERRAFORM_COMMAND_PATH")
}
