package journal

import (
	"os"
	"path/filepath"
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

	j, err := New(state, "test move")
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
	must(t, back.Undo(func(string) (host.FS, error) { return fsys, nil }))
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

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
