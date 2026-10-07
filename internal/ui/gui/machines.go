package gui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/roeehrl/hopsesh/internal/app"
	"github.com/roeehrl/hopsesh/internal/config"
	"github.com/roeehrl/hopsesh/internal/core/hosts"
	"github.com/roeehrl/hopsesh/internal/core/secrets"
	"github.com/roeehrl/hopsesh/internal/core/transport"
	"github.com/roeehrl/hopsesh/internal/version"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

// HereDTO is this machine on the Machines screen.
type HereDTO struct {
	Name    string   `json:"name"`
	Agents  []string `json:"agents"`
	Hopsesh string   `json:"hopsesh"`
	Receive bool     `json:"receive"`
}

// MachineRow is a machine you added: how hopsesh logs in, and what the last scan found.
type MachineRow struct {
	Relay       bool        `json:"relay"`
	Receive     *bool       `json:"receive,omitempty"`
	Name        string      `json:"name"`
	Destination string      `json:"destination"`
	OS          string      `json:"os"`
	Auth        string      `json:"auth"`     // key | password
	Keychain    bool        `json:"keychain"` // the password is remembered
	CanRemember bool        `json:"canRemember"`
	Scanned     bool        `json:"scanned"` // the last scan reached for it
	Scan        MachineScan `json:"scan"`
	Status      string      `json:"status"` // the last scan's (app.Status*)
	Hint        string      `json:"hint"`
	Error       string      `json:"error"`
	Agents      []string    `json:"agents"`
	Hopsesh     string      `json:"hopsesh"`
	Sessions    int         `json:"sessions"`
}

// FoundRow is a machine discovery found that is not added.
type FoundRow struct {
	Name        string   `json:"name"`
	Destination string   `json:"destination"`
	Via         []string `json:"via"`
	OS          string   `json:"os"`
	Online      *bool    `json:"online"`
	Owner       string   `json:"owner"` // shared by someone else
}

// MachinesDTO is the Machines screen.
type MachinesDTO struct {
	Here     HereDTO      `json:"here"`
	Machines []MachineRow `json:"machines"`
	Found    []FoundRow   `json:"found"`
	// Clouds are the agents' clouds hopsesh can do something with, as the last scan found
	// them (nil before a scan).
	Clouds []CloudDTO `json:"clouds"`
}

// Machines lists this machine, the machines you added (with the last scan's findings) and
// the ones discovery found (connecting to none).
func (a *App) Machines() MachinesDTO {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cands, _ := hosts.Discover(ctx)
	a.mu.Lock()
	defer a.mu.Unlock()
	out := MachinesDTO{Here: HereDTO{Name: app.LocalName(), Hopsesh: version.Version, Receive: a.core.Cfg.Peer.Receive, Agents: []string{}},
		Machines: []MachineRow{}, Found: []FoundRow{}, Clouds: []CloudDTO{}}
	if a.inv != nil {
		core := *a.core
		out.Clouds = shownClouds(&core, a.inv)
		for i := range out.Clouds {
			out.Clouds[i].Allowed = a.core.Cfg.CloudAllowed(out.Clouds[i].Name) // as set since the scan
		}
	}
	var scanned map[string]*app.Machine
	if a.inv != nil {
		scanned = map[string]*app.Machine{}
		for _, m := range a.inv.Machines {
			scanned[m.Name] = m
		}
		if here := a.inv.Local(); here != nil {
			out.Here.Agents = agentNames(here)
		}
	}
	added := map[string]bool{}
	for _, h := range a.core.Cfg.Hosts {
		if !h.Allowed {
			continue
		}
		added[h.Name] = true
		r := MachineRow{Name: h.Name, Destination: h.Destination, OS: h.OS, Auth: "key", Keychain: h.Keychain, CanRemember: secrets.Available(), Agents: []string{}}
		if h.RelayID != "" {
			r.Relay = true
			r.Destination = "Internet relay"
		}
		r.Scan = a.scans[h.Name]
		if h.UsesPassword() {
			r.Auth = "password"
		}
		if m := scanned[h.Name]; m != nil {
			r.Receive = m.Receive
			r.Scanned, r.Status, r.Hint, r.Error, r.Hopsesh = true, m.Status, m.Hint, m.Error, m.Hopsesh
			r.Agents = agentNames(m)
			if m.OS != "" {
				r.OS = m.OS
			}
			for _, e := range a.inv.Entries {
				if e.Machine == m.Name {
					r.Sessions++
				}
			}
		}
		out.Machines = append(out.Machines, r)
	}
	for _, c := range cands {
		if c.Self || added[c.Name] {
			continue
		}
		f := FoundRow{Name: c.Name, Destination: c.Destination, Via: c.Via, OS: c.OS, Online: c.Online}
		if c.OtherOwner {
			f.Owner = c.Owner
		}
		if h := a.core.Cfg.FindHost(c.Name); h != nil && h.TailscaleName == "" && c.DNSName != "" {
			h.TailscaleName = c.DNSName
		}
		out.Found = append(out.Found, f)
	}
	return out
}

