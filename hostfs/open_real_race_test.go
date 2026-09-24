package hostfs

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestOpenRealAncestorSwapKeepsOriginalDirectory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation may require Windows developer mode")
	}
	parent, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	gate := filepath.Join(parent, "gate")
	moved := filepath.Join(parent, "moved")
	outside := filepath.Join(parent, "outside")
	for _, dir := range []string{filepath.Join(gate, "work"), filepath.Join(outside, "work")} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	original, err := os.Stat(filepath.Join(gate, "work"))
	if err != nil {
		t.Fatal(err)
	}
	root, err := openRealWithHook(filepath.Join(gate, "work"), func() error {
		if err := os.Rename(gate, moved); err != nil {
			return err
		}
		return os.Symlink(outside, gate)
	})
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	opened, err := root.Stat(".")
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(original, opened) {
		t.Fatalf("opened swapped ancestor target %q, not original %q", root.Path(), filepath.Join(moved, "work"))
	}
}
