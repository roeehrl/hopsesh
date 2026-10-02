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
