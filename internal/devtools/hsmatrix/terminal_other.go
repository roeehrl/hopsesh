//go:build windows

package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/x/vt"
	"github.com/charmbracelet/x/xpty"

	"github.com/roeehrl/hopsesh/internal/core/term"
)

// hsTerminal exercises the same interactive handoff through Windows ConPTY.
// The wrapper separates JSON from terminal output without changing CLI behavior.
func (r *runner) hsTerminal(args ...string) (string, error) {
	dir, err := os.MkdirTemp("", "hsmatrix-terminal-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(dir)
	output := filepath.Join(dir, "result.json")
	self, err := os.Executable()
	if err != nil {
		return "", err
	}
	pty, err := xpty.NewPty(100, 30)
	if err != nil {
		return "", unsupportedScenario(fmt.Sprintf("required ConPTY fixture is unavailable: %v", err))
	}
	defer pty.Close()
	cmd := exec.Command(self, append([]string{"terminal-exec", output, r.hopsesh}, args...)...)
	if err := pty.Start(cmd); err != nil {
		return "", err
	}
	emu := vt.NewEmulator(100, 30)
	var mu sync.Mutex
	var shown bytes.Buffer
	readDone, replyDone := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(readDone)
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
		defer close(replyDone)
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
	defer func() {
		_ = pty.Close()
		<-readDone
		_ = emu.Close()
		<-replyDone
	}()
	// The inner command has its own shorter deadline, so killing the wrapper
	// cannot leave an unbounded CLI behind.
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute+10*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- xpty.WaitProcess(ctx, cmd) }()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	answered := false
	for {
		select {
		case err := <-done:
			_ = pty.Close()
			<-readDone
			out, readErr := os.ReadFile(output)
			screen := term.Plain(shown.Bytes())
			r.log.printf("here$ hopsesh %s (in ConPTY)\n%s\n%s\n", strings.Join(args, " "), screen, out)
			if err != nil {
				return string(out), fmt.Errorf("hopsesh terminal failed: %w\n%s\n%s", err, tail(screen, 800), tail(string(out), 800))
			}
			return string(out), readErr
		case <-ticker.C:
			mu.Lock()
			asked := strings.Contains(term.Plain(shown.Bytes()), "Quick safety check")
			mu.Unlock()
			if asked && !answered {
				answered = true
				_, _ = pty.Write([]byte("\r"))
			}
		}
	}
}
