package gui

import (
	"context"
	"reflect"
	"slices"
	"time"

	"github.com/roeehrl/hopsesh/internal/app"
	"github.com/roeehrl/hopsesh/internal/core/audit"
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
	a.mu.Lock()
	observed := *inv
	observed.Entries = slices.Clone(inv.Entries)
	a.mu.Unlock()
	a.bindTerminalSnapshot(inv, &observed, table)
}

// Presence supplies new native evidence without changing the published inventory.
// Commit checks the exact inventory owner under mu; an unrelated remote scan
// must not prevent fresh local process evidence from associating an owned PTY.
func (a *App) bindTerminalPresence(owned, snapshot *app.Inventory, live map[agent.SessionKey]agent.LiveInfo, table presence.Table) {
	observed := *snapshot
	observed.Entries = slices.Clone(snapshot.Entries)
	for i := range observed.Entries {
		e := &observed.Entries[i]
		if e.Machine == app.LocalName() {
			if li, ok := live[e.Session.Key]; ok {
				e.Live = li
			} else {
				e.Live = agent.LiveInfo{State: agent.Unknown}
			}
		}
	}
	a.bindTerminalSnapshot(owned, &observed, table)
}

func (a *App) bindTerminalSnapshot(owned, observed *app.Inventory, table presence.Table) {
	if a.Terms == nil {
		return
	}
	relationships := observed.Relationships()
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
		found, ambiguous := boundEntry(observed, table, tab, key)
		if tab.Association != "Confirmed by native process registry" {
			// Bounded, message-free native evidence for diagnosing a live failure.
			// PID/start time come from the owned PTY; Procs come from LiveDetector.
			candidates := []map[string]any{}
			for _, e := range observed.Entries {
				if e.Machine == tab.Machine && e.Session.Key == key {
					candidates = append(candidates, map[string]any{"key": e.Session.Key.String(), "state": e.Live.State, "pid": e.Live.PID, "procs": e.Live.Procs})
				}
			}
			a.snapshot().Audit.Write(audit.Entry{Action: "terminal.binding.probe", Host: tab.Machine, Session: key.String(), Detail: map[string]any{
				"tab": tab.ID, "ptyPID": tab.PID, "ptyStarted": tab.Started, "ptyParent": table[tab.PID].PPID,
				"discovering": observed.Discovering, "candidates": candidates, "matched": found != nil, "ambiguous": ambiguous,
			}})
		}
		a.commitTerminalBinding(owned, tab, key, found, ambiguous, relationships)
	}
}

// Commit only evidence already verified by boundEntry against a native process snapshot.
func (a *App) commitTerminalBinding(owned *app.Inventory, tab TermTab, key agent.SessionKey, found *app.Entry, ambiguous bool, relationships map[string]app.Relationship) {
	// Recheck the exact owned process after observing the table/registry.
	session, ok := a.Terms.Manager().Get(tab.ID)
	if !ok {
		return
	}
	info := session.Info()
	if info.State == pty.Exited || info.PID != tab.PID || info.Started != tab.Started {
		return
	}
	// Terminal exit callbacks already acquire Terms.mu before App.mu.
	// Use that same order, and recheck ownership/lifetime before committing.
	a.Terms.mu.Lock()
	a.mu.Lock()
	stale := a.historyStale[app.EntryIdentity(tab.Machine, key.String())]
	if found != nil {
		stale = stale || a.historyStale[app.EntryIdentity(found.Machine, found.Session.Key.String())]
	}
	if a.inv != owned || a.closing || a.selectionReads > 0 || a.reviewBusyLocked() || stale {
		reason := "inventory changed"
		switch {
		case a.closing:
			reason = "app closing"
		case a.selectionReads > 0:
			reason = "selection read in progress"
		case a.reviewBusyLocked():
			reason = "review or action owns inventory"
		case stale:
			reason = "History evidence invalidated"
		}
		a.mu.Unlock()
		a.Terms.mu.Unlock()
		a.snapshot().Audit.Write(audit.Entry{Action: "terminal.binding.deferred", Host: tab.Machine, Session: key.String(), Detail: map[string]any{"tab": tab.ID, "ptyPID": tab.PID, "ptyStarted": tab.Started, "reason": reason}})
		return
	}
	// Commit with both owners locked; emitting the update happens afterwards,
	// because terminal notifications can call back into the app.
	tb := a.Terms.tabs[tab.ID]
	current := session.Info()
	if current.State == pty.Exited || current.PID != tab.PID || current.Started != tab.Started || tb == nil || tb.meta.Key != tab.Key || tb.meta.LaunchedKey != tab.LaunchedKey || tb.meta.Kind != tab.Kind {
		a.mu.Unlock()
		a.Terms.mu.Unlock()
		return
	}
	m := &tb.meta
	before := *m
	if ambiguous || found == nil {
		m.Association, m.Relationship = "Session association not confirmed", nil
	} else {
		e := *found
		r := relationships[app.EntryIdentity(e.Machine, e.Session.Key.String())]
		if name := a.core.Cfg.FamilyNames[r.Family]; name != "" {
			r.Name = name
		}
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
	}
	changed := !reflect.DeepEqual(before, *m)
	a.mu.Unlock()
	a.Terms.mu.Unlock()
	if changed {
		a.Terms.changed(info)
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
