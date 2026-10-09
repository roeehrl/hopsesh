package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/roeehrl/hopsesh/internal/config"
	"github.com/roeehrl/hopsesh/internal/core/host"
	"github.com/roeehrl/hopsesh/internal/core/lineage"
	"github.com/roeehrl/hopsesh/internal/core/move"
	"github.com/roeehrl/hopsesh/internal/core/repos"
	"github.com/roeehrl/hopsesh/internal/core/transport"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

// Machine statuses.
const (
	StatusOK          = "ok"
	StatusUnreachable = "unreachable"
	StatusAuth        = "auth"
	StatusHostKey     = "host-key"
	StatusKeyChanged  = "host-key-changed"
	StatusTSCheck     = "tailscale-check"
	StatusLocalNet    = "local-network" // macOS local network privacy blocked it
	StatusError       = "error"
)

// Machine is a scanned machine.
type Machine struct {
	Kind        agent.LocationKind `json:"kind"` // always agent.AtMachine (clouds are in Inventory.Clouds)
	Name        string             `json:"name"`
	Destination string             `json:"destination,omitempty"`
	Local       bool               `json:"local"`
	Status      string             `json:"status"`
	Error       string             `json:"error,omitempty"`
	Hint        string             `json:"hint,omitempty"`
	OS          string             `json:"os,omitempty"`
	Phase       string             `json:"phase,omitempty"`
	CheckedAt   time.Time          `json:"checkedAt,omitempty"`
	Agents      []AgentState       `json:"agents"`
	// Hopsesh is the version of hopsesh installed there ("" when none was found).
	Hopsesh string `json:"hopsesh,omitempty"`

	host    *host.Machine
	account *agent.Account // reported by the machine's own hopsesh (a push)
}

// AgentState is an agent as found on a machine.
type AgentState struct {
	Agent   agent.ID      `json:"agent"`
	Name    string        `json:"name"`
	Install agent.Install `json:"install"`
	Error   string        `json:"error,omitempty"`
}

// Host returns the machine's connection (nil when it was not reached).
func (m *Machine) Host() *host.Machine { return m.host }

// Install returns an agent's install there.
func (m *Machine) Install(id agent.ID) (agent.Install, bool) {
	var chosen agent.Install
	for _, a := range m.Agents {
		if a.Agent == id && a.Install.Present && (a.Install.Profile == nil || a.Install.Profile.Default) {
			if chosen.Agent != "" {
				return agent.Install{}, false
			}
			chosen = a.Install
		}
	}
	return chosen, chosen.Agent != ""
}

// InstallProfile selects the exact registered root. An empty selector means the default.
func (m *Machine) InstallProfile(id agent.ID, profile string) (agent.Install, bool) {
	if profile == "" {
		return m.Install(id)
	}
	for _, a := range m.Agents {
		if a.Agent == id && a.Install.Present && a.Install.ProfileID() == profile {
			return a.Install, true
		}
	}
	return agent.Install{}, false
}

// Entry is one session at one location: a machine, or a cloud.
type Entry struct {
	Cached             bool              `json:"cached,omitempty"`
	NativeRelationship bool              `json:"nativeRelationship,omitempty"` // verified during this scan; no persisted family receipt
	Returns            []ReturnCandidate `json:"returns,omitempty"`
	Movement           *MovementNotice   `json:"movement,omitempty"`
	ObservedAt         time.Time         `json:"observedAt,omitempty"`
	// Location is where the session lives. Machine is the machine whose files hold it, or
	// the cloud's name for a cloud session (so "claude-cloud:<id>" names one).
	Location          agent.Location    `json:"location"`
	Machine           string            `json:"machine"`
	Agent             agent.ID          `json:"agent"`
	AgentName         string            `json:"agentName"`
	Session           agent.Summary     `json:"session"`
	Live              agent.LiveInfo    `json:"live"`
	Git               *repos.GitState   `json:"git,omitempty"`
	GitError          string            `json:"gitError,omitempty"` // the checkout could not be read
	CanArchiveLineage bool              `json:"canArchiveLineage,omitempty"`
	LineageError      string            `json:"lineageError,omitempty"`
	Lineage           *lineage.Manifest `json:"lineage,omitempty"`
	// Cloud is a cloud session as its cloud listed it.
	Cloud *agent.CloudSession `json:"cloud,omitempty"`
	// Checkout is a cloud session's repository checked out here, when known.
	Checkout string `json:"checkout,omitempty"`
	// Original is the session a cloud session was handed off from ("machine:agent/id").
	Original string                `json:"original,omitempty"`
	Profile  *agent.RuntimeProfile `json:"profile,omitempty"`
}

