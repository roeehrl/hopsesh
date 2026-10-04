//go:build !windows

package repos

import (
	"os"

	"golang.org/x/sys/unix"
)

// tryLock takes an exclusive advisory lock on f without waiting.
func tryLock(f *os.File) (bool, error) {
	err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB)
	if err == unix.EWOULDBLOCK {
		return false, nil
	}
	return err == nil, err
}

func unlockFile(f *os.File) { _ = unix.Flock(int(f.Fd()), unix.LOCK_UN) }
