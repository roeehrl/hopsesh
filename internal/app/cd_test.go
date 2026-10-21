package app

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/roeehrl/hopsesh/agents/claude"
	"github.com/roeehrl/hopsesh/internal/config"
	"github.com/roeehrl/hopsesh/internal/core/host"
	"github.com/roeehrl/hopsesh/internal/core/journal"
	"github.com/roeehrl/hopsesh/internal/core/registry"
	"github.com/roeehrl/hopsesh/sdk/agent"
	"github.com/roeehrl/hopsesh/sdk/ir"
)

// cdFixture is a Claude Code session in one folder of a home, with another folder to move
// it to, and live detection the test controls.
func cdFixture(t *testing.T) (*App, *returnTestModule, *Inventory, Entry, string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fixture's folders are POSIX paths")
	}
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(home, ".claude")
	from, to := filepath.Join(home, "a"), filepath.Join(home, "b")
	for _, d := range []string{root, from, to} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOME", home)
	t.Setenv("HOPSESH_STATE_DIR", filepath.Join(home, "state"))
	t.Setenv("HOPSESH_MACHINE", "here")
	m := &returnTestModule{Module: claude.New(), live: map[agent.SessionID]bool{},
		install: agent.Install{Agent: "claude", Present: true, Version: "2.1.284", Roots: map[string]string{"home": root}}}
	reg, err := registry.New(m)
	if err != nil {
		t.Fatal(err)
	}
	a := New(config.Defaults(), reg, filepath.Join(home, "state"), nil)
	t.Cleanup(func() { _ = a.Catalog.Close() })
	machine := &host.Machine{Local: true, Name: "here", Facts: host.Facts{Home: home, OS: runtime.GOOS, Env: map[string]string{}, Binaries: map[string]agent.BinaryFact{}}}
	t.Cleanup(func() { machine.Close() })
	ctx := context.Background()
	j, err := journal.New(a.StateDir, journal.KindMove, "seed")
	if err != nil {
		t.Fatal(err)
	}
	h, err := machine.For(ctx, m.Spec(), m.install, j)
	if err != nil {
		t.Fatal(err)
	}
	w, err := m.Module.Write(ctx, h, m.install, ir.WriteRequest{Mode: ir.WriteNew, SessionID: "22222222-2222-4222-8222-222222222222",
		Header: ir.Header{CWD: from, Title: "Work", Created: time.Now()}, Items: []ir.Item{{Role: ir.RoleUser, Text: "do the work"}, {Role: ir.RoleAgent, Text: "done"}}})
	if err != nil {
		t.Fatal(err)
	}
	s := agent.Summary{Key: agent.SessionKey{Agent: "claude", Session: agent.SessionID(w.SessionID)}, Path: w.Path, CWD: from, Title: "Work"}
	e := Entry{Machine: "here", Agent: "claude", AgentName: "Claude Code", Session: s}
	inv := &Inventory{Machines: []*Machine{{Name: "here", Local: true, host: machine, Agents: []AgentState{{Agent: "claude", Install: m.install}}}}, Entries: []Entry{e}}
	return a, m, inv, e, to
}

// An open Claude Code session changes folder from inside it: hopsesh says to type /cd and
// sees the session arrive in the new folder; it never moves the open session's files itself.
func TestChangeDirOfAnOpenSessionWaitsForCd(t *testing.T) {
	a, m, inv, e, to := cdFixture(t)
	m.live[e.Session.Key.Session] = true
	ctx := context.Background()
	c, err := a.PlanChangeDir(ctx, inv, e, to, DirOptions{OtherRepo: true})
	if err != nil {
		t.Fatal(err)
	}
	if !c.Open || c.InSession != "/cd "+to || len(c.Plan.Blockers) > 0 || c.StopFirst {
		t.Fatalf("plan: %+v", c)
	}
	if _, err := a.ChangeDir(ctx, c); err == nil || !strings.Contains(err.Error(), "/cd") {
		t.Fatalf("hopsesh must not move an open session's files: %v", err)
	}
	if _, err := os.Stat(e.Session.Path); err != nil {
		t.Fatalf("the transcript stays where Claude Code writes it: %v", err)
	}
	// Claude Code does it when the user types /cd.
	go func() {
		time.Sleep(200 * time.Millisecond)
		j, _ := journal.New(t.TempDir(), journal.KindMove, "claude /cd")
		h, _ := inv.Machines[0].host.For(ctx, m.Spec(), m.install, j)
		_ = m.ChangeDir(ctx, h, m.install, e.Session, c.Plan)
	}()
	wctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := a.WaitChangedDir(wctx, c, 50*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	// Waiting gives up when nothing happens.
	short, cancel2 := context.WithTimeout(ctx, 150*time.Millisecond)
	defer cancel2()
	c.Plan.To = "/nowhere"
	if err := a.WaitChangedDir(short, c, 50*time.Millisecond); err == nil {
		t.Fatal("waiting must end without the change")
	}
}

// --stop-local quits the open session, then changes its folder, in a journal undo reverses.
func TestChangeDirStopsTheSessionWhenAsked(t *testing.T) {
	a, m, inv, e, to := cdFixture(t)
	m.live[e.Session.Key.Session] = true
	ctx := context.Background()
	c, err := a.PlanChangeDir(ctx, inv, e, to, DirOptions{OtherRepo: true, StopLocal: true})
	if err != nil || !c.StopFirst || c.InSession != "" || len(c.Plan.Blockers) > 0 {
		t.Fatalf("plan: %+v %v", c, err)
	}
	id, err := a.ChangeDir(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.stopped) != 1 {
		t.Fatalf("the session must be quit first: %v", m.stopped)
	}
	if _, err := os.Stat(c.Plan.Main); err != nil {
		t.Fatalf("moved: %v", err)
	}
	if _, err := a.Undo(ctx, id, false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(e.Session.Path); err != nil {
		t.Fatalf("undo puts it back: %v", err)
	}
}

// Another repository, or none, is refused unless asked for; the folder it is in already is
// refused outright.
func TestChangeDirChecksTheFolder(t *testing.T) {
	a, _, inv, e, to := cdFixture(t)
	ctx := context.Background()
	c, err := a.PlanChangeDir(ctx, inv, e, to, DirOptions{})
	if err != nil || len(c.Plan.Blockers) == 0 || !strings.Contains(strings.Join(c.Plan.Blockers, " "), "--other-repo") {
		t.Fatalf("another folder outside the repository: %+v %v", c, err)
	}
	if _, err := a.PlanChangeDir(ctx, inv, e, e.Session.CWD, DirOptions{OtherRepo: true}); err == nil {
		t.Fatal("the same folder must be refused")
	}
	if _, err := a.PlanChangeDir(ctx, inv, e, filepath.Join(to, "missing"), DirOptions{OtherRepo: true}); err == nil {
		t.Fatal("a missing folder must be refused")
	}
}
