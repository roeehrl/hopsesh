package app

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"github.com/roeehrl/hopsesh/internal/config"
	"github.com/roeehrl/hopsesh/internal/core/host"
	"github.com/roeehrl/hopsesh/internal/core/lineage"
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
	Name        string       `json:"name"`
	Destination string       `json:"destination,omitempty"`
	Local       bool         `json:"local"`
	Status      string       `json:"status"`
	Error       string       `json:"error,omitempty"`
	Hint        string       `json:"hint,omitempty"`
	OS          string       `json:"os,omitempty"`
	Agents      []AgentState `json:"agents"`

	host *host.Machine
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
	for _, a := range m.Agents {
		if a.Agent == id && a.Install.Present {
			return a.Install, true
		}
	}
	return agent.Install{}, false
}

// Entry is one session on one machine.
type Entry struct {
	Machine   string            `json:"machine"`
	Agent     agent.ID          `json:"agent"`
	AgentName string            `json:"agentName"`
	Session   agent.Summary     `json:"session"`
	Live      agent.LiveInfo    `json:"live"`
	Git       *repos.GitState   `json:"git,omitempty"`
	Lineage   *lineage.Manifest `json:"lineage,omitempty"`
}

// Inventory is the result of a scan.
type Inventory struct {
	Machines []*Machine `json:"machines"`
	Entries  []Entry    `json:"entries"`
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
	Hosts   []string // only these machines ("" or none: every allowed one)
	NoLocal bool     // leave this machine out
	SkipGit bool     // no git state (faster)
}

// Scan reads this machine and the allowed machines in parallel. A machine that cannot be
// read is returned with a status and a hint, not an error.
func (a *App) Scan(ctx context.Context, o ScanOptions) *Inventory {
	inv := &Inventory{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	add := func(m *Machine, es []Entry) {
		mu.Lock()
		inv.Machines = append(inv.Machines, m)
		inv.Entries = append(inv.Entries, es...)
		mu.Unlock()
	}
	want := map[string]bool{}
	for _, h := range o.Hosts {
		want[h] = true
	}
	if !o.NoLocal && (len(want) == 0 || want[LocalName()]) {
		wg.Add(1)
		go func() { defer wg.Done(); add(a.scanMachine(ctx, a.localMachine(ctx), "", o)) }()
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
				m := &Machine{Name: h.Name, Destination: h.Destination}
				m.Status, m.Error, m.Hint = classify(err, h)
				add(m, nil)
				return
			}
			add(a.scanMachine(ctx, hm, h.Destination, o))
		}()
	}
	wg.Wait()
	sort.SliceStable(inv.Machines, func(i, j int) bool {
		if inv.Machines[i].Local != inv.Machines[j].Local {
			return inv.Machines[i].Local
		}
		return inv.Machines[i].Name < inv.Machines[j].Name
	})
	sort.SliceStable(inv.Entries, func(i, j int) bool {
		return inv.Entries[i].Session.LastActivity.After(inv.Entries[j].Session.LastActivity)
	})
	return inv
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
	m := &Machine{Name: hm.Name, Destination: dest, Local: hm.Local, Status: StatusOK, OS: hm.Facts.OS, host: hm}
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
		m.Agents = append(m.Agents, st)
		if !st.Install.Present {
			continue
		}
		ch, err := hm.For(ctx, spec, st.Install, nil)
		if err != nil {
			continue
		}
		l, err := mod.List(ctx, ch, st.Install)
		if err != nil {
			m.Agents[len(m.Agents)-1].Error = err.Error()
			continue
		}
		live := map[agent.SessionID]agent.LiveInfo{}
		if ld, ok := mod.(agent.LiveDetector); ok && len(l.Sessions) > 0 {
			ids := make([]agent.SessionID, len(l.Sessions))
			for i, s := range l.Sessions {
				ids[i] = s.Key.Session
			}
			live, _ = ld.Live(ctx, ch, st.Install, ids)
		}
		manifests := readManifests(fsys, l.Sessions)
		for i, s := range l.Sessions {
			lv := live[s.Key.Session]
			if lv.State == "" {
				lv.State = agent.Unknown
			}
			entries = append(entries, Entry{Machine: hm.Name, Agent: spec.ID, AgentName: spec.Name, Session: s, Live: lv, Lineage: manifests[i]})
		}
	}
	if !o.SkipGit {
		var dirs []string
		seen := map[string]bool{}
		for _, e := range entries {
			if d := e.Session.CWD; d != "" && !seen[d] {
				seen[d] = true
				dirs = append(dirs, d)
			}
		}
		if states, err := hm.GitProbe(ctx, dirs, a.Reg.Worktrees()); err == nil {
			by := map[string]*repos.GitState{}
			for i := range states {
				by[states[i].Dir] = &states[i]
			}
			for i := range entries {
				entries[i].Git = by[entries[i].Session.CWD]
			}
		}
	}
	a.applyPending(ctx, m, entries)
	return m, entries
}

// readManifests reads the lineage beside each session, in parallel.
func readManifests(fsys host.FS, ss []agent.Summary) []*lineage.Manifest {
	out := make([]*lineage.Manifest, len(ss))
	var wg sync.WaitGroup
	sem := make(chan struct{}, 16)
	for i, s := range ss {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer func() { <-sem; wg.Done() }()
			out[i], _ = lineage.Read(fsys, s.Path)
		}()
	}
	wg.Wait()
	return out
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
