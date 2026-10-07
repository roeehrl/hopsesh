package desktop

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/roeehrl/hopsesh/internal/config"
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

// Controller is the only owner of desktop shell state. Native calls are serialized;
// callbacks never acquire this mutex from the UI thread.
type Controller struct {
	published   atomic.Pointer[State]
	ready       chan struct{}
	background  atomic.Bool
	suspended   atomic.Bool
	ctx         context.Context
	mu          sync.Mutex
	app         *application.App
	main, quick *application.WebviewWindow
	tray        *application.SystemTray
	state       State
	cancel      context.CancelFunc
	open        func(string)
	changed     func()
	attention   bool
}

func New(app *application.App, main *application.WebviewWindow, prefs config.Desktop, privileged func(*application.WebviewWindow), open func(string), changed func()) *Controller {
	c := &Controller{ready: make(chan struct{}), app: app, main: main, open: open, changed: changed, state: State{Preferences: prefs, Capabilities: capabilities()}}
	c.state.Effective = Effective(prefs, c.state.Capabilities)
	c.publish()
	ctx, cancel := context.WithCancel(context.Background())
	c.cancel = cancel
	c.ctx = ctx
	c.quick = app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name: "quick-access", Title: "Hopsesh Quick access", Width: 430, Height: 580, MinWidth: 320, MinHeight: 300,
		URL: "/quick.html", Hidden: true, Frameless: true, HideOnFocusLost: true, HideOnEscape: true,
		Windows: application.WindowsWindow{HiddenOnTaskbar: true},
	})
	privileged(c.quick)
	c.quick.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) { e.Cancel(); c.quick.Hide() })
	if c.state.Capabilities.Tray {
		c.makeTray()
	}
	app.Event.OnApplicationEvent(events.Common.ApplicationStarted, func(*application.ApplicationEvent) { go c.start() })
	for _, event := range []events.ApplicationEventType{events.Common.SystemWillSleep, events.Common.ScreenLocked} {
		app.Event.OnApplicationEvent(event, func(*application.ApplicationEvent) { c.suspended.Store(true); c.quick.Hide() })
	}
	for _, event := range []events.ApplicationEventType{events.Common.SystemDidWake, events.Common.ScreenUnlocked} {
		app.Event.OnApplicationEvent(event, func(*application.ApplicationEvent) { c.suspended.Store(false); c.changed() })
	}
	return c
}
func (c *Controller) makeTray() {
	if c.tray != nil {
		return
	}
	c.tray = c.app.SystemTray.New()
	c.tray.AttachWindow(c.quick).OnClick(func() {
		if c.suspended.Load() {
			return
		}
		c.fitQuick(c.tray)
		c.tray.ToggleWindow() // retain Wails' Windows focus-loss debounce
	}).SetTooltip("Hopsesh · Quick access")
	if runtime.GOOS == "darwin" {
		c.tray.SetTemplateIcon(icon(true, c.attention))
	} else {
		c.tray.SetIcon(icon(false, c.attention))
	}
	m := application.NewMenu()
	m.Add("Open Quick access").OnClick(func(*application.Context) { c.showQuick(c.tray) })
	m.Add("Open Hopsesh").OnClick(func(*application.Context) { c.open("sessions") })
	m.Add("Open terminals").OnClick(func(*application.Context) { c.open("terminals") })
	m.Add("Desktop presence…").OnClick(func(*application.Context) { c.open("settings") })
	m.AddSeparator()
	m.Add("Quit Hopsesh…").OnClick(func(*application.Context) { go c.app.Quit() })
	c.tray.SetMenu(m).OnRightClick(func() { c.tray.OpenMenu() })
}
func (c *Controller) start() {
	defer close(c.ready)
	c.mu.Lock()
	_ = c.applyPlacement()
	if loginLaunch() && c.state.Effective != "app" {
		c.background.Store(true)
		c.main.Hide()
	}
	ctx := c.ctx
	c.mu.Unlock()
	if runtime.GOOS == "linux" {
		go func() {
			t := time.NewTicker(10 * time.Second)
			defer t.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-t.C:
					c.Recheck()
				}
			}
		}()
	}
	c.changed()
}

