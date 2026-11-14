package opencode

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/roeehrl/hopsesh/sdk/agent"
	"github.com/roeehrl/hopsesh/sdk/agent/agenttest"
	"github.com/roeehrl/hopsesh/sdk/ir"
)

// Moves: OpenCode's program imports the copy the core placed (the parent first), a copy
// kept beside the original gets new ids, and sessions are deleted, backed up and restored
// only through OpenCode, a delete only at the stamp it names. The stand-in OpenCode plays
// the commands over the fake machine's database.

// placeCopy puts the files of a session's bundle at the anchor of id, as the core places a
// moved copy (for another id, with the session's id renamed in it, as its rewrite does).
func placeCopy(t *testing.T, fh *agenttest.FakeHost, m *Module, h agent.Host, in agent.Install, s agent.Summary, to agent.SessionID) {
	t.Helper()
	b, err := m.Bundle(ctx, h, in, s)
	if err != nil {
		t.Fatal(err)
	}
	mp, err := m.PlanMove(in, in, s, b, agent.Placement{Key: agent.SessionKey{Agent: id, Session: to}, SourceID: s.Key.Session})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range mp.Files {
		data, err := m.Materialize(ctx, h, in, s, f.From)
		if err != nil {
			t.Fatal(err)
		}
		if to != s.Key.Session {
			data = []byte(strings.ReplaceAll(string(data), string(s.Key.Session), string(to)))
		}
		fh.Put(dataDir+"/"+f.ToRel, data, timeZero)
	}
}

func listing(t *testing.T, m *Module, h agent.Host, in agent.Install) map[agent.SessionID]agent.Summary {
	t.Helper()
	l, err := m.List(ctx, h, in)
	if err != nil {
		t.Fatal(err)
	}
	out := map[agent.SessionID]agent.Summary{}
	for _, s := range l.Sessions {
		out[s.Key.Session] = s
	}
	return out
}

func countCommands(f *fakeOpenCode, prefix string) int {
	n := 0
	for _, c := range f.commands() {
		if strings.HasPrefix(c, prefix) {
			n++
		}
	}
	return n
}

// A session and its child are imported from the placed copy, the parent first, in the
// placement's folder; running the install again imports nothing; a session there with
// other messages is ErrDiverged.
func TestAfterInstallImportsFamily(t *testing.T) {
	fh, f := newMachine(t, setup{})
	m, h, in := open(t, fh)
	s := listing(t, m, h, in)[sesSubagent]
	placeCopy(t, fh, m, h, in, s, sesSubagent)
	st, err := m.SessionStamp(ctx, h, in, sesSubagent)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Delete(ctx, h, in, sesSubagent, st); err != nil {
		t.Fatal(err)
	}
	if _, ok := listing(t, m, h, in)[sesSubagent]; ok {
		t.Fatal("not deleted")
	}
	pl := agent.Placement{Key: s.Key, SourceID: s.Key.Session, CWD: home + "/git/elsewhere"}
	if err := m.AfterInstall(ctx, h, in, s.Key, pl); err != nil {
		t.Fatal(err)
	}
	var imports []string
	for _, c := range f.commands() {
		if strings.HasPrefix(c, "session import") {
			imports = append(imports, c)
		}
	}
	if len(imports) != 2 || !strings.Contains(imports[0], "/tmp/"+sesSubagent+"-") || !strings.Contains(imports[0], ".json --directory "+home+"/git/elsewhere --standalone") ||
		!strings.Contains(imports[1], "/tmp/"+sesChild+"-") || !strings.Contains(imports[1], ".json --directory "+home+"/git/elsewhere") {
		t.Fatalf("imports: %q", imports)
	}
	got := listing(t, m, h, in)[sesSubagent]
	if got.CWD != home+"/git/elsewhere" || got.Size != s.Size || got.Subagents != 1 {
		t.Fatalf("imported: %+v", got)
	}
	if err := m.AfterInstall(ctx, h, in, s.Key, pl); err != nil || countCommands(f, "session import") != 2 {
		t.Fatalf("again: %v, %d imports", err, countCommands(f, "session import"))
	}
	// Used since: the same install finds other messages.
	if err := fh.ExecSQL(dbPath, `INSERT INTO session_message (id, session_id, type, seq, time_created, time_updated, data) VALUES
		('msg_ffffffffffffUsedSinceThere', ?, 'user', 99, 1, 1, '{"time":{"created":1},"text":"more"}')`, sesSubagent); err != nil {
		t.Fatal(err)
	}
	if err := m.AfterInstall(ctx, h, in, s.Key, pl); !errors.Is(err, agent.ErrDiverged) {
		t.Fatalf("over other messages: %v", err)
	}
}

