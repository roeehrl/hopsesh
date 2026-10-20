package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/roeehrl/hopsesh/internal/config"
	"github.com/roeehrl/hopsesh/internal/core/host"
	"github.com/roeehrl/hopsesh/internal/core/journal"
	"github.com/roeehrl/hopsesh/internal/core/move"
)

func TestRelayPullReceiptRecoveryLinksSourceOnceWithoutReinstallingNative(t *testing.T) {
	t.Setenv("HOPSESH_MACHINE", "destination")
	a := New(config.Defaults(), nil, t.TempDir(), nil)
	a.Cfg.UpsertHost(config.Host{Name: "source", RelayID: "source-peer-12345678", Allowed: true, Via: "relay"})
	native := filepath.Join(t.TempDir(), "native.jsonl")
	j, err := journal.New(a.StateDir, journal.KindMove, "pending source receipt")
	if err != nil {
		t.Fatal(err)
	}
	if err = j.WriteFile(host.LocalFS(), LocalName(), native, []byte("native-history"), 0600); err != nil {
		t.Fatal(err)
	}
	ack := journal.Acknowledgment{Machine: "source", Transport: "relay-pull", Peer: "source-peer-12345678", Operation: "original-op-12345678"}
	if err = j.QueueAcknowledgment(ack); err != nil {
		t.Fatal(err)
	}
	r := relayPullRecord{Peer: ack.Peer, Operation: ack.Operation, Ack: &relayPullAck{}, Result: &move.Result{Journal: j.ID}, SourceJournal: "confirmed-source-journal"}
	if err = saveRelayRecord(a.relayTransferPath("pull/"+r.Peer, r.Operation), &r); err != nil {
		t.Fatal(err)
	}
	acts, err := a.Activities()
	if err != nil || len(acts) != 1 || acts[0].CanUndo || !acts[0].Journal.PendingReceipts() {
		t.Fatal("pending source receipt invisible or undo allowed", err)
	}
	if _, err = a.Undo(t.Context(), j.ID, true); err == nil {
		t.Fatal("forced undo lost source receipt")
	}
	if err = os.WriteFile(native, []byte("native-history plus later work"), 0600); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err = a.RecoverReceipts(t.Context(), j.ID); err != nil {
			t.Fatal(err)
		}
	}
	j, err = journal.Load(a.StateDir, j.ID)
	if err != nil {
		t.Fatal(err)
	}
	if j.PendingReceipts() || len(j.Remote) != 1 || j.Remote[0].ID != r.SourceJournal {
		t.Fatal("source recovery duplicated component")
	}
	b, err := os.ReadFile(native)
	if err != nil || string(b) != "native-history plus later work" {
		t.Fatal("recovery rewrote native history", err)
	}
}
