package e2e

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/roeehrl/hopsesh/agents/claude"
	"github.com/roeehrl/hopsesh/internal/core/host"
	"github.com/roeehrl/hopsesh/internal/core/journal"
	"github.com/roeehrl/hopsesh/internal/core/lineage"
	"github.com/roeehrl/hopsesh/internal/core/move"
	"github.com/roeehrl/hopsesh/internal/testkit/dbagent"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

// Sessions an agent keeps in a database (the dbagent stand-in, as OpenCode does) move,
// continue and undo like files: the module makes the session's file, its own program
// imports it before lineage is recorded, and undo deletes or restores sessions through
// that program, never over later work.

const dbSession agent.SessionID = "ses_a"

func dbInstall(l location) agent.Install {
	return agent.Install{Agent: dbagent.ID, Version: "1.0", Roots: map[string]string{"data": filepath.Join(l.m.Facts.Home, ".local", "share", "dbagent")}, MainRoot: "data", Present: true}
}

func seedDB(t *testing.T, l location) {
	t.Helper()
	if err := dbagent.Seed(dbInstall(l).Main(), dbSession, l.repo, "Fix the build",
		dbagent.Message{ID: "msg_1", Role: "user", Text: "Why does the build in " + l.repo + " fail?"},
		dbagent.Message{ID: "msg_2", Role: "assistant", Text: "A missing import."}); err != nil {
		t.Fatal(err)
	}
}

func dbSessions(t *testing.T, l location, m *dbagent.Module) map[agent.SessionID]agent.Summary {
	t.Helper()
	out := map[agent.SessionID]agent.Summary{}
	for _, s := range listAgent(t, l, m, dbInstall(l)) {
		out[s.Key.Session] = s
	}
	return out
}

func dbInput(t *testing.T, m *dbagent.Module, from, to location) move.Input {
	t.Helper()
	s := dbSessions(t, from, m)[dbSession]
	lin, _ := lineage.Read(host.LocalFS(), s.Path)
	in := move.Input{Source: move.Side{Machine: from.m, Module: m, Install: dbInstall(from)}, Session: s, Lineage: lin,
		Target: move.Side{Machine: to.m, Module: m, Install: dbInstall(to)}}
	if c, ok := dbSessions(t, to, m)[dbSession]; ok {
		cl, _ := lineage.Read(host.LocalFS(), c.Path)
		in.Copies = []move.Copy{{Summary: c, Live: agent.LiveInfo{State: agent.Ended}, Lineage: cl}}
	}
	return in
}

// reachDB reaches this machine's files and the stand-in's sessions in the locations.
func reachDB(m *dbagent.Module, ls ...location) journal.Reach {
	var sides move.SideSessions
	for _, l := range ls {
		sides = append(sides, move.Side{Machine: l.m, Module: m, Install: dbInstall(l)})
	}
	return journal.Reach{FS: func(string) (host.FS, error) { return host.LocalFS(), nil }, Sessions: sides}
}

func vendorEntries(j *journal.Journal) []journal.Entry {
	var out []journal.Entry
	for _, e := range j.Entries {
		if e.Op == journal.OpVendorSession {
			out = append(out, e)
		}
	}
	return out
}

