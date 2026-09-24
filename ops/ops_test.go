package ops

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestProcessHelper(t *testing.T) {
	if os.Getenv("OPS_TEST_HELPER") != "1" {
		return
	}
	switch os.Getenv("OPS_TEST_CASE") {
	case "contract":
		args := os.Args[2:]
		if len(args) > 0 && args[0] == "--" {
			args = args[1:]
		}
		cwd, err := os.Getwd()
		if err != nil {
			os.Exit(91)
		}
		input, err := io.ReadAll(os.Stdin)
		if err != nil {
			os.Exit(92)
		}
		fmt.Fprintf(os.Stdout, "%s|%s|%s|%s", cwd, os.Getenv("OPS_VALUE"), strings.Join(args, ","), input)
		fmt.Fprint(os.Stderr, "error stream")
		os.Exit(7)
	case "echo":
		fmt.Fprint(os.Stdout, "output")
		fmt.Fprint(os.Stderr, "error")
		os.Exit(0)
	case "touch":
		if err := os.WriteFile(os.Getenv("OPS_MARKER"), []byte("ran"), 0600); err != nil {
			os.Exit(93)
		}
	case "env":
		fmt.Fprint(os.Stdout, os.Getenv("OPS_INHERITED"))
		os.Exit(0)
	case "delayed":
		if err := os.WriteFile(os.Getenv("OPS_READY"), []byte("ready"), 0600); err != nil {
			os.Exit(94)
		}
		time.Sleep(500 * time.Millisecond)
		_ = os.WriteFile(os.Getenv("OPS_MARKER"), []byte("escaped"), 0600)
	case "parent":
		child := exec.Command(os.Args[0], "-test.run=^TestProcessHelper$")
		child.Env = append(os.Environ(), "OPS_TEST_CASE=delayed")
		if err := child.Start(); err != nil {
			os.Exit(95)
		}
		for i := 0; i < 200; i++ {
			if _, err := os.Stat(os.Getenv("OPS_READY")); err == nil {
				fmt.Fprint(os.Stdout, "child ready")
				if os.Getenv("OPS_EXIT_PARENT") == "1" {
					return
				}
				_ = child.Wait()
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
		os.Exit(96)
	default:
		os.Exit(97)
	}
}

func helperSpec(caseName string) Spec {
	return Spec{
		Argv: []string{os.Args[0], "-test.run=^TestProcessHelper$"},
		Env: map[string]string{
			"OPS_TEST_HELPER": "1",
			"OPS_TEST_CASE":   caseName,
		},
	}
}

func TestRunCaptureContractAndExactExit(t *testing.T) {
	dir := t.TempDir()
	spec := helperSpec("contract")
	spec.Argv = append(spec.Argv, "--", "word with spaces", "a'b")
	spec.Cwd = dir
	spec.Env["OPS_VALUE"] = "set value"
	spec.Stdin = strings.NewReader("input data")
	result, err := Run(context.Background(), spec)
	var exit *ExitError
	if !errors.As(err, &exit) || exit.Code != 7 || result.ExitCode != 7 {
		t.Fatalf("exit: result=%+v error=%v", result, err)
	}
	var raw *exec.ExitError
	if !errors.As(err, &raw) || raw.ExitCode() != 7 {
		t.Fatalf("underlying exit status was lost: %v", err)
	}
	want := dir + "|set value|word with spaces,a'b|input data"
	if string(result.Stdout) != want || string(result.Stderr) != "error stream" {
		t.Fatalf("streams: stdout=%q stderr=%q", result.Stdout, result.Stderr)
	}
}

func TestRunStreamAndDiscard(t *testing.T) {
	var stdout, stderr bytes.Buffer
	spec := helperSpec("echo")
	spec.Output = Stream
	spec.Stdout, spec.Stderr = &stdout, &stderr
	result, err := Run(context.Background(), spec)
	if err != nil || result.ExitCode != 0 {
		t.Fatalf("stream: result=%+v error=%v", result, err)
	}
	if stdout.String() != "output" || stderr.String() != "error" || len(result.Stdout) != 0 || len(result.Stderr) != 0 {
		t.Fatalf("stream output: result=%+v stdout=%q stderr=%q", result, stdout.String(), stderr.String())
	}
	spec.Output, spec.Stdout, spec.Stderr = Discard, nil, nil
	result, err = Run(context.Background(), spec)
	if err != nil || result.ExitCode != 0 || len(result.Stdout) != 0 || len(result.Stderr) != 0 {
		t.Fatalf("discard: result=%+v error=%v", result, err)
	}
}

func TestRunClearEnvironment(t *testing.T) {
	t.Setenv("OPS_INHERITED", "parent-value")
	spec := helperSpec("env")
	result, err := Run(context.Background(), spec)
	if err != nil || string(result.Stdout) != "parent-value" {
		t.Fatalf("inherited environment: result=%+v error=%v", result, err)
	}
	spec.ClearEnv = true
	if runtime.GOOS == "windows" {
		spec.Env["SystemRoot"] = os.Getenv("SystemRoot")
	}
	result, err = Run(context.Background(), spec)
	if err != nil || len(result.Stdout) != 0 {
		t.Fatalf("cleared environment: result=%+v error=%v", result, err)
	}
}

func TestRunValidationDoesNotStartChild(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "started")
	cases := []func(*Spec){
		func(s *Spec) { s.Argv = nil },
		func(s *Spec) { s.Argv = []string{""} },
		func(s *Spec) { s.Env["BAD=KEY"] = "value" },
		func(s *Spec) { s.Timeout = -time.Second },
		func(s *Spec) { s.Output = OutputMode(99) },
		func(s *Spec) { s.Output = Capture; s.Stdout = io.Discard },
	}
	for i, change := range cases {
		spec := helperSpec("touch")
		spec.Env["OPS_MARKER"] = marker
		change(&spec)
		result, err := Run(context.Background(), spec)
		if err == nil || result.ExitCode != -1 {
			t.Fatalf("case %d: result=%+v error=%v", i, result, err)
		}
		if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("case %d started child: %v", i, err)
		}
	}
}

