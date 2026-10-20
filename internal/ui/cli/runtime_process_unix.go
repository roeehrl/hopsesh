//go:build !windows

package cli

import (
	"os"
	"os/exec"
	"syscall"
)

func detach(c *exec.Cmd)          { c.SysProcAttr = &syscall.SysProcAttr{Setsid: true} }
func runtimeSignals() []os.Signal { return []os.Signal{os.Interrupt, syscall.SIGTERM} }
