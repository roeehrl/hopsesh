package journal

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/roeehrl/hopsesh/internal/core/host"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

func TestUndoEverything(t *testing.T) {
	state, data := t.TempDir(), t.TempDir()
	fsys := host.LocalFS()
	old := filepath.Join(data, "old.jsonl")
	marked := filepath.Join(data, "marked.jsonl")
	staged := filepath.Join(data, "staged.jsonl")
	os.WriteFile(old, []byte("old\n"), 0o600)
	os.WriteFile(marked, []byte("a\n"), 0o600)
	os.WriteFile(staged, []byte("staged\n"), 0o600)
	past := time.Now().Add(-time.Hour).Truncate(time.Second)
	os.Chtimes(marked, past, past)

	j, err := New(state, KindMove, "test move")
	if err != nil {
		t.Fatal(err)
	}
	created := filepath.Join(data, "deep", "new.jsonl")
	must(t, j.WriteFile(fsys, "here", created, []byte("new\n"), 0o600))
	must(t, j.WriteFile(fsys, "here", old, []byte("replaced\n"), 0o600))
	must(t, j.Append(fsys, "here", marked, []byte("mark\n"), agent.AppendOptions{KeepMtime: true}))
	must(t, j.Place("here", staged, filepath.Join(data, "placed.jsonl"), 0o600))
	j.AddKey(agent.SessionKey{Agent: "claude", Session: "s1"})

	back, err := Load(state, j.ID)
	if err != nil || len(back.Entries) != 4 || back.Keys[0].Session != "s1" {
		t.Fatalf("reload: %+v %v", back, err)
	}
	must(t, back.Undo(ctx, Files(func(string) (host.FS, error) { return fsys, nil }), false))
	if _, err := os.Stat(created); !os.IsNotExist(err) {
		t.Error("created file must be removed")
	}
	if b, _ := os.ReadFile(old); string(b) != "old\n" {
		t.Errorf("replaced file: %q", b)
	}
	if b, _ := os.ReadFile(marked); string(b) != "a\n" {
		t.Errorf("appended file: %q", b)
	}
	if fi, _ := os.Stat(marked); !fi.ModTime().Equal(past) {
		t.Errorf("mtime %v, want %v", fi.ModTime(), past)
	}
	if _, err := os.Stat(filepath.Join(data, "placed.jsonl")); !os.IsNotExist(err) {
		t.Error("placed file must be removed")
	}
	list, _ := List(state)
	if len(list) != 1 || !list[0].Undone {
		t.Fatalf("list: %+v", list)
	}
}

