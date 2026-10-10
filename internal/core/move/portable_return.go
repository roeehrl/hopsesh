package move

import (
	"fmt"

	"github.com/roeehrl/hopsesh/internal/core/lineage"
	"github.com/roeehrl/hopsesh/sdk/ir"
)

// portableTargetState compares an original under its currently observed binding.
// The first public account observation (or a later login change) can rotate that
// binding without changing a single native record. ObserveBinding verifies the
// old projection before carrying its authorship into the new binding segment.
// It does not authorize replay of source-private state. A module may separately
// opt into appending ordinary portable text to this exact target; earlier records
// and their historical binding remain unchanged, while new work uses the current
// binding. No email comparison or cross-provider identity claim is involved.
func portableTargetState(p *Plan, in Input, c Copy, seg *ir.Segment) (lineage.State, error) {
	key, endpoint := c.Summary.Key, in.Target.Machine.Facts.Endpoint
	if key.Agent != in.Target.Module.Spec().ID || key.Profile != in.Target.Install.ProfileID() {
		return lineage.State{}, fmt.Errorf("destination session belongs to another agent or profile")
	}
	known := false
	for _, r := range p.manifest.Replicas {
		if r.Key == key && r.Endpoint == endpoint && r.Line == p.manifest.Branch {
			if _, ok := p.manifest.LatestState(r.ID); ok {
				known = true
			}
		}
	}
	if !known {
		return lineage.State{}, fmt.Errorf("destination session has no verified lineage; choose a separate fork")
	}
	_, st, err := p.manifest.ObserveBinding(lineage.Replica{
		Key: key, Endpoint: endpoint, Binding: in.Target.Install.BindingID(),
		Location: in.Target.Machine.Name, AgentVersion: in.Target.Install.Version,
		Time: seg.Header.Created,
	}, seg)
	return st, err
}
