package plan

// Fault-injection coverage for the capture regeneration machinery: the
// shared semantic validator must refuse a corrupted staged set, and the
// script's --recover mode must restore a previous fixture set from its
// out-of-workdir backup and TRANSACTION record. Both run the real script
// artifacts, not reimplementations.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func captureDirectory(t *testing.T) string {
	t.Helper()
	return filepath.Join("testdata", "provider_double_capture")
}

func copyCaptureTree(t *testing.T, destination string) {
	t.Helper()
	if err := exec.Command("cp", "-R", captureDirectory(t), destination).Run(); err != nil {
		t.Fatalf("copy capture tree: %v", err)
	}
}

func TestCaptureValidatorAcceptsCommittedSetAndRefusesCorruption(t *testing.T) {
	validator := filepath.Join(captureDirectory(t), "validate_captures.py")
	if output, err := exec.Command("python3", validator, captureDirectory(t)).CombinedOutput(); err != nil {
		t.Fatalf("validator over the committed set = %v\n%s", err, output)
	}

	workspace := t.TempDir()
	corrupted := filepath.Join(workspace, "captures")
	copyCaptureTree(t, corrupted)
	target := filepath.Join(corrupted, "refresh_true", "show.json")
	raw, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read capture: %v", err)
	}
	tampered := strings.Replace(string(raw), "018da47922f5094d", "ffffffffffffffff", 1)
	if tampered == string(raw) {
		t.Fatal("corruption sentinel not applied")
	}
	if err := os.WriteFile(target, []byte(tampered), 0o644); err != nil {
		t.Fatalf("write corrupted capture: %v", err)
	}
	output, err := exec.Command("python3", validator, corrupted).CombinedOutput()
	if err == nil {
		t.Fatalf("validator accepted a corrupted set:\n%s", output)
	}
	if !strings.Contains(string(output), "refresh") {
		t.Errorf("validator refusal output = %q, want it to name the corrupted surface", output)
	}
}

func TestCaptureRecoveryRestoresPreviousSet(t *testing.T) {
	workspace := t.TempDir()
	tree := filepath.Join(workspace, "captures")
	copyCaptureTree(t, tree)

	// Fabricate a half-promoted state: the live fixture was overwritten,
	// the backup preserves the previous bytes plus the TRANSACTION record.
	backup := filepath.Join(tree, ".capture-backup.test")
	scenario := "no_op"
	if err := os.MkdirAll(filepath.Join(backup, scenario), 0o755); err != nil {
		t.Fatal(err)
	}
	previous := []byte(`{"previous":"fixture"}`)
	if err := os.WriteFile(filepath.Join(backup, scenario, "show.json"), previous, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(backup, "TRANSACTION"), []byte("state=promoting\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tree, scenario, "show.json"), []byte(`{"half":"promoted"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	output, err := exec.Command("sh", filepath.Join(tree, "gen-captures.sh"), "--recover", backup).CombinedOutput()
	if err != nil {
		t.Fatalf("--recover = %v\n%s", err, output)
	}
	restored, err := os.ReadFile(filepath.Join(tree, scenario, "show.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(restored) != string(previous) {
		t.Errorf("recovered fixture = %q, want the backed-up previous bytes", restored)
	}
	if _, err := os.Stat(backup); !os.IsNotExist(err) {
		t.Errorf("backup directory survives recovery: %v", err)
	}

	// A backup without a TRANSACTION record refuses recovery.
	orphan := filepath.Join(tree, ".capture-backup.orphan")
	if err := os.MkdirAll(orphan, 0o755); err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command("sh", filepath.Join(tree, "gen-captures.sh"), "--recover", orphan).CombinedOutput(); err == nil {
		t.Fatalf("--recover accepted a backup without TRANSACTION:\n%s", output)
	}
}
