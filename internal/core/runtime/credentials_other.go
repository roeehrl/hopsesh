//go:build !windows && !darwin && !freebsd && !linux

package runtime

import (
	"errors"
	"net"
)

func sameUser(net.Conn) error {
	return errors.New("private runtime IPC is unsupported on this platform")
}
