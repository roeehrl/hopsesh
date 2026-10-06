package lineage

import (
	"fmt"
	"github.com/roeehrl/hopsesh/sdk/agent"
	"github.com/roeehrl/hopsesh/sdk/ir"
)

// ObserveBinding starts a new replica segment when the login bound to a root changes.
// Native anchors verify inherited history; old revisions retain their original author.
// This never grants permission to replay protected native data across accounts.
func (m *Manifest) ObserveBinding(r Replica, seg *ir.Segment) (ReplicaID, State, error) {
	id := m.Upsert(r)
	if _, exists := m.LatestState(id); !exists {
		var candidates []State
		for _, old := range m.Replicas {
			if old.ID == id || old.Endpoint != r.Endpoint || old.Key != r.Key || old.Line != m.Replica(id).Line {
				continue
			}
			if st, ok := m.LatestState(old.ID); ok {
				candidates = append(candidates, st)
			} else if len(m.stateTips(old.ID)) > 1 {
				return id, State{}, fmt.Errorf("%w: concurrent account receipts", agent.ErrDiverged)
			}
		}
		if len(candidates) > 0 {
			var previous *State
			for i, c := range candidates {
				dominates := true
				for _, other := range candidates {
					if !Subset(m.Covered(other.Heads), m.Covered(c.Heads)) || len(c.Projection) < len(other.Projection) {
						dominates = false
						break
					}
				}
				if dominates {
					previous = &candidates[i]
					break
				}
			}
			if previous == nil {
				return id, State{}, fmt.Errorf("%w: login changed with divergent account receipts; create a separate fork", agent.ErrDiverged)
			}
			m.ReplaceProjection(id, ir.Cursor{Head: previous.Head, Offset: previous.Offset}, previous.Projection, previous.Heads, previous.Loss)
		}
	}
	state, err := m.Observe(id, seg)
	return id, state, err
}
