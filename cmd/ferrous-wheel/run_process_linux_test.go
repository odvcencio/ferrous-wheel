//go:build linux

package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"
)

func TestLinuxRunCleansDescendantsAfterParentExit(t *testing.T) {
	switch os.Getenv("FW_GROUP_TEST_MODE") {
	case "grandchild":
		for {
			time.Sleep(time.Second)
		}
	case "child":
		grandchild := exec.Command(os.Args[0], "-test.run=^TestLinuxRunCleansDescendantsAfterParentExit$")
		grandchild.Env = append(os.Environ(), "FW_GROUP_TEST_MODE=grandchild")
		if err := grandchild.Start(); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(os.Getenv("FW_GROUP_TEST_PIDFILE"), []byte(strconv.Itoa(grandchild.Process.Pid)), 0600); err != nil {
			t.Fatal(err)
		}
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			if _, err := os.Stat(os.Getenv("FW_GROUP_TEST_RELEASEFILE")); err == nil {
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatal("release file did not appear")
	}

	pidFile := filepath.Join(t.TempDir(), "grandchild.pid")
	releaseFile := filepath.Join(t.TempDir(), "release")
	cmd := exec.Command(os.Args[0], "-test.run=^TestLinuxRunCleansDescendantsAfterParentExit$")
	cmd.Env = append(os.Environ(),
		"FW_GROUP_TEST_MODE=child",
		"FW_GROUP_TEST_PIDFILE="+pidFile,
		"FW_GROUP_TEST_RELEASEFILE="+releaseFile,
	)
	t.Cleanup(func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
	})
	done := make(chan error, 1)
	go func() {
		err, _ := runWithSignals(cmd, make(chan os.Signal))
		done <- err
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
		case err := <-done:
			t.Fatalf("runner stopped before fixture was ready: %v", err)
		default:
		}
		time.Sleep(20 * time.Millisecond)
	}
	if grandchildPID == 0 {
		t.Fatal("grandchild did not start")
	}
	grandchildAlive := true
	t.Cleanup(func() {
		if grandchildAlive {
			_ = syscall.Kill(grandchildPID, syscall.SIGKILL)
		}
	})
	if err := os.WriteFile(releaseFile, nil, 0600); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("normal exit: %v", err)
		}
	case <-time.After(8 * time.Second):
		t.Fatal("runner did not stop after child exit")
	}
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if !linuxProcessRunning(grandchildPID) {
			grandchildAlive = false
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("grandchild %d survived normal parent exit", grandchildPID)
}

func linuxProcessRunning(pid int) bool {
	stat, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if errors.Is(err, os.ErrNotExist) {
		return false
	}
	if err != nil {
		return true
	}
	end := bytes.LastIndexByte(stat, ')')
	if end < 0 || end+2 >= len(stat) {
		return true
	}
	return stat[end+2] != 'Z' && stat[end+2] != 'X'
}
