package gui

import (
	"encoding/json"
	"errors"
	"net/http"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"

	"github.com/roeehrl/hopsesh/internal/app"
	"github.com/roeehrl/hopsesh/internal/core/pty"
)

// The app's terminal: tabs whose programs run in pseudo-terminals here (internal/core/pty),
// shown in a separate window or a sandboxed panel in the main window.
//
// That window is isolated from the app (docs/design.md §15). Its page talks to hopsesh only
// through two capability-authorized loopback websocket streams: TerminalStream, one connection per tab (input, resize, acks,
// close, a link to open; output and the tab's state back), and TerminalTabsStream (the
// list of tabs and settings; from the renderer, validated typed requests about tabs).
// The loopback host serves no app bindings; the embedded frame has an opaque sandbox
// origin. Gate also rejects cross-origin requests before considering a native window ID.
// A link a tab's program prints opens only as http(s), and only after the user
// confirms it in a native dialog that shows the whole address.
//
// What a tab runs is decided in Go (Open, from one of the app's entry points: a session's
// resume command, a hand-off's step, a sign-in, a shell), never taken from a window. A
// request from the window names a tab and an action; Go looks up what that tab runs.

// Stream and event names, and where the terminal page is.
const (
	TerminalStream     = "hopsesh.terminal"
	TerminalTabsStream = "hopsesh.terminal.tabs"
	// TerminalEvent carries a tab's TermTab (State "" once it is gone) to the app's window,
	// for its chips and counts.
	TerminalEvent = "hopsesh:terminal"
	// QuitEvent asks the app's window to confirm quitting while programs run in tabs.
	QuitEvent = "hopsesh:quit"
	// SignedInEvent: a sign-in tab ended with code 0, and hopsesh checked the login again
	// (a SignedInDTO).
	SignedInEvent = "hopsesh:signed-in"
	// TerminalPage is where the terminal window's page is.
	TerminalPage = "/terminal/"
	// TerminalTitle is the terminal window's title.
	TerminalTitle = "hopsesh Terminal"
)

// What a tab is for (TabMeta.Kind).
const (
	TabSession = "session" // a session resumed here
	TabStep    = "step"    // a hand-off's driver step (claude --cloud)
	TabBring   = "bring"   // a bring-back's teleport (claude --teleport)
	TabSignIn  = "signin"  // a vendor's sign-in: nothing recorded
	TabShell   = "shell"   // the user's login shell: nothing recorded
)

// TabMeta is what the app knows about a tab besides its program's state.
type TabMeta struct {
	LaunchedKey  string            `json:"launchedKey,omitempty"`
	Relationship *app.Relationship `json:"relationship,omitempty"`
	Account      string            `json:"account,omitempty"`
	Association  string            `json:"association,omitempty"`
	Kind         string            `json:"kind"`
	// Command is the program and its arguments, for people.
	Command string `json:"command"`
	// Agent names whose program it is ("Claude Code"; "" for a shell).
	Agent string `json:"agent,omitempty"`
	// Machine and Key are a session tab's row (or a bring-back's copy, once known).
	Machine string `json:"machine,omitempty"`
	Key     string `json:"key,omitempty"`
	// Cloud and CloudTitle are a step's, a bring-back's or a sign-in's cloud.
	Cloud      string `json:"cloud,omitempty"`
	CloudTitle string `json:"cloudTitle,omitempty"`
	// Journal is a bring-back's journal id (its done screen asks AdoptStatus by it).
	Journal string `json:"journal,omitempty"`
	// Saved is the title of the copy a bring-back saved here while its tab still runs it
	// (Machine and Key are then that copy's: the tab is the session's).
	Saved string `json:"saved,omitempty"`
	// Link is a step's session link, once the module's reader found it after the step
	// ended.
	Link string `json:"link,omitempty"`
	// External: the tab offers "Open in my terminal" (end it here and run the same command
	// in the user's terminal app).
	External bool `json:"external"`
	// Rerun: the tab offers "Run again" once its program ended.
	Rerun bool `json:"rerun"`
}

// TermTab is a tab as the windows show it.
type TermTab struct {
	pty.Info
	TabMeta
	// Attention: the tab waits for the user (a bell or a notification from its program, a
	// step that asks or stays quiet, a bring-back until it ends); it counts in "Needs you".
	Attention bool `json:"attention"`
}

