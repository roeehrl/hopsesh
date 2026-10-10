// Package localstate provides OS-held locks for local state. Lock files are never
// unlinked: replacing their inode would allow two independent owners.
package localstate

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"
)

var ErrBusy = errors.New("state is owned by another process")

func TryLock(path string) (*os.File, error) {
	f, err := openLock(path)
	if err != nil {
		return nil, err
	}
	if err = lockFile(f); err != nil {
		f.Close()
		if busy(err) {
			return nil, ErrBusy
		}
		return nil, fmt.Errorf("lock %s: %w", path, err)
	}
	return f, nil
}

func Lock(ctx context.Context, path string) (*os.File, error) {
	for {
		f, err := TryLock(path)
		if !errors.Is(err, ErrBusy) {
			return f, err
		}
		timer := time.NewTimer(20 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}
