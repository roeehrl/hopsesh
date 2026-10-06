package gui

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/roeehrl/hopsesh/internal/app"
	"github.com/roeehrl/hopsesh/internal/config"
	"github.com/roeehrl/hopsesh/internal/core/pty"
	"github.com/roeehrl/hopsesh/internal/core/termapp"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

// The app's entry points into its terminal: resuming a session here, a sign-in, a shell in
// a session's folder (and the hand-off and bring-back steps, in step.go and below). Each
// opens where the window asks (a session's Resume menu names the place), else a tab in
// the hopsesh Terminal window or the user's terminal app, as the user chose in Settings →
// Terminal (config [terminal] resume; "" is config.AppResumeDefault). A session has at most
// one live tab: asking again shows it.

// Where an entry point opens.
const (
	WhereHere     = config.ResumeHere     // a tab in the hopsesh Terminal window
	WhereTerminal = config.ResumeTerminal // the user's terminal app
	// WhereShown: the session runs in a tab already, which is now in front.
	WhereShown = "shown"
)

// route is where an entry point opens: asked ("here" or "terminal") when the window says,
// else the setting. The app never asks first: the command line's "ask" is the default
// place here (a session's Resume menu is the app's way of asking).
func route(asked, setting string) string {
	switch asked {
	case WhereHere, WhereTerminal:
		return asked
	}
	if setting == "" || setting == config.ResumeAsk {
		return config.AppResumeDefault
	}
	return setting
}

// OpenedDTO says where an entry point opened.
type OpenedDTO struct {
	// Where: here, terminal or shown.
	Where string `json:"where"`
	// Tab is the tab it runs in (here, shown).
	Tab string `json:"tab,omitempty"`
	// Notice says why it opened elsewhere than chosen ("Opened in Terminal instead: …").
	Notice string `json:"notice,omitempty"`
}

// displayArg shortens one argument for people: a long one (a briefing) is its length.
func displayArg(s string) string {
	if n := utf8.RuneCountInString(s); n > 60 {
		return fmt.Sprintf("‹%d characters›", n)
	}
	if s == "" || strings.ContainsAny(s, " \t\"'") {
		return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
	}
	return s
}

// displayCommand is an argument list for people: the program's name and its arguments.
func displayCommand(argv []string) string {
	if len(argv) == 0 {
		return ""
	}
	parts := []string{strings.TrimSuffix(filepath.Base(argv[0]), ".exe")}
	for _, a := range argv[1:] {
		parts = append(parts, displayArg(a))
	}
	return termapp.Sanitize(strings.Join(parts, " "), 400)
}

// tabTitle is a tab's title from a session's (untrusted) title: cleaned and shortened.
func tabTitle(parts ...string) string {
	var out []string
	for _, p := range parts {
		if p = cleanTitle(p, 28); p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, " · ")
}

// terminalEnv are the variables the module whose program argv0 is wants in hopsesh's
// tabs (agent.Spec.TerminalEnv).
func (a *App) terminalEnv(argv0 string) []string {
	base := strings.TrimSuffix(strings.ToLower(filepath.Base(argv0)), ".exe")
	for _, m := range a.snapshot().Modules() {
		for _, b := range m.Spec().Binaries {
			if strings.EqualFold(b.Name, base) {
				return m.Spec().TerminalEnv
			}
		}
	}
	return nil
}

// tabSpec is a tab for a command: its program found for a tab (app.TabArgv: PATH, the
// login shell's PATH, a Windows command shim's program), the module's TerminalEnv under
// the command's own variables, and the Windows console choice.
func (a *App) tabSpec(c agent.Command, title string) (pty.Spec, error) {
	argv, err := app.TabArgv(c.Argv)
	if err != nil {
		return pty.Spec{}, err
	}
	set := append(append([]string{}, a.terminalEnv(c.Argv[0])...), c.Env...)
	a.mu.Lock()
	sys := a.core.Cfg.Terminal.SystemConsole
	a.mu.Unlock()
	return pty.Spec{Argv: argv, Dir: c.Dir, Title: title, Env: pty.Env{Unset: c.Unset, Set: set}, SystemConsole: sys}, nil
}

