// Package fakeiterm2 is a stand-in for iTerm2's API server, for tests: it listens on a
// Unix socket, speaks the WebSocket subprotocol and the protobuf messages that
// iterm2api's client uses, and keeps a small model of windows, tabs and sessions. It
// answers only those requests; anything else is answered with the protocol's "malformed
// request" error and recorded in Refused, so a test can prove the client never sent it.
// It never starts or talks to a real iTerm2.
package fakeiterm2

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/coder/websocket"
	"google.golang.org/protobuf/encoding/protowire"
)

// Server is a fake iTerm2 API server.
type Server struct {
	// Path is the Unix socket the server listens on.
	Path string

	dir string
	ln  net.Listener
	hs  *http.Server

	mu sync.Mutex
	// RequireAuth makes the server demand a cookie and key it issued (each usable once);
	// otherwise any handshake is accepted.
	requireAuth bool
	creds       map[string]string // cookie → key
	headers     []http.Header
	requests    []int
	refused     []int
	subs        map[*conn]map[uint64]bool
	conns       map[*conn]bool

	nextWin, nextTab, nextSess, nextTTY int
	windows                             []*window
	sessions                            map[string]*session
	keyWindow                           string
	activated                           []string
}

type window struct {
	id       string
	tabs     []*tab
	selected int // the selected tab's id
}

type tab struct {
	id       int
	sessions []string
	active   string
}

type session struct {
	id      string
	tty     string
	vars    map[string]string
	props   map[string]string
	profile string
}

type conn struct {
	ws *websocket.Conn
	mu sync.Mutex
}

// Start starts a server on a fresh socket in a new temporary directory (kept short: Unix
// socket paths are limited to about 100 bytes). requireAuth makes it demand credentials.
func Start(requireAuth bool) (*Server, error) {
	dir, err := os.MkdirTemp("", "it2")
	if err != nil {
		return nil, err
	}
	s := &Server{
		Path:        filepath.Join(dir, "socket"),
		dir:         dir,
		requireAuth: requireAuth,
		creds:       map[string]string{},
		subs:        map[*conn]map[uint64]bool{},
		conns:       map[*conn]bool{},
		sessions:    map[string]*session{},
	}
	if err := s.listen(); err != nil {
		os.RemoveAll(dir)
		return nil, err
	}
	return s, nil
}

func (s *Server) listen() error {
	ln, err := net.Listen("unix", s.Path)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.ln = ln
	s.hs = &http.Server{Handler: http.HandlerFunc(s.serveHTTP)}
	hs := s.hs
	s.mu.Unlock()
	go func() { _ = hs.Serve(ln) }()
	return nil
}

// Stop stops listening and drops every connection, as if the user turned the API off or
// quit iTerm2. The socket file is removed.
func (s *Server) Stop() {
	s.mu.Lock()
	hs := s.hs
	s.hs = nil
	s.mu.Unlock()
	if hs != nil {
		_ = hs.Close()
	}
	s.DropConnections()
	_ = os.Remove(s.Path)
}

// Restart listens again on the same socket after Stop.
func (s *Server) Restart() error { return s.listen() }

// Close stops the server and removes its directory.
func (s *Server) Close() {
	s.Stop()
	_ = os.RemoveAll(s.dir)
}

// DropConnections closes every client connection (the server keeps listening).
func (s *Server) DropConnections() {
	s.mu.Lock()
	var cs []*conn
	for c := range s.conns {
		cs = append(cs, c)
	}
	s.mu.Unlock()
	for _, c := range cs {
		_ = c.ws.CloseNow()
	}
}

// Issue mints a single-use cookie and key, as iTerm2's AppleScript command does.
func (s *Server) Issue() (cookie, key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := len(s.creds) + len(s.headers) + 1
	cookie, key = fmt.Sprintf("c00k1e%06d", n), fmt.Sprintf("k3y%06d", n)
	s.creds[cookie] = key
	return cookie, key
}

// Headers returns the handshake headers received so far.
func (s *Server) Headers() []http.Header {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]http.Header(nil), s.headers...)
}

