package app

import (
	"testing"

	"github.com/roeehrl/hopsesh/internal/agents/all"
	"github.com/roeehrl/hopsesh/internal/config"
	"github.com/roeehrl/hopsesh/internal/core/lineage"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

// A cloud-only module's session goes into the agent it was handed off from, when that agent
// is here; else Claude Code; else any agent here that takes sessions; else none.
func TestBringTarget(t *testing.T) {
	a := New(config.Config{}, all.Registry(), t.TempDir(), nil)
	inv := func(ids ...agent.ID) *Inventory {
		m := &Machine{Name: "here", Local: true}
		for _, id := range ids {
			m.Agents = append(m.Agents, AgentState{Agent: id, Install: agent.Install{Agent: id, Present: true}})
		}
		return &Inventory{Machines: []*Machine{m}}
	}
	thread := agent.SessionKey{Agent: "amp", Session: "T-0b6c6a8e-1d2f-4c3b-9a7e-5f4d3c2b1a01"}
	l := lineage.New("lin-1")
	from := l.Upsert(lineage.Replica{Key: agent.SessionKey{Agent: "codex", Session: "019a-local"}, Location: "here"})
	to := l.Upsert(lineage.Replica{Key: thread, Location: "amp"})
	l.AppendHop(lineage.Hop{From: from, To: to, Kind: lineage.HopHandoff})
	e := Entry{Location: agent.CloudLocation("amp"), Machine: "amp", Agent: "amp", Session: agent.Summary{Key: thread}, Lineage: l}
	for _, c := range []struct {
		inv  *Inventory
		e    Entry
		want agent.ID
	}{
		{inv("claude", "codex"), e, "codex"}, // handed off from Codex
		{inv("claude"), e, "claude"},         // Codex is not here
		{inv("claude", "codex"), Entry{Location: e.Location, Session: e.Session}, "claude"}, // no lineage
		{inv("codex"), Entry{Location: e.Location, Session: e.Session}, "codex"},
		{inv(), e, ""},
	} {
		if got := a.BringTarget(c.inv, c.e); got != c.want {
			t.Errorf("BringTarget = %q, want %q", got, c.want)
		}
	}
}
