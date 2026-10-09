package gui

import (
	"errors"
	"runtime"
	"sync"
	"sync/atomic"

	"github.com/roeehrl/hopsesh/internal/config"
	"github.com/roeehrl/hopsesh/internal/ui/desktop"
	"github.com/roeehrl/hopsesh/sdk/agent"
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

const QuickEvent = "hopsesh:quick"
const QuickRouteEvent = "hopsesh:quick-route"

// DesktopShell lets browser tests exercise the same lifecycle without modifying
// the host's login items or hiding real application windows.
type DesktopShell interface {
	Snapshot() desktop.State
	Apply(config.Desktop, bool) error
	Recheck()
	KeepOnClose() bool
	OpenMain()
	CloseQuick()
	SetAttention(int, int)
	Stop()
}
type quickState struct {
	mu         sync.Mutex
	scan       *ScanDTO
	err        string
	route      *QuickRoute
	refreshing atomic.Bool
}
type QuickRoute struct {
	Screen  string `json:"screen"`
	Machine string `json:"machine"`
	Key     string `json:"key"`
	Path    string `json:"path,omitempty"` // browsing identity across initial default-account registration
}
type QuickDTO struct {
	Runtime    RuntimeDTO    `json:"runtime"`
	Agents     []AgentDTO    `json:"agents"`
	Presence   *PresenceDTO  `json:"presence"`
	Scan       *ScanDTO      `json:"scan"`
	Tabs       []TermTab     `json:"tabs"`
	Desktop    desktop.State `json:"desktop"`
	OS         string        `json:"os"`
	Refreshing bool          `json:"refreshing"`
	Error      string        `json:"error,omitempty"`
}

// AttachDesktop installs the native shell. The popup uses the same trusted asset
// and service boundary as the main window; terminal windows remain isolated.
func (a *App) AttachDesktop(main *application.WebviewWindow) {
	a.Desktop = desktop.New(a.Wails, main, a.snapshot().Cfg.Desktop, a.Terms.Privileged, func(screen string) { _ = a.QuickOpen(screen, "", "") }, func() { a.emit(QuickEvent, nil) })
	a.Terms.RaiseMain = a.Desktop.OpenMain
	if err := a.connectRuntime(); err != nil {
		a.backend.mu.Lock()
		a.backend.problem = err.Error()
		a.backend.mu.Unlock()
	}
	for _, event := range []events.ApplicationEventType{events.Common.SystemWillSleep, events.Common.ScreenLocked} {
		a.Wails.Event.OnApplicationEvent(event, func(*application.ApplicationEvent) { a.runtimeSleep(true) })
	}
	for _, event := range []events.ApplicationEventType{events.Common.SystemDidWake, events.Common.ScreenUnlocked} {
		a.Wails.Event.OnApplicationEvent(event, func(*application.ApplicationEvent) { go a.runtimeSleep(false) })
	}

}
func (a *App) DesktopSettings() desktop.State {
	if a.Desktop != nil {
		return a.Desktop.Snapshot()
	}
	c := a.snapshot().Cfg.Desktop
	caps := desktop.Capabilities{Reason: "Desktop integration is available in the installed app."}
	return desktop.State{Preferences: c, Capabilities: caps, Effective: "app"}
}

type DesktopInput struct {
	Mode      string `json:"mode"`
	Close     string `json:"close"`
	Attention bool   `json:"attention"`
	Previews  bool   `json:"previews"`
	Login     bool   `json:"login"`
}

func (a *App) SaveDesktop(in DesktopInput) error {
	if a.Desktop == nil {
		return errors.New("desktop integration is available in the installed app")
	}
	prefs := config.Desktop{Mode: in.Mode, Close: in.Close, Attention: &in.Attention, Previews: &in.Previews}
	if err := prefs.Check(); err != nil {
		return err
	}
	a.desktopMu.Lock()
	defer a.desktopMu.Unlock()
	a.mu.Lock()
	cfgErr := a.cfgErr
	a.mu.Unlock()
	if cfgErr != nil {
		return cfgErr
	}
	old := a.Desktop.Snapshot()
	if err := a.Desktop.Apply(prefs, in.Login); err != nil {
		return err
	}
	a.mu.Lock()
	previous := a.core.Cfg.Desktop
	a.core.Cfg.Desktop = prefs
	err := a.save()
	if err != nil {
		a.core.Cfg.Desktop = previous
	}
	a.mu.Unlock()
	if err != nil {
		return errors.Join(err, a.Desktop.Apply(old.Preferences, old.Login))
	}
	a.updateQuickAttention()
	a.emit(QuickEvent, nil)
	return nil
}
func (a *App) RecheckDesktop() desktop.State {
	if a.Desktop != nil {
		a.Desktop.Recheck()
	}
	return a.DesktopSettings()
}
func (a *App) QuickSnapshot() QuickDTO {
	a.quick.mu.Lock()
	missing := a.quick.scan == nil
	a.quick.mu.Unlock()
	if missing {
		a.CachedScan()
	}
	a.quick.mu.Lock()
	scan, err := a.quick.scan, a.quick.err
	a.quick.mu.Unlock()
	a.mu.Lock()
	agents := a.agentsLocked()
	a.mu.Unlock()
	live, _ := a.Presence()
	return QuickDTO{Runtime: a.RuntimeStatus(), Agents: agents, Presence: live, Scan: scan, Tabs: a.TerminalTabs(), Desktop: a.DesktopSettings(), OS: runtime.GOOS, Refreshing: a.quick.refreshing.Load(), Error: err}
}

// publishQuick shares immutable scan DTOs. It never starts a second inventory or
// creates an independent remote scan loop.
func (a *App) publishQuick(scan *ScanDTO) {
	a.quick.mu.Lock()
	if scan.Revision == 0 {
		scan.Revision = a.scanRevision.Add(1)
	}
	if a.quick.scan != nil && scan.Revision <= a.quick.scan.Revision {
		a.quick.mu.Unlock()
		return
	}
	a.quick.scan = scan
	a.quick.err = ""
	a.quick.mu.Unlock()
	a.updateQuickAttention()
	a.emit(QuickEvent, nil)
}
func (a *App) updateQuickAttention() {
	if a.Desktop == nil {
		return
	}
	a.quick.mu.Lock()
	scan := a.quick.scan
	a.quick.mu.Unlock()
	needs := map[string]bool{}
	if scan != nil {
		for _, g := range scan.Groups {
			for _, e := range g.Entries {
				local := false
				for _, m := range scan.Machines {
					if m.Name == e.Machine {
						local = m.Local
					}
				}
				k := e.Machine + "\x00" + e.Key
				waiting := e.Needs

				if local && waiting {
					needs[k] = true
				}
			}
		}
	}
	tabs := a.TerminalTabs()
	for _, t := range tabs {
		if t.Attention {
			key := t.Machine + "\x00" + t.Key
			if t.Key == "" {
				key = t.ID
			}
			needs[key] = true
		}
	}
	a.Desktop.SetAttention(len(needs), len(tabs))
}

// QuickRefresh is local-only, including the first scan. Background operation
// must never trigger an SSH/password dialog or erase a transfer under review.
func (a *App) QuickRefresh() { go a.refreshQuick() }
func (a *App) refreshQuick() {
	if !a.quick.refreshing.CompareAndSwap(false, true) {
		return
	}
	defer func() { a.quick.refreshing.Store(false); a.emit(QuickEvent, nil) }()
	a.emit(QuickEvent, nil)
	_, err := a.RefreshHere()
	if err != nil {
		a.quick.mu.Lock()
		a.quick.err = err.Error()
		a.quick.mu.Unlock()
	}
}
func (a *App) QuickPreview(machine, key string) (*PreviewDTO, error) {
	if !a.DesktopSettings().Preferences.PreviewsOn() {
		return &PreviewDTO{Items: []PreviewItemDTO{}, Note: "Message previews are hidden."}, nil
	}
	// A remote preview can authenticate; leave it to the explicit full-app route.
	a.mu.Lock()
	inv := a.inv
	local := inv != nil && inv.Machine(machine) != nil && inv.Machine(machine).Local
	a.mu.Unlock()
	if !local {
		return &PreviewDTO{Items: []PreviewItemDTO{}, Note: "Open session details to read the conversation on its machine."}, nil
	}
	var err error
	key, err = a.quickSelectionKey(machine, key)
	if err != nil {
		return nil, err
	}
	preview, err := a.Preview(machine, key, 2)
	// The user can hide Quick previews while the native conversation is read.
	// Recheck before returning content, including the first-message summary.
	if !a.DesktopSettings().Preferences.PreviewsOn() {
		return &PreviewDTO{Items: []PreviewItemDTO{}, Note: "Message previews are hidden."}, nil
	}
	return preview, err
}

// Initial account discovery scopes the default root's previously unscoped
// session keys. Resolve that browsing-only shorthand like the core's default
// account lookup. Explicit profiles never migrate, and a cached/remote/ambiguous
// account cannot supply this transition. Transfers retain their strict keys.
func (a *App) quickSelectionKey(machine, key string) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.quickSelectionKeyLocked(machine, key)
}

