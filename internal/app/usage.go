package app

import (
	"fmt"
	"sort"

	"github.com/roeehrl/hopsesh/sdk/agent"
)

// AgentUsage is how much one agent's sessions take on one machine: the session files its
// listing sized (each copy where it is).
type AgentUsage struct {
	Agent    agent.ID `json:"agent"`
	Name     string   `json:"name"`
	Sessions int      `json:"sessions"`
	Bytes    int64    `json:"bytes"`
}

// Usage is what each agent's sessions on machine take, largest first.
func (inv *Inventory) Usage(machine string) []AgentUsage {
	by := map[agent.ID]*AgentUsage{}
	out := []AgentUsage{}
	for _, e := range inv.Entries {
		if e.Machine != machine || e.Location.IsCloud() {
			continue
		}
		u := by[e.Agent]
		if u == nil {
			u = &AgentUsage{Agent: e.Agent, Name: e.AgentName}
			by[e.Agent] = u
		}
		u.Sessions++
		u.Bytes += e.Session.Size
	}
	for _, u := range by {
		out = append(out, *u)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Bytes != out[j].Bytes {
			return out[i].Bytes > out[j].Bytes
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// HumanBytes is n as people read a file size: 812 KB, 3.4 MB, 9.8 GB.
func HumanBytes(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%d KB", (n+512)>>10)
	}
	return fmt.Sprintf("%d B", n)
}
