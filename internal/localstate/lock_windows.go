//go:build windows

package localstate

import (
	"fmt"
	"golang.org/x/sys/windows"
	"os"
)

func openLock(path string) (*os.File, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	h, err := windows.CreateFile(p, windows.GENERIC_READ|windows.GENERIC_WRITE, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_ALWAYS, windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(h), path)
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	if !st.Mode().IsRegular() || st.Mode()&os.ModeSymlink != 0 {
		f.Close()
		return nil, fmt.Errorf("lock must be a regular file: %s", path)
	}
	return f, nil
}
func lockFile(f *os.File) error {
	var ov windows.Overlapped
	return windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &ov)
}
func busy(err error) bool { return err == windows.ERROR_LOCK_VIOLATION }
