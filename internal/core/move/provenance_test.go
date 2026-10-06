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
