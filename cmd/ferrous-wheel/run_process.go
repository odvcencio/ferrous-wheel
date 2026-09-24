package main

import (
	"os"
	"os/exec"
	"syscall"
	"time"
)

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
func runWithSignals(cmd *exec.Cmd, signals <-chan os.Signal) (error, os.Signal) {
	prepareProcessGroup(cmd)
	if err := cmd.Start(); err != nil {
		return err, nil
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	var forwarded os.Signal
	var timer *time.Timer
	var deadline <-chan time.Time
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
				killProcessGroup(cmd)
			}
			return err, forwarded
		case sig := <-signals:
			if forwarded == nil {
				forwarded = sig
				forwardProcessSignal(cmd, sig)
				timer = time.NewTimer(2 * time.Second)
				deadline = timer.C
			} else {
				killProcessGroup(cmd)
			}
		case <-deadline:
			killProcessGroup(cmd)
			deadline = nil
		}
	}
}
