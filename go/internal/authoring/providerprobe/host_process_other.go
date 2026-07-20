//go:build !aix && !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd && !solaris && !windows

package providerprobe

import (
	"context"
	"errors"
	"time"
)

func legacyGitExecutableName() string { return "git" }
func legacyGitAskPass() string        { return "" }
func legacyGitNullDevice() string     { return "" }

func runLegacyGitProcess(parent context.Context, _ string, _ []string, _ []string, timeout time.Duration, _ int64) error {
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return err
	}
	return errors.New("legacy Git clone is unsupported on this platform")
}
