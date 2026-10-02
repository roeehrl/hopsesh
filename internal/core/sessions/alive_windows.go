//go:build windows

package sessions

import (
	"errors"
	"os"
)

// On Windows os.FindProcess opens the process and fails when it does not exist.
func signalZero(p *os.Process) bool {
	_ = p.Release()
	return true
}

// terminate is not offered on Windows: there is no graceful signal for a console
// program, and killing it could cut off its last transcript write.
func terminate(pid int) error {
	return errors.New("hopsesh cannot quit a running session on Windows; type /exit in it, then try again")
}
