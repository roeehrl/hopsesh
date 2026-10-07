//go:build windows

package localstate

import (
	"errors"
	"golang.org/x/sys/windows"
	"os"
)

func privateACL(path string, directory bool) error {
	st, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if st.Mode()&os.ModeSymlink != 0 || directory && !st.IsDir() || !directory && !st.Mode().IsRegular() {
		return errors.New("state path is not a regular owned file or directory")
	}
	u, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return err
	}
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION)
	if err != nil {
		return err
	}
	owner, _, err := sd.Owner()
	if err != nil {
		return err
	}
	if !windows.EqualSid(owner, u.User.Sid) {
		return errors.New("state path belongs to another OS user")
	}
	sddl := "D:P(A;;FA;;;" + u.User.Sid.String() + ")"
	if directory {
		sddl = "D:P(A;OICI;FA;;;" + u.User.Sid.String() + ")"
	}
	sd, err = windows.SecurityDescriptorFromString(sddl)
	if err != nil {
		return err
	}
	acl, _, err := sd.DACL()
	if err != nil {
		return err
	}
	return windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, acl, nil)
}
func PrivateDirectory(path string) error { return privateACL(path, true) }
func SecureDirectory(path string) error  { return privateACL(path, true) }
func PrivateFile(path string) error      { return privateACL(path, false) }