// Requests returns the request field numbers received, in order.
func (s *Server) Requests() []int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]int(nil), s.requests...)
}

// Refused returns field numbers of requests the fake does not implement (and a real
// client of hopsesh's must never send).
func (s *Server) Refused() []int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]int(nil), s.refused...)
}

// Activated returns the session ids focused through the API, in order.
func (s *Server) Activated() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.activated...)
}

// Connections is how many clients are connected.
func (s *Server) Connections() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.conns)
}

// Subscribers is how many connections subscribed to notification type kind.
func (s *Server) Subscribers(kind uint64) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, m := range s.subs {
		if m[kind] {
			n++
		}
	}
	return n
}

// AddWindow adds a window with one tab and one session, makes it the key window, and
// returns the window and session ids.
func (s *Server) AddWindow() (windowID, sessionID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	w := s.newWindowLocked()
	sess := s.newSessionLocked("", nil)
	t := s.newTabLocked(sess.id)
	w.tabs = append(w.tabs, t)
	w.selected = t.id
	s.keyWindow = w.id
	return w.id, sess.id
}

func (s *Server) newWindowLocked() *window {
	s.nextWin++
	w := &window{id: fmt.Sprintf("pty-%d", s.nextWin)}
	s.windows = append(s.windows, w)
	return w
}

func (s *Server) newTabLocked(sess string) *tab {
	s.nextTab++
	return &tab{id: s.nextTab, sessions: []string{sess}, active: sess}
}

func (s *Server) newSessionLocked(profile string, props map[string]string) *session {
	s.nextSess++
	s.nextTTY++
	sess := &session{
		id:      fmt.Sprintf("%08X-0000-4000-8000-%012d", s.nextSess, s.nextSess),
		tty:     fmt.Sprintf("/dev/ttys%03d", s.nextTTY),
		vars:    map[string]string{},
		props:   props,
		profile: profile,
	}
	s.sessions[sess.id] = sess
	return sess
}

// TTY returns a session's TTY.
func (s *Server) TTY(id string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if x := s.sessions[id]; x != nil {
		return x.tty
	}
	return ""
}

// Props returns the profile overrides a session was created with.
func (s *Server) Props(id string) map[string]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if x := s.sessions[id]; x != nil {
		out := map[string]string{}
		for k, v := range x.props {
			out[k] = v
		}
		return out
	}
	return nil
}

// Vars returns the variables set on a session.
func (s *Server) Vars(id string) map[string]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if x := s.sessions[id]; x != nil {
		out := map[string]string{}
		for k, v := range x.vars {
			out[k] = v
		}
		return out
	}
	return nil
}

// Locate returns the window holding a session and the session's index in its tab.
func (s *Server) Locate(id string) (windowID string, tabID int, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, w := range s.windows {
		for _, t := range w.tabs {
			for _, x := range t.sessions {
				if x == id {
					return w.id, t.id, true
				}
			}
		}
	}
	return "", 0, false
}

// Terminate ends a session, removes it from its tab, and notifies subscribers.
func (s *Server) Terminate(id string) {
	s.mu.Lock()
	s.removeLocked(id)
	s.mu.Unlock()
	var b []byte
	b = appendString(b, 1, id)
	s.Notify(7, b)
}

// TerminateQuietly removes a session without a notification (as if it closed while no
// client was connected).
func (s *Server) TerminateQuietly(id string) {
	s.mu.Lock()
	s.removeLocked(id)
	s.mu.Unlock()
}

func (s *Server) removeLocked(id string) {
	delete(s.sessions, id)
	for _, w := range s.windows {
		for _, t := range w.tabs {
			for i, x := range t.sessions {
				if x == id {
					t.sessions = append(t.sessions[:i], t.sessions[i+1:]...)
					break
				}
			}
		}
	}
}

// FocusSession sends a focus-changed notification naming a session.
func (s *Server) FocusSession(id string) {
	var b []byte
	b = appendString(b, 4, id)
	s.Notify(9, b)
}

