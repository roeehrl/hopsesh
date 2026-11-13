package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/roeehrl/hopsesh/agents/claude"
	"github.com/roeehrl/hopsesh/agents/codex"
	"github.com/roeehrl/hopsesh/internal/config"
	"github.com/roeehrl/hopsesh/internal/core/host"
	"github.com/roeehrl/hopsesh/internal/core/journal"
	"github.com/roeehrl/hopsesh/internal/core/lineage"
	"github.com/roeehrl/hopsesh/internal/core/move"
	"github.com/roeehrl/hopsesh/internal/core/registry"
	"github.com/roeehrl/hopsesh/internal/testkit/dbagent"
	"github.com/roeehrl/hopsesh/sdk/agent"
	"github.com/roeehrl/hopsesh/sdk/ir"
)

// dbFixture is a session of the stand-in that keeps sessions in a database, with no anchor
// file, in folder a of a home, and folder b to move it to.
func dbFixture(t *testing.T) (*App, *dbagent.Module, *Inventory, Entry, string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fixture's folders are POSIX paths")
	}
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	data := filepath.Join(home, ".local", "share", "dbagent")
	from, to := filepath.Join(home, "a"), filepath.Join(home, "b")
	for _, d := range []string{data, from, to} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOME", home)
	t.Setenv("HOPSESH_STATE_DIR", filepath.Join(home, "state"))
	t.Setenv("HOPSESH_MACHINE", "here")
	t.Setenv("HOPSESH_CONFIG_DIR", filepath.Join(home, "config"))
	m := dbagent.New()
	reg, err := registry.New(m)
	if err != nil {
		t.Fatal(err)
	}
	a := New(config.Defaults(), reg, filepath.Join(home, "state"), nil)
	t.Cleanup(func() { _ = a.Catalog.Close() })
	if err := dbagent.Seed(data, "ses_a", from, "Work",
		dbagent.Message{ID: "msg_1", Role: "user", Text: "do the work"}, dbagent.Message{ID: "msg_2", Role: "assistant", Text: "done"}); err != nil {
		t.Fatal(err)
	}
	machine := &host.Machine{Local: true, Name: "here", Facts: host.Facts{Home: home, OS: runtime.GOOS, Env: map[string]string{}, Binaries: map[string]agent.BinaryFact{}}}
	t.Cleanup(func() { machine.Close() })
	in := agent.Install{Agent: dbagent.ID, Present: true, Version: "1.0", Roots: map[string]string{"data": data}, MainRoot: "data"}
	h, err := machine.For(context.Background(), m.Spec(), in, nil)
	if err != nil {
		t.Fatal(err)
	}
	l, err := m.List(context.Background(), h, in)
	if err != nil || len(l.Sessions) != 1 {
		t.Fatalf("%+v %v", l, err)
	}
	e := Entry{Machine: "here", Agent: dbagent.ID, AgentName: "DB Agent", Session: l.Sessions[0]}
	inv := &Inventory{Machines: []*Machine{{Name: "here", Local: true, host: machine, Agents: []AgentState{{Agent: dbagent.ID, Install: in}}}}, Entries: []Entry{e}}
	return a, m, inv, e, to
}

