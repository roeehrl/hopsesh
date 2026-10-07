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
	h, err := windows.CreateFile(p, windows.GENERIC_READ, windows.FILE_SHARE_READ, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
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
			u, ue := windows.GetCurrentProcessToken().GetTokenUser()
			if e != nil {
				err = e
			} else if ue != nil {
				err = ue
			} else if !windows.EqualSid(owner, u.User.Sid) {
				err = errors.New("state file belongs to another OS user")
			}
		}
	}
	if err != nil {
		f.Close()
		return nil, err
	}
	// This handle denies replacement while the existing ownership/DACL helper
	// restricts the same object. No bytes are read until that succeeds.
	if private {
		err = PrivateFile(path)
	}
	if err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}