// OpenCode's import exits 0 when the id is taken, saying so on stderr: that is no success,
// and neither is an exit 0 that names no session.
func TestImportSuccessDetection(t *testing.T) {
	fh, f := newMachine(t, setup{})
	_, h, _ := open(t, fh)
	svc := service{State: serviceStopped}
	for _, tc := range []struct {
		res  agent.Result
		want func(error) bool
	}{
		{agent.Result{Stdout: []byte("Imported session: ses_x\n")}, func(err error) bool { return err == nil }},
		{agent.Result{Stderr: []byte("Session already exists\n")}, func(err error) bool { return errors.Is(err, errExists) }},
		{agent.Result{Stdout: []byte("Imported session: ses_other\n")}, func(err error) bool { return err != nil && !errors.Is(err, errExists) }},
		{agent.Result{}, func(err error) bool { return err != nil && !errors.Is(err, errExists) }},
		{agent.Result{Code: 1, Stderr: []byte("Error: Failed to import session: Bad Request")}, func(err error) bool {
			return err != nil && strings.Contains(err.Error(), "Bad Request")
		}},
	} {
		f.onCall = func(argv []string) (agent.Result, bool) { return tc.res, argv[0] == "session" }
		if err := importFile(ctx, h, svc, "ses_x", "/tmp/x.json", "/home/u/git/demo"); !tc.want(err) {
			t.Errorf("%+v: %v", tc.res, err)
		}
	}
	// In AfterInstall, "already exists" with other messages there is ErrDiverged.
	f.onCall = nil
	tried := countCommands(f, "session import")
	m, h, in := open(t, fh)
	s := listing(t, m, h, in)[sesFiles]
	placeCopy(t, fh, m, h, in, s, sesFiles)
	data, _ := fh.Get(anchorOf(h, in, sesFiles))
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	fh.Put(anchorOf(h, in, sesFiles), []byte(strings.Join(lines[:len(lines)-1], "\n")+"\n"), timeZero)
	if err := m.AfterInstall(ctx, h, in, s.Key, agent.Placement{Key: s.Key, SourceID: s.Key.Session, CWD: home + "/git/demo"}); !errors.Is(err, agent.ErrDiverged) {
		t.Fatalf("a session there with other messages: %v", err)
	}
	if countCommands(f, "session import") != tried {
		t.Fatal("imported over a session there")
	}
}

// Kept both, the copy comes in under the core's id, its child and every message under
// new ids made from the old ones, its child hanging from the copy; the original stays. A
// second run makes the same ids, so it imports nothing.
func TestKeepBothRemapsIDs(t *testing.T) {
	fh, f := newMachine(t, setup{})
	m, h, in := open(t, fh)
	s := listing(t, m, h, in)[sesSubagent]
	to := m.NewSessionID(in, time.Now())
	placeCopy(t, fh, m, h, in, s, to)
	key := agent.SessionKey{Agent: id, Session: to}
	pl := agent.Placement{Key: key, SourceID: s.Key.Session, CWD: home + "/git/demo", Title: "subagent (from box)"}
	if err := m.AfterInstall(ctx, h, in, key, pl); err != nil {
		t.Fatal(err)
	}
	all := listing(t, m, h, in)
	orig, copied := all[sesSubagent], all[to]
	if orig.Size != s.Size || copied.Size != s.Size || copied.Subagents != 1 || copied.Title != "subagent (from box)" || copied.TitleSource != "custom" {
		t.Fatalf("original %+v, copy %+v", orig, copied)
	}
	r, _, _ := openSQL(ctx, h, in)
	kids, err := r.descendants(ctx, to)
	if err != nil || len(kids) != 1 || kids[0] == sesChild || !strings.HasPrefix(string(kids[0]), "ses_e29a832f5ffe") {
		t.Fatalf("the copy's child: %v %v", kids, err)
	}
	_, origMsgs, _ := r.transcript(ctx, sesSubagent)
	_, copyMsgs, _ := r.transcript(ctx, to)
	for i := range origMsgs {
		if origMsgs[i].ID == copyMsgs[i].ID || origMsgs[i].ID[:16] != copyMsgs[i].ID[:16] {
			t.Fatalf("message %d: %s and %s", i, origMsgs[i].ID, copyMsgs[i].ID)
		}
	}
	// The subagent call's session is the copy's child; what the model read is unchanged.
	calls := 0
	for i, msg := range copyMsgs {
		for _, it := range msg.items() {
			if it.Type != "tool" || it.Name != "subagent" {
				continue
			}
			calls++
			var md struct {
				SessionID string `json:"sessionID"`
			}
			_ = json.Unmarshal(it.State.Metadata["sessionID"], &md.SessionID)
			if md.SessionID != string(kids[0]) {
				t.Fatalf("the subagent call names %s", md.SessionID)
			}
			if orig := origMsgs[i].items(); len(orig) != len(msg.items()) {
				t.Fatal("the copy's content differs")
			}
		}
	}
	origSeg, _ := segment(session{}, origMsgs, ir.Cursor{})
	copySeg, _ := segment(session{}, copyMsgs, ir.Cursor{})
	if calls != 1 || origSeg.Cursor.Head != copySeg.Cursor.Head {
		t.Fatalf("%d subagent calls; heads %s and %s", calls, origSeg.Cursor.Head, copySeg.Cursor.Head)
	}
	n := countCommands(f, "session import")
	if err := m.AfterInstall(ctx, h, in, key, pl); err != nil || countCommands(f, "session import") != n {
		t.Fatalf("again: %v", err)
	}
	if a, b := remappedID("msg_1d6579dae001EtgMTrA47usaHf_5", string(to)), remappedID("msg_1d6579dae001EtgMTrA47usaHf_5", string(to)); a != b ||
		!strings.HasPrefix(a, "msg_1d6579dae001") || !strings.HasSuffix(a, "_5") || len(a) != len("msg_1d6579dae001EtgMTrA47usaHf_5") {
		t.Fatalf("a fork's message id: %s", a)
	}
}

