// Package gui is the backend of the hopsesh desktop app (Wails v3). The frontend in
// assets/ calls these methods; every use case is the app layer's, as for the command
// line.
package gui

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/roeehrl/hopsesh/internal/app"
	"github.com/roeehrl/hopsesh/internal/config"
	"github.com/roeehrl/hopsesh/internal/core/appicon"
	"github.com/roeehrl/hopsesh/internal/core/audit"
	"github.com/roeehrl/hopsesh/internal/core/lnp"
	"github.com/roeehrl/hopsesh/internal/core/move"
	"github.com/roeehrl/hopsesh/internal/core/proc"
	"github.com/roeehrl/hopsesh/internal/core/registry"
	"github.com/roeehrl/hopsesh/internal/update"
	"github.com/roeehrl/hopsesh/internal/version"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

// MenuEvent carries a menu-bar command to the window ("palette", "refresh", "sessions",
// "activity", "machines", "settings", "undo-last").
const MenuEvent = "hopsesh:menu"

// App is the service bound to the frontend.
type App struct {
	mu       sync.Mutex
	core     *app.App // its Cfg is the saved configuration; calls work on snapshots
	cfgErr   error    // the configuration file could not be used (see StartFresh)
	inv      *app.Inventory
	plan     *move.Plan
	input    move.Input
	res      *move.Result
	push     *app.Push // a push planned on another machine, its connection open
	pw       *pwBroker
	appIcons map[agent.ID]string // installed apps' icons, read once ("" when none)
	pwOnce   sync.Once
	step     *pendingStep // the terminal step a hand-off waits for
	quitting atomic.Bool  // the user confirmed quitting (or an update restarts the app)
	termName atomic.Value // the user's terminal app's name, for the terminal window (a string)
	// Wails is the running application (events, clipboard, dialogs).
	Wails *application.App `json:"-"`
	// Terms are the terminal's tabs and window (not bound to the window: see terminal.go).
	Terms *Terminals `json:"-"`
	// Emitter takes the window's events when there is no Wails app (the browser tests).
	Emitter func(name string, data any) `json:"-"`
}

// NewApp loads the configuration for the modules in reg. A configuration an older hopsesh
// wrote is reported by Info, not returned: the window offers to start fresh.
func NewApp(reg *registry.Registry) *App {
	cfg, err := config.Load()
	log, _ := audit.Open(filepath.Join(config.StateDir(), "log"))
	a := &App{cfgErr: err, Terms: NewTerminals(version.Version)}
	a.core = app.New(cfg, reg, config.StateDir(), log)
	a.core.Passwords = a.passwordFor
	a.core.Steps = a.runStep
	a.attachTerminal()
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
	Import        bool               `json:"import"`  // continued sessions go through its own importer
	Version       string             `json:"version"` // installed here ("" when not)
	Folder        string             `json:"folder"`  // its data folder here
	Tested        []string           `json:"tested"`
	Capabilities  []agent.Capability `json:"capabilities"`
	Icon          string             `json:"icon,omitempty"` // a data URL: the installed app's icon or the module's mark ("": initials)
	// Clouds are the vendor clouds the agent reaches, with what hopsesh can do there.
	Clouds []AgentCloudDTO `json:"clouds"`
}

// AgentCloudDTO is one of an agent's clouds in Settings: the cloud capabilities its
// module has (cloud-list, cloud-fetch, …), and whether its listing is partial.
type AgentCloudDTO struct {
	Name         string             `json:"name"`
	Title        string             `json:"title"`
	Capabilities []agent.Capability `json:"capabilities"`
	Partial      bool               `json:"partial"`
	Fidelity     string             `json:"fidelity"` // what comes back of a conversation
}

