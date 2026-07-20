//go:build (darwin || linux) && !ios && !android && (amd64 || arm64)

package terraformcmd

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRunTerraformCommandInheritsSnapshotDescriptorBytes(t *testing.T) {
	requirePOSIX(t)
	root := t.TempDir()
	snapshot := filepath.Join(root, "snapshot")
	if err := os.WriteFile(snapshot, []byte("assessed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if err := os.Rename(snapshot, snapshot+".rebound"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(snapshot, []byte("replacement\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	executable := writeExecutable(t, `IFS= read -r value < "`+inheritedPlanPathForTest(t)+`" || exit 91; printf '%s' "$value"`)
	options := baseCommandOptions(t, executable)
	options.Output = TerraformCommandOutputCapture
	options.SnapshotFile = file
	result, err := RunTerraformCommand(options)
	if err != nil {
		t.Fatalf("RunTerraformCommand() error = %v", err)
	}
	if got := string(result.Stdout); got != "assessed" {
		t.Errorf("RunTerraformCommand() inherited bytes = %q, want original assessed bytes", got)
	}
}

func TestRunTerraformCommandRejectsInvalidSnapshotDescriptorBeforeSpawn(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "started")
	executable := writeExecutable(t, `printf x > "$MARKER"`)
	file, err := os.CreateTemp(t.TempDir(), "closed")
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	options := baseCommandOptions(t, executable)
	options.Environment = map[string]string{"MARKER": marker}
	options.SnapshotFile = file
	_, err = RunTerraformCommand(options)
	requireProcessFailure(t, err, "INVALID_TERRAFORM_COMMAND_SNAPSHOT")
	if _, statErr := os.Stat(marker); !os.IsNotExist(statErr) {
		t.Errorf("RunTerraformCommand(invalid snapshot) started child: marker stat = %v", statErr)
	}

	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	defer writer.Close()
	options.SnapshotFile = reader
	_, err = RunTerraformCommand(options)
	requireProcessFailure(t, err, "INVALID_TERRAFORM_COMMAND_SNAPSHOT")

	readWrite, err := os.OpenFile(filepath.Join(t.TempDir(), "read-write"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer readWrite.Close()
	options.SnapshotFile = readWrite
	_, err = RunTerraformCommand(options)
	requireProcessFailure(t, err, "INVALID_TERRAFORM_COMMAND_SNAPSHOT")
}

func TestRunTerraformCommandSnapshotDescriptorSpawnFailure(t *testing.T) {
	requirePOSIX(t)
	snapshot := filepath.Join(t.TempDir(), "snapshot")
	if err := os.WriteFile(snapshot, []byte("assessed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	executable := filepath.Join(t.TempDir(), "not-an-executable-image")
	if err := os.WriteFile(executable, []byte("not an executable image\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	options := baseCommandOptions(t, executable)
	options.SnapshotFile = file
	_, err = RunTerraformCommand(options)
	requireProcessFailure(t, err, "TERRAFORM_COMMAND_SPAWN_FAILED")
}

func inheritedPlanPathForTest(t *testing.T) string {
	t.Helper()
	path, err := InheritedPlanFilePath()
	if err != nil {
		t.Fatal(err)
	}
	return path
}
