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
