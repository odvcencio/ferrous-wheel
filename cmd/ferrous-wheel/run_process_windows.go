//go:build windows

package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sync"

	"m31labs.dev/ferrous-wheel/internal/winjob"
)

var processJobs sync.Map // *exec.Cmd -> *winjob.Job

func prepareProcessGroup(cmd *exec.Cmd) { winjob.Prepare(cmd) }

func postStartProcessGroup(cmd *exec.Cmd) error {
	job, err := winjob.Attach(cmd)
	if err != nil {
		return err
	}
	processJobs.Store(cmd, job)
	return nil
}

func releaseProcessGroup(cmd *exec.Cmd) error {
	value, ok := processJobs.LoadAndDelete(cmd)
	if !ok {
		return nil
	}
	return value.(*winjob.Job).Close()
}

func forwardProcessSignal(cmd *exec.Cmd, sig os.Signal) error {
	if sig != os.Interrupt {
		return killProcessGroup(cmd)
	}
	value, ok := processJobs.Load(cmd)
	if !ok {
		return errors.New("process has no job")
	}
	if err := value.(*winjob.Job).Interrupt(cmd.Process.Pid); err != nil {
		return fmt.Errorf("send console break: %w", err)
	}
	return nil
}

func killProcessGroup(cmd *exec.Cmd) error {
	value, ok := processJobs.Load(cmd)
	if !ok {
		if err := cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
			return fmt.Errorf("kill child without job: %w", err)
		}
		return errors.New("process has no job; descendants may remain")
	}
	if err := value.(*winjob.Job).Kill(); err != nil {
		if childErr := cmd.Process.Kill(); childErr != nil && !errors.Is(childErr, os.ErrProcessDone) {
			return errors.Join(fmt.Errorf("terminate job: %w", err), fmt.Errorf("kill child: %w", childErr))
		}
		return fmt.Errorf("terminate job: %w; killed direct child", err)
	}
	return nil
}

func processExitCode(exit *exec.ExitError) int {
	return exit.ExitCode()
}