// A move stages the session from the database (no file of the agent's is read), its program
// imports the placed copy before lineage is recorded, Seal stamps the session, and undo
// deletes it only while it was not used since.
func TestVirtualSessionMoveAndUndo(t *testing.T) {
	root := t.TempDir()
	box, here := newLocation(t, "box", root), newLocation(t, "here", root)
	seedDB(t, box)
	os.MkdirAll(dbInstall(here).Main(), 0o700)
	m := dbagent.New()
	ctx := context.Background()
	state := t.TempDir()
	in := dbInput(t, m, box, here)
	if _, err := os.Stat(in.Session.Path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("the source must have no anchor: the move reads the database")
	}
	p, err := move.Build(ctx, in, move.Options{TargetDir: here.repo})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Blockers) > 0 || len(p.ExpectedAbsent) != 1 {
		t.Fatalf("plan: %v %v", p.Blockers, p.ExpectedAbsent)
	}
	res, err := move.Apply(ctx, p, in, move.Env{StateDir: state})
	if err != nil {
		t.Fatal(err)
	}
	moved, ok := dbSessions(t, here, m)[dbSession]
	if !ok || moved.CWD != here.repo || moved.Size != 2 || m.Imports() != 1 {
		t.Fatalf("moved: %+v (%d imports)", moved, m.Imports())
	}
	if seg := readAll(t, here, m, dbInstall(here), moved); !mentions(seg, here.repo) || mentions(seg, box.repo) {
		t.Fatalf("paths must point here: %+v", seg.Nodes)
	}
	graph, err := lineage.Read(host.LocalFS(), moved.Path)
	if err != nil || graph == nil || len(graph.ActiveHops()) != 1 {
		t.Fatalf("lineage beside the anchor: %v %v", graph, err)
	}
	j, err := journal.Load(state, res.Journal)
	if err != nil {
		t.Fatal(err)
	}
	if v := vendorEntries(j); len(v) != 1 || !v[0].Created || v[0].Stamp == "" {
		t.Fatalf("the imported session in the journal: %+v", v)
	}
	if len(j.Receipts) == 0 || j.Receipts[0].Session == nil || j.Receipts[0].Stamp == "" {
		t.Fatalf("the destination receipt is not guarded by the session's stamp: %+v", j.Receipts)
	}

	// Used since: undo refuses, and forced deletes it with that work.
	if err := dbagent.Continue(dbInstall(here).Main(), dbSession, dbagent.Message{ID: "msg_3", Role: "user", Text: "And now?"}); err != nil {
		t.Fatal(err)
	}
	if err := j.Undo(ctx, reachDB(m, box, here), false); !errors.Is(err, journal.ErrChanged) || !strings.Contains(err.Error(), "continued after this") {
		t.Fatalf("undo over later work: %v", err)
	}
	if !dbagent.Exists(dbInstall(here).Main(), dbSession) {
		t.Fatal("an unforced undo deleted a used session")
	}
	if err := j.Undo(ctx, reachDB(m, box, here), true); err != nil {
		t.Fatal(err)
	}
	if dbagent.Exists(dbInstall(here).Main(), dbSession) {
		t.Fatal("a forced undo left the session")
	}
	if _, err := os.Stat(moved.Path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("undo left the anchor")
	}
	if !dbagent.Exists(dbInstall(box).Main(), dbSession) {
		t.Fatal("the source was touched")
	}
}

