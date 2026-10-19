package serve

import (
	"io/fs"
	"net"
	"os"

	"golang.org/x/sys/windows"
)

func lockFile(f *os.File) error {
	var overlapped windows.Overlapped
	return windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &overlapped)
}

// The folder is under the user's own profile, which other users cannot open.
func checkOwner(string, fs.FileInfo) error { return nil }

func sameUser(net.Conn) bool { return true }
