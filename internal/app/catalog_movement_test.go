package app

import (
	"context"
	"testing"
	"time"

	"github.com/roeehrl/hopsesh/internal/core/lineage"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

func TestCachedMovementSurvivesPreliminaryDiscovery(t *testing.T) {
	a, inv := movedPair(t)
	a.EnrichMovement(t.Context(), inv)
	for _, e := range inv.Entries {
		if e.Departure != nil {
			e.Departure.Status = "diverged"
			e.Movement = e.Departure
			a.saveCatalog(&Inventory{Machines: inv.Machines, Entries: []Entry{e}}, time.Now())
			break
		}
	}
	saved := a.CachedInventory()
	if len(saved.Entries) != 1 || saved.Entries[0].Movement == nil || saved.Entries[0].Departure == nil {
		t.Fatal("cached startup discarded movement role")
	}
	old := saved.Entries[0]
	preliminary := Entry{Machine: old.Machine, Agent: old.Agent, AgentName: old.AgentName, Session: old.Session}
	next := MergeDiscovery(saved, ScanUpdate{Machine: inv.Machines[0], Entries: []Entry{preliminary}})
	got := next.Entries[0]
	if got.Departure != old.Departure || got.Movement != old.Movement || got.Arrival != old.Arrival || !got.Cached {
		t.Fatal("preliminary summary discarded established movement roles")
	}
	if got.Live.State != "" && got.Live.State != agent.Unknown {
		t.Fatal("cached movement claimed current presence")
	}
}

func TestCommittedCurrentCopyOutranksAdministrativeActivity(t *testing.T) {
	a, inv := movedPair(t)
	defer inv.Close()
	g := inv.Entries[0].Lineage.Clone()
	at := time.Now().Add(-2 * time.Hour)
	g.Hops[0].Time = at
	ids := []lineage.ReplicaID{g.Hops[0].From, g.Hops[0].To}
	for i := 1; i < 4; i++ {
		if err := g.AppendHop(lineage.Hop{ID: string(rune('1' + i)), Time: at.Add(time.Duration(i) * time.Minute), Kind: lineage.HopContinue, From: ids[i%2], To: ids[(i+1)%2], Notify: true}); err != nil {
			t.Fatal(err)
		}
	}
	for i := range inv.Entries {
		inv.Entries[i].Lineage = g.Clone()
	}
	a.EnrichMovement(context.Background(), inv)
	if inv.Entries[0].Departure != nil || inv.Entries[0].Arrival == nil || inv.Entries[0].Arrival.Kind != "returned" || inv.Entries[1].Departure == nil {
		t.Fatal("fixture did not complete A -> B -> A -> B -> A")
	}
	for _, cached := range []bool{false, true} {
		inv.Entries[0].Cached, inv.Entries[1].Cached = cached, cached
		inv.Entries[0].Session.LastActivity = time.Now().Add(-time.Hour)
		inv.Entries[1].Session.LastActivity = time.Now()
		inv.Entries[1].Live = agent.LiveInfo{State: agent.Live}
		items := inv.Items()
		if len(items) != 1 || items[0].Entry.Machine != inv.Entries[0].Machine {
			t.Fatalf("bookkeeping/live status promoted the blocked original (cached=%t)", cached)
		}
	}
}

func TestLegacyCachedOutlineRecoversRolesWithoutContentReads(t *testing.T) {
	a, inv := movedPair(t)
	defer inv.Close()
	// Simulate the old catalog: committed receipt present, derived role fields absent.
	for i := range inv.Entries {
		inv.Entries[i].Departure, inv.Entries[i].Arrival, inv.Entries[i].Movement = nil, nil, nil
	}
	a.saveCatalog(inv, time.Now())
	before := contentReads.Load()
	saved := a.CachedInventory()
	if saved.Entries[0].Departure == nil || saved.Entries[0].Movement == nil || saved.Entries[1].Arrival == nil {
		t.Fatal("old cached outline lost its committed movement roles")
	}
	if saved.Entries[0].Movement.Status == "diverged" || saved.Entries[0].Live.State != agent.Unknown || contentReads.Load() != before {
		t.Fatal("outline recovery invented content or presence evidence")
	}
}
