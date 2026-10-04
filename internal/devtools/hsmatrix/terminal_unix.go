//go:build !windows

package main

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/charmbracelet/x/vt"
	"github.com/charmbracelet/x/xpty"

	"github.com/roeehrl/hopsesh/internal/core/term"
)

// hsTerminal runs hopsesh with its input and standard error on a pseudo-terminal, as in
// the user's terminal (standard output, the JSON, is kept apart): a hand-off to Claude Code
// cloud runs claude --cloud there. The matrix plays the user: a terminal that answers the
// program's queries, and Enter when Claude Code asks whether the folder is trusted. hopsesh
// itself types nothing.
func (r *runner) hsTerminal(args ...string) (string, error) {
	pty, err := xpty.NewPty(100, 30)
	if err != nil {
		return "", err
	}
	defer pty.Close()
	cmd := exec.Command(r.hopsesh, args...)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true}
	if err := pty.Start(cmd); err != nil {
		return "", err
	}
	emu := vt.NewEmulator(100, 30)
	var mu sync.Mutex
	var shown bytes.Buffer
	go func() {
		buf := make([]byte, 32<<10)
		for {
			n, err := pty.Read(buf)
			if n > 0 {
				mu.Lock()
				_, _ = emu.Write(buf[:n])
				shown.Write(buf[:n])
				mu.Unlock()
			}
			if err != nil {
				return
			}
		}
	}()
	go func() {
		buf := make([]byte, 1024)
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
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	answered := false
	timeout := time.After(3 * time.Minute)
	for {
		select {
		case err = <-done:
			mu.Lock()
			screen := term.Plain(shown.Bytes())
			mu.Unlock()
			r.log.printf("here$ hopsesh %s (in a terminal)\n%s\n%s\n", strings.Join(args, " "), screen, out.String())
			if err != nil {
				return out.String(), fmt.Errorf("hopsesh %s failed: %v\n%s\n%s", strings.Join(args, " "), err, tail(screen, 800), tail(out.String(), 800))
			}
			return out.String(), nil
		case <-timeout:
			_ = cmd.Process.Kill()
			return out.String(), fmt.Errorf("hopsesh %s did not end in its terminal", strings.Join(args, " "))
		case <-time.After(50 * time.Millisecond):
		}
		mu.Lock()
		asked := strings.Contains(term.Plain(shown.Bytes()), "Quick safety check")
		mu.Unlock()
		if asked && !answered {
			answered = true
			_, _ = pty.Write([]byte("\r"))
		}
	}
}
