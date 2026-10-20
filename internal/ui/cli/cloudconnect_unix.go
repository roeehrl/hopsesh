//go:build !windows

package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
)

// startDetachedServe runs this session's connector in its own process session so
// it outlives the hook. It stops at its lease, a superseding claim or VM pause.
func startDetachedServe(directory string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	log, err := os.OpenFile(filepath.Join(directory, "serve.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return err
	}
	defer log.Close()
	c := exec.Command(exe, "cloud-integration", "serve", directory)
	c.Stdout, c.Stderr = log, log
	c.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err = c.Start(); err != nil {
		return err
	}
	// The process ID lets a later disconnect (or a test) stop this connector.
	if err = os.WriteFile(filepath.Join(directory, "serve.pid"), []byte(strconv.Itoa(c.Process.Pid)), 0600); err != nil {
		_ = c.Process.Kill()
		return err
	}
	return c.Process.Release()
}
