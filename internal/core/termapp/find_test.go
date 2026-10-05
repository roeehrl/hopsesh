package termapp

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// fakeProcs is a process table: who runs, and on which terminal.
type fakeProcs struct {
	alive map[int]bool
	tty   map[int]string
}

func (p *fakeProcs) Alive(pid int) bool                    { return p.alive[pid] }
func (p *fakeProcs) TTY(_ context.Context, pid int) string { return p.tty[pid] }

// iterm2Tabs answers iTerm2's find and focus scripts from a list of its sessions
// (id → tty), as iTerm2 would.
func iterm2Tabs(f *fakeScripts, tabs map[string]string) {
	f.answer = func(s string) (string, error) {
		for id, tty := range tabs {
			if strings.Contains(s, "select s") {
				if strings.Contains(s, `unique ID of s is "`+id+`"`) && strings.Contains(s, `tty of s is "`+tty+`"`) {
					return "ok", nil
				}
				continue
			}
			if strings.Contains(s, `if tty of s is "`+tty+`"`) {
				return id + "\t" + tty, nil
			}
		}
		return "", nil
	}
}

// Show in iTerm2: the agent's process id → its terminal device (ps) → the iTerm2 session
// with that tty; a record's device must still be its process's; a dead process, one on
// no terminal, or one hopsesh's terminals do not have is not found.
func TestFindTab(t *testing.T) {
	ctx := context.Background()
	f := &fakeScripts{}
	iterm2Tabs(f, map[string]string{"SESSION-A": "/dev/ttys004", "SESSION-B": "/dev/ttys007"})
	it := ITerm2Using(f.run, yes)
	tf := &fakeScripts{}
	set := NewSet(it, TerminalAppUsing(tf.run, yes))
	procs := &fakeProcs{alive: map[int]bool{100: true, 200: true, 300: true, 400: true},
		tty: map[int]string{100: "/dev/ttys004", 200: "/dev/ttys007", 300: "", 400: "/dev/ttys099"}}

	got, ok, err := set.FindTab(ctx, procs, it, []Candidate{{PID: 100}})
	if err != nil || !ok || got.Handle.Ref != "SESSION-A" || got.Handle.TTY != "/dev/ttys004" || got.PID != 100 || got.Name() != "iTerm2" {
		t.Fatalf("%+v %v %v", got, ok, err)
	}
	// A record says the process started on ttys007: still there, found.
	if got, ok, _ := set.FindTab(ctx, procs, it, []Candidate{{PID: 200, TTY: "/dev/ttys007"}}); !ok || got.Handle.Ref != "SESSION-B" {
		t.Fatalf("record: %+v", got)
	}
	// A record whose process is now on another device (a reused id): not trusted.
	if _, ok, _ := set.FindTab(ctx, procs, it, []Candidate{{PID: 200, TTY: "/dev/ttys004"}}); ok {
		t.Fatal("a moved process was trusted")
	}
	for _, c := range []Candidate{{PID: 999}, {PID: 300}, {PID: 0}} {
		if _, ok, err := set.FindTab(ctx, procs, it, []Candidate{c}); ok || err != nil {
			t.Fatalf("%+v: %v %v", c, ok, err)
		}
	}
	// On a terminal neither app has: iTerm2, then Terminal, were asked; nothing found.
	tf.answer = func(string) (string, error) { return "", nil }
	if _, ok, err := set.FindTab(ctx, procs, it, []Candidate{{PID: 400}}); ok || err != nil {
		t.Fatal(ok, err)
	}
	if !strings.Contains(tf.last(), `if tty of t is "/dev/ttys099" then return tty of t`) {
		t.Fatalf("Terminal was not asked: %q", tf.last())
	}
	// Terminal has it.
	tf.answer = func(string) (string, error) { return "/dev/ttys099", nil }
	if got, ok, _ := set.FindTab(ctx, procs, it, []Candidate{{PID: 400}}); !ok || got.Terminal.ID() != IDTerminalApp {
		t.Fatalf("%+v", got)
	}
	// A denied permission is said, not hidden.
	tf.answer = func(string) (string, error) { return "", ErrDenied }
	f.answer = func(string) (string, error) { return "", ErrDenied }
	if _, ok, err := set.FindTab(ctx, procs, it, []Candidate{{PID: 100}}); ok || err == nil || !strings.Contains(err.Error(), "macOS did not allow") {
		t.Fatal(ok, err)
	}
	// Finding never starts iTerm2 to look: the script returns at once when it is not running.
	if !strings.HasPrefix(iterm2Find("/dev/ttys004"), `if application id "com.googlecode.iterm2" is not running then return ""`) {
		t.Fatal("iTerm2 would be started to look")
	}
}

// Show checks again that the process still runs on that tab's device before selecting it.
func TestShow(t *testing.T) {
	ctx := context.Background()
	f := &fakeScripts{}
	iterm2Tabs(f, map[string]string{"SESSION-A": "/dev/ttys004"})
	it := ITerm2Using(f.run, yes)
	procs := &fakeProcs{alive: map[int]bool{100: true}, tty: map[int]string{100: "/dev/ttys004"}}
	found := Found{Terminal: it, Handle: Handle{Terminal: IDITerm2, Ref: "SESSION-A", TTY: "/dev/ttys004"}, PID: 100}
	if err := Show(ctx, procs, found); err != nil {
		t.Fatal(err)
	}
	if s := f.last(); !strings.Contains(s, "select w") || !strings.Contains(s, "activate") || VetScript(s) != nil {
		t.Fatal(s)
	}
	n := len(f.scripts)
	procs.tty[100] = "/dev/ttys005" // the session's process moved: never select the old tab
	if err := Show(ctx, procs, found); !errors.Is(err, ErrGone) || len(f.scripts) != n {
		t.Fatalf("%v (%d scripts)", err, len(f.scripts)-n)
	}
	procs.tty[100], procs.alive[100] = "/dev/ttys004", false
	if err := Show(ctx, procs, found); !errors.Is(err, ErrGone) {
		t.Fatal(err)
	}
	// The tab closed between finding and showing.
	procs.alive[100] = true
	iterm2Tabs(f, map[string]string{})
	if err := Show(ctx, procs, found); !errors.Is(err, ErrGone) {
		t.Fatal(err)
	}
	// Malformed handles never reach a script.
	bad := found
	bad.Handle.Ref = `x" & (do shell script "id") & "`
	if err := it.(Focuser).Focus(ctx, bad.Handle); err == nil {
		t.Fatal("focused a malformed handle")
	}
	if _, _, err := it.(Finder).FindTTY(ctx, `/dev/ttys004" or true or "`); err == nil {
		t.Fatal("found by a malformed tty")
	}
}

func TestDevTTY(t *testing.T) {
	for in, want := range map[string]string{"ttys003\n": "/dev/ttys003", " pts/12 ": "/dev/pts/12", "??": "", "?": "", "": "", "console": "", "ttys1/../x": ""} {
		if got := DevTTY(in); got != want {
			t.Errorf("DevTTY(%q) = %q, want %q", in, got, want)
		}
	}
}