// Notify sends a Notification with field kind set to payload to every connection
// subscribed to kind (subscription types and Notification fields share numbers for the
// kinds the client uses).
func (s *Server) Notify(kind uint64, payload []byte) {
	s.mu.Lock()
	var cs []*conn
	for c, m := range s.subs {
		if m[kind] {
			cs = append(cs, c)
		}
	}
	s.mu.Unlock()
	var n []byte
	n = appendBytes(n, protowire.Number(kind), payload)
	var msg []byte
	msg = appendBytes(msg, 1000, n)
	for _, c := range cs {
		c.send(msg)
	}
}

// SendRaw sends bytes as one message to every connection (to test that the client
// ignores what it did not ask for).
func (s *Server) SendRaw(msg []byte) {
	s.mu.Lock()
	var cs []*conn
	for c := range s.conns {
		cs = append(cs, c)
	}
	s.mu.Unlock()
	for _, c := range cs {
		c.send(msg)
	}
}

func (c *conn) send(msg []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	_ = c.ws.Write(context.Background(), websocket.MessageBinary, msg)
}

func (s *Server) serveHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	s.headers = append(s.headers, r.Header.Clone())
	ok := true
	if s.requireAuth {
		cookie, key := r.Header.Get("X-iTerm2-Cookie"), r.Header.Get("X-iTerm2-Key")
		want, issued := s.creds[cookie]
		ok = cookie != "" && issued && want == key
		if ok {
			delete(s.creds, cookie) // single use
		}
	}
	s.mu.Unlock()
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if !strings.Contains(r.Header.Get("Sec-WebSocket-Protocol"), "api.iterm2.com") {
		http.Error(w, "subprotocol", http.StatusBadRequest)
		return
	}
	w.Header().Set("X-iTerm2-Protocol-Version", "1.19")
	ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		Subprotocols:   []string{"api.iterm2.com"},
		OriginPatterns: []string{"localhost"},
	})
	if err != nil {
		return
	}
	c := &conn{ws: ws}
	s.mu.Lock()
	s.conns[c] = true
	s.subs[c] = map[uint64]bool{}
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.conns, c)
		delete(s.subs, c)
		s.mu.Unlock()
		_ = ws.CloseNow()
	}()
	for {
		_, data, err := ws.Read(context.Background())
		if err != nil {
			return
		}
		if resp := s.handle(c, data); resp != nil {
			c.send(resp)
		}
	}
}

func (s *Server) handle(c *conn, data []byte) []byte {
	var id uint64
	var field protowire.Number
	var body []byte
	_ = walk(data, func(n protowire.Number, t protowire.Type, v []byte, u uint64) {
		if n == 1 && t == protowire.VarintType {
			id = u
		} else if t == protowire.BytesType {
			field, body = n, v
		}
	})
	s.mu.Lock()
	s.requests = append(s.requests, int(field))
	s.mu.Unlock()
	var out []byte
	out = protowire.AppendTag(out, 1, protowire.VarintType)
	out = protowire.AppendVarint(out, id)
	var resp []byte
	var err error
	switch field {
	case 103:
		resp, err = s.subscribe(c, body)
	case 106:
		resp = s.listSessions()
	case 108:
		resp, err = s.createTab(body)
	case 109:
		resp, err = s.splitPane(body)
	case 114:
		resp = s.activate(body)
	case 115:
		resp = s.variable(body)
	case 117:
		resp = s.focus()
	default:
		s.mu.Lock()
		s.refused = append(s.refused, int(field))
		s.mu.Unlock()
		err = errors.New("request not supported by the fake")
	}
	if err != nil {
		return appendString(out, 2, err.Error())
	}
	return appendBytes(out, field, resp)
}

