//go:build windows

package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func TestWindowsRunStopsDescendants(t *testing.T) {
	switch os.Getenv("FW_WINJOB_TEST_MODE") {
	case "grandchild":
		for {
			time.Sleep(time.Second)
		}
	case "child", "child-exit":
		mode := os.Getenv("FW_WINJOB_TEST_MODE")
		grandchild := exec.Command(os.Args[0], "-test.run=^TestWindowsRunStopsDescendants$")
		grandchild.Env = append(os.Environ(), "FW_WINJOB_TEST_MODE=grandchild")
		if err := grandchild.Start(); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(os.Getenv("FW_WINJOB_TEST_PIDFILE"), []byte(strconv.Itoa(grandchild.Process.Pid)), 0600); err != nil {
			t.Fatal(err)
		}
		if mode == "child-exit" {
			deadline := time.Now().Add(10 * time.Second)
			for time.Now().Before(deadline) {
				if _, err := os.Stat(os.Getenv("FW_WINJOB_TEST_RELEASEFILE")); err == nil {
					return
				}
				time.Sleep(20 * time.Millisecond)
			}
			t.Fatal("release file did not appear")
		}
		for {
			time.Sleep(time.Second)
		}
	}

	pidFile := filepath.Join(t.TempDir(), "grandchild.pid")
	cmd := exec.Command(os.Args[0], "-test.run=^TestWindowsRunStopsDescendants$")
	cmd.Env = append(os.Environ(), "FW_WINJOB_TEST_MODE=child", "FW_WINJOB_TEST_PIDFILE="+pidFile)
	signals := make(chan os.Signal, 1)
	type result struct {
		err error
		sig os.Signal
	}
	done := make(chan result, 1)
	go func() {
		err, sig := runWithSignals(cmd, signals)
		done <- result{err: err, sig: sig}
	}()
	t.Cleanup(func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
	})

	var grandchildPID int
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if raw, err := os.ReadFile(pidFile); err == nil {
			grandchildPID, err = strconv.Atoi(string(raw))
			if err != nil {
				t.Fatal(err)
			}
			break
		}
		select {
		case outcome := <-done:
			t.Fatalf("runner stopped before fixture was ready: %v", outcome.err)
		default:
		}
		time.Sleep(20 * time.Millisecond)
	}
	if grandchildPID == 0 {
		t.Fatal("grandchild did not start")
	}
	grandchild, err := windows.OpenProcess(windows.SYNCHRONIZE|windows.PROCESS_TERMINATE, false, uint32(grandchildPID))
	if err != nil {
		t.Fatalf("open grandchild %d: %v", grandchildPID, err)
	}
	t.Cleanup(func() {
		_ = windows.TerminateProcess(grandchild, 1)
		_ = windows.CloseHandle(grandchild)
	})

	signals <- syscall.SIGTERM
	select {
	case outcome := <-done:
		if outcome.sig != syscall.SIGTERM {
			t.Fatalf("forwarded signal = %v, want SIGTERM", outcome.sig)
		}
		var exit *exec.ExitError
		if !errors.As(outcome.err, &exit) {
			t.Fatalf("child result = %v, want exit status", outcome.err)
		}
	case <-time.After(8 * time.Second):
		t.Fatal("runner did not stop after SIGTERM")
	}
	state, err := windows.WaitForSingleObject(grandchild, 3000)
	if err != nil || state != windows.WAIT_OBJECT_0 {
		t.Fatal(fmt.Errorf("grandchild %d survived runner stop: wait state %d, error %v", grandchildPID, state, err))
	}
}

func TestWindowsRunCleansDescendantsAfterParentExit(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "grandchild.pid")
	releaseFile := filepath.Join(t.TempDir(), "release")
	cmd := exec.Command(os.Args[0], "-test.run=^TestWindowsRunStopsDescendants$")
	cmd.Env = append(os.Environ(),
		"FW_WINJOB_TEST_MODE=child-exit",
		"FW_WINJOB_TEST_PIDFILE="+pidFile,
		"FW_WINJOB_TEST_RELEASEFILE="+releaseFile,
	)
	t.Cleanup(func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
	})
	type result struct {
		err error
		sig os.Signal
	}
	done := make(chan result, 1)
	go func() {
		err, sig := runWithSignals(cmd, make(chan os.Signal))
		done <- result{err: err, sig: sig}
	}()

	var grandchildPID int
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if raw, err := os.ReadFile(pidFile); err == nil {
			grandchildPID, err = strconv.Atoi(string(raw))
			if err != nil {
				t.Fatal(err)
			}
			break
		}
		select {
		case outcome := <-done:
			t.Fatalf("runner stopped before fixture was ready: %v", outcome.err)
		default:
		}
		time.Sleep(20 * time.Millisecond)
	}
	if grandchildPID == 0 {
		t.Fatal("grandchild did not start")
	}
	grandchild, err := windows.OpenProcess(windows.SYNCHRONIZE|windows.PROCESS_TERMINATE, false, uint32(grandchildPID))
	if err != nil {
		t.Fatalf("open grandchild %d: %v", grandchildPID, err)
	}
	t.Cleanup(func() {
		_ = windows.TerminateProcess(grandchild, 1)
		_ = windows.CloseHandle(grandchild)
	})
	if err := os.WriteFile(releaseFile, nil, 0600); err != nil {
		t.Fatal(err)
	}
	select {
	case outcome := <-done:
		if outcome.err != nil || outcome.sig != nil {
			t.Fatalf("normal exit: err=%v, signal=%v", outcome.err, outcome.sig)
		}
	case <-time.After(8 * time.Second):
		t.Fatal("runner did not stop after child exit")
	}
	state, err := windows.WaitForSingleObject(grandchild, 3000)
	if err != nil || state != windows.WAIT_OBJECT_0 {
		t.Fatalf("grandchild %d survived normal exit: wait state %d, error %v", grandchildPID, state, err)
	}
}
