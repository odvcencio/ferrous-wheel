package main

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func writeFWFile(t *testing.T, dir, name, source string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(source), 0644); err != nil {
		t.Fatalf("write fw file: %v", err)
	}
	return path
}

func captureOutput(t *testing.T, fn func() error) (string, string, error) {
	t.Helper()

	oldStdout := os.Stdout
	oldStderr := os.Stderr

	stdoutR, stdoutW, err := os.Pipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}
	stderrR, stderrW, err := os.Pipe()
	if err != nil {
		t.Fatalf("stderr pipe: %v", err)
	}

	os.Stdout = stdoutW
	os.Stderr = stderrW

	runErr := fn()

	if err := stdoutW.Close(); err != nil {
		t.Fatalf("close stdout writer: %v", err)
	}
	if err := stderrW.Close(); err != nil {
		t.Fatalf("close stderr writer: %v", err)
	}

	os.Stdout = oldStdout
	os.Stderr = oldStderr

	stdout, err := io.ReadAll(stdoutR)
	if err != nil {
		t.Fatalf("read stdout: %v", err)
	}
	stderr, err := io.ReadAll(stderrR)
	if err != nil {
		t.Fatalf("read stderr: %v", err)
	}

	return string(stdout), string(stderr), runErr
}

func TestTranspileFileAddsGeneratedHeader(t *testing.T) {
	fw := writeFWFile(t, t.TempDir(), "hello.fw", `package main

func main() {
	let x = 1
	_ = x
}
`)

	got, err := transpileFile(fw)
	if err != nil {
		t.Fatalf("transpileFile: %v", err)
	}
	if !strings.HasPrefix(got, generatedHeader) {
		t.Fatalf("expected generated header, got:\n%s", got)
	}
	if !strings.Contains(got, "x := 1") {
		t.Fatalf("expected transpiled let declaration, got:\n%s", got)
	}
}

func TestWriteTempProjectCreatesGoModuleAndMain(t *testing.T) {
	tmpDir, cleanup, err := writeTempProject(generatedHeader + "package main\n\nfunc main() {}\n")
	if err != nil {
		t.Fatalf("writeTempProject: %v", err)
	}
	defer cleanup()

	goMod, err := os.ReadFile(filepath.Join(tmpDir, "go.mod"))
	if err != nil {
		t.Fatalf("read go.mod: %v", err)
	}
	mainGo, err := os.ReadFile(filepath.Join(tmpDir, "main.go"))
	if err != nil {
		t.Fatalf("read main.go: %v", err)
	}

	if !strings.Contains(string(goMod), "module fwrun") {
		t.Fatalf("expected temp module, got:\n%s", goMod)
	}
	if !strings.Contains(string(mainGo), "func main() {}") {
		t.Fatalf("expected main.go contents, got:\n%s", mainGo)
	}
}

func TestRunExecutesTranspiledProgram(t *testing.T) {
	fw := writeFWFile(t, t.TempDir(), "hello.fw", `package main

import "fmt"

func main() {
	fmt.Println("hello from run")
}
`)

	stdout, stderr, err := captureOutput(t, func() error { return run(fw) })
	if err != nil {
		t.Fatalf("run: %v\nstderr:\n%s", err, stderr)
	}
	if strings.TrimSpace(stdout) != "hello from run" {
		t.Fatalf("expected program output, got stdout=%q stderr=%q", stdout, stderr)
	}
}

func TestBuildCreatesRunnableBinary(t *testing.T) {
	tmpDir := t.TempDir()
	fw := writeFWFile(t, tmpDir, "hello.fw", `package main

import "fmt"

func main() {
	fmt.Println("hello from build")
}
`)
	outputPath := filepath.Join(tmpDir, "hello-bin")

	_, stderr, err := captureOutput(t, func() error { return build(fw, outputPath) })
	if err != nil {
		t.Fatalf("build: %v\nstderr:\n%s", err, stderr)
	}
	if !strings.Contains(stderr, "hello.fw -> hello-bin") {
		t.Fatalf("expected build status line, got stderr=%q", stderr)
	}

	cmd := exec.Command(outputPath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("run built binary: %v\n%s", err, out)
	}
	if strings.TrimSpace(string(out)) != "hello from build" {
		t.Fatalf("expected built binary output, got %q", out)
	}
}

