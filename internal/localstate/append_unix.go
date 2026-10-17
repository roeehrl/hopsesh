//go:build !windows

package localstate

import (
	"errors"
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

// OpenPrivateAppend checks the opened object before returning a writable handle.
func OpenPrivateAppend(path string) (*os.File, error) {
	fd, err := unix.Open(path, unix.O_WRONLY|unix.O_APPEND|unix.O_CREAT|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0600)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), path)
	st, err := f.Stat()
	if err == nil && (!st.Mode().IsRegular() || st.Mode().Perm()&0077 != 0 || st.Sys().(*syscall.Stat_t).Uid != uint32(os.Geteuid())) {
		err = errors.New("log must be a private regular owned file")
	}
	if err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}
