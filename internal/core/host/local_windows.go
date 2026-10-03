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
	defer windows.CloseHandle(h)
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

// probeLock is not available on Windows yet.
func probeLock(string) agent.LockState { return agent.LockUnknown }

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

