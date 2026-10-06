package move

import (
	"fmt"
	"strings"

	"github.com/roeehrl/hopsesh/internal/core/lineage"
	"github.com/roeehrl/hopsesh/sdk/agent"
	"github.com/roeehrl/hopsesh/sdk/ir"
)

func cloudReplica(m *lineage.Manifest, key agent.SessionKey, cloud, account string) lineage.ReplicaID {
	if _, id, ok := m.Find(key, cloud); ok {
		return id
	}
	return m.Upsert(lineage.Replica{Key: key, Endpoint: cloudEndpoint(cloud, account), Location: cloud})
}

// A cloud's prompt is generated only when the owning handoff saved its exact value.
// Imported native records require both module anchors and content hashes from that
// handoff's source receipt. Similar text alone never establishes inheritance.
func seedCloudPrefix(m *lineage.Manifest, id lineage.ReplicaID, seg *ir.Segment, brief string) error {
	old, _ := m.LatestState(id)
	known := map[string]bool{}
	for _, p := range old.Projection {
		known[p.Anchor] = true
	}
	inherited := map[string]ir.Projection{}
	for _, hop := range m.OrderedHops() {
		if hop.To != id || hop.Kind != lineage.HopHandoff {
			continue
		}
		for _, p := range m.State(hop.Source).Projection {
			inherited[p.Anchor+"/"+p.Hash] = p
		}
	}
	var ps []ir.Projection
	for i, n := range seg.Nodes {
		anchor := fmt.Sprintf("node:%d", i)
		if n.Native != nil && n.Native.Anchor != "" {
			anchor = n.Native.Anchor
		}
		if known[anchor] {
			continue
		}
		if p, ok := inherited[anchor+"/"+ir.ContentHash(n)]; ok {
			ps = append(ps, p)
			continue
		}
		if i == 0 && brief != "" && n.Actor == ir.User && n.Kind == ir.KindMessage && strings.TrimSpace(n.Text) == strings.TrimSpace(brief) {
			ps = append(ps, ir.Projection{Anchor: anchor, Hash: ir.ContentHash(n), Coverage: old.Heads, Generated: true, Fidelity: "handoff briefing"})
		}
	}
	if len(ps) > 0 {
		m.Deliver(id, ir.Cursor{Head: old.Head, Offset: old.Offset}, ps, old.Heads, old.Loss)
	}
	return nil
}

func missingNodes(nodes []ir.Node, covered map[ir.NodeID]bool) []ir.Node {
	var out []ir.Node
	for _, n := range nodes {
		if n.Generated {
			continue
		}
		have := len(n.Coverage) > 0
		for _, id := range n.Coverage {
			if !covered[id] {
				have = false
			}
		}
		if have {
			continue
		}
		if len(n.Fragments) > 0 {
			var texts []string
			var cv []ir.NodeID
			var parts []ir.Fragment
			for _, p := range n.Fragments {
				have := len(p.Coverage) > 0
				for _, id := range p.Coverage {
					if !covered[id] {
						have = false
					}
				}
				if !have {
					texts = append(texts, p.Text)
					cv = append(cv, p.Coverage...)
					parts = append(parts, p)
				}
			}
			n.Text = strings.Join(texts, "\n\n")
			n.Coverage = cv
			n.Fragments = parts
		}
		out = append(out, n)
	}
	return out
}
