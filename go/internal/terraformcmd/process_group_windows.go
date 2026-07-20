//go:build windows

package terraformcmd

import (
	"os"
	"os/exec"
	"syscall"
)

const terraformProcessGroupsSupported = false

func configureTerraformProcess(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
}

func killTerraformProcessGroup(_ int, process *os.Process) {
	if process != nil {
		_ = process.Kill()
	}
}

func startTerraformProcess(command *exec.Cmd) (func(), error) {
	if err := command.Start(); err != nil {
		return nil, err
	}
	return func() {}, nil
}
