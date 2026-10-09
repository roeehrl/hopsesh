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
	t.Cleanup(func() { _ = a.Catalog.Close() })
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
