//go:build windows

package ops

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWindowsJobClosesOrphanedChild(t *testing.T) {
	dir := t.TempDir()
	spec := helperSpec("parent")
	spec.Env["OPS_READY"] = filepath.Join(dir, "ready")
	spec.Env["OPS_MARKER"] = filepath.Join(dir, "escaped")
	spec.Env["OPS_EXIT_PARENT"] = "1"
	result, err := Run(context.Background(), spec)
	if err != nil || result.ExitCode != 0 || !strings.Contains(string(result.Stdout), "child ready") {
		t.Fatalf("parent exit: result=%+v error=%v", result, err)
	}
	if _, err := os.Stat(spec.Env["OPS_READY"]); err != nil {
		t.Fatalf("child did not start: %v", err)
	}
	time.Sleep(600 * time.Millisecond)
	if _, err := os.Stat(spec.Env["OPS_MARKER"]); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("job left a grandchild alive: %v", err)
	}
}
