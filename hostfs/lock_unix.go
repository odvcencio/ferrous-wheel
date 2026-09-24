//go:build linux || darwin

package hostfs

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"syscall"
	"time"
)

// Lock takes an advisory lock on one direct child file. Close releases it.
// All mutators of a tree must honor the same lock for it to serialize them.
func (r *Root) Lock(ctx context.Context, name string) (*FileLock, error) {
	if err := childName(name); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	before, err := r.file.Lstat(name)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	if err == nil && before.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("lock %q: %w", name, ErrSymlink)
	}
	file, err := r.file.OpenFile(name, os.O_RDWR|os.O_CREATE|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open lock %q: %w", name, err)
	}
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || (before != nil && !os.SameFile(before, opened)) {
		_ = file.Close()
		return nil, fmt.Errorf("lock %q: %w", name, errors.Join(err, ErrChanged))
	}
	for {
		if err := ctx.Err(); err != nil {
			_ = file.Close()
			return nil, err
		}
		err = syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			current, statErr := r.file.Lstat(name)
			if statErr != nil || !os.SameFile(current, opened) {
				_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
				_ = file.Close()
				return nil, fmt.Errorf("lock %q: %w", name, errors.Join(statErr, ErrChanged))
			}
			return &FileLock{file: file}, nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EAGAIN) {
			_ = file.Close()
			return nil, fmt.Errorf("lock %q: %w", name, err)
		}
		timer := time.NewTimer(20 * time.Millisecond)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			_ = file.Close()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}

func unlockFile(file *os.File) error {
	return syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
}
