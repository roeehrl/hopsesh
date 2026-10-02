//go:build windows

package host

import (
	"errors"
	"os"

	"github.com/roeehrl/hopsesh/sdk/agent"
)

// On Windows os.FindProcess opens the process and fails when it does not exist.
func processExists(pid int) bool {
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	_ = p.Release()
	return true
}

// terminate is not offered on Windows: there is no graceful signal for a console
// program, and killing it could cut off its last write.
func terminate(int) error {
	return errors.New("hopsesh cannot quit a running session on Windows; exit it yourself, then try again")
}

// probeLock is not available on Windows yet.
func probeLock(string) agent.LockState { return agent.LockUnknown }
