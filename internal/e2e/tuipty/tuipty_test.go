// Package tuipty runs the real hopsesh binary's terminal UI in a pseudo-terminal: Windows'
// ConPTY on Windows (where the TUI meets the console the way people's terminals present
// it), a Unix pty elsewhere, and reads the screen through a terminal emulator.
// HOPSESH_PTY=1 runs it (the nightly CI run does, on every system).
package tuipty

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/charmbracelet/x/vt"
	"github.com/charmbracelet/x/xpty"

	"github.com/roeehrl/hopsesh/internal/testkit"
)

func TestTUIInAPseudoTerminal(t *testing.T) {
	if os.Getenv("HOPSESH_PTY") != "1" {
		t.Skip("set HOPSESH_PTY=1 to run the real binary in a pseudo-terminal")
	}
	bin := filepath.Join(t.TempDir(), "hopsesh")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	if out, err := exec.Command("go", "build", "-o", bin, "github.com/roeehrl/hopsesh/cmd/hopsesh").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	home := t.TempDir()
	if err := testkit.DemoHome(home); err != nil {
		t.Fatal(err)
	}
	const w, h = 120, 40
	pty, err := xpty.NewPty(w, h)
	if err != nil {
		t.Fatal(err)
	}
	defer pty.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin)
	cmd.Env = append(os.Environ(), "TERM=xterm-256color", "COLUMNS=120", "LINES=40")
	for k, v := range testkit.Env(home) {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	if err := pty.Start(cmd); err != nil {
		t.Fatal(err)
	}

	// The screen: everything the program prints goes through the emulator, and the
	// emulator's answers to its queries (cursor position, colors) go back to it.
	emu := vt.NewEmulator(w, h)
	var mu sync.Mutex
	go func() {
		buf := make([]byte, 32<<10)
		for {
			n, err := pty.Read(buf)
			if n > 0 {
				mu.Lock()
				_, _ = emu.Write(buf[:n])
				mu.Unlock()
			}
			if err != nil {
				return
			}
		}
	}()
	go func() {
		buf := make([]byte, 4<<10)
		for {
			n, err := emu.Read(buf)
			if n > 0 {
				_, _ = pty.Write(buf[:n])
			}
			if err != nil {
				return
			}
		}
	}()
	screen := func() string {
		mu.Lock()
		defer mu.Unlock()
		return emu.String()
	}
	waitFor := func(text string) {
		t.Helper()
		end := time.Now().Add(60 * time.Second)
		for time.Now().Before(end) {
			if strings.Contains(screen(), text) {
				return
			}
			time.Sleep(200 * time.Millisecond)
		}
		t.Fatalf("%q never appeared; the screen:\n%s", text, screen())
	}

	waitFor("Find the codeword")
	waitFor("What is the codeword in notes.txt?")
	if _, err := pty.Write([]byte("q")); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("hopsesh exited with %v; the screen:\n%s", err, screen())
		}
	case <-time.After(20 * time.Second):
		t.Fatalf("q did not quit; the screen:\n%s", screen())
	}
}
