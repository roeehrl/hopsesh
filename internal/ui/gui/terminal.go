package gui

import (
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"

	"github.com/roeehrl/hopsesh/internal/core/pty"
)

// The app's terminal: tabs whose programs run in pseudo-terminals here (internal/core/pty),
// shown in a terminal window of their own.
//
// That window is isolated from the app (docs/design.md §15). Its page talks to hopsesh only
// through two Wails streams: TerminalStream, one connection per tab (input, resize, acks,
// close, a link to open; output and the tab's state back), and TerminalTabsStream (the
// list of tabs). Gate, the asset middleware, gives any window other than the app's own the
// terminal page, the Wails runtime script and the stream endpoints only: no bound method,
// no event, no clipboard, no dialog and no other page. Wails tags every request with the
// window it came from in the native layer, so a page cannot claim another window's id. A
// link a tab's program prints opens only as http(s), and only after the user confirms it
// in a native dialog that shows the whole address.
//
// What a tab runs is decided in Go (openTab, from a typed request such as a hand-off's
// step), never taken from a window.

// Stream names the terminal page connects to.
const (
	TerminalStream     = "hopsesh.terminal"
	TerminalTabsStream = "hopsesh.terminal.tabs"
	// TerminalEvent carries a tab's Info (State "" once it is gone) to the app's window,
	// for its badges.
	TerminalEvent = "hopsesh:terminal"
	// TerminalPage is where the terminal window's page is.
	TerminalPage = "/terminal/"
)

// Terminals holds the app's terminal tabs and their window.
type Terminals struct {
	mgr    *pty.Manager
	showMu sync.Mutex // one terminal window at a time

	mu    sync.Mutex
	app   *application.App
	main  map[uint]bool // windows with the app's own page and its bindings
	win   *application.WebviewWindow
	winID uint
	asked time.Time // when a link's confirmation was last shown
	lists map[*tabList]bool
}

// tabList is a terminal window's TerminalTabsStream connection.
type tabList struct{ ch chan []pty.Info }

// NewTerminals is the app's tabs; version is hopsesh's.
func NewTerminals(version string) *Terminals {
	t := &Terminals{main: map[uint]bool{}, lists: map[*tabList]bool{}}
	t.mgr = pty.NewManager(pty.Options{Version: version, OnChange: t.changed})
	return t
}

// Attach connects the tabs to the running app: its streams, and its window events.
func (t *Terminals) Attach(app *application.App) {
	t.mu.Lock()
	t.app = app
	t.mu.Unlock()
	app.HandleStream(TerminalStream, t.serveTab)
	app.HandleStream(TerminalTabsStream, t.serveList)
}

// Privileged marks a window as the app's own (its requests pass Gate).
func (t *Terminals) Privileged(id uint) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.main[id] = true
}

// Manager is the tabs' manager.
func (t *Terminals) Manager() *pty.Manager { return t.mgr }

// CloseAll ends every tab (the app is quitting).
func (t *Terminals) CloseAll() { t.mgr.CloseAll() }

// windowOf is the id Wails' native layer tagged a request with.
func windowOf(r *http.Request) (uint, bool) {
	raw := r.Header.Get("x-wails-window-id")
	if raw == "" {
		return 0, false
	}
	id, err := strconv.ParseUint(raw, 10, strconv.IntSize)
	return uint(id), err == nil
}

// Gate is the asset middleware that isolates terminal windows (see the top of this
// file): requests from the app's own windows pass; any other window gets only what the
// terminal page needs, and its page a strict content security policy.
func (t *Terminals) Gate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if id, ok := windowOf(r); ok {
			t.mu.Lock()
			main := t.main[id]
			t.mu.Unlock()
			if main {
				next.ServeHTTP(w, r)
				return
			}
		}
		if !terminalMayFetch(r) {
			http.Error(w, "not available to the terminal window", http.StatusForbidden)
			return
		}
		if strings.HasPrefix(r.URL.Path, TerminalPage) {
			w.Header().Set("Content-Security-Policy", terminalCSP)
			w.Header().Set("Cache-Control", "no-store")
		}
		next.ServeHTTP(w, r)
	})
}

// terminalCSP keeps the terminal page to its own scripts and styles, and its requests to
// the app's asset server (the streams): no inline script, no other origin, no frames, no
// forms, no plugins.
const terminalCSP = "default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self' data:; font-src 'self'; connect-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'; object-src 'none'"

// terminalMayFetch is what a terminal window may request: its page's files, the Wails
// runtime scripts, and the two stream endpoints.
func terminalMayFetch(r *http.Request) bool {
	p := r.URL.Path
	switch r.Method {
	case http.MethodGet, http.MethodHead:
		return strings.HasPrefix(p, TerminalPage) && !strings.Contains(p, "..") ||
			p == "/wails/runtime.js" || p == "/wails/transport.js" || p == "/wails/custom.js" || p == "/wails/stream/poll"
	case http.MethodPost:
		return p == "/wails/stream/send"
	}
	return false
}

