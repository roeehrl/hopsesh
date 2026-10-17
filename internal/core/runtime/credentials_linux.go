//go:build linux

package runtime

import (
	"fmt"
	"golang.org/x/sys/unix"
	"net"
	"os"
)

func sameUser(c net.Conn) error {
	s, err := c.(*net.UnixConn).SyscallConn()
	if err != nil {
		return err
	}
	var auth error
	err = s.Control(func(fd uintptr) {
		u, e := unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
		if e != nil {
			auth = e
		} else if u.Uid != uint32(os.Geteuid()) {
			auth = fmt.Errorf("runtime peer belongs to another user")
		}
	})
	if err != nil {
		return err
	}
	return auth
}
