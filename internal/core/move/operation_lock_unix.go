//go:build !windows

package move

import (
	"golang.org/x/sys/unix"
	"os"
)

func lockOperationFile(f *os.File) error { return unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB) }
