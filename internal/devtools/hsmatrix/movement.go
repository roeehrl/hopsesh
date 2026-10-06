package main

import (
	"encoding/json"
	"fmt"

	"github.com/roeehrl/hopsesh/internal/core/move"
)

// Exercise sender options over the real peer protocol. The original is missing,
// but a synchronized replica survives. Check explicit intent in the receiver's
// plan too: these cross-profile fixtures already require a fresh portable copy,
// which would otherwise hide a receiver dropping the NewReplica option.
func (r *runner) newSessionAfterMissingOriginal(s *sc, source, survivor Found, incoming string) error {
	if survivor.ID == s.id || survivor.Graph == nil {
		return fmt.Errorf("new-session fixture needs a separate surviving replica")
	}
	if err := r.there.do("remove", RemoveReq{Agent: s.row.From, ID: s.id, Marker: s.marker}, &struct{}{}); err != nil {
		return err
	}
	args := []string{"push", s.row.To + "/" + source.ID, s.host, "--yes", "--json", "--new-session", "--to", s.srcCwd}
	if s.row.To != s.row.From {
		args = append(args, "--in", s.row.From)
	}
	out, err := r.hs(true, args...)
	if err != nil {
		return err
	}
	var reply struct{ Plan move.Plan }
	if err := json.Unmarshal([]byte(out), &reply); err != nil {
		return fmt.Errorf("new-session peer result: %w", err)
	}
	p := reply.Plan
	if !p.Options.NewReplica || p.NoWork || p.Options.Fork || p.Continue == nil || p.Continue.AppendTo != nil {
		return fmt.Errorf("receiver lost explicit new-session intent or selected an existing copy")
	}
	var found []Found
	if err := r.there.do("find", FindReq{Marker: s.marker, Needles: []string{incoming, s.srcCwd}}, &found); err != nil {
		return err
	}
	preserved, created := 0, 0
	for _, f := range found {
		if f.Agent != s.row.From {
			continue
		}
		switch f.ID {
		case s.id:
			return fmt.Errorf("new-session recreated the missing original's native ID")
		case survivor.ID:
			preserved++
			if f.Path != survivor.Path || f.SHA256 != survivor.SHA256 || f.Mark != survivor.Mark {
				return fmt.Errorf("new-session modified the surviving native copy")
			}
		default:
			created++
			if f.ID != string(p.Placement.Key.Session) {
				return fmt.Errorf("new-session did not create the receiver's planned native ID")
			}
			if f.Mark != "" || !f.Has[incoming] || !f.Has[s.srcCwd] {
				return fmt.Errorf("new-session lost incoming work or destination folder: %+v", f)
			}
			if f.Graph == nil || f.Graph.Branch != survivor.Graph.Branch {
				return fmt.Errorf("new-session forked or lost the logical branch")
			}
			if err := checkMovement(f, 3, true, false); err != nil {
				return err
			}
		}
	}
	if preserved != 1 || created != 1 {
		return fmt.Errorf("new-session with missing original: want one preserved and one fresh replica, got %d and %d", preserved, created)
	}
	return nil
}
