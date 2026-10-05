package gui

import (
	"os"
	"runtime"
	"sync"
	"time"

	"github.com/roeehrl/hopsesh/internal/config"
)

// Settings → Terminal, the terminal window's settings, and quitting or closing the app's
// window while programs run in tabs.

// TerminalSettingsDTO is Settings → Terminal: where steps and resumes open, the user's
// terminal app, and the hopsesh Terminal window's look and behaviour.
type TerminalSettingsDTO struct {
	TerminalAppsDTO
	// Where is where sessions resume and steps run: here or terminal (the setting, or its
	// default; the command line's "ask" is the default here).
	Where        string `json:"where"`
	Default      string `json:"default"` // config.AppResumeDefault
	Font         string `json:"font"`
	FontSize     int    `json:"fontSize"`
	Scrollback   int    `json:"scrollback"`
	Scrollbacks  []int  `json:"scrollbacks"`
	KeepTabs     bool   `json:"keepTabs"`
	Notify       bool   `json:"notify"`
	CloseEnded   bool   `json:"closeEnded"`   // a tab closes once its program ended well
	ScreenReader string `json:"screenReader"` // "" automatic, on, off
	// SystemConsole (Windows): tabs use the system's pseudoconsole.
	SystemConsole bool `json:"systemConsole"`
	// Bundled: this app carries Microsoft's newer console host (Windows).
	Bundled bool `json:"bundled"`
}

// TerminalSettings are Settings → Terminal.
func (a *App) TerminalSettings() TerminalSettingsDTO {
	d := TerminalSettingsDTO{TerminalAppsDTO: a.TerminalApps(), Default: config.AppResumeDefault, Scrollbacks: config.TerminalScrollbacks}
	a.mu.Lock()
	c := a.core.Cfg
	a.mu.Unlock()
	d.Where, d.Font, d.FontSize, d.Scrollback = route("", c.AppResume()), c.Terminal.Font, c.TerminalFont(), c.TerminalLines()
	d.KeepTabs, d.Notify, d.ScreenReader, d.SystemConsole = c.KeepTabsOn(), c.NotifyOn(), c.Terminal.ScreenReader, c.Terminal.SystemConsole
	d.CloseEnded = c.CloseEndedOn()
	d.Bundled = a.Terms != nil && a.Terms.Manager().BundledConsole()
	return d
}

// TerminalSettingsInput are the settings the user can change in Settings → Terminal.
type TerminalSettingsInput struct {
	App           string `json:"app"`
	Where         string `json:"where"`
	Font          string `json:"font"`
	FontSize      int    `json:"fontSize"`
	Scrollback    int    `json:"scrollback"`
	KeepTabs      bool   `json:"keepTabs"`
	Notify        bool   `json:"notify"`
	CloseEnded    bool   `json:"closeEnded"`
	ScreenReader  string `json:"screenReader"`
	SystemConsole bool   `json:"systemConsole"`
}

// SetTerminalSettings stores Settings → Terminal, and gives the terminal window its new
// look.
func (a *App) SetTerminalSettings(in TerminalSettingsInput) error {
	a.mu.Lock()
	c := a.core.Cfg
	t := c.Terminal
	t.App, t.Resume, t.Font, t.FontSize, t.Scrollback = in.App, in.Where, in.Font, in.FontSize, in.Scrollback
	keep, notify := in.KeepTabs, in.Notify
	t.KeepTabs, t.Notify, t.ScreenReader, t.SystemConsole = &keep, &notify, in.ScreenReader, in.SystemConsole
	if keep {
		t.KeepTabs = nil
	}
	if notify {
		t.Notify = nil
	}
	t.KeepEnded = nil
	if !in.CloseEnded {
		keepEnded := true
		t.KeepEnded = &keepEnded
	}
	if t.Resume == config.AppResumeDefault && c.Terminal.Resume == "" {
		t.Resume = "" // still the default
	}
	if t.FontSize == config.TerminalFontSize {
		t.FontSize = 0
	}
	if t.Scrollback == config.TerminalScrollback {
		t.Scrollback = 0
	}
	c.Terminal = t
	if err := c.Check(); err != nil {
		a.mu.Unlock()
		return err
	}
	a.core.Cfg.Terminal = t
	err := a.save()
	a.mu.Unlock()
	a.termName.Store("") // looked up again
	if a.Terms != nil {
		a.Terms.Refresh()
	}
	return err
}

