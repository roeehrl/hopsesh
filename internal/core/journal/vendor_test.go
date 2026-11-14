package journal

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/roeehrl/hopsesh/internal/core/host"
	"github.com/roeehrl/hopsesh/internal/core/lineage"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

// fakeSessions is an agent's database of sessions, reached the way undo reaches one: a
// session is its content, and its stamp is the content's length in the module's form.
type fakeSessions struct {
	form     string
	sessions map[agent.SessionID]string
	backups  map[string]string
	deletes  []agent.SessionStamp
	dirs     []string
	restores int
	// failRestore makes every Restore fail (the import fails, the database is locked).
	failRestore error
	// partialRestore makes the next Restore put the session back and then fail (a child's
	// import failed); restamp gives a session put back a new stamp, as an import does.
	partialRestore error
	restamp        bool
	imported       map[agent.SessionID]int
}

func newFakeSessions() *fakeSessions {
	return &fakeSessions{form: "v1", sessions: map[agent.SessionID]string{}, backups: map[string]string{}}
}

func (f *fakeSessions) stamp(id agent.SessionID) agent.SessionStamp {
	st := fmt.Sprintf("%s:%d", f.form, len(f.sessions[id]))
	if n := f.imported[id]; n > 0 {
		st += fmt.Sprintf("@%d", n)
	}
	return agent.SessionStamp(st)
}

func (f *fakeSessions) Stamp(_ context.Context, _ string, key agent.SessionKey) (agent.SessionStamp, error) {
	if _, ok := f.sessions[key.Session]; !ok {
		return "", agent.ErrNotFound
	}
	return f.stamp(key.Session), nil
}

func (f *fakeSessions) Delete(_ context.Context, _ string, key agent.SessionKey, expect agent.SessionStamp) error {
	if _, ok := f.sessions[key.Session]; !ok {
		return agent.ErrNotFound
	}
	if f.stamp(key.Session) != expect {
		return agent.ErrDiverged
	}
	f.deletes = append(f.deletes, expect)
	delete(f.sessions, key.Session)
	return nil
}

func (f *fakeSessions) Restore(_ context.Context, _ string, key agent.SessionKey, backup string) (agent.SessionStamp, error) {
	f.restores++
	if f.failRestore != nil {
		return "", f.failRestore
	}
	want := f.backups[backup]
	if have, ok := f.sessions[key.Session]; ok {
		if have != want {
			return "", fmt.Errorf("%w: it changed since its backup", agent.ErrDiverged)
		}
		return f.stamp(key.Session), nil
	}
	f.sessions[key.Session] = want
	if f.restamp {
		if f.imported == nil {
			f.imported = map[agent.SessionID]int{}
		}
		f.imported[key.Session]++
	}
	if err := f.partialRestore; err != nil {
		f.partialRestore = nil
		return "", err
	}
	return f.stamp(key.Session), nil
}

func (f *fakeSessions) ChangeDir(_ context.Context, _ string, _ agent.SessionKey, dir string) error {
	f.dirs = append(f.dirs, dir)
	return nil
}

func vendorReach(f *fakeSessions) Reach {
	return Reach{FS: func(string) (host.FS, error) { return host.LocalFS(), nil }, Sessions: f, Dirs: f}
}

var vendorKey = agent.SessionKey{Agent: "dbagent", Session: "ses_1"}

