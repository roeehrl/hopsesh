package journal

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
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
	must(t, back.Undo(func(string) (host.FS, error) { return fsys, nil }, false))
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
	must(t, j.Append(fsys, "here", idx, []byte(`{"id":"b"}`+"\n"), agent.AppendOptions{NewLine: true}))
	f, _ := os.OpenFile(idx, os.O_APPEND|os.O_WRONLY, 0)
	f.WriteString(`{"id":"c"}` + "\n")
	f.Close()
	must(t, j.Undo(func(string) (host.FS, error) { return fsys, nil }, false))
	if b, _ := os.ReadFile(idx); string(b) != `{"id":"a"}`+"\n"+`{"id":"c"}`+"\n" {
		t.Fatalf("after undo: %q", b)
	}
	if fi, _ := os.Stat(idx); runtime.GOOS != "windows" && fi.Mode().Perm() != 0o644 {
		t.Fatalf("mode %v", fi.Mode())
	}

	other := filepath.Join(data, "t.jsonl")
	os.WriteFile(other, []byte("x\n"), 0o600)
	j2, _ := New(state, KindMove, "mark")
	must(t, j2.Append(fsys, "here", other, []byte("mark\n"), agent.AppendOptions{}))
	os.WriteFile(other, []byte("x\nMARK\n"), 0o600)
	if err := j2.Undo(func(string) (host.FS, error) { return fsys, nil }, false); err == nil {
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
	if err := j.Changed(here); err != nil {
		t.Fatalf("nothing changed yet: %v", err)
	}
	f, _ := os.OpenFile(placed, os.O_APPEND|os.O_WRONLY, 0)
	f.WriteString("new turn\n")
	f.Close()
	err := j.Undo(here, false)
	if !errors.Is(err, ErrChanged) {
		t.Fatalf("undo must refuse: %v", err)
	}
	if b, _ := os.ReadFile(placed); string(b) != "moved\nnew turn\n" {
		t.Fatal("a refused undo changes nothing")
	}
	must(t, j.Undo(here, true))
	if _, err := os.Stat(placed); !os.IsNotExist(err) {
		t.Fatal("forced undo removes it")
	}
}
