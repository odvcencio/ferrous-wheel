package hostfs

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func openTestRoot(t *testing.T) (*Root, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "work")
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	root, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = root.Close() })
	return root, path
}

func TestTempDirCleansAndClosesIdempotently(t *testing.T) {
	root, path := openTestRoot(t)
	temp, err := root.TempDir("task-")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(temp.Path(), "state"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := temp.Close(); err != nil {
		t.Fatal(err)
	}
	if err := temp.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(path, temp.Name())); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("temporary directory remains: %v", err)
	}
}

func TestTempDirRetriesCleanupAfterPermissionFault(t *testing.T) {
	root, path := openTestRoot(t)
	temp, err := root.TempDir("task-")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o500); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(path, 0o700)
	if err := temp.Close(); err == nil {
		t.Fatal("cleanup must report an unwritable parent")
	}
	if err := os.Chmod(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := temp.Close(); err != nil {
		t.Fatalf("cleanup retry: %v", err)
	}
}

func TestTempDirRejectsUnsafePrefixAndClosedRoot(t *testing.T) {
	root, _ := openTestRoot(t)
	if _, err := root.TempDir("../bad"); !errors.Is(err, ErrOutsideRoot) {
		t.Fatalf("unsafe prefix error = %v", err)
	}
	if err := root.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := root.TempDir("safe-"); err == nil {
		t.Fatal("closed root created temporary directory")
	}
}

