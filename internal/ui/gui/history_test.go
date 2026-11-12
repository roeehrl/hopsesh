package gui

import (
	"context"
	"strings"
	"testing"

	"github.com/roeehrl/hopsesh/internal/agents/all"
	"github.com/roeehrl/hopsesh/internal/app"
	"github.com/roeehrl/hopsesh/internal/config"
	"github.com/roeehrl/hopsesh/sdk/agent"
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

func TestHistoryLimitChangeReplansCachedSource(t *testing.T) {
	home(t)
	a := NewApp(all.Registry(), WithoutSessionWatching())
	t.Cleanup(a.Shutdown)
	if err := a.SetHistorySettings(config.History{LineageMB: 16}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Scan(); err != nil {
		t.Fatal(err)
	}
	var source app.Entry
	for i := range a.inv.Entries {
		e := &a.inv.Entries[i]
		if e.Agent == "claude" && string(e.Session.Key.Session) == sid {
			// The saved row carries an earlier resource failure; its native file
			// is readable. A new plan must re-read it after changing the limit.
			e.LineageError = (&ir.LimitError{Stage: ir.StageLineage, Limit: 16 << 20, Size: 17 << 20}).Error()
			e.CanArchiveLineage = true
			source = *e
			break
		}
	}
	if source.Session.Key.Session == "" {
		t.Fatal("fixture source missing")
	}
	if _, err := a.Plan(source.Machine, source.Session.Key.String(), "codex", OptsDTO{}); err == nil || !strings.Contains(err.Error(), "history.lineage_mb") {
		t.Fatalf("resource failure not exercised: %v", err)
	}
	old, oldCore := a.inv, a.snapshot()
	if err := a.SetHistorySettings(config.History{LineageMB: 64}); err != nil {
		t.Fatal(err)
	}
	if old != a.inv || old.Entries[0].Cached {
		t.Fatal("settings mutated/replaced a borrowed scan")
	}
	if _, _, err := a.selectionInventory(context.Background(), oldCore, old, source); err == nil {
		t.Fatal("old settings snapshot can still publish selection evidence")
	}
	if _, err := a.Plan(source.Machine, source.Session.Key.String(), "codex", OptsDTO{}); err != nil {
		t.Fatalf("direct retry retained the old resource error: %v", err)
	}
	if a.plan.Options.Limits.LineageBytes != ir.DefaultLineageBytes {
		t.Fatal("retry used the previous ledger budget")
	}
}

func TestHistoryRejectedSaveKeepsReviewedEvidence(t *testing.T) {
	home(t)
	a := NewApp(all.Registry(), WithoutSessionWatching())
	t.Cleanup(a.Shutdown)
	key := agent.SessionKey{Agent: "codex", Session: "source"}
	a.inv = &app.Inventory{Entries: []app.Entry{{Machine: "here", Session: agent.Summary{Key: key}}}}
	if err := a.SetHistorySettings(config.History{LineageMB: 1024}); err == nil {
		t.Fatal("invalid budget saved")
	}
	if len(a.historyStale) != 0 {
		t.Fatal("rejected settings invalidated observations")
	}
}
