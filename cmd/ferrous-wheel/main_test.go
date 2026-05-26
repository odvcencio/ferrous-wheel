package main

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	ferrouswheel "m31labs.dev/ferrous-wheel"
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

	got, warnings, err := transpileFile(fw)
	if err != nil {
		t.Fatalf("transpileFile: %v", err)
	}
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings, got %+v", warnings)
	}
	if !strings.HasPrefix(got, generatedHeader) {
		t.Fatalf("expected generated header, got:\n%s", got)
	}
	if !strings.Contains(got, "x := 1") {
		t.Fatalf("expected transpiled let declaration, got:\n%s", got)
	}
}

func TestWriteTempProjectCreatesGoModuleAndMain(t *testing.T) {
	// Pass an empty sourcePath so writeTempProject falls into the legacy
	// /tmp branch (with a fresh fwrun go.mod). The new project-inheritance
	// branch is exercised by TestWriteTempProjectInheritsParentGoMod.
	tmpDir, cleanup, err := writeTempProject(generatedHeader+"package main\n\nfunc main() {}\n", "")
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

// TestWriteTempProjectInheritsParentGoMod verifies that when the .fw source
// lives inside a Go module, the temp project is staged under
// `<module>/.ferrous-wheel-build/fwrun-*/` and does NOT write a fresh
// go.mod. This is what lets .fw scripts depend on third-party packages that
// are already in the host project's go.mod.
func TestWriteTempProjectInheritsParentGoMod(t *testing.T) {
	parent := t.TempDir()
	if err := os.WriteFile(filepath.Join(parent, "go.mod"), []byte("module example.com/host\n\ngo 1.24.0\n"), 0644); err != nil {
		t.Fatalf("seed parent go.mod: %v", err)
	}
	scriptDir := filepath.Join(parent, "scripts")
	if err := os.MkdirAll(scriptDir, 0755); err != nil {
		t.Fatalf("mkdir scripts: %v", err)
	}
	scriptPath := filepath.Join(scriptDir, "demo.fw")
	if err := os.WriteFile(scriptPath, []byte("package main\n"), 0644); err != nil {
		t.Fatalf("seed script: %v", err)
	}

	tmpDir, cleanup, err := writeTempProject(generatedHeader+"package main\n\nfunc main() {}\n", scriptPath)
	if err != nil {
		t.Fatalf("writeTempProject: %v", err)
	}
	defer cleanup()

	if !strings.HasPrefix(tmpDir, filepath.Join(parent, ".ferrous-wheel-build")) {
		t.Fatalf("expected staged dir under parent's .ferrous-wheel-build/, got: %s", tmpDir)
	}
	if _, err := os.Stat(filepath.Join(tmpDir, "go.mod")); !os.IsNotExist(err) {
		t.Fatalf("expected NO go.mod in inherited staging dir, stat=%v", err)
	}
	mainGo, err := os.ReadFile(filepath.Join(tmpDir, "main.go"))
	if err != nil {
		t.Fatalf("read main.go: %v", err)
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

func TestEmitPrintsWarningsToStderr(t *testing.T) {
	fw := writeFWFile(t, t.TempDir(), "warn.fw", `package main

func main() {
	fallback := fn(v) v ?? "fallback"
	_ = fallback
}
`)

	stdout, stderr, err := captureOutput(t, func() error { return emit(fw) })
	if err != nil {
		t.Fatalf("emit: %v\nstderr:\n%s", err, stderr)
	}
	if !strings.Contains(stderr, "warning: line 4:") {
		t.Fatalf("expected warning location on stderr, got %q", stderr)
	}
	if !strings.Contains(stderr, "using reflection fallback") {
		t.Fatalf("expected warning message on stderr, got %q", stderr)
	}
	if !strings.Contains(stdout, "// fw:warn:") {
		t.Fatalf("expected inline warning comment in output, got:\n%s", stdout)
	}
}

func TestPrintWarningsFormatsLineAndColumn(t *testing.T) {
	var buf bytes.Buffer
	printWarnings(&buf, []ferrouswheel.Warning{{
		Line:    12,
		Col:     7,
		Message: "type of 'user' unresolved for ?. - using reflection fallback",
	}})
	if got := buf.String(); got != "warning: line 12:7: type of 'user' unresolved for ?. - using reflection fallback\n" {
		t.Fatalf("unexpected warning output %q", got)
	}
}

func TestCLIFmtToStdout(t *testing.T) {
	tmpDir := t.TempDir()
	fwFile := filepath.Join(tmpDir, "test.fw")
	os.WriteFile(fwFile, []byte("package main\n\nfunc main() {\n\tlet   x   =   1\n}\n"), 0644)

	var stderr bytes.Buffer
	code := runCLI([]string{"ferrous-wheel", "fmt", fwFile}, &stderr)
	if code != 0 {
		t.Errorf("expected exit 0, got %d: %s", code, stderr.String())
	}
}

func TestCLIFmtCheck(t *testing.T) {
	tmpDir := t.TempDir()
	fwFile := filepath.Join(tmpDir, "test.fw")
	os.WriteFile(fwFile, []byte("package main\n\nfunc main() {\n\tlet   x   =   1\n}\n"), 0644)

	var stderr bytes.Buffer
	code := runCLI([]string{"ferrous-wheel", "fmt", "--check", fwFile}, &stderr)
	if code != 1 {
		t.Errorf("expected exit 1 for unformatted file, got %d", code)
	}
}

func TestCLIFmtCheckAlreadyFormatted(t *testing.T) {
	tmpDir := t.TempDir()
	fwFile := filepath.Join(tmpDir, "test.fw")
	os.WriteFile(fwFile, []byte("package main\n\nfunc main() {\n\tlet x = 1\n}\n"), 0644)

	var stderr bytes.Buffer
	code := runCLI([]string{"ferrous-wheel", "fmt", "--check", fwFile}, &stderr)
	if code != 0 {
		t.Errorf("expected exit 0 for already formatted file, got %d", code)
	}
}

func TestCLIFmtWriteInPlace(t *testing.T) {
	tmpDir := t.TempDir()
	fwFile := filepath.Join(tmpDir, "test.fw")
	os.WriteFile(fwFile, []byte("package main\n\nfunc main() {\n\tlet   x   =   1\n}\n"), 0644)

	var stderr bytes.Buffer
	code := runCLI([]string{"ferrous-wheel", "fmt", "-w", fwFile}, &stderr)
	if code != 0 {
		t.Errorf("expected exit 0, got %d: %s", code, stderr.String())
	}

	result, _ := os.ReadFile(fwFile)
	if !strings.Contains(string(result), "let x = 1") {
		t.Errorf("expected formatted content written to file, got: %s", result)
	}
}

func TestCLILint(t *testing.T) {
	tmpDir := t.TempDir()
	fwFile := filepath.Join(tmpDir, "test.fw")
	os.WriteFile(fwFile, []byte("package main\n\nfunc main() {\n\tlet x = 1\n}\n"), 0644)

	var stderr bytes.Buffer
	code := runCLI([]string{"ferrous-wheel", "lint", fwFile}, &stderr)
	// Should have warnings (unused-let) but exit 0 (warnings don't block)
	if code != 0 {
		t.Errorf("expected exit 0 for warnings only, got %d", code)
	}
	if !strings.Contains(stderr.String(), "unused-let") {
		t.Error("expected unused-let warning in output")
	}
}

func TestCLILintError(t *testing.T) {
	tmpDir := t.TempDir()
	fwFile := filepath.Join(tmpDir, "test.fw")
	os.WriteFile(fwFile, []byte("package main\n\nfunc main() {\n\tlet x = if true { 1 }\n\t_ = x\n}\n"), 0644)

	var stderr bytes.Buffer
	code := runCLI([]string{"ferrous-wheel", "lint", fwFile}, &stderr)
	// Should exit 1 because missing-else-if-expr is a LintError
	if code != 1 {
		t.Errorf("expected exit 1 for lint errors, got %d", code)
	}
	if !strings.Contains(stderr.String(), "missing-else-if-expr") {
		t.Error("expected missing-else-if-expr error in output")
	}
}

func TestCLILintNoArgs(t *testing.T) {
	var stderr bytes.Buffer
	code := runCLI([]string{"ferrous-wheel", "lint"}, &stderr)
	if code != 1 {
		t.Errorf("expected exit 1, got %d", code)
	}
	if !strings.Contains(stderr.String(), "Usage:") {
		t.Error("expected usage message")
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
