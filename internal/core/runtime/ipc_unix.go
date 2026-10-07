//go:build !windows

package runtime

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"syscall"
)

func address(dir, id string) string {
	return filepath.Join("/tmp", fmt.Sprintf("hopsesh-%d", os.Geteuid()), id+".sock")
}
func privateDirectory(dir string) error {
	st, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if !st.IsDir() || st.Mode().Perm()&0077 != 0 || st.Sys().(*syscall.Stat_t).Uid != uint32(os.Geteuid()) {
		return fmt.Errorf("runtime directory must be private and owned by this user: %s", dir)
	}
	return nil
}
func listen(n Namespace) (net.Listener, error) {
	socketDir := filepath.Dir(n.Address)
	if err := os.MkdirAll(socketDir, 0700); err != nil {
		return nil, err
	}
	if err := privateDirectory(socketDir); err != nil {
		return nil, err
	}
	if len(n.Address) > 100 {
		return nil, fmt.Errorf("runtime socket path exceeds platform limit: %s", n.Address)
	}
	st, err := os.Lstat(n.Address)
	if err == nil {
		if st.Mode()&os.ModeSocket == 0 || st.Sys().(*syscall.Stat_t).Uid != uint32(os.Geteuid()) {
			return nil, fmt.Errorf("refusing to replace non-owned socket: %s", n.Address)
		}
		if err = os.Remove(n.Address); err != nil {
			return nil, err
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	l, err := net.Listen("unix", n.Address)
	if err != nil {
		return nil, err
	}
	if err = os.Chmod(n.Address, 0600); err != nil {
		l.Close()
		return nil, err
	}
	return l, nil
}
func dial(ctx context.Context, n Namespace) (net.Conn, error) {
	if err := privateDirectory(n.Directory); err != nil {
		return nil, err
	}
	if err := privateDirectory(filepath.Dir(n.Address)); err != nil {
		return nil, err
	}
	c, err := (&net.Dialer{}).DialContext(ctx, "unix", n.Address)
	if err != nil {
		return nil, err
	}
	if err = sameUser(c); err != nil {
		c.Close()
		return nil, err
	}
	return c, nil
}
