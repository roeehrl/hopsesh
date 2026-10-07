package gui

import (
	"context"
	"encoding/json"
	"github.com/roeehrl/hopsesh/internal/agents/all"
	"github.com/roeehrl/hopsesh/internal/app"
	"github.com/roeehrl/hopsesh/internal/config"
	"github.com/roeehrl/hopsesh/internal/core/move"
	"github.com/roeehrl/hopsesh/internal/core/observe"
	"github.com/roeehrl/hopsesh/sdk/agent"
	"testing"
	"time"
)

func TestRuntimeDoesNotReplaceInventoryForReviewedTransfer(t *testing.T) {
	home(t)
	cfg := config.Defaults()
	if err := config.Save(&cfg); err != nil {
		t.Fatal(err)
	}
	current := &app.Inventory{}
	a := &App{core: app.New(cfg, all.Registry(), config.StateDir(), nil), inv: current, plan: &move.Plan{}}
	body, _ := json.Marshal(app.Observation{Machine: app.LocalName(), InventoryComplete: true})
	s := observe.Snapshot{Epoch: "owner", Sequence: 3, ObservedAt: time.Now(), ExpiresAt: time.Now().Add(time.Minute), Data: body}
	a.acceptRuntimeSnapshot(s)
	if a.inv != current || a.backend.appliedSequence != 0 || a.backend.snapshot.Sequence != 3 {
		t.Fatal("notification replaced a reviewed plan or lost its deferred evidence")
	}
	a.res = &move.Result{}
	a.acceptRuntimeSnapshot(s)
	if a.inv == current || a.backend.appliedSequence != 3 {
		t.Fatal("completed transfer did not adopt deferred observation")
	}
}

func TestDesktopAttachesHeadlessWithoutAnotherOwnerAndQuitLeavesItRunning(t *testing.T) {
	home(t)
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	owner, err := app.New(cfg, all.Registry(), config.StateDir(), nil).StartRuntime(t.Context(), "headless", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	a := NewApp(all.Registry())
	defer a.Shutdown()
	if _, err = a.RefreshHere(); err != nil {
		t.Fatal(err)
	}
	if got := a.RuntimeStatus(); !got.Connected || got.Owner.Mode != "headless" || got.Owner.Epoch != owner.Status().Epoch {
		t.Fatalf("attachment: %+v", got)
	}
	a.Shutdown()
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	var status any
	if err = a.backend.client.Call(ctx, "status", nil, &status); err != nil {
		t.Fatalf("GUI quit stopped external owner: %v", err)
	}
}

func TestOlderSharedObservationCannotReplaceFreshScannedIdentityAndSettings(t *testing.T) {
	home(t)
	cfg := config.Defaults()
	if err := config.Save(&cfg); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	current := &app.Inventory{Entries: []app.Entry{{Machine: app.LocalName(), Session: agent.Summary{Key: agent.SessionKey{Agent: "claude", Session: "current-session", Profile: "registered-profile"}}}}}
	a := &App{core: app.New(cfg, all.Registry(), config.StateDir(), nil), inv: current, invAt: now}
	cfg.FamilyNames = map[string]string{"family": "Saved family label"}
	if err := config.Save(&cfg); err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(app.Observation{Machine: app.LocalName(), InventoryComplete: true, Entries: []app.Entry{{Machine: app.LocalName(), Session: agent.Summary{Key: agent.SessionKey{Agent: "claude", Session: "old-unregistered-session"}}}}})
	a.acceptRuntimeSnapshot(observe.Snapshot{Epoch: "old-owner", Sequence: 1, ObservedAt: now.Add(-time.Second), ExpiresAt: now.Add(time.Minute), Data: body})
	if a.inv != current || a.inv.Entries[0].Session.Key.Profile != "registered-profile" {
		t.Fatal("older snapshot changed the fresh session identity")
	}
	if a.core.Cfg.FamilyNames["family"] != "Saved family label" {
		t.Fatal("settings adoption lost a saved family label")
	}
}
func TestGUIAdoptsCLISettingsAndPreservesConcurrentWriters(t *testing.T) {
	home(t)
	a := NewApp(all.Registry())
	defer a.Shutdown()
	if _, err := a.RefreshHere(); err != nil {
		t.Fatal(err)
	}
	if _, err := config.SetSetting("appearance", []byte(`"dark"`), nil); err != nil {
		t.Fatal(err)
	}
	if _, err := a.RefreshHere(); err != nil {
		t.Fatal(err)
	}
	if a.snapshot().Cfg.AppearanceMode() != "dark" {
		t.Fatal("CLI settings not adopted")
	}
	if err := a.SetAgent("codex", false, false, false); err != nil {
		t.Fatal(err)
	}
	saved, err := config.Load()
	if err != nil || saved.AppearanceMode() != "dark" || saved.AgentEnabled("codex") {
		t.Fatalf("settings lost: %+v %v", saved, err)
	}
}
