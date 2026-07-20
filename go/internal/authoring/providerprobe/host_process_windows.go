//go:build windows

package providerprobe

import (
	"context"
	"errors"
	"time"
)

func legacyGitExecutableName() string { return "git.exe" }
func legacyGitAskPass() string        { return "NUL" }
func legacyGitNullDevice() string     { return "NUL" }

// Windows has no safe process-tree cleanup primitive in the standard library.
// Refuse the operation rather than claim a direct-child kill satisfies the
// legacy process-group cleanup guarantee.
func runLegacyGitProcess(parent context.Context, _ string, _ []string, _ []string, timeout time.Duration, _ int64) error {
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return err
	}
	return errors.New("legacy Git clone is unsupported on Windows")
}
