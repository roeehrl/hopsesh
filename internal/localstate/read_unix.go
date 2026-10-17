//go:build !windows

package localstate

import (
	"errors"
	"golang.org/x/sys/unix"
	"os"
	"syscall"
)

func openOwnedRead(path string, private bool) (*os.File, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), path)
	st, err := f.Stat()
	if err == nil && (!st.Mode().IsRegular() || private && st.Mode().Perm()&0077 != 0 || st.Sys().(*syscall.Stat_t).Uid != uint32(os.Geteuid())) {
		err = errors.New("state file must be private, regular and owned by this user")
	}
	if err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}
