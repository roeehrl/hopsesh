// Command termprobe is the program of a terminal check (the app's terminal window in a test
// build, see cmd/hopsesh-app/testport_on.go): in raw mode it prints its terminal's size,
// asks the terminal what it is (DA1) as agent programs do at start, prints the answer,
// and ends with 0 when one came (2 when none did within thirty seconds).
package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	"golang.org/x/term"
)

func main() {
	prepareConsole()
	in, out := int(os.Stdin.Fd()), int(os.Stdout.Fd())
	st, err := term.MakeRaw(in)
	if err != nil {
		fmt.Printf("termprobe: no raw mode: %v\r\n", err)
		os.Exit(1)
	}
	w, h, _ := term.GetSize(out)
	fmt.Printf("termprobe: size=%dx%d\r\n", w, h)
	fmt.Print("\x1b[c")
	got := make(chan byte, 64)
	go func() {
		b := make([]byte, 64)
		for {
			n, err := os.Stdin.Read(b)
			for _, c := range b[:n] {
				got <- c
			}
			if err != nil {
				close(got)
				return
			}
		}
	}()
	reply := ""
	// The child starts before the terminal WebView and its stream attach. A cold
	// WebView2 startup on a hosted runner can exceed five seconds; allow the
	// buffered query to reach the actual emulator, within the app's one-minute
	// test deadline. A missing response still fails, without retrying the query.
	timeout := time.After(30 * time.Second)
wait:
	for !strings.HasSuffix(reply, "c") {
		select {
		case c, ok := <-got:
			if !ok {
				break wait
			}
			reply += string(c)
		case <-timeout:
			break wait
		}
	}
	_ = term.Restore(in, st)
	fmt.Printf("termprobe: da1=%q\r\n", reply)
	if !strings.HasPrefix(reply, "\x1b[?") {
		os.Exit(2)
	}
}
