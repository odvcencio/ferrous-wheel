package ops

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRunCancellationKillsGrandchild(t *testing.T) {
	dir := t.TempDir()
	ready := filepath.Join(dir, "ready")
	marker := filepath.Join(dir, "escaped")
	spec := helperSpec("parent")
	spec.Env["OPS_READY"], spec.Env["OPS_MARKER"] = ready, marker
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	type outcome struct {
		result Result
		err    error
	}
	finished := make(chan outcome, 1)
	go func() {
		result, err := Run(ctx, spec)
		finished <- outcome{result, err}
	}()
	deadline := time.After(5 * time.Second)
	for {
		if _, err := os.Stat(ready); err == nil {
			break
		}
		select {
		case done := <-finished:
			t.Fatalf("child exited before ready: result=%+v error=%v", done.result, done.err)
		case <-deadline:
			t.Fatal("child did not become ready")
		case <-time.After(5 * time.Millisecond):
		}
	}
	cancel()
	select {
	case done := <-finished:
		if !errors.Is(done.err, context.Canceled) || !done.result.Canceled || done.result.ExitCode == 0 {
			t.Fatalf("cancel: result=%+v error=%v", done.result, done.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after cancellation")
	}
	time.Sleep(600 * time.Millisecond)
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("grandchild survived cancellation: %v", err)
	}
}

func TestRunTimeoutKillsProcess(t *testing.T) {
	dir := t.TempDir()
	spec := helperSpec("delayed")
	spec.Env["OPS_READY"] = filepath.Join(dir, "ready")
	spec.Env["OPS_MARKER"] = filepath.Join(dir, "escaped")
	spec.Timeout = 200 * time.Millisecond
	result, err := Run(context.Background(), spec)
	if !errors.Is(err, context.DeadlineExceeded) || !result.TimedOut || result.ExitCode == 0 {
		t.Fatalf("timeout: result=%+v error=%v", result, err)
	}
	if _, err := os.Stat(spec.Env["OPS_READY"]); err != nil {
		t.Fatalf("process did not start: %v", err)
	}
	time.Sleep(500 * time.Millisecond)
	if _, err := os.Stat(spec.Env["OPS_MARKER"]); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("timed-out process wrote marker: %v", err)
	}
	if strings.Contains(err.Error(), "ops: nil context") {
		t.Fatalf("wrong timeout error: %v", err)
	}
}
