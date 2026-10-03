//go:build !windows

package main

import (
	"os/exec"
	"syscall"
)

// detach starts cmd in its own session, so it keeps running after this program (and the
// CI step) ends.
func detach(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true} }
