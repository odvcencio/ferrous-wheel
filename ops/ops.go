// Package ops provides a small process API for compiled Ferrous Wheel programs.
// It uses argument arrays, not a local shell. Captured output belongs to the
// caller; audit records never contain process output or environment values.
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
	"sort"
	"strings"
	"time"
)

// OutputMode selects how stdout and stderr are handled. The zero value captures
// both streams separately. Stream sends them to the specified writers, or to
// os.Stdout and os.Stderr if the writers are nil. Discard drops both streams.
type OutputMode uint8

const (
	Capture OutputMode = iota
	Stream
	Discard
)

// Spec describes one process. Env overrides the inherited environment unless
// ClearEnv is set. An empty Env value sets an empty variable. Stdin is empty
// unless set explicitly. A bare Argv[0] is found using the caller's PATH before
// Env is applied; use an absolute executable path when Env changes PATH.
// Timeout zero means no extra deadline. The context may still have a deadline.
// Audit receives one JSON line before start and one after the process ends.
// Secrets and nonempty Env values replace literal matches in audit fields.
// Output streams are never copied into Audit. Set Output to Stream to send
// output to writers.
type Spec struct {
	Argv     []string
	Cwd      string
	Env      map[string]string
	ClearEnv bool
	Stdin    io.Reader
	Output   OutputMode
	Stdout   io.Writer
	Stderr   io.Writer
	Timeout  time.Duration
	Audit    io.Writer
	Secrets  []string
}

// Result reports the child's process status. ExitCode is -1 if the process did
// not start or ended by a signal. TimedOut and Canceled report why cancellation
// was requested. Captured output is present only in Capture mode.
type Result struct {
	ExitCode int
	Stdout   []byte
	Stderr   []byte
	TimedOut bool
	Canceled bool
}

// ExitError reports a nonzero child exit. Code is the child's exact numeric
// exit code, or -1 when the operating system reports signal termination.
type ExitError struct {
	Code  int
	Cause error
}

func (e *ExitError) Error() string {
	if e.Code >= 0 {
		message := fmt.Sprintf("process exited with code %d", e.Code)
		if e.Cause == nil {
			return message
		}
		if _, onlyExit := e.Cause.(*exec.ExitError); !onlyExit {
			return message + ": " + e.Cause.Error()
		}
		return message
	}
	if e.Cause == nil {
		return "process terminated"
	}
	return "process terminated: " + e.Cause.Error()
}

func (e *ExitError) Unwrap() error { return e.Cause }

type auditEvent struct {
	Phase    string   `json:"phase"`
	Argv     []string `json:"argv,omitempty"`
	Cwd      string   `json:"cwd,omitempty"`
	EnvKeys  []string `json:"envKeys,omitempty"`
	ExitCode *int     `json:"exitCode,omitempty"`
	TimedOut bool     `json:"timedOut,omitempty"`
	Canceled bool     `json:"canceled,omitempty"`
	Error    string   `json:"error,omitempty"`
}

