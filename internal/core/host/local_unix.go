//go:build !windows

package host

import (
	"errors"
	"os"
	"syscall"

	"github.com/roeehrl/hopsesh/sdk/agent"
)

func processExists(pid int) bool {
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = p.Signal(syscall.Signal(0))
	return err == nil || errors.Is(err, syscall.EPERM)
}

// terminate sends SIGTERM, which lets an agent finish writing its session and exit.
func terminate(pid int) error { return syscall.Kill(pid, syscall.SIGTERM) }

// probeLock tries to take a shared lock without blocking and releases it at once: a
// writer holding an exclusive lock makes it fail. The file is opened read-only and never
// created.
func probeLock(p string) agent.LockState {
	f, err := os.Open(p)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return agent.LockFree
		}
		return agent.LockUnknown
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_SH|syscall.LOCK_NB); err != nil {
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return agent.LockHeld
		}
		return agent.LockUnknown
	}
	_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	return agent.LockFree
}
