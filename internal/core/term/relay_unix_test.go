//go:build !windows

package term_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/charmbracelet/x/vt"
	"github.com/charmbracelet/x/xpty"
	xterm "golang.org/x/term"

	"github.com/roeehrl/hopsesh/internal/core/term"
)

// The test binary plays three parts: the test; "relay", a hopsesh that relays a program
// in its terminal (TERM_TEST_ROLE=relay); and "child", the program relayed: a small
// interactive one that asks the terminal what it is (DA1), shows its size, follows a
// resize, echoes keys in raw mode and quits on q (CHILD_MODE=interactive), or one that
// sleeps in a cooked terminal until Ctrl-C ends it (CHILD_MODE=sleep).
func TestMain(m *testing.M) {
	switch os.Getenv("TERM_TEST_ROLE") {
	case "relay":
		os.Exit(relayMain())
	case "child":
		os.Exit(childMain())
	}
	os.Exit(m.Run())
}

func relayMain() int {
	self, _ := os.Executable()
	env := append(os.Environ(), "TERM_TEST_ROLE=child")
	r := term.New([]string{self}, "", env)
	r.Capture = &term.Capture{}
	if err := r.Run(); err != nil {
		fmt.Printf("relay error: %v\r\n", err)
		return 1
	}
	text := r.Capture.Text()
	fmt.Printf("\r\nrelay: code=%d width=%d captured-ready=%v\r\n", r.Code, r.Cols, strings.Contains(text, "ready"))
	return 0
}

func childMain() int {
	size := func() string {
		w, h, _ := xterm.GetSize(int(os.Stdout.Fd()))
		return fmt.Sprintf("size=%dx%d", w, h)
	}
	if os.Getenv("CHILD_MODE") == "sleep" {
		fmt.Print("sleeping\r\n")
		time.Sleep(30 * time.Second)
		return 0
	}
	st, err := xterm.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		fmt.Printf("no raw mode: %v\r\n", err)
		return 1
	}
	defer xterm.Restore(int(os.Stdin.Fd()), st)
	fmt.Printf("%s ready\r\n", size())
	keys := make(chan byte, 16)
	go func() {
		b := make([]byte, 1)
		for {
			if n, err := os.Stdin.Read(b); n == 1 {
				keys <- b[0]
			} else if err != nil {
				close(keys)
				return
			}
		}
	}()
	fmt.Print("\x1b[c")
	reply := ""
	timeout := time.After(3 * time.Second)
da1:
	for !strings.HasSuffix(reply, "c") {
		select {
		case k := <-keys:
			reply += string(k)
		case <-timeout:
			break da1
		}
	}
	fmt.Printf("da1=%v\r\n", strings.HasPrefix(reply, "\x1b[?"))
	winch := make(chan os.Signal, 1)
	signal.Notify(winch, syscall.SIGWINCH)
	for {
		select {
		case <-winch:
			fmt.Printf("resized %s\r\n", size())
		case k, ok := <-keys:
			if !ok {
				return 4
			}
			if k == 'q' {
				return 3
			}
			fmt.Printf("got:%02x\r\n", k)
		}
	}
}

// screen is a user's terminal: a pseudo-terminal running the relay part, read through an
// emulator that answers the program's queries as a terminal does.
type screen struct {
	t   *testing.T
	pty xpty.Pty
	emu *vt.Emulator
	mu  sync.Mutex
	cmd *exec.Cmd
}

func startScreen(t *testing.T, mode string) *screen {
	t.Helper()
	pty, err := xpty.NewPty(80, 24)
	if err != nil {
		t.Skipf("no pseudo-terminal here: %v", err)
	}
	t.Cleanup(func() { pty.Close() })
	self, _ := os.Executable()
	cmd := exec.Command(self)
	cmd.Env = append(os.Environ(), "TERM_TEST_ROLE=relay", "CHILD_MODE="+mode, "TERM=xterm-256color")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true}
	if err := pty.Start(cmd); err != nil {
		t.Fatal(err)
	}
	s := &screen{t: t, pty: pty, emu: vt.NewEmulator(80, 24), cmd: cmd}
	go func() {
		buf := make([]byte, 32<<10)
		for {
			n, err := pty.Read(buf)
			if n > 0 {
				s.mu.Lock()
				_, _ = s.emu.Write(buf[:n])
				s.mu.Unlock()
			}
			if err != nil {
				return
			}
		}
	}()
	go func() {
		buf := make([]byte, 4<<10)
		for {
			n, err := s.emu.Read(buf)
			if n > 0 {
				_, _ = pty.Write(buf[:n])
			}
			if err != nil {
				return
			}
		}
	}()
	return s
}

func (s *screen) text() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.emu.String()
}

func (s *screen) waitFor(text string) {
	s.t.Helper()
	end := time.Now().Add(20 * time.Second)
	for time.Now().Before(end) {
		if strings.Contains(s.text(), text) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	s.t.Fatalf("%q never appeared; the screen:\n%s", text, s.text())
}

// type sends keys as the user types them (the test harness types; the relay never does).
func (s *screen) typeKeys(keys string) {
	s.t.Helper()
	if _, err := s.pty.Write([]byte(keys)); err != nil {
		s.t.Fatal(err)
	}
}

// The relay hands the program a terminal of the user's terminal's size, lets the user's
// terminal answer its query, passes keys through raw (each key as it is typed), follows a
// resize, captures what it printed, and reports its exit code.
func TestRelayIsTransparent(t *testing.T) {
	s := startScreen(t, "interactive")
	s.waitFor("size=80x24 ready")
	s.waitFor("da1=true")
	s.typeKeys("a")
	s.waitFor("got:61")
	s.typeKeys("\r")
	s.waitFor("got:0d") // raw: Enter is a carriage return, not a line
	if err := s.pty.Resize(100, 30); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	s.emu.Resize(100, 30)
	s.mu.Unlock()
	_ = s.cmd.Process.Signal(syscall.SIGWINCH)
	s.waitFor("resized size=100x30")
	s.typeKeys("q")
	s.waitFor("relay: code=3 width=80 captured-ready=true")
}

// Ctrl-C reaches the program, not hopsesh: the program, in its own session with the
// pseudo-terminal as its terminal, gets SIGINT, and the relay reports 128+2.
func TestRelayPassesCtrlC(t *testing.T) {
	s := startScreen(t, "sleep")
	s.waitFor("sleeping")
	s.typeKeys("\x03")
	s.waitFor("relay: code=130")
}

// A relay with no terminal on either side (as in the tests and the app's background runs)
// still gives the program one, and stops when the program ends.
func TestRelayWithoutATerminal(t *testing.T) {
	self, _ := os.Executable()
	r := term.New([]string{self}, "", append(os.Environ(), "TERM_TEST_ROLE=child", "CHILD_MODE=interactive"))
	r.Capture = &term.Capture{}
	in, typed := io.Pipe()
	var out bytes.Buffer
	r.SetStdin(in)
	r.SetStdout(&out)
	go func() {
		time.Sleep(3500 * time.Millisecond) // after the DA1 query went unanswered
		_, _ = typed.Write([]byte("xq"))
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- r.Run() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("the relay did not end with the program")
	}
	_ = typed.Close()
	text := r.Capture.Text()
	if r.Code != 3 || !strings.Contains(text, "size=100x30 ready") || !strings.Contains(text, "da1=false") || !strings.Contains(text, "got:78") {
		t.Fatalf("code %d, captured:\n%s", r.Code, text)
	}
	if !strings.Contains(out.String(), "got:78") {
		t.Error("what the program printed reaches the user")
	}
}