// Interrupted after the copy was placed and before its import, a move recovers by
// importing it then, exactly once; a failed import fails the move, which undo reverses.
func TestVirtualSessionInstallRecovery(t *testing.T) {
	root := t.TempDir()
	box, here := newLocation(t, "box", root), newLocation(t, "here", root)
	seedDB(t, box)
	os.MkdirAll(dbInstall(here).Main(), 0o700)
	m := dbagent.New()
	ctx := context.Background()
	in := dbInput(t, m, box, here)
	opt := move.Options{TargetDir: here.repo, OperationID: "vendor-install"}
	p, err := move.Build(ctx, in, opt)
	if err != nil {
		t.Fatal(err)
	}
	env := move.Env{StateDir: t.TempDir(), Failpoint: func(stage string) error {
		if stage == "native-written" {
			return errors.New("simulated process exit")
		}
		return nil
	}}
	if _, err := move.Apply(ctx, p, in, env); err == nil {
		t.Fatal("the fault did not interrupt the move")
	}
	if m.Imports() != 0 || dbagent.Exists(dbInstall(here).Main(), dbSession) {
		t.Fatal("imported before the failpoint")
	}
	if p, err = move.Build(ctx, in, opt); err != nil {
		t.Fatal(err)
	}
	env.Failpoint = nil
	res, err := move.Apply(ctx, p, in, env)
	if err != nil {
		t.Fatal(err)
	}
	if m.Imports() != 1 || !dbagent.Exists(dbInstall(here).Main(), dbSession) {
		t.Fatalf("recovery imported %d time(s)", m.Imports())
	}
	if again, err := move.Apply(ctx, p, in, env); err != nil || again.Journal != res.Journal || m.Imports() != 1 {
		t.Fatalf("a repeated request: %v (%d imports)", err, m.Imports())
	}
	graph, err := lineage.Read(host.LocalFS(), dbagent.Anchor(dbInstall(here).Main(), dbSession))
	if err != nil || graph == nil || len(graph.ActiveHops()) != 1 {
		t.Fatalf("recovered lineage: %v %v", graph, err)
	}

	// The agent refuses the import: the move fails before lineage, and undo removes the copy.
	root = t.TempDir()
	box, here = newLocation(t, "box", root), newLocation(t, "here", root)
	seedDB(t, box)
	if err := dbagent.Init(dbInstall(here).Main()); err != nil {
		t.Fatal(err)
	}
	in = dbInput(t, m, box, here)
	if p, err = move.Build(ctx, in, move.Options{TargetDir: here.repo}); err != nil {
		t.Fatal(err)
	}
	m.FailImport.Store(true)
	state := t.TempDir()
	res, err = move.Apply(ctx, p, in, move.Env{StateDir: state})
	m.FailImport.Store(false)
	if err == nil || !strings.Contains(err.Error(), "did not import") {
		t.Fatalf("a failed import: %v", err)
	}
	anchor := dbagent.Anchor(dbInstall(here).Main(), dbSession)
	if _, err := os.Stat(lineage.PathFor(anchor)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("lineage was recorded for a session that is not there")
	}
	j, err := journal.Load(state, res.Journal)
	if err != nil {
		t.Fatal(err)
	}
	if err := j.Undo(ctx, reachDB(m, box, here), false); err != nil {
		t.Fatalf("undo of a failed import: %v", err)
	}
	if _, err := os.Stat(anchor); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("undo left the placed copy")
	}

	// The agent there has no database: that is not "the session is gone", so undo cannot
	// tell unforced, and says so when forced.
	root = t.TempDir()
	box, here = newLocation(t, "box", root), newLocation(t, "here", root)
	seedDB(t, box)
	os.MkdirAll(dbInstall(here).Main(), 0o700)
	in = dbInput(t, m, box, here)
	if p, err = move.Build(ctx, in, move.Options{TargetDir: here.repo}); err != nil {
		t.Fatal(err)
	}
	m.FailImport.Store(true)
	res, err = move.Apply(ctx, p, in, move.Env{StateDir: state})
	m.FailImport.Store(false)
	if err == nil {
		t.Fatal("the import did not fail")
	}
	j, _ = journal.Load(state, res.Journal)
	if err := j.Undo(ctx, reachDB(m, box, here), false); !errors.Is(err, journal.ErrChanged) {
		t.Fatalf("undo with no database there: %v", err)
	}
	if err := j.Undo(ctx, reachDB(m, box, here), true); err != nil || len(j.Left) != 1 || !strings.Contains(j.Left[0], "no database") {
		t.Fatalf("a forced undo with no database there: %v %v", err, j.Left)
	}
}

