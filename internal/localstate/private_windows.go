//go:build windows

package localstate

import (
	"bytes"
	"errors"
	"golang.org/x/sys/windows"
	"unsafe"
)

func privateACL(path string, directory bool) error {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	flags := uint32(windows.FILE_FLAG_OPEN_REPARSE_POINT)
	if directory {
		flags |= windows.FILE_FLAG_BACKUP_SEMANTICS
	}
	h, err := windows.CreateFile(p, windows.READ_CONTROL|windows.WRITE_DAC|windows.WRITE_OWNER|windows.FILE_READ_ATTRIBUTES, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, flags, 0)
	if err != nil {
		return err
	}
	defer func() { _ = windows.CloseHandle(h) }()
	return secureOwnedHandle(h, directory)
}

func secureOwnedHandle(h windows.Handle, directory bool) error {
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(h, &info); err != nil {
		return err
	}
	if info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 || (info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY != 0) != directory {
		return errors.New("state path is not a regular owned file or directory")
	}
	u, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return err
	}
	sd, err := windows.GetSecurityInfo(h, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return err
	}
	owner, _, err := sd.Owner()
	if err != nil {
		return err
	}
	allowed, err := tokenOwnsSID(owner)
	if err != nil {
		return err
	}
	if !allowed {
		return errors.New("state path belongs to another OS user")
	}
	sddl := "D:P(A;;FA;;;" + u.User.Sid.String() + ")"
	if directory {
		sddl = "D:P(A;OICI;FA;;;" + u.User.Sid.String() + ")"
	}
	desired, err := windows.SecurityDescriptorFromString(sddl)
	if err != nil {
		return err
	}
	acl, _, err := desired.DACL()
	if err != nil {
		return err
	}
	// SetSecurityInfo propagates inheritable directory ACEs to existing children.
	// Rewriting an already-private parent for each read makes a large registry
	// quadratic. Recheck the opened object's exact protected ACL every time, but
	// only write when ownership or permissions actually need narrowing.
	if windows.EqualSid(owner, u.User.Sid) && sameProtectedDACL(sd, acl) {
		return nil
	}
	// Elevated Windows tokens can default new objects to Administrators. Narrow
	// that exact token-default owner to the current user as well as the DACL;
	// arbitrary foreign owners are never adopted. Apply to the opened object so
	// a path replacement cannot redirect this ownership/permission change.
	security := windows.SECURITY_INFORMATION(windows.DACL_SECURITY_INFORMATION | windows.PROTECTED_DACL_SECURITY_INFORMATION)
	var newOwner *windows.SID
	if !windows.EqualSid(owner, u.User.Sid) {
		security |= windows.OWNER_SECURITY_INFORMATION
		newOwner = u.User.Sid
	}
	return windows.SetSecurityInfo(h, windows.SE_FILE_OBJECT, security, newOwner, nil, acl, nil)
}

func sameProtectedDACL(sd *windows.SECURITY_DESCRIPTOR, desired *windows.ACL) bool {
	control, _, err := sd.Control()
	if err != nil || control&windows.SE_DACL_PROTECTED == 0 {
		return false
	}
	actual, defaulted, err := sd.DACL()
	if err != nil || defaulted || actual == nil || desired == nil || actual.AceCount != desired.AceCount {
		return false
	}
	for i := uint32(0); i < uint32(actual.AceCount); i++ {
		var a, b *windows.ACCESS_ALLOWED_ACE
		if windows.GetAce(actual, i, &a) != nil || windows.GetAce(desired, i, &b) != nil || a == nil || b == nil || a.Header.AceSize != b.Header.AceSize {
			return false
		}
		if !bytes.Equal(unsafe.Slice((*byte)(unsafe.Pointer(a)), int(a.Header.AceSize)), unsafe.Slice((*byte)(unsafe.Pointer(b)), int(b.Header.AceSize))) {
			return false
		}
	}
	return true
}

func tokenOwnsSID(owner *windows.SID) (bool, error) {
	token := windows.GetCurrentProcessToken()
	u, err := token.GetTokenUser()
	if err != nil {
		return false, err
	}
	if windows.EqualSid(owner, u.User.Sid) {
		return true, nil
	}
	var size uint32
	err = windows.GetTokenInformation(token, windows.TokenOwner, nil, 0, &size)
	if !errors.Is(err, windows.ERROR_INSUFFICIENT_BUFFER) || size < uint32(unsafe.Sizeof(uintptr(0))) || size > 4096 {
		return false, errors.New("cannot identify the token's default object owner")
	}
	buffer := make([]byte, size)
	if err = windows.GetTokenInformation(token, windows.TokenOwner, &buffer[0], size, &size); err != nil {
		return false, err
	}
	defaultOwner := *(**windows.SID)(unsafe.Pointer(&buffer[0]))
	return defaultOwner != nil && windows.EqualSid(owner, defaultOwner), nil
}
func PrivateDirectory(path string) error { return privateACL(path, true) }
func SecureDirectory(path string) error  { return privateACL(path, true) }
func PrivateFile(path string) error      { return privateACL(path, false) }