// Run starts the specified process and waits for it. A nonzero child exit
// returns both Result.ExitCode and an *ExitError. A failed start keeps the exit
// code at -1. Cancellation kills the process group on Unix and the process tree
// on Windows. Audit write failure before start prevents the child from starting;
// audit write failure after exit is joined with the process error.
func Run(ctx context.Context, spec Spec) (Result, error) {
	result := Result{ExitCode: -1}
	if ctx == nil {
		return result, errors.New("ops: nil context")
	}
	if err := validate(spec); err != nil {
		return result, err
	}
	if err := ctx.Err(); err != nil {
		result.Canceled = errors.Is(err, context.Canceled)
		result.TimedOut = errors.Is(err, context.DeadlineExceeded)
		return result, err
	}

	runCtx := ctx
	cancel := func() {}
	if spec.Timeout > 0 {
		runCtx, cancel = context.WithTimeout(ctx, spec.Timeout)
	}
	defer cancel()

	cmd := exec.CommandContext(runCtx, spec.Argv[0], spec.Argv[1:]...)
	cmd.Dir = spec.Cwd
	var processIO ioFailures
	if spec.Stdin != nil {
		if file, ok := spec.Stdin.(*os.File); ok {
			// Passing the descriptor directly avoids a copy goroutine that can
			// block on an open terminal or pipe after context cancellation.
			cmd.Stdin = file
		} else {
			cmd.Stdin = checkedReader{reader: spec.Stdin, failures: &processIO}
		}
	}
	cmd.WaitDelay = 2 * time.Second
	if spec.ClearEnv || len(spec.Env) > 0 {
		var inherited []string
		if !spec.ClearEnv {
			inherited = os.Environ()
		}
		cmd.Env = make([]string, 0, len(inherited)+len(spec.Env))
		cmd.Env = append(cmd.Env, inherited...)
		keys := make([]string, 0, len(spec.Env))
		for key := range spec.Env {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			cmd.Env = append(cmd.Env, key+"="+spec.Env[key])
		}
	}
	var stdout, stderr bytes.Buffer
	switch spec.Output {
	case Capture:
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
	case Stream:
		cmd.Stdout, cmd.Stderr = spec.Stdout, spec.Stderr
		if cmd.Stdout == nil {
			cmd.Stdout = os.Stdout
		}
		if cmd.Stderr == nil {
			cmd.Stderr = os.Stderr
		}
		cmd.Stdout = checkedWriter{writer: cmd.Stdout, failures: &processIO, writeMu: &processIO.writeMu}
		cmd.Stderr = checkedWriter{writer: cmd.Stderr, failures: &processIO, writeMu: &processIO.writeMu}
	case Discard:
		cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
	}

	secrets := auditSecrets(spec)
	if err := writeAudit(spec.Audit, auditEvent{
		Phase:   "start",
		Argv:    redactArgs(spec.Argv, secrets),
		Cwd:     redact(spec.Cwd, secrets),
		EnvKeys: envKeys(spec.Env, secrets),
	}); err != nil {
		return result, fmt.Errorf("ops: write start audit: %w", err)
	}

	err := runProcess(cmd)
	if cmd.ProcessState != nil {
		result.ExitCode = cmd.ProcessState.ExitCode()
	}
	if spec.Output == Capture {
		result.Stdout, result.Stderr = stdout.Bytes(), stderr.Bytes()
	}
	if runCtx.Err() != nil && err != nil {
		result.TimedOut = errors.Is(runCtx.Err(), context.DeadlineExceeded)
		result.Canceled = errors.Is(runCtx.Err(), context.Canceled)
	}
	var childExit *exec.ExitError
	if errors.As(err, &childExit) {
		err = &ExitError{Code: result.ExitCode, Cause: err}
	}
	for _, ioErr := range processIO.errors() {
		if !errors.Is(err, ioErr) {
			err = errors.Join(err, ioErr)
		}
	}
	if (result.TimedOut || result.Canceled) && err != nil {
		err = errors.Join(err, runCtx.Err())
	}
	message := ""
	if err != nil {
		message = redact(err.Error(), secrets)
	}
	auditErr := writeAudit(spec.Audit, auditEvent{
		Phase:    "end",
		ExitCode: &result.ExitCode,
		TimedOut: result.TimedOut,
		Canceled: result.Canceled,
		Error:    message,
	})
	if auditErr != nil {
		err = errors.Join(err, fmt.Errorf("ops: write end audit: %w", auditErr))
	}
	return result, err
}

func validate(spec Spec) error {
	if len(spec.Argv) == 0 || spec.Argv[0] == "" {
		return errors.New("ops: argv must start with a program")
	}
	for _, arg := range spec.Argv {
		if strings.ContainsRune(arg, 0) {
			return errors.New("ops: argv contains NUL")
		}
	}
	if strings.ContainsRune(spec.Cwd, 0) {
		return errors.New("ops: cwd contains NUL")
	}
	if spec.Timeout < 0 {
		return errors.New("ops: timeout must not be negative")
	}
	if spec.Output > Discard {
		return errors.New("ops: invalid output mode")
	}
	if spec.Output != Stream && (spec.Stdout != nil || spec.Stderr != nil) {
		return errors.New("ops: stdout and stderr writers require Stream mode")
	}
	for key, value := range spec.Env {
		if key == "" || strings.ContainsAny(key, "=\x00") || strings.ContainsRune(value, 0) {
			return fmt.Errorf("ops: invalid environment key %q", key)
		}
	}
	for _, secret := range spec.Secrets {
		if secret == "" {
			return errors.New("ops: secret must not be empty")
		}
	}
	return nil
}

func envKeys(env map[string]string, secrets []string) []string {
	if len(env) == 0 {
		return nil
	}
	keys := make([]string, 0, len(env))
	for key := range env {
		keys = append(keys, redact(key, secrets))
	}
	sort.Strings(keys)
	return keys
}

func auditSecrets(spec Spec) []string {
	secrets := append([]string(nil), spec.Secrets...)
	for _, value := range spec.Env {
		if value != "" {
			secrets = append(secrets, value)
		}
	}
	return secrets
}

func redactArgs(args, secrets []string) []string {
	redacted := make([]string, len(args))
	for i, arg := range args {
		redacted[i] = redact(arg, secrets)
	}
	return redacted
}

func redact(s string, secrets []string) string {
	ordered := append([]string(nil), secrets...)
	sort.Slice(ordered, func(i, j int) bool { return len(ordered[i]) > len(ordered[j]) })
	for _, secret := range ordered {
		s = strings.ReplaceAll(s, secret, "[REDACTED]")
	}
	return s
}

func writeAudit(dst io.Writer, event auditEvent) error {
	if dst == nil {
		return nil
	}
	line, err := json.Marshal(event)
	if err != nil {
		return err
	}
	line = append(line, '\n')
	n, err := dst.Write(line)
	if err != nil {
		return err
	}
	if n != len(line) {
		return io.ErrShortWrite
	}
	return nil
}
