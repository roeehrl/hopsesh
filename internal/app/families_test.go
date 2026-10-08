package app

import (
	"strings"
	"testing"

	"github.com/roeehrl/hopsesh/internal/core/lineage"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

func TestFamiliesSeparateIdentityFromTitleAndCopies(t *testing.T) {
	key := agent.SessionKey{Agent: "claude", Session: "same-id"}
	inv := &Inventory{Entries: []Entry{{Machine: "A", Session: agent.Summary{Key: key, Title: "Same title"}}, {Machine: "B", Session: agent.Summary{Key: key, Title: "Same title"}}}}
	r := inv.Relationships()
	if r[EntryIdentity("A", key.String())].Family == r[EntryIdentity("B", key.String())].Family {
		t.Fatal("native IDs on different endpoints established ancestry")
	}
	if len(inv.Items()) != 2 || len(inv.FamilyGroups()) != 2 {
		t.Fatal("display grouping merged unrelated native IDs")
	}
	root := &lineage.Manifest{Family: "f", Branch: "root", Branches: []lineage.Branch{{ID: "root"}, {ID: "child", Parent: "root"}, {ID: "grandchild", Parent: "child"}}}
	child := root.Clone()
	child.Branch = "child"
	grand := root.Clone()
	grand.Branch = "grandchild"
	inv.Entries[0].Lineage = root
	inv.Entries[1].Lineage = root.Clone()
	inv.Entries = append(inv.Entries, Entry{Machine: "A", Session: agent.Summary{Key: agent.SessionKey{Agent: "codex", Session: "child"}, Title: "Experiment"}, Lineage: child}, Entry{Machine: "C", Session: agent.Summary{Key: agent.SessionKey{Agent: "codex", Session: "grand"}, Title: "Nested"}, Lineage: grand})
	r = inv.Relationships()
	nested := r[EntryIdentity("C", "codex/grand")]
	if nested.Branches != 3 || nested.Depth != 2 || len(nested.Ancestors) != 2 {
		t.Fatalf("copies inflated branches or nested ancestry lost: %+v", nested)
	}
	if len(inv.FamilyGroups()) != 1 {
		t.Fatal("verified family separated")
	}
}

func TestFamiliesRejectConflictsCyclesAndKeepMissingParentExplicit(t *testing.T) {
	for _, tc := range []struct {
		name    string
		parents []lineage.Branch
		other   []lineage.Branch
		issue   string
	}{
		{"cycle", []lineage.Branch{{ID: "one", Parent: "two"}, {ID: "two", Parent: "one"}}, nil, "cyclic"},
		{"conflict", []lineage.Branch{{ID: "one"}, {ID: "two", Parent: "one"}}, []lineage.Branch{{ID: "two", Parent: "elsewhere"}}, "conflicting"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := &lineage.Manifest{Family: "f", Branch: "one", Branches: tc.parents}
			b := a.Clone()
			b.Branch = "two"
			if tc.other != nil {
				b.Branches = tc.other
			}
			inv := &Inventory{Entries: []Entry{{Machine: "A", Session: agent.Summary{Key: agent.SessionKey{Agent: "claude", Session: "1"}}, Lineage: a}, {Machine: "A", Session: agent.Summary{Key: agent.SessionKey{Agent: "claude", Session: "2"}}, Lineage: b}}}
			if len(inv.FamilyGroups()) != 2 {
				t.Fatal("ambiguous ancestry silently merged")
			}
			for _, r := range inv.Relationships() {
				if !strings.Contains(r.Issue, tc.issue) {
					t.Fatalf("missing issue: %+v", r)
				}
			}
		})
	}
	inv := &Inventory{Entries: []Entry{{Machine: "A", Session: agent.Summary{Key: agent.SessionKey{Agent: "claude", Session: "1"}}, Lineage: &lineage.Manifest{Family: "f", Branch: "child", Branches: []lineage.Branch{{ID: "child", Parent: "absent"}}}}}}
	r := inv.Relationships()[EntryIdentity("A", "claude/1")]
	if len(r.Ancestors) != 1 || r.Ancestors[0] != "Parent unavailable" {
		t.Fatalf("invented missing parent: %+v", r)
	}
}
