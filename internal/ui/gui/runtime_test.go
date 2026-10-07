package gui

import (
	"context"
	"github.com/roeehrl/hopsesh/internal/agents/all"
	"github.com/roeehrl/hopsesh/internal/app"
	"github.com/roeehrl/hopsesh/internal/config"
	"testing"
	"time"
)

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