// A copy here is replaced: it is backed up and deleted through the agent's program, and
// undo deletes the new one and imports the backup again. Kept both, the copy comes in under
// an id the agent mints. A session whose anchor is missing still counts as there.
func TestVirtualSessionReplaceAndKeepBoth(t *testing.T) {
	root := t.TempDir()
	box, here := newLocation(t, "box", root), newLocation(t, "here", root)
	seedDB(t, box)
	os.MkdirAll(dbInstall(here).Main(), 0o700)
	m := dbagent.New()
	ctx := context.Background()
	env := move.Env{StateDir: t.TempDir()}
	in := dbInput(t, m, box, here)
	p, err := move.Build(ctx, in, move.Options{TargetDir: here.repo})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := move.Apply(ctx, p, in, env); err != nil {
		t.Fatal(err)
	}
	// Work goes on at box, and comes here again over the copy left from the first move.
	if err := dbagent.Continue(dbInstall(box).Main(), dbSession, dbagent.Message{ID: "msg_3", Role: "user", Text: "Fixed?"}); err != nil {
		t.Fatal(err)
	}
	in = dbInput(t, m, box, here)
	if len(in.Copies) != 1 {
		t.Fatal("the copy here is not listed")
	}
	p, err = move.Build(ctx, in, move.Options{TargetDir: here.repo})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Blockers) > 0 || len(p.SetAside) != 1 {
		t.Fatalf("plan over the copy here: %v %v %s", p.Blockers, p.SetAside, p.Conflict)
	}
	res, err := move.Apply(ctx, p, in, env)
	if err != nil {
		t.Fatal(err)
	}
	if s := dbSessions(t, here, m)[dbSession]; s.Size != 3 {
		t.Fatalf("replaced: %+v", s)
	}
	j, _ := journal.Load(env.StateDir, res.Journal)
	if v := vendorEntries(j); len(v) != 2 || v[0].Created || v[0].Backup == "" || !v[1].Created {
		t.Fatalf("set aside, then imported: %+v", v)
	}
	if err := j.Undo(ctx, reachDB(m, box, here), false); err != nil {
		t.Fatal(err)
	}
	if s, ok := dbSessions(t, here, m)[dbSession]; !ok || s.Size != 2 {
		t.Fatalf("restored: %+v", s)
	}

	// The copy here was continued too: kept both, the incoming one gets an id in the
	// agent's form.
	if err := dbagent.Continue(dbInstall(here).Main(), dbSession, dbagent.Message{ID: "msg_4", Role: "user", Text: "Here instead."}); err != nil {
		t.Fatal(err)
	}
	in = dbInput(t, m, box, here)
	p, err = move.Build(ctx, in, move.Options{TargetDir: here.repo, Conflict: move.ConflictKeepBoth})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(p.Placement.Key.Session), "ses_") || p.Placement.Key.Session == dbSession || len(p.Blockers) > 0 {
		t.Fatalf("kept both: %s %v", p.Placement.Key.Session, p.Blockers)
	}
	if _, err := move.Apply(ctx, p, in, env); err != nil {
		t.Fatal(err)
	}
	both := dbSessions(t, here, m)
	if both[dbSession].Size != 3 || both[p.Placement.Key.Session].Size != 3 {
		t.Fatalf("both copies: %+v", both)
	}

	// No anchor, but the agent has the session: it is there.
	root = t.TempDir()
	box, here = newLocation(t, "box", root), newLocation(t, "here", root)
	seedDB(t, box)
	os.MkdirAll(dbInstall(here).Main(), 0o700)
	in = dbInput(t, m, box, here)
	p, err = move.Build(ctx, in, move.Options{TargetDir: here.repo})
	if err != nil || len(p.ExpectedAbsent) != 1 {
		t.Fatalf("plan: %v %v", p.ExpectedAbsent, err)
	}
	seedDB(t, here) // the session appears here, with no anchor
	if _, err := move.Apply(ctx, p, in, env); err == nil || !strings.Contains(err.Error(), "appeared since planning") {
		t.Fatalf("a session that appeared without an anchor: %v", err)
	}
}

// Continuing another agent's session in one that keeps sessions in a database journals the
// session its program makes, under an id the agent mints, so undo deletes it while unused;
// a writer that picks its own id is followed.
func TestContinueIntoVirtualSession(t *testing.T) {
	for _, ownIDs := range []bool{false, true} {
		t.Run(map[bool]string{false: "asked id", true: "writer's id"}[ownIDs], func(t *testing.T) {
			root := t.TempDir()
			box, here := newLocation(t, "box", root), newLocation(t, "here", root)
			seed(t, box)
			os.MkdirAll(dbInstall(here).Main(), 0o700)
			m := dbagent.New()
			m.OwnIDs.Store(ownIDs)
			ctx := context.Background()
			state := t.TempDir()
			in := move.Input{Source: move.Side{Machine: box.m, Module: claude.New(), Install: box.in}, Session: list(t, box)[sid],
				Target: move.Side{Machine: here.m, Module: m, Install: dbInstall(here)}}
			p, err := move.Build(ctx, in, move.Options{TargetDir: here.repo})
			if err != nil {
				t.Fatal(err)
			}
			if p.Kind != move.KindContinue || len(p.Blockers) > 0 || !strings.HasPrefix(string(p.Placement.Key.Session), "ses_") {
				t.Fatalf("plan: %s %v %s", p.Kind, p.Blockers, p.Placement.Key.Session)
			}
			asked := p.Placement.Key.Session
			res, err := move.Apply(ctx, p, in, move.Env{StateDir: state})
			if err != nil {
				t.Fatal(err)
			}
			sessions := dbSessions(t, here, m)
			if len(sessions) != 1 {
				t.Fatalf("sessions here: %+v", sessions)
			}
			var made agent.SessionID
			for id := range sessions {
				made = id
			}
			if ownIDs == (made == asked) || !strings.Contains(res.Command, "dbagent -s "+string(made)) {
				t.Fatalf("made %s (asked %s); resumes with %s", made, asked, res.Command)
			}
			j, err := journal.Load(state, res.Journal)
			if err != nil {
				t.Fatal(err)
			}
			created := false
			for _, e := range vendorEntries(j) {
				if e.Created && e.Key.Session == made && e.Stamp != "" {
					created = true
				}
			}
			if !created {
				t.Fatalf("the session made is not journaled: %+v", vendorEntries(j))
			}
			if err := dbagent.Continue(dbInstall(here).Main(), made, dbagent.Message{ID: "msg_x", Role: "user", Text: "Go on."}); err != nil {
				t.Fatal(err)
			}
			if err := j.Undo(ctx, reachDB(m, here), false); !errors.Is(err, journal.ErrChanged) {
				t.Fatalf("undo after the session was used: %v", err)
			}
			if err := j.Undo(ctx, reachDB(m, here), true); err != nil {
				t.Fatal(err)
			}
			if dbagent.Exists(dbInstall(here).Main(), made) {
				t.Fatal("undo left the session")
			}
		})
	}
}

