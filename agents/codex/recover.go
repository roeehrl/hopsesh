package codex

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
	start, prefixCount := 1, 0
	if req.Mode == ir.WriteAppend {
		body, e := agent.ReadNative(ctx, h.FS(), sum.Path)
		if e != nil {
			return ir.WriteResult{}, e
		}
		if int64(len(body)) < req.Expect.Offset {
			return ir.WriteResult{}, agent.ErrDiverged
		}
		recs, _, e := readLines(strings.NewReader(string(body[:req.Expect.Offset])))
		if e != nil {
			return ir.WriteResult{}, e
		}
		start = len(recs)
		for _, n := range actual.Nodes {
			var index int
			_, _ = fmt.Sscanf(n.Native.Anchor, "line:%d/", &index)
			if index < start {
				prefixCount++
			}
		}
		if prefixCount > 0 && actual.Nodes[prefixCount-1].ID != req.Expect.Head {
			return ir.WriteResult{}, agent.ErrDiverged
		}
	}
	if len(actual.Nodes) != prefixCount+len(req.Items) {
		return ir.WriteResult{}, fmt.Errorf("%w: native work followed the interrupted write", agent.ErrDiverged)
	}
	by := map[string]ir.Node{}
	for _, n := range actual.Nodes {
		by[n.Native.Anchor] = n
	}
	out := ir.WriteResult{SessionID: req.SessionID, Path: sum.Path, From: req.Expect.Offset, To: actual.Cursor.Offset, Cursor: actual.Cursor}
	for i, item := range req.Items {
		anchor := fmt.Sprintf("line:%d/0", start+2*i+1)
		n, ok := by[anchor]
		actor := ir.Agent
		if item.Role == ir.RoleUser {
			actor = ir.User
		}
		if !ok || n.Kind != ir.KindMessage || n.Actor != actor || n.Text != item.Text {
			return ir.WriteResult{}, fmt.Errorf("%w: interrupted projection changed", agent.ErrDiverged)
		}
		out.Projection = append(out.Projection, ir.ProjectionFor(n, item))
	}
	return out, nil
}
