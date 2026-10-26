package gui

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/roeehrl/hopsesh/internal/app"
	"github.com/roeehrl/hopsesh/internal/core/pty"
)

// Only sessions are written down: a tab with a key to resume (or a terminal on another
// machine to show again) whose program still runs. Shells, steps, sign-ins, a session not
// yet bound and a bring-back with no copy saved have nothing to resume.
func TestReopenable(t *testing.T) {
	tab := func(state pty.State, m TabMeta) TermTab {
		return TermTab{Info: pty.Info{ID: m.Kind + m.Key, Title: "t " + m.Key, State: state, Dir: "/w"}, TabMeta: m}
	}
	got := reopenable([]TermTab{
		tab(pty.Running, TabMeta{Kind: TabSession, Machine: "here", Key: "claude/a", Agent: "Claude Code"}),
		tab(pty.Waiting, TabMeta{Kind: TabSession, Machine: "here", Key: "codex/b", Agent: "Codex"}),
		tab(pty.Exited, TabMeta{Kind: TabSession, Machine: "here", Key: "claude/ended"}),
		tab(pty.Running, TabMeta{Kind: TabSession, Machine: "here"}),
		tab(pty.Running, TabMeta{Kind: TabSession, Machine: "here", Key: "claude/checking", Association: associationChecking}),
		tab(pty.Running, TabMeta{Kind: TabShell}),
		tab(pty.Running, TabMeta{Kind: TabStep, Cloud: "claude"}),
		tab(pty.Running, TabMeta{Kind: TabSignIn}),
		tab(pty.Running, TabMeta{Kind: TabBring, Machine: "here", Key: "claude/unsaved"}),
		tab(pty.Waiting, TabMeta{Kind: TabBring, Machine: "here", Key: "claude/saved", Saved: "Saved copy"}),
		tab(pty.Waiting, TabMeta{Kind: TabRemote, Machine: "laptop", Key: "claude/r", Agent: "Claude Code", Remote: "t1"}),
		tab(pty.Running, TabMeta{Kind: TabRemote, Machine: "laptop"}), // a sign-in there
	})
	var keys []string
	for _, r := range got {
		keys = append(keys, r.Kind+" "+r.Key+" "+r.Remote)
	}
	if want := "session claude/a ,session codex/b ,session claude/saved ,remote claude/r t1"; strings.Join(keys, ",") != want {
		t.Fatalf("reopenable:\n%s\nwant\n%s", strings.Join(keys, ","), want)
	}
	if !got[0].Working || got[1].Working || got[0].Title != "t claude/a" || got[0].Dir != "/w" || got[0].Agent != "Claude Code" {
		t.Fatalf("details: %+v", got[:2])
	}
}

// The list is written once (an update writes it before the new version starts, and the
// quit that follows does not write it again), keeps a list the window never answered, and
// is dropped once answered.
func TestRememberTabs(t *testing.T) {
	a := updateApp(t)
	if a.Terms == nil {
		a.Terms = NewTerminals("test")
	}
	write := func(r ReopenDTO) {
		b, _ := json.Marshal(r)
		if err := os.WriteFile(reopenPath(), b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(ReopenDTO{At: time.Now(), Tabs: []ReopenTab{{Kind: TabSession, Machine: app.LocalName(), Key: "claude/old", Title: "old"}}})

	a.rememberTabs(true)
	r := a.ReopenOffer()
	if r == nil || !r.Update || len(r.Tabs) != 1 || r.Tabs[0].Key != "claude/old" {
		t.Fatalf("the unanswered list is kept: %+v", r)
	}
	write(ReopenDTO{At: time.Now(), Tabs: []ReopenTab{{Kind: TabSession, Machine: app.LocalName(), Key: "claude/x", Title: "x"}}})
	a.rememberTabs(false) // the quit after the update: written already
	if r := a.ReopenOffer(); r == nil || r.Tabs[0].Key != "claude/x" {
		t.Fatalf("written again: %+v", r)
	}

	// Reopening drops the list, whatever opened (no scan here: the session isn't known).
	done, err := a.Reopen()
	if err != nil || done.Opened != 0 || len(done.Failed) != 1 || !strings.HasPrefix(done.Failed[0], "x: ") {
		t.Fatalf("reopen: %+v %v", done, err)
	}
	if a.ReopenOffer() != nil {
		t.Fatal("the list stays after reopening")
	}

	// A stale list isn't offered; nothing to write removes the file.
	write(ReopenDTO{At: time.Now().Add(-reopenStale - time.Hour), Tabs: []ReopenTab{{Kind: TabSession, Key: "claude/y"}}})
	if a.ReopenOffer() != nil {
		t.Fatal("a week-old list is offered")
	}
	b := &App{Terms: NewTerminals("test")}
	b.rememberTabs(false)
	if _, err := os.Stat(reopenPath()); !os.IsNotExist(err) {
		t.Fatalf("no tabs: %v", err)
	}
	write(ReopenDTO{At: time.Now(), Tabs: []ReopenTab{{Kind: TabSession, Key: "claude/z"}}})
	b.ForgetReopen()
	if b.ReopenOffer() != nil {
		t.Fatal("forgotten list offered")
	}
}

// A session tab on another machine's row isn't resumed here.
func TestReopenTabElsewhere(t *testing.T) {
	a := &App{}
	if err := a.reopenTab(ReopenTab{Kind: TabSession, Machine: "laptop", Key: "claude/a"}); err == nil || !strings.Contains(err.Error(), "laptop") {
		t.Fatal(err)
	}
	if err := a.reopenTab(ReopenTab{Kind: TabShell}); err == nil {
		t.Fatal("a shell reopened")
	}
}