// A folder change of a session kept in a database is journaled with the session's stamp:
// undo refuses once the session was used, also in the activity list, and a change typed in
// the open session is seen with no anchor file to watch.
func TestChangeDirOfVirtualSession(t *testing.T) {
	a, m, inv, e, to := dbFixture(t)
	ctx := context.Background()
	if _, err := os.Stat(e.Session.Path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("the fixture has an anchor")
	}
	c, err := a.PlanChangeDir(ctx, inv, e, to, DirOptions{OtherRepo: true})
	if err != nil || len(c.Plan.Blockers) > 0 {
		t.Fatalf("plan: %+v %v", c, err)
	}
	id, err := a.ChangeDir(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	j, err := journal.Load(a.StateDir, id)
	if err != nil {
		t.Fatal(err)
	}
	if len(j.Entries) != 1 || j.Entries[0].Op != journal.OpChangeDir || j.Entries[0].Stamp == "" {
		t.Fatalf("journaled: %+v", j.Entries)
	}
	data := inv.Machines[0].Agents[0].Install.Main()
	if err := dbagent.Continue(data, "ses_a", dbagent.Message{ID: "msg_3", Role: "user", Text: "more"}); err != nil {
		t.Fatal(err)
	}
	acts, err := a.Activities()
	if err != nil {
		t.Fatal(err)
	}
	if len(acts) != 1 || acts[0].CanUndo || !strings.Contains(acts[0].Why, "continued after this") {
		t.Fatalf("activity: %+v", acts)
	}
	if _, err := a.Undo(ctx, id, false); !errors.Is(err, journal.ErrChanged) {
		t.Fatalf("undo after the session was used: %v", err)
	}
	if _, err := a.Undo(ctx, id, true); err != nil {
		t.Fatal(err)
	}
	h, _ := inv.Machines[0].host.For(ctx, m.Spec(), inv.Machines[0].Agents[0].Install, nil)
	if dir, err := folderOf(ctx, m, h, inv.Machines[0].Agents[0].Install, e.Session.Key); err != nil || dir != e.Session.CWD {
		t.Fatalf("changed back to %s, %v", dir, err)
	}

	// Typed in the open session: hopsesh waits for the agent's listing, as there is no file.
	go func() {
		time.Sleep(100 * time.Millisecond)
		_ = m.ChangeDir(ctx, h, inv.Machines[0].Agents[0].Install, e.Session, c.Plan)
	}()
	wctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := a.WaitChangedDir(wctx, c, 50*time.Millisecond); err != nil {
		t.Fatal(err)
	}
}

// Movement reads and the family fingerprint follow the session's stamp, not its anchor
// file: a session used since is read again though no file changed (or exists).
func TestMovementCachesFollowVirtualStamps(t *testing.T) {
	a, m, inv, e, _ := dbFixture(t)
	ctx := context.Background()
	in := inv.Machines[0].Agents[0].Install
	h, _ := inv.Machines[0].host.For(ctx, m.Spec(), in, nil)
	seg, err := a.readMovementSegment(ctx, m, h, in, e, "endpoint")
	if err != nil || len(seg.Nodes) != 2 {
		t.Fatalf("%+v %v", seg, err)
	}
	if again, _ := a.readMovementSegment(ctx, m, h, in, e, "endpoint"); again.Cursor != seg.Cursor {
		t.Fatal("an unchanged session read differently")
	}
	e.Lineage = lineage.New("family")
	inv.Entries[0] = e
	before := a.movementFingerprint(ctx, inv, "family")
	if err := dbagent.Continue(in.Main(), "ses_a", dbagent.Message{ID: "msg_3", Role: "assistant", Text: "and more"}); err != nil {
		t.Fatal(err)
	}
	seg, err = a.readMovementSegment(ctx, m, h, in, e, "endpoint")
	if err != nil || len(seg.Nodes) != 3 {
		t.Fatalf("a session used since was not read again: %d nodes, %v", len(seg.Nodes), err)
	}
	if a.movementFingerprint(ctx, inv, "family") == before {
		t.Fatal("the family fingerprint missed the session's new stamp")
	}
}

// The notice hook of an agent that keeps sessions in a database names only the session:
// its lineage is found beside the anchor its id gives, though the anchor file is missing.
func TestVirtualSessionNoticeByID(t *testing.T) {
	t.Setenv("HOPSESH_MACHINE", "alice-desktop")
	t.Setenv("HOPSESH_CONFIG_DIR", t.TempDir())
	root := t.TempDir()
	t.Setenv("DBAGENT_DATA_DIR", root)
	reg, err := registry.New(claude.New(), codex.New(), dbagent.New())
	if err != nil {
		t.Fatal(err)
	}
	a := New(config.Defaults(), reg, t.TempDir(), nil)
	t.Cleanup(func() { _ = a.Catalog.Close() })
	g := lineage.New("movement-test")
	from := g.Upsert(lineage.Replica{Location: LocalName(), Key: agent.SessionKey{Agent: dbagent.ID, Session: "alice-session"}})
	to := g.Upsert(lineage.Replica{Location: "bob-laptop", Key: agent.SessionKey{Agent: "codex", Session: "bob-session"}})
	source := ir.Segment{Nodes: []ir.Node{{Kind: ir.KindMessage, Actor: ir.User, Text: "Fix the tests"}}, Cursor: ir.Cursor{Head: "source-one", Offset: 2}}
	st, err := g.Observe(from, &source)
	if err != nil {
		t.Fatal(err)
	}
	dst := g.Deliver(to, ir.Cursor{Head: "target-one", Offset: 25}, st.Projection, st.Heads, nil)
	if err = g.AppendHop(lineage.Hop{Kind: lineage.HopContinue, ID: "move-one", From: from, To: to, Source: st.ID, Target: dst.ID, Notify: true}); err != nil {
		t.Fatal(err)
	}
	anchor := dbagent.Anchor(root, "alice-session")
	if err := os.MkdirAll(filepath.Dir(anchor), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(lineage.PathFor(anchor), g.Encode(), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := a.SessionMovementNoticeForPath(context.Background(), dbagent.ID, "", "alice-session", "")
	if err != nil || !strings.Contains(s, "Prepared in Codex on bob-laptop") {
		t.Fatalf("notice %q, %v", s, err)
	}
}

// A peer push of a session kept in a database packages the module's file at its anchor;
// the receiver, whose snapshot has no database, plans and installs from that file.
func TestPeerPackageOfVirtualSession(t *testing.T) {
	a, m, inv, e, to := dbFixture(t)
	ctx := context.Background()
	pkg, err := a.packageOf(ctx, inv.Machines[0], e)
	if err != nil {
		t.Fatal(err)
	}
	if len(pkg.Files) != 1 || pkg.Files[0].Path != e.Session.Path || !strings.Contains(string(pkg.Files[0].Data), `"do the work"`) {
		t.Fatalf("package: %+v", pkg.Files)
	}
	if _, err := os.Stat(e.Session.Path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("packaging wrote the anchor")
	}
	snap := host.NewSnapshot("box", pkg.Facts, pkg.Files)
	src := move.Side{Machine: snap, Module: m, Install: pkg.Install}
	data := filepath.Join(filepath.Dir(to), "receiver", "dbagent")
	if err := os.MkdirAll(data, 0o700); err != nil {
		t.Fatal(err)
	}
	tgt := move.Side{Machine: inv.Machines[0].host, Module: m, Install: agent.Install{Agent: dbagent.ID, Present: true, Version: "1.0", Roots: map[string]string{"data": data}, MainRoot: "data"}}
	in := move.Input{Source: src, Session: pkg.Session, Target: tgt}
	p, err := move.Build(ctx, in, move.Options{TargetDir: to})
	if err != nil || len(p.Blockers) > 0 {
		t.Fatalf("plan from a snapshot: %v %v", err, p.Blockers)
	}
	if _, err := move.Apply(ctx, p, in, move.Env{StateDir: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	h, _ := tgt.Machine.For(ctx, m.Spec(), tgt.Install, nil)
	l, err := m.List(ctx, h, tgt.Install)
	if err != nil || len(l.Sessions) != 1 || l.Sessions[0].CWD != to || l.Sessions[0].Size != 2 {
		t.Fatalf("received: %+v %v", l, err)
	}
}