// termPrefs are the terminal window's settings.
func (a *App) termPrefs() TermPrefs {
	a.mu.Lock()
	c := a.core.Cfg
	a.mu.Unlock()
	reader := c.Terminal.ScreenReader == "on" || c.Terminal.ScreenReader == "" && screenReader()
	home, _ := os.UserHomeDir()
	name, _ := a.termName.Load().(string)
	if name == "" {
		name = a.TerminalApps().Name
		a.termName.Store(name)
	}
	return TermPrefs{Font: c.Terminal.Font, FontSize: c.TerminalFont(), Scrollback: c.TerminalLines(), ScreenReader: reader, OS: runtime.GOOS,
		Home: home, TerminalName: name}
}

// saveTermPrefs stores what the terminal window changed: its font size (⌘= and ⌘-) or
// its screen reader mode.
func (a *App) saveTermPrefs(size int, reader *bool) {
	a.mu.Lock()
	t := a.core.Cfg.Terminal
	if size > 0 {
		t.FontSize = size
		if size == config.TerminalFontSize {
			t.FontSize = 0
		}
	}
	if reader != nil {
		t.ScreenReader = map[bool]string{true: "on", false: "off"}[*reader]
	}
	c := a.core.Cfg
	c.Terminal = t
	if c.Check() == nil {
		a.core.Cfg.Terminal = t
		_ = a.save()
	}
	a.mu.Unlock()
	a.Terms.Refresh()
}

// screenReader is screenReaderRunning, asked at most every ten seconds.
var (
	readerMu   sync.Mutex
	readerAt   time.Time
	readerSeen bool
)

func screenReader() bool {
	readerMu.Lock()
	defer readerMu.Unlock()
	if time.Since(readerAt) > 10*time.Second {
		readerSeen, readerAt = screenReaderRunning(), time.Now()
	}
	return readerSeen
}

// attachTerminal connects the app's settings and entry points to its terminal.
func (a *App) attachTerminal() {
	t := a.Terms
	t.Prefs = a.termPrefs
	t.Emit = a.emit
	t.SavePrefs = a.saveTermPrefs
	t.Shell = func(dir string) error { _, err := a.shellTab(dir); return err }
	t.NotifyOn = func() bool {
		a.mu.Lock()
		defer a.mu.Unlock()
		return a.core.Cfg.NotifyOn()
	}
	t.AutoClose = func() bool {
		a.mu.Lock()
		defer a.mu.Unlock()
		return a.core.Cfg.CloseEndedOn()
	}
}

// ShouldQuit is the app's quit check: with programs running in tabs, the app's window
// asks first (QuitEvent), and quitting waits for QuitAndEnd.
func (a *App) ShouldQuit() bool {
	if a.quitting.Load() || a.TerminalRunning() == 0 {
		return true
	}
	a.askQuit()
	return false
}

// askQuit shows the app's window and asks it to confirm quitting.
func (a *App) askQuit() {
	if a.Terms != nil {
		a.Terms.ShowMain()
	}
	a.emit(QuitEvent, a.TerminalTabs())
}

// QuitAndEnd quits the app and ends the programs in its tabs (the user confirmed).
func (a *App) QuitAndEnd() {
	a.quitting.Store(true)
	if a.Wails != nil {
		go a.Wails.Quit()
		return
	}
	for _, t := range a.TerminalTabs() { // no app (the browser tests): only the tabs end
		_ = a.Terms.close(t.ID)
	}
}

// MainClosing decides what closing the app's window does: nothing special without
// running tabs (cancel false); with them, it hides (the setting "Keep tabs when the window
// closes"), or asks whether to quit.
func (a *App) MainClosing() (cancel, hide bool) {
	if a.quitting.Load() || a.TerminalRunning() == 0 {
		return false, false
	}
	a.mu.Lock()
	keep := a.core.Cfg.KeepTabsOn()
	a.mu.Unlock()
	if keep {
		// Without a Dock to bring the app back from (Windows, Linux), the terminal window
		// stays (or comes) up, and closing it brings this window back.
		if runtime.GOOS != "darwin" && a.Terms != nil && !a.Terms.WindowOpen() {
			a.Terms.Show()
		}
		return true, true
	}
	a.askQuit()
	return true, false
}
