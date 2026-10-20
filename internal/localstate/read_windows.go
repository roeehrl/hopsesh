//go:build windows

package localstate

import (
	"errors"
	"golang.org/x/sys/windows"
	"os"
)

func openOwnedRead(path string, private bool) (*os.File, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	rights := uint32(windows.GENERIC_READ)
	if private {
		rights |= windows.READ_CONTROL | windows.WRITE_DAC | windows.WRITE_OWNER
	}
	h, err := windows.CreateFile(p, rights, windows.FILE_SHARE_READ, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(h), path)
	st, err := f.Stat()
	if err == nil && (!st.Mode().IsRegular() || st.Mode()&os.ModeSymlink != 0) {
		err = errors.New("state file must be a regular file")
	}
	if err == nil {
		sd, e := windows.GetSecurityInfo(h, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION)
		if e != nil {
			err = e
		} else {
			owner, _, e := sd.Owner()
			if e != nil {
				err = e
			} else {
				var allowed bool
				allowed, err = tokenOwnsSID(owner)
				if err == nil && !allowed {
					err = errors.New("state file belongs to another OS user")
				}
			}
		}
	}
	if err != nil {
		f.Close()
		return nil, err
	}
	// This handle denies replacement and narrows this exact object's owner/DACL.
	// Vendor reads remain passive and do not change native file permissions.
	if private {
		err = secureOwnedHandle(h, false)
	}
	if err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}