// A delete names the stamp it deletes: none is refused, a stale one deletes nothing, and
// a session gone is ErrNotFound. Its children go with it.
func TestDeleteIsStampGuarded(t *testing.T) {
	fh, f := newMachine(t, setup{})
	m, h, in := open(t, fh)
	if err := m.Delete(ctx, h, in, sesSubagent, ""); !errors.Is(err, agent.ErrDenied) {
		t.Fatalf("no stamp: %v", err)
	}
	if err := m.Delete(ctx, h, in, sesSubagent, stampFor(1, 1)); !errors.Is(err, agent.ErrDiverged) {
		t.Fatalf("a stale stamp: %v", err)
	}
	if countCommands(f, "session delete") != 0 {
		t.Fatal("deleted at a stale stamp")
	}
	st, _ := m.SessionStamp(ctx, h, in, sesSubagent)
	if err := m.Delete(ctx, h, in, sesSubagent, st); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(f.commands(), "session delete "+sesSubagent+" --standalone") {
		t.Fatalf("commands: %q", f.commands())
	}
	if _, err := m.SessionStamp(ctx, h, in, sesChild); !errors.Is(err, agent.ErrNotFound) {
		t.Fatalf("the child: %v", err)
	}
	if err := m.Delete(ctx, h, in, sesSubagent, st); !errors.Is(err, agent.ErrNotFound) {
		t.Fatalf("gone: %v", err)
	}
}

// A backup is OpenCode's export of the session and its child; restoring it imports both
// again, a second restore changes nothing, and a restore over newer work is ErrDiverged.
func TestBackupAndRestore(t *testing.T) {
	fh, f := newMachine(t, setup{})
	m, h, in := open(t, fh)
	before := listing(t, m, h, in)[sesSubagent]
	backup, st, err := m.Backup(ctx, h, in, sesSubagent)
	if err != nil || !strings.HasPrefix(backup, dataDir+"/hopsesh/opencode/backup/"+sesSubagent+"-") {
		t.Fatalf("backup %s: %v", backup, err)
	}
	kid, ok := fh.Get(strings.TrimSuffix(backup, ".json") + ".children/001-" + sesChild + ".json")
	if !ok || !strings.Contains(string(kid), `"parentID": "`+sesSubagent+`"`) {
		t.Fatalf("the child's backup: %s", kid)
	}
	if err := m.Delete(ctx, h, in, sesSubagent, st); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if _, err := m.Restore(ctx, h, in, sesSubagent, backup); err != nil {
			t.Fatalf("restore %d: %v", i+1, err)
		}
	}
	if n := countCommands(f, "session import "+backup); n != 1 {
		t.Fatalf("the backup was imported %d times", n)
	}
	after := listing(t, m, h, in)[sesSubagent]
	if after.Size != before.Size || after.Subagents != 1 || after.CWD != before.CWD {
		t.Fatalf("restored: %+v, was %+v", after, before)
	}
	if err := fh.ExecSQL(dbPath, `INSERT INTO session_message (id, session_id, type, seq, time_created, time_updated, data) VALUES
		('msg_ffffffffffffNewerWorkHere', ?, 'user', 99, 1, 1, '{"time":{"created":1},"text":"newer"}')`, sesSubagent); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Restore(ctx, h, in, sesSubagent, backup); !errors.Is(err, agent.ErrDiverged) {
		t.Fatalf("over newer work: %v", err)
	}
}