// Undoing an append takes out only hopsesh's bytes: lines the agent wrote afterwards stay,
// and bytes that changed are left alone.
func TestUndoAppendKeepsLaterWrites(t *testing.T) {
	state, data := t.TempDir(), t.TempDir()
	fsys := host.LocalFS()
	idx := filepath.Join(data, "session_index.jsonl")
	os.WriteFile(idx, []byte(`{"id":"a"}`), 0o644) // no trailing newline: Append adds one
	j, _ := New(state, KindMove, "title")
	must(t, j.Append(fsys, "here", idx, []byte(`{"id":"b"}`+"\n"), agent.AppendOptions{NewLine: true, Standalone: true}))
	f, _ := os.OpenFile(idx, os.O_APPEND|os.O_WRONLY, 0)
	f.WriteString(`{"id":"c"}` + "\n")
	f.Close()
	must(t, j.Undo(ctx, Files(func(string) (host.FS, error) { return fsys, nil }), false))
	if b, _ := os.ReadFile(idx); string(b) != `{"id":"a"}`+"\n"+`{"id":"c"}`+"\n" {
		t.Fatalf("after undo: %q", b)
	}
	if fi, _ := os.Stat(idx); runtime.GOOS != "windows" && fi.Mode().Perm() != 0o644 {
		t.Fatalf("mode %v", fi.Mode())
	}

	other := filepath.Join(data, "t.jsonl")
	os.WriteFile(other, []byte("x\n"), 0o600)
	j2, _ := New(state, KindMove, "mark")
	must(t, j2.Append(fsys, "here", other, []byte("mark\n"), agent.AppendOptions{Standalone: true}))
	os.WriteFile(other, []byte("x\nMARK\n"), 0o600)
	if err := j2.Undo(ctx, Files(func(string) (host.FS, error) { return fsys, nil }), false); err == nil {
		t.Fatal("changed bytes must not be cut out")
	}
	if b, _ := os.ReadFile(other); string(b) != "x\nMARK\n" {
		t.Fatalf("a refused undo changes nothing: %q", b)
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// A file used after the operation is not undone (that work would be lost), unless forced.
func TestUndoRefusesWhatWasUsedSince(t *testing.T) {
	state, data := t.TempDir(), t.TempDir()
	fsys := host.LocalFS()
	j, _ := New(state, KindMove, "move")
	placed := filepath.Join(data, "s.jsonl")
	must(t, j.WriteFile(fsys, "here", placed, []byte("moved\n"), 0o600))
	here := func(string) (host.FS, error) { return fsys, nil }
	must(t, j.Seal(here))
	if err := j.Changed(ctx, Files(here)); err != nil {
		t.Fatalf("nothing changed yet: %v", err)
	}
	f, _ := os.OpenFile(placed, os.O_APPEND|os.O_WRONLY, 0)
	f.WriteString("new turn\n")
	f.Close()
	err := j.Undo(ctx, Files(here), false)
	if !errors.Is(err, ErrChanged) {
		t.Fatalf("undo must refuse: %v", err)
	}
	if b, _ := os.ReadFile(placed); string(b) != "moved\nnew turn\n" {
		t.Fatal("a refused undo changes nothing")
	}
	must(t, j.Undo(ctx, Files(here), true))
	if _, err := os.Stat(placed); !os.IsNotExist(err) {
		t.Fatal("forced undo removes it")
	}
}

// An append that creates a shared file (an agent's index) is undone by taking out its
// own line: later lines from other operations stay, and the file goes only when nothing
// else is in it. Neither counts as the operation's session being used since.
func TestUndoAppendThatCreatedTheFile(t *testing.T) {
	state, data := t.TempDir(), t.TempDir()
	fsys := host.LocalFS()
	fsFor := func(string) (host.FS, error) { return fsys, nil }
	index := filepath.Join(data, "codex", "session_index.jsonl")

	first, _ := New(state, KindContinue, "first")
	must(t, first.Append(fsys, "here", index, []byte(`{"id":"a"}`+"\n"), agent.AppendOptions{NewLine: true, Standalone: true}))
	must(t, first.Seal(fsFor))
	second, _ := New(state, KindContinue, "second")
	must(t, second.Append(fsys, "here", index, []byte(`{"id":"b"}`+"\n"), agent.AppendOptions{NewLine: true, Standalone: true}))
	must(t, second.Seal(fsFor))

	if err := first.Changed(ctx, Files(fsFor)); err != nil {
		t.Fatalf("another operation's line in a shared file is not a later use: %v", err)
	}
	must(t, first.Undo(ctx, Files(fsFor), false))
	if b, _ := os.ReadFile(index); string(b) != `{"id":"b"}`+"\n" {
		t.Fatalf("undoing the first keeps the second's line: %q", b)
	}
	must(t, second.Undo(ctx, Files(fsFor), false))
	if b, err := os.ReadFile(index); err == nil && len(b) > 0 {
		t.Fatalf("both lines are taken out: %q", b)
	}

	// Undone in the order they were made, the creating append removes the file.
	third, _ := New(state, KindContinue, "third")
	other := filepath.Join(data, "other", "index.jsonl")
	must(t, third.Append(fsys, "here", other, []byte("x\n"), agent.AppendOptions{NewLine: true}))
	must(t, third.Undo(ctx, Files(fsFor), false))
	if _, err := os.Stat(other); !os.IsNotExist(err) {
		t.Fatal("a file an append created goes when its undo leaves it empty")
	}
}

// New turns appended to a session build on what came before: once the agent wrote more
// after them, undo refuses (that work would be lost), and a forced undo cuts the file
// where hopsesh's turns began.
func TestUndoSessionAppendUsedSince(t *testing.T) {
	state, data := t.TempDir(), t.TempDir()
	fsys := host.LocalFS()
	here := func(string) (host.FS, error) { return fsys, nil }
	s := filepath.Join(data, "s.jsonl")
	os.WriteFile(s, []byte("turn 1\n"), 0o600)
	j, _ := New(state, KindContinue, "back")
	must(t, j.Append(fsys, "here", s, []byte("turn 2 (other agent)\n"), agent.AppendOptions{NewLine: true}))
	must(t, j.Seal(here))
	if err := j.Changed(ctx, Files(here)); err != nil {
		t.Fatalf("nothing follows yet: %v", err)
	}
	f, _ := os.OpenFile(s, os.O_APPEND|os.O_WRONLY, 0)
	f.WriteString("turn 3 (builds on turn 2)\n")
	f.Close()
	if err := j.Undo(ctx, Files(here), false); !errors.Is(err, ErrChanged) {
		t.Fatalf("undo must refuse: %v", err)
	}
	if b, _ := os.ReadFile(s); string(b) != "turn 1\nturn 2 (other agent)\nturn 3 (builds on turn 2)\n" {
		t.Fatalf("a refused undo changes nothing: %q", b)
	}
	must(t, j.Undo(ctx, Files(here), true))
	if b, _ := os.ReadFile(s); string(b) != "turn 1\n" {
		t.Fatalf("forced undo: %q", b)
	}
}

var ctx = context.Background()

// fakeRefs is a remote's refs, as undo sees them.
type fakeRefs struct {
	refs    map[string]string
	deleted []string
}

func (f *fakeRefs) RemoteRef(_ context.Context, machine, dir, remote, ref string) (string, error) {
	return f.refs[ref], nil
}

func (f *fakeRefs) DeleteRef(_ context.Context, machine, dir, remote, ref, expect string) error {
	if f.refs[ref] != expect {
		return errors.New("stale lease")
	}
	delete(f.refs, ref)
	f.deleted = append(f.deleted, ref)
	return nil
}

func (f *fakeRefs) RestoreRef(_ context.Context, machine, dir, remote, ref, sha string) error {
	if f.refs[ref] != "" {
		return errors.New("already exists")
	}
	f.refs[ref] = sha
	return nil
}

// fakeClouds is a cloud that can archive (or not), with its sessions' last activity.
type fakeClouds struct {
	updated  map[agent.SessionID]time.Time
	canArch  bool
	archived []agent.SessionID
}

func (f *fakeClouds) Updated(_ context.Context, cloud string, key agent.SessionKey) (time.Time, error) {
	return f.updated[key.Session], nil
}

func (f *fakeClouds) Archive(_ context.Context, cloud string, key agent.SessionKey) error {
	if !f.canArch {
		return ErrManual
	}
	f.archived = append(f.archived, key.Session)
	return nil
}

// A handoff's journal: the pushed branch is deleted with a lease, a cloud that cannot
// archive leaves a step for the user, and a session file the vendor's CLI wrote is set
// aside.
func TestUndoHandoff(t *testing.T) {
	state, data := t.TempDir(), t.TempDir()
	fsys := host.LocalFS()
	here := Files(func(string) (host.FS, error) { return fsys, nil })
	t0 := time.Date(2026, 10, 4, 9, 0, 0, 0, time.UTC)
	const ref = "refs/heads/hopsesh/handoff/20261004-aaaaaaaa"
	teleported := filepath.Join(data, "projects", "p", "t1.jsonl")
	must(t, os.MkdirAll(filepath.Dir(teleported), 0o700))
	must(t, os.WriteFile(teleported, []byte(`{"type":"teleported-from"}`+"\n"), 0o600))

	j, err := New(state, KindHandoff, "hand off")
	if err != nil {
		t.Fatal(err)
	}
	must(t, j.PushRef("here", data, "origin", ref, "1111111"))
	key := agent.SessionKey{Agent: "codex", Session: "task_e_1"}
	must(t, j.Cloud("here", agent.CloudSession{Key: key, Cloud: "codex-cloud", URL: "https://chatgpt.com/codex/tasks/task_e_1", Updated: t0}))
	must(t, j.Adopt(fsys, "here", teleported))

	refs := &fakeRefs{refs: map[string]string{ref: "1111111"}}
	clouds := &fakeClouds{updated: map[agent.SessionID]time.Time{"task_e_1": t0}}
	r := Reach{FS: here.FS, Refs: refs, Clouds: clouds}

	// The cloud pushed past the snapshot: refused without force.
	refs.refs[ref] = "2222222"
	if err := j.Changed(ctx, r); !errors.Is(err, ErrChanged) || !strings.Contains(err.Error(), "moved on") {
		t.Fatalf("a branch the cloud pushed to must count as changed: %v", err)
	}
	refs.refs[ref] = "1111111"
	// The cloud session has new activity: refused without force.
	clouds.updated["task_e_1"] = t0.Add(time.Minute)
	if err := j.Undo(ctx, r, false); !errors.Is(err, ErrChanged) || !strings.Contains(err.Error(), "new activity") {
		t.Fatalf("a cloud session used since must count as changed: %v", err)
	}
	clouds.updated["task_e_1"] = t0
	// The adopted file grew: refused without force.
	must(t, os.WriteFile(teleported, []byte(`{"type":"teleported-from"}`+"\n"+`{"type":"user"}`+"\n"), 0o600))
	if err := j.Changed(ctx, r); !errors.Is(err, ErrChanged) || !strings.Contains(err.Error(), "were added after this") {
		t.Fatalf("an adopted file that grew must count as changed: %v", err)
	}
	must(t, os.WriteFile(teleported, []byte(`{"type":"teleported-from"}`+"\n"), 0o600))

	must(t, j.Undo(ctx, r, false))
	if len(refs.deleted) != 1 || len(refs.refs) != 0 {
		t.Fatalf("the branch must be deleted: %+v", refs)
	}
	if _, err := os.Stat(teleported); !os.IsNotExist(err) {
		t.Fatal("the adopted file must be set aside")
	}
	back, err := Load(state, j.ID)
	if err != nil || !back.Undone || len(back.Manual) != 1 || back.Manual[0].Key != key || back.Manual[0].URL == "" {
		t.Fatalf("a cloud that cannot archive leaves a step for the user: %+v %v", back, err)
	}
	set, _ := filepath.Glob(filepath.Join(Dir(state), j.ID, "backup", "adopted-*"))
	if len(set) != 1 {
		t.Fatalf("the adopted file is kept in the journal: %v", set)
	}
}

// Forced, undo deletes the branch where it points now and archives a cloud session that
// was used since, where the cloud can.
func TestUndoHandoffForced(t *testing.T) {
	state := t.TempDir()
	here := Files(func(string) (host.FS, error) { return host.LocalFS(), nil })
	const ref = "refs/heads/hopsesh/handoff/x"
	j, err := New(state, KindHandoff, "hand off")
	if err != nil {
		t.Fatal(err)
	}
	must(t, j.PushRef("here", "/repo", "origin", ref, "1111111"))
	must(t, j.Cloud("here", agent.CloudSession{Key: agent.SessionKey{Agent: "fake", Session: "s1"}, Cloud: "fake-cloud"}))
	refs := &fakeRefs{refs: map[string]string{ref: "3333333"}}
	clouds := &fakeClouds{updated: map[agent.SessionID]time.Time{"s1": time.Now()}, canArch: true}
	r := Reach{FS: here.FS, Refs: refs, Clouds: clouds}
	if err := j.Undo(ctx, r, false); !errors.Is(err, ErrChanged) {
		t.Fatalf("want a refusal, got %v", err)
	}
	must(t, j.Undo(ctx, r, true))
	if len(refs.refs) != 0 || len(clouds.archived) != 1 || len(j.Manual) != 0 {
		t.Fatalf("forced undo: refs %v, archived %v, manual %v", refs.refs, clouds.archived, j.Manual)
	}
	// Without a way to reach the remote, the branch is reported, not silently kept.
	j2, _ := New(state, KindHandoff, "again")
	must(t, j2.PushRef("here", "/repo", "origin", ref, "1111111"))
	if err := j2.Undo(ctx, here, false); err == nil || !strings.Contains(err.Error(), "remote cannot be reached") {
		t.Fatalf("an unreachable remote must be reported: %v", err)
	}
}

// A branch the user chose to keep stays on its remote through undo, and is reported; a
// branch a clean-up deleted comes back on undo, unless the remote has one of that name
// again.
func TestUndoKeepsAndRestoresBranches(t *testing.T) {
	state := t.TempDir()
	here := Files(func(string) (host.FS, error) { return host.LocalFS(), nil })
	const kept, gone = "refs/heads/hopsesh/handoff/kept", "refs/heads/claude/web-session-x"
	j, err := New(state, KindHandoff, "hand off, keep the branch")
	if err != nil {
		t.Fatal(err)
	}
	must(t, j.KeepPushed("here", "/repo", "origin", kept, "1111111"))
	refs := &fakeRefs{refs: map[string]string{kept: "2222222"}}
	r := Reach{FS: here.FS, Refs: refs}
	// Moved on since, but kept: nothing to refuse, nothing deleted.
	must(t, j.Undo(ctx, r, false))
	if refs.refs[kept] != "2222222" || len(refs.deleted) != 0 || strings.Join(j.Kept, ",") != "origin hopsesh/handoff/kept" {
		t.Fatalf("a kept branch: %+v %v", refs, j.Kept)
	}

	c, err := New(state, KindCleanup, "clean up")
	if err != nil {
		t.Fatal(err)
	}
	must(t, c.DeletedRef("here", "/repo", "origin", gone, "3333333"))
	refs.refs[gone] = "4444444" // someone pushed a branch of that name since
	if err := c.Undo(ctx, r, false); !errors.Is(err, ErrChanged) || !strings.Contains(err.Error(), "is there again") {
		t.Fatalf("a deleted branch that is back: %v", err)
	}
	delete(refs.refs, gone)
	must(t, c.Undo(ctx, r, false))
	if refs.refs[gone] != "3333333" {
		t.Fatalf("undo must push the branch back: %+v", refs.refs)
	}
}

// A composite operation names its legs, and each leg its operation.
func TestParts(t *testing.T) {
	state := t.TempDir()
	hop, _ := New(state, KindHop, "hop")
	leg, _ := New(state, KindFetch, "leg")
	if hop.ID == leg.ID || leg.ID < hop.ID {
		t.Fatalf("two journals at once: %s then %s", hop.ID, leg.ID)
	}
	for i := 0; i < 20; i++ {
		if _, err := New(state, KindMove, "many"); err != nil {
			t.Fatal(err)
		}
	}
	if js, _ := List(state); len(js) != 22 {
		t.Fatalf("every journal is kept: %d", len(js))
	}
	must(t, hop.AddPart(leg.ID))
	must(t, leg.SetPartOf(hop.ID))
	h, _ := Load(state, hop.ID)
	l, _ := Load(state, leg.ID)
	if strings.Join(h.Parts, ",") != leg.ID || l.PartOf != hop.ID {
		t.Fatalf("parts: %+v %+v", h, l)
	}
}
