//go:build !windows

package cli

import (
	"os"
	"os/signal"
	"syscall"

	"golang.org/x/term"
)

// watchSize sends the terminal's size now and after each change.
func watchSize(fd int, sizes chan<- [2]int) {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGWINCH)
	for {
		if w, h, err := term.GetSize(fd); err == nil {
			sizes <- [2]int{w, h}
		}
		<-ch
	}
}