func (s *Server) subscribe(c *conn, b []byte) ([]byte, error) {
	var kind uint64
	var sub, hasArgs bool
	_ = walk(b, func(n protowire.Number, t protowire.Type, _ []byte, u uint64) {
		switch {
		case n == 2 && t == protowire.VarintType:
			sub = u != 0
		case n == 3 && t == protowire.VarintType:
			kind = u
		case n >= 4:
			hasArgs = true
		}
	})
	if hasArgs {
		return nil, errors.New("monitor arguments not supported by the fake")
	}
	s.mu.Lock()
	m := s.subs[c]
	st := uint64(0)
	if m != nil {
		if sub && m[kind] {
			st = 4
		}
		m[kind] = sub
	}
	s.mu.Unlock()
	return appendVarint(nil, 1, st), nil
}

func (s *Server) listSessions() []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []byte
	for _, w := range s.windows {
		var wb []byte
		for _, t := range w.tabs {
			if len(t.sessions) == 0 {
				continue
			}
			var root []byte
			root = appendBool(root, 1, true)
			for _, sid := range t.sessions {
				var summary []byte
				summary = appendString(summary, 1, sid)
				summary = appendString(summary, 4, "a title the client must ignore")
				var link []byte
				link = appendBytes(link, 1, summary)
				root = appendBytes(root, 2, link)
			}
			var tb []byte
			tb = appendString(tb, 2, fmt.Sprint(t.id))
			tb = appendBytes(tb, 3, root)
			tb = appendString(tb, 7, t.active)
			wb = appendBytes(wb, 1, tb)
		}
		wb = appendString(wb, 2, w.id)
		if w.selected != 0 {
			wb = appendString(wb, 5, fmt.Sprint(w.selected))
		}
		out = appendBytes(out, 1, wb)
	}
	return out
}

func readProps(v []byte, props map[string]string) {
	var k, val string
	_ = walk(v, func(n protowire.Number, t protowire.Type, v []byte, _ uint64) {
		switch n {
		case 1:
			k = string(v)
		case 2:
			val = string(v)
		}
	})
	props[k] = val
}

