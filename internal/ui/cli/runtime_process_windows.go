//go:build windows

package cli

import (
	"os"
	"os/exec"
	"syscall"
)

func detach(c *exec.Cmd) {
	c.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x00000200 | 0x00000008, HideWindow: true}
}
func runtimeSignals() []os.Signal { return []os.Signal{os.Interrupt} }
