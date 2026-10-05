package gui

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/roeehrl/hopsesh/internal/app"
	"github.com/roeehrl/hopsesh/internal/config"
	"github.com/roeehrl/hopsesh/internal/core/termapp"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

// The user's terminal app: every session, teleport and hand-off step the window opens
// outside itself goes there (iTerm2 as a new tab in its front window, Terminal as a new
// window, Windows Terminal), by ticket: the terminal runs only hopsesh's own verb, which
// labels the tab and starts the agent. A session that runs already is shown instead of
// opened twice.

// TerminalEvent tells the window that a launch opened in another terminal than the chosen
// one (TerminalNotice), so it can say so.
const TerminalEvent = "hopsesh:terminal"

// TerminalNotice is a TerminalEvent: where it opened, and why not in the chosen one.
type TerminalNotice struct {
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
	return nil
}

// noteOpened tells the window when a launch went to another terminal than the chosen one
// (iTerm2 denied, so Terminal).
func (a *App) noteOpened(o termapp.Opened) {
	if o.FellBack {
		a.emit(TerminalEvent, TerminalNotice{Terminal: o.Terminal, Message: "Opened in " + o.Terminal + " instead (" + o.Reason + ")."})
	}
}

// TerminalsDTO is the terminal settings: the apps hopsesh can open launches in, the chosen
// one (and the one used, when that is not installed), and where sessions resume.
type TerminalsDTO struct {
	Apps   []termapp.Info `json:"apps"`
	App    string         `json:"app"`    // configured ("" automatic)
	Using  string         `json:"using"`  // the id launches open in now
	Name   string         `json:"name"`   // its name: "iTerm2"
	Resume string         `json:"resume"` // here | terminal | ask
}

// Terminals returns the terminal settings.
func (a *App) Terminals() TerminalsDTO {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	core := a.snapshot()
	t := core.MyTerminal(ctx)
	return TerminalsDTO{Apps: core.TerminalApps(ctx), App: core.Cfg.Terminal.App, Using: t.ID(), Name: t.Name(), Resume: core.Cfg.ResumeIn()}
}

// SetTerminals stores the chosen terminal app ("" automatic) and where sessions resume.
func (a *App) SetTerminals(appID, resume string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	t := config.Terminal{App: appID, Resume: resume}
	c := a.core.Cfg
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

// EntryTab finds the tab a session on this machine runs in (nil: none hopsesh can show).
func (a *App) EntryTab(machine, key string) (*TabDTO, error) {
	f, ok, err := a.entryTab(machine, key)
	if err != nil || !ok {
		return nil, err
	}
	return &TabDTO{Terminal: f.Name()}, nil
}

// ShowEntry brings forward the tab a session on this machine runs in, and returns the
// terminal's name.
func (a *App) ShowEntry(machine, key string) (string, error) {
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
