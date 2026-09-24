package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"time"
)

type processControlError struct {
	processErr error
	controlErr error
}

func (e *processControlError) Error() string {
	if e.processErr != nil {
		return fmt.Sprintf("process control: %v; child result: %v", e.controlErr, e.processErr)
	}
	return fmt.Sprintf("process control: %v", e.controlErr)
}

func (e *processControlError) Unwrap() error { return e.controlErr }

func signalNumber(sig os.Signal) int {
	if number, ok := sig.(syscall.Signal); ok {
		return int(number)
	}
	if sig == os.Interrupt {
		return 2
	}
	return 15
}

// runWithSignals forwards an interrupt to the whole process group. It gives
// the command two seconds to finish, then kills the group. The caller owns
// signal.Notify and keeps it active through both compilation and execution.
func runWithSignals(cmd *exec.Cmd, signals <-chan os.Signal) (runErr error, forwarded os.Signal) {
	prepareProcessGroup(cmd)
	if err := cmd.Start(); err != nil {
		return err, nil
	}
	if err := postStartProcessGroup(cmd); err != nil {
		killErr := cmd.Process.Kill()
		reaped := make(chan error, 1)
		go func() { reaped <- cmd.Wait() }()
		select {
		case <-reaped:
			if killErr != nil && !errors.Is(killErr, os.ErrProcessDone) {
				return fmt.Errorf("attach process group: %w; kill child: %v", err, killErr), nil
			}
			return fmt.Errorf("attach process group: %w", err), nil
		case <-time.After(2 * time.Second):
			return fmt.Errorf("attach process group: %w; child did not stop after kill: %v", err, killErr), nil
		}
	}
	defer func() {
		if err := releaseProcessGroup(cmd); err != nil {
			cleanupErr := fmt.Errorf("release process group: %w", err)
			if control, ok := runErr.(*processControlError); ok {
				control.controlErr = fmt.Errorf("%v; %w", control.controlErr, cleanupErr)
			} else {
				runErr = &processControlError{processErr: runErr, controlErr: cleanupErr}
			}
			forwarded = nil
		}
	}()
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	var timer *time.Timer
	var deadline <-chan time.Time
	var controlErr error
	forced := false
	appendError := func(err error) {
		if err == nil {
			return
		}
		if controlErr == nil {
			controlErr = err
		} else {
			controlErr = fmt.Errorf("%v; %w", controlErr, err)
		}
	}
	defer func() {
		if timer != nil {
			timer.Stop()
		}
	}()
	for {
		select {
		case err := <-done:
			if forwarded != nil {
				// A descendant may outlive the direct child after a signal.
				appendError(killProcessGroup(cmd))
			}
			if controlErr != nil {
				return &processControlError{processErr: err, controlErr: controlErr}, nil
			}
			return err, forwarded
		case sig := <-signals:
			if forwarded == nil {
				forwarded = sig
				if signalErr := forwardProcessSignal(cmd, sig); signalErr != nil {
					appendError(fmt.Errorf("forward signal: %w", signalErr))
					appendError(killProcessGroup(cmd))
				}
				timer = time.NewTimer(2 * time.Second)
				deadline = timer.C
			} else {
				appendError(killProcessGroup(cmd))
			}
		case <-deadline:
			if forced {
				if controlErr != nil {
					return &processControlError{controlErr: fmt.Errorf("%v; child did not exit after forced stop", controlErr)}, nil
				}
				return &processControlError{controlErr: errors.New("child did not exit after forced stop")}, nil
			}
			appendError(killProcessGroup(cmd))
			forced = true
			timer.Reset(2 * time.Second)
			deadline = timer.C
		}
	}
}
