package e2e

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/roeehrl/hopsesh/agents/opencode"
	"github.com/roeehrl/hopsesh/internal/core/host"
	"github.com/roeehrl/hopsesh/internal/core/journal"
	"github.com/roeehrl/hopsesh/internal/core/lineage"
	"github.com/roeehrl/hopsesh/internal/core/move"
	"github.com/roeehrl/hopsesh/internal/testkit"
	"github.com/roeehrl/hopsesh/internal/testkit/fakeopencode"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

// OpenCode sessions move between machines through OpenCode's own program, here the
// stand-in OpenCode 2.3.0 (internal/testkit/fakeopencode) run as `opencode` from PATH:
// the source's export is staged, the target's `opencode session import` installs it (the
// parent, then its child) before lineage is recorded, and undo deletes it with `opencode
// session delete` only while it is as the move left it. No database is ever written by
// hopsesh.

// The fixture's session with a subagent, and the subagent's session.
const (
	ocParent agent.SessionID = "ses_e29a8385effeUhBGvRVeVFrDLo"
	ocChild  agent.SessionID = "ses_e29a832f5ffebHEWUP5fBLspvU"
)

// ocWorld puts the stand-in opencode on PATH and logs its calls; it returns the log and
// two locations whose machines have it.
func ocWorld(t *testing.T) (string, location, location) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the stand-in runs as a symbolic link to the test binary")
	}
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	if err := os.MkdirAll(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	self, _ := os.Executable()
	if err := os.Symlink(self, filepath.Join(bin, "opencode")); err != nil {
		t.Fatal(err)
	}
	testkit.UnsetAgentEnv(t)
	log := filepath.Join(root, "agents.log")
	t.Setenv("PATH", bin+string(os.PathListSeparator)+testPath())
	t.Setenv("HOME", root)
	t.Setenv("FAKE_AGENT_LOG", log)
	t.Setenv("FAKE_OPENCODE_SERVICE", "")
	var ls []location
	for _, name := range []string{"box", "here"} {
		l := newLocation(t, name, root)
		l.m.Facts.Binaries["opencode"] = agent.BinaryFact{Path: filepath.Join(bin, "opencode"), Version: "opencode v2.3.0"}
		ls = append(ls, l)
	}
	return log, ls[0], ls[1]
}

func ocInstall(l location) agent.Install {
	home := l.m.Facts.Home
	return agent.Install{Agent: "opencode", Version: "2.3.0", Binary: l.m.Facts.Binaries["opencode"].Path, MainRoot: "data", Present: true, Roots: map[string]string{
		"data": filepath.Join(home, ".local", "share", "opencode"), "state": filepath.Join(home, ".local", "state", "opencode"),
		"config": filepath.Join(home, ".config", "opencode")}}
}

func ocDB(l location) string { return filepath.Join(ocInstall(l).Root("data"), "opencode.db") }

