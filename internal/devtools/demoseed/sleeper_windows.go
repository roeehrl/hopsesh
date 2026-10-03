package main

import (
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

// sleeper is a process that runs until killed, detached, standing in for a running agent.
func sleeper() *exec.Cmd {
	cmd := exec.Command("powershell.exe", "-NoProfile", "-Command", "Start-Sleep -Seconds 2147483")
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NEW_PROCESS_GROUP | windows.DETACHED_PROCESS}
	return cmd
}
