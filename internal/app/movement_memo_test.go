package app

import (
	"testing"

	"github.com/roeehrl/hopsesh/internal/core/lineage"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

// A remembered family observation is rebound to the rows of the scan that recalls it,
// even when those rows moved (entries are sorted by activity), and is dropped when any
// input changes.
func TestMovementMemoRebindsRowsAndMissesOnChange(t *testing.T) {
	a := &App{movementMemo: &movementMemo{}}
	g := lineage.New("family")
	row := func(id string, size int64) Entry {
		return Entry{Machine: "one", Lineage: g, Session: agent.Summary{Key: agent.SessionKey{Agent: "codex", Session: agent.SessionID(id)}, Size: size}}
	}
	first := &Inventory{Entries: []Entry{row("a", 10), row("b", 20)}}
	fp := a.movementFingerprint(t.Context(), first, "family")
	observed := map[lineage.ReplicaID]movementObservation{"ra": {entry: &first.Entries[0], valid: true}, "rb": {entry: &first.Entries[1]}}
	a.movementMemo.put("family", fp, g, first, observed, map[int]lineage.ReplicaID{0: "ra", 1: "rb"})

	second := &Inventory{Entries: []Entry{row("b", 20), row("a", 10)}}
	memo, ok := a.movementMemo.get("family", a.movementFingerprint(t.Context(), second, "family"))
	if !ok {
		t.Fatal("an unchanged family missed the memo")
	}
	gotObserved, gotIDs := map[lineage.ReplicaID]movementObservation{}, map[int]lineage.ReplicaID{}
	if memo.recall(second, gotObserved, gotIDs) != g {
		t.Fatal("recall lost the observed graph")
	}
	if gotIDs[0] != "rb" || gotIDs[1] != "ra" || gotObserved["ra"].entry != &second.Entries[1] || !gotObserved["ra"].valid {
		t.Fatalf("recall bound the wrong rows: %v", gotIDs)
	}
	third := &Inventory{Entries: []Entry{row("a", 11), row("b", 20)}}
	if _, ok := a.movementMemo.get("family", a.movementFingerprint(t.Context(), third, "family")); ok {
		t.Fatal("a grown transcript reused the old observation")
	}
}