// Materialize takes OpenCode's export, so the session's permission rules travel; where
// OpenCode's program does not run, a session with media is refused.
func TestMaterializeCarriesWhatImportReads(t *testing.T) {
	fh, f := newMachine(t, setup{})
	m, h, in := open(t, fh)
	s := listing(t, m, h, in)[sesFiles]
	// The rules OpenCode's PATCH sets (its column is named like a table hopsesh never reads).
	if err := fh.ExecSQL(dbPath, `UPDATE session_v2 SET permission = '[{"action":"edit","resource":"*","effect":"deny"}]' WHERE id = ?`, sesFiles); err != nil {
		t.Fatal(err)
	}
	b, _ := m.Bundle(ctx, h, in, s)
	data, err := m.Materialize(ctx, h, in, s, b.Files[0])
	if err != nil || !strings.Contains(strings.SplitN(string(data), "\n", 2)[0], `"permissions":[{"action":"edit"`) {
		t.Fatalf("%s %v", strings.SplitN(string(data), "\n", 2)[0], err)
	}
	var first info
	if json.Unmarshal([]byte(strings.SplitN(string(data), "\n", 2)[0]), &first) != nil || first.Version != "2.3.0" {
		t.Fatalf("the version that wrote it: %+v", first)
	}
	delete(fh.Programs, binName)
	_ = f
	if _, err := m.Materialize(ctx, h, in, s, b.Files[0]); err != nil {
		t.Fatalf("from the database: %v", err)
	}
	if err := fh.ExecSQL(dbPath, `UPDATE session_message SET data = json_set(data, '$.files', json('[{"data":"media:abc","mime":"image/png","source":{"type":"inline"}}]')) WHERE session_id = ? AND type = 'user'`, sesFiles); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Materialize(ctx, h, in, s, b.Files[0]); !errors.Is(err, agent.ErrUnsupported) {
		t.Fatalf("media from the database: %v", err)
	}
}

// A staged copy is read as a file, never from the database, though the database has a
// session at its id.
func TestReadStagedFile(t *testing.T) {
	fh, _ := newMachine(t, setup{})
	m, h, in := open(t, fh)
	s := listing(t, m, h, in)[sesFiles]
	b, _ := m.Bundle(ctx, h, in, s)
	data, _ := m.Materialize(ctx, h, in, s, b.Files[0])
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	fh.Put("/staging/out/x.jsonl", []byte(strings.Join(lines[:3], "\n")+"\n"), timeZero)
	staged := s
	staged.Path = "/staging/out/x.jsonl"
	seg, err := m.Read(ctx, fh, agent.Install{}, staged, ir.Cursor{})
	if err != nil || seg.Cursor.Offset != 2 {
		t.Fatalf("staged: %d messages, %v", seg.Cursor.Offset, err)
	}
	full, err := m.Read(ctx, h, in, s, ir.Cursor{})
	if err != nil || full.Cursor.Offset != s.Size {
		t.Fatalf("the session: %d, %v", full.Cursor.Offset, err)
	}
}

// A staged copy must be the session at the placement's id, and each child file the child
// it is named for.
func TestVerifyStagedCopy(t *testing.T) {
	fh, _ := newMachine(t, setup{})
	m, h, in := open(t, fh)
	s := listing(t, m, h, in)[sesSubagent]
	b, _ := m.Bundle(ctx, h, in, s)
	p := agent.Placement{Key: s.Key, SourceID: s.Key.Session}
	mp, _ := m.PlanMove(in, in, s, b, p)
	staged := map[string]string{}
	for i, f := range mp.Files {
		data, _ := m.Materialize(ctx, h, in, s, f.From)
		path := "/staging/" + string(rune('a'+i))
		fh.Put(path, data, timeZero)
		staged[agent.StagedKey(f)] = path
	}
	if err := m.Verify(ctx, fh, mp, staged, p); err != nil {
		t.Fatal(err)
	}
	other := p
	other.Key.Session = sesFiles
	if err := m.Verify(ctx, fh, mp, staged, other); err == nil {
		t.Fatal("a copy of another session verified")
	}
}

