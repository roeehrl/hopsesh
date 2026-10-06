package app

import (
	"github.com/roeehrl/hopsesh/internal/core/lineage"
	"github.com/roeehrl/hopsesh/sdk/agent"
	"testing"
)

func TestDeferredMarkCannotOutliveItsBranchDeparture(t *testing.T) {
	m := lineage.New("pending-test")
	a := m.Upsert(lineage.Replica{Endpoint: "A", Location: "A", Key: agent.SessionKey{Agent: "claude", Session: "a"}})
	b := m.Upsert(lineage.Replica{Endpoint: "B", Location: "B", Key: agent.SessionKey{Agent: "claude", Session: "a"}})
	if err := m.AppendHop(lineage.Hop{ID: "departure", From: a, To: b, Kind: lineage.HopMove}); err != nil {
		t.Fatal(err)
	}
	p := lineage.Pending{Operation: "departure", Branch: m.Branch, Replica: a, Key: m.Replica(a).Key}
	if !currentDeparture(p, m) {
		t.Fatal("committed departure should remain markable")
	}
	sibling := m.Clone()
	sibling.Branch = sibling.Fork("sibling", nil)
	if currentDeparture(p, sibling) {
		t.Fatal("cannot mark a sibling branch")
	}
	if err := m.AppendHop(lineage.Hop{ID: "return", From: b, To: a, Kind: lineage.HopMove}); err != nil {
		t.Fatal(err)
	}
	if currentDeparture(p, m) {
		t.Fatal("a return cancels the old departure's deferred mark")
	}
	if err := m.UndoOperation("departure"); err != nil {
		t.Fatal(err)
	}
	if currentDeparture(p, m) {
		t.Fatal("undone departure cannot mark native files")
	}
}
