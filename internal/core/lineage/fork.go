package lineage

import (
	"fmt"
	"github.com/roeehrl/hopsesh/sdk/agent"
	"github.com/roeehrl/hopsesh/sdk/ir"
	"slices"
)

// AdoptNativeFork requires module-verified native inheritance. It returns a new branch
// view without modifying the parent's manifest or its native session.
func (m *Manifest) AdoptNativeFork(parent ReplicaID, child Replica, proof []agent.NativeInheritance, seg *ir.Segment) (*Manifest, error) {
	out := m.Clone()
	known := map[string]ir.Projection{}
	for _, st := range out.States {
		if st.Replica != parent {
			continue
		}
		for _, p := range st.Projection {
			if old, ok := known[p.Anchor+"/"+p.Hash]; ok && old.Hash == p.Hash && !slices.Equal(old.Coverage, p.Coverage) {
				return nil, fmt.Errorf("ambiguous native fork projection")
			}
			known[p.Anchor+"/"+p.Hash] = p
		}
	}
	var projected []ir.Projection
	var heads []ir.NodeID
	for _, edge := range proof {
		p, ok := known[edge.ParentAnchor+"/"+edge.Hash]
		if !ok || p.Hash != edge.Hash {
			return nil, fmt.Errorf("native fork prefix has no matching parent receipt")
		}
		p.Anchor = edge.ChildAnchor
		projected = append(projected, p)
		heads = append(heads, p.Coverage...)
	}
	child.Line = out.Fork("native/"+child.Endpoint+"/"+child.Key.String(), out.Heads(heads))
	out.Branch = child.Line
	id := out.Upsert(child)
	parentState, _ := out.LatestState(parent)
	out.ReplaceProjection(id, ir.Cursor{}, projected, out.Heads(heads), slices.Clone(parentState.Loss))
	if _, err := out.Observe(id, seg); err != nil {
		return nil, err
	}
	return out, out.Validate()
}
