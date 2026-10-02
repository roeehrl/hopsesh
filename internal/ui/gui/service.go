// Package gui is the backend of the hopsesh desktop app (Wails v3). The frontend in
// assets/ calls these methods; every use case is the app layer's, as for the command
// line.
package gui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/roeehrl/hopsesh/internal/app"
	"github.com/roeehrl/hopsesh/internal/config"
	"github.com/roeehrl/hopsesh/internal/core/audit"
	"github.com/roeehrl/hopsesh/internal/core/hosts"
	"github.com/roeehrl/hopsesh/internal/core/lnp"
	"github.com/roeehrl/hopsesh/internal/core/move"
	"github.com/roeehrl/hopsesh/internal/core/registry"
	"github.com/roeehrl/hopsesh/internal/core/secrets"
	"github.com/roeehrl/hopsesh/internal/core/transport"
	"github.com/roeehrl/hopsesh/internal/update"
	"github.com/roeehrl/hopsesh/internal/version"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

// App is the service bound to the frontend.
type App struct {
	mu     sync.Mutex
	core   *app.App // its Cfg is the saved configuration; calls work on snapshots
	cfgErr error    // the configuration file could not be used (see StartFresh)
	inv    *app.Inventory
	plan   *move.Plan
	input  move.Input
	res    *move.Result
	pw     *pwBroker
	pwOnce sync.Once
	// Wails is the running application (events, clipboard, dialogs).
	Wails *application.App `json:"-"`
}

// NewApp loads the configuration for the modules in reg. A configuration an older hopsesh
// wrote is reported by Info, not returned: the window offers to start fresh.
func NewApp(reg *registry.Registry) *App {
	cfg, err := config.Load()
	log, _ := audit.Open(filepath.Join(config.StateDir(), "log"))
	a := &App{cfgErr: err}
	a.core = app.New(cfg, reg, config.StateDir(), log)
	a.core.Passwords = a.passwordFor
	return a
}

// snapshot is the app layer with a private copy of the configuration, for calls that run
// while the window changes settings.
func (a *App) snapshot() *app.App {
	a.mu.Lock()
	defer a.mu.Unlock()
	c := *a.core
	c.Cfg.Hosts = slices.Clone(a.core.Cfg.Hosts)
	c.Cfg.Agents = maps.Clone(a.core.Cfg.Agents)
	return &c
}

// save stores the configuration (callers hold a.mu).
func (a *App) save() error {
	if a.cfgErr != nil {
		return a.cfgErr // never overwrite a file the user has not set aside
	}
	return config.Save(a.core.Cfg)
}

// AgentDTO is one agent module.
type AgentDTO struct {
	ID            agent.ID           `json:"id"`
	Name          string             `json:"name"`
	Stability     agent.Stability    `json:"stability"`
	Enabled       bool               `json:"enabled"`
	RemoteControl bool               `json:"remoteControl"`
	Capabilities  []agent.Capability `json:"capabilities"`
}

// Info is static information for the window.
type Info struct {
	Version     string     `json:"version"`
	Host        string     `json:"host"`
	ReposDir    string     `json:"reposDir"`
	AuditDir    string     `json:"auditDir"`
	HasHosts    bool       `json:"hasHosts"`
	ConfigError string     `json:"configError,omitempty"`
	Agents      []AgentDTO `json:"agents"`
	// LocalNetwork describes macOS local network privacy: gated (macOS 15+) and firstRun
	// (the prompt has probably not been answered yet).
	LocalNetwork struct {
		Gated    bool `json:"gated"`
		FirstRun bool `json:"firstRun"`
	} `json:"localNetwork"`
	UpdateCheck string `json:"updateCheck"` // "", "on" or "off"
	SkillState  string `json:"skillState"`  // across every agent: absent | current | stale | modified | foreign | broken
	SkillPrompt string `json:"skillPrompt"` // "declined" once the user said not now
	CLIOffer    bool   `json:"cliOffer"`    // offer to link the command-line tool
	Defaults    struct {
		MarkMoved  bool `json:"markMoved"`
		SyncCode   bool `json:"syncCode"`
		PushSource bool `json:"pushSource"`
	} `json:"defaults"`
}

