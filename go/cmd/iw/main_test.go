package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestPackageRootExplicitOverrideWins(t *testing.T) {
	workspace := t.TempDir()
	t.Chdir(workspace)
	configured := filepath.Join("runtime", "root")
	t.Setenv("INFRAWRIGHT_PACKAGE_ROOT", configured)

	got, err := packageRoot()
	if err != nil {
		t.Fatalf("packageRoot() error = %v, want nil", err)
	}
	want := filepath.Join(workspace, configured)
	if got != want {
		t.Errorf("packageRoot() = %q, want explicit absolute root %q", got, want)
	}
}

func TestPackageRootRejectsEmptyExplicitOverride(t *testing.T) {
	t.Setenv("INFRAWRIGHT_PACKAGE_ROOT", "")

	_, err := packageRoot()
	if err == nil || !strings.Contains(err.Error(), "INFRAWRIGHT_PACKAGE_ROOT must not be empty") {
		t.Errorf("packageRoot() error = %v, want empty-override error", err)
	}
}

func TestFindPackageRootUsesRuntimeDataMarkersNotPackageJSON(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "packs"), 0o755); err != nil {
		t.Fatalf("os.Mkdir(%q) error = %v, want nil", "packs", err)
	}
	if err := os.WriteFile(filepath.Join(root, "packs", "full.packset.json"), []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("os.WriteFile(full.packset.json) error = %v, want nil", err)
	}
	nested := filepath.Join(root, "bin", "nested")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("os.MkdirAll(%q) error = %v, want nil", nested, err)
	}
	if err := os.WriteFile(filepath.Join(root, "bin", "package.json"), []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("os.WriteFile(package.json) error = %v, want nil", err)
	}

	got, err := findPackageRoot(nested)
	if err != nil {
		t.Fatalf("findPackageRoot(%q) error = %v, want nil", nested, err)
	}
	if got != root {
		t.Errorf("findPackageRoot(%q) = %q, want runtime-data root %q", nested, got, root)
	}
}

func TestFindPackageRootRetainsLegacyPackageJSONFallback(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "package.json"), []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("os.WriteFile(package.json) error = %v, want nil", err)
	}
	nested := filepath.Join(root, "bin")
	if err := os.Mkdir(nested, 0o755); err != nil {
		t.Fatalf("os.Mkdir(%q) error = %v, want nil", nested, err)
	}

	got, err := findPackageRoot(nested)
	if err != nil {
		t.Fatalf("findPackageRoot(%q) error = %v, want nil", nested, err)
	}
	if got != root {
		t.Errorf("findPackageRoot(%q) = %q, want legacy marker root %q", nested, got, root)
	}
}

func TestRelocatedBinaryUsesExplicitPackageRoot(t *testing.T) {
	repository := repoRoot(t)
	binary := filepath.Join(t.TempDir(), "iw")
	build := exec.Command("go", "build", "-o", binary, ".")
	build.Dir = filepath.Join(repository, "go", "cmd", "iw")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build relocated iw error = %v, want nil\n%s", err, output)
	}

	run := exec.Command(binary, "root-catalog", "--providers", "zcc")
	run.Dir = t.TempDir()
	run.Env = []string{
		"HOME=" + os.Getenv("HOME"),
		"INFRAWRIGHT_PACKAGE_ROOT=" + repository,
		"PATH=" + os.Getenv("PATH"),
		"TMPDIR=" + os.Getenv("TMPDIR"),
	}
	output, err := run.CombinedOutput()
	if err != nil {
		t.Fatalf("relocated iw root-catalog error = %v, want nil\n%s", err, output)
	}
	var catalog map[string]any
	if err := json.Unmarshal(output, &catalog); err != nil {
		t.Fatalf("json.Unmarshal(relocated iw output) error = %v, want nil\n%s", err, output)
	}
	if _, ok := catalog["resources"]; !ok {
		t.Errorf("relocated iw root-catalog keys = %v, want resources", catalog)
	}
}