// Inventory is the result of a scan.
type Inventory struct {
	Discovering bool       `json:"discovering,omitempty"`
	Machines    []*Machine `json:"machines"`
	Clouds      []*Cloud   `json:"clouds"`
	Entries     []Entry    `json:"entries"`
	// Adopted are sessions brought from a cloud that this scan found and adopted (their
	// driver wrote them since); Waiting, the ones still waiting for it.
	Adopted []*move.Fetch `json:"adopted,omitempty"`
	Waiting []*move.Fetch `json:"waiting,omitempty"`
}

// Close ends every connection the scan opened.
func (inv *Inventory) Close() {
	for _, m := range inv.Machines {
		if m.host != nil {
			m.host.Close()
		}
	}
}

// Machine returns a scanned machine by name.
func (inv *Inventory) Machine(name string) *Machine {
	for _, m := range inv.Machines {
		if m.Name == name {
			return m
		}
	}
	return nil
}

// Local returns this machine.
func (inv *Inventory) Local() *Machine {
	for _, m := range inv.Machines {
		if m.Local {
			return m
		}
	}
	return nil
}

// ScanOptions narrow a scan.
type ScanOptions struct {
	NoCache       bool             // fresh action validation never reads cached summaries
	Progress      func(ScanUpdate) // serialized immutable batches; never owns transports
	ForceAccounts bool             // explicitly refresh public login metadata
	Hosts         []string         // only these machines and clouds ("" or none: every allowed one)
	NoLocal       bool             // leave this machine out
	SkipGit       bool             // no git state (faster)
	// GitFor, when set, limits the git probe to the folders of the sessions it accepts (the
	// others get no git state); see App.GitFor.
	GitFor func(Entry) bool
}

// ScanUpdate describes one independently progressing source. Preliminary rows
// have unknown presence and require fresh validation before an action.
type ScanUpdate struct {
	Machine  *Machine
	Clouds   []*Cloud
	Entries  []Entry
	Complete bool
	Listed   bool // summaries and lineage are ready, before slower enrichment
}

