package move

import (
	"fmt"
	"slices"
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

// seedCheckpointPrefix inherits only an unambiguous contiguous prefix with both
// native anchors and content hashes in the same task endpoint, binding and line.
// A task assertion alone never makes rewritten/compacted records inherited.
func seedCheckpointPrefix(m *lineage.Manifest, source lineage.Replica, seg *ir.Segment) error {
	if source.Line != m.Branch {
		return nil
	}
	id := m.Upsert(source)
	if old, ok := m.LatestState(id); ok && len(old.Projection) > 0 {
		return nil
	}
	known := map[string]ir.Projection{}
	ambiguous := map[string]bool{}
	var priorHeads []ir.NodeID
	for _, replica := range m.Replicas {
		if replica.ID == id || replica.Endpoint != source.Endpoint || replica.Binding != source.Binding || replica.Line != m.Branch || replica.Key.Agent != source.Key.Agent {
			continue
		}
		state, _ := m.LatestState(replica.ID)
		priorHeads = append(priorHeads, state.Heads...)
		for _, p := range state.Projection {
			k := p.Anchor + "/" + p.Hash
			if old, ok := known[k]; ok && (!slices.Equal(old.Coverage, p.Coverage) || old.Generated != p.Generated || !slices.EqualFunc(old.Fragments, p.Fragments, func(a, b ir.Fragment) bool { return a.Text == b.Text && slices.Equal(a.Coverage, b.Coverage) })) {
				ambiguous[k] = true
			}
			known[k] = p
		}
	}
	var prefix []ir.Projection
	for _, node := range seg.Nodes {
		if node.Native == nil || node.Native.Anchor == "" {
			break
		}
		k := node.Native.Anchor + "/" + ir.ContentHash(node)
		p, ok := known[k]
		if !ok || ambiguous[k] {
			break
		}
		prefix = append(prefix, p)
	}
	if len(prefix) != len(known) {
		// Retain historical ancestry for an explicitly requested fork, without
		// claiming that rewritten records represent any of the previous turns.
		m.Deliver(id, ir.Cursor{}, nil, priorHeads, nil)
		return fmt.Errorf("%w: rebuilt cloud task does not preserve its verified native prefix; choose an independent fork", agent.ErrDiverged)
	}
	if len(prefix) > 0 {
		var heads []ir.NodeID
		for _, p := range prefix {
			heads = append(heads, p.Coverage...)
		}
		m.Deliver(id, ir.Cursor{}, prefix, heads, nil)
	}
	return nil
}
