//go:build !windows

package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"syscall"
)

func prepareProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func postStartProcessGroup(_ *exec.Cmd) error { return nil }

func releaseProcessGroup(cmd *exec.Cmd) error {
	if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		return fmt.Errorf("stop remaining process group members: %w", err)
	}
	return nil
}

func forwardProcessSignal(cmd *exec.Cmd, sig os.Signal) error {
	if s, ok := sig.(syscall.Signal); ok {
		if err := syscall.Kill(-cmd.Process.Pid, s); err != nil && !errors.Is(err, syscall.ESRCH) {
			return fmt.Errorf("signal process group: %w", err)
		}
		return nil
	}
	return fmt.Errorf("unsupported process signal %v", sig)
}

func killProcessGroup(cmd *exec.Cmd) error {
	if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		if childErr := cmd.Process.Kill(); childErr != nil && !errors.Is(childErr, os.ErrProcessDone) {
			return errors.Join(fmt.Errorf("kill process group: %w", err), fmt.Errorf("kill child: %w", childErr))
		}
		return fmt.Errorf("kill process group: %w; killed direct child", err)
	}
	return nil
}

func processExitCode(exit *exec.ExitError) int {
	if code := exit.ExitCode(); code >= 0 {
		return code
	}
	if status, ok := exit.Sys().(syscall.WaitStatus); ok && status.Signaled() {
		return 128 + int(status.Signal())
	}
	return 1
}
