package secrets

import (
	"errors"
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Windows Credential Manager: a generic credential per account, named "hopsesh:<account>",
// kept for this user on this machine.

var (
	advapi32   = windows.NewLazySystemDLL("advapi32.dll")
	credWrite  = advapi32.NewProc("CredWriteW")
	credRead   = advapi32.NewProc("CredReadW")
	credDelete = advapi32.NewProc("CredDeleteW")
	credFree   = advapi32.NewProc("CredFree")
)

// credential is CREDENTIALW.
type credential struct {
	Flags              uint32
	Type               uint32
	TargetName         *uint16
	Comment            *uint16
	LastWritten        windows.Filetime
	CredentialBlobSize uint32
	CredentialBlob     *byte
	Persist            uint32
	AttributeCount     uint32
	Attributes         uintptr
	TargetAlias        *uint16
	UserName           *uint16
}

const (
	credTypeGeneric          = 1
	credPersistLocalMachine  = 2
	errorNotFound            = windows.ERROR_NOT_FOUND
	maxCredentialBlobBytes   = 5 * 512
	credentialTargetPrefix   = service + ":"
	credentialCommentForUser = "Machine password remembered by hopsesh"
)

const (
	storeName = "Windows Credential Manager"
	service   = "hopsesh"
)

func available() bool { return credWrite.Find() == nil }

func target(account string) (*uint16, error) {
	return windows.UTF16PtrFromString(credentialTargetPrefix + account)
}

func set(account, password string) error {
	if len(password) > maxCredentialBlobBytes {
		return errors.New("the password is too long for Windows Credential Manager")
	}
	name, err := target(account)
	if err != nil {
		return err
	}
	user, _ := windows.UTF16PtrFromString(account)
	comment, _ := windows.UTF16PtrFromString(credentialCommentForUser)
	blob := []byte(password)
	c := credential{Type: credTypeGeneric, TargetName: name, Comment: comment, Persist: credPersistLocalMachine, UserName: user,
		CredentialBlobSize: uint32(len(blob))}
	if len(blob) > 0 {
		c.CredentialBlob = &blob[0]
	}
	if r, _, err := credWrite.Call(uintptr(unsafe.Pointer(&c)), 0); r == 0 {
		return fmt.Errorf("Windows Credential Manager: %w", err)
	}
	return nil
}

func get(account string) (string, bool) {
	name, err := target(account)
	if err != nil {
		return "", false
	}
	var p *credential
	if r, _, _ := credRead.Call(uintptr(unsafe.Pointer(name)), credTypeGeneric, 0, uintptr(unsafe.Pointer(&p))); r == 0 || p == nil {
		return "", false
	}
	defer credFree.Call(uintptr(unsafe.Pointer(p)))
	if p.CredentialBlobSize == 0 || p.CredentialBlob == nil {
		return "", true
	}
	return string(unsafe.Slice(p.CredentialBlob, p.CredentialBlobSize)), true
}

func del(account string) error {
	name, err := target(account)
	if err != nil {
		return err
	}
	if r, _, err := credDelete.Call(uintptr(unsafe.Pointer(name)), credTypeGeneric, 0); r == 0 && !errors.Is(err, errorNotFound) {
		return fmt.Errorf("Windows Credential Manager: %w", err)
	}
	return nil
}
