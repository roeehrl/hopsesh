package move

import (
	"github.com/roeehrl/hopsesh/sdk/ir"
	"strings"
	"testing"
)

func TestMissingNodesKeepsOnlyUnreceivedFragments(t *testing.T) {
	n := ir.Node{Kind: ir.KindMessage, Actor: ir.User, Text: "old and new coalesced", Coverage: []ir.NodeID{"old", "new"}, Fragments: []ir.Fragment{{Text: "already received", Coverage: []ir.NodeID{"old"}}, {Text: "new delta", Coverage: []ir.NodeID{"new"}}}}
	got := missingNodes([]ir.Node{n, {Generated: true, Text: "old generated briefing"}}, map[ir.NodeID]bool{"old": true})
	if len(got) != 1 || got[0].Text != "new delta" || len(got[0].Coverage) != 1 || got[0].Coverage[0] != "new" {
		t.Fatalf("incorrect return delta: %+v", got)
	}
	n.Fragments = []ir.Fragment{{Text: "summarized old plus new", Coverage: []ir.NodeID{"old", "new"}}}
	got = missingNodes([]ir.Node{n}, map[ir.NodeID]bool{"old": true})
	if len(got) != 1 || !strings.Contains(got[0].Text, "new") || len(got[0].Coverage) != 2 {
		t.Fatal("indivisible summary lost unreceived revisions")
	}
	if got = missingNodes([]ir.Node{n}, map[ir.NodeID]bool{"old": true, "new": true}); len(got) != 0 {
		t.Fatal("already received summary was imported twice")
	}
}

// A copy whose agent gave its records new ids maps to the source node for node, only while
// every node has the source's content in the source's order.
func TestRenamedProjection(t *testing.T) {
	node := func(anchor, text string) ir.Node {
		return ir.Node{Kind: ir.KindMessage, Actor: ir.User, Text: text, Native: &ir.Native{Format: "x", Anchor: anchor}}
	}
	src := ir.Segment{Nodes: []ir.Node{node("msg_a", "one"), node("msg_b", "two")}}
	ir.Chain(src.Nodes, "")
	copied := ir.Segment{Nodes: []ir.Node{node("msg_a2", "one"), node("msg_b2", "two")}}
	ir.Chain(copied.Nodes, "")
	ps, err := nativeProjection(src, copied)
	if err != nil || len(ps) != 2 || ps[1].Anchor != "msg_b2" {
		t.Fatalf("%+v %v", ps, err)
	}
	changed := ir.Segment{Nodes: []ir.Node{node("msg_a2", "one"), node("msg_b2", "other")}}
	ir.Chain(changed.Nodes, "")
	if _, err := nativeProjection(src, changed); err == nil {
		t.Fatal("a copy with other content was mapped")
	}
	if _, err := nativeProjection(src, ir.Segment{Nodes: copied.Nodes[:1]}); err == nil {
		t.Fatal("a shorter copy was mapped")
	}
}