func TestAuditRedactsArgumentsAndOmitsOutputAndEnvironmentValues(t *testing.T) {
	var audit bytes.Buffer
	spec := helperSpec("contract")
	spec.Argv = append(spec.Argv, "--", "token=secret-value", "auto-secret")
	spec.Env["OPS_VALUE"] = "another-secret"
	spec.Env["OPS_ARG_SECRET"] = "auto-secret"
	spec.Env["secret-value-KEY"] = "hidden"
	spec.Secrets = []string{"secret-value"}
	spec.Audit = &audit
	result, err := Run(context.Background(), spec)
	if result.ExitCode != 7 || err == nil {
		t.Fatalf("result=%+v error=%v", result, err)
	}
	log := audit.String()
	if strings.Contains(log, "secret-value") || strings.Contains(log, "auto-secret") || strings.Contains(log, "another-secret") || strings.Contains(log, "error stream") || !strings.Contains(log, "[REDACTED]") {
		t.Fatalf("audit leaked data or missed redaction: %q", log)
	}
	lines := strings.Split(strings.TrimSpace(log), "\n")
	if len(lines) != 2 {
		t.Fatalf("want start and end events, got %q", log)
	}
	for _, line := range lines {
		var event map[string]any
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatalf("invalid audit JSON %q: %v", line, err)
		}
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("sink unavailable") }

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, errors.New("stdin unavailable") }

type failAfterFirstWriter struct{ writes int }

func (w *failAfterFirstWriter) Write(p []byte) (int, error) {
	w.writes++
	if w.writes > 1 {
		return 0, errors.New("end audit unavailable")
	}
	return len(p), nil
}

func TestRunDoesNotIgnoreAuditOrStreamFailure(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "started")
	spec := helperSpec("touch")
	spec.Env["OPS_MARKER"] = marker
	spec.Audit = failingWriter{}
	result, err := Run(context.Background(), spec)
	if err == nil || !strings.Contains(err.Error(), "sink unavailable") || result.ExitCode != -1 {
		t.Fatalf("audit failure: result=%+v error=%v", result, err)
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("child started after audit failure: %v", err)
	}
	spec = helperSpec("echo")
	spec.Output, spec.Stdout, spec.Stderr = Stream, failingWriter{}, io.Discard
	result, err = Run(context.Background(), spec)
	if err == nil || result.ExitCode != 0 || !strings.Contains(err.Error(), "sink unavailable") {
		t.Fatalf("stream failure: result=%+v error=%v", result, err)
	}
	spec = helperSpec("contract")
	spec.Output, spec.Stdout, spec.Stderr = Stream, failingWriter{}, io.Discard
	spec.Stdin = failingReader{}
	result, err = Run(context.Background(), spec)
	if result.ExitCode != 7 || err == nil || !strings.Contains(err.Error(), "sink unavailable") || !strings.Contains(err.Error(), "stdin unavailable") {
		t.Fatalf("combined child and I/O failure: result=%+v error=%v", result, err)
	}
	audit := &failAfterFirstWriter{}
	spec = helperSpec("echo")
	spec.Audit = audit
	result, err = Run(context.Background(), spec)
	if err == nil || result.ExitCode != 0 || audit.writes != 2 || !strings.Contains(err.Error(), "end audit unavailable") {
		t.Fatalf("end audit failure: result=%+v error=%v", result, err)
	}
}

