package gui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/roeehrl/hopsesh/internal/app"
	"github.com/roeehrl/hopsesh/internal/core/peer"
	"github.com/roeehrl/hopsesh/internal/core/pty"
	"github.com/roeehrl/hopsesh/internal/core/termapp"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

// ContinueOnMachine continues a session where it is, on another machine: hopsesh in the
// login session there runs it (its sign-in works there), and a tab here shows it.
func (a *App) ContinueOnMachine(machine, key string) (*OpenedDTO, error) {
	a.mu.Lock()
	e, err := a.find(machine, key)
	a.mu.Unlock()
	if err != nil {
		return nil, err
	}
	if machine == app.LocalName() {
		return nil, errors.New("the session is on this machine; resume it here")
	}
	if a.Terms != nil {
		if id := a.Terms.remoteTab(machine, key); id != "" {
			a.Terms.Focus(id)
			return &OpenedDTO{Where: WhereShown, Tab: id}, nil
		}
	}
	req := peer.TerminalRequest{Kind: peer.TerminalResume, Agent: e.Agent, Profile: e.Session.Key.Profile, Session: string(e.Session.Key.Session)}
	return a.remoteTab(machine, req, TabMeta{Kind: TabRemote, Agent: e.AgentName, Machine: machine, Key: key}, titleOf(e.Session)+" · "+machine)
}

// OpenOnMachine continues a session on another machine that is not a row yet (one just
// sent there), by its key there, in a tab here.
func (a *App) OpenOnMachine(machine, key string) (*OpenedDTO, error) {
	k, err := agent.ParseKey(key)
	if err != nil {
		return nil, err
	}
	name := string(k.Agent)
	if m, ok := a.snapshot().Module(k.Agent); ok {
		name = m.Spec().Name
	}
	req := peer.TerminalRequest{Kind: peer.TerminalResume, Agent: k.Agent, Profile: k.Profile, Session: string(k.Session)}
	return a.remoteTab(machine, req, TabMeta{Kind: TabRemote, Agent: name, Machine: machine, Key: key}, name+" · "+machine)
}

// NewSessionOn starts a new session of an agent on a machine, in a folder there, as the
// account of one of its folders ("" its default), and shows it in a tab here.
func (a *App) NewSessionOn(machine string, id agent.ID, profile, dir string) (*OpenedDTO, error) {
	name := string(id)
	if m, ok := a.snapshot().Module(id); ok {
		name = m.Spec().Name
	}
	if machine == "" || machine == app.LocalName() {
		core := a.snapshot()
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		c, err := core.NewSessionCommand(ctx, id, profile, dir)
		if err != nil {
			return nil, err
		}
		// The tab binds to the session the agent starts, as any session tab here does.
		return a.sessionTab(c, app.Launch{Kind: termapp.KindSession, Run: c, Labels: termapp.Labels{Title: "New " + name + " session", Agent: name, Machine: app.LocalName()}},
			TabMeta{Kind: TabSession, Agent: name, Machine: app.LocalName()}, "New "+name+" session")
	}
	req := peer.TerminalRequest{Kind: peer.TerminalNew, Agent: id, Profile: profile, Dir: dir}
	return a.remoteTab(machine, req, TabMeta{Kind: TabRemote, Agent: name, Machine: machine}, "New "+name+" session · "+machine)
}

// SignInOn runs an account folder's sign-in on another machine (atScreen: in the browser
// on that machine's screen; otherwise it prints a link and a code for this one's), in a
// tab here whose output hopsesh never reads.
func (a *App) SignInOn(machine string, id agent.ID, profile string, atScreen bool) (*OpenedDTO, error) {
	req := peer.TerminalRequest{Kind: peer.TerminalSignIn, Agent: id, Profile: profile, AtScreen: atScreen}
	return a.remoteTab(machine, req, TabMeta{Kind: TabRemote, Machine: machine}, "Sign in · "+machine)
}

// remoteTab has hopsesh on machine run req, and opens a tab here that shows it.
func (a *App) remoteTab(machine string, req peer.TerminalRequest, meta TabMeta, title string) (*OpenedDTO, error) {
	core := a.snapshot()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	t, err := core.RemoteTerminal(ctx, machine, req)
	if err != nil {
		return nil, err
	}
	return a.attachTab(machine, t, meta, title)
}

// reattach shows again a terminal hopsesh still runs on another machine (a tab the app
// showed when it quit): false when it is not there any more.
func (a *App) reattach(r ReopenTab) (bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	ts, err := a.snapshot().RemoteTerminals(ctx, r.Machine)
	if err != nil {
		return false, err
	}
	for _, t := range ts {
		if t.ID == r.Remote && t.State != "exited" {
			if a.Terms != nil {
				for _, d := range a.Terms.Tabs() {
					if d.Kind == TabRemote && d.Machine == r.Machine && d.Remote == t.ID && d.State != pty.Exited {
						return true, nil // shown already
					}
				}
			}
			_, err := a.attachTab(r.Machine, t, TabMeta{Kind: TabRemote, Agent: r.Agent, Machine: r.Machine, Key: r.Key}, r.Title)
			return true, err
		}
	}
	return false, nil
}

// attachTab opens a tab here that shows terminal t, which hopsesh runs on machine.
func (a *App) attachTab(machine string, t peer.Terminal, meta TabMeta, title string) (*OpenedDTO, error) {
	cli, err := app.CLIExecutable()
	if err != nil {
		return nil, fmt.Errorf("showing a session from %s needs the hopsesh command here: %w", machine, err)
	}
	home, _ := os.UserHomeDir()
	c := agent.Command{Argv: []string{cli, "terminals", "attach", machine, t.ID}, Dir: home}
	meta.Remote = t.ID
	meta.Command = fmt.Sprintf("%s on %s", t.Title, machine)
	meta.External, meta.Rerun = true, true
	return a.sessionTab(c, app.Launch{Kind: termapp.KindSession, Run: c, Labels: termapp.Labels{Title: title, Agent: meta.Agent, Machine: machine}}, meta, title)
}