// tab is what the app keeps for an open tab.
type tab struct {
	meta TabMeta
	spec pty.Spec
	// external ends the tab and runs its command in the user's terminal app (nil: not
	// offered); rerun starts the same spec in a new tab.
	external func(id string) error
	// exited is told once that the program ended.
	exited   func(pty.Info)
	notified bool // this waiting episode was notified
	exitSeen bool
	// background: the tab opened without bringing the window forward (a hand-off's step,
	// which may finish without the user); raised once it came forward because it waits
	// for the user or failed; raise is the timer of a quiet step's coming forward.
	background, raised bool
	raise              *time.Timer
	closing            bool // it ended well and closes (AutoClose)
}

// TabSetup is what an entry point opens a tab with, besides the program.
type TabSetup struct {
	Meta TabMeta
	// External ends tab id and runs its command in the user's terminal app (nil: the tab
	// does not offer it); Exited is told once that its program ended.
	External func(id string) error
	Exited   func(pty.Info)
	// Background opens the tab without bringing the window forward: it comes forward when
	// it waits for the user or fails (a hand-off's step, which may need nobody).
	Background bool
}

// TermPrefs are the terminal window's settings, sent with the list of tabs.
type TermPrefs struct {
	Collapsed    []string `json:"collapsed"`
	Placement    string   `json:"placement"`
	Grouping     string   `json:"grouping"`
	Appearance   string   `json:"appearance"`
	Font         string   `json:"font"`
	FontSize     int      `json:"fontSize"`
	Scrollback   int      `json:"scrollback"`
	ScreenReader bool     `json:"screenReader"`
	OS           string   `json:"os"`
	// Home is the home folder (the window shortens paths under it to ~); TerminalName is
	// the user's terminal app ("iTerm2"), for "Open in my terminal".
	Home         string `json:"home"`
	TerminalName string `json:"terminalName"`
}

// Terminals holds the app's terminal tabs and their window.
type Terminals struct {
	winURL          string // guarded by showMu
	winReady        bool   // guarded by showMu; WebView2 must finish creation before focus/navigation
	winPendingURL   string // guarded by showMu; latest show request during native creation
	embeddedVisible bool   // guarded by mu; main window presentation state
	host            terminalHost
	SavePlacement   func(string) error
	SaveGrouping    func(string, string, *bool) error
	mgr             *pty.Manager
	showMu          sync.Mutex // one terminal window at a time

	mu      sync.Mutex
	app     *application.App
	main    map[uint]bool // windows with the app's own page and its bindings
	mainWin *application.WebviewWindow
	win     *application.WebviewWindow
	winID   uint
	asked   time.Time // when a link's confirmation was last shown
	lists   map[*tabList]bool
	tabs    map[string]*tab
	active  string // the tab the terminal window shows
	badge   int

	// Prefs are the window's settings (nil: defaults); SavePrefs stores what the window
	// changed (a font size, the screen reader mode; nil: unchanged); Shell opens a shell
	// tab in a folder ("" home) for the window's "+"; NotifyOn says whether desktop
	// notifications are on. The App sets them.
	Prefs     func() TermPrefs
	SavePrefs func(fontSize int, reader *bool)
	Shell     func(dir string) error
	NotifyOn  func() bool
	// RaiseMain uses the desktop shell's readiness guard when it is attached.
	RaiseMain func()
	// AutoClose says whether a tab closes once its program ended well (Settings →
	// Terminal; nil: on).
	AutoClose func() bool
	// Emit sends the app's window an event (the App's emit).
	Emit func(name string, data any)

	notes *notifier
}

// tabList is a terminal window's TerminalTabsStream connection: the messages waiting for
// it. A newer list of tabs replaces one not yet sent, so a slow window always gets the
// latest without a queue growing.
type tabList struct {
	mu    sync.Mutex
	queue []listItem
	wake  chan struct{}
}

type listItem struct {
	list bool // a list of tabs (replaced by a newer one)
	b    []byte
}

func newTabList() *tabList { return &tabList{wake: make(chan struct{}, 1)} }

func (l *tabList) push(it listItem) {
	l.mu.Lock()
	if it.list {
		l.queue = slices.DeleteFunc(l.queue, func(x listItem) bool { return x.list })
	}
	if len(l.queue) < 64 {
		l.queue = append(l.queue, it)
	}
	l.mu.Unlock()
	select {
	case l.wake <- struct{}{}:
	default:
	}
}

