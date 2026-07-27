package main

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestMakeGenEnvPassesStateAwareFlag pins the Make interface, which no Go test
// otherwise reaches. Every gate in this repository stays green if
// `$(if $(STATE_AWARE),--state-aware)` is deleted from the gen-env recipe --
// the flag keeps working through `iw gen-env --state-aware`, while
// `make gen-env STATE_AWARE=1`, the documented entry point, silently becomes
// state-blind and generation quietly stops falling back.
//
// `make -n` expands the recipe without running it or building dist/iw, so this
// pins the wiring itself rather than any generation behaviour.
func TestMakeGenEnvPassesStateAwareFlag(t *testing.T) {
	repositoryRoot, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatalf("resolve repository root: %v", err)
	}

	recipe := func(t *testing.T, arguments ...string) string {
		t.Helper()
		command := exec.Command("make", append([]string{"-n", "gen-env", "TENANT=state-aware-probe"}, arguments...)...)
		command.Dir = repositoryRoot
		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("make -n gen-env %v: %v\n%s", arguments, err, output)
		}
		return string(output)
	}

	withFlag := recipe(t, "STATE_AWARE=1")
	if !strings.Contains(withFlag, "--state-aware") {
		t.Errorf("make gen-env STATE_AWARE=1 recipe = %q, want it to pass --state-aware", withFlag)
	}

	withoutFlag := recipe(t)
	if strings.Contains(withoutFlag, "--state-aware") {
		t.Errorf("make gen-env recipe = %q, want no --state-aware without STATE_AWARE", withoutFlag)
	}
}