// Info is static information for the window.
type Info struct {
	Version     string `json:"version"`
	OS          string `json:"os"` // runtime.GOOS: the window words things for its system
	Host        string `json:"host"`
	ReposDir    string `json:"reposDir"`
	AuditDir    string `json:"auditDir"`
	HasHosts    bool   `json:"hasHosts"`
	ConfigError string `json:"configError,omitempty"`
	// ConfigNewer: a newer hopsesh wrote the configuration; the window offers to update
	// hopsesh first, and setting the file aside only as a second, confirmed choice.
	ConfigNewer bool       `json:"configNewer,omitempty"`
	Agents      []AgentDTO `json:"agents"`
	// LocalNetwork describes macOS local network privacy: gated (macOS 15+) and firstRun
	// (the prompt has probably not been answered yet).
	LocalNetwork struct {
		Gated    bool `json:"gated"`
		FirstRun bool `json:"firstRun"`
	} `json:"localNetwork"`
	UpdateCheck string `json:"updateCheck"` // "", "on" or "off"
	// Terminal is the terminal app sessions open in, by name ("iTerm2"): the window says
	// "Open in iTerm2".
	Terminal string `json:"terminal"`
	// Where is where sessions resume and steps run: here (the hopsesh Terminal window),
	// terminal (the user's terminal app) or ask.
	Where       string `json:"where"`
	SkillState  string `json:"skillState"`  // across every agent: absent | current | stale | modified | foreign | broken
	SkillPrompt string `json:"skillPrompt"` // "declined" once the user said not now
	CLIOffer    bool   `json:"cliOffer"`    // offer to link the command-line tool
	Receive     bool   `json:"receive"`     // other machines' hopsesh may send sessions here
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
	info := Info{Version: version.Version, OS: runtime.GOOS, Host: app.LocalName(), ReposDir: cfg.ReposDir,
		AuditDir: filepath.Join(config.StateDir(), "log"), UpdateCheck: cfg.UpdateCheck,
		SkillState: skill.State, SkillPrompt: cfg.SkillPrompt, Receive: cfg.Peer.Receive}
	if a.cfgErr != nil {
		info.ConfigError = a.cfgErr.Error()
		info.ConfigNewer = errors.Is(a.cfgErr, config.ErrNewConfig)
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
	if t := a.core.MyTerminal(ctx); t != nil && runtime.GOOS == "darwin" {
		info.Terminal = t.Name()
	}
	info.Where = cfg.AppResume()
	return info
}

// iconLocked pictures an agent: its installed desktop app's icon (read once, when the
// setting is on), else the module's mark, else nothing (the window shows initials).
func (a *App) iconLocked(s agent.Spec) string {
	if a.core.Cfg.AppIconsOn() {
		if a.appIcons == nil {
			a.appIcons = map[agent.ID]string{}
		}
		url, seen := a.appIcons[s.ID]
		if !seen {
			home, _ := os.UserHomeDir()
			url, _ = appicon.Find(s.Icon.Apps, home)
			a.appIcons[s.ID] = url
		}
		if url != "" {
			return url
		}
	}
	if s.Icon.SVG != "" {
		return "data:image/svg+xml;base64," + base64.StdEncoding.EncodeToString([]byte(s.Icon.SVG))
	}
	return ""
}

func (a *App) agentsLocked() []AgentDTO {
	var here map[agent.ID]app.AgentState
	if a.inv != nil && a.inv.Local() != nil {
		here = map[agent.ID]app.AgentState{}
		for _, st := range a.inv.Local().Agents {
			here[st.Agent] = st
		}
	}
	var out []AgentDTO
	for _, m := range a.core.Reg.All() {
		s := m.Spec()
		ac := a.core.Cfg.Agents[string(s.ID)]
		d := AgentDTO{ID: s.ID, Name: s.Name, Stability: s.Stability, Enabled: !ac.Disabled, RemoteControl: ac.RemoteControl,
			Import: ac.Import, Tested: s.Tested, Capabilities: agent.Capabilities(m), Icon: a.iconLocked(s), Clouds: []AgentCloudDTO{}}
		for _, c := range s.Clouds {
			ac := AgentCloudDTO{Name: c.Name, Title: c.Title, Capabilities: []agent.Capability{}, Fidelity: string(c.Down)}
			for _, cp := range d.Capabilities {
				if agent.IsCloudCapability(cp) {
					ac.Capabilities = append(ac.Capabilities, cp)
				}
			}
			if a.inv != nil {
				if sc := a.inv.Cloud(c.Name); sc != nil {
					ac.Partial = sc.Partial
				}
			}
			d.Clouds = append(d.Clouds, ac)
		}
		if st, ok := here[s.ID]; ok && (st.Install.Present || st.Install.Binary != "") {
			d.Version = st.Install.Version
			for _, r := range s.Roots {
				d.Folder = st.Install.Root(r.Name)
				break
			}
		}
		out = append(out, d)
	}
	return out
}

// StartFresh sets aside a configuration file an older hopsesh wrote and starts with
// defaults (machines are added again). It returns where the old file went. A file a newer
// hopsesh wrote is set aside only when the user chose that over updating (newer).
func (a *App) StartFresh(newer bool) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	switch {
	case errors.Is(a.cfgErr, config.ErrNewConfig):
		if !newer {
			return "", errors.New("a newer hopsesh wrote these settings: update hopsesh, or choose to set them aside")
		}
	case !errors.Is(a.cfgErr, config.ErrOldConfig):
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
	// CanInstall: this app can install the update itself (a release build, not managed by
	// a package manager); otherwise the window offers the release page.
	CanInstall bool `json:"canInstall"`
}

// canInstall reports whether this app updates itself.
func canInstall() bool {
	t, err := update.Current()
	if err != nil || t.Kind == update.KindCLI || update.PublicKey == "" {
		return false
	}
	who, _ := update.ManagedBy()
	return who == ""
}

// InstallUpdate downloads the newest release, verifies it (release signature and
// checksum; on macOS also the same Apple developer and notarization), installs it over
// this app, opens the new version and quits this one.
func (a *App) InstallUpdate() error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	rel, err := update.Latest(ctx)
	if err != nil {
		return fmt.Errorf("could not check for updates: %w", err)
	}
	if !update.Newer(rel.Version, version.Version) {
		return errors.New("this is already the newest version")
	}
	t, err := update.Install(ctx, rel)
	if err != nil {
		return err
	}
	a.core.Audit.Write(audit.Entry{Action: "update", Detail: map[string]any{"from": version.Version, "to": rel.Version, "what": string(t.Kind)}})
	if err := update.Relaunch(t); err != nil {
		return fmt.Errorf("installed hopsesh %s; quit and reopen the app to use it (%w)", rel.Version, err)
	}
	if a.Wails != nil {
		a.quitting.Store(true) // the window asked about running tabs before installing
		go func() { time.Sleep(300 * time.Millisecond); a.Wails.Quit() }()
	}
	return nil
}

