package codex

import (
	"fmt"
	"github.com/roeehrl/hopsesh/sdk/agent"
	"github.com/roeehrl/hopsesh/sdk/ir"
)

// ProjectSanitized maps the sanitizer, which drops whole reasoning and compaction records, retaining every other record
// in order. Reader lifting preserves that order, so this mapping uses the exact drop
// policy, not text similarity or wall-clock ancestry guesses.
func (m *Module) ProjectSanitized(source, target ir.Segment) ([]ir.Projection, error) {
	var retained []ir.Node
	for _, n := range source.Nodes {
		if n.Kind != ir.KindReasoning && n.Kind != ir.KindCompaction {
			retained = append(retained, n)
		}
	}
	if len(retained) != len(target.Nodes) {
		return nil, fmt.Errorf("%w: sanitized native shape changed", agent.ErrDiverged)
	}
	var out []ir.Projection
	for i, n := range target.Nodes {
		src := retained[i]
		if n.Kind != src.Kind || n.Actor != src.Actor || n.Native == nil {
			return nil, fmt.Errorf("%w: sanitizer reordered native nodes", agent.ErrDiverged)
		}
		out = append(out, ir.Projection{Anchor: n.Native.Anchor, Hash: ir.ContentHash(n), Coverage: src.Coverage, Generated: src.Generated, Fragments: src.Fragments, Fidelity: "native"})
	}
	return out, nil
}
