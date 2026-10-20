package main

import (
	"encoding/json"
	"fmt"

	"github.com/roeehrl/hopsesh/internal/core/move"
)

// repeatRoundtrip crosses the actual OS pair four times: there-here-there-here-
// there. Fresh portable replicas preserve each native account boundary. Every
// hop extends the previous conversation and repeats its durable request ID.
func (r *runner) repeatRoundtrip(s *sc) error {
	current := Found{Agent: s.row.From, ID: s.id, Path: s.srcFile}
	source := side(r.there)
	work := []string{s.text}
	var family, branch string
	for hop := range 4 {
		text := fmt.Sprintf("repeated-return-hop-%d-%s", hop, s.marker)
		work = append(work, text)
		if err := source.do("append", AppendReq{Agent: current.Agent, Path: current.Path, ID: current.ID, Text: text}, &struct{}{}); err != nil {
			return err
		}
		target, agent, cwd, previousCwd := side(r.here), s.row.To, s.dstCwd, s.srcCwd
		args := []string{"pull", s.host + ":" + current.Agent + "/" + current.ID}
		if hop%2 == 1 {
			target, agent, cwd, previousCwd = r.there, s.row.From, s.srcCwd, s.dstCwd
			args = []string{"push", current.Agent + "/" + current.ID, s.host}
		}
		args = append(args, "--in", agent, "--to", cwd, "--new-session", "--yes", "--json", "--operation-id", fmt.Sprintf("repeat-%s-%d", s.id, hop))
		out, err := r.hs(true, args...)
		if err != nil {
			return err
		}
		var result struct{ Plan move.Plan }
		if err := json.Unmarshal([]byte(out), &result); err != nil {
			return err
		}
		id := string(result.Plan.Placement.Key.Session)
		if id == "" || id == current.ID || !result.Plan.Options.NewReplica {
			return fmt.Errorf("hop %d did not respect the portable native identity boundary", hop)
		}
		findArrival := func() (Found, error) {
			var found []Found
			if err := target.do("find", FindReq{Marker: s.marker, Needles: append(append([]string{}, work...), cwd, previousCwd)}, &found); err != nil {
				return Found{}, err
			}
			for _, f := range found {
				if f.Agent == agent && f.ID == id {
					return f, nil
				}
			}
			return Found{}, fmt.Errorf("hop %d: planned native arrival is missing", hop)
		}
		arrival, err := findArrival()
		if err != nil {
			return err
		}
		for _, needle := range work {
			if !arrival.Has[needle] {
				return fmt.Errorf("hop %d lost prior work %q", hop, needle)
			}
		}
		if !arrival.Has[cwd] || cwd != previousCwd && arrival.Has[previousCwd] {
			return fmt.Errorf("hop %d did not translate its native working directory", hop)
		}
		if err := checkMovement(arrival, hop+1, true, false); err != nil {
			return err
		}
		g := arrival.Graph
		if hop == 0 {
			family, branch = string(g.Family), string(g.Branch)
		}
		returns, transfers := (hop+1)/2, hop+1
		if string(g.Family) != family || string(g.Branch) != branch || g.Journey().RoundTrips != returns || g.Journey().Transfers != transfers {
			return fmt.Errorf("hop %d lost family/branch identity or repeated return count: %+v", hop, g.Journey())
		}
		// A fresh CLI process must recover the exact committed arrival, including
		// when the source now carries a departure notice.
		if _, err := r.hs(true, args...); err != nil {
			return err
		}
		repeated, err := findArrival()
		if err != nil || repeated.SHA256 != arrival.SHA256 || repeated.Graph == nil || len(repeated.Graph.ActiveHops()) != hop+1 {
			return fmt.Errorf("hop %d retry changed native history or duplicated lineage: %v", hop, err)
		}
		current, source = repeated, target
	}
	return nil
}

// Exercise sender options over the real peer protocol. The original is missing. When the
// return made a separate replica, that replica survives and must stay untouched; when the
// return appended to the exact original (survivor is the original), nothing survives.
// Check explicit intent in the receiver's plan too, which would otherwise hide a receiver
// dropping the NewReplica option.
func (r *runner) newSessionAfterMissingOriginal(s *sc, source, survivor Found, incoming string) error {
	if survivor.Graph == nil {
		return fmt.Errorf("new-session fixture needs the returned replica's lineage")
	}
	intoOriginal := survivor.ID == s.id
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
			if f.Path != survivor.Path || f.SHA256 != survivor.SHA256 || f.Label != survivor.Label {
				return fmt.Errorf("new-session modified the surviving native copy")
			}
		default:
			created++
			if f.ID != string(p.Placement.Key.Session) {
				return fmt.Errorf("new-session did not create the receiver's planned native ID")
			}
			if f.Label != "" || !f.Has[incoming] || !f.Has[s.srcCwd] {
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
	want := 1
	if intoOriginal {
		want = 0 // the return went into the original, which this fixture removed
	}
	if preserved != want || created != 1 {
		return fmt.Errorf("new-session with missing original: want %d preserved and one fresh replica, got %d and %d", want, preserved, created)
	}
	return nil
}
