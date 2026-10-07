package tui

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/roeehrl/hopsesh/internal/app"
	"github.com/roeehrl/hopsesh/internal/core/observe"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

func TestSharedTUIUpdatesPreserveSelectionRemoteEntriesAndPlanUnderReview(t *testing.T) {
	m := newModel(t)
	m.Update(m.Init()())
	if len(m.inv.Entries) == 0 {
		t.Fatal("fixture has no sessions")
	}
	remote := m.inv.Entries[0]
	remote.Machine = "remote-box"
	remote.Location = agent.MachineLocation("remote-box")
	remote.Session.Key.Session = "remote-session"
	m.inv.Entries = append(m.inv.Entries, remote)
	m.buildRows()
	for i, r := range m.rows {
		if r.item != nil && r.item.Entry.Machine == "remote-box" {
			m.cursor = i
			break
		}
	}
	obs, err := m.deps.App.ObserveLocal(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	obs.Entries = nil
	obs.InventoryComplete = false
	data, _ := json.Marshal(obs)
	s := observe.Snapshot{Epoch: "test", Sequence: 2, ObservedAt: time.Now(), ExpiresAt: time.Now().Add(time.Minute), Data: data}
	m.Update(runtimeSnapshot{s})
	if m.rows[m.cursor].item.Entry.Machine != "remote-box" {
		t.Fatal("local notification moved selection")
	}
	for _, e := range m.inv.Entries {
		if e.Machine == app.LocalName() && e.Live.State != agent.Unknown {
			t.Fatal("partial observation inferred idle or absence")
		}
	}
	old := m.inv
	m.mode = modePlan
	m.Update(runtimeSnapshot{s})
	if m.inv != old || m.mode != modePlan {
		t.Fatal("notification replaced a plan under review")
	}
	m.mode = modeBrowse
	m.Update(runtimeDisconnected{errors.New("test disconnect")})
	for _, r := range m.rows {
		if r.item != nil && r.item.Entry.Machine == app.LocalName() && r.item.Entry.Live.State != agent.Unknown {
			t.Fatal("disconnected cached row remained active")
		}
	}
}

func TestSharedTUISnapshotDoesNotClaimExpiredEvidenceIsFresh(t *testing.T) {
	m := newModel(t)
	m.Update(m.Init()())
	for i := range m.inv.Entries {
		if m.inv.Entries[i].Machine == app.LocalName() {
			m.inv.Entries[i].Live.State = agent.Live
		}
	}
	m.buildRows()
	m.Update(runtimeSnapshot{observe.Snapshot{Epoch: "old", Sequence: 4, ObservedAt: time.Now().Add(-time.Hour), ExpiresAt: time.Now().Add(-time.Minute)}})
	for _, r := range m.rows {
		if r.item != nil && r.item.Entry.Machine == app.LocalName() && r.item.Entry.Live.State != agent.Unknown {
			t.Fatal("expired source became fresh through subscriber delivery")
		}
	}
}
