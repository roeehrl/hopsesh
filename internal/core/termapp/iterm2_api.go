package termapp

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/roeehrl/hopsesh/internal/core/termapp/iterm2api"
)

// iTerm2's Python API, the richer path beside AppleScript, used only when the user has
// turned the API on in iTerm2 themselves (hopsesh never does). It adds three things:
// learning that a launch's tab closed (Watcher), opening a step in a split beside the
// session the user is in (Beside), and finding and focusing a tab by its terminal device
// without walking every tab through AppleScript. Every error falls back to the AppleScript
// path silently. A refusal (the user declined the macOS Automation prompt, or iTerm2
// rejected hopsesh) turns the API off for the rest of the process, so nothing is asked
// twice; while the API is not listening, it is probed again (without credentials, so
// without any prompt) at most every apiRetry.

// apiRetry is how long the API stays off after it was not listening.
const apiRetry = 30 * time.Second

// exitPoll is how often a watched launch's exit code is looked for.
const exitPoll = 400 * time.Millisecond

type apiPath struct {
	opts iterm2api.Options
	now  func() time.Time

	mu      sync.Mutex
	refused error     // ErrNotAuthorized or ErrTooOld: off for good
	retryAt time.Time // after ErrUnavailable
	client  *iterm2api.Client
	monitor *iterm2api.Monitor
}

func newAPIPath(o iterm2api.Options) *apiPath { return &apiPath{opts: o, now: time.Now} }

// sharedAPI is the API path every ITerm2() shares: one connection per process, nil under
// test (no test talks to a real iTerm2).
var sharedAPI = func() *apiPath {
	if testing.Testing() {
		return nil
	}
	return newAPIPath(iterm2api.Options{Credentials: vettedCookie})
}()

// vettedCookie asks iTerm2 for an API cookie with the one script the AppleScript
// allowlist has for it.
func vettedCookie(ctx context.Context, app string) (iterm2api.Credentials, error) {
	if err := VetScript(iterm2api.CookieScript(app)); err != nil {
		return iterm2api.Credentials{}, err
	}
	return iterm2api.AppleScriptCredentials(ctx, app)
}