func (a *App) quickSelectionKeyLocked(machine, key string) (string, error) {
	_, original := a.find(machine, key)
	if original == nil {
		return key, nil
	}
	want, err := agent.ParseKey(key)
	if err != nil || want.Profile != "" || a.inv == nil {
		return "", original
	}
	m := a.inv.Machine(machine)
	if m == nil || !m.Local || m.Host() == nil || m.Host().Facts.Endpoint == "" {
		return "", original
	}
	resolved := ""
	for _, e := range a.inv.Entries {
		p := e.Profile
		if e.Machine != machine || e.Cached || e.Location.IsCloud() || e.Session.Key.Agent != want.Agent || e.Session.Key.Session != want.Session {
			continue
		}
		// Profile.Error can describe an unavailable sign-in check. Browsing a
		// freshly observed conversation does not require a working agent CLI.
		if p == nil || !p.Default || p.ID == "" || p.ID != e.Session.Key.Profile || p.Agent != want.Agent || p.Endpoint != m.Host().Facts.Endpoint {
			continue
		}
		if resolved != "" {
			return "", original
		}
		resolved = e.Session.Key.String()
	}
	if resolved == "" {
		return "", original
	}
	return resolved, nil
}

func (a *App) QuickOpen(screen, machine, key string) error {
	switch screen {
	case "sessions", "settings", "terminals":
	default:
		return errors.New("unknown Quick access destination")
	}
	path := ""
	if machine != "" || key != "" {
		var err error
		a.mu.Lock()
		key, err = a.quickSelectionKeyLocked(machine, key)
		if err == nil {
			e, _ := a.find(machine, key)
			path = e.Session.Path
		}
		a.mu.Unlock()
		if err != nil {
			return err
		}
	}
	a.quick.mu.Lock()
	a.quick.route = &QuickRoute{Screen: screen, Machine: machine, Key: key, Path: path}
	a.quick.mu.Unlock()
	if a.Desktop != nil {
		a.Desktop.OpenMain()
	} else if a.Terms != nil {
		a.Terms.ShowMain()
	}
	a.emit(QuickRouteEvent, nil)
	return nil
}

// TakeQuickRoute also runs at main-window readiness, so a cold-start click isn't
// lost before its frontend event listener exists.
func (a *App) TakeQuickRoute() *QuickRoute {
	a.quick.mu.Lock()
	defer a.quick.mu.Unlock()
	r := a.quick.route
	a.quick.route = nil
	return r
}
func (a *App) QuickClose() {
	if a.Desktop != nil {
		a.Desktop.CloseQuick()
	}
}
func (a *App) QuickQuit() {
	if a.Wails != nil {
		go a.Wails.Quit()
	}
}

// InitialScan does not contact remote machines during a quiet login launch.
func (a *App) InitialScan() (*ScanDTO, error) {
	if shell, ok := a.Desktop.(interface{ BackgroundLaunch() bool }); ok && shell.BackgroundLaunch() {
		return a.RefreshHere()
	}
	if err := a.connectRuntime(); err != nil {
		return nil, err
	}
	return a.Scan()
}

func (a *App) QuickShow() error {
	shell, ok := a.Desktop.(interface{ ShowQuick() })
	if !ok || a.Desktop.Snapshot().Effective == "app" {
		return errors.New("enable menu bar or system tray presence first")
	}
	shell.ShowQuick()
	return nil
}
