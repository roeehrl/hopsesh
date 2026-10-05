package main

import (
	"os"

	"golang.org/x/sys/windows"
)

// prepareConsole turns on the console's processing of control sequences in output.
func prepareConsole() {
	h := windows.Handle(os.Stdout.Fd())
	var mode uint32
	if windows.GetConsoleMode(h, &mode) == nil {
		_ = windows.SetConsoleMode(h, mode|windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING|windows.DISABLE_NEWLINE_AUTO_RETURN)
	}
}
