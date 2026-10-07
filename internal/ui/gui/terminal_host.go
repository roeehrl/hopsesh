package gui

// Terminal views live on their own loopback origin. This listener exposes only
// terminal assets and two typed streams, never app bindings. A random capability,
// exact Host and websocket Origin checks prevent ambient browser access. The
// embedded iframe is sandboxed and cannot navigate or script its parent.
import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"net"
	"net/http"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
)

const TerminalWorkspaceEvent = "hopsesh:terminal-workspace"

type terminalHost struct {
	mu                        sync.Mutex
	server                    *http.Server
	origin, secret            string
	generation                string
	cancel                    context.CancelFunc
	pending, pendingPlacement string
	pendingTimer              *time.Timer
	conns                     map[*terminalSocket]string
}

func token() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

func (t *Terminals) hostURL(fresh bool) (string, error) {
	t.host.mu.Lock()
	defer t.host.mu.Unlock()
	h := &t.host
	if h.server == nil {
		l, err := net.Listen("tcp4", "127.0.0.1:0")
		if err != nil {
			return "", err
		}
		h.origin = "http://" + l.Addr().String()
		h.secret = token()
		h.server = &http.Server{Handler: http.HandlerFunc(t.terminalHTTP), ReadHeaderTimeout: 5 * time.Second}
		server := h.server
		go func() {
			if err := server.Serve(l); err != nil && !errors.Is(err, http.ErrServerClosed) {
				t.notice("", "Terminal view server stopped: "+err.Error())
			}
		}()
	}
	if fresh || h.generation == "" {
		h.generation = token()
	}
	return h.origin + "/terminal/?host=" + h.secret + "&view=" + h.generation, nil
}

func (t *Terminals) terminalHTTP(w http.ResponseWriter, r *http.Request) {
	h := &t.host
	h.mu.Lock()
	origin, secret, generation, pending := h.origin, h.secret, h.generation, h.pending
	requested := r.URL.Query().Get("view")
	validView := requested == generation || pending != "" && requested == pending
	h.mu.Unlock()
	if r.Host != strings.TrimPrefix(origin, "http://") {
		http.Error(w, "invalid host", http.StatusForbidden)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if r.URL.Path == "/stream" {
		if r.URL.Query().Get("host") != secret || !validView || (r.Header.Get("Origin") != origin && r.Header.Get("Origin") != "null") {
			http.Error(w, "expired terminal view", http.StatusForbidden)
			return
		}
		name := r.URL.Query().Get("name")
		if name != TerminalStream && name != TerminalTabsStream {
			http.Error(w, "unknown stream", http.StatusForbidden)
			return
		}
		c, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true}) // origin and capability checked above, including opaque sandbox origins
		if err != nil {
			return
		}
		c.SetReadLimit(9 << 20)
		ctx, cancel := context.WithCancel(r.Context())
		defer cancel()
		conn := &terminalSocket{c: c, ctx: ctx, valid: func() bool {
			h.mu.Lock()
			defer h.mu.Unlock()
			return h.generation == requested
		}}
		if name == TerminalTabsStream && requested == pending {
			if !t.commitWorkspace(requested) {
				conn.Close()
				return
			}
		}
		h.mu.Lock()
		if h.generation != requested {
			h.mu.Unlock()
			conn.Close()
			return
		}
		if h.conns == nil {
			h.conns = map[*terminalSocket]string{}
		}
		h.conns[conn] = requested
		h.mu.Unlock()
		defer func() { h.mu.Lock(); delete(h.conns, conn); h.mu.Unlock() }()
		if name == TerminalTabsStream {
			// One workspace owner. Revocation also closes every PTY connection from
			// the old generation, including late resize/input/close requests.
			h.mu.Lock()
			if h.generation != requested {
				h.mu.Unlock()
				conn.Close()
				return
			}
			if h.cancel != nil {
				h.cancel()
			}
			owner, revoke := context.WithCancel(context.Background())
			h.cancel = revoke
			h.mu.Unlock()
			defer revoke()
			go func() {
				select {
				case <-owner.Done():
					conn.Close()
				case <-ctx.Done():
				}
			}()
			t.ServeList(conn)
		} else {
			h.mu.Lock()
			valid := h.generation == requested
			h.mu.Unlock()
			if !valid {
				conn.Close()
				return
			}
			// PTY ownership is additionally fenced inside pty.Serve, even when a
			// transport delivers already buffered frames after it was replaced.
			t.ServeTab(conn)
		}
		return
	}
	if r.Method != "GET" && r.Method != "HEAD" || !strings.HasPrefix(r.URL.Path, "/terminal/") || strings.Contains(r.URL.Path, "..") {
		http.Error(w, "not available to terminal", http.StatusForbidden)
		return
	}
	// Only the document carries the capability. Scripts are immutable public
	// assets; they have no authority without the document's random token.
	if r.URL.Path == "/terminal/" && (r.URL.Query().Get("host") != secret || !validView) {
		http.Error(w, "invalid terminal view", http.StatusForbidden)
		return
	}
	policy := strings.Replace(TerminalCSP, "frame-ancestors 'none'", "frame-ancestors 'self' wails://localhost http://wails.localhost wails://wails.localhost http://127.0.0.1:*", 1)
	// WebKitGTK resolves 'self' against the sandbox's opaque origin. Name this
	// listener explicitly so the same restricted assets can load in that frame;
	// never broaden this to arbitrary loopback ports or remove the sandbox.
	policy = strings.ReplaceAll(policy, "'self'", origin)
	policy = strings.Replace(policy, "connect-src "+origin, "connect-src "+origin+" "+strings.Replace(origin, "http://", "ws://", 1), 1)
	w.Header().Set("Content-Security-Policy", policy)
	// Bundled browser assets have known types. Windows file associations can
	// override Go's MIME table, which must not disable strict module loading.
	switch path.Ext(r.URL.Path) {
	case ".js", ".mjs":
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	case ".css":
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
	}
	sub, _ := fs.Sub(Assets, "assets")
	http.FileServer(http.FS(sub)).ServeHTTP(w, r)
}

