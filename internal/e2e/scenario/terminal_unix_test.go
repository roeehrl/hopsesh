//go:build !windows

package scenario

import (
	"os/exec"
	"syscall"
)

// setCtty gives the program the pseudo-terminal as its controlling terminal.
func setCtty(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true} }