// Interrupted after its write, a continuation into a session kept in a database recovers
// by the session the write made, also when the writer gave it an id of its own: it imports
// nothing again, resumes that session, journals it at the stamp the write left, and records
// lineage beside it.
func TestContinueIntoVirtualSessionRecovery(t *testing.T) {
	for _, ownIDs := range []bool{false, true} {
		t.Run(map[bool]string{false: "asked id", true: "writer's id"}[ownIDs], func(t *testing.T) {
			root := t.TempDir()
			box, here := newLocation(t, "box", root), newLocation(t, "here", root)
			seed(t, box)
			os.MkdirAll(dbInstall(here).Main(), 0o700)
			m := dbagent.New()
			m.OwnIDs.Store(ownIDs)
			ctx := context.Background()
			in := move.Input{Source: move.Side{Machine: box.m, Module: claude.New(), Install: box.in}, Session: list(t, box)[sid],
				Target: move.Side{Machine: here.m, Module: m, Install: dbInstall(here)}}
			opt := move.Options{TargetDir: here.repo, OperationID: "continue-recovery"}
			p, err := move.Build(ctx, in, opt)
			if err != nil {
				t.Fatal(err)
			}
			env := move.Env{StateDir: t.TempDir(), Failpoint: func(stage string) error {
				if stage == "native-written" {
					return errors.New("simulated process exit")
				}
				return nil
			}}
			if _, err := move.Apply(ctx, p, in, env); err == nil {
				t.Fatal("the fault did not interrupt the continuation")
			}
			sessions := dbSessions(t, here, m)
			if len(sessions) != 1 || m.Imports() != 1 {
				t.Fatalf("written before the fault: %+v (%d imports)", sessions, m.Imports())
			}
			var made agent.SessionID
			for id := range sessions {
				made = id
			}
			if p, err = move.Build(ctx, in, opt); err != nil {
				t.Fatal(err)
			}
			env.Failpoint = nil
			res, err := move.Apply(ctx, p, in, env)
			if err != nil {
				t.Fatal(err)
			}
			if m.Imports() != 1 || len(dbSessions(t, here, m)) != 1 || !strings.Contains(res.Command, "dbagent -s "+string(made)) {
				t.Fatalf("recovered: %d imports, resumes with %s (made %s)", m.Imports(), res.Command, made)
			}
			j, err := journal.Load(env.StateDir, res.Journal)
			if err != nil {
				t.Fatal(err)
			}
			stamped := false
			for _, e := range vendorEntries(j) {
				stamped = stamped || e.Created && e.Key.Session == made && e.Stamp != ""
			}
			if !stamped {
				t.Fatalf("the session made is not journaled at its stamp: %+v", vendorEntries(j))
			}
			graph, err := lineage.Read(host.LocalFS(), dbagent.Anchor(dbInstall(here).Main(), made))
			if err != nil || graph == nil || len(graph.ActiveHops()) != 1 {
				t.Fatalf("recovered lineage: %v %v", graph, err)
			}
			if err := j.Undo(ctx, reachDB(m, here), false); err != nil {
				t.Fatal(err)
			}
			if dbagent.Exists(dbInstall(here).Main(), made) {
				t.Fatal("undo left the session")
			}
		})
	}
}