// Scan reads this machine, the allowed machines and the clouds in parallel. A machine or
// a cloud that cannot be read is returned with a status and a hint, not an error. The
// clouds are listed through this machine once its own sessions are listed (their lineage
// names the cloud copies hopsesh made), alongside the other machines.
func (a *App) Scan(ctx context.Context, o ScanOptions) *Inventory {
	started := time.Now()
	defer func() {
		a.Log.Debug("session discovery completed", "duration", time.Since(started), "cache", !o.NoCache)
	}()

	followed, release := a.discoveryLease(ctx, o)
	defer release()
	if followed != nil {
		wanted := map[string]bool{}
		for _, name := range o.Hosts {
			wanted[name] = true
		}
		followed.Machines = slices.DeleteFunc(followed.Machines, func(m *Machine) bool { return (o.NoLocal && m.Local) || (len(wanted) > 0 && !wanted[m.Name]) })
		followed.Clouds = slices.DeleteFunc(followed.Clouds, func(c *Cloud) bool { return len(wanted) > 0 && !wanted[c.Name] })
		followed.Entries = slices.DeleteFunc(followed.Entries, func(e Entry) bool {
			return (o.NoLocal && e.Machine == LocalName()) || (len(wanted) > 0 && !wanted[e.Machine])
		})
		return followed
	}
	cached := a.CachedInventory()
	localEntries := make(chan []Entry, 1)
	var localListed sync.Once
	var publishMu sync.Mutex
	lastSaved := time.Time{}
	lastSavedScope := ""
	progress := o.Progress
	publish := func(u ScanUpdate) {
		publishMu.Lock()
		defer publishMu.Unlock()
		if u.Listed && u.Machine != nil && u.Machine.Local {
			localListed.Do(func() { localEntries <- slices.Clone(u.Entries) })
		}
		scope := a.CatalogScope()
		if !o.NoCache && (scope != lastSavedScope || time.Since(lastSaved) > 250*time.Millisecond || u.Complete) {
			update := &Inventory{Discovering: !u.Complete, Clouds: u.Clouds, Entries: u.Entries}
			if u.Machine != nil {
				update.Machines = []*Machine{u.Machine}
			}
			a.saveCatalog(update, started)
			lastSaved = time.Now()
			lastSavedScope = scope
		}
		if progress != nil {
			progress(u)
		}
	}
	if progress != nil {
		o.Progress = publish
	}
	a.tests.forget() // a sign-in since shows on the next plan
	inv := &Inventory{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	add := func(m *Machine, es []Entry) {
		m.Phase = "done"
		if m.Status == StatusOK {
			m.CheckedAt = time.Now().UTC()
		} else {
			m.Phase = "error"
		}
		for _, st := range m.Agents {
			if st.Error != "" {
				m.Phase = "error"
			}
		}
		mu.Lock()
		inv.Machines = append(inv.Machines, m)
		inv.Entries = append(inv.Entries, es...)
		mu.Unlock()
		publish(ScanUpdate{Machine: m, Entries: es, Complete: true})
	}
	want := map[string]bool{}
	for _, h := range o.Hosts {
		want[h] = true
	}
	local := sync.OnceValue(func() *host.Machine {
		if progress != nil {
			return &host.Machine{Name: LocalName(), Local: true, Facts: host.ProbeLocalFast(ctx, a.Specs()), Log: a.Log}
		}
		return a.localMachine(ctx)
	})
	if !o.NoLocal && (len(want) == 0 || want[LocalName()]) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			adopted, waiting := a.adoptWaiting(ctx, local())
			m, es := a.scanMachine(ctx, local(), "", o)
			add(m, es)
			mu.Lock()
			inv.Adopted, inv.Waiting = adopted, waiting
			mu.Unlock()
			localListed.Do(func() { localEntries <- es })
		}()
	} else {
		localEntries <- nil
	}
	var clouds []cloudRef
	for _, r := range a.clouds() {
		if len(want) == 0 || want[r.cloud.Name] {
			clouds = append(clouds, r)
		}
	}
	if len(clouds) > 0 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			mine := <-localEntries
			cloudMachine := local()
			if progress != nil {
				cloudMachine = a.localMachine(ctx)
				defer cloudMachine.Close()
			}
			cs, es := a.scanClouds(ctx, cloudMachine, clouds, knownClouds(mine, clouds, a.Pasted()), mine)
			mu.Lock()
			inv.Clouds = cs
			inv.Entries = append(inv.Entries, es...)
			mu.Unlock()
			publish(ScanUpdate{Clouds: cs, Entries: es, Complete: true})
		}()
	}
	for _, h := range a.Cfg.Hosts {
		if !h.Allowed || (len(want) > 0 && !want[h.Name]) {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			hm, err := a.Connect(ctx, h)
			if err != nil {
				m := &Machine{Kind: agent.AtMachine, Name: h.Name, Destination: h.Destination}
				m.Status, m.Error, m.Hint = classify(err, h)
				add(m, nil)
				return
			}
			add(a.scanMachine(ctx, hm, h.Destination, o))
		}()
	}
	wg.Wait()
	// Preserve last-known rows only for failed/incomplete sources.
	for _, m := range inv.Machines {
		merged := MergeDiscovery(cached, ScanUpdate{Machine: m, Entries: machineEntries(inv, m.Name), Complete: true})
		for _, e := range merged.Entries {
			if e.Machine == m.Name && e.Cached {
				inv.Entries = append(inv.Entries, e)
			}
		}
	}
	for _, c := range inv.Clouds {
		if c.Error != "" {
			for _, e := range cached.Entries {
				if e.Machine == c.Name {
					inv.Entries = append(inv.Entries, e)
				}
			}
		}
	}
	a.EnrichMovement(ctx, inv)
	sort.SliceStable(inv.Machines, func(i, j int) bool {
		if inv.Machines[i].Local != inv.Machines[j].Local {
			return inv.Machines[i].Local
		}
		return inv.Machines[i].Name < inv.Machines[j].Name
	})
	sort.SliceStable(inv.Entries, func(i, j int) bool {
		return inv.Entries[i].Session.LastActivity.After(inv.Entries[j].Session.LastActivity)
	})
	if !o.NoCache {
		a.saveCatalog(inv, started)
	}
	return inv
}