// A folder change goes through OpenCode's running service and waits for its
// location-switched message; with the service stopped it is refused before anything runs.
func TestChangeDir(t *testing.T) {
	fh, f := newMachine(t, setup{})
	m, h, in := open(t, fh)
	s := listing(t, m, h, in)[sesFiles]
	to := home + "/git/elsewhere"
	p, err := m.PlanChangeDir(ctx, h, in, s, to)
	if err != nil || len(p.Blockers) != 1 || p.Method != agent.DirAgent || p.Main != s.Path {
		t.Fatalf("with the service stopped: %+v %v", p, err)
	}
	if err := m.ChangeDir(ctx, h, in, s, p); !errors.Is(err, errNoService) || countCommands(f, "api") != 0 {
		t.Fatalf("changed with the service stopped: %v %q", err, f.commands())
	}
	f.status = "http://127.0.0.1:49374"
	if p, err = m.PlanChangeDir(ctx, h, in, s, to); err != nil || len(p.Blockers) != 0 {
		t.Fatalf("plan: %+v %v", p, err)
	}
	stamp, _ := m.SessionStamp(ctx, h, in, sesFiles)
	if err := m.ChangeDir(ctx, h, in, s, p); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(f.commands(), `api POST /api/session/`+sesFiles+`/move -d {"directory":"`+to+`"}`) {
		t.Fatalf("commands: %q", f.commands())
	}
	got := listing(t, m, h, in)[sesFiles]
	if now, _ := m.SessionStamp(ctx, h, in, sesFiles); got.CWD != to || now == stamp {
		t.Fatalf("moved to %s, stamp %s (was %s)", got.CWD, now, stamp)
	}
	// OpenCode takes the move and never makes it: hopsesh says so after a while.
	defer func(w time.Duration) { cdWait = w }(cdWait)
	cdWait = 300 * time.Millisecond
	f.onCall = func(argv []string) (agent.Result, bool) {
		return agent.Result{}, argv[0] == "api" && argv[1] == "POST"
	}
	back, _ := m.PlanChangeDir(ctx, h, in, got, home+"/git/demo")
	if err := m.ChangeDir(ctx, h, in, got, back); err == nil || !strings.Contains(err.Error(), "has not made it") {
		t.Fatalf("a move never made: %v", err)
	}
	if m.InSession(in, to) != "/cd "+to {
		t.Error(m.InSession(in, to))
	}
}

// OpenCode's forks are verified message by message up to their boundary ("through" its
// last message, or "before" one); a fork whose parent is gone is a session of its own.
func TestNativeForks(t *testing.T) {
	fh, _ := newMachine(t, setup{})
	m, h, in := open(t, fh)
	all := listing(t, m, h, in)
	parent := all[sesFiles]
	for _, fork := range []string{sesForkAll, sesForkPre} {
		child := all[agent.SessionID(fork)]
		if child.NativeParent != sesFiles {
			t.Fatalf("%s names %s", fork, child.NativeParent)
		}
		proof, err := m.VerifyNativeFork(ctx, h, in, parent, child)
		if err != nil || len(proof) == 0 {
			t.Fatalf("%s: %v", fork, err)
		}
		seg, _ := m.Read(ctx, h, in, child, ir.Cursor{})
		if proof[0].ChildAnchor != seg.Nodes[0].Native.Anchor {
			t.Fatalf("%s: anchors %+v", fork, proof[0])
		}
		if _, err := m.VerifyNativeFork(ctx, h, in, all[sesShell], child); !errors.Is(err, agent.ErrDiverged) {
			t.Fatalf("another parent: %v", err)
		}
		if gone, err := m.NativeParentGone(ctx, h, in, child); err != nil || gone {
			t.Fatalf("parent there: %v %v", gone, err)
		}
	}
	child := all[sesForkAll]
	if err := fh.ExecSQL(dbPath, `UPDATE session_message SET data = json_set(data, '$.text', 'changed') WHERE session_id = ? AND type = 'user'`, sesForkAll); err != nil {
		t.Fatal(err)
	}
	if _, err := m.VerifyNativeFork(ctx, h, in, parent, child); !errors.Is(err, agent.ErrDiverged) {
		t.Fatalf("a changed prefix: %v", err)
	}
	if err := fh.ExecSQL(dbPath, `DELETE FROM session_v2 WHERE id = ?`, sesFiles); err != nil {
		t.Fatal(err)
	}
	if gone, err := m.NativeParentGone(ctx, h, in, child); err != nil || !gone {
		t.Fatalf("parent deleted: %v %v", gone, err)
	}
}

