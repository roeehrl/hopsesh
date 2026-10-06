package gui

import (
	"context"
	"github.com/roeehrl/hopsesh/internal/agents/all"
	"github.com/roeehrl/hopsesh/internal/app"
	"github.com/roeehrl/hopsesh/sdk/agent"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAccountPlanInvalidatedByEditAndRemoval(t *testing.T) {
	home(t)
	a := NewApp(all.Registry())
	ctx := context.Background()
	p, err := a.core.RegisterAccount(ctx, "", "claude", "Second personal", "", []string{" Personal ", "personal"})
	if err != nil {
		t.Fatal(err)
	}
	scan, err := a.Scan()
	if err != nil {
		t.Fatal(err)
	}
	e := findEntry(t, scan, "claude/"+sid)
	plan, err := a.Plan(e.Machine, e.Key, "claude", OptsDTO{TargetProfile: p.ID})
	if err != nil || len(plan.Blockers) > 0 {
		t.Fatal(plan, err)
	}
	ps, _ := a.core.Accounts()
	for _, v := range ps {
		if v.ID == p.ID {
			p = v
		}
	}
	if err = a.core.EditAccount(p.ID, "Renamed", p.Tags, p.Generation); err != nil {
		t.Fatal(err)
	}
	if _, err = a.Apply(); err == nil || !strings.Contains(err.Error(), "changed") {
		t.Fatal("stale account plan accepted", err)
	}
	files, _ := filepath.Glob(filepath.Join(p.Root, "projects", "*", "*.jsonl"))
	if len(files) > 0 {
		t.Fatal("stale plan wrote native files")
	}
	scan, err = a.Scan()
	if err != nil {
		t.Fatal(err)
	}
	e = findEntry(t, scan, "claude/"+sid)
	if _, err = a.Plan(e.Machine, e.Key, "claude", OptsDTO{TargetProfile: p.ID}); err != nil {
		t.Fatal(err)
	}
	ps, _ = a.core.Accounts()
	for _, v := range ps {
		if v.ID == p.ID {
			p = v
		}
	}
	if err = a.core.ForgetAccount(p.ID, p.Generation); err != nil {
		t.Fatal(err)
	}
	if _, err = a.Apply(); err == nil || !strings.Contains(err.Error(), "removed") {
		t.Fatal("removed account plan accepted", err)
	}
	if _, err = os.Stat(p.Root); err != nil {
		t.Fatal("forget removed vendor data", err)
	}
}

func TestCodexDesktopAvailabilityAndExactThreadThroughService(t *testing.T) {
	home(t)
	a := NewApp(all.Registry())
	scan, _ := a.Scan()
	e := findEntry(t, scan, "claude/"+sid)
	if _, err := a.Plan(e.Machine, e.Key, "codex", OptsDTO{}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Apply(); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Scan(); err != nil {
		t.Fatal(err)
	}
	local := a.inv.Local()
	for i := range local.Agents {
		if local.Agents[i].Agent == "codex" {
			local.Agents[i].Install.Desktop = "/Applications/Codex.app"
			local.Agents[i].Install.OS = "darwin"
		}
	}
	var entry app.Entry
	for _, v := range a.inv.Entries {
		if v.Agent == "codex" {
			entry = v
		}
	}
	d := entryDTO(a.core, a.inv, app.Item{Entry: entry}, nil)
	if !d.CanApp {
		t.Fatal("desktop option hidden", d.AppWhy)
	}
	c, err := a.core.Resume(a.inv, entry, agent.ResumeOptions{App: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Argv) != 4 || c.Argv[3] != "codex://threads/"+string(entry.Session.Key.Session) || !c.Wait {
		t.Fatalf("wrong launch: %+v", c)
	}
	for i := range local.Agents {
		if local.Agents[i].Agent == "codex" {
			local.Agents[i].Install.Desktop = ""
			local.Agents[i].Install.DesktopWhy = "Install the desktop app"
		}
	}
	d = entryDTO(a.core, a.inv, app.Item{Entry: entry}, nil)
	if d.CanApp || d.AppWhy == "" {
		t.Fatal("missing installation offered")
	}
	if _, err = a.core.Resume(a.inv, entry, agent.ResumeOptions{App: true}); err == nil {
		t.Fatal("missing installation fell back to terminal")
	}
}

func TestRegisterDefaultRootBeforeFirstScanStillAllowsUnqualifiedSelection(t *testing.T) {
	home(t)
	a := NewApp(all.Registry())
	root := filepath.Join(os.Getenv("HOME"), ".claude")
	p, err := a.core.RegisterAccount(context.Background(), "local", "claude", "My personal login", root, []string{"Personal"})
	if err != nil {
		t.Fatal(err)
	}
	scan, err := a.Scan()
	if err != nil {
		t.Fatal(err)
	}
	in, ok := a.inv.Local().Install("claude")
	if !ok || in.ProfileID() != p.ID || !in.Profile.Default {
		t.Fatalf("registered default disappeared: %+v", in)
	}
	e := findEntry(t, scan, "claude/"+sid)
	if _, err = a.core.Resume(a.inv, a.inv.Entries[0], agent.ResumeOptions{}); err != nil {
		t.Fatal("default root could not resume", err)
	}
	if e.Profile == nil || e.Profile.Name != "My personal login" {
		t.Fatal("scan overwrote custom label")
	}
}