func (l *tabList) take() []listItem {
	l.mu.Lock()
	defer l.mu.Unlock()
	q := l.queue
	l.queue = nil
	return q
}

// NewTerminals is the app's tabs; version is hopsesh's.
func NewTerminals(version string) *Terminals {
	t := &Terminals{main: map[uint]bool{}, lists: map[*tabList]bool{}, tabs: map[string]*tab{}}
	t.mgr = pty.NewManager(pty.Options{Version: version, OnChange: t.changed, MaxTabs: maxTabs})
	t.notes = newNotifier(func(id, title, body string) { osNotify(t, id, title, body) }, 10*time.Second)
	t.notes.still = func(id string) bool {
		t.mu.Lock()
		defer t.mu.Unlock()
		tb := t.tabs[id]
		return tb != nil && tb.notified
	}
	return t
}

// maxTabs is how many tabs may be open at once (the spec's 12 live tabs).
const maxTabs = 12

// Attach connects the tabs to the running app: its streams, and its window events.
func (t *Terminals) Attach(app *application.App) {
	t.mu.Lock()
	t.app = app
	t.mu.Unlock()
}

// Privileged marks a window as the app's own (its requests pass Gate); the first is the
// main window, which notifications and the terminal window's "back to sessions" raise.
func (t *Terminals) Privileged(w *application.WebviewWindow) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.main[w.ID()] = true
	if t.mainWin == nil {
		t.mainWin = w
	}
}

// privilegedID marks a window id as the app's own (tests).
func (t *Terminals) privilegedID(id uint) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.main[id] = true
}

// Manager is the tabs' manager.
func (t *Terminals) Manager() *pty.Manager { return t.mgr }

// CloseAll ends every tab (the app is quitting).
func (t *Terminals) CloseAll() {
	t.mgr.CloseAll()
	t.host.mu.Lock()
	defer t.host.mu.Unlock()
	if t.host.server != nil {
		_ = t.host.server.Close()
	}
	if t.host.pendingTimer != nil {
		t.host.pendingTimer.Stop()
	}
	for c := range t.host.conns {
		_ = c.c.CloseNow()
	}
	if t.host.cancel != nil {
		t.host.cancel()
	}
}

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
		// Native window IDs identify the containing window, not the calling frame.
		// Never let a cross-origin/sandboxed frame inherit main-window authority.
		origin := r.Header.Get("Origin")
		if origin != "" && origin != "http://wails.localhost" && origin != "wails://wails.localhost" && origin != "wails://localhost" && origin != "https://wails.localhost" {
			http.Error(w, "cross-origin app request", http.StatusForbidden)
			return
		}
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
			w.Header().Set("Content-Security-Policy", TerminalCSP)
			w.Header().Set("Cache-Control", "no-store")
		}
		next.ServeHTTP(w, r)
	})
}

// TerminalCSP keeps the terminal page to its own scripts, and its requests to the app's
// asset server (the streams): no inline script, no other origin, no frames, no forms, no
// plugins. Styles may be inline because xterm.js writes its scrollbar's and its DOM
// renderer's rules into <style> elements it makes; the page's own markup never carries a
// program's text as anything but text.
const TerminalCSP = "default-src 'none'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; font-src 'self'; connect-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'; object-src 'none'"

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

// ServeTab serves one TerminalStream connection that is known to come from the terminal
// window (the app checks the window first; the browser tests have only that page).
func (t *Terminals) ServeTab(c pty.Conn) {
	find := func(id string) (*pty.Session, error) {
		if s, ok := t.mgr.Get(id); ok {
			return s, nil
		}
		return nil, errors.New("no such tab")
	}
	_ = pty.Serve(c, find, func(u string) { t.confirmLink(u) })
}

// ServeList serves one TerminalTabsStream connection known to come from the terminal
// window: the tabs and settings now and on every change, and the window's requests.
func (t *Terminals) ServeList(c pty.Conn) {
	defer c.Close()
	l := newTabList()
	t.mu.Lock()
	t.lists[l] = true
	t.mu.Unlock()
	defer func() {
		t.mu.Lock()
		delete(t.lists, l)
		t.mu.Unlock()
	}()
	l.push(listItem{list: true})
	go func() {
		for {
			b, err := c.Receive()
			if err != nil {
				return
			}
			t.request(b)
		}
	}()
	for {
		select {
		case <-l.wake:
			for _, it := range l.take() {
				if it.list {
					it.b = t.listMessage()
				}
				if err := c.Send(it.b); err != nil {
					return
				}
			}
		case <-c.Context().Done():
			return
		}
	}
}

