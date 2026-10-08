package app

import (
	"crypto/sha256"
	"fmt"
	"sort"

	"github.com/roeehrl/hopsesh/internal/core/lineage"
)

// Relationship is presentation metadata. Only verified lineage provides a
// shared family ID; an untracked native copy gets its own namespaced identity.
// It is never an append/merge authority and scanning does not persist it.
type Relationship struct {
	Family     string   `json:"family"`
	Branch     string   `json:"branch"`
	Parent     string   `json:"parent,omitempty"`
	Name       string   `json:"name"`
	BranchName string   `json:"branchName"`
	Depth      int      `json:"depth"`
	Evidence   string   `json:"evidence"`
	Issue      string   `json:"issue,omitempty"`
	Ancestors  []string `json:"ancestors,omitempty"`
	Branches   int      `json:"branches"`
}

func EntryIdentity(machine, key string) string { return machine + "\x00" + key }

func (inv *Inventory) Relationships() map[string]Relationship {
	out := map[string]Relationship{}
	// A missing local receipt can use another copy's replica evidence only when
	// the endpoint, profile and installation binding all match. A native ID alone
	// is never proof that two accounts or machines share a conversation.
	verified := map[string]*lineage.Manifest{}
	conflicts := map[string]bool{}
	for _, e := range inv.Entries {
		if e.Lineage != nil && e.LineageError == "" {
			verified[EntryIdentity(e.Machine, e.Session.Key.String())] = e.Lineage
		}
	}
	replicaIndex := map[string]*lineage.Manifest{}
	replicaConflicts := map[string]bool{}
	replicaKey := func(endpoint, key, binding string) string { return endpoint + "\x00" + key + "\x00" + binding }
	for _, source := range inv.Entries {
		m := source.Lineage
		if m == nil || source.LineageError != "" {
			continue
		}
		for _, r := range m.Replicas {
			if r.Line != m.Branch {
				continue
			}
			k := replicaKey(r.Endpoint, r.Key.String(), r.Binding)
			if old := replicaIndex[k]; old != nil && (old.Family != m.Family || old.Branch != m.Branch) {
				replicaConflicts[k] = true
			}
			replicaIndex[k] = m
		}
	}
	for _, e := range inv.Entries {
		if e.Lineage != nil || e.LineageError != "" {
			continue
		}
		machine := inv.Machine(e.Machine)
		if machine == nil || machine.host == nil || machine.host.Facts.Endpoint == "" {
			continue
		}
		in, ok := machine.InstallProfile(e.Agent, e.Session.Key.Profile)
		if !ok {
			continue
		}
		k := replicaKey(machine.host.Facts.Endpoint, e.Session.Key.String(), in.BindingID())
		id := EntryIdentity(e.Machine, e.Session.Key.String())
		if m := replicaIndex[k]; m != nil {
			verified[id] = m
			conflicts[id] = replicaConflicts[k]
		}
	}
	// Native fork evidence can be discovered without a persisted endpoint. Its
	// read-only labels must not change with each newly reserved transfer identity.
	persisted := map[string]bool{}
	for _, e := range inv.Entries {
		if e.Lineage != nil && !e.NativeRelationship {
			persisted[e.Lineage.Family] = true
		}
	}
	for _, e := range inv.Entries {
		id := EntryIdentity(e.Machine, e.Session.Key.String())
		m := verified[id]
		if m == nil || !e.NativeRelationship || persisted[m.Family] {
			continue
		}
		originKey := func(branch string) string {
			for _, b := range m.Branches {
				if b.ID == branch {
					for _, r := range m.Replicas {
						if r.ID == b.Origin {
							return r.Key.String() + "\x00" + r.Binding
						}
					}
				}
			}
			for _, r := range m.Replicas {
				if r.Line == branch {
					return r.Key.String() + "\x00" + r.Binding
				}
			}
			return branch
		}
		aliases := map[string]string{}
		root := ""
		for _, b := range m.Branches {
			aliases[b.ID] = fmt.Sprintf("native-%x", sha256.Sum256([]byte(e.Machine+"\x00"+originKey(b.ID))))
			if b.Parent == "" {
				root = b.ID
			}
		}
		if root == "" {
			continue
		}
		copy := m.Clone()
		copy.Family = aliases[root]
		copy.Branch = aliases[m.Branch]
		for i := range copy.Branches {
			b := &copy.Branches[i]
			b.ID = aliases[b.ID]
			if b.Parent != "" {
				b.Parent = aliases[b.Parent]
			}
		}
		verified[id] = copy
	}
	badFamilies := map[string]string{}
	parentEvidence := map[string]string{}
	for _, m := range verified {
		for _, b := range m.Branches {
			k := m.Family + "/" + b.ID
			if parent, exists := parentEvidence[k]; exists && parent != b.Parent {
				badFamilies[m.Family] = "Relationship needs review: conflicting parents"
			}
			parentEvidence[k] = b.Parent
		}
	}
	for _, m := range verified {
		for _, b := range m.Branches {
			seen := map[string]bool{}
			for p := b.ID; p != ""; p = parentEvidence[m.Family+"/"+p] {
				if seen[p] {
					badFamilies[m.Family] = "Relationship needs review: cyclic ancestry"
					break
				}
				seen[p] = true
			}
		}
	}
	names := map[string]string{}
	parents := map[string]string{}
	roots := map[string]string{}
	members := map[string]map[string]bool{}
	for _, e := range inv.Entries {
		id := EntryIdentity(e.Machine, e.Session.Key.String())
		title := e.Session.Title
		if title == "" {
			title = e.Session.LastPrompt
		}
		if title == "" {
			title = "Untitled conversation"
		}
		r := Relationship{Name: title, BranchName: title, Evidence: "Separate conversation", Issue: e.LineageError}
		if m := verified[id]; m != nil && m.Family != "" && !conflicts[id] && badFamilies[m.Family] == "" {
			j := m.Journey()
			r.Family, r.Branch, r.Parent = m.Family, m.Branch, j.ParentBranch
			r.Evidence = "Verified conversation lineage"
			if e.NativeRelationship {
				r.Evidence = "Verified native fork prefix"
			}
			for _, b := range m.Branches {
				parents[m.Family+"/"+b.ID] = b.Parent
			}
		} else {
			if conflicts[id] {
				r.Issue = "Relationship needs review: conflicting copy identity"
			}
			if m := verified[id]; m != nil && badFamilies[m.Family] != "" {
				r.Issue = badFamilies[m.Family]
			}
			namespace := id
			if machine := inv.Machine(e.Machine); machine != nil {
				if machine.host != nil && machine.host.Facts.Endpoint != "" {
					namespace = machine.host.Facts.Endpoint + "\x00" + e.Session.Key.String()
				}
				if in, ok := machine.InstallProfile(e.Agent, e.Session.Key.Profile); ok {
					namespace += "\x00" + in.BindingID()
				}
			}
			r.Family = fmt.Sprintf("copy-%x", sha256.Sum256([]byte(namespace)))
			r.Branch = r.Family
		}
		k := r.Family + "/" + r.Branch
		if names[k] == "" || title < names[k] {
			names[k] = title
		}
		if r.Parent == "" && (roots[r.Family] == "" || title < roots[r.Family]) {
			roots[r.Family] = title
		}
		if members[r.Family] == nil {
			members[r.Family] = map[string]bool{}
		}
		members[r.Family][r.Branch] = true
		out[id] = r
	}
	for id, r := range out {
		if roots[r.Family] != "" {
			r.Name = roots[r.Family]
		} else {
			r.Name = "Conversation family (parent unavailable)"
		}
		r.Branches = len(members[r.Family])
		seen := map[string]bool{r.Branch: true}
		for p := r.Parent; p != ""; p = parents[r.Family+"/"+p] {
			if seen[p] {
				r.Issue = "Relationship needs review: cyclic ancestry"
				break
			}
			seen[p] = true
			name := names[r.Family+"/"+p]
			if name == "" {
				name = "Parent unavailable"
			}
			r.Ancestors = append([]string{name}, r.Ancestors...)
			r.Depth++
		}
		out[id] = r
	}
	return out
}

// FamilyGroups uses the same verified relationships as the desktop. Copies
// already combined by Items count once; unrelated titles remain separate.
func (inv *Inventory) FamilyGroups() []Group {
	rel := inv.Relationships()
	by := map[string]*Group{}
	var out []Group
	for _, it := range inv.Items() {
		r := rel[EntryIdentity(it.Entry.Machine, it.Entry.Session.Key.String())]
		g := by[r.Family]
		if g == nil {
			g = &Group{Identity: r.Family, Name: r.Name}
			by[r.Family] = g
		}
		g.Items = append(g.Items, it)
	}
	for _, g := range by {
		sort.SliceStable(g.Items, func(i, j int) bool {
			return rel[EntryIdentity(g.Items[i].Entry.Machine, g.Items[i].Entry.Session.Key.String())].Depth < rel[EntryIdentity(g.Items[j].Entry.Machine, g.Items[j].Entry.Session.Key.String())].Depth
		})
		out = append(out, *g)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
