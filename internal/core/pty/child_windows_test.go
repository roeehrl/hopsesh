//go:build windows

package pty_test

import (
	"os"
	"time"

	"golang.org/x/sys/windows"
)

// prepareConsole turns on the console's processing of control sequences in what the
// program prints (a pseudoconsole's program sets it as any console program does).
func prepareConsole() {
	h := windows.Handle(os.Stdout.Fd())
	var mode uint32
	if windows.GetConsoleMode(h, &mode) == nil {
		_ = windows.SetConsoleMode(h, mode|windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING|windows.DISABLE_NEWLINE_AUTO_RETURN)
	}
}

// watchSize polls the console's size (Windows has no SIGWINCH).
func watchSize(size func() string) <-chan os.Signal {
	ch := make(chan os.Signal, 4)
	go func() {
		last := size()
		for range time.Tick(100 * time.Millisecond) {
			if s := size(); s != last {
				last = s
				ch <- os.Interrupt
			}
		}
	}()
	return ch
}
