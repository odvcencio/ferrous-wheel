// Package hostfs provides bounded, root-relative file operations for host jobs.
//
// A Root keeps an open handle to its directory. Path replacement and symlink
// swaps cannot redirect its operations to an unrelated tree. Operations do not
// cross a symlink that points outside the opened root. Callers that delete a
// tree must keep other writers from moving opened child directories during the
// deletion. A same-user writer can otherwise move a child after it is opened.
package hostfs

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
)

var (
	ErrOutsideRoot = errors.New("path must stay within the opened root")
	ErrSymlink     = errors.New("symlink is not allowed")
	ErrLimit       = errors.New("file scan limit exceeded")
	ErrChanged     = errors.New("file changed during operation")
	ErrUnsupported = errors.New("file locks are unsupported on this platform")
)

type Root struct {
	path string
	file *os.Root
}

// Open anchors all later operations to the directory open at this call.
func Open(path string) (*Root, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	info, err := os.Lstat(abs)
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("open %q: %w", abs, ErrSymlink)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("open %q: not a directory", abs)
	}
	file, err := os.OpenRoot(abs)
	if err != nil {
		return nil, err
	}
	opened, err := file.Stat(".")
	if err != nil {
		_ = file.Close()
		return nil, err
	}
	if !os.SameFile(info, opened) {
		_ = file.Close()
		return nil, fmt.Errorf("open %q: %w", abs, ErrChanged)
	}
	return &Root{path: abs, file: file}, nil
}

// OpenReal opens each path component from an anchored directory handle. It
// rejects symlinks in the root path, including ancestors. A later rename of
// the visible path does not redirect the returned handle.
func OpenReal(path string) (*Root, error) {
	return openRealWithHook(path, nil)
}

func openRealWithHook(path string, beforeOpen func() error) (*Root, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	volume := filepath.VolumeName(abs)
	prefix := volume + string(filepath.Separator)
	current, err := os.OpenRoot(prefix)
	if err != nil {
		return nil, err
	}
	defer func() {
		if current != nil {
			_ = current.Close()
		}
	}()
	rest := strings.TrimPrefix(abs, prefix)
	if rest == abs {
		return nil, fmt.Errorf("open %q: %w", abs, ErrOutsideRoot)
	}
	if rest != "" {
		parts := strings.Split(rest, string(filepath.Separator))
		for i, part := range parts {
			info, err := current.Lstat(part)
			if err != nil {
				return nil, fmt.Errorf("open %q: %w", abs, err)
			}
			if info.Mode()&os.ModeSymlink != 0 {
				return nil, fmt.Errorf("open %q: %w", abs, ErrSymlink)
			}
			if !info.IsDir() {
				return nil, fmt.Errorf("open %q: not a directory", abs)
			}
			if i == len(parts)-1 && beforeOpen != nil {
				if err := beforeOpen(); err != nil {
					return nil, err
				}
			}
			next, err := current.OpenRoot(part)
			if err != nil {
				return nil, fmt.Errorf("open %q: %w", abs, err)
			}
			opened, err := next.Stat(".")
			if err != nil {
				_ = next.Close()
				return nil, fmt.Errorf("open %q: %w", abs, err)
			}
			if !os.SameFile(info, opened) {
				_ = next.Close()
				return nil, fmt.Errorf("open %q: %w", abs, ErrChanged)
			}
			_ = current.Close()
			current = next
		}
	}
	root := &Root{path: abs, file: current}
	current = nil
	return root, nil
}

func (r *Root) Close() error { return r.file.Close() }

// Path is the original path. It can become stale if another process moves it.
func (r *Root) Path() string { return r.path }

// Stat resolves a relative path within the opened root.
func (r *Root) Stat(name string) (fs.FileInfo, error) {
	if err := local(name, true); err != nil {
		return nil, err
	}
	return r.file.Stat(name)
}

// Lstat inspects a relative path without following its final symlink.
func (r *Root) Lstat(name string) (fs.FileInfo, error) {
	if err := local(name, true); err != nil {
		return nil, err
	}
	return r.file.Lstat(name)
}

func local(name string, allowDot bool) error {
	if name == "" || !filepath.IsLocal(name) || (!allowDot && filepath.Clean(name) == ".") {
		return fmt.Errorf("%q: %w", name, ErrOutsideRoot)
	}
	return nil
}

