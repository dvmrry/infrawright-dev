//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package terraformcmd

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestRunTerraformCommandContextCancellationReapsProcessGroup(t *testing.T) {
	root := t.TempDir()
	directPIDFile := filepath.Join(root, "direct.pid")
	descendantPIDFile := filepath.Join(root, "descendant.pid")
	executable := writeExecutable(t, `
printf '%s' "$$" > "$DIRECT_PID_FILE"
while :; do :; done &
printf '%s' "$!" > "$DESCENDANT_PID_FILE"
while :; do :; done
`)
	options := baseCommandOptions(t, executable)
	options.Environment = map[string]string{
		"DIRECT_PID_FILE":     directPIDFile,
		"DESCENDANT_PID_FILE": descendantPIDFile,
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	result := make(chan error, 1)
	go func() {
		_, err := RunTerraformCommandContext(ctx, options)
		result <- err
	}()
	directPID := waitForPIDFile(t, directPIDFile)
	descendantPID := waitForPIDFile(t, descendantPIDFile)
	cancel()
	select {
	case err := <-result:
		if err != context.Canceled {
			t.Errorf("RunTerraformCommandContext() error = %v, want raw context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("RunTerraformCommandContext did not return after cancellation")
	}
	waitForProcessMissing(t, directPID)
	waitForProcessMissing(t, descendantPID)
}