// A copy last written by a newer OpenCode than the one here is refused before it is
// imported.
func TestInstallRefusesNewerSession(t *testing.T) {
	fh, f := newMachine(t, setup{})
	m, h, in := open(t, fh)
	s := listing(t, m, h, in)[sesFiles]
	placeCopy(t, fh, m, h, in, s, sesFiles)
	anchor := anchorOf(h, in, sesFiles)
	data, _ := fh.Get(anchor)
	fh.Put(anchor, []byte(strings.Replace(string(data), `"version":"2.3.0"`, `"version":"2.9.0"`, 1)), timeZero)
	st, _ := m.SessionStamp(ctx, h, in, sesFiles)
	_ = m.Delete(ctx, h, in, sesFiles, st)
	if err := m.AfterInstall(ctx, h, in, s.Key, agent.Placement{Key: s.Key, SourceID: s.Key.Session, CWD: home + "/git/demo"}); !errors.Is(err, agent.ErrUnsupported) ||
		!strings.Contains(err.Error(), "2.9.0") {
		t.Fatalf("a newer copy: %v", err)
	}
	if countCommands(f, "session import") != 0 {
		t.Fatal("imported")
	}
}

// Kept both, a summary's kept message (metadata.keepID) and a subagent's answer's session
// (metadata.childID) name the copy's own message and child, not the original's.
func TestKeepBothRemapsReferences(t *testing.T) {
	fh, _ := newMachine(t, setup{})
	m, h, in := open(t, fh)
	all := listing(t, m, h, in)
	r, _, _ := openSQL(ctx, h, in)
	for _, sid := range []agent.SessionID{sesLong, sesSubagent} {
		s := all[sid]
		to := m.NewSessionID(in, time.Now())
		placeCopy(t, fh, m, h, in, s, to)
		key := agent.SessionKey{Agent: id, Session: to}
		if err := m.AfterInstall(ctx, h, in, key, agent.Placement{Key: key, SourceID: sid, CWD: home + "/git/demo"}); err != nil {
			t.Fatal(err)
		}
		_, msgs, err := r.transcript(ctx, to)
		if err != nil {
			t.Fatal(err)
		}
		mine := map[string]bool{}
		for _, msg := range msgs {
			mine[msg.ID] = true
		}
		kids, _ := r.descendants(ctx, to)
		refs := 0
		for _, msg := range msgs {
			var v struct {
				Metadata struct {
					KeepID  string `json:"keepID"`
					ChildID string `json:"childID"`
				} `json:"metadata"`
			}
			_ = json.Unmarshal(msg.Raw, &v)
			if k := v.Metadata.KeepID; k != "" {
				refs++
				if !mine[k] {
					t.Errorf("%s: the copy's summary keeps %s, not one of its messages", sid, k)
				}
			}
			if c := v.Metadata.ChildID; c != "" {
				refs++
				if len(kids) != 1 || c != string(kids[0]) {
					t.Errorf("%s: the copy's subagent answer names %s, its child is %v", sid, c, kids)
				}
			}
		}
		if refs == 0 {
			t.Errorf("%s: no reference to check", sid)
		}
	}
	// A pending revert names a message of the copy too.
	info := map[string]json.RawMessage{"id": json.RawMessage(`"ses_a"`), "revert": json.RawMessage(`{"messageID":"msg_1d6579dae001EtgMTrA47usaHf"}`)}
	p := &placedSession{id: "ses_a", info: info, msgs: []json.RawMessage{json.RawMessage(`{"id":"msg_1d6579dae001EtgMTrA47usaHf"}`)}}
	remapIDs([]*placedSession{p}, "ses_a", "ses_b")
	if !strings.Contains(string(p.info["revert"]), remappedID("msg_1d6579dae001EtgMTrA47usaHf", "ses_b")) {
		t.Errorf("the revert: %s", p.info["revert"])
	}
}

// A child that OpenCode has with other messages fails the install, but not as the copy's
// own divergence: the root this run imported stays the operation's, for undo.
func TestChildDivergedKeepsRoot(t *testing.T) {
	fh, _ := newMachine(t, setup{})
	m, h, in := open(t, fh)
	s := listing(t, m, h, in)[sesSubagent]
	placeCopy(t, fh, m, h, in, s, sesSubagent)
	// OpenCode keeps a session at the child's id, of its own, with other messages.
	if err := fh.ExecSQL(dbPath, `UPDATE session_v2 SET parent_id = NULL WHERE id = ?`, sesChild); err != nil {
		t.Fatal(err)
	}
	if err := fh.ExecSQL(dbPath, `INSERT INTO session_message (id, session_id, type, seq, time_created, time_updated, data) VALUES
		('msg_ffffffffffffUsedSinceThere', ?, 'user', 99, 1, 1, '{"time":{"created":1},"text":"more"}')`, sesChild); err != nil {
		t.Fatal(err)
	}
	st, _ := m.SessionStamp(ctx, h, in, sesSubagent)
	if err := m.Delete(ctx, h, in, sesSubagent, st); err != nil {
		t.Fatal(err)
	}
	err := m.AfterInstall(ctx, h, in, s.Key, agent.Placement{Key: s.Key, SourceID: s.Key.Session, CWD: home + "/git/demo"})
	if err == nil || errors.Is(err, agent.ErrDiverged) || !strings.Contains(err.Error(), sesChild) {
		t.Fatalf("a diverged child: %v", err)
	}
	if _, err := m.SessionStamp(ctx, h, in, sesSubagent); err != nil {
		t.Fatalf("the root: %v", err)
	}
}