// usable reports, cheaply and without connecting, whether the API may be tried now: not
// refused, not in its retry pause, and iTerm2's API socket exists.
func (p *apiPath) usable() bool {
	if p == nil {
		return false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.usableLocked()
}

func (p *apiPath) usableLocked() bool {
	if p.refused != nil || p.now().Before(p.retryAt) {
		return false
	}
	path := p.opts.SocketPath
	if path == "" {
		path = iterm2api.DefaultSocketPath()
	}
	if path == "" {
		return false
	}
	_, err := os.Stat(path)
	return err == nil
}

// noteLocked records why a connection failed.
func (p *apiPath) noteLocked(err error) {
	switch {
	case errors.Is(err, iterm2api.ErrNotAuthorized), errors.Is(err, iterm2api.ErrTooOld):
		p.refused = err
	case errors.Is(err, iterm2api.ErrUnavailable):
		p.retryAt = p.now().Add(apiRetry)
	}
}

// conn is a connection for one-shot requests: the monitor's while it runs, else one kept
// for the process. Connecting is serialised, so two callers never ask for two cookies.
func (p *apiPath) conn(ctx context.Context) (*iterm2api.Client, error) {
	if p == nil {
		return nil, iterm2api.ErrUnavailable
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.usableLocked() {
		if p.refused != nil {
			return nil, p.refused
		}
		return nil, iterm2api.ErrUnavailable
	}
	if p.monitor != nil {
		if c := p.monitor.Client(); c != nil {
			return c, nil
		}
	}
	if p.client != nil && p.client.Err() == nil {
		select {
		case <-p.client.Done():
		default:
			return p.client, nil
		}
	}
	c, err := iterm2api.Connect(ctx, p.opts)
	if err != nil {
		p.noteLocked(err)
		return nil, err
	}
	p.client = c
	return c, nil
}

// watcher is the process's monitor (one long-lived, subscribed connection), started on
// first use.
func (p *apiPath) watcher(ctx context.Context) (*iterm2api.Monitor, error) {
	if p == nil {
		return nil, iterm2api.ErrUnavailable
	}
	p.mu.Lock()
	if p.monitor != nil {
		m := p.monitor
		p.mu.Unlock()
		return m, nil
	}
	if !p.usableLocked() {
		err := p.refused
		p.mu.Unlock()
		if err == nil {
			err = iterm2api.ErrUnavailable
		}
		return nil, err
	}
	m := iterm2api.NewMonitor(iterm2api.MonitorOptions{Options: p.opts})
	p.monitor = m
	p.mu.Unlock()

	done := make(chan error, 1)
	go func() { done <- m.Run(context.Background()) }()
	go func() {
		for range m.Events() { // the watches read Watch channels, not events
		}
	}()
	forget := func(err error) {
		p.mu.Lock()
		p.noteLocked(err)
		if p.monitor == m {
			p.monitor = nil
		}
		p.mu.Unlock()
	}
	select {
	case <-m.Started():
		go func() { forget(<-done) }()
		return m, nil
	case err := <-done:
		forget(err)
		return nil, err
	case <-ctx.Done():
		go func() { forget(<-done) }()
		return nil, ctx.Err()
	}
}

// openBeside opens a launch in a split beside the active session of iTerm2's key window.
func (t *iterm2) openBeside(ctx context.Context, l Launch) (Handle, error) {
	c, err := t.api.conn(ctx)
	if err != nil {
		return Handle{}, err
	}
	win, err := c.KeyWindow(ctx)
	if err != nil {
		return Handle{}, err
	}
	layout, err := c.ListSessions(ctx)
	if err != nil {
		return Handle{}, err
	}
	var beside string
	for _, w := range layout.Windows {
		if w.ID != win {
			continue
		}
		for _, tab := range w.Tabs {
			if tab.ID == w.SelectedTabID {
				beside = tab.ActiveSessionID
			}
		}
	}
	if beside == "" {
		return Handle{}, errors.New("iTerm2 did not say which session is in front")
	}
	o, err := c.OpenSplit(ctx, beside, iterm2api.SplitRight, iterm2api.Launch{Command: commandLine(l, "--hold")})
	if err != nil {
		return Handle{}, err
	}
	h := Handle{Terminal: IDITerm2}
	if refName.MatchString(o.SessionID) {
		h.Ref = o.SessionID
	}
	if tty, err := c.SessionTTY(ctx, o.SessionID); err == nil && ttyName.MatchString(tty) {
		h.TTY = tty
	}
	_ = c.Focus(ctx, o.SessionID) // bring it forward, as the AppleScript path's activate does
	return handleFor(h, l), nil
}

// findAPI finds the session on tty through the API (ok false with a nil error: none).
func (t *iterm2) findAPI(ctx context.Context, tty string) (Handle, bool, error) {
	c, err := t.api.conn(ctx)
	if err != nil {
		return Handle{}, false, err
	}
	loc, found, err := c.FindTTY(ctx, tty)
	if err != nil || !found {
		return Handle{}, false, err
	}
	if !refName.MatchString(loc.SessionID) {
		return Handle{}, false, errors.New("iTerm2 gave a session id hopsesh does not use")
	}
	return Handle{Terminal: IDITerm2, Ref: loc.SessionID, TTY: tty}, true, nil
}

// focusAPI selects a session through the API, after checking it is still on the same
// terminal device (ErrGone when not).
func (t *iterm2) focusAPI(ctx context.Context, h Handle) error {
	c, err := t.api.conn(ctx)
	if err != nil {
		return err
	}
	tty, err := c.SessionTTY(ctx, h.Ref)
	if err != nil {
		return err
	}
	if tty != h.TTY {
		return ErrGone
	}
	return c.Focus(ctx, h.Ref)
}

// Exited reports a launch's exit code: as soon as hopsesh's verb has written it (the agent
// ended; with --hold its tab goes on as a shell), or when iTerm2 says the tab closed (the
// code if the verb wrote one first, else -1). It needs the Python API; without it the
// error says so and the caller does without.
func (t *iterm2) Exited(ctx context.Context, h Handle) (<-chan int, error) {
	if !ttyName.MatchString(h.TTY) {
		return nil, errors.New("a launch hopsesh did not open in iTerm2")
	}
	m, err := t.api.watcher(ctx)
	if err != nil {
		return nil, err
	}
	c := m.Client()
	if c == nil {
		return nil, iterm2api.ErrClosed
	}
	// The tab is found by its terminal device; the session id iTerm2's API uses is then
	// what is watched.
	loc, found, err := c.FindTTY(ctx, h.TTY)
	if err != nil {
		return nil, err
	}
	gone := make(<-chan struct{})
	if found {
		gone = m.Watch(loc.SessionID)
	} else {
		closed := make(chan struct{})
		close(closed)
		gone = closed
	}
	out := make(chan int, 1)
	go func() {
		defer close(out)
		if found {
			defer m.Unwatch(loc.SessionID)
		}
		tick := time.NewTicker(exitPoll)
		defer tick.Stop()
		for {
			if code, ok := t.exitOf(h); ok {
				out <- code
				return
			}
			select {
			case <-gone:
				code := -1
				if c, ok := t.exitOf(h); ok {
					code = c
				}
				out <- code
				return
			case <-tick.C:
			case <-ctx.Done():
				return
			}
		}
	}()
	return out, nil
}

func (t *iterm2) exitOf(h Handle) (int, bool) {
	if t.exits == nil {
		return 0, false
	}
	return t.exits(h)
}

func (t *iterm2) withExits(src ExitSource) Terminal {
	c := *t
	c.exits = src
	return &c
}
