//go:build windows

package host

import (
	"context"
	"errors"
	"fmt"
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

// lockHolders is not available on Windows yet.
func lockHolders(context.Context, string) ([]int, error) {
	return nil, fmt.Errorf("%w: finding which program holds a lock on Windows", agent.ErrUnsupported)
}

// processNames is not needed on Windows, where hopsesh quits no session.
func processNames(context.Context, []int) (map[int]string, error) {
	return nil, fmt.Errorf("%w: process names on Windows", agent.ErrUnsupported)
}
