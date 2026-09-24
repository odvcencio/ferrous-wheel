//go:build linux || darwin

package hostfs

import (
	"context"
	"fmt"
	"math"
	"path/filepath"
	"syscall"
)

// AllocatedBytes counts allocated 512-byte blocks below start. It does not
// follow symlinks, count a hard-linked file twice, or descend into another
// filesystem. It fails when either scan bound is reached; a partial count is
// never returned as a success. All writers of child directories must honor
// the root's advisory lock during a destructive host job.
func (r *Root) AllocatedBytes(ctx context.Context, start string, limits Limits) (int64, error) {
	type fileID struct{ dev, ino uint64 }
	seen := make(map[fileID]bool)
	var rootDev uint64
	rootSeen := false
	var total int64
	err := r.Walk(ctx, start, limits, func(entry Entry) error {
		stat, ok := entry.Info.Sys().(*syscall.Stat_t)
		if !ok || stat.Blocks < 0 || stat.Blocks > math.MaxInt64/512 {
			return fmt.Errorf("size %q: invalid allocation data", entry.Path)
		}
		dev := uint64(stat.Dev)
		if !rootSeen {
			rootDev, rootSeen = dev, true
		} else if dev != rootDev {
			if entry.Info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.Info.IsDir() && entry.Depth >= limits.MaxDepth {
			return fmt.Errorf("size %q: depth %d: %w", entry.Path, limits.MaxDepth, ErrLimit)
		}
		if !entry.Info.IsDir() && stat.Nlink > 1 {
			id := fileID{dev: dev, ino: uint64(stat.Ino)}
			if seen[id] {
				return nil
			}
			seen[id] = true
		}
		blocks := stat.Blocks * 512
		if total > math.MaxInt64-blocks {
			return fmt.Errorf("size %q: allocation overflow: %w", entry.Path, ErrLimit)
		}
		total += blocks
		return nil
	})
	if err != nil {
		return 0, err
	}
	return total, nil
}
