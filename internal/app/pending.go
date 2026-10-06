package app

import (
	"context"
	"time"

	"github.com/roeehrl/hopsesh/internal/core/audit"
	"github.com/roeehrl/hopsesh/internal/core/journal"
	"github.com/roeehrl/hopsesh/internal/core/lineage"
	"github.com/roeehrl/hopsesh/sdk/agent"
	"github.com/roeehrl/hopsesh/sdk/ir"
)

// applyPending writes marks owed to copies on this machine that were still open when they
// moved on. A copy that gained new work afterwards was kept in use: it is not marked.
func (a *App) applyPending(ctx context.Context, m *Machine, entries []Entry) {
	all, err := lineage.LoadPending(a.StateDir)
	if err != nil || len(all) == 0 {
		return
	}
	var keep []lineage.Pending
	changed := false
	for _, p := range all {
		if p.Location != m.Name {
			keep = append(keep, p)
			continue
		}
		var e *Entry
		for i := range entries {
			if entries[i].Session.Key == p.Key {
				e = &entries[i]
			}
		}
		switch {
		case e == nil, e.Session.Mark != nil:
			changed = true // gone, or marked already
			continue
		case e.Live.State == agent.Live:
			keep = append(keep, p) // still open: try on a later scan
			continue
		}
		changed = true
		if !currentDeparture(p, e.Lineage) {
			continue
		}
		mod, ok := a.Module(e.Agent)
		if !ok {
			continue
		}
		if r, isReader := mod.(agent.Reader); isReader && p.Head != "" {
			h, err := m.host.For(ctx, mod.Spec(), mustProfileInstall(m, e.Session.Key), nil)
			if err == nil {
				seg, err := r.Read(ctx, h, mustProfileInstall(m, e.Session.Key), e.Session, ir.Cursor{Head: ir.NodeID(p.Head)})
				if err != nil || len(seg.Nodes) > 0 {
					continue // kept in use after it moved on
				}
			}
		}
		marker, ok := mod.(agent.Marker)
		if !ok {
			continue
		}
		j, err := journal.New(a.StateDir, journal.KindMark, "mark "+p.Title+" on "+m.Name)
		if err != nil {
			continue
		}
		j.AddKey(p.Key)
		h, err := m.host.For(ctx, mod.Spec(), mustProfileInstall(m, e.Session.Key), j)
		if err == nil {
			err = marker.Mark(ctx, h, mustProfileInstall(m, e.Session.Key), e.Session, p.Mark)
		}
		if err == nil {
			mk := p.Mark
			e.Session.Mark = &mk
		} else if time.Since(p.Time) < 7*24*time.Hour {
			keep = append(keep, p) // try again later
		}
		a.Audit.Write(audit.Entry{Action: "mark.deferred", Host: m.Name, Session: p.Key.String(), Detail: map[string]any{"ok": err == nil}})
	}
	if changed {
		_ = lineage.SavePending(a.StateDir, keep)
	}
}

// Deferred titles belong to one committed departure, never a later return or sibling.
func currentDeparture(p lineage.Pending, m *lineage.Manifest) bool {
	if m == nil || p.Operation == "" || p.Branch != m.Branch || m.Replica(p.Replica).Key != p.Key {
		return false
	}
	undone := map[string]bool{}
	for _, c := range m.Compensations {
		undone[c.Operation] = true
	}
	var last lineage.Hop
	for _, h := range m.OrderedHops() {
		if h.Line == p.Branch && !h.Backup && !undone[h.ID] {
			last = h
		}
	}
	return last.ID == p.Operation && last.From == p.Replica
}

func mustProfileInstall(m *Machine, key agent.SessionKey) agent.Install {
	in, _ := m.InstallProfile(key.Agent, key.Profile)
	return in
}
