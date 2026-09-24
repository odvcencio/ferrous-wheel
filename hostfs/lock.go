package hostfs

import (
	"errors"
	"os"
	"sync"
)

// FileLock is an advisory lock. Keep the lock file after Close so all
// cooperating processes continue to lock the same inode.
type FileLock struct {
	file   *os.File
	mu     sync.Mutex
	closed bool
}

func (l *FileLock) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return nil
	}
	l.closed = true
	return errors.Join(unlockFile(l.file), l.file.Close())
}