func TestRunCancellationBeforeStart(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "started")
	spec := helperSpec("touch")
	spec.Env["OPS_MARKER"] = marker
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result, err := Run(ctx, spec)
	if !errors.Is(err, context.Canceled) || result.ExitCode != -1 || !result.Canceled {
		t.Fatalf("result=%+v error=%v", result, err)
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("canceled child started: %v", err)
	}
}

func TestRunTimeoutWithOpenStdinPipe(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	defer writer.Close()
	dir := t.TempDir()
	spec := helperSpec("delayed")
	spec.Env["OPS_READY"] = filepath.Join(dir, "ready")
	spec.Env["OPS_MARKER"] = filepath.Join(dir, "escaped")
	spec.Stdin = reader
	spec.Timeout = 75 * time.Millisecond
	finished := make(chan error, 1)
	go func() {
		_, err := Run(context.Background(), spec)
		finished <- err
	}()
	select {
	case err := <-finished:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("timeout: %v", err)
		}
	case <-time.After(500 * time.Millisecond):
		writer.Close() // Release a blocked copy before failing the test.
		t.Fatal("timeout waited for an open stdin pipe")
	}
}

func TestSSHCommandQuotesPOSIXArguments(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell contract")
	}
	marker := filepath.Join(t.TempDir(), "injected")
	args := []string{"printf", "%s|%s|%s|%s", "space value", "a'b", "$(touch " + marker + ")", "line\nbreak"}
	command, err := POSIXCommand(args)
	if err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("sh", "-c", command).Output()
	if err != nil || string(out) != "space value|a'b|$(touch "+marker+")|line\nbreak" {
		t.Fatalf("quoted command: %q, output=%q, error=%v", command, out, err)
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("shell substitution ran: %v", err)
	}
	if _, err := POSIXCommand([]string{"echo", "bad\x00arg"}); err == nil {
		t.Fatal("NUL argument accepted")
	}
}

func TestSSHSpecAndToolWrappers(t *testing.T) {
	spec, err := SSH("user@example.org", []string{"echo", "a b"})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(spec.Argv, []string{"ssh", "-o", "BatchMode=yes", "--", "user@example.org", "'echo' 'a b'"}) {
		t.Fatalf("ssh argv: %#v", spec.Argv)
	}
	if _, err := SSH("-oProxyCommand=bad", []string{"echo"}); err == nil {
		t.Fatal("option-like host accepted")
	}
	git := Git("/tmp/repo with space", "rev-parse", "HEAD")
	if git.Cwd != "/tmp/repo with space" || !reflect.DeepEqual(git.Argv, []string{"git", "rev-parse", "HEAD"}) {
		t.Fatalf("git spec: %+v", git)
	}
	gh := GH("owner/repo", "pr", "view", "7", "--json", "state", "--", "literal")
	if !reflect.DeepEqual(gh.Argv, []string{"gh", "-R", "owner/repo", "pr", "view", "7", "--json", "state", "--", "literal"}) {
		t.Fatalf("gh spec: %+v", gh)
	}
}

func TestJSONPointerAndTypedDecode(t *testing.T) {
	data := []byte(`{"items":[{"a/b":{"~key":7}}],"flag":true}`)
	raw, err := JSONPointer(data, "/items/0/a~1b/~0key")
	if err != nil || string(raw) != "7" {
		t.Fatalf("pointer: %s %v", raw, err)
	}
	value, err := DecodeJSONAt[int](data, "/items/0/a~1b/~0key")
	if err != nil || value != 7 {
		t.Fatalf("typed pointer: %d %v", value, err)
	}
	for _, pointer := range []string{"items/0", "/items/3", "/items/-", "/items/00", "/items/0/missing", "/items/0/a~2b"} {
		if _, err := JSONPointer(data, pointer); err == nil {
			t.Errorf("accepted invalid or missing pointer %q", pointer)
		}
	}
	if _, err := JSONPointer([]byte(`{} {}`), ""); err == nil {
		t.Fatal("accepted multiple JSON documents")
	}
}

func TestAppendFileReportsWriteFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.log")
	if err := AppendFile(path, []byte("first\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := AppendFile(path, []byte("second\n"), 0600); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "first\nsecond\n" {
		t.Fatalf("append: %q %v", data, err)
	}
	if runtime.GOOS == "linux" {
		full := filepath.Join(t.TempDir(), "full")
		if err := os.Symlink("/dev/full", full); err != nil {
			t.Fatal(err)
		}
		if err := AppendFile(full, []byte("one byte"), 0600); err == nil {
			t.Fatal("write failure was ignored")
		}
	}
}
