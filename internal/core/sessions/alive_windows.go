//go:build windows

package sessions

import "os"

// On Windows os.FindProcess opens the process and fails when it does not exist.
func signalZero(p *os.Process) bool {
	_ = p.Release()
	return true
}
