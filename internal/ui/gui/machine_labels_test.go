package gui

import (
	"slices"
	"testing"
	"time"

	"github.com/roeehrl/hopsesh/internal/agents/all"
	"github.com/roeehrl/hopsesh/internal/app"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

func TestMachineSubtitlesUseDetectedAgentsWithoutSessions(t *testing.T) {
	home(t)
	a := NewApp(all.Registry())
	states := []app.AgentState{
		{Agent: "codex", Name: "Codex", Install: agent.Install{Version: "0.160.1", Binary: "/bin/codex", Roots: map[string]string{"data": "/codex"}}},
		{Agent: "claude", Name: "Claude Code", Install: agent.Install{Version: "2.1.288", Present: true, Roots: map[string]string{"data": "/claude"}}},
		{Agent: "claude", Name: "Claude Code", Install: agent.Install{Present: true, Roots: map[string]string{"data": "/claude-work"}}},
		{Agent: "amp", Name: "Amp", Install: agent.Install{Present: true}}, // a cloud driver is not a local agent
	}
	inv := &app.Inventory{Machines: []*app.Machine{
		{Name: "here", Local: true, Status: app.StatusOK, Agents: states},
		{Name: "other-mac", OS: "darwin", Status: app.StatusOK, Agents: states},
		{Name: "empty", Status: app.StatusOK},
	}}
	dto := scanDTO(a.core, inv, time.Now(), time.Now())
	for _, m := range dto.Machines {
		if m.Sessions != 0 {
			t.Fatal("fixture unexpectedly contains sessions")
		}
		if m.Name == "empty" {
			if m.AgentNames == nil || len(m.AgentNames) != 0 {
				t.Fatal("empty discovery must remain an explicit empty list")
			}
			continue
		}
		if !slices.Equal(m.AgentNames, []string{"Claude Code", "Codex"}) {
			t.Fatalf("%s has inconsistent/duplicated agent labels: %v", m.Name, m.AgentNames)
		}
		if !slices.Equal(m.Agents, []string{"Codex 0.160.1", "Claude Code 2.1.288"}) {
			t.Fatalf("%s lost detailed version evidence: %v", m.Name, m.Agents)
		}
	}
}