func machineEntries(inv *Inventory, name string) []Entry {
	var out []Entry
	for _, e := range inv.Entries {
		if e.Machine == name {
			out = append(out, e)
		}
	}
	return out
}

func (a *App) localMachine(ctx context.Context) *host.Machine {
	return &host.Machine{Name: LocalName(), Local: true, Facts: host.ProbeLocal(ctx, a.Specs()), Log: a.Log}
}

// Connect reaches a configured machine and probes it (one round trip).
func (a *App) Connect(ctx context.Context, h config.Host) (*host.Machine, error) {
	conn, err := transport.NewConn(h.Destination, a.StateDir, a.Audit)
	if err != nil {
		return nil, err
	}
	if h.TailscaleName != "" && h.TailscaleName != h.Destination {
		conn.Fallbacks = []string{h.TailscaleName}
	}
	if h.UsesPassword() {
		if a.Passwords == nil {
			conn.Close()
			return nil, errNeedsPassword
		}
		conn.Password = a.Passwords(h)
	}
	routes := loadRoutes(a.StateDir)
	if r := routes[h.Destination]; r != "" {
		conn.Prefer(r)
	}
	facts, err := host.ProbeRemote(ctx, conn, a.Specs())
	if conn.Override() != routes[h.Destination] {
		saveRoute(a.StateDir, h.Destination, conn.Override())
	}
	if err != nil {
		conn.Close()
		return nil, err
	}
	return &host.Machine{Name: h.Name, Conn: conn, Facts: facts, Log: a.Log}, nil
}

var errNeedsPassword = errors.New("this machine logs in with a password")

