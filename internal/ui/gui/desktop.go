package gui

import (
	"context"
	"errors"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/roeehrl/hopsesh/internal/config"
	"github.com/roeehrl/hopsesh/internal/ui/desktop"
	"github.com/wailsapp/wails/v3/pkg/application"
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
	presence   *PresenceDTO
	mu         sync.Mutex
	scan       *ScanDTO
	err        string
	route      *QuickRoute
	refreshing atomic.Bool
	cancel     context.CancelFunc
}
type QuickRoute struct {
	Screen  string `json:"screen"`
	Machine string `json:"machine"`
	Key     string `json:"key"`
}
type QuickDTO struct {
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
	ctx, cancel := context.WithCancel(context.Background())
	a.quick.cancel = cancel
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		lastScan, lastPresence := time.Time{}, time.Time{}
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if shell, ok := a.Desktop.(interface{ Suspended() bool }); ok && shell.Suspended() {
					continue
				}
				fg := false
				if shell, ok := a.Desktop.(interface{ Foreground() bool }); ok {
					fg = shell.Foreground()
				}
				a.quick.mu.Lock()
				scanned := a.quick.scan
				a.quick.mu.Unlock()
				if scanned != nil {
					if at, err := time.Parse(time.RFC3339, scanned.Updated); err == nil && at.After(lastScan) {
						lastScan = at
					}
				}
				if time.Since(lastScan) >= time.Minute {
					a.refreshQuick()
					lastScan = time.Now()
				}
				interval := 30 * time.Second
				if fg {
					interval = 5 * time.Second
				}
				if time.Since(lastPresence) >= interval {
					if p, err := a.Presence(); err == nil {
						a.quick.mu.Lock()
						a.quick.presence = p
						a.quick.mu.Unlock()
						a.updateQuickAttention()
						a.emit(QuickEvent, nil)
					}
					lastPresence = time.Now()
				}
			}
		}
	}()
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
	scan, err := a.quick.scan, a.quick.err
	presence := a.quick.presence
	a.quick.mu.Unlock()
	a.mu.Lock()
	agents := a.agentsLocked()
	a.mu.Unlock()
	return QuickDTO{Agents: agents, Presence: presence, Scan: scan, Tabs: a.TerminalTabs(), Desktop: a.DesktopSettings(), OS: runtime.GOOS, Refreshing: a.quick.refreshing.Load(), Error: err}
}

// publishQuick shares immutable scan DTOs. It never starts a second inventory or
// creates an independent remote scan loop.
func (a *App) publishQuick(scan *ScanDTO) {
	a.quick.mu.Lock()
	a.quick.scan = scan
	a.quick.presence = nil
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
	presence := a.quick.presence
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
				if presence != nil {
					if p, ok := presence.Entries[k]; ok {
						waiting = p.Needs
					}
				}
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
	return a.Preview(machine, key, 2)
}
func (a *App) QuickOpen(screen, machine, key string) error {
	switch screen {
	case "sessions", "settings", "terminals":
	default:
		return errors.New("unknown Quick access destination")
	}
	if machine != "" || key != "" {
		a.mu.Lock()
		_, err := a.find(machine, key)
		a.mu.Unlock()
		if err != nil {
			return err
		}
	}
	a.quick.mu.Lock()
	a.quick.route = &QuickRoute{Screen: screen, Machine: machine, Key: key}
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
