package lineage

import (
	"github.com/roeehrl/hopsesh/sdk/agent"
	"github.com/roeehrl/hopsesh/sdk/ir"
	"testing"
)

func TestAccountBindingSegmentsPreserveAuthorship(t *testing.T) {
	m := New("accounts")
	r := Replica{Endpoint: "one-machine", Key: agent.SessionKey{Agent: "claude", Profile: "personal", Session: "same"}, Binding: "login-a"}
	a := m.Upsert(r)
	observeText(t, m, a, "old work")
	original := m.Revisions[0]
	r.Binding = "login-b"
	seg := ir.Segment{Nodes: []ir.Node{{Kind: ir.KindMessage, Actor: ir.User, Text: "old work", Native: &ir.Native{Anchor: "a"}}, {Kind: ir.KindMessage, Actor: ir.User, Text: "new work", Native: &ir.Native{Anchor: "b"}}}}
	ir.Chain(seg.Nodes, "")
	seg.Cursor.Head = seg.Nodes[1].ID
	b, st, err := m.ObserveBinding(r, &seg)
	if err != nil {
		t.Fatal(err)
	}
	if a == b || len(m.Revisions) != 2 || len(m.Covered(st.Heads)) != 2 {
		t.Fatalf("login segmentation duplicated history: %v", m.Revisions)
	}
	found := false
	for _, rev := range m.Revisions {
		if rev.ID == original.ID {
			found = true
			if rev.Replica != a {
				t.Fatal("old work was attributed to new login")
			}
		}
	}
	if !found {
		t.Fatal("lost original work")
	}
	r.Binding = "login-c"
	seg.Nodes[0].Text = "rewritten old work"
	ir.Chain(seg.Nodes, "")
	if _, _, err = m.ObserveBinding(r, &seg); err == nil {
		t.Fatal("changed native history inherited old coverage")
	}
}