// scanMachine lists every enabled agent's sessions on a reached machine.
func (a *App) scanMachine(ctx context.Context, hm *host.Machine, dest string, o ScanOptions) (*Machine, []Entry) {
	m := &Machine{Kind: agent.AtMachine, Name: hm.Name, Destination: dest, Local: hm.Local, Status: StatusOK, OS: hm.Facts.OS, host: hm,
		Hopsesh: hopseshVersion(hm.Facts.Binaries[host.Hopsesh.Name]), Phase: "reading"}
	fsys, err := hm.FS(ctx)
	if err != nil {
		m.Status, m.Error, m.Hint = StatusError, "SFTP: "+err.Error(), "the machine's SSH server must allow the sftp subsystem"
		return m, nil
	}
	var entries []Entry
	for _, mod := range a.Modules() {
		spec := mod.Spec()
		st := AgentState{Agent: spec.ID, Name: spec.Name}
		h, err := hm.For(ctx, spec, agent.Install{}, nil)
		if err == nil {
			st.Install, err = mod.Detect(ctx, h)
		}
		if err != nil {
			st.Error = err.Error()
		}

		installs, scanErr := a.profileInstalls(ctx, hm, mod, st.Install, o.ForceAccounts, o.Progress != nil)
		if scanErr != nil {
			st.Error = scanErr.Error()
			m.Agents = append(m.Agents, st)
			continue
		}
		// Registering another default profile changes the inventory namespace.
		// Carry already discovered rows into that namespace before its listing.
		if o.Progress != nil && len(entries) > 0 {
			partial := *m
			partial.Agents = slices.Clone(m.Agents)
			partial.host = nil
			o.Progress(ScanUpdate{Machine: &partial, Entries: slices.Clone(entries)})
		}
		for _, in := range installs {
			state := AgentState{Agent: spec.ID, Name: spec.Name, Install: in, Error: st.Error}
			// A failed identity probe does not make a successful directory
			// enumeration incomplete. Keep auth errors on the profile card.
			if !in.Present && in.Profile != nil && in.Profile.Error != "" {
				state.Error = in.Profile.Error
			}
			m.Agents = append(m.Agents, state)
			if !in.Present {
				continue
			}
			ch, e := hm.For(ctx, spec, in, nil)
			if e == nil {
				var l agent.Listing
				listCtx := ctx
				// The module reports summaries before live/git/lineage enrichment.
				var batchMu sync.Mutex
				var batch []Entry
				firstBatch := true
				found := func(sum agent.Summary) {
					if o.Progress == nil {
						return
					}
					sum.Key.Profile = in.ProfileID()
					batchMu.Lock()
					defer batchMu.Unlock()
					batch = append(batch, Entry{Cached: true, Location: agent.MachineLocation(hm.Name), Machine: hm.Name, Agent: spec.ID, AgentName: spec.Name, Session: sum, Profile: in.Profile, Live: agent.LiveInfo{State: agent.Unknown}})
					if firstBatch || len(batch) >= 25 {
						partial := *m
						partial.Agents = append([]AgentState(nil), m.Agents...)
						partial.host = nil
						o.Progress(ScanUpdate{Machine: &partial, Entries: batch})
						batch = nil
						firstBatch = false
					}
				}
				if !o.NoCache {
					listCtx = a.listingContext(ctx, hm.Name, in, found)
				} else if o.Progress != nil {
					listCtx = agent.WithListingHooks(ctx, agent.ListingHooks{Found: found})
				}
				l, e = mod.List(listCtx, ch, in)
				if len(batch) > 0 && o.Progress != nil {
					partial := *m
					partial.Agents = append([]AgentState(nil), m.Agents...)
					partial.host = nil
					o.Progress(ScanUpdate{Machine: &partial, Entries: batch})
				}
				if e == nil {
					for i := range l.Sessions {
						l.Sessions[i].Key.Profile = in.ProfileID()
					}
					entries = append(entries, listedEntries(ctx, hm, fsys, mod, ch, state, l)...)
					if len(l.Errors) > 0 {
						e = fmt.Errorf("%d session files could not be read; first: %w", len(l.Errors), l.Errors[0].Err)
					}
				}
			}
			if e != nil {
				m.Agents[len(m.Agents)-1].Error = e.Error()
			}
		}
	}

	if o.Progress != nil {
		partial := *m
		partial.Agents = append([]AgentState(nil), m.Agents...)
		partial.host = nil
		o.Progress(ScanUpdate{Machine: &partial, Entries: entries, Listed: true})
	}
	if hm.Local && o.Progress != nil {
		endpoint := hm.Facts.Endpoint
		hm.Facts = host.ProbeLocal(ctx, a.Specs())
		hm.Facts.Endpoint = endpoint
		for i := range m.Agents {
			st := &m.Agents[i]
			if mod, ok := a.Module(st.Agent); ok {
				if h, err := hm.For(ctx, mod.Spec(), st.Install, nil); err == nil {
					if detailed, err := mod.Detect(ctx, h); err == nil {
						st.Install.Version = detailed.Version
						st.Install.Binary = detailed.Binary
						st.Install.Desktop = detailed.Desktop
						st.Install.DesktopVersion = detailed.DesktopVersion
						st.Install.DesktopWhy = detailed.DesktopWhy
					}
				}
			}
		}
	}
	if o.Progress != nil {
		for _, mod := range a.Modules() {
			h, err := hm.For(ctx, mod.Spec(), agent.Install{}, nil)
			if err != nil {
				continue
			}
			base, err := mod.Detect(ctx, h)
			if err != nil {
				continue
			}
			installs, err := a.profileInstalls(ctx, hm, mod, base, o.ForceAccounts)
			if err != nil {
				for i := range m.Agents {
					if m.Agents[i].Agent == mod.Spec().ID {
						m.Agents[i].Error = err.Error()
					}
				}
				continue
			}
			for _, in := range installs {
				for i := range m.Agents {
					st := &m.Agents[i]
					if st.Agent == mod.Spec().ID && st.Install.ProfileID() == in.ProfileID() {
						st.Install = in
					}
				}
				for i := range entries {
					if entries[i].Agent == mod.Spec().ID && entries[i].Session.Key.Profile == in.ProfileID() {
						entries[i].Profile = in.Profile
					}
				}
			}
		}
	}
	if !o.SkipGit {
		var dirs []string
		seen := map[string]bool{}
		for _, e := range entries {
			if d := e.Session.CWD; d != "" && !seen[d] && (o.GitFor == nil || o.GitFor(e)) {
				seen[d] = true
				dirs = append(dirs, d)
			}
		}
		states, err := hm.GitProbe(ctx, dirs, a.Reg.Worktrees())
		if err != nil && len(dirs) > 0 { // once more: a dropped connection is not "no repository"
			states, err = hm.GitProbe(ctx, dirs, a.Reg.Worktrees())
		}
		by := map[string]*repos.GitState{}
		for i := range states {
			by[states[i].Dir] = &states[i]
		}
		for i := range entries {
			if d := entries[i].Session.CWD; d != "" && seen[d] {
				setGit(&entries[i], by[d], err)
			}
		}
	}
	a.applyPending(ctx, m, entries)
	return m, entries
}

