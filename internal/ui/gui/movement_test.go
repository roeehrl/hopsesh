package gui

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/roeehrl/hopsesh/internal/agents/all"
	"github.com/roeehrl/hopsesh/internal/app"
	"github.com/roeehrl/hopsesh/internal/config"
	"github.com/roeehrl/hopsesh/internal/core/lineage"
	"github.com/roeehrl/hopsesh/internal/core/move"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

func TestEntryMovementContract(t *testing.T) {
	home(t)
	a := NewApp(all.Registry())
	t.Cleanup(func() { _ = a.core.Catalog.Close() })
	checked := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	e := app.Entry{Machine: app.LocalName(), Agent: "codex", AgentName: "Codex", Session: agent.Summary{Key: agent.SessionKey{Agent: "codex", Session: "source"}}, ObservedAt: checked,
		Returns:  []app.ReturnCandidate{{Replica: "replica", Machine: "studio", Agent: "claude", AgentName: "Claude Code", Profile: "work", ProfileLabel: "Work", Key: "claude@work/original", Status: "verify", Reason: "offline"}},
		Movement: &app.MovementNotice{Operation: "op", Status: "prepared", Text: "Destination prepared", Machine: "studio", Agent: "claude", Key: "claude@work/original", CheckedAt: checked, Delivery: "pending"}}
	d := entryDTO(a.snapshot(), &app.Inventory{}, app.Item{Entry: e}, nil)
	b, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"returns":[`, `"movement":`, `"status":"prepared"`, `"profileLabel":"Work"`, `"observedAt":"2026-01-02T03:04:05Z"`} {
		if !strings.Contains(string(b), want) {
			t.Fatalf("missing %s in %s", want, b)
		}
	}
	o := OptsDTO{TargetProfile: e.Returns[0].Profile, TargetSession: e.Returns[0].Key, Notify: true}.options(move.Options{})
	if o.TargetProfile != "work" || o.TargetSession != "claude@work/original" || !o.Notify {
		t.Fatalf("lost exact return options: %+v", o)
	}
}

func TestMovementNoticeSettingsPersist(t *testing.T) {
	home(t)
	a := NewApp(all.Registry())
	t.Cleanup(func() { _ = a.core.Catalog.Close() })
	if !a.Info().Defaults.MovementNotices || !a.Settings().MovementNotices {
		t.Fatal("notices should default on")
	}
	for _, on := range []bool{false, true} {
		original := map[bool]string{true: config.OriginalBlock, false: config.OriginalOff}[on]
		if err := a.SaveSettings(SettingsInput{Layout: "flat", Original: original, Previews: true}); err != nil {
			t.Fatal(err)
		}
		reloaded := NewApp(all.Registry())
		t.Cleanup(func() { _ = reloaded.core.Catalog.Close() })
		if reloaded.Info().Defaults.MovementNotices != on || reloaded.Settings().MovementNotices != on {
			t.Fatal("notice preference not persisted", on)
		}
	}
}

func TestResolveEntryFindsHiddenRawReplica(t *testing.T) {
	home(t)
	a := NewApp(all.Registry())
	t.Cleanup(func() { _ = a.core.Catalog.Close() })
	key := agent.SessionKey{Agent: "claude", Session: "original"}
	old := app.Entry{Machine: app.LocalName(), Agent: "claude", AgentName: "Claude Code", Session: agent.Summary{Key: key, Title: "Original", LastActivity: time.Now().Add(-time.Hour)}}
	old.Lineage = &lineage.Manifest{Family: "family", Branch: "original"}
	newer := old
	newer.Machine = "studio"
	newer.Session.Title = "Newer"
	newer.Session.LastActivity = time.Now()
	a.inv = &app.Inventory{Entries: []app.Entry{old, newer}, Machines: []*app.Machine{{Name: old.Machine, Local: true}, {Name: "studio"}}}
	if len(a.inv.Items()) != 1 || a.inv.Items()[0].Entry.Machine != "studio" {
		t.Fatal("fixture did not hide original")
	}
	d, err := a.ResolveEntry(old.Machine, key.String())
	if err != nil {
		t.Fatal(err)
	}
	if d.Machine != old.Machine || d.Title != "Original" || len(d.Copies) != 2 {
		t.Fatalf("resolved wrong representative: %+v", d)
	}
	if _, err = a.ResolveEntry("unknown", key.String()); err == nil {
		t.Fatal("guessed another machine")
	}
}
