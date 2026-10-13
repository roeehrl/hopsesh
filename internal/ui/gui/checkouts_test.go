package gui

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/roeehrl/hopsesh/internal/agents/all"
	"github.com/roeehrl/hopsesh/internal/app"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

func TestRepositoryAvailabilityRemainsPendingUntilDiscoveryCompletes(t *testing.T) {
	home(t)
	a := NewApp(all.Registry())
	defer a.Shutdown()
	inv := &app.Inventory{Discovering: true, Machines: []*app.Machine{{Name: "here", Local: true}}, Entries: []app.Entry{{
		Machine: "here", Agent: "claude", AgentName: "Claude Code",
		Session: agent.Summary{Key: agent.SessionKey{Agent: "claude", Session: "pending-repo"}, CWD: t.TempDir()},
	}}}
	first := scanDTO(a.core, inv, time.Now(), time.Now(), nil)
	if len(first.Groups) != 1 || !first.Groups[0].RepoPending || first.Groups[0].NoRepo {
		t.Fatalf("unknown repository became an availability claim: %+v", first.Groups)
	}
	inv.Discovering = false
	final := scanDTO(a.core, inv, time.Now(), time.Now(), nil)
	if len(final.Groups) != 1 || final.Groups[0].RepoPending || !final.Groups[0].NoRepo {
		t.Fatalf("completed no-repository discovery remained pending: %+v", final.Groups)
	}
}

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