// A failed import after the copy here was set aside leaves only its backup: the error says
// so, running the same operation again imports the incoming copy, and undo then puts the
// copy that was here back from its backup.
func TestVirtualSessionImportRetryAfterSetAside(t *testing.T) {
	root := t.TempDir()
	box, here := newLocation(t, "box", root), newLocation(t, "here", root)
	seedDB(t, box)
	os.MkdirAll(dbInstall(here).Main(), 0o700)
	m := dbagent.New()
	ctx := context.Background()
	env := move.Env{StateDir: t.TempDir()}
	in := dbInput(t, m, box, here)
	p, err := move.Build(ctx, in, move.Options{TargetDir: here.repo})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := move.Apply(ctx, p, in, env); err != nil {
		t.Fatal(err)
	}
	if err := dbagent.Continue(dbInstall(box).Main(), dbSession, dbagent.Message{ID: "msg_3", Role: "user", Text: "Fixed?"}); err != nil {
		t.Fatal(err)
	}
	in = dbInput(t, m, box, here)
	opt := move.Options{TargetDir: here.repo, OperationID: "replace-retry"}
	if p, err = move.Build(ctx, in, opt); err != nil || len(p.SetAside) != 1 {
		t.Fatalf("plan: %v %v", p.SetAside, err)
	}
	m.FailImport.Store(true)
	_, err = move.Apply(ctx, p, in, env)
	m.FailImport.Store(false)
	if err == nil || !strings.Contains(err.Error(), "set aside and is backed up") || !strings.Contains(err.Error(), "running this again retries the import") {
		t.Fatalf("a failed import after a set-aside: %v", err)
	}
	if dbagent.Exists(dbInstall(here).Main(), dbSession) {
		t.Fatal("the copy here was not set aside")
	}
	imports := m.Imports()
	if p, err = move.Build(ctx, dbInput(t, m, box, here), opt); err != nil {
		t.Fatal(err)
	}
	res, err := move.Apply(ctx, p, in, env)
	if err != nil {
		t.Fatalf("the retry: %v", err)
	}
	if s := dbSessions(t, here, m)[dbSession]; s.Size != 3 || m.Imports() != imports+1 {
		t.Fatalf("retried: %+v (%d imports)", s, m.Imports()-imports)
	}
	j, _ := journal.Load(env.StateDir, res.Journal)
	if err := j.Undo(ctx, reachDB(m, box, here), false); err != nil {
		t.Fatal(err)
	}
	if s, ok := dbSessions(t, here, m)[dbSession]; !ok || s.Size != 2 {
		t.Fatalf("restored from its backup: %+v", s)
	}
}