// setGit gives an entry its folder's git state. A probe that failed, or a folder git could
// not answer for in time, leaves the reason instead: unknown is not "no repository".
func setGit(e *Entry, g *repos.GitState, err error) {
	switch {
	case err != nil:
		e.Git, e.GitError = nil, err.Error()
	case g != nil && g.Error != "":
		e.Git, e.GitError = nil, g.Error
	default:
		e.Git, e.GitError = g, ""
	}
}

// hopseshVersion reads "hopsesh 0.3.0 (commit …)" from the probe ("" when not found;
// "installed" when it gave no version).
func hopseshVersion(b agent.BinaryFact) string {
	if b.Path == "" {
		return ""
	}
	if f := strings.Fields(b.Version); len(f) >= 2 && f[0] == "hopsesh" {
		return f[1]
	}
	return "installed"
}

// readManifests reads the lineage beside each session, in parallel.
func readManifests(fsys host.FS, ss []agent.Summary) ([]*lineage.Manifest, []string) {
	problems := make([]string, len(ss))
	out := make([]*lineage.Manifest, len(ss))
	var wg sync.WaitGroup
	sem := make(chan struct{}, 16)
	for i, s := range ss {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer func() { <-sem; wg.Done() }()
			var err error
			out[i], err = lineage.Read(fsys, s.Path)
			if err != nil {
				problems[i] = err.Error()
			}
		}()
	}
	wg.Wait()
	return out, problems
}

func classify(err error, h config.Host) (status, msg, hint string) {
	var tc *transport.TailscaleCheckError
	var ln *transport.LocalNetworkError
	switch {
	case errors.Is(err, errNeedsPassword):
		return StatusAuth, err.Error(), "run hopsesh in a terminal (it asks for the password), or set up key login: hopsesh hosts setup-key " + h.Name
	case errors.As(err, &ln):
		hint := ln.Hint()
		if h.TailscaleName == "" {
			hint += " Adding the machine by its Tailscale name avoids this."
		}
		return StatusLocalNet, err.Error(), hint
	case errors.Is(err, transport.ErrHostKeyUnknown):
		return StatusHostKey, err.Error(), "confirm its host key: hopsesh trust " + h.Name
	case errors.Is(err, transport.ErrHostKeyChanged):
		return StatusKeyChanged, err.Error(), "the host key changed since you trusted it; verify the machine before fixing known_hosts"
	case errors.Is(err, transport.ErrWrongPassword):
		return StatusAuth, err.Error(), "check the password and try again"
	case errors.Is(err, transport.ErrNoPasswordPrompt):
		return StatusAuth, err.Error(), "the machine may only allow keys; set up key login from a machine that can reach it, or turn on password login there"
	case errors.Is(err, transport.ErrPasswordCancelled):
		return StatusAuth, err.Error(), "enter the password to scan this machine"
	case errors.Is(err, transport.ErrAuth) && h.UsesPassword():
		return StatusAuth, err.Error(), "check the password, or set up key login: hopsesh hosts setup-key " + h.Name
	case errors.Is(err, transport.ErrAuth):
		return StatusAuth, err.Error(), "make sure `ssh " + h.Destination + "` works without a prompt (add your key to the agent), or if it logs in with a password: hopsesh hosts auth " + h.Name + " password"
	case errors.As(err, &tc):
		return StatusTSCheck, err.Error(), "open the URL, approve, then refresh"
	case errors.Is(err, transport.ErrUnreachable):
		return StatusUnreachable, err.Error(), "is it awake and on the network? (Remote Login / SSH server must be on)"
	case strings.Contains(err.Error(), "kex_exchange_identification"):
		return StatusError, err.Error(), "the machine closed the connection before login; OpenSSH 9.8 and later refuses new connections for a while after failed logins, so wait a minute and try again (otherwise its SSH server may be limiting connections)"
	}
	return StatusError, err.Error(), ""
}