func TestWriteAtomicReplacesAndCleansFailedStage(t *testing.T) {
	root, path := openTestRoot(t)
	if err := root.WriteAtomic("state.json", []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(path, "state.json"))
	if err != nil || string(got) != "new" {
		t.Fatalf("atomic write = %q, %v", got, err)
	}
	if err := os.Mkdir(filepath.Join(path, "occupied"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := root.WriteAtomic("occupied", []byte("bad"), 0o600); err == nil {
		t.Fatal("replacing a directory must fail")
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".hostfs-write-") {
			t.Fatalf("failed write left stage %q", entry.Name())
		}
	}
}

func TestWriteAtomicReplacesLinkWithoutTouchingTarget(t *testing.T) {
	root, path := openTestRoot(t)
	target := filepath.Join(t.TempDir(), "keep")
	if err := os.WriteFile(target, []byte("safe"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(path, "state")); err != nil {
		t.Fatal(err)
	}
	if err := root.WriteAtomic("state", []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(target); err != nil || string(got) != "safe" {
		t.Fatalf("outside target = %q, %v", got, err)
	}
	if got, err := os.ReadFile(filepath.Join(path, "state")); err != nil || string(got) != "new" {
		t.Fatalf("replacement = %q, %v", got, err)
	}
}

func TestWriteAtomicFailsWithoutParent(t *testing.T) {
	root, path := openTestRoot(t)
	if err := root.WriteAtomic(filepath.Join("missing", "state"), []byte("x"), 0o600); err == nil {
		t.Fatal("missing parent did not fail")
	}
	entries, err := os.ReadDir(path)
	if err != nil || len(entries) != 0 {
		t.Fatalf("write left entries: %v, %v", entries, err)
	}
}

func TestWriteAtomicReportsUnwritableParent(t *testing.T) {
	root, path := openTestRoot(t)
	if err := os.Chmod(path, 0o500); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(path, 0o700)
	if err := root.WriteAtomic("state", []byte("x"), 0o600); err == nil {
		t.Fatal("unwritable parent did not fail")
	}
	if err := os.Chmod(path, 0o700); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(path)
	if err != nil || len(entries) != 0 {
		t.Fatalf("failed write left entries: %v, %v", entries, err)
	}
}

func TestWalkAndGlobStayBoundedAndDoNotFollowLinks(t *testing.T) {
	root, path := openTestRoot(t)
	if err := os.MkdirAll(filepath.Join(path, "a", "b"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "a", "b", "file"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(path, "link")); err != nil {
		t.Fatal(err)
	}
	var seen []string
	err := root.Walk(context.Background(), ".", Limits{MaxDepth: 2, MaxEntries: 10}, func(e Entry) error {
		seen = append(seen, e.Path)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(seen, ","), "secret") || !contains(seen, "link") || contains(seen, filepath.Join("a", "b", "file")) {
		t.Fatalf("bounded walk visited %v", seen)
	}
	if err := root.Walk(context.Background(), ".", Limits{MaxDepth: 3, MaxEntries: 2}, func(Entry) error { return nil }); !errors.Is(err, ErrLimit) {
		t.Fatalf("entry bound error = %v", err)
	}
	matches, err := root.Glob(context.Background(), "a/*", Limits{MaxDepth: 2, MaxEntries: 10, MaxMatches: 2})
	if err != nil || len(matches) != 1 || matches[0] != filepath.Join("a", "b") {
		t.Fatalf("glob = %v, %v", matches, err)
	}
	if _, err := root.Glob(context.Background(), "*", Limits{MaxDepth: 2, MaxEntries: 10, MaxMatches: 1}); !errors.Is(err, ErrLimit) {
		t.Fatalf("match bound error = %v", err)
	}
	if _, err := root.Glob(context.Background(), "../*", Limits{MaxDepth: 2, MaxEntries: 10, MaxMatches: 2}); !errors.Is(err, ErrOutsideRoot) {
		t.Fatalf("outside glob error = %v", err)
	}
	if err := root.CheckNoSymlink("link/secret"); !errors.Is(err, ErrSymlink) {
		t.Fatalf("symlink check error = %v", err)
	}
}

func TestWalkCancellationAndSkip(t *testing.T) {
	root, path := openTestRoot(t)
	if err := os.Mkdir(filepath.Join(path, "skip"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "skip", "child"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := root.Walk(ctx, ".", Limits{MaxDepth: 3, MaxEntries: 10}, func(Entry) error { return nil }); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled walk error = %v", err)
	}
	var seen []string
	if err := root.Walk(context.Background(), ".", Limits{MaxDepth: 3, MaxEntries: 10}, func(entry Entry) error {
		seen = append(seen, entry.Path)
		if entry.Path == "skip" {
			return filepath.SkipDir
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if contains(seen, filepath.Join("skip", "child")) {
		t.Fatalf("SkipDir visited child: %v", seen)
	}
	if err := root.Walk(context.Background(), ".", Limits{MaxDepth: 3, MaxEntries: 10}, func(Entry) error { return filepath.SkipAll }); err != nil {
		t.Fatalf("SkipAll error = %v", err)
	}
	if err := root.Walk(context.Background(), ".", Limits{MaxDepth: 3}, func(Entry) error { return nil }); !errors.Is(err, ErrLimit) {
		t.Fatalf("invalid limit error = %v", err)
	}
}

func TestWalkRejectsStartLinkAndPropagatesCallbackError(t *testing.T) {
	root, path := openTestRoot(t)
	if err := os.Symlink(path, filepath.Join(path, "self")); err != nil {
		t.Fatal(err)
	}
	limits := Limits{MaxDepth: 2, MaxEntries: 10}
	if err := root.Walk(context.Background(), "self", limits, func(Entry) error { return nil }); !errors.Is(err, ErrSymlink) {
		t.Fatalf("start link error = %v", err)
	}
	sentinel := errors.New("callback failed")
	if err := root.Walk(context.Background(), ".", limits, func(Entry) error { return sentinel }); !errors.Is(err, sentinel) {
		t.Fatalf("callback error = %v", err)
	}
}

func TestRemoveTreeUsesOpenedRootAfterPathReplacement(t *testing.T) {
	parent := t.TempDir()
	work := filepath.Join(parent, "work")
	moved := filepath.Join(parent, "moved")
	outside := filepath.Join(parent, "outside")
	for _, path := range []string{filepath.Join(work, "old", "nested"), filepath.Join(outside, "old")} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(outside, "old", "keep"), []byte("safe"), 0o600); err != nil {
		t.Fatal(err)
	}
	root, err := Open(work)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if err := os.Rename(work, moved); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, work); err != nil {
		t.Fatal(err)
	}
	log := NewEffects(false)
	if err := root.RemoveTree("old", log); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(moved, "old")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("opened root child remains: %v", err)
	}
	if got, err := os.ReadFile(filepath.Join(outside, "old", "keep")); err != nil || string(got) != "safe" {
		t.Fatalf("outside sentinel = %q, %v", got, err)
	}
	if got := log.Entries(); len(got) != 1 || got[0].Action != "remove-tree" || !got[0].Applied {
		t.Fatalf("effect log = %+v", got)
	}
}

func TestRemoveTreeUnderConcurrentRootPathSwaps(t *testing.T) {
	parent := t.TempDir()
	work := filepath.Join(parent, "work")
	moved := filepath.Join(parent, "moved")
	outside := filepath.Join(parent, "outside")
	if err := os.Mkdir(work, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(outside, 0o700); err != nil {
		t.Fatal(err)
	}
	for i := range 60 {
		name := fmt.Sprintf("old-%02d", i)
		if err := os.MkdirAll(filepath.Join(work, name, "nested"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(filepath.Join(outside, name), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(outside, name, "keep"), []byte(name), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	root, err := Open(work)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	done := make(chan error, 1)
	go func() {
		for range 300 {
			if err := os.Rename(work, moved); err != nil {
				done <- err
				return
			}
			if err := os.Symlink(outside, work); err != nil {
				done <- err
				return
			}
			if err := os.Remove(work); err != nil {
				done <- err
				return
			}
			if err := os.Rename(moved, work); err != nil {
				done <- err
				return
			}
		}
		done <- nil
	}()
	for i := range 60 {
		name := fmt.Sprintf("old-%02d", i)
		if err := root.RemoveTree(name, NewEffects(false)); err != nil {
			t.Fatal(err)
		}
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	for i := range 60 {
		name := fmt.Sprintf("old-%02d", i)
		got, err := os.ReadFile(filepath.Join(outside, name, "keep"))
		if err != nil || string(got) != name {
			t.Fatalf("outside sentinel %s = %q, %v", name, got, err)
		}
	}
}

func TestRemoveTreeRejectsSymlinkAndDryRun(t *testing.T) {
	root, path := openTestRoot(t)
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(path, "link")); err != nil {
		t.Fatal(err)
	}
	if err := root.RemoveTree("link", NewEffects(false)); !errors.Is(err, ErrSymlink) {
		t.Fatalf("symlink remove error = %v", err)
	}
	if err := os.Mkdir(filepath.Join(path, "old"), 0o700); err != nil {
		t.Fatal(err)
	}
	log := NewEffects(true)
	if err := root.RemoveTree("old", log); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(path, "old")); err != nil {
		t.Fatalf("dry run removed directory: %v", err)
	}
	if got := log.Entries(); len(got) != 1 || got[0].Applied || got[0].Path != "old" {
		t.Fatalf("dry-run effects = %+v", got)
	}
	if err := root.RemoveTree("../outside", NewEffects(false)); !errors.Is(err, ErrOutsideRoot) {
		t.Fatalf("outside remove error = %v", err)
	}
}

func TestRemoveTreeKeepsFailedStageForInspection(t *testing.T) {
	root, path := openTestRoot(t)
	nested := filepath.Join(path, "old", "readonly")
	if err := os.MkdirAll(nested, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nested, "keep"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(nested, 0o500); err != nil {
		t.Fatal(err)
	}
	log := NewEffects(false)
	if err := root.RemoveTree("old", log); err == nil {
		t.Fatal("unwritable subtree was removed")
	}
	if got := log.Entries(); len(got) != 1 || got[0].Applied || !strings.Contains(got[0].Error, ".hostfs-remove-") {
		t.Fatalf("failed removal log = %+v", got)
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		t.Fatal(err)
	}
	var stage string
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".hostfs-remove-") {
			stage = entry.Name()
		}
	}
	if stage == "" {
		t.Fatal("failed removal lost the staged tree")
	}
	if err := os.Chmod(filepath.Join(path, stage, "readonly"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := root.RemoveTree(stage, NewEffects(false)); err != nil {
		t.Fatalf("stage cleanup retry: %v", err)
	}
}

func TestRemoveFileRecordsEffectWithoutFollowingLink(t *testing.T) {
	root, path := openTestRoot(t)
	outside := t.TempDir()
	target := filepath.Join(outside, "keep")
	if err := os.WriteFile(target, []byte("safe"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(path, "marker")); err != nil {
		t.Fatal(err)
	}
	log := NewEffects(false)
	if err := root.RemoveFile("marker", log); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(target); err != nil || string(got) != "safe" {
		t.Fatalf("outside target = %q, %v", got, err)
	}
	if got := log.Entries(); len(got) != 1 || got[0].Action != "remove-file" || !got[0].Applied {
		t.Fatalf("effect log = %+v", got)
	}
}

func TestEffectsReportFailuresAndDryRun(t *testing.T) {
	sentinel := errors.New("write failed")
	log := NewEffects(false)
	if err := log.Run("write", "state", func() error { return sentinel }); !errors.Is(err, sentinel) {
		t.Fatalf("effect error = %v", err)
	}
	if got := log.Entries(); len(got) != 1 || got[0].Applied || !strings.Contains(got[0].Error, "write failed") {
		t.Fatalf("failed effect = %+v", got)
	}
	if err := log.Run("write", "state", nil); err == nil {
		t.Fatal("nil action must fail")
	}
	var missing *Effects
	if err := missing.Run("write", "state", func() error { return nil }); err == nil {
		t.Fatal("missing effect log must fail")
	}
	dry := NewEffects(true)
	called := false
	if err := dry.Run("write", "state", func() error { called = true; return nil }); err != nil {
		t.Fatal(err)
	}
	if called || len(dry.Entries()) != 1 || dry.Entries()[0].Applied {
		t.Fatalf("dry run executed action: called=%v entries=%+v", called, dry.Entries())
	}
}

func TestRelativePathAndRootSymlinkRejection(t *testing.T) {
	root, path := openTestRoot(t)
	link := filepath.Join(t.TempDir(), "work-link")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(link); !errors.Is(err, ErrSymlink) {
		t.Fatalf("symlink root error = %v", err)
	}
	if _, err := Open(filepath.Join(path, "missing")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("missing root error = %v", err)
	}
	filePath := filepath.Join(path, "plain")
	if err := os.WriteFile(filePath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(filePath); err == nil {
		t.Fatal("plain file was accepted as a root")
	}
	if got := root.Path(); got != path {
		t.Fatalf("root path = %q, want %q", got, path)
	}
	if _, err := root.Lstat("plain"); err != nil {
		t.Fatal(err)
	}
	if err := root.CheckNoSymlink("plain"); err != nil {
		t.Fatal(err)
	}
	if _, err := root.Stat("../outside"); !errors.Is(err, ErrOutsideRoot) {
		t.Fatalf("outside stat error = %v", err)
	}
	if err := root.WriteAtomic("../outside", nil, 0o600); !errors.Is(err, ErrOutsideRoot) {
		t.Fatalf("outside write error = %v", err)
	}
	if err := root.RemoveFile("../outside", NewEffects(false)); !errors.Is(err, ErrOutsideRoot) {
		t.Fatalf("outside remove error = %v", err)
	}
	if _, err := root.Glob(context.Background(), "[", Limits{MaxDepth: 1, MaxEntries: 10, MaxMatches: 10}); err == nil {
		t.Fatal("malformed glob must fail")
	}
}

func TestOpenRealRejectsAncestorLink(t *testing.T) {
	parent := t.TempDir()
	parent, err := filepath.EvalSymlinks(parent)
	if err != nil {
		t.Fatal(err)
	}
	real := filepath.Join(parent, "real")
	if err := os.MkdirAll(filepath.Join(real, "work"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, filepath.Join(parent, "link")); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenReal(filepath.Join(parent, "link", "work")); !errors.Is(err, ErrSymlink) {
		t.Fatalf("ancestor symlink error = %v", err)
	}
	root, err := OpenReal(filepath.Join(real, "work"))
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if _, err := OpenReal(filepath.Join(real, "missing")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("missing strict root error = %v", err)
	}
}

func TestChildrenAndAnyHaveExplicitBounds(t *testing.T) {
	root, path := openTestRoot(t)
	for _, name := range []string{"a", "b", "c"} {
		if err := os.Mkdir(filepath.Join(path, name), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := root.Children(context.Background(), 2); !errors.Is(err, ErrLimit) {
		t.Fatalf("children bound error = %v", err)
	}
	children, err := root.Children(context.Background(), 3)
	if err != nil || len(children) != 3 {
		t.Fatalf("children = %+v, %v", children, err)
	}
	found, err := root.Any(context.Background(), ".", Limits{MaxDepth: 1, MaxEntries: 2}, func(entry Entry) (bool, error) {
		return entry.Path != ".", nil
	})
	if err != nil || !found {
		t.Fatalf("early any = %v, %v", found, err)
	}
	if _, err := root.Any(context.Background(), ".", Limits{MaxDepth: 1, MaxEntries: 2}, func(Entry) (bool, error) {
		return false, nil
	}); !errors.Is(err, ErrLimit) {
		t.Fatalf("any bound error = %v", err)
	}
}

func TestRemoveTreeWithSidecarsReportsAllFailures(t *testing.T) {
	root, path := openTestRoot(t)
	if err := os.Mkdir(filepath.Join(path, "old"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(path, "old.bad"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "old.good"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	log := NewEffects(false)
	if err := root.RemoveTreeWithSidecars("old", []string{".bad", ".good"}, log); err == nil {
		t.Fatal("directory sidecar must be reported")
	}
	if _, err := os.Stat(filepath.Join(path, "old.good")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("other sidecar was not removed: %v", err)
	}
	if got := log.Entries(); len(got) != 2 || got[0].Action != "remove-tree" || got[1].Action != "remove-file" {
		t.Fatalf("effects = %+v", got)
	}
	if err := root.RemoveTreeWithSidecars("../old", nil, NewEffects(false)); !errors.Is(err, ErrOutsideRoot) {
		t.Fatalf("outside tree error = %v", err)
	}
}

func TestUnsafeInputsAndFailedOperationsRemainVisible(t *testing.T) {
	root, path := openTestRoot(t)
	if _, err := root.Stat("missing"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("missing stat error = %v", err)
	}
	if _, err := root.Lstat("../outside"); !errors.Is(err, ErrOutsideRoot) {
		t.Fatalf("outside lstat error = %v", err)
	}
	if err := root.Walk(context.Background(), "missing", Limits{MaxDepth: 1, MaxEntries: 2}, func(Entry) error { return nil }); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("missing walk error = %v", err)
	}
	if err := root.Walk(context.Background(), "../outside", Limits{MaxDepth: 1, MaxEntries: 2}, func(Entry) error { return nil }); !errors.Is(err, ErrOutsideRoot) {
		t.Fatalf("outside walk error = %v", err)
	}
	if _, err := root.Children(context.Background(), 0); !errors.Is(err, ErrLimit) {
		t.Fatalf("unbounded children error = %v", err)
	}
	sentinel := errors.New("predicate failed")
	if _, err := root.Any(context.Background(), ".", Limits{MaxDepth: 1, MaxEntries: 2}, func(Entry) (bool, error) {
		return false, sentinel
	}); !errors.Is(err, sentinel) {
		t.Fatalf("predicate error = %v", err)
	}
	if _, err := root.Any(context.Background(), ".", Limits{MaxDepth: 1, MaxEntries: 2}, nil); err == nil {
		t.Fatal("missing predicate was accepted")
	}
	if err := root.RemoveTree("missing", NewEffects(false)); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("missing removal error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(path, "plain"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := root.RemoveTree("plain", NewEffects(false)); err == nil {
		t.Fatal("plain file accepted as a tree")
	}
	if err := root.RemoveTreeWithSidecars("plain", []string{"/escape"}, NewEffects(false)); !errors.Is(err, ErrOutsideRoot) {
		t.Fatalf("unsafe sidecar error = %v", err)
	}
	if err := os.Mkdir(filepath.Join(path, "dir"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := root.RemoveFile("dir", NewEffects(false)); err == nil {
		t.Fatal("directory accepted as a file")
	}
	if err := root.CheckNoSymlink("../outside"); !errors.Is(err, ErrOutsideRoot) {
		t.Fatalf("outside check error = %v", err)
	}
}

func TestLockRespectsContextAndRelease(t *testing.T) {
	root, _ := openTestRoot(t)
	first, err := root.Lock(context.Background(), ".gc.lock")
	if errors.Is(err, ErrUnsupported) {
		t.Skip("advisory locks are unavailable on this platform")
	}
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := root.Lock(ctx, ".gc.lock"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("contended lock error = %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	second, err := root.Lock(context.Background(), ".gc.lock")
	if err != nil {
		t.Fatal(err)
	}
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}
	if err := second.Close(); err != nil {
		t.Fatalf("second close: %v", err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := root.Lock(canceled, ".gc.lock"); !errors.Is(err, context.Canceled) {
		t.Fatalf("pre-canceled lock error = %v", err)
	}
	if _, err := root.Lock(context.Background(), "../outside"); !errors.Is(err, ErrOutsideRoot) {
		t.Fatalf("unsafe lock path error = %v", err)
	}
	if err := os.Symlink(".gc.lock", filepath.Join(root.Path(), ".link.lock")); err != nil {
		t.Fatal(err)
	}
	if _, err := root.Lock(context.Background(), ".link.lock"); err == nil {
		t.Fatal("symlink lock path was accepted")
	}
	if err := os.Mkdir(filepath.Join(root.Path(), "directory.lock"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := root.Lock(context.Background(), "directory.lock"); err == nil {
		t.Fatal("directory was accepted as a lock file")
	}
	if err := root.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := root.Lock(context.Background(), ".gc.lock"); err == nil {
		t.Fatal("closed root was accepted for locking")
	}
}

func contains(items []string, target string) bool {
	for _, item := range items {
		if item == target {
			return true
		}
	}
	return false
}
