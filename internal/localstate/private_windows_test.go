//go:build windows

package localstate

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

func assertCurrentOwner(t *testing.T, path string) {
	t.Helper()
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	owner, _, err := sd.Owner()
	if err != nil {
		t.Fatal(err)
	}
	u, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	if !windows.EqualSid(owner, u.User.Sid) {
		t.Fatal("private state did not narrow token-default ownership to the current user")
	}
	acl, present, err := sd.DACL()
	if err != nil || !present || acl == nil || acl.AceCount != 1 {
		t.Fatal("private state does not have one explicit user ACL", err)
	}
}

func TestPrivateWindowsStateNormalizesTokenDefaultOwner(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "private")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := PrivateDirectory(dir); err != nil {
		t.Fatal(err)
	}
	assertCurrentOwner(t, dir)
	file := filepath.Join(dir, "state.json")
	if err := os.WriteFile(file, []byte("private"), 0600); err != nil {
		t.Fatal(err)
	}
	if data, err := ReadPrivateFile(file, 7); err != nil || string(data) != "private" {
		t.Fatal("newly created private file is unreadable", err)
	}
	assertCurrentOwner(t, file)
	log := filepath.Join(dir, "runtime.log")
	f, err := OpenPrivateAppend(log)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.Write([]byte("private"))
	_ = f.Close()
	if err != nil {
		t.Fatal(err)
	}
	assertCurrentOwner(t, log)
	lock := filepath.Join(dir, "state.lock")
	l, err := TryLock(lock)
	if err != nil {
		t.Fatal(err)
	}
	_ = l.Close()
	assertCurrentOwner(t, lock)
}

func TestPrivateWindowsOwnerPolicyRejectsUnrelatedSID(t *testing.T) {
	// Anonymous is neither a user token nor an eligible default object owner.
	foreign, err := windows.StringToSid("S-1-5-7")
	if err != nil {
		t.Fatal(err)
	}
	if allowed, err := tokenOwnsSID(foreign); err != nil || allowed {
		t.Fatal("foreign owner accepted", err)
	}
}

func TestPrivateWindowsReadHandlePreventsReplacement(t *testing.T) {
	file := filepath.Join(t.TempDir(), "state.json")
	if err := os.WriteFile(file, []byte("private"), 0600); err != nil {
		t.Fatal(err)
	}
	f, err := openOwnedRead(file, true)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := os.Rename(file, file+".replaced"); err == nil {
		t.Fatal("opened private object could be replaced while its owner and ACL were checked")
	}
	assertCurrentOwner(t, file)
}
