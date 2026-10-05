package app

import (
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/roeehrl/hopsesh/internal/core/repos"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

// Copy is one copy of a session that exists in several places or agents.
type Copy struct {
	Machine    string           `json:"machine"`
	Agent      agent.ID         `json:"agent"`
	AgentName  string           `json:"agentName"`
	Key        agent.SessionKey `json:"key"`
	Local      bool             `json:"local,omitempty"`
	LastActive time.Time        `json:"lastActive"`
	Mark       *agent.Mark      `json:"mark,omitempty"`
	Live       bool             `json:"live,omitempty"`
	Newest     bool             `json:"newest,omitempty"`
}

// Item is one session, shown as its newest copy, with every copy when there are several.
type Item struct {
	Entry  Entry  `json:"entry"`
	Copies []Copy `json:"copies,omitempty"`
}

// Group is sessions of one repository.
type Group struct {
	Identity string `json:"identity"`
	Name     string `json:"name"`
	Remote   string `json:"remote,omitempty"`
	Local    string `json:"localCheckout,omitempty"` // a checkout on this machine
	Items    []Item `json:"items"`
}

// keptWorking is how much newer a copy marked as left behind must be than the newest
// unmarked copy before it counts as the newest again (someone kept working on it; a short
// reply to the move notice does not count).
const keptWorking = 10 * time.Minute

// Items merges copies of the same session: by lineage (across agents and places) or by
// key (one agent, several places).
func (inv *Inventory) Items() []Item {
	local := map[string]bool{}
	for _, m := range inv.Machines {
		local[m.Name] = m.Local
	}
	id := func(e Entry) string {
		if e.Location.IsCloud() {
			// A cloud session is a row of its own, next to its relatives here.
			return "C:" + e.Location.Name + ":" + e.Session.Key.String()
		}
		if e.Lineage != nil && e.Lineage.Logical != "" {
			return "L:" + e.Lineage.Logical
		}
		return "K:" + e.Session.Key.String()
	}
	by := map[string][]Entry{}
	var order []string
	for _, e := range inv.Entries {
		k := id(e)
		if _, seen := by[k]; !seen {
			order = append(order, k)
		}
		by[k] = append(by[k], e)
	}
	// Sessions keyed alone that a lineage group also contains join that group.
	for _, k := range order {
		if !strings.HasPrefix(k, "L:") {
			continue
		}
		for _, e := range by[k] {
			for _, r := range e.Lineage.Replicas {
				alone := "K:" + r.Key.String()
				if es, ok := by[alone]; ok {
					by[k] = append(by[k], es...)
					delete(by, alone)
				}
			}
		}
	}
	var out []Item
	for _, k := range order {
		es, ok := by[k]
		if !ok {
			continue
		}
		cs := make([]Copy, len(es))
		for i, e := range es {
			cs[i] = Copy{Machine: e.Machine, Agent: e.Agent, AgentName: e.AgentName, Key: e.Session.Key, Local: local[e.Machine],
				LastActive: e.Session.LastActivity, Mark: e.Session.Mark, Live: e.Live.State == agent.Live}
		}
		pick := newest(cs)
		it := Item{Entry: es[pick]}
		if len(cs) > 1 {
			cs[pick].Newest = true
			it.Copies = cs
		}
		out = append(out, it)
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].Entry.Session.LastActivity.After(out[j].Entry.Session.LastActivity)
	})
	return out
}

// newest picks the copy to show and continue: the most recently active copy that was not
// left behind, unless a left-behind copy kept being used well after that.
func newest(cs []Copy) int {
	best, bestLeft := -1, -1
	for i, c := range cs {
		if c.Mark == nil {
			if best < 0 || c.LastActive.After(cs[best].LastActive) || (c.LastActive.Equal(cs[best].LastActive) && c.Live) {
				best = i
			}
		} else if bestLeft < 0 || c.LastActive.After(cs[bestLeft].LastActive) {
			bestLeft = i
		}
	}
	switch {
	case best < 0:
		return bestLeft
	case bestLeft >= 0 && cs[bestLeft].LastActive.Sub(cs[best].LastActive) > keptWorking:
		return bestLeft
	}
	return best
}

// Groups arranges sessions by repository; sessions outside git are in a group with no
// identity. localRoots are searched for checkouts on this machine.
func (inv *Inventory) Groups(localRoots []string) []Group {
	idx := map[string]int{}
	var groups []Group
	for _, it := range inv.Items() {
		e := it.Entry
		id, remote, name := "", "", "No repository"
		if g := e.Git; g != nil && g.Identity != "" {
			id, remote, name = g.Identity, g.Remote, repos.Name(g.Identity)
		} else if g != nil && g.IsRepo {
			id, name = "local:"+e.Machine+":"+g.Toplevel, filepath.Base(g.Toplevel)+" (no remote)"
		}
		gi, ok := idx[id]
		if !ok {
			g := Group{Identity: id, Name: name, Remote: remote}
			if id != "" && !strings.HasPrefix(id, "local:") {
				if found := repos.FindLocal(id, localRoots); len(found) > 0 {
					g.Local = found[0].Path
				}
			}
			groups = append(groups, g)
			gi = len(groups) - 1
			idx[id] = gi
		}
		groups[gi].Items = append(groups[gi].Items, it)
		if groups[gi].Remote == "" && remote != "" {
			groups[gi].Remote = remote // a cloud session first knows the repository, not its remote
		}
	}
	// A checkout outside the roots that a session here works in is a checkout here too.
	for gi, g := range groups {
		if g.Local != "" || g.Identity == "" || strings.HasPrefix(g.Identity, "local:") {
			continue
		}
		for _, it := range g.Items {
			e := it.Entry
			if m := inv.Machine(e.Machine); m != nil && m.Local && e.Git != nil && e.Git.Identity == g.Identity {
				if dir := nonEmpty(e.Git.MainWorktree, e.Git.Toplevel); dir != "" {
					groups[gi].Local = dir
					break
				}
			}
		}
	}
	sort.SliceStable(groups, func(a, b int) bool {
		if (groups[a].Identity == "") != (groups[b].Identity == "") {
			return groups[b].Identity == ""
		}
		return groups[a].Items[0].Entry.Session.LastActivity.After(groups[b].Items[0].Entry.Session.LastActivity)
	})
	return groups
}
