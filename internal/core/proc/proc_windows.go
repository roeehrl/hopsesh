package proc

import (
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

// noConsole: this process has no console window (a window program), so a console child
// would get a new, visible one.
var noConsole = func() bool {
	h, _, _ := windows.NewLazySystemDLL("kernel32.dll").NewProc("GetConsoleWindow").Call()
	return h == 0
}()

func hide(cmd *exec.Cmd) {
	if !noConsole {
		return
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= windows.CREATE_NO_WINDOW
	cmd.SysProcAttr.HideWindow = true
}