// seedOpenCode imports the fixture's session and its subagent's into a location's
// OpenCode, in its repository.
func seedOpenCode(t *testing.T, l location) {
	t.Helper()
	for _, f := range []string{"export-04.json", "export-05.json"} {
		b, err := os.ReadFile(filepath.Join("../../agents/opencode/testdata/2.3.0", f))
		if err != nil {
			t.Fatal(err)
		}
		b = []byte(strings.ReplaceAll(string(b), "/home/u/git/demo", jsonText(l.repo)))
		if _, err := fakeopencode.Import(ocDB(l), b, l.repo, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
}

// useOpenCode continues a session, as a prompt in OpenCode does: a message, and the
// session's update time.
func useOpenCode(t *testing.T, l location, sid agent.SessionID, text string) {
	t.Helper()
	db, err := fakeopencode.Open(ocDB(l))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Now().UnixMilli()
	id := "msg_ffffffffffff" + strings.Repeat("u", 10) + time.Now().Format("150405.000")[7:]
	if _, err := db.Exec(`INSERT INTO session_message (id, session_id, type, seq, time_created, time_updated, data)
		VALUES (?, ?, 'user', (SELECT max(seq) + 1 FROM session_message WHERE session_id = ?), ?, ?, json_object('time', json_object('created', ?), 'text', ?))`,
		id, string(sid), string(sid), now, now, now, text); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE session_v2 SET time_updated = max(time_updated + 1, ?) WHERE id = ?`, now, string(sid)); err != nil {
		t.Fatal(err)
	}
}

func ocSessions(t *testing.T, l location, m *opencode.Module) map[agent.SessionID]agent.Summary {
	t.Helper()
	out := map[agent.SessionID]agent.Summary{}
	for _, s := range listAgent(t, l, m, ocInstall(l)) {
		out[s.Key.Session] = s
	}
	return out
}

func ocInput(t *testing.T, m *opencode.Module, from, to location) move.Input {
	t.Helper()
	s := ocSessions(t, from, m)[ocParent]
	if s.Path == "" {
		t.Fatalf("%s is not listed at %s", ocParent, from.m.Name)
	}
	lin, _ := lineage.Read(host.LocalFS(), s.Path)
	in := move.Input{Source: move.Side{Machine: from.m, Module: m, Install: ocInstall(from)}, Session: s, Lineage: lin,
		Target: move.Side{Machine: to.m, Module: m, Install: ocInstall(to)}}
	if c, ok := ocSessions(t, to, m)[ocParent]; ok {
		cl, _ := lineage.Read(host.LocalFS(), c.Path)
		in.Copies = []move.Copy{{Summary: c, Live: agent.LiveInfo{State: agent.Ended}, Lineage: cl}}
	}
	return in
}

func ocReach(m *opencode.Module, ls ...location) journal.Reach {
	var sides move.SideSessions
	for _, l := range ls {
		sides = append(sides, move.Side{Machine: l.m, Module: m, Install: ocInstall(l)})
	}
	return journal.Reach{FS: func(string) (host.FS, error) { return host.LocalFS(), nil }, Sessions: sides}
}

// calls are the logged opencode commands that start with prefix.
func calls(t *testing.T, log, prefix string) []string {
	t.Helper()
	b, _ := os.ReadFile(log)
	var out []string
	for _, l := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(l, "opencode "+prefix) {
			out = append(out, l)
		}
	}
	return out
}

// sessionThere reports whether a location's OpenCode has a session.
func sessionThere(t *testing.T, l location, sid agent.SessionID) bool {
	t.Helper()
	db, err := fakeopencode.Open(ocDB(l))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var n int
	if err := db.QueryRow("SELECT count(*) FROM session_v2 WHERE id = ?", string(sid)).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n == 1
}

// A move stages OpenCode's export of the session and its child, imports both at the
// target before lineage is recorded, resumes with a command checked there, and undo
// deletes both while unused; an undo after the copy was used is refused unless forced.
func TestOpenCodeMoveAndUndo(t *testing.T) {
	log, box, here := ocWorld(t)
	seedOpenCode(t, box)
	m := opencode.New()
	ctx := context.Background()
	state := t.TempDir()
	in := ocInput(t, m, box, here)
	p, err := move.Build(ctx, in, move.Options{TargetDir: here.repo})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Blockers) > 0 || len(p.Files.Files) != 2 {
		t.Fatalf("plan: %v, %d files", p.Blockers, len(p.Files.Files))
	}
	res, err := move.Apply(ctx, p, in, move.Env{StateDir: state})
	if err != nil {
		t.Fatal(err)
	}
	moved := ocSessions(t, here, m)[ocParent]
	if moved.CWD != here.repo || moved.Size != in.Session.Size || moved.Subagents != 1 {
		t.Fatalf("moved: %+v (source %+v)", moved, in.Session)
	}
	imports := calls(t, log, "session import")
	if len(imports) != 2 || !strings.Contains(imports[0], "/tmp/"+string(ocParent)+"-") || !strings.Contains(imports[0], ".json --directory "+here.repo+" --standalone") || !strings.Contains(imports[1], "/tmp/"+string(ocChild)+"-") {
		t.Fatalf("imports: %q", imports)
	}
	if len(calls(t, log, "session export "+string(ocParent))) == 0 {
		t.Fatal("the source was not staged from OpenCode's export")
	}
	if exp, err := fakeopencode.Export(ocDB(here), string(ocParent)); err != nil || !strings.Contains(string(exp), jsonText(here.repo)) || strings.Contains(string(exp), jsonText(box.repo)) {
		t.Fatalf("paths must point here: %v", err)
	}
	if seg := readAll(t, here, m, ocInstall(here), moved); seg.Cursor.Offset != in.Session.Size {
		t.Fatalf("read here: %d messages", seg.Cursor.Offset)
	}
	if !strings.Contains(res.Command, "opencode -s "+string(ocParent)) {
		t.Fatalf("resume: %s", res.Command)
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
	if calls(t, log, "service start") != nil || strings.Contains(strings.Join(calls(t, log, ""), "\n"), "started the background service") {
		t.Fatal("the background service was started")
	}

	// Used since: undo refuses, and leaves it.
	useOpenCode(t, here, ocParent, "And now?")
	if err := j.Undo(ctx, ocReach(m, box, here), false); !errors.Is(err, journal.ErrChanged) {
		t.Fatalf("undo over later work: %v", err)
	}
	if !sessionThere(t, here, ocParent) || len(calls(t, log, "session delete")) != 0 {
		t.Fatal("an unforced undo deleted a used session")
	}
	if err := j.Undo(ctx, ocReach(m, box, here), true); err != nil {
		t.Fatal(err)
	}
	if sessionThere(t, here, ocParent) || sessionThere(t, here, ocChild) {
		t.Fatal("a forced undo left the session or its child")
	}
	if !sessionThere(t, box, ocParent) || !sessionThere(t, box, ocChild) {
		t.Fatal("the source was touched")
	}
	if _, err := os.Stat(moved.Path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("undo left the anchor")
	}

	// Moved again, unused: undo deletes it at the stamp it journaled.
	in = ocInput(t, m, box, here)
	if p, err = move.Build(ctx, in, move.Options{TargetDir: here.repo}); err != nil || len(p.Blockers) > 0 {
		t.Fatalf("plan: %v %v", p.Blockers, err)
	}
	if res, err = move.Apply(ctx, p, in, move.Env{StateDir: state}); err != nil {
		t.Fatal(err)
	}
	j, _ = journal.Load(state, res.Journal)
	if err := j.Undo(ctx, ocReach(m, box, here), false); err != nil {
		t.Fatal(err)
	}
	if sessionThere(t, here, ocParent) {
		t.Fatal("undo left the session")
	}
}

// A copy here that was continued too is kept: the incoming one comes in beside it, under
// a new id with its child and messages under new ids, and undo removes only it.
func TestOpenCodeKeepBoth(t *testing.T) {
	log, box, here := ocWorld(t)
	seedOpenCode(t, box)
	m := opencode.New()
	ctx := context.Background()
	env := move.Env{StateDir: t.TempDir()}
	in := ocInput(t, m, box, here)
	p, err := move.Build(ctx, in, move.Options{TargetDir: here.repo})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := move.Apply(ctx, p, in, env); err != nil {
		t.Fatal(err)
	}
	useOpenCode(t, box, ocParent, "Work at box.")
	useOpenCode(t, here, ocParent, "Work here.")
	in = ocInput(t, m, box, here)
	p, err = move.Build(ctx, in, move.Options{TargetDir: here.repo, Conflict: move.ConflictKeepBoth})
	if err != nil {
		t.Fatal(err)
	}
	to := p.Placement.Key.Session
	if !strings.HasPrefix(string(to), "ses_") || to == ocParent || len(p.Blockers) > 0 {
		t.Fatalf("kept both: %s %v", to, p.Blockers)
	}
	res, err := move.Apply(ctx, p, in, env)
	if err != nil {
		t.Fatal(err)
	}
	both := ocSessions(t, here, m)
	if both[ocParent].Size != in.Session.Size || both[to].Size != in.Session.Size || both[to].Subagents != 1 || !strings.Contains(both[to].Title, "(from box)") {
		t.Fatalf("both copies: %+v / %+v", both[ocParent], both[to])
	}
	if !strings.Contains(res.Command, "opencode -s "+string(to)) {
		t.Fatalf("resume: %s", res.Command)
	}
	db, _ := fakeopencode.Open(ocDB(here))
	var kid string
	err = db.QueryRow("SELECT id FROM session_v2 WHERE parent_id = ?", string(to)).Scan(&kid)
	db.Close()
	if err != nil || kid == string(ocChild) || !sessionThere(t, here, ocChild) {
		t.Fatalf("the copy's child: %q %v", kid, err)
	}
	// A title recorded later (another rename or copy) does not stop this undo, which takes
	// out only the copy's own record.
	titles := filepath.Join(ocInstall(here).Root("data"), "hopsesh", "opencode", "titles.jsonl")
	later := `{"id":"ses_other","title":"renamed later"}` + "\n"
	if f, err := os.OpenFile(titles, os.O_APPEND|os.O_WRONLY, 0o600); err != nil {
		t.Fatal(err)
	} else {
		_, _ = f.WriteString(later)
		f.Close()
	}
	j, _ := journal.Load(env.StateDir, res.Journal)
	if err := j.Undo(ctx, ocReach(m, box, here), false); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(titles); string(b) != later {
		t.Fatalf("titles after undo: %q", b)
	}
	if sessionThere(t, here, to) || sessionThere(t, here, agent.SessionID(kid)) || !sessionThere(t, here, ocParent) || !sessionThere(t, here, ocChild) {
		t.Fatal("undo: the copy kept beside must go, with its child, and the one here stay")
	}
	if d := calls(t, log, "session delete"); len(d) != 1 || !strings.Contains(d[0], string(to)) {
		t.Fatalf("deletes: %q", d)
	}
}

// Moved again over an unchanged copy here, the copy is set aside (OpenCode's export of it
// and its child as a backup, then a stamp-guarded delete) and the newer one imported; undo
// deletes that and imports the backup again, the child too. The preview says what hopsesh
// cannot see about OpenCode.
func TestOpenCodeResyncAndUndo(t *testing.T) {
	log, box, here := ocWorld(t)
	seedOpenCode(t, box)
	m := opencode.New()
	ctx := context.Background()
	env := move.Env{StateDir: t.TempDir()}
	in := ocInput(t, m, box, here)
	p, err := move.Build(ctx, in, move.Options{TargetDir: here.repo})
	if err != nil || len(p.Blockers) > 0 {
		t.Fatal(err, p.Blockers)
	}
	if !slices.ContainsFunc(p.Warnings, func(w string) bool { return strings.Contains(w, "cannot see an OpenCode window") }) ||
		slices.ContainsFunc(p.Warnings, func(w string) bool { return strings.Contains(w, "not found") }) {
		t.Fatalf("warnings: %q", p.Warnings)
	}
	if _, err := move.Apply(ctx, p, in, env); err != nil {
		t.Fatal(err)
	}
	first := ocSessions(t, here, m)[ocParent].Size
	useOpenCode(t, box, ocParent, "More at box.")
	in = ocInput(t, m, box, here)
	if p, err = move.Build(ctx, in, move.Options{TargetDir: here.repo}); err != nil {
		t.Fatal(err)
	}
	if len(p.Blockers) > 0 || len(p.SetAside) != 1 {
		t.Fatalf("over the copy here: %v, %d set aside", p.Blockers, len(p.SetAside))
	}
	res, err := move.Apply(ctx, p, in, env)
	if err != nil {
		t.Fatal(err)
	}
	if s := ocSessions(t, here, m)[ocParent]; s.Size != first+1 || s.Subagents != 1 {
		t.Fatalf("replaced: %+v", s)
	}
	if d := calls(t, log, "session delete"); len(d) != 1 || !strings.Contains(d[0], string(ocParent)+" --standalone") {
		t.Fatalf("deletes: %q", d)
	}
	j, _ := journal.Load(env.StateDir, res.Journal)
	var backup string
	for _, e := range vendorEntries(j) {
		if !e.Created {
			backup = e.Backup
		}
	}
	if backup == "" {
		t.Fatal("no set-aside entry")
	}
	if err := j.Undo(ctx, ocReach(m, box, here), false); err != nil {
		t.Fatal(err)
	}
	if s, ok := ocSessions(t, here, m)[ocParent]; !ok || s.Size != first || s.Subagents != 1 || !sessionThere(t, here, ocChild) {
		t.Fatalf("restored: %v %+v", ok, s)
	}
	if !slices.ContainsFunc(calls(t, log, "session import"), func(c string) bool { return strings.Contains(c, backup) }) {
		t.Fatal("not restored from the backup")
	}
	if _, err := os.Stat(backup); !os.IsNotExist(err) {
		t.Fatal("the backup outlived the session put back")
	}
	if _, err := os.Stat(strings.TrimSuffix(backup, ".json") + ".children"); err == nil {
		if left, _ := os.ReadDir(strings.TrimSuffix(backup, ".json") + ".children"); len(left) > 0 {
			t.Fatalf("the child's backup outlived the session put back: %v", left)
		}
	}
}

// A copy last written by a newer OpenCode than the one here is refused before the copy
// here is set aside: the move fails with nothing changed here.
func TestOpenCodeNewerRefusedFirst(t *testing.T) {
	log, box, here := ocWorld(t)
	seedOpenCode(t, box)
	m := opencode.New()
	ctx := context.Background()
	env := move.Env{StateDir: t.TempDir()}
	in := ocInput(t, m, box, here)
	p, _ := move.Build(ctx, in, move.Options{TargetDir: here.repo})
	if _, err := move.Apply(ctx, p, in, env); err != nil {
		t.Fatal(err)
	}
	useOpenCode(t, box, ocParent, "More at box, with a newer OpenCode.")
	db, _ := fakeopencode.Open(ocDB(box))
	_, err := db.Exec(`UPDATE session_v2 SET version = '2.9.0' WHERE id = ?`, string(ocParent))
	db.Close()
	if err != nil {
		t.Fatal(err)
	}
	before := ocSessions(t, here, m)[ocParent]
	in = ocInput(t, m, box, here)
	if p, err = move.Build(ctx, in, move.Options{TargetDir: here.repo}); err != nil {
		t.Fatal(err)
	}
	if _, err := move.Apply(ctx, p, in, env); err == nil || !strings.Contains(err.Error(), "2.9.0") {
		t.Fatalf("a newer copy: %v", err)
	}
	if after, ok := ocSessions(t, here, m)[ocParent]; !ok || after.Size != before.Size {
		t.Fatalf("the copy here changed: %v %+v", ok, after)
	}
	if n := len(calls(t, log, "session delete")) + len(calls(t, log, "session import")); n != 2 {
		t.Fatalf("commands after the refusal: %q %q", calls(t, log, "session delete"), calls(t, log, "session import"))
	}
}

// Interrupted after the copy was placed and before its import, a move recovers by
// importing it then, once; a repeated request imports nothing more.
func TestOpenCodeInstallRecovery(t *testing.T) {
	log, box, here := ocWorld(t)
	seedOpenCode(t, box)
	m := opencode.New()
	ctx := context.Background()
	in := ocInput(t, m, box, here)
	opt := move.Options{TargetDir: here.repo, OperationID: "opencode-recovery"}
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
	if len(calls(t, log, "session import")) != 0 {
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
	if n := len(calls(t, log, "session import")); n != 2 || !sessionThere(t, here, ocParent) || !sessionThere(t, here, ocChild) {
		t.Fatalf("recovery: %d imports", n)
	}
	if again, err := move.Apply(ctx, p, in, env); err != nil || again.Journal != res.Journal || len(calls(t, log, "session import")) != 2 {
		t.Fatalf("a repeated request: %v", err)
	}
	graph, err := lineage.Read(host.LocalFS(), ocSessions(t, here, m)[ocParent].Path)
	if err != nil || graph == nil || len(graph.ActiveHops()) != 1 {
		t.Fatalf("recovered lineage: %v %v", graph, err)
	}

	// Interrupted after the import: the recovery finds the session as imported, and imports
	// nothing again.
	_, box, here = ocWorld(t)
	seedOpenCode(t, box)
	in = ocInput(t, m, box, here)
	opt.OperationID = "opencode-recovery-after-import"
	if p, err = move.Build(ctx, in, opt); err != nil {
		t.Fatal(err)
	}
	env.Failpoint = func(stage string) error {
		if stage == "vendor-imported" {
			return errors.New("simulated process exit")
		}
		return nil
	}
	if _, err := move.Apply(ctx, p, in, env); err == nil {
		t.Fatal("the fault did not interrupt the move")
	}
	before := len(calls(t, log, "session import"))
	env.Failpoint = nil
	if p, err = move.Build(ctx, in, opt); err != nil {
		t.Fatal(err)
	}
	if _, err := move.Apply(ctx, p, in, env); err != nil {
		t.Fatal(err)
	}
	if n := len(calls(t, log, "session import")); n != before {
		t.Fatalf("the recovery imported %d time(s) again", n-before)
	}
}

// A peer's package carries the session's and its child's files made at the sender; the
// receiver, whose snapshot of the sender has no database and runs nothing, installs them.
func TestOpenCodePeerPackage(t *testing.T) {
	log, box, here := ocWorld(t)
	seedOpenCode(t, box)
	m := opencode.New()
	ctx := context.Background()
	s := ocSessions(t, box, m)[ocParent]
	boxIn := ocInstall(box)
	h, err := box.m.For(ctx, m.Spec(), boxIn, nil)
	if err != nil {
		t.Fatal(err)
	}
	b, err := m.Bundle(ctx, h, boxIn, s)
	if err != nil || len(b.Files) != 2 {
		t.Fatalf("bundle: %+v %v", b, err)
	}
	var files []host.SnapshotFile
	for _, f := range b.Files {
		data, err := m.Materialize(ctx, h, boxIn, s, f)
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, host.SnapshotFile{Path: filepath.Join(boxIn.Root(f.Root), filepath.FromSlash(f.Rel)), Data: data, Mode: 0o600, ModTime: time.Now()})
	}
	if err := box.m.CommitIdentity(ctx); err != nil {
		t.Fatal(err)
	}
	snap := host.NewSnapshot("box", host.Facts{OS: runtime.GOOS, Home: box.m.Facts.Home, Endpoint: box.m.Facts.Endpoint}, files)
	in := move.Input{Source: move.Side{Machine: snap, Module: m, Install: boxIn}, Session: s, Target: move.Side{Machine: here.m, Module: m, Install: ocInstall(here)}}
	p, err := move.Build(ctx, in, move.Options{TargetDir: here.repo})
	if err != nil || len(p.Blockers) > 0 {
		t.Fatalf("plan from a snapshot: %v %v", err, p.Blockers)
	}
	if _, err := move.Apply(ctx, p, in, move.Env{StateDir: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	got := ocSessions(t, here, m)[ocParent]
	if got.CWD != here.repo || got.Size != s.Size || got.Subagents != 1 {
		t.Fatalf("received: %+v", got)
	}
	if len(calls(t, log, "session import")) != 2 {
		t.Fatalf("imports: %q", calls(t, log, "session import"))
	}
}
