//go:build windows

package ops

import (
	"errors"
	"fmt"
	"os"
	"os/exec"

	"m31labs.dev/ferrous-wheel/internal/winjob"
)

func runProcess(cmd *exec.Cmd) error {
	winjob.Prepare(cmd)
	ready := make(chan struct{})
	var job *winjob.Job
	cmd.Cancel = func() error {
		// The child is suspended until Attach puts it in the job. A context
		// cancellation during Start or Attach must wait for that handoff.
		<-ready
		if job != nil {
			return job.Kill()
		}
		if cmd.Process != nil {
			return cmd.Process.Kill()
		}
		return os.ErrProcessDone
	}
	if err := cmd.Start(); err != nil {
		close(ready)
		return err
	}
	attached, attachErr := winjob.Attach(cmd)
	job = attached
	close(ready)
	if attachErr != nil {
		killErr := cmd.Process.Kill()
		if errors.Is(killErr, os.ErrProcessDone) {
			killErr = nil
		}
		waitErr := cmd.Wait()
		var childExit *exec.ExitError
		if errors.As(waitErr, &childExit) {
			waitErr = nil // The suspended child was killed during setup.
		}
		return errors.Join(fmt.Errorf("ops: attach Windows process job: %w", attachErr), killErr, waitErr)
	}
	waitErr := cmd.Wait()
	closeErr := job.Close()
	return errors.Join(waitErr, closeErr)
}
