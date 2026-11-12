//go:build !windows

package serve

import (
	"fmt"
	"io/fs"
	"net"
	"os"

	"golang.org/x/sys/unix"
)

func lockFile(f *os.File) error { return unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB) }

func checkOwner(dir string, fi fs.FileInfo) error {
	st, ok := fi.Sys().(*unix.Stat_t)
	if ok && int(st.Uid) != os.Getuid() {
		return fmt.Errorf("%s belongs to another user", dir)
	}
	if fi.Mode().Perm()&0o077 != 0 {
		if err := os.Chmod(dir, 0o700); err != nil {
			return fmt.Errorf("%s is open to other users", dir)
		}
	}
	return nil
}

// sameUser: the connection comes from a process of this user.
func sameUser(c net.Conn) bool {
	uc, ok := c.(*net.UnixConn)
	if !ok {
		return false
	}
	raw, err := uc.SyscallConn()
	if err != nil {
		return false
	}
	uid := -1
	_ = raw.Control(func(fd uintptr) { uid = peerUID(int(fd)) })
	return uid == os.Getuid()
}