// A session the operation imported is deleted on undo at the stamp Seal recorded, and undo
// refuses once it was continued: deleting it would lose that work.
func TestUndoVendorSessionCreated(t *testing.T) {
	ctx := context.Background()
	for _, used := range []bool{false, true} {
		t.Run(map[bool]string{false: "unused", true: "continued"}[used], func(t *testing.T) {
			f := newFakeSessions()
			j, err := New(t.TempDir(), KindMove, "import")
			if err != nil {
				t.Fatal(err)
			}
			must(t, j.RecordCreatedSession("here", vendorKey))
			must(t, j.RecordCreatedSession("here", vendorKey)) // a recovery records it again
			f.sessions["ses_1"] = "ab"
			must(t, j.SealWith(ctx, vendorReach(f)))
			back, err := Load(filepath.Dir(filepath.Dir(j.dir)), j.ID)
			if err != nil || len(back.Entries) != 1 || back.Entries[0].Stamp != "v1:2" {
				t.Fatalf("the journal on disk: %+v %v", back, err)
			}
			if !used {
				must(t, j.Undo(ctx, vendorReach(f), false))
				if _, ok := f.sessions["ses_1"]; ok || len(f.deletes) != 1 || f.deletes[0] != "v1:2" {
					t.Fatalf("not deleted at its stamp: %v", f.deletes)
				}
				return
			}
			f.sessions["ses_1"] = "abc"
			err = j.Undo(ctx, vendorReach(f), false)
			if !errors.Is(err, ErrChanged) || !strings.Contains(err.Error(), "continued after this") {
				t.Fatalf("undo over later work: %v", err)
			}
			if _, ok := f.sessions["ses_1"]; !ok || j.Undone {
				t.Fatal("the continued session was deleted")
			}
			must(t, j.Undo(ctx, vendorReach(f), true))
			if _, ok := f.sessions["ses_1"]; ok || f.deletes[0] != "v1:3" {
				t.Fatal("a forced undo deletes it as it is now")
			}
		})
	}
}

// A session whose state was never recorded (the operation was cut short) is deleted only
// when the undo is forced; one that never got made is no obstacle.
func TestUndoVendorSessionUnsealed(t *testing.T) {
	ctx := context.Background()
	f := newFakeSessions()
	j, _ := New(t.TempDir(), KindMove, "cut short")
	must(t, j.RecordCreatedSession("here", vendorKey))
	f.sessions["ses_1"] = "ab"
	if err := j.Changed(ctx, vendorReach(f)); !errors.Is(err, ErrChanged) || !strings.Contains(err.Error(), "did not record") {
		t.Fatalf("an unrecorded session: %v", err)
	}
	must(t, j.Undo(ctx, vendorReach(f), true))
	if _, ok := f.sessions["ses_1"]; ok {
		t.Fatal("a forced undo left it")
	}

	never, _ := New(t.TempDir(), KindMove, "import failed")
	must(t, never.RecordCreatedSession("here", vendorKey))
	must(t, never.Undo(ctx, vendorReach(f), false))
	if !never.Undone {
		t.Fatal("a session that was never made blocks undo")
	}
}

// A module that stamps sessions another way now cannot say whether one was used: undo asks
// (refuses unless forced) instead of failing for good.
func TestUndoVendorSessionStampForm(t *testing.T) {
	ctx := context.Background()
	f := newFakeSessions()
	j, _ := New(t.TempDir(), KindMove, "import")
	must(t, j.RecordCreatedSession("here", vendorKey))
	f.sessions["ses_1"] = "ab"
	must(t, j.SealWith(ctx, vendorReach(f)))
	f.form = "v2"
	err := j.Undo(ctx, vendorReach(f), false)
	if !errors.Is(err, ErrChanged) || !strings.Contains(err.Error(), "cannot tell") {
		t.Fatalf("another stamp form: %v", err)
	}
	must(t, j.Undo(ctx, vendorReach(f), true))
}