// A restore that stopped after the session (a child's import failed) is finished by the
// next one, which imports only what is missing.
func TestRestoreFinishesHalfDone(t *testing.T) {
	fh, f := newMachine(t, setup{})
	m, h, in := open(t, fh)
	backup, st, err := m.Backup(ctx, h, in, sesSubagent)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Delete(ctx, h, in, sesSubagent, st); err != nil {
		t.Fatal(err)
	}
	f.onCall = func(argv []string) (agent.Result, bool) {
		fails := len(argv) > 2 && argv[0] == "session" && argv[1] == "import" && strings.Contains(argv[2], ".children")
		return agent.Result{Code: 1, Stderr: []byte("Error: LocationNotFoundError")}, fails
	}
	if _, err := m.Restore(ctx, h, in, sesSubagent, backup); err == nil {
		t.Fatal("a failed child import restored")
	}
	if _, err := m.SessionStamp(ctx, h, in, sesSubagent); err != nil {
		t.Fatalf("the session: %v", err)
	}
	f.onCall = nil
	if _, err := m.Restore(ctx, h, in, sesSubagent, backup); err != nil {
		t.Fatal(err)
	}
	if n := countCommands(f, "session import "+backup); n != 1 {
		t.Fatalf("the session was imported %d times", n)
	}
	if _, err := m.SessionStamp(ctx, h, in, sesChild); err != nil {
		t.Fatalf("the child: %v", err)
	}
}

// When `service status` fails, nothing changes through OpenCode (a command could start the
// service, or run a private server beside it), and a move's export reads the database.
func TestUnknownServiceChangesNothing(t *testing.T) {
	fh, f := newMachine(t, setup{})
	m, h, in := open(t, fh)
	s := listing(t, m, h, in)[sesFiles]
	st, _ := m.SessionStamp(ctx, h, in, sesFiles)
	f.onCall = func(argv []string) (agent.Result, bool) {
		return agent.Result{Code: 1, Stderr: []byte("Error: timed out")}, argv[0] == "service"
	}
	if err := m.Delete(ctx, h, in, sesFiles, st); err == nil || !strings.Contains(err.Error(), "could not tell whether") {
		t.Fatalf("a delete: %v", err)
	}
	if _, err := m.Restore(ctx, h, in, sesFiles, "/nowhere.json"); err == nil {
		t.Fatal("a restore ran")
	}
	if err := m.Rename(ctx, h, in, s, "x"); err == nil {
		t.Fatal("a rename ran")
	}
	b, _ := m.Bundle(ctx, h, in, s)
	if _, err := m.Materialize(ctx, h, in, s, b.Files[0]); err != nil {
		t.Fatalf("a move's file: %v", err)
	}
	if n := countCommands(f, "session delete") + countCommands(f, "session export") + countCommands(f, "api"); n != 0 {
		t.Fatalf("commands: %q", f.commands())
	}
}

// A move's export runs a private server only when its OpenCode reads every session in the
// database, as for a change; else the session is read from the database.
func TestExportNeedsANewEnoughProgram(t *testing.T) {
	fh, f := newMachine(t, setup{})
	m, h, in := open(t, fh)
	s := listing(t, m, h, in)[sesFiles]
	if err := fh.ExecSQL(dbPath, `UPDATE session_v2 SET version = '2.9.0' WHERE id = ?`, sesShell); err != nil {
		t.Fatal(err)
	}
	b, _ := m.Bundle(ctx, h, in, s)
	if _, err := m.Materialize(ctx, h, in, s, b.Files[0]); err != nil {
		t.Fatal(err)
	}
	if _, _, err := m.Backup(ctx, h, in, sesFiles); err != nil {
		t.Fatal(err)
	}
	if n := countCommands(f, "session export"); n != 0 {
		t.Fatalf("exported through an older private server: %q", f.commands())
	}
}

