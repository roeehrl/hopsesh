//go:build windows

package term

import (
	"io"
	"os"
	"os/exec"
	"time"

	"github.com/charmbracelet/x/xpty"
	"golang.org/x/sys/windows"
	"golang.org/x/term"
)

// closesOnExit: ConPTY keeps its output open until the pseudo console is closed, which
// flushes what is left.
const closesOnExit = false

func setSession(*exec.Cmd) {}

func afterStart(xpty.Pty) {}

// prepareOutput turns on the console's VT processing for the program's output (Windows
// Terminal has it; a classic console needs it) and returns how to put it back.
func prepareOutput(out io.Writer) func() {
	f, ok := out.(*os.File)
	if !ok {
		return func() {}
	}
	h := windows.Handle(f.Fd())
	var mode uint32
	if windows.GetConsoleMode(h, &mode) != nil {
		return func() {}
	}
	_ = windows.SetConsoleMode(h, mode|windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING|windows.DISABLE_NEWLINE_AUTO_RETURN)
	return func() { _ = windows.SetConsoleMode(h, mode) }
}

// interruptSignals: Ctrl-C reaches the program as a key while the console is raw; one
// that reaches hopsesh anyway is dropped.
func interruptSignals() []os.Signal { return []os.Signal{os.Interrupt} }

func forward(*exec.Cmd, os.Signal) {}

// watchSize follows the console's size (Windows has no SIGWINCH) until stop.
func watchSize(stop <-chan struct{}, out io.Writer, p xpty.Pty) {
	f, ok := out.(*os.File)
	if !ok || !term.IsTerminal(int(f.Fd())) {
		<-stop
		return
	}
	lw, lh, _ := term.GetSize(int(f.Fd()))
	t := time.NewTicker(250 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			if w, h, err := term.GetSize(int(f.Fd())); err == nil && (w != lw || h != lh) {
				lw, lh = w, h
				_ = p.Resize(w, h)
			}
		case <-stop:
			return
		}
	}
}

func signalCode(*os.ProcessState) int { return -1 }
