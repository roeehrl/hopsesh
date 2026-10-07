package gui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime"
	"time"

	"github.com/roeehrl/hopsesh/internal/app"
	"github.com/roeehrl/hopsesh/internal/core/appicon"
	"github.com/roeehrl/hopsesh/internal/core/proc"
	"github.com/roeehrl/hopsesh/internal/core/termapp"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

// The user's terminal app: every session, teleport and hand-off step the window opens
// outside itself goes there (iTerm2 as a new tab in its front window, Terminal as a new
// window, Windows Terminal), by ticket: the terminal runs only hopsesh's own verb, which
// labels the tab and starts the agent. A session that runs already is shown instead of
// opened twice.

// TerminalAppEvent tells the window that a launch opened in another terminal than the chosen
// one (TerminalAppNotice), so it can say so.
const TerminalAppEvent = "hopsesh:terminal-app"

// TerminalAppNotice is a TerminalAppEvent: where it opened, and why not in the chosen one.
type TerminalAppNotice struct {
	Terminal string `json:"terminal"`
	Message  string `json:"message"`
}

// testTerminal, when set, opens every launch instead of the user's terminal app.
var testTerminal termapp.Terminal

// SetTerminal replaces the user's terminal app with one that runs each launch's line
// (hopsesh's verb on a ticket, in the launch's folder) with f: the browser tests run it in
// the background, as a terminal would.
func SetTerminal(f func(line string) error) {
	testTerminal = termapp.LineTerminal("test", "a terminal", f)
}

// openLaunch opens a launch in the user's terminal app.
func (a *App) openLaunch(l app.Launch) error {
	prog, err := stepProgram()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	o, err := a.snapshot().OpenInTerminal(ctx, prog, testTerminal, l)
	if err != nil {
		if errors.Is(err, termapp.ErrNoTerminal) {
			return fmt.Errorf("%w: copy the command instead", err)
		}
		return err
	}
	a.noteOpened(o)
	a.watchLaunch(o, l)
	return nil
}

// noteOpened tells the window when a launch went to another terminal than the chosen one
// (iTerm2 denied, so Terminal).
func (a *App) noteOpened(o termapp.Opened) {
	if o.FellBack {
		a.emit(TerminalAppEvent, TerminalAppNotice{Terminal: o.Terminal, Message: "Opened in " + o.Terminal + " instead (" + o.Reason + ")."})
	}
}

// TerminalAppsDTO is the terminal settings: the apps hopsesh can open launches in, the chosen
// one (and the one used, when that is not installed), and where sessions resume.
type TerminalAppsDTO struct {
	Apps   []termapp.Info `json:"apps"`
	App    string         `json:"app"`    // configured ("" automatic)
	Using  string         `json:"using"`  // the id launches open in now
	Name   string         `json:"name"`   // its name: "iTerm2"
	Resume string         `json:"resume"` // here | terminal | ask
}

// TerminalApps returns the terminal settings.
func (a *App) TerminalApps() TerminalAppsDTO {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	core := a.snapshot()
	t := core.MyTerminal(ctx)
	return TerminalAppsDTO{Apps: core.TerminalApps(ctx), App: core.Cfg.Terminal.App, Using: t.ID(), Name: t.Name(), Resume: core.Cfg.ResumeIn()}
}

// SetTerminalApps stores the chosen terminal app ("" automatic) and where sessions resume.
func (a *App) SetTerminalApps(appID, resume string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	c := a.core.Cfg
	t := c.Terminal
	t.App, t.Resume = appID, resume
	c.Terminal = t
	if err := c.Check(); err != nil {
		return err
	}
	a.core.Cfg.Terminal = t
	return a.save()
}

// TabDTO is where a running session's tab is: the terminal that has it.
type TabDTO struct {
	Terminal string `json:"terminal"` // its name: "iTerm2"
}

// EntryTab finds the tab a session on this machine runs in (nil: none hopsesh can show):
// one of the hopsesh Terminal window's, or one in the user's terminal app.
func (a *App) EntryTab(machine, key string) (*TabDTO, error) {
	if a.Terms != nil && a.Terms.liveSessionTab(machine, key) != "" {
		return &TabDTO{Terminal: TerminalTitle}, nil
	}
	f, ok, err := a.entryTab(machine, key)
	if err != nil || !ok {
		return nil, err
	}
	return &TabDTO{Terminal: f.Name()}, nil
}

// ShowEntry brings forward the tab a session on this machine runs in, and returns the
// terminal's name.
func (a *App) ShowEntry(machine, key string) (string, error) {
	if a.Terms != nil {
		if id := a.Terms.liveSessionTab(machine, key); id != "" {
			a.Terms.Focus(id)
			return TerminalTitle, nil
		}
	}
	f, ok, err := a.entryTab(machine, key)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", errors.New("it is not running in a terminal tab hopsesh can show")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := a.snapshot().ShowTab(ctx, f); err != nil {
		return "", err
	}
	return f.Name(), nil
}

// ShowApp brings forward the agent's own desktop app, which runs a session on this
// machine, and returns its name. Modules with exact chat links navigate to that chat;
// other modules can only bring their app forward.
func (a *App) ShowApp(machine, key string) (string, error) {
	core := a.snapshot()
	a.mu.Lock()
	e, err := a.find(machine, key)
	local := false
	if a.inv != nil {
		m := a.inv.Machine(machine)
		local = m != nil && m.Local
	}
	a.mu.Unlock()
	if err != nil {
		return "", err
	}
	if !local {
		return "", errors.New("the session is not on this machine")
	}
	m, ok := core.Module(e.Agent)
	if !ok {
		return "", fmt.Errorf("%s is turned off", e.AgentName)
	}
	apps := m.Spec().Icon.Apps
	name := nonEmptyStr(appicon.Name(apps), e.AgentName)
	if _, exact := m.(agent.AppChecker); exact {
		a.mu.Lock()
		inv := a.inv
		a.mu.Unlock()
		c, err := core.Resume(inv, e, agent.ResumeOptions{App: true})
		if err != nil {
			return "", err
		}
		if appHook != nil {
			return name, appHook(name, c)
		}
		return name, start(c)
	}
	if appHook != nil {
		return name, appHook(name, agent.Command{})
	}
	home, _ := os.UserHomeDir()
	p := appicon.Installed(apps, home)
	if p == "" {
		return "", fmt.Errorf("hopsesh can't find the %s app on this machine", name)
	}
	return name, openApp(p)
}

var appHook func(name string, command agent.Command) error

// SetAppHook sends every desktop app ShowApp would bring forward to f instead (tests: the
// machine running them may have the real app).
func SetAppHook(f func(name string, command agent.Command) error) { appHook = f }

// openApp brings an installed app to the front (starting it when it isn't running).
func openApp(p string) error {
	switch runtime.GOOS {
	case "darwin":
		return proc.Command("open", p).Run()
	case "windows":
		return proc.Command(p).Start() // a running app takes its second start as "show me"
	}
	return errors.New("hopsesh can't open desktop apps here")
}

func (a *App) entryTab(machine, key string) (termapp.Found, bool, error) {
	a.mu.Lock()
	e, err := a.find(machine, key)
	local := false
	if a.inv != nil {
		m := a.inv.Machine(machine)
		local = m != nil && m.Local
	}
	a.mu.Unlock()
	if err != nil {
		return termapp.Found{}, false, err
	}
	if !local {
		return termapp.Found{}, false, errors.New("the session is not on this machine")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pid := 0
	if e.Live.State == agent.Live {
		pid = e.Live.PID
	}
	return a.snapshot().SessionTab(ctx, e.Session.Key, pid)
}
