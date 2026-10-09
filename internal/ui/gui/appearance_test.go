package gui

import (
	"errors"
	"testing"

	"github.com/roeehrl/hopsesh/internal/agents/all"
	"github.com/roeehrl/hopsesh/internal/config"
)

func TestAppearanceSettingsPersistAndNotify(t *testing.T) {
	home(t)
	a := NewApp(all.Registry())
	t.Cleanup(func() { _ = a.core.Catalog.Close() })
	if a.Appearance() != "system" || a.Settings().Appearance != "system" {
		t.Fatal("default must follow system")
	}
	var changes []string
	a.Emitter = func(name string, data any) {
		if name == AppearanceEvent {
			// Also proves notifications are sent without holding the config lock.
			if a.Appearance() != data.(string) {
				t.Error("notification differs from saved choice")
			}
			changes = append(changes, data.(string))
		}
	}
	for _, mode := range []string{"dark", "light", "system"} {
		if err := a.SaveSettings(SettingsInput{Appearance: &mode}); err != nil {
			t.Fatal(err)
		}
		saved, err := config.Load()
		if err != nil || saved.AppearanceMode() != mode {
			t.Fatalf("saved %q: %+v %v", mode, saved, err)
		}
		if a.Settings().Appearance != mode || a.termPrefs().Appearance != mode {
			t.Fatalf("windows disagree on %q", mode)
		}
		if got := NewApp(all.Registry()).Appearance(); got != mode {
			t.Fatalf("restarted app: %q", got)
		}
	}
	if len(changes) != 3 {
		t.Fatalf("events: %v", changes)
	}
	// Unrelated saves from older clients preserve the choice and do not broadcast.
	dark := "dark"
	if err := a.SaveSettings(SettingsInput{Appearance: &dark}); err != nil {
		t.Fatal(err)
	}
	if err := a.SaveSettings(SettingsInput{Layout: "ghq"}); err != nil {
		t.Fatal(err)
	}
	if a.Appearance() != "dark" || len(changes) != 4 {
		t.Fatalf("unrelated save reset appearance: %v", changes)
	}
	bad := "sepia"
	if err := a.SaveSettings(SettingsInput{Appearance: &bad}); err == nil {
		t.Fatal("invalid choice accepted")
	}
	if a.Appearance() != "dark" || len(changes) != 4 {
		t.Fatal("invalid choice changed appearance")
	}
	// A failed save must neither apply nor publish the unsaved preference.
	a.cfgErr = errors.New("config cannot be overwritten")
	light := "light"
	if err := a.SaveSettings(SettingsInput{Appearance: &light}); err == nil {
		t.Fatal("expected save error")
	}
	if a.Appearance() != "dark" || len(changes) != 4 {
		t.Fatal("failed save changed appearance")
	}
}
