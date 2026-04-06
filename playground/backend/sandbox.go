package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

var (
	unshareOnce sync.Once
	unshareOK   bool
)

// canUnshare checks once whether "unshare --net true" actually works.
func canUnshare() bool {
	unshareOnce.Do(func() {
		if _, err := exec.LookPath("unshare"); err != nil {
			return
		}
		// Probe with a trivial command.
		err := exec.Command("unshare", "--net", "true").Run()
		unshareOK = err == nil
	})
	return unshareOK
}

// SandboxResult holds the output from a sandbox execution.
type SandboxResult struct {
	Stdout string `json:"stdout"`
	Stderr string `json:"stderr"`
	Error  string `json:"error,omitempty"`
}

func runSandbox(code string) (*SandboxResult, error) {
	tmpDir, err := os.MkdirTemp("", "fw-playground-*")
	if err != nil {
		return nil, fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	goMod := "module fwplay\n\ngo 1.24.0\n"
	if err := os.WriteFile(filepath.Join(tmpDir, "go.mod"), []byte(goMod), 0644); err != nil {
		return nil, fmt.Errorf("write go.mod: %w", err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "main.go"), []byte(code), 0644); err != nil {
		return nil, fmt.Errorf("write main.go: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Build the command. Don't use CommandContext for cancellation because
	// it only kills the top-level process; "go run" spawns a child that
	// would survive. Instead we manage the process group ourselves.
	var cmd *exec.Cmd
	if canUnshare() {
		cmd = exec.Command("unshare", "--net", "go", "run", ".")
	} else {
		cmd = exec.Command("go", "run", ".")
	}
	cmd.Dir = tmpDir
	cmd.Env = append(os.Environ(), "GOPROXY=off")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start command: %w", err)
	}

	// Wait for either the process to finish or the context to expire.
	doneCh := make(chan error, 1)
	go func() {
		doneCh <- cmd.Wait()
	}()

	var runErr error
	select {
	case runErr = <-doneCh:
		// Process exited on its own.
	case <-ctx.Done():
		// Timeout: kill the entire process group.
		if cmd.Process != nil {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
		<-doneCh // Reap the process.
		result := &SandboxResult{
			Stdout: stdout.String(),
			Stderr: stderr.String(),
			Error:  "execution timed out (5s limit)",
		}
		return result, nil
	}

	result := &SandboxResult{
		Stdout: stdout.String(),
		Stderr: stderr.String(),
	}
	if runErr != nil {
		result.Error = stderr.String()
	}
	return result, nil
}
