//go:build linux || darwin

package prototypes

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func buildOperationsPort(t *testing.T, name string) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), strings.TrimSuffix(name, ".fw"))
	cmd := exec.Command("go", "run", "../cmd/ferrous-wheel", "build", name, "-o", bin)
	cmd.Env = append(os.Environ(), "GOWORK=off")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build %s: %v\n%s", name, err, out)
	}
	return bin
}

func writePortStub(t *testing.T, dir, name, source string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"+source), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestCodexLanePortPreservesExitAndWritesMetadata(t *testing.T) {
	bin := buildOperationsPort(t, "codex-lane.fw")
	stubDir := t.TempDir()
	argsFile := filepath.Join(t.TempDir(), "argv")
	writePortStub(t, stubDir, "codex", `printf '%s\n' "$@" > "$FW_PORT_ARGS"
exit 17
`)
	state := t.TempDir()
	worktree := t.TempDir()
	brief := filepath.Join(t.TempDir(), "brief")
	if err := os.WriteFile(brief, []byte("brief with spaces"), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bin, "lane1", "model1", "high", worktree, brief, "1")
	cmd.Env = append(os.Environ(),
		"PATH="+stubDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"FW_CODEX_LANE_STATE_DIR="+state, "FW_PORT_ARGS="+argsFile,
	)
	out, err := cmd.CombinedOutput()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 17 {
		t.Fatalf("port exit = %v, want 17; output=%s", err, out)
	}
	metadata, err := os.ReadFile(filepath.Join(state, "lane1.meta"))
	if err != nil || !strings.Contains(string(metadata), "rc=17\n") {
		t.Fatalf("metadata = %q, %v", metadata, err)
	}
	argv, err := os.ReadFile(argsFile)
	if err != nil || !strings.HasSuffix(string(argv), "brief with spaces\n") {
		t.Fatalf("child arguments = %q, %v", argv, err)
	}
}

func TestFinishPRPortDryRunOnlyReadsState(t *testing.T) {
	bin := buildOperationsPort(t, "finish-pr.fw")
	stubDir := t.TempDir()
	callsFile := filepath.Join(t.TempDir(), "calls")
	writePortStub(t, stubDir, "gh", `printf '%s\n' "$*" >> "$FW_PORT_CALLS"
case "$*" in
  *'pr view'*) printf '{"state":"OPEN","mergeStateStatus":"CLEAN"}' ;;
  *) exit 99 ;;
esac
`)
	logDir := t.TempDir()
	cmd := exec.Command(bin, "--dry-run", "repo", "123", t.TempDir())
	cmd.Env = append(os.Environ(),
		"PATH="+stubDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"FW_FINISH_PR_LOG_DIR="+logDir, "FW_PORT_CALLS="+callsFile,
	)
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "dry-run: would check CI") {
		t.Fatalf("dry run = %v; output=%s", err, out)
	}
	calls, err := os.ReadFile(callsFile)
	if err != nil || !strings.Contains(string(calls), "pr view 123") || strings.Count(strings.TrimSpace(string(calls)), "\n") != 0 {
		t.Fatalf("dry-run calls = %q, %v", calls, err)
	}
}