// A session set aside (backed up, then deleted) and replaced by an import comes back on
// undo: the import is deleted first, then the backup imported again.
func TestUndoVendorSessionReplaced(t *testing.T) {
	ctx := context.Background()
	f := newFakeSessions()
	data := t.TempDir()
	backup := filepath.Join(data, "backup", "ses_1.jsonl")
	f.sessions["ses_1"] = "old"
	j, _ := New(t.TempDir(), KindMove, "re-sync")
	// The module's Backup writes its file through the journal.
	must(t, j.WriteFile(host.LocalFS(), "here", backup, []byte("old"), 0o600))
	f.backups[backup] = "old"
	must(t, j.DeletedSession("here", vendorKey, backup, "v1:3"))
	delete(f.sessions, "ses_1")
	must(t, j.RecordCreatedSession("here", vendorKey))
	f.sessions["ses_1"] = "newer"
	must(t, j.SealWith(ctx, vendorReach(f)))
	must(t, j.Undo(ctx, vendorReach(f), false))
	if f.sessions["ses_1"] != "old" {
		t.Fatalf("restored %q", f.sessions["ses_1"])
	}
	if _, err := os.Stat(backup); !os.IsNotExist(err) {
		t.Fatal("the backup file outlived the undo")
	}
	if err := j.DeletedSession("here", vendorKey, "", "v1:3"); err == nil {
		t.Fatal("a delete without a backup was recorded")
	}

	// There with other work (made again since, or never deleted and then used): undo
	// refuses rather than report success over it; forced, it leaves it and its backup (the
	// restore it asks for, which tells other work from a restore that did not finish,
	// refuses it).
	f2 := newFakeSessions()
	f2.backups[backup] = "old"
	f2.sessions["ses_1"] = "someone else's"
	j2, _ := New(t.TempDir(), KindMove, "set aside")
	must(t, j2.WriteFile(host.LocalFS(), "here", backup, []byte("old"), 0o600))
	must(t, j2.DeletedSession("here", vendorKey, backup, "v1:3"))
	if err := j2.Undo(ctx, vendorReach(f2), false); !errors.Is(err, ErrChanged) || !strings.Contains(err.Error(), "holds other work") || j2.Undone {
		t.Fatalf("restore over other work: %v", err)
	}
	must(t, j2.Undo(ctx, vendorReach(f2), true))
	if f2.sessions["ses_1"] != "someone else's" || f2.restores != 1 || len(j2.Left) != 1 || !strings.Contains(j2.Left[0], backup) {
		t.Fatalf("a forced undo over other work: %q, %d restores, left %v", f2.sessions["ses_1"], f2.restores, j2.Left)
	}
	if _, err := os.Stat(backup); err != nil {
		t.Fatal("the backup of a session not put back was deleted")
	}
}

// A restore that fails (the import fails, the database is locked) keeps the backup: it is
// the only copy of the session set aside. Forced or not, undo is then incomplete, and a
// retry once the agent can import puts the session back.
func TestUndoKeepsBackupWhenRestoreFails(t *testing.T) {
	ctx := context.Background()
	for _, force := range []bool{false, true} {
		t.Run(map[bool]string{false: "plain", true: "forced"}[force], func(t *testing.T) {
			f := newFakeSessions()
			backup := filepath.Join(t.TempDir(), "backup", "ses_1.jsonl")
			f.sessions["ses_1"] = "old"
			j, _ := New(t.TempDir(), KindMove, "replace")
			must(t, j.WriteFile(host.LocalFS(), "here", backup, []byte("old"), 0o600))
			f.backups[backup] = "old"
			must(t, j.DeletedSession("here", vendorKey, backup, "v1:3"))
			delete(f.sessions, "ses_1")
			must(t, j.RecordCreatedSession("here", vendorKey))
			f.sessions["ses_1"] = "newer"
			must(t, j.SealWith(ctx, vendorReach(f)))
			f.failRestore = errors.New("the database is locked")
			err := j.Undo(ctx, vendorReach(f), force)
			if err == nil || j.Undone || !strings.Contains(err.Error(), "its backup stays at "+backup) {
				t.Fatalf("a failed restore: %v (undone %v)", err, j.Undone)
			}
			if _, err := os.Stat(backup); err != nil {
				t.Fatal("the backup of a session not put back was deleted")
			}
			if _, ok := f.sessions["ses_1"]; ok {
				t.Fatal("the imported copy is still there")
			}
			f.failRestore = nil
			must(t, j.Undo(ctx, vendorReach(f), force))
			if f.sessions["ses_1"] != "old" || !j.Undone {
				t.Fatalf("the retry: %q", f.sessions["ses_1"])
			}
			if _, err := os.Stat(backup); !os.IsNotExist(err) {
				t.Fatal("the backup outlived the session put back")
			}
		})
	}
}

