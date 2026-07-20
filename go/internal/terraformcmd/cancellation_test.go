package terraformcmd

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestRunTerraformCommandContextCancelledBeforeSpawnHasNoSideEffects(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "started")
	executable := writeExecutable(t, `printf x > "$MARKER"`)
	options := baseCommandOptions(t, executable)
	options.Environment = map[string]string{"MARKER": marker}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := RunTerraformCommandContext(ctx, options)
	if err != context.Canceled {
		t.Errorf("RunTerraformCommandContext() error = %v, want raw context.Canceled", err)
	}
	if _, statErr := os.Stat(marker); !os.IsNotExist(statErr) {
		t.Errorf("RunTerraformCommandContext(cancelled) started child: marker stat = %v", statErr)
	}
}
