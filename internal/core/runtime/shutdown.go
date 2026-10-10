package runtime

import (
	"context"
	"errors"
	"path/filepath"
	"time"

	"github.com/roeehrl/hopsesh/internal/localstate"
)

// WaitReleased waits for the OS-held ownership lock, rather than treating a
// disconnected IPC listener as proof that native operations have drained.
func (c Client) WaitReleased(ctx context.Context) error {
	timer := time.NewTicker(50 * time.Millisecond)
	defer timer.Stop()
	for {
		lock, err := localstate.TryLock(filepath.Join(c.Namespace.Directory, "owner.lock"))
		if err == nil {
			return lock.Close()
		}
		if !errors.Is(err, localstate.ErrBusy) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
		}
	}
}
