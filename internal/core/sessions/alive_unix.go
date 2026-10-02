//go:build !windows

package sessions

import (
	"errors"
	"os"
	"syscall"
)

func signalZero(p *os.Process) bool {
	err := p.Signal(syscall.Signal(0))
	return err == nil || errors.Is(err, syscall.EPERM)
}

// terminate sends SIGTERM, which Claude Code handles by exiting cleanly.
func terminate(pid int) error {
	return syscall.Kill(pid, syscall.SIGTERM)
}