func childName(name string) error {
	if err := local(name, false); err != nil {
		return err
	}
	if filepath.Base(name) != name {
		return fmt.Errorf("%q: expected one child name: %w", name, ErrOutsideRoot)
	}
	return nil
}

func randomName(prefix string) (string, error) {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", err
	}
	return prefix + hex.EncodeToString(bytes[:]), nil
}

type TempDir struct {
	root   *Root
	name   string
	mu     sync.Mutex
	closed bool
}

// TempDir creates a private directory under Root. Defer Close after creation.
func (r *Root) TempDir(prefix string) (*TempDir, error) {
	if prefix != "" {
		if err := childName(prefix); err != nil {
			return nil, err
		}
	}
	for range 10 {
		name, err := randomName(prefix)
		if err != nil {
			return nil, err
		}
		err = r.file.Mkdir(name, 0o700)
		if errors.Is(err, fs.ErrExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		return &TempDir{root: r, name: name}, nil
	}
	return nil, fmt.Errorf("create temporary directory: name collisions")
}

func (d *TempDir) Name() string { return d.name }
func (d *TempDir) Path() string { return filepath.Join(d.root.path, d.name) }

// Close removes the temporary tree. A failed Close may be retried.
func (d *TempDir) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return nil
	}
	if err := d.root.file.RemoveAll(d.name); err != nil {
		return err
	}
	d.closed = true
	return nil
}

// WriteAtomic writes a sibling stage, syncs its bytes, and renames it into place.
// The destination parent must exist. A failed rename removes the stage.
func (r *Root) WriteAtomic(name string, data []byte, perm fs.FileMode) error {
	if err := local(name, false); err != nil {
		return err
	}
	parent := filepath.Dir(name)
	base := filepath.Base(name)
	for range 10 {
		stageBase, err := randomName(".hostfs-write-")
		if err != nil {
			return err
		}
		stage := filepath.Join(parent, stageBase)
		file, err := r.file.OpenFile(stage, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
		if errors.Is(err, fs.ErrExist) {
			continue
		}
		if err != nil {
			return err
		}
		defer r.file.Remove(stage)
		n, err := file.Write(data)
		if err == nil && n != len(data) {
			err = io.ErrShortWrite
		}
		if err != nil {
			_ = file.Close()
			return fmt.Errorf("write %q: %w", name, err)
		}
		if err := file.Sync(); err != nil {
			_ = file.Close()
			return fmt.Errorf("sync %q: %w", name, err)
		}
		if err := file.Close(); err != nil {
			return fmt.Errorf("close %q: %w", name, err)
		}
		if err := r.file.Rename(stage, filepath.Join(parent, base)); err != nil {
			return fmt.Errorf("replace %q: %w", name, err)
		}
		if runtime.GOOS == "windows" {
			return nil
		}
		dir, err := r.file.Open(parent)
		if err != nil {
			return fmt.Errorf("open parent of %q after replace: %w", name, err)
		}
		err = dir.Sync()
		closeErr := dir.Close()
		if err != nil || closeErr != nil {
			return fmt.Errorf("sync parent of %q after replace: %w", name, errors.Join(err, closeErr))
		}
		return nil
	}
	return fmt.Errorf("write %q: name collisions", name)
}

type Limits struct {
	MaxDepth   int // Start is depth zero. A depth limit prunes deeper entries.
	MaxEntries int // Includes the start entry. Exhaustion returns ErrLimit.
	MaxMatches int // Glob only. Exhaustion returns ErrLimit.
}

type Entry struct {
	Path  string
	Depth int
	Info  fs.FileInfo
}

// Walk reads entries without following symlinks. It never scans beyond limits.
// Entries arrive in directory order. The callback may return filepath.SkipDir
// or filepath.SkipAll.
func (r *Root) Walk(ctx context.Context, start string, limits Limits, visit func(Entry) error) error {
	if err := local(start, true); err != nil {
		return err
	}
	if limits.MaxDepth < 0 || limits.MaxEntries <= 0 {
		return fmt.Errorf("walk limits: %w", ErrLimit)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	before, err := r.file.Lstat(start)
	if err != nil {
		return err
	}
	if before.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("walk %q: %w", start, ErrSymlink)
	}
	dir, err := r.file.OpenRoot(start)
	if err != nil {
		return err
	}
	defer dir.Close()
	opened, err := dir.Stat(".")
	if err != nil {
		return err
	}
	if !os.SameFile(before, opened) {
		return fmt.Errorf("walk %q: %w", start, ErrChanged)
	}
	count := 0
	var scan func(*os.Root, string, int, fs.FileInfo) error
	scan = func(current *os.Root, rel string, depth int, info fs.FileInfo) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		count++
		if count > limits.MaxEntries {
			return fmt.Errorf("walk %q after %d entries: %w", rel, limits.MaxEntries, ErrLimit)
		}
		err := visit(Entry{Path: rel, Depth: depth, Info: info})
		if errors.Is(err, filepath.SkipAll) {
			return filepath.SkipAll
		}
		if errors.Is(err, filepath.SkipDir) {
			return nil
		}
		if err != nil {
			return err
		}
		if depth >= limits.MaxDepth || !info.IsDir() {
			return nil
		}
		f, err := current.Open(".")
		if err != nil {
			return err
		}
		defer f.Close()
		for {
			entries, err := f.ReadDir(64)
			if err != nil && !errors.Is(err, io.EOF) {
				return err
			}
			for _, child := range entries {
				name := child.Name()
				childInfo, statErr := current.Lstat(name)
				if errors.Is(statErr, fs.ErrNotExist) {
					return fmt.Errorf("walk %q: %w", filepath.Join(rel, name), ErrChanged)
				}
				if statErr != nil {
					return statErr
				}
				childRel := filepath.Join(rel, name)
				if childInfo.IsDir() && depth+1 < limits.MaxDepth {
					sub, openErr := current.OpenRoot(name)
					if openErr != nil {
						return fmt.Errorf("walk %q: %w", childRel, openErr)
					}
					openedInfo, statErr := sub.Stat(".")
					if statErr != nil || !os.SameFile(childInfo, openedInfo) {
						_ = sub.Close()
						return fmt.Errorf("walk %q: %w", childRel, errors.Join(statErr, ErrChanged))
					}
					childErr := scan(sub, childRel, depth+1, openedInfo)
					_ = sub.Close()
					if childErr != nil {
						return childErr
					}
				} else if childErr := scan(current, childRel, depth+1, childInfo); childErr != nil {
					return childErr
				}
			}
			if errors.Is(err, io.EOF) {
				return nil
			}
		}
	}
	err = scan(dir, filepath.Clean(start), 0, opened)
	if errors.Is(err, filepath.SkipAll) {
		return nil
	}
	return err
}

