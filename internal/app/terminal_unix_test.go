//go:build !windows

package app

import (
	"bytes"
	"context"
	"encoding/base64"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/charmbracelet/x/xpty"

	"github.com/roeehrl/hopsesh/internal/agents/all"
	"github.com/roeehrl/hopsesh/internal/core/termapp"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

// tab is a terminal tab: a pseudo-terminal running hopsesh's verb on a ticket, its raw
// output kept (escape sequences and all).
type tab struct {
	t   *testing.T
	pty xpty.Pty
	mu  sync.Mutex
	out bytes.Buffer
	cmd *exec.Cmd
}

func openTab(t *testing.T, ticket string, env ...string) *tab {
	t.Helper()
	pty, err := xpty.NewPty(100, 30)
	if err != nil {
		t.Skipf("no pseudo-terminal here: %v", err)
	}
	t.Cleanup(func() { pty.Close() })
	self, _ := os.Executable()
	cmd := exec.Command(self)
	cmd.Env = append(append(os.Environ(), "TERMAPP_ROLE=verb", "TERMAPP_TICKET="+ticket, "TERM=xterm-256color"), env...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true}
	if err := pty.Start(cmd); err != nil {
		t.Fatal(err)
	}
	tb := &tab{t: t, pty: pty, cmd: cmd}
	go func() {
		buf := make([]byte, 8<<10)
		for {
			n, err := pty.Read(buf)
			if n > 0 {
				tb.mu.Lock()
				tb.out.Write(buf[:n])
				tb.mu.Unlock()
			}
			if err != nil {
				return
			}
		}
	}()
	return tb
}

func (tb *tab) text() string {
	tb.mu.Lock()
	defer tb.mu.Unlock()
	return tb.out.String()
}

func (tb *tab) waitFor(s string) {
	tb.t.Helper()
	end := time.Now().Add(20 * time.Second)
	for time.Now().Before(end) {
		if strings.Contains(tb.text(), s) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	tb.t.Fatalf("never saw %q in:\n%q", s, tb.text())
}

// hopsesh's verb in a tab (iTerm2, as its environment says): it labels the tab with the
// sanitised title, starts the agent attached to the tab (a terminal, with the agent's
// folder variable from the app where the tab lacks it), records the tab and the agent's
// process while it runs, and says how it ended; the record and the ticket are gone after.
func TestTicketInATab(t *testing.T) {
	cloudEnv(t, "claude")
	a := cloudApp(t, all.Registry())
	dir := t.TempDir()
	cfgDir := filepath.Join(t.TempDir(), "claude-config")
	t.Setenv("CLAUDE_CONFIG_DIR", cfgDir) // as the app adopted it from the login shell
	key := agent.SessionKey{Agent: "claude", Session: "sess-1"}
	hostile := "Fix \x1b]0;pwned\a the\nparser \u202egnp.exe"
	id, err := a.NewTicket(Launch{Kind: termapp.KindSession, Key: key, Run: agent.Command{Argv: []string{"claude", "--resume", "sess-1"}, Dir: dir},
		Labels: termapp.Labels{Title: hostile, Agent: "Claude Code", Machine: "here"}})
	if err != nil {
		t.Fatal(err)
	}
	tb := openTab(t, id, "TERMAPP_AGENT=wait", "TERM_PROGRAM=iTerm.app", "CLAUDE_CONFIG_DIR=")
	tb.waitFor("agent running")
	out := tb.text()
	if !strings.Contains(out, "agent running: tty=true args=--resume sess-1 config="+cfgDir) {
		t.Fatalf("the agent: %q", out)
	}
	title := base64.StdEncoding.EncodeToString([]byte("Fix ]0;pwned the parser gnp.exe"))
	if !strings.Contains(out, "\x1b]1337;SetUserVar=hopsesh_title="+title+"\a") || !strings.Contains(out, "\x1b]1337;SetBadgeFormat=") {
		t.Fatalf("labels: %q", out)
	}
	if strings.Contains(out, "\x1b]0;pwned") || strings.Contains(out, "\u202e") {
		t.Fatalf("the hostile title reached the tab: %q", out)
	}
	if !strings.Contains(out, "hopsesh: resuming “Fix ]0;pwned the parser gnp.exe” in Claude Code.") {
		t.Fatalf("header: %q", out)
	}

	// While it runs: a record of the tab's terminal and the agent's process.
	recs := a.termStore().Running(key.String(), termapp.SystemProcs().Alive)
	if len(recs) != 1 || recs[0].PID <= 0 || recs[0].Program != "iTerm.app" || recs[0].Ticket != id {
		t.Fatalf("records: %+v", recs)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if tty := termapp.SystemProcs().TTY(ctx, recs[0].PID); tty == "" || tty != recs[0].TTY {
		t.Fatalf("the agent's terminal %q, recorded %q", tty, recs[0].TTY)
	}
	if fi, err := os.Stat(filepath.Join(a.StateDir, "terminal", "running", id+".json")); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("record file: %v %v", fi, err)
	}

	// The user ends the agent; the verb says how, and forgets the tab.
	if _, err := tb.pty.Write([]byte("\r")); err != nil {
		t.Fatal(err)
	}
	tb.waitFor("[claude exited with code 3]")
	_ = tb.cmd.Wait()
	if recs := a.termStore().Running("", termapp.SystemProcs().Alive); len(recs) != 0 {
		t.Fatalf("records stay: %+v", recs)
	}
	if !strings.Contains(tb.text(), "\x1b]1337;SetBadgeFormat=\a") {
		t.Fatal("the badge stays after the agent ended")
	}
	if _, err := a.termStore().Take(id, ticketAge); err == nil {
		t.Fatal("the ticket can run again")
	}
}

// --hold (a terminal that closes a tab with its command): once the agent ends, the tab
// goes on as the user's login shell in the session's folder. In a terminal that shows no
// labels, none are printed.
func TestTicketHold(t *testing.T) {
	cloudEnv(t, "claude")
	a := cloudApp(t, all.Registry())
	dir, _ := filepath.EvalSymlinks(t.TempDir())
	shell := filepath.Join(t.TempDir(), "shell")
	if err := os.WriteFile(shell, []byte("#!/bin/sh\necho \"held shell $0 in $(pwd)\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	id, err := a.NewTicket(Launch{Kind: termapp.KindStep, Run: agent.Command{Argv: []string{"claude", "--teleport", "session_01"}, Dir: dir},
		Labels: termapp.Labels{Title: "Bring it back"}})
	if err != nil {
		t.Fatal(err)
	}
	tb := openTab(t, id, "TERMAPP_AGENT=exit", "TERMAPP_HOLD=1", "SHELL="+shell, "TERM_PROGRAM=Apple_Terminal", "LC_TERMINAL=")
	tb.waitFor("in " + dir + "\r\n")
	out := tb.text()
	if !strings.Contains(out, "[claude exited with code 3]") || !strings.Contains(out, "This tab stays open as a shell") || !strings.Contains(out, "held shell "+shell) {
		t.Fatalf("%q", out)
	}
	if strings.Contains(out, "\x1b]1337;") {
		t.Fatalf("labels in Terminal: %q", out)
	}
	if recs := a.termStore().Running("", termapp.SystemProcs().Alive); len(recs) != 0 {
		t.Fatalf("a step was recorded: %+v", recs)
	}
}