// LatestRelease looks for the newest release now, whatever the daily check is set to: the
// user asked, from the page that says a newer hopsesh wrote the configuration.
func (a *App) LatestRelease() (*UpdateDTO, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	rel, err := update.Latest(ctx)
	if err != nil {
		return nil, fmt.Errorf("could not check for updates: %w", err)
	}
	return &UpdateDTO{Latest: rel.Version, URL: rel.URL, Newer: update.Newer(rel.Version, version.Version), CanInstall: canInstall()}, nil
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
		last.DTO.CanInstall = canInstall()
		return &last.DTO, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	rel, err := update.Latest(ctx)
	if err != nil {
		return &UpdateDTO{}, nil // quiet: offline or nothing published
	}
	last.At, last.DTO = time.Now(), UpdateDTO{Latest: rel.Version, URL: rel.URL, Newer: update.Newer(rel.Version, version.Version), CanInstall: canInstall()}
	if b, err := json.Marshal(last); err == nil {
		_ = os.WriteFile(stamp, b, 0o600)
	}
	return &last.DTO, nil
}

// OpenURL opens a web page in the default browser: hopsesh's release pages, Tailscale's
// sign-in check for a machine, a cloud session's page, and an upstream problem a cloud
// points to.
func (a *App) OpenURL(url string) error {
	if !strings.HasPrefix(url, "https://github.com/"+update.Repo+"/") && !strings.HasPrefix(url, "https://login.tailscale.com/") && !cloudPage(a.snapshot(), url) && !a.listedPage(url) && !handoffPage(url) {
		return errors.New("only hopsesh release pages, Tailscale sign-in and cloud sessions' pages can be opened")
	}
	return openInBrowser(url)
}

// openInBrowser opens a web page in the default browser.
func openInBrowser(url string) error {
	switch runtime.GOOS {
	case "darwin":
		return proc.Command("open", url).Run()
	case "windows":
		return proc.Command("rundll32", "url.dll,FileProtocolHandler", url).Run()
	}
	return proc.Command("xdg-open", url).Run()
}

// OpenLocalNetworkSettings opens System Settings at Privacy & Security, where the Local
// Network list is (macOS offers no link to the list itself).
func (a *App) OpenLocalNetworkSettings() error {
	if runtime.GOOS != "darwin" {
		return errors.New("only macOS has a Local Network setting")
	}
	return proc.Command("open", lnp.SettingsURL).Run()
}

// RetryLocalNetwork makes the next scan wait for the macOS prompt again, for someone who
// just changed the setting or wants to answer a prompt they dismissed.
func (a *App) RetryLocalNetwork() {
	lnp.ResetWait(config.StateDir()) // each scan makes new connections, so nothing else is cached
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

// Shutdown closes connections and ends the terminal's tabs.
func (a *App) Shutdown() {
	if a.Terms != nil {
		a.Terms.CloseAll()
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.inv != nil {
		a.inv.Close()
	}
	a.closePushLocked()
}

// emit sends an event to the window (nothing without one).
func (a *App) emit(name string, data any) {
	switch {
	case a.Wails != nil:
		a.Wails.Event.Emit(name, data)
	case a.Emitter != nil:
		a.Emitter(name, data)
	}
}
