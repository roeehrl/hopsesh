package app

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"slices"
	"sync"
	"time"

	"github.com/roeehrl/hopsesh/internal/config"
	"github.com/roeehrl/hopsesh/internal/core/host"
	"github.com/roeehrl/hopsesh/internal/core/integrate"
	"github.com/roeehrl/hopsesh/internal/core/peer"
	"github.com/roeehrl/hopsesh/internal/core/pty"
	"github.com/roeehrl/hopsesh/internal/core/serve"
	"github.com/roeehrl/hopsesh/internal/version"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

// Login is hopsesh serve's own state: it runs in the user's login session, where the
// sign-ins and apps are, and owns the terminals it runs for hopsesh on other machines, so
// they outlive the connections that show them.
type Login struct {
	Terms *pty.Manager
	mu    sync.Mutex
	tabs  map[string]peer.Terminal
}

func (l *Login) changed(i pty.Info) {
	l.mu.Lock()
	defer l.mu.Unlock()
	t, ok := l.tabs[i.ID]
	if !ok {
		return
	}
	if i.State == "" {
		delete(l.tabs, i.ID)
		return
	}
	t.State, t.Code = string(i.State), i.Code
	l.tabs[i.ID] = t
}

// List returns its terminals, oldest first.
func (l *Login) List() []peer.Terminal {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]peer.Terminal, 0, len(l.tabs))
	for _, t := range l.tabs {
		out = append(out, t)
	}
	slices.SortFunc(out, func(a, b peer.Terminal) int {
		if a.Started < b.Started {
			return -1
		}
		if a.Started > b.Started {
			return 1
		}
		return 0
	})
	return out
}

// RunServe is hopsesh serve: it answers hopsesh on this machine's other side of SSH
// (hopsesh peer hands its conversation over) and the terminals it runs, until ctx ends.
func (a *App) RunServe(ctx context.Context) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	l, err := serve.Listen(home)
	if err != nil {
		return err
	}
	defer l.Close()
	// The login session's environment: a login service starts with almost none (the
	// agents are on the PATH the user's shell has).
	_ = os.Setenv(host.LoginSessionEnv, "1")
	integrate.AdoptLoginPATH()
	integrate.AdoptLoginEnv()
	lg := &Login{tabs: map[string]peer.Terminal{}}
	lg.Terms = pty.NewManager(pty.Options{Version: version.Version, MaxTabs: 32, IdleAfter: -1, OnChange: lg.changed})
	defer lg.Terms.CloseAll()
	a.Login = lg
	go func() {
		<-ctx.Done()
		_ = l.Close()
	}()
	a.Log.Info("hopsesh serve: listening", "socket", serve.SocketPath(home), "version", version.Version)
	for {
		c, r, h, err := l.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				continue
			}
			return err
		}
		go a.serveConn(ctx, c, r, h)
	}
}

func (a *App) serveConn(ctx context.Context, c net.Conn, r *bufio.Reader, h serve.Hello) {
	defer c.Close()
	ok := serve.Welcome{OK: true, Version: version.Version, PID: os.Getpid()}
	switch h.Mode {
	case serve.ModePing:
		_ = serve.Reply(c, ok)
	case serve.ModePeer:
		// Each conversation reads the configuration as it is now.
		conv := *a
		if cfg, err := config.Load(); err == nil {
			conv.Cfg = cfg
		}
		if serve.Reply(c, ok) == nil {
			_ = conv.Serve(ctx, r, c)
		}
	case serve.ModeTerminal:
		if serve.Reply(c, ok) != nil {
			return
		}
		_ = pty.Serve(serve.NewFrameConn(ctx, r, c, c.Close), func(id string) (*pty.Session, error) {
			if s, found := a.Login.Terms.Get(id); found {
				return s, nil
			}
			return nil, fmt.Errorf("no terminal %s here (it ended, or hopsesh restarted)", id)
		}, nil)
	default:
		_ = serve.Reply(c, serve.Welcome{Error: "unknown mode " + h.Mode})
	}
}