func (s *Server) createTab(b []byte) ([]byte, error) {
	var profile, win string
	props := map[string]string{}
	var bad error
	_ = walk(b, func(n protowire.Number, t protowire.Type, v []byte, _ uint64) {
		switch n {
		case 1:
			profile = string(v)
		case 2:
			win = string(v)
		case 5:
			readProps(v, props)
		default:
			bad = fmt.Errorf("create tab field %d not supported by the fake", n)
		}
	})
	if bad != nil {
		return nil, bad
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var w *window
	if win == "" {
		w = s.newWindowLocked()
	} else {
		for _, x := range s.windows {
			if x.id == win {
				w = x
			}
		}
		if w == nil {
			return appendVarint(nil, 1, 2), nil // INVALID_WINDOW_ID
		}
	}
	sess := s.newSessionLocked(profile, props)
	t := s.newTabLocked(sess.id)
	w.tabs = append(w.tabs, t)
	w.selected = t.id
	var out []byte
	out = appendVarint(out, 1, 0)
	out = appendString(out, 2, w.id)
	out = appendVarint(out, 3, uint64(t.id))
	out = appendString(out, 4, sess.id)
	return out, nil
}

func (s *Server) splitPane(b []byte) ([]byte, error) {
	var target, profile string
	props := map[string]string{}
	var bad error
	_ = walk(b, func(n protowire.Number, t protowire.Type, v []byte, _ uint64) {
		switch n {
		case 1:
			target = string(v)
		case 2, 3:
		case 4:
			profile = string(v)
		case 5:
			readProps(v, props)
		default:
			bad = fmt.Errorf("split field %d not supported by the fake", n)
		}
	})
	if bad != nil {
		return nil, bad
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, w := range s.windows {
		for _, t := range w.tabs {
			for _, x := range t.sessions {
				if x == target {
					sess := s.newSessionLocked(profile, props)
					t.sessions = append(t.sessions, sess.id)
					var out []byte
					out = appendVarint(out, 1, 0)
					return appendString(out, 2, sess.id), nil
				}
			}
		}
	}
	return appendVarint(nil, 1, 1), nil // SESSION_NOT_FOUND
}

func (s *Server) activate(b []byte) []byte {
	var id string
	_ = walk(b, func(n protowire.Number, t protowire.Type, v []byte, _ uint64) {
		if n == 3 && t == protowire.BytesType {
			id = string(v)
		}
	})
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sessions[id] == nil {
		return appendVarint(nil, 1, 1) // BAD_IDENTIFIER
	}
	s.activated = append(s.activated, id)
	for _, w := range s.windows {
		for _, t := range w.tabs {
			for _, x := range t.sessions {
				if x == id {
					t.active = id
					w.selected = t.id
					s.keyWindow = w.id
				}
			}
		}
	}
	return appendVarint(nil, 1, 0)
}

func (s *Server) variable(b []byte) []byte {
	var id string
	var gets []string
	sets := map[string]string{}
	_ = walk(b, func(n protowire.Number, t protowire.Type, v []byte, _ uint64) {
		switch n {
		case 1:
			id = string(v)
		case 2:
			var k, val string
			_ = walk(v, func(n protowire.Number, _ protowire.Type, v []byte, _ uint64) {
				switch n {
				case 1:
					k = string(v)
				case 2:
					val = string(v)
				}
			})
			sets[k] = val
		case 3:
			gets = append(gets, string(v))
		}
	})
	s.mu.Lock()
	defer s.mu.Unlock()
	sess := s.sessions[id]
	if sess == nil {
		return appendVarint(nil, 1, 1) // SESSION_NOT_FOUND
	}
	for k, v := range sets {
		if !strings.HasPrefix(k, "user.") {
			return appendVarint(nil, 1, 2) // INVALID_NAME
		}
		sess.vars[k] = v
	}
	var out []byte
	out = appendVarint(out, 1, 0)
	for _, g := range gets {
		switch g {
		case "tty":
			out = appendString(out, 2, `"`+sess.tty+`"`)
		default:
			out = appendString(out, 2, "null")
		}
	}
	return out
}

func (s *Server) focus() []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []byte
	active := appendBool(nil, 1, true)
	out = appendBytes(out, 1, active)
	for _, w := range s.windows {
		st := uint64(2)
		if w.id == s.keyWindow {
			st = 0
		}
		var wb []byte
		wb = appendVarint(wb, 1, st)
		wb = appendString(wb, 2, w.id)
		var f []byte
		f = appendBytes(f, 2, wb)
		out = appendBytes(out, 1, f)
	}
	return out
}

func walk(b []byte, f func(n protowire.Number, t protowire.Type, v []byte, u uint64)) error {
	for len(b) > 0 {
		n, t, l := protowire.ConsumeTag(b)
		if l < 0 {
			return protowire.ParseError(l)
		}
		b = b[l:]
		switch t {
		case protowire.BytesType:
			v, m := protowire.ConsumeBytes(b)
			if m < 0 {
				return protowire.ParseError(m)
			}
			f(n, t, v, 0)
			b = b[m:]
		case protowire.VarintType:
			u, m := protowire.ConsumeVarint(b)
			if m < 0 {
				return protowire.ParseError(m)
			}
			f(n, t, nil, u)
			b = b[m:]
		default:
			m := protowire.ConsumeFieldValue(n, t, b)
			if m < 0 {
				return protowire.ParseError(m)
			}
			b = b[m:]
		}
	}
	return nil
}

func appendBytes(b []byte, n protowire.Number, v []byte) []byte {
	b = protowire.AppendTag(b, n, protowire.BytesType)
	return protowire.AppendBytes(b, v)
}

func appendString(b []byte, n protowire.Number, v string) []byte {
	b = protowire.AppendTag(b, n, protowire.BytesType)
	return protowire.AppendString(b, v)
}

func appendVarint(b []byte, n protowire.Number, v uint64) []byte {
	b = protowire.AppendTag(b, n, protowire.VarintType)
	return protowire.AppendVarint(b, v)
}

func appendBool(b []byte, n protowire.Number, v bool) []byte {
	return appendVarint(b, n, protowire.EncodeBool(v))
}