// A backup kept in several files (the session's, and its children's beside it, named
// after it) is kept whole while the session is not put back, and goes whole once it is.
func TestUndoKeepsChildBackupsWhenRestoreFails(t *testing.T) {
	ctx := context.Background()
	f := newFakeSessions()
	dir := t.TempDir()
	backup := filepath.Join(dir, "backup", "ses_1-1.json")
	child := filepath.Join(dir, "backup", "ses_1-1.children", "001-ses_kid.json")
	other := filepath.Join(dir, "backup", "ses_1-10.json") // another backup's
	f.sessions["ses_1"] = "old"
	j, _ := New(t.TempDir(), KindMove, "replace")
	must(t, j.WriteFile(host.LocalFS(), "here", backup, []byte("old"), 0o600))
	must(t, j.WriteFile(host.LocalFS(), "here", child, []byte("kid"), 0o600))
	must(t, j.WriteFile(host.LocalFS(), "here", other, []byte("x"), 0o600))
	f.backups[backup] = "old"
	must(t, j.DeletedSession("here", vendorKey, backup, "v1:3"))
	delete(f.sessions, "ses_1")
	must(t, j.SealWith(ctx, vendorReach(f)))
	f.failRestore = errors.New("the folder is gone")
	if err := j.Undo(ctx, vendorReach(f), false); err == nil {
		t.Fatal("a failed restore undid")
	}
	for _, p := range []string{backup, child} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("%s: %v", p, err)
		}
	}
	if _, err := os.Stat(other); !os.IsNotExist(err) {
		t.Fatal("a file named like the backup but not part of it was kept")
	}
	f.failRestore = nil
	must(t, j.Undo(ctx, vendorReach(f), false))
	for _, p := range []string{backup, child} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Fatalf("%s outlived the session put back", p)
		}
	}
}

// A restore that stopped halfway (the session is back, a child is not) is finished by the
// next undo, unforced, though the import gave the session a new stamp: the restore tells
// it from other work. When the operation had made a session at the same id, which the
// first undo deleted, the next undo does not take the one put back for it.
func TestUndoFinishesPartialRestore(t *testing.T) {
	ctx := context.Background()
	for _, replaced := range []bool{false, true} {
		t.Run(map[bool]string{false: "set aside", true: "replaced"}[replaced], func(t *testing.T) {
			f := newFakeSessions()
			f.restamp = true
			backup := filepath.Join(t.TempDir(), "backup", "ses_1-1.json")
			f.sessions["ses_1"] = "old"
			j, _ := New(t.TempDir(), KindMove, "replace")
			must(t, j.WriteFile(host.LocalFS(), "here", backup, []byte("old"), 0o600))
			f.backups[backup] = "old"
			must(t, j.DeletedSession("here", vendorKey, backup, "v1:3"))
			delete(f.sessions, "ses_1")
			if replaced {
				must(t, j.RecordCreatedSession("here", vendorKey))
				f.sessions["ses_1"] = "newer"
			}
			must(t, j.SealWith(ctx, vendorReach(f)))
			f.partialRestore = errors.New("a child's folder is gone")
			if err := j.Undo(ctx, vendorReach(f), false); err == nil || j.Undone {
				t.Fatalf("a partial restore: %v", err)
			}
			if _, err := os.Stat(backup); err != nil {
				t.Fatal("the backup of a restore that did not finish was deleted")
			}
			restores := f.restores
			must(t, j.Undo(ctx, vendorReach(f), false))
			if f.sessions["ses_1"] != "old" || f.restores != restores+1 || !j.Undone || len(f.deletes) != map[bool]int{false: 0, true: 1}[replaced] {
				t.Fatalf("the retry: %q, %d restores, %d deletes", f.sessions["ses_1"], f.restores, len(f.deletes))
			}
			if _, err := os.Stat(backup); !os.IsNotExist(err) {
				t.Fatal("the backup outlived the session put back")
			}
		})
	}
	// The session the operation made at the id is still there (its delete failed): the
	// one set aside is not put back over it, and its backup stays.
	f := newFakeSessions()
	backup := filepath.Join(t.TempDir(), "ses_1.json")
	f.sessions["ses_1"] = "old"
	j, _ := New(t.TempDir(), KindMove, "replace")
	must(t, j.WriteFile(host.LocalFS(), "here", backup, []byte("old"), 0o600))
	f.backups[backup] = "old"
	must(t, j.DeletedSession("here", vendorKey, backup, "v1:3"))
	must(t, j.RecordCreatedSession("here", vendorKey))
	f.sessions["ses_1"] = "old" // the same content, as a re-sync of an unchanged copy makes
	must(t, j.SealWith(ctx, vendorReach(f)))
	sessions := &refusingDeletes{f}
	err := j.Undo(ctx, Reach{FS: vendorReach(f).FS, Sessions: sessions}, true)
	if err == nil || !strings.Contains(err.Error(), "once the session this made at its id is deleted") || f.restores != 0 {
		t.Fatalf("restore over the operation's own session: %v, %d restores", err, f.restores)
	}
	if _, err := os.Stat(backup); err != nil {
		t.Fatal("the backup was deleted")
	}
}

