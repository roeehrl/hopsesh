package gui

import (
	"os/exec"
	"unsafe"

	"golang.org/x/sys/windows"
)

// loginShell is PowerShell 7 (pwsh.exe) when it is installed, else Windows PowerShell, as
// the Windows Terminal launches use.
func loginShell() []string {
	for _, name := range []string{"pwsh.exe", "powershell.exe"} {
		if p, err := exec.LookPath(name); err == nil {
			return []string{p, "-NoLogo"}
		}
	}
	return []string{"powershell.exe", "-NoLogo"}
}

// screenReaderRunning reports whether a screen reader runs (SPI_GETSCREENREADER).
func screenReaderRunning() bool {
	const spiGetScreenReader = 0x0046
	var on int32
	r, _, _ := windows.NewLazySystemDLL("user32.dll").NewProc("SystemParametersInfoW").Call(spiGetScreenReader, 0, uintptr(unsafe.Pointer(&on)), 0)
	return r != 0 && on != 0
}
