package app

import (
	"context"
	"errors"
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

func TestRecoverReceiptsPreservesLaterNativeWorkAndUndoGuard(t *testing.T) {
	t.Setenv("HOPSESH_MACHINE", "A")
	a := New(config.Defaults(), all.Registry(), t.TempDir(), nil)
	native := filepath.Join(t.TempDir(), "native.jsonl")
	j, _ := journal.New(a.StateDir, journal.KindMove, "pending ack")
	if err := j.WriteFile(host.LocalFS(), "A", native, []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	m := lineage.New("app-recovery")
	m.Upsert(lineage.Replica{Endpoint: "A", Location: "A", Key: agent.SessionKey{Agent: "claude", Session: "original"}})
	lock := lineage.PathFor(native) + ".receipt-lock"
	os.WriteFile(lock, []byte("busy"), 0600)
	if err := j.WriteReceipt(host.LocalFS(), "A", lineage.PathFor(native), m.Encode(), false); err == nil {
		t.Fatal("fault not triggered")
	}
	if err := j.Seal(func(string) (host.FS, error) { return host.LocalFS(), nil }); err != nil {
		t.Fatal(err)
	}
	os.Remove(lock)
	later := []byte("original plus later work")
	os.WriteFile(native, later, 0600)
	if err := a.RecoverReceipts(context.Background(), j.ID); err != nil {
		t.Fatal(err)
	}
	loaded, err := journal.Load(a.StateDir, j.ID)
	if err != nil || loaded.PendingReceipts() {
		t.Fatal("ack not recovered", err)
	}
	after, _ := os.ReadFile(native)
	if string(after) != string(later) {
		t.Fatal("repair altered native work")
	}
	if err = loaded.Changed(context.Background(), journal.Files(func(string) (host.FS, error) { return host.LocalFS(), nil })); !errors.Is(err, journal.ErrChanged) {
		t.Fatal("repair blessed later work for undo", err)
	}
	loaded.Undone = true
	loaded.Save()
	if err = a.RecoverReceipts(context.Background(), j.ID); err == nil {
		t.Fatal("undone operation was revived")
	}
}

func TestRecoveryCompletesFirstReplicaPlaceholderWithoutRewritingEitherNative(t *testing.T) {
	t.Setenv("HOPSESH_MACHINE", "A")
	a := New(config.Defaults(), all.Registry(), t.TempDir(), nil)
	root := t.TempDir()
	source, target := filepath.Join(root, "source.jsonl"), filepath.Join(root, "target.jsonl")
	for _, path := range []string{source, target} {
		if err := os.WriteFile(path, []byte("later native work"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	empty := lineage.New("first-replica")
	if err := os.WriteFile(lineage.PathFor(source), empty.Encode(), 0600); err != nil {
		t.Fatal(err)
	}
	complete := empty.Clone()
	complete.Upsert(lineage.Replica{Endpoint: "A", Location: "A", Key: agent.SessionKey{Agent: "claude", Session: "source"}})
	complete.Upsert(lineage.Replica{Endpoint: "A", Location: "A", Key: agent.SessionKey{Agent: "codex", Session: "target"}})
	j, err := journal.New(a.StateDir, journal.KindMove, "first source acknowledgment")
	if err != nil {
		t.Fatal(err)
	}
	if err = j.WriteReceipt(host.LocalFS(), "A", lineage.PathFor(target), complete.Encode(), false); err != nil {
		t.Fatal(err)
	}
	lock := lineage.PathFor(source) + ".receipt-lock"
	if err = os.WriteFile(lock, []byte("busy"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = j.WriteReceipt(host.LocalFS(), "A", lineage.PathFor(source), complete.Encode(), false); err == nil {
		t.Fatal("expected pending receipt")
	}
	if err = os.Remove(lock); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err = a.RecoverReceipts(context.Background(), j.ID); err != nil {
			t.Fatal(err)
		}
		for _, path := range []string{source, target} {
			raw, err := os.ReadFile(path)
			if err != nil || string(raw) != "later native work" {
				t.Fatal("native changed", err)
			}
			graph, err := lineage.Read(host.LocalFS(), path)
			if err != nil || len(graph.Replicas) != 2 {
				t.Fatal("receipt not recovered", err)
			}
		}
	}
	disk, err := journal.Load(a.StateDir, j.ID)
	if err != nil || disk.PendingReceipts() {
		t.Fatal("pending receipt", err)
	}
}