// refusingDeletes is fakeSessions whose agent refuses every delete.
type refusingDeletes struct{ *fakeSessions }

func (refusingDeletes) Delete(context.Context, string, agent.SessionKey, agent.SessionStamp) error {
	return errors.New("the database is locked")
}

// A session set aside whose delete never happened (the agent refused it, or the move was
// cut short) is there at the stamp it was backed up at: undo has nothing to put back.
func TestUndoSetAsideNeverDeleted(t *testing.T) {
	ctx := context.Background()
	f := newFakeSessions()
	backup := filepath.Join(t.TempDir(), "ses_1.jsonl")
	f.sessions["ses_1"] = "old"
	f.backups[backup] = "old"
	j, _ := New(t.TempDir(), KindMove, "replace")
	must(t, j.WriteFile(host.LocalFS(), "here", backup, []byte("old"), 0o600))
	must(t, j.DeletedSession("here", vendorKey, backup, "v1:3"))
	must(t, j.Undo(ctx, vendorReach(f), false))
	if f.restores != 0 || f.sessions["ses_1"] != "old" || !j.Undone {
		t.Fatalf("%d restores, %q", f.restores, f.sessions["ses_1"])
	}
}

// A stamp recorded right after the agent made the session is the one undo keeps: a later
// Seal (a recovery's) does not take work done since for the operation's.
func TestSealKeepsRecordedStamp(t *testing.T) {
	ctx := context.Background()
	f := newFakeSessions()
	j, _ := New(t.TempDir(), KindMove, "import")
	must(t, j.RecordCreatedSession("here", vendorKey))
	f.sessions["ses_1"] = "ab"
	must(t, j.StampCreatedSession("here", vendorKey, "v1:2"))
	f.sessions["ses_1"] = "abc" // continued before the recovery sealed
	must(t, j.SealWith(ctx, vendorReach(f)))
	must(t, j.StampCreatedSession("here", vendorKey, "v1:3"))
	if j.Entries[0].Stamp != "v1:2" {
		t.Fatalf("the stamp became %q", j.Entries[0].Stamp)
	}
	if err := j.Undo(ctx, vendorReach(f), false); !errors.Is(err, ErrChanged) {
		t.Fatalf("an unforced undo over the work done since: %v", err)
	}
}

// A session the operation was to create that the agent already had (its import found
// another at the id) is dropped from the journal unless the operation stamped it as its
// own; a set-aside the agent refused is dropped too, so undo leaves both alone.
func TestForgetSession(t *testing.T) {
	j, _ := New(t.TempDir(), KindMove, "import")
	must(t, j.RecordCreatedSession("here", vendorKey))
	must(t, j.ForgetSession("here", vendorKey, true))
	if len(j.Entries) != 0 {
		t.Fatalf("kept %+v", j.Entries)
	}
	must(t, j.RecordCreatedSession("here", vendorKey))
	must(t, j.StampCreatedSession("here", vendorKey, "v1:2"))
	must(t, j.ForgetSession("here", vendorKey, true))
	if len(j.Entries) != 1 {
		t.Fatal("a session the operation made was forgotten")
	}
	must(t, j.DeletedSession("here", vendorKey, "/b", "v1:1"))
	if !j.SetAsideSession("here", vendorKey) {
		t.Fatal("the set-aside is not reported")
	}
	must(t, j.ForgetSession("here", vendorKey, false))
	if len(j.Entries) != 1 || !j.Entries[0].Created || j.SetAsideSession("here", vendorKey) {
		t.Fatalf("the set-aside: %+v", j.Entries)
	}
}