// The copy here was used between its backup and its delete: the agent refuses the delete,
// the move fails with that copy kept, and undo leaves it as it is (nothing to put back).
func TestVirtualSessionSetAsideRefused(t *testing.T) {
	root := t.TempDir()
	box, here := newLocation(t, "box", root), newLocation(t, "here", root)
	seedDB(t, box)
	os.MkdirAll(dbInstall(here).Main(), 0o700)
	m := dbagent.New()
	ctx := context.Background()
	env := move.Env{StateDir: t.TempDir()}
	in := dbInput(t, m, box, here)
	p, err := move.Build(ctx, in, move.Options{TargetDir: here.repo})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := move.Apply(ctx, p, in, env); err != nil {
		t.Fatal(err)
	}
	if err := dbagent.Continue(dbInstall(box).Main(), dbSession, dbagent.Message{ID: "msg_3", Role: "user", Text: "Fixed?"}); err != nil {
		t.Fatal(err)
	}
	in = dbInput(t, m, box, here)
	if p, err = move.Build(ctx, in, move.Options{TargetDir: here.repo}); err != nil || len(p.SetAside) != 1 {
		t.Fatalf("plan: %v %v", p.SetAside, err)
	}
	m.BeforeDelete = func(id agent.SessionID) {
		m.BeforeDelete = nil
		if err := dbagent.Continue(dbInstall(here).Main(), id, dbagent.Message{ID: "msg_h", Role: "user", Text: "Still here."}); err != nil {
			t.Error(err)
		}
	}
	res, err := move.Apply(ctx, p, in, env)
	if !errors.Is(err, agent.ErrDiverged) {
		t.Fatalf("a refused set-aside: %v", err)
	}
	j, err := journal.Load(env.StateDir, res.Journal)
	if err != nil {
		t.Fatal(err)
	}
	if v := vendorEntries(j); len(v) != 0 {
		t.Fatalf("a delete the agent refused is journaled: %+v", v)
	}
	if err := j.Undo(ctx, reachDB(m, box, here), false); err != nil {
		t.Fatal(err)
	}
	if s := dbSessions(t, here, m)[dbSession]; s.Size != 3 {
		t.Fatalf("the copy here with its work: %+v", s)
	}
}

// A session at the target with no anchor counts as there when planning; --via import is
// refused for an agent that keeps its sessions in a database (it reads the session's file).
func TestVirtualSessionPlanChecks(t *testing.T) {
	root := t.TempDir()
	box, here := newLocation(t, "box", root), newLocation(t, "here", root)
	seedDB(t, box)
	seedDB(t, here) // there, with no anchor
	m := dbagent.New()
	ctx := context.Background()
	in := dbInput(t, m, box, here)
	in.Copies = nil // as a plan that did not list it sees it
	p, err := move.Build(ctx, in, move.Options{TargetDir: here.repo})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.ExpectedAbsent) != 0 {
		t.Fatalf("a session with no anchor is expected absent: %v", p.ExpectedAbsent)
	}
	cin := move.Input{Source: move.Side{Machine: box.m, Module: m, Install: dbInstall(box)}, Session: dbSessions(t, box, m)[dbSession],
		Target: move.Side{Machine: here.m, Module: claude.New(), Install: here.in}}
	if _, err := move.Build(ctx, cin, move.Options{TargetDir: here.repo, Via: move.ViaImport}); !errors.Is(err, agent.ErrUnsupported) || !strings.Contains(err.Error(), "database") {
		t.Fatalf("--via import from a database agent: %v", err)
	}
}

// Continuing a session kept in a database in another agent keeps the database agent's own
// copy here too: its program imports it, the journal holds it at its stamp, and undo
// deletes both.
func TestContinueFromVirtualSession(t *testing.T) {
	root := t.TempDir()
	box, here := newLocation(t, "box", root), newLocation(t, "here", root)
	seedDB(t, box)
	os.MkdirAll(dbInstall(here).Main(), 0o700)
	m := dbagent.New()
	ctx := context.Background()
	state := t.TempDir()
	cl := claude.New()
	in := move.Input{Source: move.Side{Machine: box.m, Module: m, Install: dbInstall(box)}, Session: dbSessions(t, box, m)[dbSession],
		Target: move.Side{Machine: here.m, Module: cl, Install: here.in},
		Native: &move.NativeSide{Target: move.Side{Machine: here.m, Module: m, Install: dbInstall(here)}}}
	p, err := move.Build(ctx, in, move.Options{TargetDir: here.repo})
	if err != nil {
		t.Fatal(err)
	}
	if p.Kind != move.KindContinue || len(p.Blockers) > 0 || p.NativeCopy == nil {
		t.Fatalf("plan: %s %v %v %v", p.Kind, p.Blockers, p.NativeCopy, p.Warnings)
	}
	res, err := move.Apply(ctx, p, in, move.Env{StateDir: state})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Warnings) > 0 {
		t.Fatalf("warnings: %v", res.Warnings)
	}
	if m.Imports() != 1 || !dbagent.Exists(dbInstall(here).Main(), dbSession) {
		t.Fatalf("the database agent's copy here: %d imports", m.Imports())
	}
	j, err := journal.Load(state, res.Journal)
	if err != nil {
		t.Fatal(err)
	}
	if v := vendorEntries(j); len(v) != 1 || !v[0].Created || v[0].Stamp == "" || v[0].Key.Session != dbSession {
		t.Fatalf("the copy kept here in the journal: %+v", v)
	}
	if err := j.Undo(ctx, reachDB(m, box, here), false); err != nil {
		t.Fatal(err)
	}
	if dbagent.Exists(dbInstall(here).Main(), dbSession) || !dbagent.Exists(dbInstall(box).Main(), dbSession) {
		t.Fatal("undo: the copy here must go, the source stay")
	}
}

