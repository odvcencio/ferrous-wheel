//go:build windows

package main

import (
	"os"
	"os/exec"
)

func prepareProcessGroup(_ *exec.Cmd) {}

func forwardProcessSignal(cmd *exec.Cmd, sig os.Signal) {
	if err := cmd.Process.Signal(sig); err != nil {
		_ = cmd.Process.Kill()
	}
}

func killProcessGroup(cmd *exec.Cmd) { _ = cmd.Process.Kill() }

func processExitCode(exit *exec.ExitError) int {
	return exit.ExitCode()
}
