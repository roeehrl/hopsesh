package gui

import (
	"context"
	"os"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/roeehrl/hopsesh/internal/app"
	"github.com/roeehrl/hopsesh/internal/core/audit"
	"github.com/roeehrl/hopsesh/internal/core/presence"
	"github.com/roeehrl/hopsesh/internal/core/pty"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

// Codex's shared daemon owns the writer locks on behalf of unrelated CLI clients.
// Its initialize handshake has no client PID/thread association. Use the vendor's
// per-invocation embedded server for owned session tabs, so native lock evidence
// can still prove ancestry (including a native /new or /fork). This does not change
// profile roots, authentication, hooks, or commands opened in an external terminal.
// --no-daemon is supported since 0.156.0; unknown/older versions stay unchanged.
// Source: openai/codex rust-v0.160.1, tui/src/cli.rs and tui/src/daemon_startup.rs.
func (a *App) ownedSessionCommand(c agent.Command, meta TabMeta) agent.Command {
	key, err := agent.ParseKey(meta.Key)
	if err != nil || key.Agent != "codex" || meta.Machine != app.LocalName() || meta.Kind != TabSession || len(c.Argv) < 2 || (c.Argv[1] != "resume" && c.Argv[1] != "fork") {
		return c
	}
	a.mu.Lock()
	var in agent.Install
	if a.inv != nil {
		if local := a.inv.Local(); local != nil {
			in, _ = local.InstallProfile(key.Agent, key.Profile)
		}
	}
	a.mu.Unlock()
	// Version evidence belongs to this exact scoped executable, not another
	// wrapper or a bare name that PATH could resolve differently at launch.
	if in.Agent != "codex" || in.Binary == "" || !agent.HookVersionAtLeast(in.Version, "0.156.0") || c.Argv[0] != in.Binary || slices.Contains(c.Argv, "--no-daemon") {
		return c
	}
	// --no-daemon conflicts with an explicit remote endpoint. Never change that
	// transport/authentication choice, including an inherited endpoint not removed
	// by the already-scoped command. Vendor daemon-auto-start is optional, whereas
	// daemon-only `agents` is excluded by the resume/fork gate above.
	for _, arg := range c.Argv[2:] {
		if arg == "--remote" || strings.HasPrefix(arg, "--remote=") || arg == "--remote-auth-token-env" || strings.HasPrefix(arg, "--remote-auth-token-env=") {
			return c
		}
	}
	endpoint := os.Getenv("CODEX_APP_SERVER_ADDRESS")
	endpointName := func(name string) bool {
		return name == "CODEX_APP_SERVER_ADDRESS" || in.OS == "windows" && strings.EqualFold(name, "CODEX_APP_SERVER_ADDRESS")
	}
	for _, name := range c.Unset {
		if endpointName(name) {
			endpoint = ""
		}
	}
	for _, env := range c.Env {
		if name, v, ok := strings.Cut(env, "="); ok && endpointName(name) {
			endpoint = v
		}
	}
	if endpoint != "" {
		return c
	}
	c.Argv = append([]string{c.Argv[0], c.Argv[1], "--no-daemon"}, c.Argv[2:]...)
	return c
}

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

const associationChecking = "Checking session association"

type terminalBindingControls struct{ external, rerun bool }

type terminalBindingRead struct {
	sequence uint64
	source   string
	checking bool
}

func (a *App) beginTerminalBindingRead(source string) terminalBindingRead {
	if a.Terms == nil {
		return terminalBindingRead{}
	}
	a.Terms.mu.Lock()
	a.Terms.bindingSequence++
	read := terminalBindingRead{sequence: a.Terms.bindingSequence, source: source}
	a.Terms.mu.Unlock()
	return read
}

func (a *App) bindingRead(read []terminalBindingRead, source string) terminalBindingRead {
	if len(read) > 0 {
		return read[0]
	}
	return a.beginTerminalBindingRead(source)
}

func (a *App) bindTerminalSessions(inv *app.Inventory, reads ...terminalBindingRead) {
	if a.Terms == nil {
		return
	}
	read := a.bindingRead(reads, "scan")
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
	a.bindTerminalSnapshot(inv, &observed, table, read)
}

// Presence supplies new native evidence without changing the published inventory.
// Commit checks the exact inventory owner under mu; an unrelated remote scan
// must not prevent fresh local process evidence from associating an owned PTY.
func (a *App) bindTerminalPresence(owned, snapshot *app.Inventory, live map[agent.SessionKey]agent.LiveInfo, table presence.Table, reads ...terminalBindingRead) {
	read := a.bindingRead(reads, "presence")
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
	a.bindTerminalSnapshot(owned, &observed, table, read)
}

func (a *App) bindTerminalSnapshot(owned, observed *app.Inventory, table presence.Table, reads ...terminalBindingRead) {
	if a.Terms == nil {
		return
	}
	read := a.bindingRead(reads, "snapshot")
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
		observation := read
		observation.checking = bindingIsChecking(observed, tab)
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
		a.commitTerminalBinding(owned, tab, key, found, ambiguous, relationships, observation)
	}
}

