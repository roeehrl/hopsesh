// Package termfake is a stand-in program for the terminal's tests: it runs in a tab, asks
// the terminal what it is (DA1 and DA2, as Claude Code does), says what came back, and then
// does what keys ask, so a test can drive a tab end to end through the real window.
//
//	S         print the terminal's size ("size=COLSxROWS")
//	B         ring the bell (the tab waits for the user)
//	L         print an https link
//	O         print an OSC 8 link to a vscode:// address (which must stay blocked)
//	0-9       exit with that code
//	Return    a new line
//	anything else is echoed back as "typed=<text>" once Return comes
//
// Started as "termfake trust", it first asks Claude Code's workspace-trust question and
// waits for 1 or Return. "quiet" (in any order with "trust") still asks the terminal but
// prints none of its own diagnostics, for screenshots.
package termfake

import (
	"fmt"
	"os"
	"strings"
	"time"

	"golang.org/x/term"
)

// Main runs the stand-in and returns its exit code.
func Main() int {
	in, out := int(os.Stdin.Fd()), int(os.Stdout.Fd())
	if !term.IsTerminal(in) {
		fmt.Println("termfake: no terminal")
		return 2
	}
	st, err := term.MakeRaw(in)
	if err != nil {
		fmt.Println("termfake:", err)
		return 2
	}
	defer func() { _ = term.Restore(in, st) }()
	keys := make(chan byte, 256)
	go func() {
		b := make([]byte, 64)
		for {
			n, err := os.Stdin.Read(b)
			for _, c := range b[:n] {
				keys <- c
			}
			if err != nil {
				close(keys)
				return
			}
		}
	}()
	say := func(format string, a ...any) { fmt.Printf(format+"\r\n", a...) }
	ask := func(q string) string {
		fmt.Print(q)
		reply := ""
		deadline := time.After(3 * time.Second)
		for {
			select {
			case k, ok := <-keys:
				if !ok {
					return reply
				}
				reply += string(k)
				if len(reply) > 2 && k >= 0x40 && k <= 0x7e {
					return reply
				}
			case <-deadline:
				return reply
			}
		}
	}
	opts := map[string]bool{}
	for _, a := range os.Args[1:] {
		opts[a] = true
	}
	quiet := opts["quiet"]
	note := func(format string, a ...any) {
		if !quiet {
			say(format, a...)
		}
	}
	note("termfake: da1=%q", ask("\x1b[c"))
	note("termfake: da2=%q", ask("\x1b[>c"))
	w, h, _ := term.GetSize(out)
	note("termfake: size=%dx%d", w, h)
	if opts["trust"] {
		say("\x1b[1mQuick safety check: Is this a project you created or one you trust?\x1b[22m")
		say(" ❯ 1. Yes, I trust this folder")
		say("   2. No, exit")
		for k := range keys {
			if k == '1' || k == '\r' {
				note("termfake: trusted")
				break
			}
			if k == '2' {
				return 1
			}
		}
	}
	note("termfake: ready")
	var line strings.Builder
	for k := range keys {
		switch {
		case k == 'S':
			w, h, _ := term.GetSize(out)
			say("size=%dx%d", w, h)
		case k == 'B':
			fmt.Print("\a")
		case k == 'L':
			say("see https://example.com/hopsesh-test?x=1 for more")
		case k == 'O':
			say("\x1b]8;;vscode://file/etc/passwd\x1b\\an app link\x1b]8;;\x1b\\")
		case k >= '0' && k <= '9':
			say("termfake: exiting with %c", k)
			return int(k - '0')
		case k == '\r' || k == '\n':
			say("typed=%s", line.String())
			line.Reset()
		case k == 0x1b:
			// Shift+Return's ESC CR, as Claude Code reads a new line.
			select {
			case k2 := <-keys:
				if k2 == '\r' {
					say("newline")
					continue
				}
				line.WriteByte(k)
				line.WriteByte(k2)
			case <-time.After(200 * time.Millisecond):
			}
		default:
			line.WriteByte(k)
		}
	}
	return 0
}