// The core asks before anything changes whether OpenCode here imports a session last
// written by a version: the running service's version counts, else the program's.
func TestCheckInstall(t *testing.T) {
	fh, f := newMachine(t, setup{})
	m, h, in := open(t, fh)
	if err := m.CheckInstall(ctx, h, in, "2.9.0"); !errors.Is(err, agent.ErrUnsupported) || !strings.Contains(err.Error(), "2.9.0") {
		t.Fatalf("a newer session: %v", err)
	}
	for _, v := range []string{"2.3.0", "2.2.0", ""} {
		if err := m.CheckInstall(ctx, h, in, v); err != nil {
			t.Fatalf("%q: %v", v, err)
		}
	}
	f.status, f.version = serviceURL, "2.9.0"
	if err := m.CheckInstall(ctx, h, in, "2.9.0"); err != nil {
		t.Fatalf("the service reads it: %v", err)
	}
	if countCommands(f, "session import")+countCommands(f, "session delete") != 0 {
		t.Fatal("the check changed something")
	}
}

// A family is imported parents first; a member whose parent is not in it comes after the
// root, and members that do not hang from the root are refused.
func TestFamilyOrder(t *testing.T) {
	order, err := familyOrder([]string{"r", "b", "a", "x"}, []string{"", "a", "r", "gone"})
	if err != nil || !slices.Equal(order, []int{0, 2, 3, 1}) {
		t.Fatalf("%v %v", order, err)
	}
	if _, err := familyOrder([]string{"r", "a", "b"}, []string{"", "b", "a"}); err == nil || !strings.Contains(err.Error(), "do not hang from") {
		t.Fatalf("a cycle: %v", err)
	}
	root := &placedSession{id: "r", info: map[string]json.RawMessage{}}
	a := &placedSession{id: "a", parent: "b", info: map[string]json.RawMessage{}}
	b := &placedSession{id: "b", parent: "a", info: map[string]json.RawMessage{}}
	if _, err := parentsFirst(root, []*placedSession{a, b}); err == nil {
		t.Fatal("a placed cycle was imported")
	}
}

// A move's preview says a window that has the session open while it is idle cannot be
// seen, and that a Claude Agent session stays an OpenCode one.
func TestMoveWarnings(t *testing.T) {
	fh, _ := newMachine(t, setup{})
	m, h, in := open(t, fh)
	s := listing(t, m, h, in)[sesFiles]
	b, _ := m.Bundle(ctx, h, in, s)
	if len(b.Warnings) != 1 || !strings.Contains(b.Warnings[0], "cannot see an OpenCode window") {
		t.Fatalf("%q", b.Warnings)
	}
	if err := fh.ExecSQL(dbPath, `UPDATE session_v2 SET model = '{"id":"claude-sonnet","providerID":"claude-agent"}' WHERE id = ?`, sesFiles); err != nil {
		t.Fatal(err)
	}
	b, _ = m.Bundle(ctx, h, in, s)
	if len(b.Warnings) != 2 || !strings.Contains(b.Warnings[1], "Claude Agent") {
		t.Fatalf("%q", b.Warnings)
	}
}

// Where hopsesh cannot read the database it waits for a folder change through OpenCode's
// API, asking whether the service still runs before each call (a call without
// --standalone would start it again) and stopping when it does not.
func TestChangeDirThroughAPI(t *testing.T) {
	fh, f := newMachine(t, setup{})
	m, sqlHost, in := open(t, fh)
	s := listing(t, m, sqlHost, in)[sesFiles]
	h := agent.Confine(noSQL{fh}, m.Spec(), in)
	f.status = serviceURL
	to := home + "/git/elsewhere"
	p, _ := m.PlanChangeDir(ctx, h, in, s, to)
	if err := m.ChangeDir(ctx, h, in, s, p); err != nil {
		t.Fatal(err)
	}
	if countCommands(f, "api GET /api/session/"+sesFiles+"/message") == 0 {
		t.Fatalf("commands: %q", f.commands())
	}
	defer func(w time.Duration) { cdAPIPoll = w }(cdAPIPoll)
	cdAPIPoll = 10 * time.Millisecond
	posted := false
	f.onCall = func(argv []string) (agent.Result, bool) {
		switch {
		case argv[0] == "api" && argv[1] == "POST":
			posted = true
			return agent.Result{}, true // taken, never made
		case argv[0] == "service" && posted:
			return agent.Result{Stdout: []byte("stopped\n")}, true
		}
		return agent.Result{}, false
	}
	got := listing(t, m, sqlHost, in)[sesFiles]
	back, _ := m.PlanChangeDir(ctx, h, in, got, home+"/git/demo")
	n := len(f.commands())
	err := m.ChangeDir(ctx, h, in, got, back)
	if err == nil || !strings.Contains(err.Error(), "stopped before it made it") {
		t.Fatalf("the service stopped: %v", err)
	}
	after := f.commands()[n:]
	for i, c := range after {
		if strings.HasPrefix(c, "api POST") {
			for _, later := range after[i+1:] {
				if strings.HasPrefix(later, "api") {
					t.Fatalf("an API call after the service stopped: %q", after)
				}
			}
		}
	}
}
