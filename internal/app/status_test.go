package app

import (
	"testing"

	"github.com/roeehrl/hopsesh/internal/core/lineage"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

// A copy's movement status comes from lineage, never from its title. A label an older
// hopsesh wrote is used only for a copy without lineage.
func TestStatusComesFromLineage(t *testing.T) {
	ended := Entry{Agent: "claude", Live: agent.LiveInfo{State: agent.Ended}}
	withLineage := func(e Entry) Entry { e.Lineage = lineage.New("status"); return e }
	for _, c := range []struct {
		name string
		e    Entry
		want string
	}{
		{"no movement", withLineage(ended), "ended"},
		{"moved", withDeparture(withLineage(ended), MovementNotice{Status: "prepared", Agent: "claude", AgentName: "Claude Code", Machine: "box"}), "moved to box"},
		{"moved and continued there", withDeparture(withLineage(ended), MovementNotice{Status: "continued", Agent: "claude", Machine: "box"}), "moved to box"},
		{"prepared in another agent", withDeparture(withLineage(ended), MovementNotice{Status: "prepared", Agent: "codex", AgentName: "Codex", Machine: "studio"}), "prepared in Codex on studio"},
		{"continued in another agent", withDeparture(withLineage(ended), MovementNotice{Status: "continued", Agent: "codex", AgentName: "Codex", Machine: "studio"}), "continued in Codex on studio"},
		{"handed off to a cloud", withDeparture(withLineage(ended), MovementNotice{Status: "prepared", Agent: "claude", Machine: "claude-cloud", Cloud: "Claude Code cloud"}), "continued in Claude Code cloud"},
		{"live wins", withDeparture(withLineage(Entry{Agent: "claude", Live: agent.LiveInfo{State: agent.Live, Status: "idle"}}), MovementNotice{Agent: "claude", Machine: "box"}), "live idle"},
		// A return cleared the departure; an old label left in the title is not believed.
		{"returned, old label", withLabel(withLineage(ended), agent.LegacyLabel{Kind: agent.LabelMoved, Location: "box"}), "ended"},
		{"no lineage, old label", withLabel(ended, agent.LegacyLabel{Kind: agent.LabelContinued, AgentName: "Codex", Location: "studio"}), "continued in Codex on studio"},
		{"no lineage, old moved label", withLabel(ended, agent.LegacyLabel{Kind: agent.LabelMoved, Location: "box"}), "moved to box"},
	} {
		if got := c.e.Status(); got != c.want {
			t.Errorf("%s: Status() = %q, want %q", c.name, got, c.want)
		}
		if c.e.Live.State != agent.Live && (c.want != "ended") != c.e.MovedOn() {
			t.Errorf("%s: MovedOn() = %t", c.name, c.e.MovedOn())
		}
	}
}

func withDeparture(e Entry, d MovementNotice) Entry { e.Departure = &d; return e }
func withLabel(e Entry, l agent.LegacyLabel) Entry  { e.Session.LegacyLabel = &l; return e }