func TestBuildCreatesMissingOutputDirectory(t *testing.T) {
	tmpDir := t.TempDir()
	fw := writeFWFile(t, tmpDir, "hello.fw", `package main

import "fmt"

func main() {
	fmt.Println("hello from nested build")
}
`)
	outputPath := filepath.Join(tmpDir, "nested", "bin", "hello-bin")

	_, stderr, err := captureOutput(t, func() error { return build(fw, outputPath) })
	if err != nil {
		t.Fatalf("build: %v\nstderr:\n%s", err, stderr)
	}

	cmd := exec.Command(outputPath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("run built binary: %v\n%s", err, out)
	}
	if strings.TrimSpace(string(out)) != "hello from nested build" {
		t.Fatalf("expected built binary output, got %q", out)
	}
}

func TestEmitPrintsGeneratedGo(t *testing.T) {
	fw := writeFWFile(t, t.TempDir(), "hello.fw", `package main

func main() {
	let x = 42
	_ = x
}
`)

	stdout, stderr, err := captureOutput(t, func() error { return emit(fw) })
	if err != nil {
		t.Fatalf("emit: %v\nstderr:\n%s", err, stderr)
	}
	if !strings.HasPrefix(stdout, generatedHeader) {
		t.Fatalf("expected generated header, got:\n%s", stdout)
	}
	if !strings.Contains(stdout, "x := 42") {
		t.Fatalf("expected transpiled code, got:\n%s", stdout)
	}
}

func TestRunCLIErrors(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		wantCode   int
		wantStderr string
	}{
		{
			name:       "missing command",
			args:       []string{"ferrous-wheel"},
			wantCode:   1,
			wantStderr: "Usage: ferrous-wheel <command> <file.fw>",
		},
		{
			name:       "unknown command",
			args:       []string{"ferrous-wheel", "nope"},
			wantCode:   1,
			wantStderr: "Unknown command: nope",
		},
		{
			name:       "missing run file",
			args:       []string{"ferrous-wheel", "run"},
			wantCode:   1,
			wantStderr: "Usage: ferrous-wheel run <file.fw>",
		},
		{
			name:       "extra run arg",
			args:       []string{"ferrous-wheel", "run", "main.fw", "extra"},
			wantCode:   1,
			wantStderr: "Usage: ferrous-wheel run <file.fw>",
		},
		{
			name:       "missing build file",
			args:       []string{"ferrous-wheel", "build"},
			wantCode:   1,
			wantStderr: "Usage: ferrous-wheel build <file.fw> [-o output]",
		},
		{
			name:       "missing build output path",
			args:       []string{"ferrous-wheel", "build", "main.fw", "-o"},
			wantCode:   1,
			wantStderr: "missing output path after -o",
		},
		{
			name:       "duplicate build output flag",
			args:       []string{"ferrous-wheel", "build", "main.fw", "-o", "one", "-o", "two"},
			wantCode:   1,
			wantStderr: "duplicate -o flag",
		},
		{
			name:       "unexpected build arg",
			args:       []string{"ferrous-wheel", "build", "main.fw", "extra"},
			wantCode:   1,
			wantStderr: "unexpected build argument: extra",
		},
		{
			name:       "missing emit file",
			args:       []string{"ferrous-wheel", "emit"},
			wantCode:   1,
			wantStderr: "Usage: ferrous-wheel emit <file.fw>",
		},
		{
			name:       "extra emit arg",
			args:       []string{"ferrous-wheel", "emit", "main.fw", "extra"},
			wantCode:   1,
			wantStderr: "Usage: ferrous-wheel emit <file.fw>",
		},
		{
			name:       "runtime error",
			args:       []string{"ferrous-wheel", "emit", "/does/not/exist.fw"},
			wantCode:   1,
			wantStderr: "error:",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stderr bytes.Buffer
			gotCode := runCLI(tt.args, &stderr)
			if gotCode != tt.wantCode {
				t.Fatalf("expected exit code %d, got %d", tt.wantCode, gotCode)
			}
			if !strings.Contains(stderr.String(), tt.wantStderr) {
				t.Fatalf("expected stderr to contain %q, got %q", tt.wantStderr, stderr.String())
			}
		})
	}
}
