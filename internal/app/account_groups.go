package app

import (
	"github.com/roeehrl/hopsesh/sdk/agent"
	"sort"
)

// AccountLabel describes the current sign-in for a root, never its historical owner.
func AccountLabel(p *agent.RuntimeProfile) string {
	if p == nil {
		return "No account profile"
	}
	if p.Account != nil && p.Account.Email != "" {
		return p.Account.Email
	}
	return p.Name
}

// AccountGroups groups sessions by the account that ran them (OwnerOf) within each root:
// a root signed in to another account before keeps that account's sessions apart, and
// sessions whose account was not recorded are not filed under the root's sign-in.
func (inv *Inventory) AccountGroups() []Group {
	logins := inv.Logins()
	by := map[string]*Group{}
	for _, e := range inv.Entries {
		o := logins.OwnerOf(e)
		label := OwnerLabel(o)
		if label == "" {
			label = "Account not recorded"
		}
		id := "account:" + e.Machine + ":" + string(e.Agent) + ":" + e.Session.Key.Profile + ":" + label
		g := by[id]
		if g == nil {
			g = &Group{Identity: id, Name: label + " · " + e.AgentName + " · " + e.Machine}
			if e.Profile != nil && (!e.Profile.Default || !o.Current) {
				g.Name += " · in " + e.Profile.Name
			}
			by[id] = g
		}
		g.Items = append(g.Items, Item{Entry: e})
	}
	out := make([]Group, 0, len(by))
	for _, g := range by {
		sort.SliceStable(g.Items, func(i, j int) bool {
			return g.Items[i].Entry.Session.LastActivity.After(g.Items[j].Entry.Session.LastActivity)
		})
		out = append(out, *g)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name == out[j].Name {
			return out[i].Identity < out[j].Identity
		}
		return out[i].Name < out[j].Name
	})
	return out
}