// listMessage is the window's list: every tab, and its settings.
func (t *Terminals) listMessage() []byte {
	b, _ := json.Marshal(map[string]any{"tabs": t.Tabs(), "prefs": t.prefs()})
	return b
}

func (t *Terminals) prefs() TermPrefs {
	if t.Prefs != nil {
		return t.Prefs()
	}
	return TermPrefs{Placement: "separate", Grouping: "family", FontSize: 13, Scrollback: 5000, OS: runtime.GOOS}
}

// send queues a message for every terminal window's list connection.
func (t *Terminals) send(m []byte) { t.queue(listItem{b: m}) }

// sendList queues a refresh, not a snapshot. Concurrent state changes can finish
// queueing out of order; taking the snapshot at delivery prevents an older list
// from replacing the captured link or other newly learned tab metadata.
func (t *Terminals) sendList() { t.queue(listItem{list: true}) }

func (t *Terminals) queue(it listItem) {
	t.mu.Lock()
	lists := make([]*tabList, 0, len(t.lists))
	for l := range t.lists {
		lists = append(lists, l)
	}
	t.mu.Unlock()
	for _, l := range lists {
		l.push(it)
	}
}

// TerminalPreferencesEvent synchronizes Settings with the terminal menu.
const TerminalPreferencesEvent = "hopsesh:terminal-preferences"

// Refresh sends both views their committed terminal preferences.
func (t *Terminals) Refresh() {
	t.sendList()
	if t.Emit != nil {
		t.Emit(TerminalPreferencesEvent, t.prefs())
	}
}

// windowRequest is what the terminal window may ask of hopsesh about its tabs; every
// field is checked, and a tab is looked up by its id.
type windowRequest struct {
	// Op: "active" (the window shows tab ID), "external" (end ID and open its command in
	// the user's terminal app), "rerun" (run ID's command again), "shell" (a new shell tab
	// in ID's folder, or home), "main" (raise the app's window), "maximize" (the
	// terminal window), "font" (Size), "reader" (On).
	Op   string `json:"op"`
	ID   string `json:"id"`
	Size int    `json:"size"`
	On   *bool  `json:"on"`
}

// request carries out one windowRequest.
func (t *Terminals) request(b []byte) {
	if len(b) > 512 {
		return
	}
	var r windowRequest
	if json.Unmarshal(b, &r) != nil {
		return
	}
	t.mu.Lock()
	tb := t.tabs[r.ID]
	var meta TabMeta
	if tb != nil {
		meta = tb.meta
	}
	t.mu.Unlock()
	switch r.Op {
	case "grouping", "collapse":
		if t.SaveGrouping != nil {
			if err := t.SaveGrouping(r.Op, r.ID, r.On); err != nil {
				t.notice("", err.Error())
			} else {
				t.Refresh()
			}
		}
	case "placement":
		if r.ID == "bottom" || r.ID == "right" || r.ID == "separate" {
			go t.moveWorkspace(r.ID)
		}
	case "active":
		if tb != nil {
			t.mu.Lock()
			t.active = r.ID
			t.mu.Unlock()
		}
	case "external":
		if tb != nil && meta.External && tb.external != nil {
			go func() {
				if err := tb.external(r.ID); err != nil {
					t.notice(r.ID, err.Error())
				}
			}()
		}
	case "rerun":
		if tb != nil && meta.Rerun {
			go t.rerun(r.ID, tb)
		}
	case "shell":
		dir := ""
		if tb != nil {
			dir = tb.spec.Dir
		}
		if t.Shell != nil {
			go func() {
				if err := t.Shell(dir); err != nil {
					t.notice(r.ID, err.Error())
				}
			}()
		}
	case "main":
		t.ShowMain()
		if t.Emit != nil {
			t.Emit("hopsesh:terminal-main", nil)
		}
	case "maximize":
		t.mu.Lock()
		win := t.win
		t.mu.Unlock()
		if win != nil {
			win.ToggleMaximise()
		}
	case "font":
		if r.Size >= 9 && r.Size <= 24 && t.SavePrefs != nil {
			t.SavePrefs(r.Size, nil)
		}
	case "reader":
		if r.On != nil && t.SavePrefs != nil {
			t.SavePrefs(0, r.On)
		}
	}
}