// applyPlacement leaves a reachable full window when shell integration fails.
func (c *Controller) publish() { s := c.state; c.published.Store(&s) }
func (c *Controller) applyPlacement() error {
	defer c.publish()
	c.state.Effective = Effective(c.state.Preferences, c.state.Capabilities)
	if c.state.Effective == "app" {
		c.main.Show()
		c.quick.Hide()
	}
	if c.state.Capabilities.Tray {
		c.makeTray()
	}
	if c.tray != nil {
		if c.state.Effective == "app" {
			c.tray.Hide()
		} else {
			c.tray.Show()
		}
	}
	err := setAppHidden(c.main, c.state.Effective == "tray")
	if err != nil {
		c.state.Error = err.Error()
		c.state.Effective = "app"
		c.main.Show().Focus()
		return err
	}
	c.state.Error = ""
	return nil
}
func (c *Controller) Snapshot() State {
	s := *c.published.Load()
	enabled, err := c.app.Autostart.IsEnabled()
	s.Login = enabled
	if err != nil {
		s.LoginError = err.Error()
	}
	return s
}
func (c *Controller) Apply(p config.Desktop, login bool) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := Validate(p, c.state.Capabilities); err != nil {
		return err
	}
	old := c.state.Preferences
	enabled, err := c.app.Autostart.IsEnabled()
	if err != nil {
		return err
	}
	if enabled != login {
		if login {
			err = c.app.Autostart.EnableWithOptions(loginOptions())
		} else {
			err = c.app.Autostart.Disable()
		}
		if err != nil {
			return err
		}
		actual, statusErr := c.app.Autostart.IsEnabled()
		if statusErr != nil {
			return statusErr
		}
		if actual != login {
			return errors.New("the operating system has not applied login startup; review its Login Items or Startup Apps settings")
		}
	}
	c.state.Preferences = p
	if err = c.applyPlacement(); err != nil {
		c.state.Preferences = old
		err = errors.Join(err, c.applyPlacement())
		if enabled != login {
			if enabled {
				err = errors.Join(err, c.app.Autostart.EnableWithOptions(loginOptions()))
			} else {
				err = errors.Join(err, c.app.Autostart.Disable())
			}
		}
		return err
	}
	return nil
}
func (c *Controller) Recheck() {
	caps := capabilities()
	c.mu.Lock()
	changed := caps != c.state.Capabilities
	if changed {
		old := c.state.Effective
		c.state.Capabilities = caps
		_ = c.applyPlacement()
		if old != "app" && c.state.Effective == "app" {
			c.main.Show().Focus()
		}
	}
	c.mu.Unlock()
	if changed {
		c.changed()
	}
}
func (c *Controller) KeepOnClose() bool {
	s := c.published.Load()
	return KeepOnClose(s.Preferences)
}
func (c *Controller) OpenMain()   { c.quick.Hide(); c.main.Show().Focus() }
func (c *Controller) CloseQuick() { c.quick.Hide() }
func (c *Controller) SetAttention(needs, tabs int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.tray == nil {
		return
	}
	attention := needs > 0 && c.state.Preferences.AttentionOn()
	if attention != c.attention {
		c.attention = attention
		if runtime.GOOS == "darwin" {
			c.tray.SetTemplateIcon(icon(true, attention))
		} else {
			c.tray.SetIcon(icon(false, attention))
		}
	}
	c.tray.SetTooltip(fmt.Sprintf("Hopsesh · %d need you · %d open terminal tabs", needs, tabs))
}
func (c *Controller) Stop() {
	if c.cancel != nil {
		c.cancel()
	}
}

func (c *Controller) BackgroundLaunch() bool {
	select {
	case <-c.ready:
		return c.background.Load()
	case <-c.ctx.Done():
		return false
	case <-time.After(5 * time.Second):
		return false
	}
}
func (c *Controller) Foreground() bool { return c.main.IsVisible() || c.quick.IsVisible() }

func (c *Controller) ShowQuick() {
	c.mu.Lock()
	tray := c.tray
	enabled := c.state.Effective != "app"
	c.mu.Unlock()
	if tray != nil && enabled {
		c.showQuick(tray)
	}
}

func (c *Controller) Suspended() bool { return c.suspended.Load() }

func (c *Controller) fitQuick(tray *application.SystemTray) {
	// Position first to pick the icon's display, then cap the size to its work area.
	_ = tray.PositionWindow(c.quick, 4)
	if screen, err := c.quick.GetScreen(); err == nil && screen != nil {
		width, height := min(430, screen.WorkArea.Width-16), min(580, screen.WorkArea.Height-16)
		if width > 0 && height > 0 {
			c.quick.SetMinSize(min(320, width), min(300, height))
			c.quick.SetSize(width, height)
		}
	}
}

func (c *Controller) showQuick(tray *application.SystemTray) {
	if c.suspended.Load() {
		return
	}
	c.fitQuick(tray)
	tray.ShowWindow()
}
