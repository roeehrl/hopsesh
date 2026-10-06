package tui

import (
	"fmt"
	"strings"
)

func (m *model) cycleDestination() bool {
	if len(m.destinations) == 0 {
		return false
	}
	next := 0
	for i, destination := range m.destinations {
		if destination.Key.String() == m.opts.TargetSession {
			next = (i + 1) % len(m.destinations)
			break
		}
	}
	m.opts.TargetSession = m.destinations[next].Key.String()
	return true
}

func (m *model) journeyLines() []string {
	if m.sel.item == nil || m.sel.item.Entry.Lineage == nil {
		return nil
	}
	graph := m.sel.item.Entry.Lineage
	journey := graph.Journey()
	lines := []string{"Last seen lineage for " + m.sel.item.Entry.Session.Title,
		"Branch: " + journey.Branch, "Family: " + journey.Family,
		fmt.Sprintf("Origin: %s · %d transfers · %d round trips to origin · %d returns", journey.Origin, journey.Transfers, journey.RoundTrips, journey.Returns)}
	if journey.Fork {
		lines = append(lines, "Fork of branch: "+journey.ParentBranch)
	}
	undone := map[string]bool{}
	for _, compensation := range graph.Compensations {
		undone[compensation.Operation] = true
	}
	for _, hop := range graph.OrderedHops() {
		if hop.Line != graph.Branch {
			continue
		}
		from, to := graph.Replica(hop.From), graph.Replica(hop.To)
		status := string(hop.Kind)
		if undone[hop.ID] {
			status += " · undone"
		}
		if hop.Backup {
			status += " · native backup"
		}
		lines = append(lines, fmt.Sprintf("%s/%s → %s/%s · %s", from.Location, from.Key.Agent, to.Location, to.Key.Agent, status), "  Operation: "+hop.ID)
		if state := graph.State(hop.Target); len(state.Loss) > 0 {
			lines = append(lines, "  Fidelity: "+strings.Join(state.Loss, "; "))
		}
	}
	return lines
}

func (m *model) viewJourney(b *strings.Builder) {
	lines := m.journeyLines()
	height := max(1, m.height-5)
	m.journeyOffset = min(m.journeyOffset, max(0, len(lines)-height))
	fmt.Fprintln(b, "\n  Session journey")
	for _, line := range lines[m.journeyOffset:min(len(lines), m.journeyOffset+height)] {
		fmt.Fprintf(b, "  %s\n", truncate(line, max(1, m.width-4)))
	}
	fmt.Fprintf(b, "\n  ↑↓/PgUp/PgDn scroll · esc/h back · %d–%d of %d\n", m.journeyOffset+1, min(len(lines), m.journeyOffset+height), len(lines))
}
