package gui

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/roeehrl/hopsesh/internal/agents/all"
	"github.com/roeehrl/hopsesh/internal/app"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

func TestCheckoutsBeforeRepositoryEnrichment(t *testing.T) {
	repo := home(t)
	for _, args := range [][]string{{"init"}, {"remote", "add", "origin", "https://github.com/example/demo.git"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git: %s %v", out, err)
		}
	}
	sub := filepath.Join(repo, "subfolder")
	os.MkdirAll(sub, 0700)
	a := NewApp(all.Registry())
	defer a.Shutdown()
	a.inv = &app.Inventory{Discovering: true, Machines: []*app.Machine{{Name: "here", Local: true}, {Name: "remote"}}, Entries: []app.Entry{
		{Machine: "here", Session: agent.Summary{CWD: repo}},
		{Machine: "here", Session: agent.Summary{CWD: sub}},
		{Machine: "here", Session: agent.Summary{CWD: t.TempDir()}},
		{Machine: "here", Session: agent.Summary{CWD: filepath.Join(repo, "missing")}},
		{Machine: "remote", Session: agent.Summary{CWD: "/remote/not-to-probe"}},
	}}
	before := a.inv
	choices := a.Checkouts()
	if len(choices) != 1 || choices[0].Identity != "github.com/example/demo" || choices[0].Path != repo {
		t.Fatalf("early checkout choices: %+v", choices)
	}
	if a.inv != before || a.inv.Entries[0].Git != nil {
		t.Fatal("picker replaced/enriched shared inventory")
	}
}
