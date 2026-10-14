package cli

import (
	"time"

	"golang.org/x/term"
)

// watchSize sends the console's size now and after each change (Windows has no signal
// for it).
func watchSize(fd int, sizes chan<- [2]int) {
	last := [2]int{}
	for {
		if w, h, err := term.GetSize(fd); err == nil && [2]int{w, h} != last {
			last = [2]int{w, h}
			sizes <- last
		}
		time.Sleep(300 * time.Millisecond)
	}
}