// notice tells the terminal window something hopsesh could not do about a tab.
func (t *Terminals) notice(id, msg string) {
	b, _ := json.Marshal(map[string]string{"notice": msg, "id": id})
	t.send(b)
}

// rerun starts a tab's command again in a new tab, in place of the ended one.
func (t *Terminals) rerun(id string, tb *tab) {
	if s, ok := t.mgr.Get(id); !ok || s.Info().State != pty.Exited {
		return
	}
	if tb.meta.Kind == TabSession && t.liveSessionTab(tb.meta.Machine, tb.meta.Key) != "" {
		t.notice(id, "It is running in another tab already.")
		return
	}
	_ = t.mgr.Close(id)
	if _, err := t.Open(tb.spec, TabSetup{Meta: tb.meta, External: tb.external, Exited: tb.exited}); err != nil {
		t.notice("", err.Error())
	}
}

// changed tells the app's window and the terminal window's list about a tab, notifies the
// user when a tab they cannot see starts waiting, and tells a tab's opener that it ended.
func (t *Terminals) changed(i pty.Info) {
	t.mu.Lock()
	tb := t.tabs[i.ID]
	app := t.app
	var (
		wait, forward, closeNow bool
		exited                  func(pty.Info)
		meta                    TabMeta
	)
	if tb != nil {
		meta = tb.meta
		att := attention(i, tb.meta)
		if att && !tb.notified {
			tb.notified, wait = true, true
		} else if !att {
			tb.notified = false
		}
		if i.State == pty.Exited && !tb.exitSeen {
			tb.exitSeen, exited = true, tb.exited
		}
		if tb.background && !tb.raised {
			// A background step comes forward when it waits for the user (at once for a
			// bell or a notification, after raiseAfter more of quiet), or when it failed.
			switch {
			case i.State == pty.Exited && i.Code > 0:
				forward = true
			case att && i.Reason != pty.ReasonIdle:
				forward = true
			case att && tb.raise == nil:
				id := i.ID
				tb.raise = time.AfterFunc(raiseAfter, func() {
					if s, ok := t.mgr.Get(id); ok && s.Info().State == pty.Waiting {
						t.comeForward(id)
					}
					t.mu.Lock()
					if tb := t.tabs[id]; tb != nil {
						tb.raise = nil
					}
					t.mu.Unlock()
				})
			case !att && tb.raise != nil:
				tb.raise.Stop()
				tb.raise = nil
			}
		}
		// A tab whose program ended well closes (a step once its link is known; a sign-in
		// closes itself, in its window).
		if i.State == pty.Exited && i.Code == 0 && !tb.closing && tb.meta.Kind != TabSignIn && (tb.meta.Kind != TabStep || tb.meta.Link != "") && t.autoClose() {
			tb.closing, closeNow = true, true
		}
	}
	if i.State == "" {
		delete(t.tabs, i.ID)
		if t.active == i.ID {
			t.active = ""
		}
	}
	t.mu.Unlock()
	t.sendList()
	if t.Emit != nil {
		t.Emit(TerminalEvent, TermTab{Info: i, TabMeta: meta, Attention: tb != nil && attention(i, meta)})
	}
	if app != nil {
		t.updateBadge()
	}
	if wait && !t.visible(i.ID) && t.notifyOn() {
		t.notes.waiting(i.ID, waitingText(meta, i.Title))
		t.flash()
	}
	if forward {
		go t.comeForward(i.ID)
	}
	if closeNow {
		t.closeEnded(i.ID)
	}
	if exited != nil {
		if (meta.Kind == TabStep || meta.Kind == TabSignIn || meta.Kind == TabBring) && !t.appFocused() && t.notifyOn() {
			t.notes.now(i.ID, endedText(meta, i))
		}
		go exited(i)
	}
}