// OpenTerminal runs what req asks in a terminal this hopsesh serve owns.
func (a *App) OpenTerminal(ctx context.Context, req peer.TerminalRequest) (peer.Terminal, error) {
	if a.Login == nil {
		return peer.Terminal{}, peer.NoLogin(LocalName())
	}
	mod, ok := a.Module(req.Agent)
	if !ok {
		return peer.Terminal{}, fmt.Errorf("%s is not enabled on %s", req.Agent, LocalName())
	}
	var c agent.Command
	t := peer.Terminal{Kind: req.Kind, Agent: req.Agent, Profile: req.Profile, Session: req.Session}
	switch req.Kind {
	case peer.TerminalResume:
		inv := a.Scan(ctx, ScanOptions{Hosts: []string{LocalName()}, SkipGit: true})
		defer inv.Close()
		var found []Entry
		for _, e := range inv.Entries {
			if e.Agent == req.Agent && string(e.Session.Key.Session) == req.Session && (req.Profile == "" || e.Session.Key.Profile == req.Profile) {
				found = append(found, e)
			}
		}
		if len(found) != 1 {
			return t, fmt.Errorf("found %d sessions %s of %s on %s", len(found), req.Session, mod.Spec().Name, LocalName())
		}
		e := found[0]
		for _, open := range a.Login.List() {
			if open.Session == req.Session && open.Agent == req.Agent && open.State != string(pty.Exited) {
				return open, nil // already running here: show that one
			}
		}
		// Open elsewhere on this machine (a terminal app, the desktop app): a second
		// writer would interleave with it.
		if e.Live.State == agent.Live {
			where := "another program"
			if e.Live.App {
				where = "its desktop app"
			}
			return t, fmt.Errorf("%s is open on %s in %s; continue it there, or close it first", e.Session.Title, LocalName(), where)
		}
		cmd, err := a.Resume(inv, e, agent.ResumeOptions{})
		if err != nil {
			return t, err
		}
		c, t.Title, t.Dir = cmd, e.Session.Title, e.Session.CWD
	case peer.TerminalSignIn:
		cmd, err := a.AccountLogin(ctx, req.Profile, !req.AtScreen)
		if err != nil {
			return t, err
		}
		c, t.Title = cmd, "Sign in · "+mod.Spec().Name
	case peer.TerminalNew:
		cmd, err := a.NewSessionCommand(ctx, req.Agent, req.Profile, req.Dir)
		if err != nil {
			return t, err
		}
		c, t.Title, t.Dir = cmd, "New "+mod.Spec().Name+" session", cmd.Dir
	default:
		return t, fmt.Errorf("unknown terminal kind %q", req.Kind)
	}
	argv, err := TabArgv(c.Argv)
	if err != nil {
		return t, err
	}
	set := append(append([]string{}, mod.Spec().TerminalEnv...), c.Env...)
	spec := pty.Spec{Argv: argv, Dir: c.Dir, Title: t.Title, Cols: req.Cols, Rows: req.Rows, Env: pty.Env{Unset: c.Unset, Set: set}, Private: req.Kind == peer.TerminalSignIn}
	s, err := a.Login.Terms.Start(spec)
	if err != nil {
		return t, err
	}
	info := s.Info()
	t.ID, t.State, t.Started = info.ID, string(info.State), info.Started.UTC().Format(time.RFC3339Nano)
	a.Login.mu.Lock()
	a.Login.tabs[t.ID] = t
	a.Login.mu.Unlock()
	return t, nil
}

// NewSessionCommand starts a new session of an agent in dir, signed in as the account of
// the folder profile ("" its default folder).
func (a *App) NewSessionCommand(ctx context.Context, id agent.ID, profile, dir string) (agent.Command, error) {
	mod, ok := a.Module(id)
	if !ok {
		return agent.Command{}, fmt.Errorf("%s is not enabled", id)
	}
	m := a.localMachine(ctx)
	defer m.Close()
	h, err := m.For(ctx, mod.Spec(), agent.Install{}, nil)
	if err != nil {
		return agent.Command{}, err
	}
	in, err := mod.Detect(ctx, h)
	if err != nil {
		return agent.Command{}, err
	}
	if in.Binary == "" {
		return agent.Command{}, fmt.Errorf("%w: %s", agent.ErrNotInstalled, mod.Spec().Name)
	}
	if dir == "" {
		dir = m.Facts.Home
	}
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		return agent.Command{}, fmt.Errorf("no folder %s on %s", dir, LocalName())
	}
	c := agent.Command{Argv: []string{in.Binary}, Dir: dir}
	if profile == "" {
		return c, nil
	}
	installs, err := a.profileInstalls(ctx, m, mod, in, false, true)
	if err != nil {
		return agent.Command{}, err
	}
	for _, p := range installs {
		if p.ProfileID() == profile {
			p.Accounts = mod.Spec().Accounts
			return p.ScopeCommand(c), nil
		}
	}
	return agent.Command{}, fmt.Errorf("no account folder %s on %s", profile, LocalName())
}

// CloseTerminal ends one of hopsesh serve's terminals.
func (a *App) CloseTerminal(id string) error {
	if a.Login == nil {
		return peer.NoLogin(LocalName())
	}
	return a.Login.Terms.Close(id)
}
