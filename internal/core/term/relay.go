// Package term runs a vendor's interactive command in a pseudo-terminal of its own,
// attached to the user's terminal (a relay): the user sees the program and answers what it
// asks, and hopsesh only watches what it prints and how it ends.
//
// The rules (docs/design.md §15, "Terminal steps"): the program's input is fed only by the
// user's keyboard, paste and the terminal's own answers to its queries; hopsesh writes no
// byte into it. The program is started from its argument list, never typed into a shell.
// What it prints goes to the user unchanged and, passively, into a bounded buffer that
// only the module's reader looks at (the session's link, a refusal); nothing of it is
// logged, journaled or kept once the step is read.
package term

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"sync"
	"time"

	"github.com/charmbracelet/x/xpty"
	"github.com/muesli/cancelreader"
	"golang.org/x/term"

	"github.com/roeehrl/hopsesh/internal/core/winshim"
)

// ErrNoPseudoTerminal is returned when this system cannot give the program a
// pseudo-terminal (a Windows older than 10 1809 has no ConPTY). The caller can run the
// program on the terminal directly instead, without watching it.
var ErrNoPseudoTerminal = errors.New("no pseudo-terminal on this system")

// Default size when neither end is a terminal (tests, a window that reports none).
const (
	defaultWidth  = 100
	defaultHeight = 30
)

// Relay runs one program under a pseudo-terminal attached to the user's terminal. It
// implements Bubble Tea's ExecCommand (Run, SetStdin, SetStdout, SetStderr), so a terminal
// UI can hand it the terminal with tea.Exec and get it back afterwards.
type Relay struct {
	Argv []string
	Dir  string
	Env  []string // the program's whole environment
	// Capture gets a copy of everything the program prints (nil: nothing is kept).
	Capture *Capture
	// Width and Height are the pseudo-terminal's size when the user's side is not a
	// terminal (0: a default); a terminal's own size wins.
	Width, Height int

	in     io.Reader
	out    io.Writer
	errOut io.Writer

	// Code is the program's exit code once Run returned (-1: unknown); Cols, the width it
	// started with.
	Code int
	Cols int
}

// New is a relay for argv in dir with the environment env.
func New(argv []string, dir string, env []string) *Relay {
	return &Relay{Argv: argv, Dir: dir, Env: env, Code: -1}
}

// SetStdin sets where the user's keys come from (default: os.Stdin).
func (r *Relay) SetStdin(in io.Reader) { r.in = in }

// SetStdout sets the user's terminal (default: os.Stdout).
func (r *Relay) SetStdout(out io.Writer) { r.out = out }

// SetStderr is unused: the program's errors come through its terminal like the rest.
func (r *Relay) SetStderr(w io.Writer) { r.errOut = w }