var routesMu sync.Mutex

// routes remember which host name reached a destination last time (e.g. its Tailscale
// name when the alias points at an unresolvable .local name), saving a failed lookup.
func loadRoutes(stateDir string) map[string]string {
	routesMu.Lock()
	defer routesMu.Unlock()
	m := map[string]string{}
	if b, err := os.ReadFile(filepath.Join(stateDir, "routes.json")); err == nil {
		_ = json.Unmarshal(b, &m)
	}
	return m
}

func saveRoute(stateDir, dest, via string) {
	routesMu.Lock()
	defer routesMu.Unlock()
	p := filepath.Join(stateDir, "routes.json")
	m := map[string]string{}
	if b, err := os.ReadFile(p); err == nil {
		_ = json.Unmarshal(b, &m)
	}
	if via == "" {
		delete(m, dest)
	} else {
		m[dest] = via
	}
	b, _ := json.MarshalIndent(m, "", "  ")
	_ = os.MkdirAll(stateDir, 0o700)
	_ = os.WriteFile(p, b, 0o600)
}

// Status is a session's state in words: "live …", where it went ("moved to studio"),
// "ended", or a cloud session's state ("running", "state unknown").
func (e Entry) Status() string {
	if e.Cached {
		return "state unknown"
	}
	switch {
	case e.Cloud != nil:
		if e.Cloud.State == agent.CloudUnknown || e.Cloud.State == "" {
			return "state unknown"
		}
		return string(e.Cloud.State)
	case e.Live.State == agent.Live:
		if e.Live.Status == "" {
			return "live running"
		}
		return "live " + e.Live.Status
	case e.Session.Mark != nil:
		return MarkWords(*e.Session.Mark)
	}
	return "ended"
}

// MarkWords is a mark in words ("moved to studio", "continued in Codex on studio").
func MarkWords(m agent.Mark) string { return strings.TrimPrefix(agent.MarkTitle(m, ""), "↪ ") }

// listedEntries are one agent's listed sessions on a machine, with their live state and
// lineage.
func listedEntries(ctx context.Context, hm *host.Machine, fsys host.FS, mod agent.Module, ch agent.Host, st AgentState, l agent.Listing) []Entry {
	spec := mod.Spec()
	var out []Entry
	live := map[agent.SessionID]agent.LiveInfo{}
	if ld, ok := mod.(agent.LiveDetector); ok && len(l.Sessions) > 0 {
		ids := make([]agent.SessionID, len(l.Sessions))
		for i, s := range l.Sessions {
			ids[i] = s.Key.Session
		}
		live, _ = ld.Live(ctx, ch, st.Install, ids)
	}
	manifests, problems := readManifests(fsys, l.Sessions)
	persistedFamilies := map[string]bool{}
	for _, m := range manifests {
		if m != nil {
			persistedFamilies[m.Family] = true
		}
	}
	unsupported := make([]bool, len(problems))
	for i, err := range problems {
		unsupported[i] = err != ""
	}
	nativeForkManifests(ctx, hm, ch, mod, st.Install, l.Sessions, manifests, problems)
	for i, s := range l.Sessions {
		lv := live[s.Key.Session]
		if lv.State == "" {
			lv.State = agent.Unknown
		}
		nativeOnly := manifests[i] != nil && !persistedFamilies[manifests[i].Family]
		out = append(out, Entry{NativeRelationship: nativeOnly, ObservedAt: time.Now().UTC(), Location: agent.MachineLocation(hm.Name), Machine: hm.Name, Agent: spec.ID, AgentName: spec.Name, Profile: st.Install.Profile, Session: s, Live: lv, Lineage: manifests[i], LineageError: problems[i], CanArchiveLineage: unsupported[i]})
	}
	return out
}
