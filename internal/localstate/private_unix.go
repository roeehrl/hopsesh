//go:build !windows

package localstate

import (
	"fmt"
	"os"
	"syscall"
)

func PrivateDirectory(path string) error {
	st, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !st.IsDir() || st.Mode().Perm()&0077 != 0 || st.Sys().(*syscall.Stat_t).Uid != uint32(os.Geteuid()) {
		return fmt.Errorf("state directory must be private and owned by this user: %s", path)
	}
	return nil
}

// SecureDirectory restricts only an actual directory owned by the current user.
// It never follows a symlink or takes ownership of another user's directory.
func SecureDirectory(path string) error {
	st, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !st.IsDir() || st.Sys().(*syscall.Stat_t).Uid != uint32(os.Geteuid()) {
		return fmt.Errorf("settings directory must be owned by this user: %s", path)
	}
	if err = os.Chmod(path, 0700); err != nil {
		return err
	}
	return PrivateDirectory(path)
}
func PrivateFile(path string) error {
	st, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !st.Mode().IsRegular() || st.Mode().Perm()&0077 != 0 || st.Sys().(*syscall.Stat_t).Uid != uint32(os.Geteuid()) {
		return fmt.Errorf("state file must be private and owned by this user: %s", path)
	}
	return nil
}
