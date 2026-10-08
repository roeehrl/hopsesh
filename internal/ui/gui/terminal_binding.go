package gui

import (
	"context"
	"time"

	"github.com/roeehrl/hopsesh/internal/app"
	"github.com/roeehrl/hopsesh/internal/core/presence"
	"github.com/roeehrl/hopsesh/internal/core/pty"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

// descendsFrom verifies a process chain in one native snapshot. The root is a
// still-owned PTY child: its process handle has not exited, so its PID cannot
// have been recycled. Missing/cyclic parent chains do not establish identity.
func descendsFrom(table presence.Table, pid, root int) bool {
	seen := map[int]bool{}
	for i := 0; i < 64 && pid > 0 && !seen[pid]; i++ {
		if pid == root {
			return true
		}
		seen[pid] = true
		p, ok := table[pid]
		if !ok {
			return false
		}
		pid = p.PPID
	}
	return false
}
func (a *App) bindTerminalSessions(inv *app.Inventory) {
	if a.Terms == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	table, err := presence.Snapshot(ctx)
	if err != nil {
		return
	}
	relationships := inv.Relationships()
	for _, tab := range a.Terms.Tabs() {
		if tab.Kind != TabSession || tab.Machine != app.LocalName() || tab.PID == 0 || tab.State == pty.Exited {
			continue
		}
		launched := tab.LaunchedKey
		if launched == "" {
			launched = tab.Key
		}
		key, err := agent.ParseKey(launched)
		if err != nil {
			continue
		}
		found, ambiguous := boundEntry(inv, table, tab, key)
		// Recheck the exact owned process after observing the table/registry.
		session, ok := a.Terms.Manager().Get(tab.ID)
		if !ok || session.Info().State == pty.Exited {
			continue
		}
		if ambiguous || found == nil {
			a.Terms.setMeta(tab.ID, func(m *TabMeta) { m.Association = "Session association not confirmed"; m.Relationship = nil })
			continue
		}
		e := *found
		r := relationships[app.EntryIdentity(e.Machine, e.Session.Key.String())]
		a.mu.Lock()
		if name := a.core.Cfg.FamilyNames[r.Family]; name != "" {
			r.Name = name
		}
		a.mu.Unlock()
		a.Terms.setMeta(tab.ID, func(m *TabMeta) {
			if m.Key != e.Session.Key.String() {
				m.External = false
				m.Rerun = false
			}
			m.Account = ""
			if e.Profile != nil && e.Profile.Account != nil {
				m.Account = e.Profile.Account.Email
			}
			m.Key = e.Session.Key.String()
			m.Relationship = &r
			m.Association = "Confirmed by native process registry"
		})
	}
}

func boundEntry(inv *app.Inventory, table presence.Table, tab TermTab, key agent.SessionKey) (*app.Entry, bool) {
	var found *app.Entry
	ambiguous := false
	for i := range inv.Entries {
		e := &inv.Entries[i]
		if e.Machine != tab.Machine || e.Session.Key.Agent != key.Agent || e.Session.Key.Profile != key.Profile || e.Live.State != agent.Live {
			continue
		}
		matches := false
		for _, p := range e.Live.Procs {
			// A stale registry file whose numeric PID was reused cannot rebind
			// this launch. No timestamp means evidence is insufficient.
			if !p.ObservedAt.Before(tab.Started) && !p.ObservedAt.IsZero() && descendsFrom(table, p.PID, tab.PID) {
				matches = true
			}
		}

		if matches {
			if found != nil && found.Session.Key != e.Session.Key {
				ambiguous = true
			}
			found = e
		}
	}

	return found, ambiguous
}
