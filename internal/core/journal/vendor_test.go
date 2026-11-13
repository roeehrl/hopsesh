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
}

func newFakeSessions() *fakeSessions {
	return &fakeSessions{form: "v1", sessions: map[agent.SessionID]string{}, backups: map[string]string{}}
}

func (f *fakeSessions) stamp(id agent.SessionID) agent.SessionStamp {
	return agent.SessionStamp(fmt.Sprintf("%s:%d", f.form, len(f.sessions[id])))
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
	want := f.backups[backup]
	if have, ok := f.sessions[key.Session]; ok {
		if have != want {
			return "", fmt.Errorf("%w: it changed since its backup", agent.ErrDiverged)
		}
		return f.stamp(key.Session), nil
	}
	f.sessions[key.Session] = want
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

	// Continued in between: the restore refuses rather than report success over it.
	f2 := newFakeSessions()
	f2.backups[backup] = "old"
	f2.sessions["ses_1"] = "someone else's"
	j2, _ := New(t.TempDir(), KindMove, "set aside")
	must(t, j2.DeletedSession("here", vendorKey, backup, "v1:3"))
	if err := j2.Undo(ctx, vendorReach(f2), false); err == nil || !strings.Contains(err.Error(), "restoring the dbagent session ses_1") || j2.Undone {
		t.Fatalf("restore over other work: %v", err)
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