// Glob matches a relative filepath pattern over a bounded walk.
func (r *Root) Glob(ctx context.Context, pattern string, limits Limits) ([]string, error) {
	if err := local(pattern, false); err != nil {
		return nil, err
	}
	if _, err := filepath.Match(pattern, ""); err != nil {
		return nil, err
	}
	if limits.MaxMatches <= 0 {
		return nil, fmt.Errorf("glob limits: %w", ErrLimit)
	}
	var matches []string
	err := r.Walk(ctx, ".", limits, func(entry Entry) error {
		if entry.Path == "." {
			return nil
		}
		matched, err := filepath.Match(pattern, entry.Path)
		if err != nil {
			return err
		}
		if matched {
			if len(matches) >= limits.MaxMatches {
				return fmt.Errorf("glob %q after %d matches: %w", pattern, limits.MaxMatches, ErrLimit)
			}
			matches = append(matches, entry.Path)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(matches)
	return matches, nil
}

// Children returns direct children in name order or ErrLimit if the directory
// has more than maxEntries. It does not follow child symlinks.
func (r *Root) Children(ctx context.Context, maxEntries int) ([]Entry, error) {
	if maxEntries <= 0 || maxEntries == math.MaxInt {
		return nil, fmt.Errorf("children limit: %w", ErrLimit)
	}
	children := make([]Entry, 0)
	err := r.Walk(ctx, ".", Limits{MaxDepth: 1, MaxEntries: maxEntries + 1}, func(entry Entry) error {
		if entry.Depth == 1 {
			children = append(children, entry)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(children, func(i, j int) bool { return children[i].Path < children[j].Path })
	return children, nil
}

// Any stops at the first entry accepted by test. Scan errors still fail.
func (r *Root) Any(ctx context.Context, start string, limits Limits, test func(Entry) (bool, error)) (bool, error) {
	if test == nil {
		return false, errors.New("entry test is required")
	}
	found := false
	err := r.Walk(ctx, start, limits, func(entry Entry) error {
		ok, err := test(entry)
		if err != nil {
			return err
		}
		if ok {
			found = true
			return filepath.SkipAll
		}
		return nil
	})
	return found, err
}

// CheckNoSymlink rejects any symlink component currently visible in name.
// Use Root operations afterward; this check alone cannot make a later plain
// path operation safe against replacement races.
func (r *Root) CheckNoSymlink(name string) error {
	if err := local(name, true); err != nil {
		return err
	}
	if filepath.Clean(name) == "." {
		return nil
	}
	current := ""
	for _, part := range strings.Split(filepath.Clean(name), string(filepath.Separator)) {
		current = filepath.Join(current, part)
		info, err := r.file.Lstat(current)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%q: %w", current, ErrSymlink)
		}
	}
	return nil
}

type Effect struct {
	Action  string
	Path    string
	Applied bool
	Error   string
}

type Effects struct {
	dryRun  bool
	mu      sync.Mutex
	entries []Effect
}

func NewEffects(dryRun bool) *Effects { return &Effects{dryRun: dryRun} }

func (e *Effects) Entries() []Effect {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]Effect(nil), e.entries...)
}

// Run records an effect and its result. A dry run records the plan only.
func (e *Effects) Run(action, path string, apply func() error) error {
	if e == nil {
		return errors.New("effect log is required")
	}
	entry := Effect{Action: action, Path: path}
	if apply == nil {
		return errors.New("effect action is required")
	}
	if e.dryRun {
		e.mu.Lock()
		e.entries = append(e.entries, entry)
		e.mu.Unlock()
		return nil
	}
	err := apply()
	entry.Applied = err == nil
	if err != nil {
		entry.Error = err.Error()
	}
	e.mu.Lock()
	e.entries = append(e.entries, entry)
	e.mu.Unlock()
	return err
}

// RemoveTree removes one direct child after moving it to a random stage under
// the opened root. It rejects symlinks and records the effect. Other writers
// must not move child directories during deletion. A failed delete leaves the
// stage in place and returns its name for inspection.
func (r *Root) RemoveTree(name string, effects *Effects) error {
	if err := childName(name); err != nil {
		return err
	}
	info, err := r.file.Lstat(name)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("remove %q: %w", name, ErrSymlink)
	}
	if !info.IsDir() {
		return fmt.Errorf("remove %q: not a directory", name)
	}
	return effects.Run("remove-tree", name, func() error {
		stage, err := randomName(".hostfs-remove-")
		if err != nil {
			return err
		}
		if err := r.file.Rename(name, stage); err != nil {
			return err
		}
		moved, err := r.file.Lstat(stage)
		if err != nil || !os.SameFile(info, moved) {
			return fmt.Errorf("remove %q staged as %q: %w", name, stage, errors.Join(err, ErrChanged))
		}
		if err := r.file.RemoveAll(stage); err != nil {
			return fmt.Errorf("remove %q staged as %q: %w", name, stage, err)
		}
		return nil
	})
}

// RemoveFile removes one direct child that is not a directory. A symlink is
// removed as a link. The operation never follows the link target.
func (r *Root) RemoveFile(name string, effects *Effects) error {
	if err := childName(name); err != nil {
		return err
	}
	info, err := r.file.Lstat(name)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return fmt.Errorf("remove %q: expected a file", name)
	}
	return effects.Run("remove-file", name, func() error { return r.file.Remove(name) })
}

// RemoveTreeWithSidecars removes a direct child and its optional file sidecars.
// It attempts every sidecar and returns all non-absence errors.
func (r *Root) RemoveTreeWithSidecars(name string, suffixes []string, effects *Effects) error {
	if err := childName(name); err != nil {
		return err
	}
	for _, suffix := range suffixes {
		if suffix == "" || filepath.Base(suffix) != suffix {
			return fmt.Errorf("sidecar suffix %q: %w", suffix, ErrOutsideRoot)
		}
		if err := childName(name + suffix); err != nil {
			return err
		}
	}
	if err := r.RemoveTree(name, effects); err != nil {
		return err
	}
	var errs []error
	for _, suffix := range suffixes {
		sidecar := name + suffix
		if err := r.RemoveFile(sidecar, effects); err != nil && !errors.Is(err, fs.ErrNotExist) {
			errs = append(errs, fmt.Errorf("remove sidecar %q: %w", sidecar, err))
		}
	}
	return errors.Join(errs...)
}
