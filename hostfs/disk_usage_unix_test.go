//go:build linux || darwin

package hostfs

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

type swapRootContext struct {
	context.Context
	calls   int
	swapped bool
	swap    func() error
	err     error
}

func (c *swapRootContext) Err() error {
	c.calls++
	if c.calls == 3 {
		c.err = c.swap()
		c.swapped = true
	}
	if c.err != nil {
		return c.err
	}
	return c.Context.Err()
}

func allocatedBlocks(t *testing.T, path string) int64 {
	t.Helper()
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		t.Fatalf("no Unix stat data for %s", path)
	}
	return stat.Blocks * 512
}

func TestAllocatedBytesUsesOpenedRootDuringPathSwap(t *testing.T) {
	parent := t.TempDir()
	work := filepath.Join(parent, "work")
	moved := filepath.Join(parent, "moved")
	outside := filepath.Join(parent, "outside")
	for _, path := range []string{filepath.Join(work, "candidate"), filepath.Join(outside, "candidate")} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	insideFile := filepath.Join(work, "candidate", "small")
	if err := os.WriteFile(insideFile, []byte("inside"), 0o600); err != nil {
		t.Fatal(err)
	}
	outsideFile := filepath.Join(outside, "candidate", "large")
	if err := os.WriteFile(outsideFile, make([]byte, 2<<20), 0o600); err != nil {
		t.Fatal(err)
	}
	want := allocatedBlocks(t, filepath.Join(work, "candidate")) + allocatedBlocks(t, insideFile)
	root, err := Open(work)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	ctx := &swapRootContext{Context: context.Background(), swap: func() error {
		if err := os.Rename(work, moved); err != nil {
			return err
		}
		return os.Symlink(outside, work)
	}}
	got, err := root.AllocatedBytes(ctx, "candidate", Limits{MaxDepth: 4, MaxEntries: 100})
	if err != nil || !ctx.swapped {
		t.Fatalf("size after root swap = %d, %v; swapped=%v", got, err, ctx.swapped)
	}
	if got != want {
		t.Fatalf("opened-root bytes = %d, want %d; outside path now has %d", got, want, allocatedBlocks(t, outsideFile))
	}
	if _, err := os.Stat(filepath.Join(outside, "candidate", "large")); err != nil {
		t.Fatalf("outside sentinel changed: %v", err)
	}
}

func TestAllocatedBytesBoundsAndHardLinks(t *testing.T) {
	root, path := openTestRoot(t)
	if err := os.MkdirAll(filepath.Join(path, "candidate", "deep"), 0o700); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(path, "candidate", "data")
	if err := os.WriteFile(file, make([]byte, 4096), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(file, filepath.Join(path, "candidate", "data-link")); err != nil {
		t.Fatal(err)
	}
	if _, err := root.AllocatedBytes(context.Background(), "candidate", Limits{MaxDepth: 1, MaxEntries: 20}); !errors.Is(err, ErrLimit) {
		t.Fatalf("depth bound error = %v", err)
	}
	if _, err := root.AllocatedBytes(context.Background(), "candidate", Limits{MaxDepth: 4, MaxEntries: 2}); !errors.Is(err, ErrLimit) {
		t.Fatalf("entry bound error = %v", err)
	}
	got, err := root.AllocatedBytes(context.Background(), "candidate", Limits{MaxDepth: 4, MaxEntries: 20})
	if err != nil {
		t.Fatal(err)
	}
	want := allocatedBlocks(t, filepath.Join(path, "candidate")) + allocatedBlocks(t, filepath.Join(path, "candidate", "deep")) + allocatedBlocks(t, file)
	if got != want {
		t.Fatalf("hard-linked bytes = %d, want %d", got, want)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := root.AllocatedBytes(ctx, "candidate", Limits{MaxDepth: 4, MaxEntries: 20}); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled size error = %v", err)
	}
	if _, err := root.AllocatedBytes(context.Background(), "../outside", Limits{MaxDepth: 4, MaxEntries: 20}); !errors.Is(err, ErrOutsideRoot) {
		t.Fatalf("outside size error = %v", err)
	}
	if _, err := root.AllocatedBytes(context.Background(), "missing", Limits{MaxDepth: 4, MaxEntries: 20}); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("missing size error = %v", err)
	}
}