// Run starts the program, relays until it ends, and restores the user's terminal. An
// exit code other than 0 is not an error (Code has it); an error means the program could
// not be run, or the relay broke.
func (r *Relay) Run() error {
	if len(r.Argv) == 0 {
		return errors.New("nothing to run")
	}
	// On Windows, an npm command shim (claude.cmd) runs as the program behind it.
	argv, err := winshim.Argv(r.Argv)
	if err != nil {
		return fmt.Errorf("starting %s: %w", r.Argv[0], err)
	}
	in, out := r.in, r.out
	if in == nil {
		in = os.Stdin
	}
	if out == nil {
		out = os.Stdout
	}
	w, h := r.size(out, in)
	r.Cols = w
	pty, err := xpty.NewPty(w, h)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrNoPseudoTerminal, err)
	}
	defer pty.Close()

	cmd := exec.Command(argv[0], argv[1:]...) //nolint:gosec // a module's driver, by its argument list
	cmd.Dir, cmd.Env = r.Dir, r.Env
	setSession(cmd)
	if err := pty.Start(cmd); err != nil {
		return fmt.Errorf("starting %s: %w", r.Argv[0], err)
	}
	afterStart(pty)

	// The user's terminal: raw, so every key (Ctrl-C, arrows, Enter) goes to the program,
	// which sets its own terminal's modes.
	restore := func() {}
	if f, ok := in.(*os.File); ok && term.IsTerminal(int(f.Fd())) {
		if st, err := term.MakeRaw(int(f.Fd())); err == nil {
			restore = func() { _ = term.Restore(int(f.Fd()), st) }
		}
	}
	restoreOut := prepareOutput(out)
	defer func() {
		resetModes(out)
		restoreOut()
		restore()
	}()

	// Signals to hopsesh while the program runs go to the program (it has the terminal).
	sigs := make(chan os.Signal, 4)
	signal.Notify(sigs, interruptSignals()...)
	defer signal.Stop(sigs)

	// What the program prints: to the user, and to the capture.
	printed := make(chan struct{})
	go func() {
		defer close(printed)
		buf := make([]byte, 32<<10)
		for {
			n, err := pty.Read(buf)
			if n > 0 {
				_, _ = out.Write(buf[:n])
				if r.Capture != nil {
					_, _ = r.Capture.Write(buf[:n])
				}
			}
			if err != nil {
				return
			}
		}
	}()

	// What the user types (and the terminal's answers to the program's queries): to the
	// program. A cancelable reader, so the copy stops when the program ends and the next
	// key goes to whoever reads the terminal then.
	src := in
	var cancel func() bool
	if f, ok := in.(*os.File); ok && term.IsTerminal(int(f.Fd())) {
		if cr, err := cancelreader.NewReader(f); err == nil {
			src, cancel = cr, cr.Cancel
			defer cr.Close()
		}
	}
	typed := make(chan struct{})
	go func() {
		defer close(typed)
		_, _ = io.Copy(pty, src)
	}()

	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		watchSize(stop, out, pty)
	}()
	go func() {
		for {
			select {
			case s := <-sigs:
				forward(cmd, s)
			case <-stop:
				return
			}
		}
	}()

	werr := xpty.WaitProcess(context.Background(), cmd)
	r.Code = exitCode(cmd, werr)
	close(stop)
	wg.Wait()

	// The rest of what it printed: until the pseudo-terminal says it is done, briefly.
	drained := func(d time.Duration) bool {
		select {
		case <-printed:
			return true
		case <-time.After(d):
			return false
		}
	}
	if !closesOnExit || !drained(1500*time.Millisecond) {
		_ = pty.Close()
		drained(2 * time.Second)
	}
	if cancel != nil {
		cancel()
		select {
		case <-typed:
		case <-time.After(time.Second):
		}
	}
	return nil
}

// size is the user's terminal's size, else the relay's default.
func (r *Relay) size(out io.Writer, in io.Reader) (int, int) {
	for _, x := range []any{out, in} {
		if f, ok := x.(*os.File); ok && term.IsTerminal(int(f.Fd())) {
			if w, h, err := term.GetSize(int(f.Fd())); err == nil && w > 0 && h > 0 {
				return w, h
			}
		}
	}
	w, h := r.Width, r.Height
	if w <= 0 {
		w = defaultWidth
	}
	if h <= 0 {
		h = defaultHeight
	}
	return w, h
}

// resetModes leaves the user's terminal as a shell expects it, whatever the program left
// on: the cursor shown, bracketed paste, focus reports, mouse reports and the kitty
// keyboard protocol off. Only on a terminal.
func resetModes(out io.Writer) {
	if f, ok := out.(*os.File); !ok || !term.IsTerminal(int(f.Fd())) {
		return
	}
	_, _ = io.WriteString(out, "\x1b[?25h\x1b[?2004l\x1b[?1004l\x1b[?1000l\x1b[?1002l\x1b[?1003l\x1b[?1006l\x1b[<u")
}

// exitCode is the program's exit code: 128 plus the signal for one a signal ended, -1 when
// it is unknown.
func exitCode(cmd *exec.Cmd, err error) int {
	if cmd.ProcessState == nil {
		if err != nil {
			return -1
		}
		return 0
	}
	if c := cmd.ProcessState.ExitCode(); c >= 0 {
		return c
	}
	return signalCode(cmd.ProcessState)
}
