package tui

import (
	"testing"

	"github.com/roeehrl/hopsesh/internal/app"
	"github.com/roeehrl/hopsesh/internal/config"
	"github.com/roeehrl/hopsesh/internal/core/lineage"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

func TestFamilyNavigationAndCollapsePersistence(t *testing.T) {
	t.Setenv("HOPSESH_CONFIG_DIR", t.TempDir())
	core := &app.App{Cfg: config.Defaults()}
	core.Cfg.List.GroupBy = "family"
	root := &lineage.Manifest{Family: "family", Branch: "root", Branches: []lineage.Branch{{ID: "root"}, {ID: "fork", Parent: "root"}}}
	fork := root.Clone()
	fork.Branch = "fork"
	inv := &app.Inventory{Entries: []app.Entry{{Machine: "here", Session: agent.Summary{Key: agent.SessionKey{Agent: "claude", Session: "root"}, Title: "Original"}, Lineage: root}, {Machine: "here", Session: agent.Summary{Key: agent.SessionKey{Agent: "claude", Session: "fork"}, Title: "Experiment"}, Lineage: fork}}}
	m := &model{deps: Deps{App: core}, inv: inv, mode: modeBrowse}
	m.buildRows()
	if len(m.rows) != 3 || m.rows[2].relationship.Depth != 1 {
		t.Fatalf("missing family hierarchy: %+v", m.rows)
	}
	_, cmd := m.key("enter")
	if cmd != nil || m.mode != modeBrowse || len(m.rows) != 1 {
		t.Fatal("header launched a session instead of collapsing")
	}
	saved, err := config.Load()
	if err != nil || len(saved.List.Collapsed) != 1 {
		t.Fatalf("collapse not persisted: %v", err)
	}
	core.Cfg = saved
	next := &model{deps: Deps{App: core}, inv: inv, mode: modeBrowse}
	next.buildRows()
	if len(next.rows) != 1 {
		t.Fatal("collapse lost on restart")
	}
	next.key("right")
	if len(next.rows) != 3 {
		t.Fatal("right did not expand")
	}
	next.key("g")
	saved, err = config.Load()
	if err != nil || saved.List.GroupBy != "account" {
		t.Fatal("group preference not saved")
	}
}