// Missing discovery data or a failed native read is not evidence that the last
// verified session ended. Retain it only as last-known, explicitly checking;
// a known Ended/Live mismatch or ambiguity still removes the association.
func bindingIsChecking(inv *app.Inventory, tab TermTab) bool {
	for _, e := range inv.Entries {
		if e.Machine == tab.Machine && e.Session.Key.String() == tab.Key {
			return e.Live.State == agent.Unknown
		}
	}
	return inv.Discovering
}

// Commit only evidence already verified by boundEntry against a native process snapshot.
func (a *App) commitTerminalBinding(owned *app.Inventory, tab TermTab, key agent.SessionKey, found *app.Entry, ambiguous bool, relationships map[string]app.Relationship, reads ...terminalBindingRead) {
	read := a.bindingRead(reads, "snapshot")
	skip := func(reason string) {
		a.snapshot().Audit.Write(audit.Entry{Action: "terminal.binding.deferred", Host: tab.Machine, Session: key.String(), Detail: map[string]any{"tab": tab.ID, "ptyPID": tab.PID, "ptyStarted": tab.Started, "reason": reason, "source": read.source, "sequence": read.sequence}})
	}
	// Recheck the exact owned process after observing the table/registry.
	session, ok := a.Terms.Manager().Get(tab.ID)
	if !ok {
		skip("owned PTY no longer exists")
		return
	}
	info := session.Info()
	if info.State == pty.Exited || info.PID != tab.PID || info.Started != tab.Started {
		skip("owned PTY lifetime changed")
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
		skip(reason)
		return
	}
	// Commit with both owners locked; emitting the update happens afterwards,
	// because terminal notifications can call back into the app.
	tb := a.Terms.tabs[tab.ID]
	current := session.Info()
	if current.State == pty.Exited || current.PID != tab.PID || current.Started != tab.Started || tb == nil || tb.meta.Key != tab.Key || tb.meta.LaunchedKey != tab.LaunchedKey || tb.meta.Kind != tab.Kind {
		a.mu.Unlock()
		a.Terms.mu.Unlock()
		skip("tab metadata or PTY lifetime changed")
		return
	}
	if read.sequence < tb.bindingSequence {
		a.mu.Unlock()
		a.Terms.mu.Unlock()
		skip("older native observation")
		return
	}
	tb.bindingSequence = read.sequence
	m := &tb.meta
	before := *m
	if ambiguous || found == nil {
		if !ambiguous && read.checking && (m.Association == "Confirmed by native process registry" || m.Association == associationChecking) {
			if m.Association != associationChecking {
				tb.checkingControls = &terminalBindingControls{external: m.External, rerun: m.Rerun}
			}
			m.Association = associationChecking
			m.External, m.Rerun = false, false
		} else {
			m.Association, m.Relationship = "Session association not confirmed", nil
			tb.checkingControls = nil
		}
	} else {
		e := *found
		if before.Association == associationChecking && e.Session.Key.String() == before.Key && tb.checkingControls != nil {
			m.External, m.Rerun = tb.checkingControls.external, tb.checkingControls.rerun
		}
		tb.checkingControls = nil
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
	after := *m
	a.mu.Unlock()
	a.Terms.mu.Unlock()
	if changed {
		a.snapshot().Audit.Write(audit.Entry{Action: "terminal.binding.commit", Host: tab.Machine, Session: after.Key, Detail: map[string]any{"tab": tab.ID, "ptyPID": tab.PID, "ptyStarted": tab.Started, "from": before.Association, "association": after.Association, "matched": found != nil, "ambiguous": ambiguous, "source": read.source, "sequence": read.sequence}})
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