type terminalSocket struct {
	c     *websocket.Conn
	ctx   context.Context
	valid func() bool
}

func (c *terminalSocket) Context() context.Context { return c.ctx }
func (c *terminalSocket) Send(b []byte) error      { return c.c.Write(c.ctx, websocket.MessageBinary, b) }
func (c *terminalSocket) Receive() ([]byte, error) {
	_, b, e := c.c.Read(c.ctx)
	if e == nil && c.valid != nil && !c.valid() {
		return nil, errors.New("expired terminal view")
	}
	return b, e
}
func (c *terminalSocket) Close() error { return c.c.Close(websocket.StatusNormalClosure, "") }

type TerminalWorkspaceDTO struct {
	URL       string `json:"url"`
	Placement string `json:"placement"`
}

func (a *App) TerminalWorkspace() (TerminalWorkspaceDTO, error) {
	if a.Terms == nil {
		return TerminalWorkspaceDTO{}, errors.New("terminal unavailable")
	}
	u, e := a.Terms.hostURL(false)
	p := a.Terms.prefs().Placement
	h := &a.Terms.host
	h.mu.Lock()
	if h.pending != "" {
		u = h.origin + "/terminal/?host=" + h.secret + "&view=" + h.pending
		p = h.pendingPlacement
	}
	h.mu.Unlock()
	return TerminalWorkspaceDTO{URL: u, Placement: p}, e
}

// TerminalPlacement asks the existing renderer to checkpoint before rehosting.
// Changing bottom/right never changes the renderer, stream or process.
func (a *App) TerminalPlacement(placement string) error {
	if placement != "bottom" && placement != "right" && placement != "separate" {
		return errors.New("invalid terminal placement")
	}
	a.Terms.requestPlacement(placement)
	return nil
}
func (t *Terminals) requestPlacement(p string) {
	t.mu.Lock()
	viewing := len(t.lists) > 0
	t.mu.Unlock()
	if viewing && (p == "separate") != (t.prefs().Placement == "separate") {
		t.sendJSON(map[string]string{"rehost": p})
		return
	}
	t.moveWorkspace(p)
}
func (t *Terminals) sendJSON(v any) { b, _ := json.Marshal(v); t.send(b) }

