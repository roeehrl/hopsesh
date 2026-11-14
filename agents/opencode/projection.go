package opencode

import (
	"fmt"

	"github.com/roeehrl/hopsesh/sdk/agent"
	"github.com/roeehrl/hopsesh/sdk/ir"
)

// ProjectSanitized maps a sanitized copy's nodes to the source's: the sanitizer drops
// reasoning items only and keeps everything else in order, so the copy is the source
// without its reasoning nodes.
func (m *Module) ProjectSanitized(source, target ir.Segment) ([]ir.Projection, error) {
	var retained []ir.Node
	for _, n := range source.Nodes {
		if n.Kind != ir.KindReasoning {
			retained = append(retained, n)
		}
	}
	if len(retained) != len(target.Nodes) {
		return nil, fmt.Errorf("%w: the sanitized OpenCode session has %d nodes, the source %d without its reasoning", agent.ErrDiverged, len(target.Nodes), len(retained))
	}
	out := make([]ir.Projection, 0, len(target.Nodes))
	for i, n := range target.Nodes {
		src := retained[i]
		if n.Kind != src.Kind || n.Actor != src.Actor || n.Native == nil {
			return nil, fmt.Errorf("%w: the sanitizer reordered the OpenCode session", agent.ErrDiverged)
		}
		out = append(out, ir.Projection{Anchor: n.Native.Anchor, Hash: ir.ContentHash(n), Coverage: src.Coverage, Generated: src.Generated, Fragments: src.Fragments, Fidelity: "native"})
	}
	return out, nil
}
