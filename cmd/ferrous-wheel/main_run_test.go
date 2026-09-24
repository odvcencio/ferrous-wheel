//go:build !windows

package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func buildRunnerForTest(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "ferrous-wheel")
	cmd := exec.Command("go", "build", "-o", bin, ".")
	cmd.Env = append(os.Environ(), "GOWORK=off")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build runner: %v\n%s", err, output)
	}
	return bin
}

func writeRunFixture(t *testing.T, name, source string) (string, string) {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/runfixture\n\ngo 1.25.0\n"), 0644); err != nil {
		t.Fatal(err)
	}
	path := writeFWFile(t, root, name, source)
	return root, path
}

func assertNoRunStage(t *testing.T, root string) {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(root, ".ferrous-wheel-build", "fwrun-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Fatalf("run left staging directories: %v", matches)
	}
}

func TestRunExitCodeAndSingleErrorLine(t *testing.T) {
	bin := buildRunnerForTest(t)
	root, script := writeRunFixture(t, "exit.fw", `package main
import "os"
func main() { os.Exit(7) }
`)
	cmd := exec.Command(bin, "run", script)
	cmd.Env = append(os.Environ(), "GOWORK=off")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err := cmd.Run()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 7 {
		t.Fatalf("want exit 7, got %v; stderr=%q", err, stderr.String())
	}
	if got := strings.Split(strings.TrimSpace(stderr.String()), "\n"); len(got) != 1 {
		t.Fatalf("want one error line, got %q", stderr.String())
	}
	assertNoRunStage(t, root)
}

func TestRunArgumentsAndWorkingDirectory(t *testing.T) {
	bin := buildRunnerForTest(t)
	root, script := writeRunFixture(t, "args.fw", `package main
import ("fmt"; "os")
func main() {
	cwd, err := os.Getwd()
	if err != nil { panic(err) }
	fmt.Printf("%s\n%q\n", cwd, os.Args[1:])
}
`)
	caller := t.TempDir()
	cmd := exec.Command(bin, "run", script, "--", "two words", "a'b", "")
	cmd.Dir = caller
	cmd.Env = append(os.Environ(), "GOWORK=off")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("run: %v\n%s", err, output)
	}
	want := fmt.Sprintf("%s\n[%q %q %q]\n", caller, "two words", "a'b", "")
	if string(output) != want {
		t.Fatalf("want %q, got %q", want, output)
	}
	assertNoRunStage(t, root)

	target := t.TempDir()
	cmd = exec.Command(bin, "run", "--cwd", target, script, "--", "space arg")
	cmd.Dir = caller
	cmd.Env = append(os.Environ(), "GOWORK=off")
	output, err = cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("run --cwd: %v\n%s", err, output)
	}
	want = fmt.Sprintf("%s\n[%q]\n", target, "space arg")
	if string(output) != want {
		t.Fatalf("want %q, got %q", want, output)
	}
	assertNoRunStage(t, root)
}

func TestRunCompileFailureCleansStage(t *testing.T) {
	bin := buildRunnerForTest(t)
	root, script := writeRunFixture(t, "compile.fw", `package main
func main() { missingFunction() }
`)
	cmd := exec.Command(bin, "run", script)
	cmd.Env = append(os.Environ(), "GOWORK=off")
	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatal("want compile failure")
	}
	if !strings.Contains(string(output), "missingFunction") {
		t.Fatalf("want compile diagnostic, got %q", output)
	}
	assertNoRunStage(t, root)
}

func TestRunSIGINTStopsProgram(t *testing.T) {
	bin := buildRunnerForTest(t)
	root, script := writeRunFixture(t, "interrupt.fw", `package main
import ("os"; "time")
func main() {
	if err := os.WriteFile(os.Getenv("READYFILE"), []byte("ready"), 0600); err != nil { panic(err) }
	for { time.Sleep(time.Second) }
}
`)
	ready := filepath.Join(t.TempDir(), "ready")
	cmd := exec.Command(bin, "run", script)
	cmd.Env = append(os.Environ(), "GOWORK=off", "READYFILE="+ready)
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill() })
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(ready); err == nil {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if _, err := os.Stat(ready); err != nil {
		_ = cmd.Process.Kill()
		t.Fatalf("script never started: %s", output.String())
	}
	if err := cmd.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	err := cmd.Wait()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 130 {
		t.Fatalf("want exit 130, got %v; output=%q", err, output.String())
	}
	assertNoRunStage(t, root)
}

func TestRunSIGTERMStopsChildTreeAndCleansStage(t *testing.T) {
	bin := buildRunnerForTest(t)
	root, script := writeRunFixture(t, "signals.fw", `package main
import ("os"; "os/exec"; "strconv"; "time")
func main() {
	if len(os.Args) > 1 && os.Args[1] == "child" {
		for { time.Sleep(time.Second) }
	}
	child := exec.Command(os.Args[0], "child")
	if err := child.Start(); err != nil { panic(err) }
	if err := os.WriteFile(os.Getenv("READYFILE"), []byte(strconv.Itoa(child.Process.Pid)), 0600); err != nil { panic(err) }
	for { time.Sleep(time.Second) }
}
`)
	ready := filepath.Join(t.TempDir(), "ready")
	cmd := exec.Command(bin, "run", script)
	cmd.Env = append(os.Environ(), "GOWORK=off", "READYFILE="+ready)
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill() })
	deadline := time.Now().Add(30 * time.Second)
	childPID := 0
	for time.Now().Before(deadline) {
		if raw, err := os.ReadFile(ready); err == nil {
			childPID, err = strconv.Atoi(string(raw))
			if err != nil {
				t.Fatal(err)
			}
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if childPID == 0 {
		_ = cmd.Process.Kill()
		t.Fatalf("script never started: %s", output.String())
	}
	childAlive := true
	t.Cleanup(func() {
		if childAlive {
			_ = syscall.Kill(childPID, syscall.SIGKILL)
		}
	})
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	wait := make(chan error, 1)
	go func() { wait <- cmd.Wait() }()
	select {
	case err := <-wait:
		var exit *exec.ExitError
		if !errors.As(err, &exit) || exit.ExitCode() != 143 {
			t.Fatalf("want exit 143, got %v; output=%q", err, output.String())
		}
	case <-time.After(8 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatalf("runner did not stop after SIGTERM: %s", output.String())
	}
	childDeadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(childDeadline) {
		if err := syscall.Kill(childPID, 0); errors.Is(err, syscall.ESRCH) {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if err := syscall.Kill(childPID, 0); !errors.Is(err, syscall.ESRCH) {
		t.Fatalf("child process %d survived SIGTERM: %v", childPID, err)
	}
	childAlive = false
	assertNoRunStage(t, root)
}