// attention reports whether a tab waits for the user.
func attention(i pty.Info, m TabMeta) bool {
	switch {
	case i.State == pty.Exited || i.State == "":
		return false
	case m.Kind == TabBring && m.Saved == "":
		return true // the user sends a message (and the copy is saved then)
	case m.Kind == TabStep:
		return i.State == pty.Waiting // a question, or quiet: the cloud's command may wait for the user
	}
	return i.State == pty.Waiting && (i.Reason == pty.ReasonBell || i.Reason == pty.ReasonNotification)
}

func (t *Terminals) notifyOn() bool { return t.NotifyOn == nil || t.NotifyOn() }

func (t *Terminals) autoClose() bool { return t.AutoClose == nil || t.AutoClose() }

// visible: the terminal window has focus and shows the tab.
func (t *Terminals) visible(id string) bool {
	t.mu.Lock()
	win, main, active, embedded := t.win, t.mainWin, t.active, t.embeddedVisible
	t.mu.Unlock()
	if t.prefs().Placement != "separate" {
		return embedded && main != nil && active == id && main.IsFocused()
	}
	return win != nil && active == id && win.IsFocused()
}

// appFocused: one of hopsesh's windows has focus.
func (t *Terminals) appFocused() bool {
	t.mu.Lock()
	win, main := t.win, t.mainWin
	t.mu.Unlock()
	return win != nil && win.IsFocused() || main != nil && main.IsFocused()
}

// flash asks for attention on the taskbar (Windows) while the terminal window is in the
// background.
func (t *Terminals) flash() {
	t.mu.Lock()
	win := t.win
	t.mu.Unlock()
	if runtime.GOOS == "windows" && win != nil && !win.IsFocused() {
		win.Flash(true)
	}
}

// updateBadge shows how many tabs wait for the user on the Dock icon (macOS).
func (t *Terminals) updateBadge() {
	n := 0
	for _, d := range t.Tabs() {
		if d.Attention {
			n++
		}
	}
	t.mu.Lock()
	same := n == t.badge
	t.badge = n
	app := t.app
	t.mu.Unlock()
	if !same && app != nil {
		setBadge(n)
	}
}

// confirmLink asks the user whether to open a link a tab's program printed (already
// checked to be http or https), showing all of it; one question at a time.
func (t *Terminals) confirmLink(u string) {
	if linkHook != nil {
		linkHook(u)
		return
	}
	t.mu.Lock()
	app, w := t.app, t.win
	// A link is sent when the user clicks one, so a second request while one question
	// shows is a double click.
	if app == nil || time.Since(t.asked) < 2*time.Second {
		t.mu.Unlock()
		return
	}
	t.asked = time.Now()
	t.mu.Unlock()
	d := app.Dialog.Question().SetTitle("Open this link in your browser?").
		SetMessage("A program in a terminal tab asks to open this address:\n\n" + u + "\n\nOpen it only if you expected it.")
	d.AddButton("Open").OnClick(func() { _ = openInBrowser(u) })
	cancel := d.AddButton("Cancel")
	d.SetDefaultButton(cancel).SetCancelButton(cancel)
	if w != nil {
		d.AttachToWindow(w)
	}
	d.Show()
}

// linkHook replaces the link dialog (the browser tests: they see what would be asked; no
// browser opens).
var linkHook func(url string)

// SetLinkHook sends every link a tab asks to open to f instead of the dialog (tests).
func SetLinkHook(f func(url string)) { linkHook = f }

// Open opens a tab running spec's program, for what setup says, and shows it in the
// terminal window. Only hopsesh's own Go code calls it, with a program it chose, never
// with one a window sent: Terminals is not bound to the window.
func (t *Terminals) Open(spec pty.Spec, setup TabSetup) (pty.Info, error) {
	s, err := t.mgr.Start(spec)
	if err != nil {
		return pty.Info{}, err
	}
	t.mu.Lock()
	t.tabs[s.ID()] = &tab{meta: setup.Meta, spec: spec, external: setup.External, exited: setup.Exited, background: setup.Background}
	t.mu.Unlock()
	if _, ok := t.mgr.Get(s.ID()); !ok { // gone already
		t.mu.Lock()
		delete(t.tabs, s.ID())
		t.mu.Unlock()
	}
	t.changed(s.Info()) // again, now that the tab's purpose is known (it may have ended)
	if !setup.Background {
		t.Focus(s.ID())
	}
	return s.Info(), nil
}