// Interrupted after the agent imported the copy, and the session used before the move is
// recovered: the stamp taken right after the import stays the operation's, so the recovery
// does not take that work for the move's, and an unforced undo keeps it.
func TestVirtualSessionRecoveryKeepsImportStamp(t *testing.T) {
	root := t.TempDir()
	box, here := newLocation(t, "box", root), newLocation(t, "here", root)
	seedDB(t, box)
	os.MkdirAll(dbInstall(here).Main(), 0o700)
	m := dbagent.New()
	ctx := context.Background()
	in := dbInput(t, m, box, here)
	opt := move.Options{TargetDir: here.repo, OperationID: "import-stamp"}
	p, err := move.Build(ctx, in, opt)
	if err != nil {
		t.Fatal(err)
	}
	env := move.Env{StateDir: t.TempDir(), Failpoint: func(stage string) error {
		if stage == "vendor-imported" {
			return errors.New("simulated process exit")
		}
		return nil
	}}
	res, err := move.Apply(ctx, p, in, env)
	if err == nil || !dbagent.Exists(dbInstall(here).Main(), dbSession) {
		t.Fatalf("the fault: %v", err)
	}
	if err := dbagent.Continue(dbInstall(here).Main(), dbSession, dbagent.Message{ID: "msg_3", Role: "user", Text: "Used before the recovery."}); err != nil {
		t.Fatal(err)
	}
	env.Failpoint = nil
	if p, err = move.Build(ctx, in, opt); err != nil {
		t.Fatal(err)
	}
	_, _ = move.Apply(ctx, p, in, env) // the agent finds other work at the id
	j, err := journal.Load(env.StateDir, res.Journal)
	if err != nil {
		t.Fatal(err)
	}
	if err := j.Undo(ctx, reachDB(m, box, here), false); !errors.Is(err, journal.ErrChanged) {
		t.Fatalf("an unforced undo over the work done before the recovery: %v", err)
	}
	if s := dbSessions(t, here, m)[dbSession]; s.Size != 3 {
		t.Fatalf("the session used: %+v", s)
	}
}

// Moving a session again with nothing new syncs lineage only: its receipt beside the copy
// here is guarded by that session's stamp, as its anchor does not change when it is used.
func TestVirtualSessionNoWorkSync(t *testing.T) {
	root := t.TempDir()
	box, here := newLocation(t, "box", root), newLocation(t, "here", root)
	seedDB(t, box)
	os.MkdirAll(dbInstall(here).Main(), 0o700)
	m := dbagent.New()
	ctx := context.Background()
	env := move.Env{StateDir: t.TempDir()}
	in := dbInput(t, m, box, here)
	p, err := move.Build(ctx, in, move.Options{TargetDir: here.repo})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := move.Apply(ctx, p, in, env); err != nil {
		t.Fatal(err)
	}
	in = dbInput(t, m, box, here)
	if p, err = move.Build(ctx, in, move.Options{TargetDir: here.repo}); err != nil || !p.NoWork || len(p.Blockers) > 0 {
		t.Fatalf("plan: no work %v, %v %v", p.NoWork, p.Blockers, err)
	}
	res, err := move.Apply(ctx, p, in, env)
	if err != nil {
		t.Fatal(err)
	}
	j, _ := journal.Load(env.StateDir, res.Journal)
	guarded := false
	for _, r := range j.Receipts {
		guarded = guarded || r.Session != nil && r.Session.Session == dbSession && r.Stamp != ""
	}
	if !guarded || m.Imports() != 1 {
		t.Fatalf("receipts %+v (%d imports)", j.Receipts, m.Imports())
	}
}
