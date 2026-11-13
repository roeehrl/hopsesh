package opencode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/roeehrl/hopsesh/sdk/agent"
	"github.com/roeehrl/hopsesh/sdk/ir"
)

// OpenCode's fork (POST /api/session/{id}/fork) copies the parent's settled messages up to
// a boundary into a new session: each keeps the parent's seq under a new id
// (<id>_<seq>), and the copy's row names its parent (fork_session_id) and the boundary
// ({"type":"through"|"before","messageID":…}). An import carries neither, so forks are
// verified where the database is read, and travel in hopsesh's lineage.

const seqQuery = `SELECT seq FROM session_message WHERE session_id = ? AND id = ?`

// VerifyNativeFork checks that child is a fork of parent: its row names parent, and its
// messages up to the boundary read as the parent's, message by message (by seq).
func (m *Module) VerifyNativeFork(ctx context.Context, h agent.Host, in agent.Install, parent, child agent.Summary) ([]agent.NativeInheritance, error) {
	if parent.Key.Agent != child.Key.Agent || parent.Key.Profile != child.Key.Profile || parent.Key.Session == child.Key.Session {
		return nil, fmt.Errorf("%w: native fork parent is not declared", agent.ErrDiverged)
	}
	r, ok, err := openSQL(ctx, h, in)
	if !ok {
		return nil, fmt.Errorf("%w: OpenCode forks are verified where hopsesh reads OpenCode's database", agent.ErrUnsupported)
	}
	if err != nil {
		return nil, err
	}
	cs, cmsgs, err := r.transcript(ctx, child.Key.Session)
	if err != nil {
		return nil, err
	}
	if cs.ForkSessionID != string(parent.Key.Session) {
		return nil, fmt.Errorf("%w: native fork parent is not declared", agent.ErrDiverged)
	}
	var b struct {
		Type      string `json:"type"`
		MessageID string `json:"messageID"`
	}
	if json.Unmarshal(cs.ForkBoundary, &b) != nil || b.MessageID == "" || b.Type != "through" && b.Type != "before" {
		return nil, fmt.Errorf("%w: the fork's boundary is not one hopsesh reads", agent.ErrUnsupported)
	}
	rows, err := r.sq.Query(ctx, r.db, seqQuery, string(parent.Key.Session), b.MessageID)
	if err != nil {
		return nil, err
	}
	if len(rows.Rows) == 0 {
		return nil, fmt.Errorf("%w: the fork's boundary is no longer in its parent", agent.ErrDiverged)
	}
	last := rows.Rows[0].Int("seq")
	if b.Type == "before" {
		last--
	}
	ps, pmsgs, err := r.transcript(ctx, parent.Key.Session)
	if err != nil {
		return nil, err
	}
	prefix := func(msgs []message) []message {
		var out []message
		for _, msg := range msgs {
			if msg.Seq <= last {
				out = append(out, msg)
			}
		}
		return out
	}
	pp, cp := prefix(pmsgs), prefix(cmsgs)
	if len(pp) != len(cp) {
		return nil, fmt.Errorf("%w: history differs before declared fork boundary", agent.ErrDiverged)
	}
	for i := range pp {
		if pp[i].Seq != cp[i].Seq || pp[i].Type != cp[i].Type {
			return nil, fmt.Errorf("%w: history differs before declared fork boundary", agent.ErrDiverged)
		}
	}
	pseg, err := segment(ps, pp, ir.Cursor{})
	if err != nil {
		return nil, err
	}
	cseg, err := segment(cs, cp, ir.Cursor{})
	if err != nil {
		return nil, err
	}
	if len(pseg.Nodes) != len(cseg.Nodes) {
		return nil, fmt.Errorf("%w: native fork prefix changed representation", agent.ErrDiverged)
	}
	var out []agent.NativeInheritance
	for i, pn := range pseg.Nodes {
		cn := cseg.Nodes[i]
		if ir.ContentHash(pn) != ir.ContentHash(cn) {
			return nil, fmt.Errorf("%w: native fork prefix changed representation", agent.ErrDiverged)
		}
		out = append(out, agent.NativeInheritance{ParentAnchor: pn.Native.Anchor, ChildAnchor: cn.Native.Anchor, Hash: ir.ContentHash(cn)})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%w: native fork has no verified inherited conversation", agent.ErrDiverged)
	}
	return out, nil
}

// NativeParentGone reports a fork whose parent OpenCode no longer has: an OpenCode fork
// holds its whole inherited history, so it is a conversation of its own then.
func (m *Module) NativeParentGone(ctx context.Context, h agent.Host, in agent.Install, s agent.Summary) (bool, error) {
	if s.NativeParent == "" {
		return false, nil
	}
	r, ok, err := openSQL(ctx, h, in)
	if !ok {
		return false, fmt.Errorf("%w: OpenCode forks are checked where hopsesh reads OpenCode's database", agent.ErrUnsupported)
	}
	if err != nil {
		return false, err
	}
	_, err = r.session(ctx, s.NativeParent)
	switch {
	case errors.Is(err, agent.ErrNotFound):
		return true, nil
	case err != nil:
		return false, err
	}
	return false, nil
}