// raiseAfter is how long a background step stays quiet (after the terminal's own idle
// time) before it comes forward as waiting for the user: a cloud's command pauses while
// it starts the session, a question waits.
const raiseAfter = 5 * time.Second

// closeAfter is how long a tab the window shows stays after its program ended well, so
// its last line can be read.
const closeAfter = 3 * time.Second

// comeForward shows a background tab (once): it waits for the user, or failed.
func (t *Terminals) comeForward(id string) {
	t.mu.Lock()
	tb := t.tabs[id]
	if tb == nil || tb.raised {
		t.mu.Unlock()
		return
	}
	tb.raised = true
	if tb.raise != nil {
		tb.raise.Stop()
	}
	t.mu.Unlock()
	t.Focus(id)
}

// closeEnded closes a tab whose program ended well: at once when the window doesn't show
// it, else after closeAfter.
func (t *Terminals) closeEnded(id string) {
	t.mu.Lock()
	shown := (t.win != nil || t.embeddedVisible) && t.active == id
	t.mu.Unlock()
	d := time.Duration(0)
	if shown {
		d = closeAfter
	}
	time.AfterFunc(d, func() {
		if s, ok := t.mgr.Get(id); ok && s.Info().State == pty.Exited {
			_ = t.mgr.Close(id)
		}
	})
}

// Focus shows the terminal window with tab id in front.
func (t *Terminals) Focus(id string) {
	t.Show()
	t.mu.Lock()
	if _, ok := t.tabs[id]; ok {
		t.active = id
	}
	t.mu.Unlock()
	b, _ := json.Marshal(map[string]string{"select": id})
	t.send(b)
}

// Show opens the terminal window, or brings it to the front.
func (t *Terminals) Show() {
	t.host.mu.Lock()
	pending, p := t.host.pending, t.host.pendingPlacement
	u := t.host.origin + "/terminal/?host=" + t.host.secret + "&view=" + pending
	t.host.mu.Unlock()
	if pending != "" {
		t.presentWorkspace(p, u, false)
		return
	}
	if p := t.prefs().Placement; p == "bottom" || p == "right" {
		u, err := t.hostURL(false)
		if err != nil {
			t.notice("", err.Error())
			return
		}
		t.ShowMain()
		if t.Emit != nil {
			t.Emit(TerminalWorkspaceEvent, TerminalWorkspaceDTO{URL: u, Placement: p})
		}
		return
	}
	u, err := t.hostURL(false)
	if err != nil {
		t.notice("", err.Error())
		return
	}
	t.showDetached(u)
}
func (t *Terminals) showDetached(u string) {
	t.showMu.Lock()
	defer t.showMu.Unlock()
	t.mu.Lock()
	app, win := t.app, t.win
	t.mu.Unlock()
	if app == nil {
		return
	}
	if win != nil {
		if !t.winReady {
			t.winPendingURL = u
			return
		}
		if t.winURL != u {
			win.SetURL(u)
			t.winURL = u
		}
		win.Show().Focus()
		return
	}
	w := app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:      "hopsesh-terminal",
		Title:     TerminalTitle,
		Width:     1040,
		Height:    680,
		MinWidth:  560,
		MinHeight: 320,
		URL:       u,
		// As the app's window: no title bar, the window's buttons inset into the tab strip,
		// which drags the window (--wails-draggable in terminal.css).
		Mac: application.MacWindow{TitleBar: application.MacTitleBarHiddenInset, InvisibleTitleBarHeight: 52},
	})
	t.winReady = runtime.GOOS != "windows"
	t.winPendingURL = ""
	t.mu.Lock()
	t.win, t.winID = w, w.ID()
	t.winURL = u
	t.mu.Unlock()
	// NewWithOptions returns before Windows finishes constructing WebView2.
	// A second Open/Show can otherwise enter Focus through its nested message
	// pump while the controller is nil. This native event also works for our
	// isolated page, which deliberately never loads the Wails runtime.
	w.OnWindowEvent(events.Windows.WebViewNavigationCompleted, func(*application.WindowEvent) {
		t.showMu.Lock()
		t.mu.Lock()
		current := t.win == w
		t.mu.Unlock()
		pending := ""
		if current && !t.winReady {
			t.winReady = true
			pending, t.winPendingURL = t.winPendingURL, ""
		}
		t.showMu.Unlock()
		if pending != "" {
			t.showDetached(pending)
		}
	})
	w.OnWindowEvent(events.Common.WindowClosing, func(*application.WindowEvent) {
		t.mu.Lock()
		if t.win == w {
			t.win, t.winID, t.active = nil, 0, ""
		}
		main := t.mainWin
		t.mu.Unlock()
		// The programs keep running; with the app's window hidden, it comes back so the
		// tabs (and quitting) stay within reach.
		if main != nil && !main.IsVisible() {
			t.ShowMain()
		}
	})
}

