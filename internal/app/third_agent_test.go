package app

import (
	"testing"

	"github.com/roeehrl/hopsesh/agents/claude"
	"github.com/roeehrl/hopsesh/agents/codex"
	"github.com/roeehrl/hopsesh/internal/config"
	"github.com/roeehrl/hopsesh/internal/core/registry"
	"github.com/roeehrl/hopsesh/internal/testkit/thirdagent"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

// A third local agent is named, listed and chosen like the first two, and a disabled agent
// keeps its name.
func TestThirdAgent(t *testing.T) {
	reg, err := registry.New(claude.New(), codex.New(), thirdagent.New())
	if err != nil {
		t.Fatal(err)
	}
	newApp := func(cfg config.Config) *App {
		a := New(cfg, reg, t.TempDir(), nil)
		t.Cleanup(func() { _ = a.Catalog.Close() })
		return a
	}
	a := newApp(config.Config{})
	if got := a.agentName(thirdagent.ID); got != "Third Agent" {
		t.Errorf("agentName = %q", got)
	}
	if got := a.accountAgents(); got != "Claude Code, Codex and Third Agent" {
		t.Errorf("accountAgents = %q", got)
	}
	inv := func(ids ...agent.ID) *Inventory {
		m := &Machine{Name: "here", Local: true}
		for _, id := range ids {
			m.Agents = append(m.Agents, AgentState{Agent: id, Install: agent.Install{Agent: id, Present: true}})
		}
		return &Inventory{Machines: []*Machine{m}}
	}
	cloud := Entry{Location: agent.CloudLocation("amp"), Session: agent.Summary{Key: agent.SessionKey{Agent: "amp", Session: "T-1"}}}
	if got := a.BringTarget(inv(thirdagent.ID), cloud); got != thirdagent.ID {
		t.Errorf("BringTarget with only the third agent here = %q", got)
	}
	if got := a.BringTarget(inv(thirdagent.ID, "codex"), cloud); got != "codex" {
		t.Errorf("BringTarget follows display order: got %q", got)
	}

	off := newApp(config.Config{Agents: map[string]config.Agent{"codex": {Disabled: true}}})
	if got := off.agentName("codex"); got != "Codex" {
		t.Errorf("a disabled agent's name = %q", got)
	}
	if got := off.accountAgents(); got != "Claude Code, Codex and Third Agent" {
		t.Errorf("accountAgents with Codex disabled = %q", got)
	}
	if got := off.BringTarget(inv(thirdagent.ID, "codex"), cloud); got != thirdagent.ID {
		t.Errorf("BringTarget skips a disabled agent: got %q", got)
	}
}
