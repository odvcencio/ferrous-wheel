//go:build linux

package prototypes

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestBuildboxWorkGCContract(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "buildbox-work-gc")
	build := exec.Command("go", "run", "../cmd/ferrous-wheel", "build", "buildbox-work-gc.fw", "-o", bin)
	build.Env = append(os.Environ(), "GOWORK=off")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build prototype: %v\n%s", err, out)
	}
	root := filepath.Join(t.TempDir(), "work")
	for _, name := range []string{"old", "active", "recent"} {
		if err := os.MkdirAll(filepath.Join(root, name, "nested"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"old.last-used", "old.bundle", "recent.last-used"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("marker"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	future := time.Now().Add(5 * 24 * time.Hour)
	if err := os.Chtimes(filepath.Join(root, "recent.last-used"), future, future); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "keep"), []byte("safe"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	procRoot := t.TempDir()
	if err := os.Mkdir(filepath.Join(procRoot, "101"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "active"), filepath.Join(procRoot, "101", "cwd")); err != nil {
		t.Fatal(err)
	}
	args := []string{"--root", root, "--now-unix", fmt.Sprint(future.Unix()), "--free-kb", "50000000", "--proc-root", procRoot}
	badProc := filepath.Join(procRoot, "102")
	if err := os.Mkdir(badProc, 0o000); err != nil {
		t.Fatal(err)
	}
	blocked := exec.Command(bin, args...)
	out, err := blocked.CombinedOutput()
	if err == nil || !strings.Contains(string(out), "cannot inspect process 102 cwd") {
		t.Fatalf("unreadable process must block cleanup: %v\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(root, "old")); err != nil {
		t.Fatalf("visibility fault removed old copy: %v", err)
	}
	if err := os.Chmod(badProc, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(badProc); err != nil {
		t.Fatal(err)
	}
	dry := exec.Command(bin, append(args, "--dry-run")...)
	out, err = dry.CombinedOutput()
	if err != nil {
		t.Fatalf("dry run: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "would remove old ") || strings.Contains(string(out), "would remove active ") || strings.Contains(string(out), "would remove recent ") {
		t.Fatalf("dry-run plan:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(root, "old")); err != nil {
		t.Fatalf("dry run changed old copy: %v", err)
	}
	stubPath := t.TempDir()
	probe := filepath.Join(t.TempDir(), "du-called")
	if err := os.WriteFile(filepath.Join(stubPath, "du"), []byte("#!/bin/sh\nprintf called > \"$FW_DU_PROBE\"\nexit 9\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	noDu := exec.Command(bin, append(args, "--dry-run")...)
	noDu.Env = append(os.Environ(), "PATH="+stubPath, "FW_DU_PROBE="+probe)
	out, err = noDu.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "would remove old ") {
		t.Fatalf("rooted size scan: %v\n%s", err, out)
	}
	if _, err := os.Stat(probe); !os.IsNotExist(err) {
		t.Fatalf("size probe invoked du: %v", err)
	}
	live := exec.Command(bin, args...)
	out, err = live.CombinedOutput()
	if err != nil {
		t.Fatalf("live run: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "removed old ") {
		t.Fatalf("live output:\n%s", out)
	}
	for _, name := range []string{"old", "old.last-used", "old.bundle"} {
		if _, err := os.Lstat(filepath.Join(root, name)); !os.IsNotExist(err) {
			t.Fatalf("%s remains: %v", name, err)
		}
	}
	for _, name := range []string{"active", "recent", "recent.last-used"} {
		if _, err := os.Lstat(filepath.Join(root, name)); err != nil {
			t.Fatalf("%s was removed: %v", name, err)
		}
	}
	if got, err := os.ReadFile(filepath.Join(outside, "keep")); err != nil || string(got) != "safe" {
		t.Fatalf("outside sentinel = %q, %v", got, err)
	}
}