// fellBack tells the window that something opened in the user's terminal app because
// the hopsesh Terminal could not start it.
func (a *App) fellBack(why error) string {
	name := "your terminal"
	if t := a.TerminalApps(); t.Name != "" {
		name = t.Name
	}
	msg := fmt.Sprintf("Opened in %s instead: hopsesh's terminal can't start here (%v).", name, why)
	a.emit(TerminalAppEvent, TerminalAppNotice{Terminal: name, Message: msg})
	return msg
}

// ResumeSession continues a session that is on this machine: in a tab of the hopsesh
// Terminal window or in the user's terminal app (where: "here", "terminal", or "" for the
// setting). A session that runs in a tab already is shown instead.
func (a *App) ResumeSession(machine, key, where string) (*OpenedDTO, error) {
	if a.Terms != nil {
		if id := a.Terms.liveSessionTab(machine, key); id != "" {
			a.Terms.Focus(id)
			return &OpenedDTO{Where: WhereShown, Tab: id}, nil
		}
	}
	a.mu.Lock()
	setting := a.core.Cfg.AppResume()
	a.mu.Unlock()
	switch route(where, setting) {
	case WhereTerminal:
		return &OpenedDTO{Where: WhereTerminal}, a.ResumeEntry(machine, key, false)
	}
	core := a.snapshot()
	a.mu.Lock()
	e, err := a.find(machine, key)
	inv := a.inv
	local := false
	if inv != nil {
		m := inv.Machine(machine)
		local = m != nil && m.Local
	}
	a.mu.Unlock()
	if err != nil {
		return nil, err
	}
	if !local {
		return nil, errors.New("the session is not on this machine; hop it here first")
	}
	c, err := core.Resume(inv, e, agent.ResumeOptions{})
	if err != nil {
		return nil, err
	}
	ctx, cancel := ctx20()
	defer cancel()
	if err := core.OpenElsewhere(ctx, e.Session.Key, e.Live); err != nil {
		return nil, err
	}
	l := app.Launch{Kind: termapp.KindSession, Run: c, Key: e.Session.Key,
		Labels: termapp.Labels{Title: titleOf(e.Session), Agent: e.AgentName, Machine: e.Machine}}
	return a.sessionTab(c, l, TabMeta{Kind: TabSession, Agent: e.AgentName, Machine: machine, Key: key}, titleOf(e.Session))
}

// sessionTab opens a session's command in a tab; when the terminal cannot start it, the
// same launch opens in the user's terminal app instead.
func (a *App) sessionTab(c agent.Command, l app.Launch, meta TabMeta, title string) (*OpenedDTO, error) {
	spec, err := a.tabSpec(c, tabTitle(title, strings.TrimSuffix(filepath.Base(c.Argv[0]), ".exe")))
	if err == nil && a.Terms == nil {
		err = errors.New("no terminal")
	}
	var info pty.Info
	if err == nil {
		meta.Command, meta.External, meta.Rerun = displayCommand(c.Argv), true, true
		info, err = a.Terms.Open(spec, TabSetup{Meta: meta, External: func(id string) error { return a.moveOut(id, l) }})
	}
	if err != nil {
		if oerr := a.openLaunch(l); oerr != nil {
			return nil, fmt.Errorf("%v; and %w", err, oerr)
		}
		return &OpenedDTO{Where: WhereTerminal, Notice: a.fellBack(err)}, nil
	}
	return &OpenedDTO{Where: WhereHere, Tab: info.ID}, nil
}

// moveOut ends a tab and runs its launch in the user's terminal app ("Open in my
// terminal", which the terminal window confirmed with the user): the program here ends
// first, so the same conversation never runs twice.
func (a *App) moveOut(id string, l app.Launch) error {
	if a.Terms != nil && id != "" {
		if err := a.Terms.close(id); err != nil {
			return err
		}
	}
	return a.openLaunch(l)
}

