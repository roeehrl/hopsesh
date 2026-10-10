package claude

import (
	"context"
	"fmt"
	"github.com/roeehrl/hopsesh/sdk/agent"
	"github.com/roeehrl/hopsesh/sdk/ir"
	"strings"
)

func (m *Module) RecoverWrite(ctx context.Context, h agent.Host, in agent.Install, req ir.WriteRequest) (ir.WriteResult, error) {
	list, err := m.List(ctx, h, in)
	if err != nil {
		return ir.WriteResult{}, err
	}
	var sum agent.Summary
	for _, s := range list.Sessions {
		if string(s.Key.Session) == req.SessionID {
			sum = s
		}
	}
	if sum.Path == "" {
		return ir.WriteResult{}, agent.ErrNotFound
	}
	actual, err := m.Read(ctx, h, in, sum, ir.Cursor{})
	if err != nil {
		return ir.WriteResult{}, err
	}
	w := writer{session: req.SessionID, version: in.Version, cwd: req.Header.CWD, branch: req.Header.GitBranch, model: claudeModel(req.Header.Model)}
	records, _, err := readRecords(strings.NewReader(string(w.records(req))))
	if err != nil {
		return ir.WriteResult{}, err
	}
	wanted := map[string]ir.Node{}
	for _, r := range records {
		for i, n := range nodes(r) {
			wanted[fmt.Sprintf("%s/%d", r.UUID, i)] = n
		}
	}
	var prefix ir.Segment
	if req.Mode == ir.WriteAppend {
		body, e := h.FS().ReadFile(sum.Path, 1<<30)
		if e != nil {
			return ir.WriteResult{}, e
		}
		if int64(len(body)) < req.Expect.Offset {
			return ir.WriteResult{}, agent.ErrDiverged
		}
		recs, _, e := readRecords(strings.NewReader(string(body[:req.Expect.Offset])))
		if e != nil {
			return ir.WriteResult{}, e
		}
		branch, e := activeBranch(recs)
		if e != nil {
			return ir.WriteResult{}, e
		}
		for _, index := range branch {
			prefix.Nodes = append(prefix.Nodes, nodes(recs[index])...)
		}
		ir.Chain(prefix.Nodes, "")
		var head ir.NodeID
		if len(prefix.Nodes) > 0 {
			head = prefix.Nodes[len(prefix.Nodes)-1].ID
		}
		if head != req.Expect.Head {
			return ir.WriteResult{}, agent.ErrDiverged
		}
	}
	if len(actual.Nodes) != len(prefix.Nodes)+len(wanted) {
		return ir.WriteResult{}, fmt.Errorf("%w: native work followed the interrupted write", agent.ErrDiverged)
	}
	out := ir.WriteResult{SessionID: req.SessionID, Path: sum.Path, From: req.Expect.Offset, To: actual.Cursor.Offset, Cursor: actual.Cursor}
	for _, n := range actual.Nodes {
		if expected, ok := wanted[n.Native.Anchor]; ok {
			if ir.ContentHash(expected) != ir.ContentHash(n) {
				return ir.WriteResult{}, fmt.Errorf("%w: interrupted projection changed", agent.ErrDiverged)
			}
			out.Projection = append(out.Projection, ir.ProjectionFor(n, w.provenance[n.Native.Anchor]))
			delete(wanted, n.Native.Anchor)
		}
	}
	if len(wanted) > 0 {
		return ir.WriteResult{}, fmt.Errorf("%w: write was incomplete", agent.ErrDiverged)
	}
	return out, nil
}
