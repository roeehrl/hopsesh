//go:build !windows

package main

import (
	"os/exec"
	"syscall"
)

// sleeper is a process that runs until killed, in its own session, standing in for a
// running agent.
func sleeper() *exec.Cmd {
	cmd := exec.Command("sleep", "infinity")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	return cmd
}
