package codex

import (
	"fmt"

	"github.com/roeehrl/hopsesh/sdk/agent"
)

// Codex 0.160.1's rollout ordinal.rs resumes with the final durable ordinal + 1.
// A standalone paginated rollout starts at zero. A by-reference fork/revert also
// needs its inherited prefix; it cannot be treated as a complete local transcript.
func paginatedNext(mt meta, records []line) (*uint64, error) {
	if mt.HistoryMode != "paginated" {
		return nil, nil
	}
	if mt.HistoryBase != nil {
		return nil, fmt.Errorf("%w: Codex inherited paginated history is not materialized; cannot verify the complete conversation", agent.ErrUnsupported)
	}
	for i, r := range records {
		if r.Ordinal == nil || *r.Ordinal != uint64(i) {
			return nil, fmt.Errorf("%w: Codex paginated ordinal at record %d is missing or discontinuous", agent.ErrDiverged, i)
		}
	}
	next := uint64(len(records))
	if mt.SubagentHistoryStartOrdinal != nil && *mt.SubagentHistoryStartOrdinal > next {
		return nil, fmt.Errorf("%w: Codex subagent inherited prefix is incomplete", agent.ErrDiverged)
	}
	return &next, nil
}