// hasAgent reports whether an agent is on a machine: its data or its program. A cloud-only
// module (no data folders anywhere) has only its cloud's driver there, which the Clouds
// cards show.
func hasAgent(st app.AgentState) bool {
	return len(st.Install.Roots) > 0 && (st.Install.Present || st.Install.Binary != "")
}

func agentNames(m *app.Machine) []string {
	out := []string{}
	seen := map[agent.ID]bool{}
	for _, st := range m.Agents {
		if hasAgent(st) && !seen[st.Agent] {
			seen[st.Agent] = true
			out = append(out, strings.TrimSpace(st.Name+" "+st.Install.Version))
		}
	}
	return out
}

// machineAgentNames labels capabilities found by the scan, including agents with
// no sessions. Profiles do not duplicate an agent in a machine's subtitle.
func machineAgentNames(m *app.Machine) []string {
	out := []string{}
	seen := map[agent.ID]bool{}
	for _, st := range m.Agents {
		if hasAgent(st) && !seen[st.Agent] {
			seen[st.Agent] = true
			out = append(out, st.Name)
		}
	}
	slices.Sort(out)
	return out
}

// SetReceive lets (or stops letting) hopsesh on your other machines send sessions here.
func (a *App) SetReceive(on bool) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.core.Cfg.Peer.Receive = on
	return a.save()
}

// RemoveHost forgets a machine: hopsesh stops connecting to it and drops its remembered
// password (discovery may still list it).
func (a *App) RemoveHost(name string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	h := a.core.Cfg.FindHost(name)
	if h == nil {
		return fmt.Errorf("unknown machine %q", name)
	}
	_ = secrets.Delete(secrets.Account(h.Name, h.Destination))
	a.forget(h.Destination)
	a.core.Cfg.Hosts = slices.DeleteFunc(a.core.Cfg.Hosts, func(x config.Host) bool { return x.Name == name })
	return a.save()
}

// SetAllowed records consent for a machine.
func (a *App) SetAllowed(name, destination string, allowed bool) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	h := a.core.Cfg.FindHost(name)
	if h == nil {
		a.core.Cfg.Hosts = append(a.core.Cfg.Hosts, config.Host{Name: name, Destination: destination, Via: "gui"})
		h = a.core.Cfg.FindHost(name)
	}
	h.Allowed = allowed
	return a.save()
}

// AddHost adds and allows a machine by ssh destination. password: it logs in with a
// password (asked for when hopsesh connects; remember keeps it in the Keychain).
func (a *App) AddHost(name, destination string, password, remember bool) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	h := config.Host{Name: name, Destination: destination, Via: "manual", Allowed: true}
	if password {
		h.Auth, h.Keychain = "password", remember && secrets.Available()
	}
	a.core.Cfg.UpsertHost(h)
	return a.save()
}

// KeysDTO describes host keys awaiting confirmation.
type KeysDTO struct {
	Host         string   `json:"host"`
	Address      string   `json:"address"`
	Fingerprints []string `json:"fingerprints"`
	Verified     string   `json:"verified"` // how it was verified, "" if not
}

func (a *App) conn(name string) (*transport.Conn, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	h := a.core.Cfg.FindHost(name)
	dest := name
	if h != nil {
		dest = h.Destination
	}
	c, err := transport.NewConn(dest, config.StateDir(), a.core.Audit)
	if err != nil {
		return nil, err
	}
	if h != nil && h.TailscaleName != "" && h.TailscaleName != dest {
		c.Fallbacks = []string{h.TailscaleName}
	}
	return c, nil
}

// ScanKeys fetches a machine's host keys for the trust dialog.
func (a *App) ScanKeys(name string) (*KeysDTO, error) {
	c, err := a.conn(name)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second) // room for the macOS prompt
	defer cancel()
	keys, r, err := c.ScanHostKeys(ctx)
	if err != nil {
		var ln *transport.LocalNetworkError
		if errors.As(err, &ln) {
			return nil, fmt.Errorf("%v. %s", err, ln.Hint())
		}
		return nil, err
	}
	d := &KeysDTO{Host: name, Address: r.HostName + ":" + r.Port}
	for _, k := range keys {
		d.Fingerprints = append(d.Fingerprints, k.Type+" "+k.Fingerprint)
	}
	cands, _ := hosts.Discover(ctx)
	for _, cd := range cands {
		if cd.Name == name && transport.MatchesTailscale(keys, cd.SSHHostKeys) {
			d.Verified = "matches the key Tailscale reports for this machine"
		}
	}
	if d.Verified == "" {
		home, _ := os.UserHomeDir()
		if k := transport.KnownElsewhere(keys, filepath.Join(home, ".ssh", "known_hosts")); len(k) > 0 {
			slices.Sort(k)
			d.Verified = "matches a key you already trust for " + strings.Join(slices.Compact(k), ", ")
		}
	}
	return d, nil
}

// TrustHost re-scans and records the machine's host keys (after the user confirmed).
func (a *App) TrustHost(name string) error {
	c, err := a.conn(name)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	keys, _, err := c.ScanHostKeys(ctx)
	if err != nil {
		return err
	}
	return c.Trust(keys)
}
