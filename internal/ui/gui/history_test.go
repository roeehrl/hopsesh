package gui

import (
	"testing"

	"github.com/roeehrl/hopsesh/internal/agents/all"
	"github.com/roeehrl/hopsesh/internal/config"
	"github.com/roeehrl/hopsesh/sdk/ir"
)

func TestHistorySettingsPersistDefaultsAsUnset(t *testing.T) {
	home(t)
	a := NewApp(all.Registry())
	t.Cleanup(func() { _ = a.core.Catalog.Close() })
	if d := a.HistorySettings(); d.Effective != ir.DefaultLimits() || len(d.ContextBudgets) == 0 {
		t.Fatalf("defaults: %+v", d)
	}
	if err := a.SetHistorySettings(config.History{ContextBudget: 64_000, Older: ir.OlderRecent, ReadMemoryMB: 256, ArchiveMB: 1024}); err != nil {
		t.Fatal(err)
	}
	c, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.History != (config.History{ContextBudget: 64_000, Older: ir.OlderRecent, ArchiveMB: 1024}) {
		t.Fatalf("a default value must be stored as unset: %+v", c.History)
	}
	if a.core.DefaultOptions().Limits.ArchiveBytes != 1<<30 {
		t.Fatal("transfers do not receive the saved limits")
	}
	if err := a.SetHistorySettings(config.History{Older: "everything"}); err == nil {
		t.Fatal("invalid settings saved")
	}
	if a.HistorySettings().Older != ir.OlderRecent {
		t.Fatal("a rejected save changed the settings")
	}
}
