//go:build windows

package localstate

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

// OpenPrivateAppend refuses reparse points and confines the opened file to this SID.
func OpenPrivateAppend(path string) (*os.File, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	h, err := windows.CreateFile(p, windows.FILE_APPEND_DATA|windows.GENERIC_READ, windows.FILE_SHARE_READ, nil, windows.OPEN_ALWAYS, windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(h), path)
	st, err := f.Stat()
	if err == nil && (!st.Mode().IsRegular() || st.Mode()&os.ModeSymlink != 0) {
		err = errors.New("log must be a regular file")
	}
	if err == nil {
		err = PrivateFile(path)
	}
	if err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}
