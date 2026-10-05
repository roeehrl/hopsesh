//go:build !windows

package pty_test

import (
	"os"
	"os/signal"
	"syscall"
)

func prepareConsole() {}

// watchSize signals the terminal's size changes (SIGWINCH).
func watchSize(func() string) <-chan os.Signal {
	ch := make(chan os.Signal, 4)
	signal.Notify(ch, syscall.SIGWINCH)
	return ch
}