// isTerminalWindow: the window is the terminal window hopsesh opened.
func (t *Terminals) isTerminalWindow(w application.Window) bool {
	if w == nil {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.win != nil && w.ID() == t.winID && !t.main[w.ID()]
}

// serveTab is one TerminalStream connection: a tab shown in the terminal window.
func (t *Terminals) serveTab(c *application.StreamConn) {
	if !t.isTerminalWindow(c.Window()) {
		_ = c.Close()
		return
	}
	find := func(id string) (*pty.Session, error) {
		if s, ok := t.mgr.Get(id); ok {
			return s, nil
		}
		return nil, errors.New("no such tab")
	}
	_ = pty.Serve(c, find, func(u string) { t.confirmLink(c.Window(), u) })
}

// serveList is one TerminalTabsStream connection: the tabs, again whenever one changes.
func (t *Terminals) serveList(c *application.StreamConn) {
	defer c.Close()
	if !t.isTerminalWindow(c.Window()) {
		return
	}
	l := &tabList{ch: make(chan []pty.Info, 1)}
	t.mu.Lock()
	t.lists[l] = true
	t.mu.Unlock()
	defer func() {
		t.mu.Lock()
		delete(t.lists, l)
		t.mu.Unlock()
	}()
	l.ch <- t.mgr.List()
	go func() { // the page sends nothing; this notices it going
		for {
			if _, err := c.Receive(); err != nil {
				return
			}
		}
	}()
	for {
		select {
		case tabs := <-l.ch:
			b, _ := json.Marshal(tabs)
			if err := c.Send(b); err != nil {
				return
			}
		case <-c.Context().Done():
			return
		}
	}
}

// changed tells the app's window and the terminal window's list about a tab.
func (t *Terminals) changed(i pty.Info) {
	t.mu.Lock()
	app := t.app
	lists := make([]*tabList, 0, len(t.lists))
	for l := range t.lists {
		lists = append(lists, l)
	}
	t.mu.Unlock()
	if len(lists) > 0 {
		tabs := t.mgr.List()
		for _, l := range lists {
			select { // only the newest list matters
			case <-l.ch:
			default:
			}
			select {
			case l.ch <- tabs:
			default:
			}
		}
	}
	if app != nil {
		app.Event.Emit(TerminalEvent, i)
	}
}

// confirmLink asks the user whether to open a link a tab's program printed (already
// checked to be http or https), showing all of it; one question at a time.
func (t *Terminals) confirmLink(w application.Window, u string) {
	t.mu.Lock()
	app := t.app
	// A link is sent when the user clicks one, so a second request while one question
	// shows is a double click.
	if app == nil || time.Since(t.asked) < 2*time.Second {
		t.mu.Unlock()
		return
	}
	t.asked = time.Now()
	t.mu.Unlock()
	d := app.Dialog.Question().SetTitle("Open this link?").
		SetMessage("A program in a terminal tab asks to open this address in your browser:\n\n" + u + "\n\nOpen it only if you expected it.")
	d.AddButton("Open").OnClick(func() { _ = openInBrowser(u) })
	cancel := d.AddButton("Cancel")
	d.SetDefaultButton(cancel).SetCancelButton(cancel)
	if w != nil {
		d.AttachToWindow(w)
	}
	d.Show()
}

// Open opens a tab running spec's program and shows it in the terminal window. Only
// hopsesh's own Go code calls it, with a program it chose (a hand-off's driver, an agent's
// documented resume command), never with one a window sent: Terminals is not bound to the
// window.
func (t *Terminals) Open(spec pty.Spec) (pty.Info, error) {
	s, err := t.mgr.Start(spec)
	if err != nil {
		return pty.Info{}, err
	}
	t.Show()
	return s.Info(), nil
}

// Show opens the terminal window, or brings it to the front.
func (t *Terminals) Show() {
	t.showMu.Lock()
	defer t.showMu.Unlock()
	t.mu.Lock()
	app, win := t.app, t.win
	t.mu.Unlock()
	if app == nil {
		return
	}
	if win != nil {
		win.Show().Focus()
		return
	}
	w := app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:      "hopsesh-terminal",
		Title:     "hopsesh: terminal",
		Width:     960,
		Height:    640,
		MinWidth:  480,
		MinHeight: 300,
		URL:       TerminalPage,
	})
	t.mu.Lock()
	t.win, t.winID = w, w.ID()
	t.mu.Unlock()
	w.OnWindowEvent(events.Common.WindowClosing, func(*application.WindowEvent) {
		t.mu.Lock()
		if t.win == w {
			t.win, t.winID = nil, 0
		}
		t.mu.Unlock()
	})
}

// Tabs are the open tabs, for the app's window.
func (t *Terminals) Tabs() []pty.Info { return t.mgr.List() }

// TerminalTabs are the terminal's open tabs (state, title, exit code).
func (a *App) TerminalTabs() []pty.Info {
	if a.Terms == nil {
		return []pty.Info{}
	}
	return a.Terms.Tabs()
}

// TerminalRunning is how many tabs' programs still run (the window asks before quitting).
func (a *App) TerminalRunning() int {
	if a.Terms == nil {
		return 0
	}
	return a.Terms.mgr.Running()
}

// TerminalShow brings the terminal window to the front.
func (a *App) TerminalShow() {
	if a.Terms != nil {
		a.Terms.Show()
	}
}

// TerminalCloseTab closes a tab, ending its program.
func (a *App) TerminalCloseTab(id string) error {
	if a.Terms == nil {
		return errors.New("no terminal")
	}
	if !slices.ContainsFunc(a.Terms.Tabs(), func(i pty.Info) bool { return i.ID == id }) {
		return errors.New("no such tab")
	}
	return a.Terms.mgr.Close(id)
}
