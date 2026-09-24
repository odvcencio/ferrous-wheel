package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

func automationCLI(t *testing.T) string {
	t.Helper()
	if existing := os.Getenv("FW_CONTRACT_BINARY"); existing != "" {
		return existing
	}
	name := "ferrous-wheel"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	bin := filepath.Join(t.TempDir(), name)
	cmd := exec.Command("go", "build", "-o", bin, ".")
	cmd.Env = append(os.Environ(), "GOWORK=off")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v\n%s", err, out)
	}
	return bin
}

func automationScript(t *testing.T, source string) (string, string) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "module with spaces")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/automationcontract\n\ngo 1.25.0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(root, "script with spaces.fw")
	if err := os.WriteFile(script, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	return root, script
}

func automationNoStage(t *testing.T, root string) {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(root, ".ferrous-wheel-build", "fwrun-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Fatalf("run left staging directories: %v", matches)
	}
}

func TestAutomationContractExitAndStreams(t *testing.T) {
	bin := automationCLI(t)
	root, script := automationScript(t, `package main
import ("fmt"; "os")
func main() {
	fmt.Fprintln(os.Stdout, "stdout:"+os.Args[1])
	fmt.Fprintln(os.Stderr, "stderr:"+os.Args[2])
	os.Exit(7)
}
`)
	cmd := exec.Command(bin, "run", script, "--", "a b", "c'd")
	cmd.Env = append(os.Environ(), "GOWORK=off")
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 7 {
		t.Fatalf("exit = %v, want 7; stdout=%q stderr=%q", err, stdout.String(), stderr.String())
	}
	if got := stdout.String(); got != "stdout:a b\n" {
		t.Fatalf("stdout = %q", got)
	}
	lines := strings.Split(strings.TrimSpace(stderr.String()), "\n")
	if len(lines) != 2 || lines[0] != "stderr:c'd" || lines[1] != "error: program exited with status 7" {
		t.Fatalf("stderr lines = %q", lines)
	}
	automationNoStage(t, root)
}

func TestAutomationContractCompileFailureStaysOnStderr(t *testing.T) {
	bin := automationCLI(t)
	root, script := automationScript(t, `package main
func main() { unknownCall() }
`)
	cmd := exec.Command(bin, "run", script)
	cmd.Env = append(os.Environ(), "GOWORK=off")
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 1 {
		t.Fatalf("compile failure exit = %v, want 1; stderr=%q", err, stderr.String())
	}
	if stdout.Len() != 0 || !strings.Contains(stderr.String(), "unknownCall") {
		t.Fatalf("compile diagnostics: stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
	automationNoStage(t, root)
}

func TestAutomationContractWorkingDirectoryAndArguments(t *testing.T) {
	bin := automationCLI(t)
	root, script := automationScript(t, `package main
import ("fmt"; "os")
func main() {
	cwd, err := os.Getwd()
	if err != nil { panic(err) }
	fmt.Println(cwd)
	for _, arg := range os.Args[1:] { fmt.Printf("%q\n", arg) }
}
`)
	caller := filepath.Join(t.TempDir(), "caller with spaces")
	if err := os.Mkdir(caller, 0o700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "target with spaces")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	args := []string{"two words", "a'b", "", `/mnt/c/Program Files/Acme (QA)/script.ps1`, `C:\Program Files\Acme (QA)\script.ps1`}
	for _, tc := range []struct {
		name    string
		prefix  []string
		wantCwd string
	}{
		{"caller", []string{"run", script, "--"}, caller},
		{"explicit", []string{"run", "--cwd", target, script, "--"}, target},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.Command(bin, append(tc.prefix, args...)...)
			cmd.Dir = caller
			cmd.Env = append(os.Environ(), "GOWORK=off")
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("run: %v\n%s", err, out)
			}
			lines := strings.Split(strings.TrimSpace(string(out)), "\n")
			if len(lines) != len(args)+1 {
				t.Fatalf("output lines = %q", lines)
			}
			gotInfo, err := os.Stat(strings.TrimSuffix(lines[0], "\r"))
			if err != nil {
				t.Fatal(err)
			}
			wantInfo, err := os.Stat(tc.wantCwd)
			if err != nil || !os.SameFile(gotInfo, wantInfo) {
				t.Fatalf("cwd = %q, want %q: %v", lines[0], tc.wantCwd, err)
			}
			for i, want := range args {
				got, err := strconv.Unquote(strings.TrimSuffix(lines[i+1], "\r"))
				if err != nil || got != want {
					t.Fatalf("arg %d = %q, %v; want %q", i, got, err, want)
				}
			}
		})
	}
	automationNoStage(t, root)
}

func TestAutomationContractPowerShellHandoff(t *testing.T) {
	bin := automationCLI(t)
	root, script := automationScript(t, `package main
import ("os"; "os/exec")
func main() {
	cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", "$args[0]", os.Args[1], os.Args[2])
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil { panic(err) }
}
`)
	stubDir := t.TempDir()
	stubSource := filepath.Join(stubDir, "powershell_stub.go")
	stubCode := `package main
import ("encoding/json"; "os")
func main() { if err := json.NewEncoder(os.Stdout).Encode(os.Args[1:]); err != nil { panic(err) } }
`
	if err := os.WriteFile(stubSource, []byte(stubCode), 0o600); err != nil {
		t.Fatal(err)
	}
	stub := filepath.Join(stubDir, "powershell.exe")
	build := exec.Command("go", "build", "-o", stub, stubSource)
	build.Env = append(os.Environ(), "GOWORK=off")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build PowerShell stub: %v\n%s", err, out)
	}
	wslPath := `/mnt/c/Program Files/Acme (QA)/script.ps1`
	winPath := `C:\Program Files\Acme (QA)\script.ps1`
	cmd := exec.Command(bin, "run", script, "--", wslPath, winPath)
	cmd.Env = append(os.Environ(), "GOWORK=off", "PATH="+stubDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("PowerShell handoff: %v\n%s", err, out)
	}
	var got []string
	if err := json.Unmarshal(bytes.TrimSpace(out), &got); err != nil {
		t.Fatalf("decode stub output %q: %v", out, err)
	}
	want := []string{"-NoProfile", "-NonInteractive", "-Command", "$args[0]", wslPath, winPath}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("PowerShell argv = %q, want %q", got, want)
	}
	automationNoStage(t, root)
}
