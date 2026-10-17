//go:build !windows

package localstate

import (
	"fmt"
	"golang.org/x/sys/unix"
	"os"
	"syscall"
)

func openLock(path string) (*os.File, error) {
	fd, err := unix.Open(path, unix.O_CREAT|unix.O_RDWR|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), path)
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	if !st.Mode().IsRegular() || st.Mode().Perm()&0077 != 0 || st.Sys().(*syscall.Stat_t).Uid != uint32(os.Geteuid()) {
		f.Close()
		return nil, fmt.Errorf("lock must be a private regular file owned by this user: %s", path)
	}
	return f, nil
}
func lockFile(f *os.File) error { return unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB) }
func busy(err error) bool       { return err == unix.EWOULDBLOCK || err == unix.EAGAIN }
