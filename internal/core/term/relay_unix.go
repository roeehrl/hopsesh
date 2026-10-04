//go:build !windows

package term

import (
	"io"
	"os"
	"os/exec"
	"os/signal"
	"syscall"

	"github.com/charmbracelet/x/xpty"
	"golang.org/x/term"
)

// closesOnExit: a Unix pseudo-terminal reports the end of the output once the program
// (and whatever kept its terminal open) is gone.
const closesOnExit = true

// setSession makes the program a session leader with the pseudo-terminal as its
// controlling terminal, so Ctrl-C reaches it as SIGINT and it can open /dev/tty.
func setSession(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true}
}

// afterStart closes this process's copy of the terminal's program end, so reading ends
// when the program does.
func afterStart(p xpty.Pty) {
	if u, ok := p.(*xpty.UnixPty); ok {
		_ = u.Slave().Close()
	}
}

func prepareOutput(io.Writer) func() { return func() {} }

func interruptSignals() []os.Signal {
	return []os.Signal{os.Interrupt, syscall.SIGTERM, syscall.SIGHUP}
}

// forward passes a signal to the program's process group.
func forward(cmd *exec.Cmd, s os.Signal) {
	if cmd.Process == nil {
		return
	}
	if sig, ok := s.(syscall.Signal); ok {
		_ = syscall.Kill(-cmd.Process.Pid, sig)
	}
}

// watchSize follows the user's terminal's size (SIGWINCH) until stop.
func watchSize(stop <-chan struct{}, out io.Writer, p xpty.Pty) {
	f, ok := out.(*os.File)
	if !ok || !term.IsTerminal(int(f.Fd())) {
		<-stop
		return
	}
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGWINCH)
	defer signal.Stop(ch)
	for {
		select {
		case <-ch:
			if w, h, err := term.GetSize(int(f.Fd())); err == nil {
				_ = p.Resize(w, h)
			}
		case <-stop:
			return
		}
	}
}

// signalCode is 128 plus the signal that ended the program.
func signalCode(ps *os.ProcessState) int {
	if ws, ok := ps.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		return 128 + int(ws.Signal())
	}
	return -1
}