// A folder change of a session kept in a database is guarded by its stamp: its anchor file
// does not change when the session is used.
func TestChangedSessionDirStampGuard(t *testing.T) {
	ctx := context.Background()
	f := newFakeSessions()
	f.sessions["ses_1"] = "ab"
	j, _ := New(t.TempDir(), KindChangeDir, "cd")
	must(t, j.ChangedSessionDir("here", vendorKey, "/nowhere/ses_1.jsonl", "/a", "/b", "v1:2"))
	if err := j.Changed(ctx, vendorReach(f)); err != nil {
		t.Fatal(err)
	}
	f.sessions["ses_1"] = "abc"
	if err := j.Undo(ctx, vendorReach(f), false); !errors.Is(err, ErrChanged) {
		t.Fatalf("undo after the session was used: %v", err)
	}
	f.sessions["ses_1"] = "ab"
	must(t, j.Undo(ctx, vendorReach(f), false))
	if len(f.dirs) != 1 || f.dirs[0] != "/a" {
		t.Fatalf("changed back to %v", f.dirs)
	}
	if err := j.ChangedSessionDir("here", vendorKey, "x", "/a", "/b", ""); err == nil {
		t.Fatal("a change without a stamp was recorded")
	}
	// Journaled before the change at the stamp then, and restamped after it.
	j2, _ := New(t.TempDir(), KindChangeDir, "cd")
	must(t, j2.ChangedSessionDir("here", vendorKey, "/nowhere/ses_1.jsonl", "/a", "/b", "v1:2"))
	f.sessions["ses_1"] = "abcd"
	must(t, j2.RestampSessionDir("here", vendorKey, "v1:4"))
	if err := j2.Changed(ctx, vendorReach(f)); err != nil {
		t.Fatal(err)
	}
	if err := j2.RestampSessionDir("here", agent.SessionKey{Agent: "dbagent", Session: "ses_2"}, "v1:1"); err == nil {
		t.Fatal("restamped a change never recorded")
	}
}

// A receipt beside a session kept in a database is recovered only while its stamp is the
// one the operation left.
func TestSessionReceiptRecovery(t *testing.T) {
	ctx := context.Background()
	data := t.TempDir()
	anchor := filepath.Join(data, "ses_1.jsonl")
	m := lineage.New("family")
	m.Upsert(lineage.Replica{Key: vendorKey, Endpoint: "here", Location: "here"})
	f := newFakeSessions()
	f.sessions["ses_1"] = "ab"
	for _, used := range []bool{false, true} {
		j, _ := New(t.TempDir(), KindMove, "receipt")
		offline := &failingReceiptFS{FS: host.LocalFS(), fail: true}
		if err := j.WriteSessionReceipt(offline, "here", lineage.PathFor(anchor), m.Encode(), vendorKey, "v1:2"); err == nil {
			t.Fatal("fault not triggered")
		}
		if used {
			f.sessions["ses_1"] = "abc"
		}
		err := j.RecoverReceiptsWith(ctx, vendorReach(f))
		if used != errors.Is(err, ErrChanged) || used == (err == nil) {
			t.Fatalf("used %v: %v", used, err)
		}
		if err := j.RecoverReceipts(func(string) (host.FS, error) { return host.LocalFS(), nil }); used && err == nil {
			t.Fatal("recovered without checking the session")
		}
	}
	j, _ := New(t.TempDir(), KindMove, "receipt")
	if err := j.WriteSessionReceipt(host.LocalFS(), "here", lineage.PathFor(anchor), m.Encode(), vendorKey, ""); err == nil {
		t.Fatal("a receipt without a stamp")
	}
}

// Seal names a session it could not stamp (undoing it will need force), and a session
// never made is no failure.
func TestSealReportsUnstampedSessions(t *testing.T) {
	ctx := context.Background()
	f := newFakeSessions()
	j, _ := New(t.TempDir(), KindMove, "import")
	must(t, j.RecordCreatedSession("here", vendorKey))
	must(t, j.SealWith(ctx, vendorReach(f))) // never made
	broken := &erringSessions{fakeSessions: f, err: errors.New("database is busy")}
	f.sessions["ses_1"] = "ab"
	err := j.SealWith(ctx, Reach{FS: vendorReach(f).FS, Sessions: broken})
	if err == nil || !strings.Contains(err.Error(), "undoing it will need force") || j.Entries[0].Stamp != "" {
		t.Fatalf("an unstamped session: %v (%q)", err, j.Entries[0].Stamp)
	}
	if err := j.SealWith(ctx, Reach{FS: vendorReach(f).FS}); err == nil {
		t.Fatal("no Sessions: nothing said")
	}
}