// Info returns app and machine information.
func (a *App) Info() Info {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	files, bin := a.skillFiles()
	skill := a.snapshot().Skill(ctx, files, bin)
	a.mu.Lock()
	defer a.mu.Unlock()
	cfg := a.core.Cfg
	info := Info{Version: version.Version, Host: app.LocalName(), ReposDir: cfg.ReposDir,
		AuditDir: filepath.Join(config.StateDir(), "log"), UpdateCheck: cfg.UpdateCheck,
		SkillState: skill.State, SkillPrompt: cfg.SkillPrompt}
	if a.cfgErr != nil {
		info.ConfigError = a.cfgErr.Error()
	}
	for _, h := range cfg.Hosts {
		info.HasHosts = info.HasHosts || h.Allowed
	}
	info.Agents = a.agentsLocked()
	if cfg.CLIPrompt != "declined" {
		info.CLIOffer = cliOffer()
	}
	info.Defaults.MarkMoved, info.Defaults.SyncCode, info.Defaults.PushSource = cfg.MarkMovedOn(), cfg.SyncCodeOn(), cfg.PushSource
	info.LocalNetwork.Gated = lnp.Gated()
	info.LocalNetwork.FirstRun = lnp.FirstRun(config.StateDir())
	return info
}

func (a *App) agentsLocked() []AgentDTO {
	var out []AgentDTO
	for _, m := range a.core.Reg.All() {
		s := m.Spec()
		out = append(out, AgentDTO{ID: s.ID, Name: s.Name, Stability: s.Stability, Enabled: a.core.Cfg.AgentEnabled(string(s.ID)),
			RemoteControl: a.core.Cfg.Agents[string(s.ID)].RemoteControl, Capabilities: agent.Capabilities(m)})
	}
	return out
}

// StartFresh sets aside a configuration file an older hopsesh wrote and starts with
// defaults (machines are added again). It returns where the old file went.
func (a *App) StartFresh() (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !errors.Is(a.cfgErr, config.ErrOldConfig) {
		return "", errors.New("the configuration is in use; nothing to set aside")
	}
	old, err := config.SetAside()
	if err != nil {
		return "", err
	}
	cfg, err := config.Load()
	if err != nil {
		return old, err
	}
	a.core.Cfg, a.cfgErr = cfg, nil
	return old, nil
}

// SetUpdateCheck records whether the app may look for new releases once a day.
func (a *App) SetUpdateCheck(on bool) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.core.Cfg.UpdateCheck = map[bool]string{true: "on", false: "off"}[on]
	return a.save()
}

// UpdateDTO is the result of an update check.
type UpdateDTO struct {
	Latest string `json:"latest"`
	Newer  bool   `json:"newer"`
	URL    string `json:"url"`
}

// CheckUpdate looks for a newer release, at most once a day, and only when allowed.
func (a *App) CheckUpdate() (*UpdateDTO, error) {
	a.mu.Lock()
	on := a.core.Cfg.UpdateCheck == "on"
	a.mu.Unlock()
	if !on {
		return &UpdateDTO{}, nil
	}
	stamp := filepath.Join(config.StateDir(), "update-check.json")
	var last struct {
		At  time.Time `json:"at"`
		DTO UpdateDTO `json:"result"`
	}
	if b, err := os.ReadFile(stamp); err == nil && json.Unmarshal(b, &last) == nil && time.Since(last.At) < 24*time.Hour {
		last.DTO.Newer = update.Newer(last.DTO.Latest, version.Version)
		return &last.DTO, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	rel, err := update.Latest(ctx)
	if err != nil {
		return &UpdateDTO{}, nil // quiet: offline or nothing published
	}
	last.At, last.DTO = time.Now(), UpdateDTO{Latest: rel.Version, URL: rel.URL, Newer: update.Newer(rel.Version, version.Version)}
	if b, err := json.Marshal(last); err == nil {
		_ = os.WriteFile(stamp, b, 0o600)
	}
	return &last.DTO, nil
}

// OpenURL opens a web page in the default browser (release notes only).
func (a *App) OpenURL(url string) error {
	if !strings.HasPrefix(url, "https://github.com/"+update.Repo+"/") {
		return errors.New("only hopsesh release pages can be opened")
	}
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", url).Run()
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Run()
	}
	return exec.Command("xdg-open", url).Run()
}