// ShowMain brings the app's window to the front.
func (t *Terminals) ShowMain() {
	if t.RaiseMain != nil {
		t.RaiseMain()
		return
	}
	t.mu.Lock()
	main := t.mainWin
	t.mu.Unlock()
	if main != nil {
		main.Show().Focus()
	}
}

// Toggle is Ctrl+`: from the terminal window back to the app's window, from anywhere else
// to the terminal window.
func (t *Terminals) Toggle() {
	t.mu.Lock()
	win := t.win
	t.mu.Unlock()
	if win != nil && win.IsFocused() {
		t.ShowMain()
		return
	}
	t.Show()
}

// WindowOpen reports whether the terminal window is open.
func (t *Terminals) WindowOpen() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.win != nil
}

// Tabs are the open tabs, oldest first.
func (t *Terminals) Tabs() []TermTab {
	infos := t.mgr.List()
	out := make([]TermTab, 0, len(infos))
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, i := range infos {
		d := TermTab{Info: i}
		if tb := t.tabs[i.ID]; tb != nil {
			d.TabMeta = tb.meta
			d.Attention = attention(i, tb.meta)
		}
		out = append(out, d)
	}
	return out
}

// tabOf is an open tab's setup.
func (t *Terminals) tabOf(id string) (*tab, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	tb, ok := t.tabs[id]
	return tb, ok
}

// setMeta changes what the windows show about a tab.
func (t *Terminals) setMeta(id string, f func(*TabMeta)) {
	t.mu.Lock()
	tb := t.tabs[id]
	if tb != nil {
		f(&tb.meta)
	}
	t.mu.Unlock()
	if tb != nil {
		if s, ok := t.mgr.Get(id); ok {
			t.changed(s.Info())
		}
	}
}

// liveSessionTab is the tab whose program still runs a session (machine, key), if any: a
// session's, or the bring-back's that saved it.
func (t *Terminals) liveSessionTab(machine, key string) string {
	for _, d := range t.Tabs() {
		if (d.Kind == TabSession || d.Kind == TabBring) && d.Machine == machine && d.Key == key && d.State != pty.Exited {
			return d.ID
		}
	}
	return ""
}

// close ends a tab and waits for its program.
func (t *Terminals) close(id string) error { return t.mgr.Close(id) }

// TerminalTabs are the terminal's open tabs (state, title, exit code, what each is for).
func (a *App) TerminalTabs() []TermTab {
	if a.Terms == nil {
		return []TermTab{}
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

// TerminalFocus shows the terminal window with one of its tabs in front.
func (a *App) TerminalFocus(id string) error {
	if a.Terms == nil {
		return errors.New("no terminal")
	}
	if _, ok := a.Terms.tabOf(id); !ok {
		return errors.New("no such tab")
	}
	a.Terms.Focus(id)
	return nil
}

// TerminalOpenElsewhere ends a tab and runs its command in the user's terminal app ("Move
// to Terminal…", which the window confirmed with the user).
func (a *App) TerminalOpenElsewhere(id string) error {
	if a.Terms == nil {
		return errors.New("no terminal")
	}
	a.Terms.mu.Lock()
	tb, ok := a.Terms.tabs[id]
	allowed := ok && tb.meta.External && tb.external != nil
	a.Terms.mu.Unlock()
	if !allowed {
		return errors.New("this tab cannot open in your terminal app")
	}
	return tb.external(id)
}

// TerminalCloseTab closes a tab, ending its program.
func (a *App) TerminalCloseTab(id string) error {
	if a.Terms == nil {
		return errors.New("no terminal")
	}
	if !slices.ContainsFunc(a.Terms.Tabs(), func(i TermTab) bool { return i.ID == id }) {
		return errors.New("no such tab")
	}
	return a.Terms.mgr.Close(id)
}