// erringSessions fails every stamp with err.
type erringSessions struct {
	*fakeSessions
	err error
}

func (e *erringSessions) Stamp(context.Context, string, agent.SessionKey) (agent.SessionStamp, error) {
	return "", e.err
}

// A stamp that cannot be read is no proof the session was not used: undo refuses unless
// forced. A machine the check leaves to the undo (ErrOutOfReach) does not count, nor does
// a check with no Sessions at all.
func TestStampCheckOutOfReach(t *testing.T) {
	ctx := context.Background()
	f := newFakeSessions()
	f.sessions["ses_1"] = "ab"
	j, _ := New(t.TempDir(), KindChangeDir, "cd")
	must(t, j.ChangedSessionDir("there", vendorKey, "/nowhere/ses_1.jsonl", "/a", "/b", "v1:2"))
	broken := &erringSessions{fakeSessions: f, err: errors.New("connection refused")}
	if err := j.Changed(ctx, Reach{Sessions: broken}); !errors.Is(err, ErrChanged) || !strings.Contains(err.Error(), "move it back anyway") {
		t.Fatalf("an unreadable stamp: %v", err)
	}
	broken.err = fmt.Errorf("%w: there", ErrOutOfReach)
	if err := j.Changed(ctx, Reach{Sessions: broken}); err != nil {
		t.Fatalf("a machine checked when undoing: %v", err)
	}
	if err := j.Changed(ctx, Reach{}); err != nil {
		t.Fatalf("no Sessions: %v", err)
	}
}

// A hop's first leg made a session its second leg moved on: the first leg's check leaves
// it to the second's, which is undone first.
func TestChangedBesidesVendorSession(t *testing.T) {
	ctx := context.Background()
	f := newFakeSessions()
	first, _ := New(t.TempDir(), KindMove, "import")
	must(t, first.RecordCreatedSession("here", vendorKey))
	f.sessions["ses_1"] = "ab"
	must(t, first.SealWith(ctx, vendorReach(f)))
	second, _ := New(t.TempDir(), KindChangeDir, "cd")
	f.sessions["ses_1"] = "abc"
	must(t, second.ChangedSessionDir("here", vendorKey, "/nowhere/ses_1.jsonl", "/a", "/b", "v1:3"))
	if err := first.Changed(ctx, vendorReach(f)); !errors.Is(err, ErrChanged) {
		t.Fatalf("alone: %v", err)
	}
	if err := first.ChangedBesides(ctx, vendorReach(f), []*Journal{second}); err != nil {
		t.Fatalf("beside the leg that changed it: %v", err)
	}
}

// Without Sessions a session's receipt stays pending; one whose session is gone is dropped
// unwritten (stale), so it no longer holds the others back.
func TestSessionReceiptPendingAndStale(t *testing.T) {
	ctx := context.Background()
	data := t.TempDir()
	anchor := filepath.Join(data, "ses_1.jsonl")
	m := lineage.New("family")
	m.Upsert(lineage.Replica{Key: vendorKey, Endpoint: "here", Location: "here"})
	j, _ := New(t.TempDir(), KindMove, "receipt")
	offline := &failingReceiptFS{FS: host.LocalFS(), fail: true}
	if err := j.WriteSessionReceipt(offline, "here", lineage.PathFor(anchor), m.Encode(), vendorKey, "v1:2"); err == nil {
		t.Fatal("fault not triggered")
	}
	if err := j.RecoverReceiptsWith(ctx, Reach{FS: vendorReach(nil).FS}); err == nil || !j.PendingReceipts() {
		t.Fatalf("recovered without Sessions: %v", err)
	}
	f := newFakeSessions()
	must(t, j.RecoverReceiptsWith(ctx, vendorReach(f)))
	if j.PendingReceipts() || !j.Receipts[0].Stale {
		t.Fatalf("a receipt of a deleted session: %+v", j.Receipts[0])
	}
	if _, err := os.Stat(lineage.PathFor(anchor)); !os.IsNotExist(err) {
		t.Fatal("a stale receipt was written")
	}
}
