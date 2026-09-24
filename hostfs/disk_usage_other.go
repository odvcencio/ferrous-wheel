//go:build !linux && !darwin

package hostfs

import "context"

func (r *Root) AllocatedBytes(_ context.Context, _ string, _ Limits) (int64, error) {
	return 0, ErrUnsupported
}