// SignedInDTO is a sign-in tab's end: the login checked again (the card's Test).
type SignedInDTO struct {
	Cloud  string        `json:"cloud"`
	Title  string        `json:"title"`
	Driver string        `json:"driver"`
	Test   *CloudTestDTO `json:"test"`
}

// SignIn runs a cloud's own sign-in command (claude auth login, codex login --device-auth,
// gh auth login --web): in a sign-in tab, whose output hopsesh never reads or keeps, or in
// the user's terminal app (where as for ResumeSession). When the tab
// ends with code 0, hopsesh checks the login again and tells the window (SignedInEvent).
func (a *App) SignIn(cloud, where string) (*OpenedDTO, error) {
	core := a.snapshot()
	m, cl, ok := cloudModuleOf(core, cloud)
	if !ok {
		return nil, fmt.Errorf("no cloud %q", cloud)
	}
	if len(cl.SignIn) == 0 {
		return nil, fmt.Errorf("hopsesh has no sign-in command for %s; sign in with %s yourself", cl.Title, cl.Driver)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	c := agent.Command{Argv: append([]string{cl.Driver}, cl.SignIn...), Dir: home}
	l := app.Launch{Kind: termapp.KindSignIn, Run: c}
	if route(where, core.Cfg.AppResume()) == WhereTerminal || a.Terms == nil {
		return &OpenedDTO{Where: WhereTerminal}, a.openLaunch(l)
	}
	spec, err := a.tabSpec(c, "Sign in · "+cl.Title)
	if err == nil {
		spec.Private = true
		var info pty.Info
		info, err = a.Terms.Open(spec, TabSetup{
			Meta:     TabMeta{Kind: TabSignIn, Command: displayCommand(c.Argv), Agent: m.Spec().Name, Cloud: cl.Name, CloudTitle: cl.Title, External: true, Rerun: true},
			External: func(id string) error { return a.moveOut(id, l) },
			Exited: func(i pty.Info) {
				if i.Code != 0 {
					return
				}
				t, _ := a.TestCloud(cl.Name)
				a.emit(SignedInEvent, SignedInDTO{Cloud: cl.Name, Title: cl.Title, Driver: cl.Driver, Test: t})
			},
		})
		if err == nil {
			return &OpenedDTO{Where: WhereHere, Tab: info.ID}, nil
		}
	}
	if oerr := a.openLaunch(l); oerr != nil {
		return nil, fmt.Errorf("%v; and %w", err, oerr)
	}
	return &OpenedDTO{Where: WhereTerminal, Notice: a.fellBack(err)}, nil
}

// OpenShell opens the user's login shell in a tab, in a session's folder on this machine
// (key "" or a folder that is gone: the home folder). Nothing in a shell tab is recorded.
func (a *App) OpenShell(machine, key string) (*OpenedDTO, error) {
	dir := ""
	if key != "" {
		a.mu.Lock()
		e, err := a.find(machine, key)
		a.mu.Unlock()
		if err == nil {
			dir = e.Session.CWD
		}
	}
	return a.shellTab(dir)
}

// shellTab opens a shell tab in dir ("" or a folder that is not there: home).
func (a *App) shellTab(dir string) (*OpenedDTO, error) {
	if a.Terms == nil {
		return nil, errors.New("no terminal")
	}
	if fi, err := os.Stat(dir); dir == "" || err != nil || !fi.IsDir() {
		dir, _ = os.UserHomeDir()
	}
	argv := loginShell()
	name := strings.TrimSuffix(filepath.Base(argv[0]), ".exe")
	a.mu.Lock()
	sys := a.core.Cfg.Terminal.SystemConsole
	a.mu.Unlock()
	spec := pty.Spec{Argv: argv, Dir: dir, Title: tabTitle("shell", filepath.Base(dir)), Private: true, SystemConsole: sys}
	info, err := a.Terms.Open(spec, TabSetup{Meta: TabMeta{Kind: TabShell, Command: "your login shell " + name, Rerun: true}})
	if err != nil {
		return nil, err
	}
	return &OpenedDTO{Where: WhereHere, Tab: info.ID}, nil
}
