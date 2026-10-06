package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/roeehrl/hopsesh/internal/agents/all"
	"github.com/roeehrl/hopsesh/internal/config"
	"github.com/roeehrl/hopsesh/internal/core/host"
	"github.com/roeehrl/hopsesh/internal/core/journal"
	"github.com/roeehrl/hopsesh/internal/core/lineage"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

func TestArchiveUnsupportedLineagePreservesNativeAndUndo(t *testing.T) {
	home := t.TempDir()
	native := filepath.Join(home, "session.jsonl")
	before := []byte("signed native bytes\n")
	old := []byte(`{"format":"lineage/2","logical":"legacy"}`)
	if err := os.WriteFile(native, before, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(lineage.PathFor(native), old, 0600); err != nil {
		t.Fatal(err)
	}
	a := New(config.Config{}, all.Registry(), t.TempDir(), nil)
	inv := &Inventory{Machines: []*Machine{{Name: "here", Local: true, host: &host.Machine{Name: "here", Local: true, Facts: host.Facts{Home: home}}}}}
	e := Entry{Machine: "here", Session: agent.Summary{Key: agent.SessionKey{Agent: "claude", Session: "archive-test"}, Path: native}}
	j, err := a.ArchiveLineage(context.Background(), inv, e)
	if err != nil {
		t.Fatal(err)
	}
	m, err := lineage.Read(host.LocalFS(), native)
	if err != nil || m == nil || len(m.Revisions) != 0 {
		t.Fatalf("reset is not an independent empty graph %v", err)
	}
	archived, err := os.ReadFile(lineage.PathFor(native) + ".archived-" + j.ID)
	if err != nil || string(archived) != string(old) {
		t.Fatal("archive discarded unsupported data")
	}
	if _, err = a.ArchiveLineage(context.Background(), inv, e); err == nil {
		t.Fatal("supported graph must not be archived")
	}
	loaded, err := journal.Load(a.StateDir, j.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = loaded.Undo(context.Background(), journal.Reach{FS: func(string) (host.FS, error) { return host.LocalFS(), nil }}, false); err != nil {
		t.Fatal(err)
	}
	restored, _ := os.ReadFile(lineage.PathFor(native))
	after, _ := os.ReadFile(native)
	if string(restored) != string(old) || string(after) != string(before) {
		t.Fatal("archive/undo changed native bytes or lost original metadata")
	}
}