// OpenLocalNetworkSettings opens System Settings at Privacy & Security, where the Local
// Network list is (macOS offers no link to the list itself).
func (a *App) OpenLocalNetworkSettings() error {
	if runtime.GOOS != "darwin" {
		return errors.New("only macOS has a Local Network setting")
	}
	return exec.Command("open", lnp.SettingsURL).Run()
}

// RetryLocalNetwork makes the next scan wait for the macOS prompt again, for someone who
// just changed the setting or wants to answer a prompt they dismissed.
func (a *App) RetryLocalNetwork() {
	lnp.ResetWait(config.StateDir()) // each scan makes new connections, so nothing else is cached
}

// HostDTO is one machine on the consent screen.
type HostDTO struct {
	Name        string   `json:"name"`
	Destination string   `json:"destination"`
	Via         []string `json:"via"`
	OS          string   `json:"os"`
	Online      *bool    `json:"online"`
	OtherOwner  bool     `json:"otherOwner"`
	Owner       string   `json:"owner"`
	Allowed     bool     `json:"allowed"`
	Auth        string   `json:"auth"`     // "key" or "password"
	Keychain    bool     `json:"keychain"` // the password is remembered
	CanRemember bool     `json:"canRemember"`
}

// Discover lists machines (connecting to none) with their consent state.
func (a *App) Discover() []HostDTO {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cands, _ := hosts.Discover(ctx)
	a.mu.Lock()
	defer a.mu.Unlock()
	var out []HostDTO
	seen := map[string]bool{}
	dto := func(d HostDTO, h *config.Host) HostDTO {
		d.Auth, d.CanRemember = "key", secrets.Available()
		if h != nil {
			d.Allowed = h.Allowed
			if h.UsesPassword() {
				d.Auth, d.Keychain = "password", h.Keychain
			}
		}
		return d
	}
	for _, c := range cands {
		if c.Self {
			continue
		}
		h := a.core.Cfg.FindHost(c.Name)
		if h != nil && h.TailscaleName == "" && c.DNSName != "" {
			h.TailscaleName = c.DNSName
		}
		seen[c.Name] = true
		out = append(out, dto(HostDTO{Name: c.Name, Destination: c.Destination, Via: c.Via, OS: c.OS, Online: c.Online, OtherOwner: c.OtherOwner, Owner: c.Owner}, h))
	}
	for i := range a.core.Cfg.Hosts {
		h := &a.core.Cfg.Hosts[i]
		if !seen[h.Name] {
			out = append(out, dto(HostDTO{Name: h.Name, Destination: h.Destination, Via: []string{h.Via}, OS: h.OS}, h))
		}
	}
	return out
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

// CopyText puts text on the clipboard.
func (a *App) CopyText(text string) bool {
	if a.Wails == nil {
		return false
	}
	return a.Wails.Clipboard.SetText(text)
}

// ChooseFolder asks for a directory.
func (a *App) ChooseFolder(title string) (string, error) {
	if a.Wails == nil {
		return "", errors.New("no window")
	}
	return a.Wails.Dialog.OpenFile().CanChooseDirectories(true).CanChooseFiles(false).SetTitle(title).PromptForSingleSelection()
}

// SetReposDir changes the clone folder.
func (a *App) SetReposDir(dir string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.core.Cfg.ReposDir = dir
	return a.save()
}

// Shutdown closes connections.
func (a *App) Shutdown() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.inv != nil {
		a.inv.Close()
	}
}

// emit sends an event to the window (nothing without one).
func (a *App) emit(name string, data any) {
	if a.Wails != nil {
		a.Wails.Event.Emit(name, data)
	}
}
