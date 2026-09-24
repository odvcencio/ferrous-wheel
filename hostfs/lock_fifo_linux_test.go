//go:build linux

package hostfs

import (
	"context"
	"path/filepath"
	"syscall"
	"testing"
)

func TestLockRejectsFIFO(t *testing.T) {
	root, path := openTestRoot(t)
	if err := syscall.Mkfifo(filepath.Join(path, "pipe.lock"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := root.Lock(context.Background(), "pipe.lock"); err == nil {
		t.Fatal("FIFO was accepted as a lock file")
	}
}
