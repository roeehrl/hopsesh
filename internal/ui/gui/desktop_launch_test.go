package gui

import (
	"context"
	"errors"
	"fmt"
	"github.com/roeehrl/hopsesh/internal/agents/all"
	"github.com/roeehrl/hopsesh/internal/app"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/roeehrl/hopsesh/sdk/agent"
)

func TestDesktopTTYReportsFailureAndCleansUp(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture; Windows PTY backend has its own ConPTY tests")
	}
	dir := t.TempDir()
	t.Setenv("CLAUDECODE", "parent")
	c := agent.Command{Argv: []string{"/bin/sh", "-c", `test -t 0 && test -t 1 && test -t 2 || exit 42; test -z "$CLAUDECODE" || exit 43; echo 'Session is open in another terminal'; exit 7`}, Dir: dir, Unset: []string{"CLAUDECODE"}, Wait: true, TTY: true}
	err := start(c)
	if err == nil || !strings.Contains(err.Error(), "exit 7") || !strings.Contains(err.Error(), "Session is open in another terminal") {
		t.Fatalf("lost launcher error or no tty: %v", err)
	}
	c.Argv = []string{"/bin/sh", "-c", "test -t 0 && test -t 1 && test -t 2"}
	if err := start(c); err != nil {
		t.Fatal(err)
	}
	c.Argv = []string{"/bin/sh", "-c", "exec sleep 60"}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	began := time.Now()
	err = startDesktopTTY(ctx, c)
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(began) > 5*time.Second {
		t.Fatalf("timeout did not clean up: %v", err)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatal(err)
	}
}

func TestShowClaudeAppKeepsSelectedSessionAndReportsAvailability(t *testing.T) {
	home(t)
	a := NewApp(all.Registry())
	t.Cleanup(func() { _ = a.core.Catalog.Close() })
	if _, err := a.Scan(); err != nil {
		t.Fatal(err)
	}
	local := a.inv.Local()
	for i := range local.Agents {
		if local.Agents[i].Agent == "claude" {
			in := &local.Agents[i].Install
			in.Profile = nil
			in.OS = "darwin"
			in.Binary = "/test/claude"
			in.Version = "2.1.289"
			in.Desktop = "/Applications/Claude.app"
			in.DesktopVersion = "2.19675.1"
		}
	}
	for i := range a.inv.Entries {
		if a.inv.Entries[i].Agent == "claude" {
			a.inv.Entries[i].Session.Key.Profile = ""
			a.inv.Entries[i].Profile = nil
		}
	}
	var selected app.Entry
	for _, entry := range a.inv.Entries {
		if entry.Agent == "claude" && string(entry.Session.Key.Session) == sid {
			selected = entry
		}
	}
	if selected.Machine == "" {
		t.Fatal("fixture missing")
	}
	var launched agent.Command
	SetAppHook(func(_ string, c agent.Command) error { launched = c; return nil })
	t.Cleanup(func() { SetAppHook(nil) })
	if _, err := a.ShowApp(selected.Machine, selected.Session.Key.String()); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(launched.Argv, []string{"/test/claude", "--desktop", "--resume", sid}) || !launched.TTY {
		t.Fatalf("lost selected session: %+v", launched)
	}
	reg := filepath.Join(os.Getenv("HOME"), ".claude", "sessions", fmt.Sprintf("%d.json", os.Getpid()))
	if err := os.MkdirAll(filepath.Dir(reg), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(reg, []byte(fmt.Sprintf(`{"pid":%d,"sessionId":%q,"entrypoint":"claude-desktop","status":"idle"}`, os.Getpid(), sid)), 0600); err != nil {
		t.Fatal(err)
	}
	for i := range a.inv.Entries {
		if a.inv.Entries[i].Session.Key == selected.Session.Key {
			a.inv.Entries[i].Live = agent.LiveInfo{State: agent.Live, App: true}
		}
	}
	if _, err := a.ShowApp(selected.Machine, selected.Session.Key.String()); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(launched.Argv, []string{"/usr/bin/open", "-a", "/Applications/Claude.app", "claude://resume?session=" + sid}) {
		t.Fatalf("did not focus owned session: %+v", launched)
	}
	if err := os.Remove(reg); err != nil {
		t.Fatal(err)
	}
	launched = agent.Command{}
	if _, err := a.ShowApp(selected.Machine, selected.Session.Key.String()); err == nil || !strings.Contains(err.Error(), "changed or closed") || len(launched.Argv) > 0 {
		t.Fatalf("stale ownership was imported: %+v %v", launched, err)
	}
	if err := os.WriteFile(reg, []byte(fmt.Sprintf(`{"pid":%d,"sessionId":%q,"entrypoint":"claude-desktop","status":"idle"}`, os.Getpid(), sid)), 0600); err != nil {
		t.Fatal(err)
	}
	for i := range local.Agents {
		if local.Agents[i].Agent == "claude" {
			local.Agents[i].Install.DesktopVersion = ""
		}
	}
	launched = agent.Command{}
	if _, err := a.ShowApp(selected.Machine, selected.Session.Key.String()); err == nil || len(launched.Argv) > 0 {
		t.Fatalf("unsupported focus launched: %+v %v", launched, err)
	}
}
