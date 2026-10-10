package app

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/roeehrl/hopsesh/internal/config"
	"github.com/roeehrl/hopsesh/internal/core/host"
	"github.com/roeehrl/hopsesh/internal/core/journal"
	"github.com/roeehrl/hopsesh/internal/core/move"
	"github.com/roeehrl/hopsesh/internal/localstate"
)

func checkpointCacheFixture(t *testing.T, id string) (*App, string) {
	t.Helper()
	a := New(config.Defaults(), nil, t.TempDir(), nil)
	root := filepath.Join(a.StateDir, "cloud-checkpoints")
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	// Intentionally invalid JSON: listing/cleanup need only private metadata,
	// not conversation parsing, current relay access or an active provider.
	if err := os.WriteFile(filepath.Join(root, id+".json"), []byte("sensitive cached conversation"), 0600); err != nil {
		t.Fatal(err)
	}
	return a, root
}

func TestCheckpointCleanupIsIdempotentAndPreservesNativeLedger(t *testing.T) {
	a, root := checkpointCacheFixture(t, "review")
	ledger := filepath.Join(root, "source-ledger")
	if err := os.WriteFile(ledger, []byte("native lineage receipt"), 0600); err != nil {
		t.Fatal(err)
	}
	entries, err := a.CloudCheckpointCache(t.Context())
	if err != nil || len(entries) != 1 || entries[0].Started || entries[0].Operation != "review" {
		t.Fatal("metadata listing failed", entries, err)
	}
	for range 2 {
		if err := a.RemoveCloudCheckpoint(t.Context(), "review"); err != nil {
			t.Fatal(err)
		}
	}
	if err := checkpointRetired(root, "review"); err == nil {
		t.Fatal("removed operation can be reused")
	}
	if data, err := os.ReadFile(ledger); err != nil || string(data) != "native lineage receipt" {
		t.Fatal("cleanup changed lineage", err)
	}
	marker, err := os.ReadFile(filepath.Join(root, "retired", "review.json"))
	if err != nil || bytes.Contains(marker, []byte("sensitive")) {
		t.Fatal("cleanup retained conversation in tombstone", err)
	}
	entries, err = a.CloudCheckpointCache(t.Context())
	if err != nil || len(entries) != 0 {
		t.Fatal("cleanup did not free cache capacity", err)
	}
	if err = a.RemoveCloudCheckpoint(t.Context(), "unknown"); err == nil {
		t.Fatal("cleanup accepted an unknown review")
	}
	if _, err = os.Stat(filepath.Join(a.StateDir, "operations", "unknown.json.lock")); !os.IsNotExist(err) {
		t.Fatal("unknown cleanup allocated durable state", err)
	}
}

func TestCheckpointCleanupExcludesApplyingAndInterruptedOperations(t *testing.T) {
	a, root := checkpointCacheFixture(t, "interrupted")
	lock, err := move.LockCheckpointCleanup(a.StateDir, "interrupted")
	if err != nil {
		t.Fatal(err)
	}
	if err := a.RemoveCloudCheckpoint(t.Context(), "interrupted"); !errors.Is(err, localstate.ErrBusy) {
		t.Fatal("cleanup raced an applying operation", err)
	}
	lock.Close()
	j, err := journal.New(a.StateDir, journal.KindMove, "interrupted import")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(map[string]any{"phase": "native", "journal": j.ID})
	if err := os.WriteFile(filepath.Join(a.StateDir, "operations", "interrupted.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	if err := a.RemoveCloudCheckpoint(t.Context(), "interrupted"); err == nil {
		t.Fatal("cleanup discarded interrupted source")
	}
	if _, err := os.Stat(filepath.Join(root, "interrupted.json")); err != nil {
		t.Fatal("failed cleanup removed checkpoint", err)
	}
	// Explicit undo resolves uncertain writes; the operation and journal remain.
	j.Undone = true
	if err := j.Save(); err != nil {
		t.Fatal(err)
	}
	if err := a.RemoveCloudCheckpoint(t.Context(), "interrupted"); err != nil {
		t.Fatal("undone operation could not release cache", err)
	}
	for _, p := range []string{filepath.Join(a.StateDir, "operations", "interrupted.json"), filepath.Join(journal.Dir(a.StateDir), j.ID, "journal.json")} {
		if _, err := os.Stat(p); err != nil {
			t.Fatal("cleanup discarded recovery history", err)
		}
	}
}

func TestCheckpointCleanupRefusesPendingReceiptsAndLinkedEntries(t *testing.T) {
	a, root := checkpointCacheFixture(t, "pending")
	j, err := journal.New(a.StateDir, journal.KindMove, "pending import receipt")
	if err != nil {
		t.Fatal(err)
	}
	if err = j.QueueAcknowledgment(journal.Acknowledgment{Operation: "pending", Machine: "source", Transport: "relay-pull", Peer: "peer"}); err != nil {
		t.Fatal(err)
	}
	if err = host.LocalFS().WriteFile(filepath.Join(a.StateDir, "operations", "pending.json"), []byte(`{"phase":"complete","journal":"`+j.ID+`"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err = a.RemoveCloudCheckpoint(t.Context(), "pending"); err == nil {
		t.Fatal("pending acknowledgment lost its source")
	}
	if err = a.RemoveCloudCheckpoint(t.Context(), "../pending"); err == nil {
		t.Fatal("cleanup accepted traversal")
	}
	outside := filepath.Join(t.TempDir(), "outside.json")
	if err = os.WriteFile(outside, []byte("outside"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink(outside, filepath.Join(root, "linked.json")); err != nil {
		if os.IsPermission(err) {
			t.Skip("runner cannot create a disposable symlink")
		}
		t.Fatal(err)
	}
	if err = a.RemoveCloudCheckpoint(t.Context(), "linked"); err == nil {
		t.Fatal("cleanup accepted a linked checkpoint")
	}
	if data, err := os.ReadFile(outside); err != nil || string(data) != "outside" {
		t.Fatal("cleanup changed an external file", err)
	}
}
