//go:build !aix && !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd && !solaris && !windows

package terraformcmd

import (
	"os"
	"os/exec"
)

const terraformProcessGroupsSupported = false

func configureTerraformProcess(_ *exec.Cmd) {}

func startTerraformProcess(command *exec.Cmd) (func(), error) {
	if err := command.Start(); err != nil {
		return nil, err
	}
	return func() {}, nil
}

func killTerraformProcessGroup(_ int, process *os.Process) {
	if process != nil {
		_ = process.Kill()
	}
}
