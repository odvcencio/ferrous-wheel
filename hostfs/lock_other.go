//go:build !linux && !darwin

package hostfs

import (
	"context"
	"os"
)

func (r *Root) Lock(_ context.Context, _ string) (*FileLock, error) {
	return nil, ErrUnsupported
}

func unlockFile(_ *os.File) error { return nil }
