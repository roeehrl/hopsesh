//go:build windows

package host

import (
	"context"
	"errors"
	"fmt"

	"golang.org/x/sys/windows"

	"github.com/roeehrl/hopsesh/sdk/agent"
)

// processExists reports whether a process with exactly this id is running. Windows ignores
// the low two bits of an id when opening a process (4242 opens 4240), and a handle can
// outlive its process, so both are checked.
func processExists(pid int) bool {
	if pid <= 0 {
		return false
	}
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	defer func() { _ = windows.CloseHandle(h) }()
	if id, err := windows.GetProcessId(h); err != nil || int(id) != pid {
		return false
	}
	var code uint32
	return windows.GetExitCodeProcess(h, &code) == nil && code == stillActive
}

const stillActive = 259 // STILL_ACTIVE: the process has not exited

// terminate is not offered on Windows: there is no graceful signal for a console
// program, and killing it could cut off its last write.
func terminate(int) error {
	return errors.New("hopsesh cannot quit a running session on Windows; exit it yourself, then try again")
}

// Probe Codex's exclusive writer lock with a nonblocking shared lock on a new,
// read-only handle. Rust File::try_lock uses LockFileEx over the whole file;
// the first byte overlaps that range even for an empty lock file. Never create
// or truncate it, and do not mistake access/sharing/unsupported errors for idle.
func probeLock(p string) (state agent.LockState) {
	if p == "" {
		return agent.LockUnknown
	}
	name, err := windows.UTF16PtrFromString(p)
	if err != nil {
		return agent.LockUnknown
	}
	h, err := windows.CreateFile(name, windows.GENERIC_READ,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		if errors.Is(err, windows.ERROR_FILE_NOT_FOUND) || errors.Is(err, windows.ERROR_PATH_NOT_FOUND) {
			return agent.LockFree
		}
		return agent.LockUnknown
	}
	defer func() {
		if err := windows.CloseHandle(h); err != nil {
			state = agent.LockUnknown
		}
	}()
	if kind, err := windows.GetFileType(h); err != nil || kind != windows.FILE_TYPE_DISK {
		return agent.LockUnknown
	}
	var offset windows.Overlapped
	if err := windows.LockFileEx(h, windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &offset); err != nil {
		if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
			return agent.LockHeld
		}
		return agent.LockUnknown
	}
	if err := windows.UnlockFileEx(h, 0, 1, 0, &offset); err != nil {
		return agent.LockUnknown
	}
	return agent.LockFree
}

// lockHolders is not available on Windows yet.
func lockHolders(context.Context, string) ([]int, error) {
	return nil, fmt.Errorf("%w: finding which program holds a lock on Windows", agent.ErrUnsupported)
}

// processNames is not needed on Windows, where hopsesh quits no session.
func processNames(context.Context, []int) (map[int]string, error) {
	return nil, fmt.Errorf("%w: process names on Windows", agent.ErrUnsupported)
}

// shortPath is a path's 8.3 short form on this machine ("" when it has none).
func shortPath(p string) string {
	long, err := windows.UTF16PtrFromString(p)
	if err != nil {
		return ""
	}
	buf := make([]uint16, 1024)
	n, err := windows.GetShortPathName(long, &buf[0], uint32(len(buf)))
	if err != nil || n == 0 || int(n) > len(buf) {
		return ""
	}
	return windows.UTF16ToString(buf[:n])
}
