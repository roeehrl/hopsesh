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
	"github.com/roeehrl/hopsesh/internal/core/integrate"
	"github.com/roeehrl/hopsesh/internal/core/proc"
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
	// Storage is what each agent's sessions take here.
	Storage []app.AgentUsage `json:"storage"`
}

// MachineRow is a machine you added: how hopsesh logs in, and what the last scan found.
type MachineRow struct {
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
	// Storage is what each agent's sessions take there.
	Storage []app.AgentUsage `json:"storage"`
	// Presence is how hopsesh works with it (app.Presence*; "" before a scan reached it),
	// and Can what that lets you do there.
	Presence    string       `json:"presence,omitempty"`
	PresenceWhy string       `json:"presenceWhy,omitempty"`
	Can         []AbilityDTO `json:"can"`
}

// AbilityDTO is one thing hopsesh can (or cannot) do on a machine.
type AbilityDTO struct {
	Text string `json:"text"`
	OK   bool   `json:"ok"`
}

// canOn is what hopsesh can do on a machine, as it works with it.
func canOn(presence, os string) []AbilityDTO {
	switch presence {
	case app.PresenceLogin:
		return []AbilityDTO{{"Lists sessions, live and moved", true}, {"Checks sign-ins there" + map[bool]string{true: " (Keychain included)"}[os == "darwin"], true},
			{"Protects originals left there", true}, {"Runs sessions there; you work in them here", true}, {"Receives sessions you send", true}}
	case app.PresenceInstalled, app.PresenceOlder:
		return []AbilityDTO{{"Lists sessions", true}, {"Brings sessions here", true}, {"Receives sessions you send", presence == app.PresenceInstalled},
			{"Checks sign-ins there" + map[bool]string{true: ": the Keychain is locked to SSH"}[os == "darwin"], os != "darwin"}, {"Runs sessions there", false}}
	case app.PresenceSSH:
		return []AbilityDTO{{"Lists sessions (no live status)", true}, {"Brings sessions here", true},
			{map[bool]string{true: "Sign-ins: the Keychain is locked to SSH", false: "Checks sign-ins over SSH"}[os == "darwin"], os != "darwin"},
			{"Originals left there aren't protected", false}, {"Can't run sessions there", false}, {"Can't receive sessions", false}}
	}
	return []AbilityDTO{}
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
	DiscoveryError string       `json:"discoveryError,omitempty"`
	Here           HereDTO      `json:"here"`
	Machines       []MachineRow `json:"machines"`
	Found          []FoundRow   `json:"found"`
	// Clouds are the agents' clouds hopsesh can do something with, as the last scan found
	// them (nil before a scan).
	Clouds []CloudDTO `json:"clouds"`
}

// Machines lists this machine, the machines you added (with the last scan's findings) and
// the ones discovery found (connecting to none).
func (a *App) Machines() MachinesDTO {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cands, discoveryErr := hosts.Discover(ctx)
	a.mu.Lock()
	defer a.mu.Unlock()
	out := MachinesDTO{Here: HereDTO{Name: app.LocalName(), Hopsesh: version.Version, Receive: a.core.Cfg.Peer.Receive, Agents: []string{}, Storage: []app.AgentUsage{}},
		Machines: []MachineRow{}, Found: []FoundRow{}, Clouds: []CloudDTO{}}
	if discoveryErr != nil {
		out.DiscoveryError = discoveryErr.Error()
	}
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
			out.Here.Storage = a.inv.Usage(here.Name)
		}
	}
	added := map[string]bool{}
	for _, h := range a.core.Cfg.Hosts {
		if !h.Allowed {
			continue
		}
		added[h.Name] = true
		r := MachineRow{Name: h.Name, Destination: h.Destination, OS: h.OS, Auth: "key", Keychain: h.Keychain, CanRemember: secrets.Available(), Agents: []string{}, Storage: []app.AgentUsage{}, Can: []AbilityDTO{}}
		r.Scan = a.scans[h.Name]
		if h.UsesPassword() {
			r.Auth = "password"
		}
		if m := scanned[h.Name]; m != nil {
			r.Scanned, r.Status, r.Hint, r.Error, r.Hopsesh = true, m.Status, m.Hint, m.Error, m.Hopsesh
			r.Presence, r.PresenceWhy = m.Presence, m.PresenceWhy
			r.Agents = agentNames(m)
			if m.OS != "" {
				r.OS = m.OS
			}
			r.Can = canOn(r.Presence, r.OS)
			for _, e := range a.inv.Entries {
				if e.Machine == m.Name {
					r.Sessions++
				}
			}
			r.Storage = a.inv.Usage(m.Name)
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

// StartAtLogin has this machine start hopsesh serve at each login, and now (hopsesh
// serve install, run by the app's own command), so it receives sessions while the app
// is closed. Only on the user's click.
func (a *App) StartAtLogin() error {
	cli, err := integrate.AppCLI()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	if out, err := proc.CommandContext(ctx, cli, "serve", "install").CombinedOutput(); err != nil {
		return fmt.Errorf("hopsesh serve install: %s", strings.TrimSpace(string(out)))
	}
	return nil
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

// MachineSetupEvent tells the window each step of setting hopsesh up on a machine.
const MachineSetupEvent = "hopsesh:machine-setup"

// MachineSetupStep is one such step.
type MachineSetupStep struct {
	Machine string        `json:"machine"`
	Step    app.SetupStep `json:"step"`
}

// SetUpMachine installs (or updates) hopsesh on a machine you added and starts it in the
// login session there, after the user asked; each step is told as it happens. The
// machine is scanned again after.
func (a *App) SetUpMachine(name string) (*app.MachineSetup, error) {
	core := a.snapshot()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	out, err := core.SetUpMachine(ctx, name, func(s app.SetupStep) {
		a.emit(MachineSetupEvent, MachineSetupStep{Machine: name, Step: s})
	})
	if err == nil {
		go func() { _ = a.ScanMachine(name) }()
	}
	return out, err
}
