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
	scenarios := []string{
		"initial_create", "no_op", "refresh_id_change", "rekey_refusal",
		"empty_for_each", "refresh_false", "refresh_true",
	}
	setup := func(t *testing.T) (string, string) {
		t.Helper()
		workspace := t.TempDir()
		tree := filepath.Join(workspace, "captures")
		copyCaptureTree(t, tree)
		backup := filepath.Join(tree, ".capture-backup.test")
		for _, scenario := range scenarios {
			if err := os.MkdirAll(filepath.Join(backup, scenario), 0o755); err != nil {
				t.Fatal(err)
			}
			previous, err := os.ReadFile(filepath.Join(tree, scenario, "show.json"))
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(backup, scenario, "show.json"), previous, 0o644); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.WriteFile(filepath.Join(backup, "TRANSACTION"), []byte("state=promoting\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		return tree, backup
	}

	t.Run("full backup restores and validates", func(t *testing.T) {
		tree, backup := setup(t)
		// Fabricate a half-promoted state: one live fixture holds garbage.
		garbage := filepath.Join(tree, "no_op", "show.json")
		if err := os.WriteFile(garbage, []byte(`{"half":"promoted"}`), 0o644); err != nil {
			t.Fatal(err)
		}
		output, err := exec.Command("sh", filepath.Join(tree, "gen-captures.sh"), "--recover", backup).CombinedOutput()
		if err != nil {
			t.Fatalf("--recover = %v\n%s", err, output)
		}
		if !strings.Contains(string(output), "RECOVERED") {
			t.Errorf("--recover output = %q, want RECOVERED", output)
		}
		if _, err := os.Stat(backup); !os.IsNotExist(err) {
			t.Errorf("backup directory survives successful recovery: %v", err)
		}
		validator := filepath.Join(tree, "validate_captures.py")
		if output, err := exec.Command("python3", validator, tree).CombinedOutput(); err != nil {
			t.Errorf("recovered set fails validation: %v\n%s", err, output)
		}
	})

	t.Run("incomplete backup refuses without touching live files", func(t *testing.T) {
		tree, backup := setup(t)
		if err := os.RemoveAll(filepath.Join(backup, "empty_for_each")); err != nil {
			t.Fatal(err)
		}
		sentinel := []byte(`{"live":"untouched"}`)
		live := filepath.Join(tree, "no_op", "show.json")
		if err := os.WriteFile(live, sentinel, 0o644); err != nil {
			t.Fatal(err)
		}
		output, err := exec.Command("sh", filepath.Join(tree, "gen-captures.sh"), "--recover", backup).CombinedOutput()
		if err == nil {
			t.Fatalf("--recover accepted an incomplete backup:\n%s", output)
		}
		if !strings.Contains(string(output), "incomplete") {
			t.Errorf("refusal output = %q, want it to name the incomplete backup", output)
		}
		after, err := os.ReadFile(live)
		if err != nil || string(after) != string(sentinel) {
			t.Errorf("live file altered by refused recovery: %q err=%v", after, err)
		}
		if _, err := os.Stat(backup); err != nil {
			t.Errorf("backup deleted by refused recovery: %v", err)
		}
	})

	t.Run("missing transaction record refuses", func(t *testing.T) {
		tree, backup := setup(t)
		if err := os.Remove(filepath.Join(backup, "TRANSACTION")); err != nil {
			t.Fatal(err)
		}
		if output, err := exec.Command("sh", filepath.Join(tree, "gen-captures.sh"), "--recover", backup).CombinedOutput(); err == nil {
			t.Fatalf("--recover accepted a backup without TRANSACTION:\n%s", output)
		}
	})

	t.Run("completed promotion refuses recovery", func(t *testing.T) {
		tree, backup := setup(t)
		// The generator appends state=done after a fully promoted set, so an
		// interrupted CLEANUP (not promotion) leaves exactly this record.
		record := "state=promoting\npromoted=initial_create\npromoted=no_op\npromoted=refresh_id_change\npromoted=rekey_refusal\npromoted=empty_for_each\npromoted=refresh_false\npromoted=refresh_true\nstate=done\n"
		if err := os.WriteFile(filepath.Join(backup, "TRANSACTION"), []byte(record), 0o644); err != nil {
			t.Fatal(err)
		}
		if output, err := exec.Command("sh", filepath.Join(tree, "gen-captures.sh"), "--recover", backup).CombinedOutput(); err == nil {
			t.Fatalf("--recover accepted a completed-promotion record:\n%s", output)
		}
	})

	t.Run("mid-promotion record with promoted lines recovers", func(t *testing.T) {
		tree, backup := setup(t)
		record := "state=promoting\npromoted=initial_create\npromoted=no_op\n"
		if err := os.WriteFile(filepath.Join(backup, "TRANSACTION"), []byte(record), 0o644); err != nil {
			t.Fatal(err)
		}
		if output, err := exec.Command("sh", filepath.Join(tree, "gen-captures.sh"), "--recover", backup).CombinedOutput(); err != nil {
			t.Fatalf("--recover(mid-promotion) = %v\n%s", err, output)
		}
	})

	t.Run("unknown promoted scenario refuses", func(t *testing.T) {
		tree, backup := setup(t)
		record := "state=promoting\npromoted=not_a_scenario\n"
		if err := os.WriteFile(filepath.Join(backup, "TRANSACTION"), []byte(record), 0o644); err != nil {
			t.Fatal(err)
		}
		if output, err := exec.Command("sh", filepath.Join(tree, "gen-captures.sh"), "--recover", backup).CombinedOutput(); err == nil {
			t.Fatalf("--recover accepted an unknown promoted scenario:\n%s", output)
		}
	})
}
