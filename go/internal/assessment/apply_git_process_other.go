//go:build !aix && !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd && !solaris

package assessment

import "os/exec"

// Non-POSIX platforms retain exec.CommandContext's direct-process
// cancellation. Exact Apply remains fail-closed through the "unknown" branch.
func configureApplyGitProcess(_ *exec.Cmd) {}

func cleanupApplyGitProcessGroup(_ *exec.Cmd) error { return nil }
