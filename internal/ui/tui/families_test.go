package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/roeehrl/hopsesh/internal/app"
	"github.com/roeehrl/hopsesh/internal/config"
	"github.com/roeehrl/hopsesh/internal/core/lineage"
	"github.com/roeehrl/hopsesh/internal/core/repos"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

func TestProgressiveBrowseDoesNotDiscoverUnrelatedCheckouts(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	core := &app.App{Cfg: config.Defaults()}
	core.Cfg.ReposDir = filepath.Join(home, "git")
	gitDir := filepath.Join(core.Cfg.ReposDir, "unrelated", ".git")
	if err := os.MkdirAll(gitDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gitDir, "config"), []byte("[remote \"origin\"]\nurl = https://github.com/example/demo.git\n"), 0600); err != nil {
		t.Fatal(err)
	}
	inv := &app.Inventory{Machines: []*app.Machine{{Name: "remote"}}, Entries: []app.Entry{{Machine: "remote", Session: agent.Summary{Key: agent.SessionKey{Agent: "claude", Session: "fixture"}}, Git: &repos.GitState{IsRepo: true, Identity: "github.com/example/demo", Remote: "https://github.com/example/demo.git"}}}}
	m := &model{deps: Deps{App: core}, inv: inv, mode: modeBrowse}
	m.buildRows()
	if len(m.rows) != 2 || strings.Contains(m.rows[0].header, "unrelated") {
		t.Fatal("browsing searched unrelated checkouts instead of using discovery metadata")
	}
}

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
