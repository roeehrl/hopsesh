package repos

import (
	"context"
	"os"
	"time"
)

// lockFolder holds an exclusive lock on the file p (made if needed) until the returned
// function runs: one hand-off of a repository at a time uses its hand-off folder. It
// waits for one held elsewhere (telling waiting once) until ctx ends. The lock goes with
// the process that holds it, so one left by a crash does not block.
func lockFolder(ctx context.Context, p string, waiting func()) (func(), error) {
	f, err := os.OpenFile(p, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	told := false
	for {
		ok, err := tryLock(f)
		if err != nil {
			f.Close()
			return nil, err
		}
		if ok {
			return func() { unlockFile(f); f.Close() }, nil
		}
		if !told && waiting != nil {
			waiting()
			told = true
		}
		select {
		case <-ctx.Done():
			f.Close()
			return nil, ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
}