// Rehosting is staged: the old view stays visible and authorized until the
// replacement has loaded its modules and opened its list connection. A failed
// renderer never strands the user in a hidden or blank terminal.
func (t *Terminals) moveWorkspace(p string) {
	previous := t.prefs().Placement
	if t.SavePlacement == nil {
		return
	}
	u, err := t.hostURL(false)
	if err != nil {
		t.notice("", err.Error())
		return
	}
	if (previous == "separate") == (p == "separate") {
		if err := t.SavePlacement(p); err != nil {
			t.notice("", err.Error())
			return
		}
		t.presentWorkspace(p, u, true)
		t.Refresh()
		return
	}
	h := &t.host
	h.mu.Lock()
	if h.pending != "" {
		h.mu.Unlock()
		t.notice("", "A terminal move is already in progress.")
		return
	}
	next := token()
	h.pending = next
	h.pendingPlacement = p
	u = h.origin + "/terminal/?host=" + h.secret + "&view=" + next
	h.pendingTimer = time.AfterFunc(8*time.Second, func() { t.rollbackWorkspace(next, previous) })
	h.mu.Unlock()
	t.presentWorkspace(p, u, false)
}

func (t *Terminals) presentWorkspace(p, u string, committed bool) {
	t.mu.Lock()
	w := t.win
	t.mu.Unlock()
	if p != "separate" {
		if committed && w != nil {
			w.Hide()
		}
		t.ShowMain()
		if t.Emit != nil {
			t.Emit(TerminalWorkspaceEvent, TerminalWorkspaceDTO{URL: u, Placement: p})
		}
	} else {
		if committed && t.Emit != nil {
			t.Emit(TerminalWorkspaceEvent, TerminalWorkspaceDTO{Placement: p})
		}
		t.showDetached(u)
	}
}
func (t *Terminals) commitWorkspace(next string) bool {
	h := &t.host
	h.mu.Lock()
	if h.pending != next {
		h.mu.Unlock()
		return false
	}
	p := h.pendingPlacement
	// Saving precedes revocation. On failure the original capability stays valid.
	if err := t.SavePlacement(p); err != nil {
		h.mu.Unlock()
		t.notice("", err.Error())
		return false
	}
	h.generation = next
	h.pending = ""
	h.pendingPlacement = ""
	if h.pendingTimer != nil {
		h.pendingTimer.Stop()
		h.pendingTimer = nil
	}
	var old []*terminalSocket
	for c, g := range h.conns {
		if g != next {
			old = append(old, c)
		}
	}
	u := h.origin + "/terminal/?host=" + h.secret + "&view=" + next
	h.mu.Unlock()
	for _, c := range old {
		go c.c.Close(websocket.StatusPolicyViolation, "terminal view moved")
	}
	t.presentWorkspace(p, u, true)
	t.Refresh()
	return true
}
func (t *Terminals) rollbackWorkspace(next, previous string) {
	h := &t.host
	h.mu.Lock()
	if h.pending != next {
		h.mu.Unlock()
		return
	}
	h.pending = ""
	h.pendingPlacement = ""
	h.pendingTimer = nil
	u := h.origin + "/terminal/?host=" + h.secret + "&view=" + h.generation
	h.mu.Unlock()
	t.presentWorkspace(previous, u, true)
	t.notice("", "Couldn’t load the new terminal view. Your programs are still running in the original view.")
}

// TerminalWorkspaceVisible reports presentation only; it grants no PTY authority.
func (a *App) TerminalWorkspaceVisible(visible bool) {
	if a.Terms == nil {
		return
	}
	a.Terms.mu.Lock()
	a.Terms.embeddedVisible = visible
	a.Terms.mu.Unlock()
}
