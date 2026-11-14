package codex

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/roeehrl/hopsesh/sdk/agent"
)

// Codex 0.160.1's rollout ordinal.rs resumes with the final durable ordinal + 1.
// A standalone paginated rollout starts at zero. A by-reference fork/revert also
// needs its inherited prefix (withInherited): only with it is it a complete transcript.
func paginatedNext(mt meta, records []line) (*uint64, error) {
	if mt.HistoryMode != "paginated" {
		return nil, nil
	}
	if mt.HistoryBase != nil && (len(records) == 0 || records[0].Ordinal == nil || *records[0].Ordinal != 0) {
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

// maxInherited bounds a chain of forks of forks.
const maxInherited = 16

// withInherited puts in front of a by-reference fork's own records (records, whose
// session_meta is mt) the history it inherits: its base thread's rollout up to
// history_base.end_byte_offset, which must hold exactly end_ordinal_exclusive records,
// the base's own inherited history first when the base is a fork too. Codex keeps
// appending to the base after the fork; nothing past the offset is read. Other
// rollouts come back as they are.
func withInherited(ctx context.Context, h agent.Host, in agent.Install, mt meta, records []line) ([]line, error) {
	if mt.HistoryMode != "paginated" || mt.HistoryBase == nil {
		return records, nil
	}
	var paths map[agent.SessionID]string
	seen := map[string]bool{mt.ID: true}
	var prefix []line
	for base, depth := mt.HistoryBase, 0; base != nil; depth++ {
		if depth == maxInherited {
			return nil, fmt.Errorf("%w: Codex forks inherit from more than %d threads", agent.ErrUnsupported, maxInherited)
		}
		if base.ThreadID == "" || base.EndOrdinalExclusive == nil || base.EndByteOffset == nil || *base.EndByteOffset < 0 {
			return nil, fmt.Errorf("%w: Codex inherited history names no thread, ordinal or byte offset", agent.ErrUnsupported)
		}
		if seen[base.ThreadID] {
			return nil, fmt.Errorf("%w: Codex inherited history is cyclic", agent.ErrDiverged)
		}
		seen[base.ThreadID] = true
		if paths == nil {
			paths = rolloutPaths(h, in)
		}
		path, ok := paths[agent.SessionID(base.ThreadID)]
		if !ok {
			return nil, fmt.Errorf("%w: the Codex thread %s this fork inherits its history from is not here", agent.ErrUnsupported, base.ThreadID)
		}
		recs, err := readPrefix(ctx, h, path, *base.EndByteOffset)
		if err != nil {
			return nil, err
		}
		var bm meta
		if len(recs) == 0 || recs[0].Type != "session_meta" || recs[0].Ordinal == nil || json.Unmarshal(recs[0].Payload, &bm) != nil || bm.ID != base.ThreadID || bm.HistoryMode != "paginated" {
			return nil, fmt.Errorf("%w: %s does not start with thread %s's paginated session_meta", agent.ErrDiverged, path, base.ThreadID)
		}
		// A base that is a fork too numbers its own records on from its inherited
		// prefix, so the fork's end ordinal counts from where the base's records start.
		if first := *recs[0].Ordinal; first > *base.EndOrdinalExclusive || uint64(len(recs)) != *base.EndOrdinalExclusive-first {
			return nil, fmt.Errorf("%w: %s holds %d records from ordinal %d before byte %d, not up to the ordinal %d the fork inherits", agent.ErrDiverged, path, len(recs), first, *base.EndByteOffset, *base.EndOrdinalExclusive)
		}
		prefix = append(recs, prefix...)
		base = bm.HistoryBase
	}
	return append(prefix, records...), nil
}

// readPrefix reads a rollout's records up to end, which must fall on a record boundary.
func readPrefix(ctx context.Context, h agent.Host, path string, end int64) ([]line, error) {
	f, err := h.FS().Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	recs, n, err := readAnalysisLines(ctx, io.LimitReader(f, end))
	if err != nil {
		return nil, &agent.FormatError{Path: path, Err: err}
	}
	if n != end {
		return nil, fmt.Errorf("%w: %s ends before byte %d, or that byte is inside a record", agent.ErrDiverged, path, end)
	}
	return recs, nil
}
