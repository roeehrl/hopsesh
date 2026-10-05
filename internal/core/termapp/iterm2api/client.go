package iterm2api

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
	"google.golang.org/protobuf/encoding/protowire"
)

const (
	subprotocol    = "api.iterm2.com"
	libraryVersion = "hopsesh-go 1"
	defaultAppName = "hopsesh"
	// maxMessage bounds one message from iTerm2 (a layout of many windows is the largest
	// thing the client asks for).
	maxMessage = 8 << 20
)

// DefaultSocketPath is where iTerm2 listens for API connections: the Unix socket under
// its Application Support folder. It is empty off macOS.
func DefaultSocketPath() string {
	if runtime.GOOS != "darwin" {
		return ""
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, "Library", "Application Support", "iTerm2", "private", "socket")
}

// Options configure Connect and Probe. The zero value is right for hopsesh on macOS.
type Options struct {
	// SocketPath is iTerm2's API socket; empty means DefaultSocketPath.
	SocketPath string
	// TCPAddress, when set, is tried if SocketPath does not exist (iTerm2 before the Unix
	// socket listened on localhost:1912). Off by default: a credential sent to a TCP port
	// could reach any local program listening there.
	TCPAddress string
	// AppName is the advisory name iTerm2 shows for this client; empty means "hopsesh".
	AppName string
	// Credentials fetches a fresh cookie and key; nil means AppleScriptCredentials.
	Credentials CredentialSource
	// Timeout bounds the connection and each handshake; zero means 5 seconds.
	Timeout time.Duration
}

func (o Options) withDefaults() Options {
	if o.SocketPath == "" {
		o.SocketPath = DefaultSocketPath()
	}
	if o.AppName == "" {
		o.AppName = defaultAppName
	}
	if o.Credentials == nil {
		o.Credentials = AppleScriptCredentials
	}
	if o.Timeout == 0 {
		o.Timeout = 5 * time.Second
	}
	return o
}

type endpoint struct{ network, address string }

func (o Options) endpoint() (endpoint, error) {
	if o.SocketPath != "" {
		if _, err := os.Stat(o.SocketPath); err == nil {
			return endpoint{"unix", o.SocketPath}, nil
		}
	}
	if o.TCPAddress != "" {
		return endpoint{"tcp", o.TCPAddress}, nil
	}
	return endpoint{}, ErrUnavailable
}

// handshake opens the WebSocket. With empty credentials it is the probe: iTerm2 answers 401
// when its API is on and wants a cookie (x-iterm2-disable-auth-ui stops it from showing its
// own dialog instead).
func (o Options) handshake(ctx context.Context, ep endpoint, c Credentials) (*websocket.Conn, *http.Response, error) {
	ctx, cancel := context.WithTimeout(ctx, o.Timeout)
	defer cancel()
	d := net.Dialer{Timeout: o.Timeout}
	tr := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return d.DialContext(ctx, ep.network, ep.address)
		},
		Proxy:             nil,
		DisableKeepAlives: true,
	}
	h := http.Header{}
	h.Set("Origin", "ws://localhost/")
	h.Set("X-iTerm2-Library-Version", libraryVersion)
	h.Set("X-iTerm2-Disable-Auth-UI", "true")
	h.Set("X-iTerm2-Advisory-Name", o.AppName)
	if !c.empty() {
		h.Set("X-iTerm2-Cookie", c.cookie)
		h.Set("X-iTerm2-Key", c.key)
	}
	conn, resp, err := websocket.Dial(ctx, "ws://localhost/", &websocket.DialOptions{
		HTTPClient:   &http.Client{Transport: tr},
		HTTPHeader:   h,
		Subprotocols: []string{subprotocol},
	})
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	return conn, resp, err
}

// classify maps a failed handshake to one of the package errors. It never includes the
// request (which may carry credentials).
func classify(resp *http.Response) error {
	if resp == nil {
		return ErrUnavailable
	}
	switch resp.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		return ErrNotAuthorized
	case http.StatusNotAcceptable:
		return ErrTooOld
	}
	return fmt.Errorf("%w (HTTP %d)", ErrUnavailable, resp.StatusCode)
}

// Probe reports whether iTerm2's API is listening, without credentials and without any
// prompt: nil if it is (and would accept a cookie), ErrUnavailable otherwise.
func Probe(ctx context.Context, opts Options) error {
	o := opts.withDefaults()
	ep, err := o.endpoint()
	if err != nil {
		return err
	}
	conn, resp, err := o.handshake(ctx, ep, Credentials{})
	if err == nil {
		_ = conn.CloseNow()
		return nil
	}
	if e := classify(resp); errors.Is(e, ErrNotAuthorized) {
		return nil
	} else {
		return e
	}
}

// Connect opens an authenticated connection to iTerm2's API. Any error means the caller
// should use its AppleScript path.
//
// Order: the API socket must exist; a credential-free handshake must show the server
// listening (so a user who has not enabled the API never sees a consent prompt); only then
// is a cookie requested, once, and used for one handshake.
func Connect(ctx context.Context, opts Options) (*Client, error) {
	o := opts.withDefaults()
	ep, err := o.endpoint()
	if err != nil {
		return nil, err
	}
	creds := takeEnvCredentials()
	fromEnv := !creds.empty()
	if !fromEnv {
		conn, resp, err := o.handshake(ctx, ep, Credentials{})
		if err == nil { // the user's iTerm2 lets local clients in without a cookie
			return newClient(conn, resp), nil
		}
		if e := classify(resp); !errors.Is(e, ErrNotAuthorized) {
			return nil, e
		}
		if creds, err = o.Credentials(ctx, o.AppName); err != nil {
			return nil, err
		}
	}
	conn, resp, err := o.handshake(ctx, ep, creds)
	creds = Credentials{}
	if err == nil {
		return newClient(conn, resp), nil
	}
	e := classify(resp)
	if fromEnv && errors.Is(e, ErrNotAuthorized) {
		// The inherited cookie was stale; ask once through AppleScript.
		if creds, err = o.Credentials(ctx, o.AppName); err != nil {
			return nil, err
		}
		conn, resp, err = o.handshake(ctx, ep, creds)
		if err == nil {
			return newClient(conn, resp), nil
		}
		e = classify(resp)
	}
	return nil, e
}

// Client is one authenticated connection to iTerm2's API. Its methods are safe for
// concurrent use.
type Client struct {
	conn     *websocket.Conn
	protocol string

	mu      sync.Mutex
	nextID  int64
	pending map[int64]chan serverMessage
	closed  bool

	writeMu sync.Mutex

	events     *eventQueue
	subscribed atomic.Bool
	closing    atomic.Bool
	done       chan struct{}
	err        error
}

func newClient(conn *websocket.Conn, resp *http.Response) *Client {
	conn.SetReadLimit(maxMessage)
	c := &Client{
		conn:    conn,
		pending: map[int64]chan serverMessage{},
		events:  newEventQueue(),
		done:    make(chan struct{}),
	}
	if resp != nil {
		c.protocol = resp.Header.Get("X-iTerm2-Protocol-Version")
	}
	go c.readLoop()
	return c
}

// ProtocolVersion is the protocol version iTerm2 reported ("1.18"), or "" if it did not.
func (c *Client) ProtocolVersion() string { return c.protocol }

// Done is closed when the connection ends; Err then says why.
func (c *Client) Done() <-chan struct{} { return c.done }

// Err is why the connection ended (nil while it is open, or after Close).
func (c *Client) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.err
}

// Close ends the connection.
func (c *Client) Close() error {
	c.closing.Store(true)
	c.events.abandon()
	// No close handshake: iTerm2's own client does not wait for one either, and a busy
	// iTerm2 would otherwise hold Close for seconds.
	err := c.conn.CloseNow()
	<-c.done
	if errors.Is(err, net.ErrClosed) {
		err = nil
	}
	return err
}

// Events delivers the notifications the client subscribed to, in order. It is closed when
// the connection ends.
func (c *Client) Events() <-chan Event { return c.events.out }

func (c *Client) readLoop() {
	var err error
	defer func() {
		c.mu.Lock()
		c.err = err
		c.closed = true
		for id, ch := range c.pending {
			close(ch)
			delete(c.pending, id)
		}
		c.mu.Unlock()
		c.events.close()
		close(c.done)
	}()
	ctx := context.Background()
	for {
		var data []byte
		_, data, err = c.conn.Read(ctx)
		if err != nil {
			if c.closing.Load() || websocket.CloseStatus(err) == websocket.StatusNormalClosure {
				err = nil
			}
			return
		}
		m, derr := decodeServerMessage(data)
		if derr != nil {
			continue // a message the client cannot parse is skipped, not fatal
		}
		if m.hasField && m.field == envNotification && !m.hasID {
			evs, derr := decodeNotification(m.body)
			if derr == nil && c.subscribed.Load() {
				for _, e := range evs {
					c.events.push(e)
				}
			}
			continue
		}
		if !m.hasID {
			continue
		}
		c.mu.Lock()
		ch := c.pending[m.id]
		delete(c.pending, m.id)
		c.mu.Unlock()
		if ch != nil {
			ch <- m
		}
	}
}

// ErrClosed is returned by requests on a connection that has ended.
var ErrClosed = errors.New("iterm2api: connection closed")

// RequestError is iTerm2 refusing a request: Status is the response's status enum, or
// Message is set when iTerm2 called the request malformed.
type RequestError struct {
	Request string
	Status  uint64
	Message string
}

func (e *RequestError) Error() string {
	if e.Message != "" {
		return fmt.Sprintf("iterm2api: %s: %s", e.Request, e.Message)
	}
	return fmt.Sprintf("iterm2api: %s: status %d", e.Request, e.Status)
}

// call sends one request and waits for its response body.
func (c *Client) call(ctx context.Context, r request) ([]byte, error) {
	ch := make(chan serverMessage, 1)
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, ErrClosed
	}
	c.nextID++
	id := c.nextID
	c.pending[id] = ch
	c.mu.Unlock()

	c.writeMu.Lock()
	err := c.conn.Write(ctx, websocket.MessageBinary, encodeClientMessage(id, r))
	c.writeMu.Unlock()
	if err != nil {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return nil, fmt.Errorf("iterm2api: send: %w", err)
	}
	select {
	case m, ok := <-ch:
		if !ok {
			return nil, ErrClosed
		}
		if m.isError {
			return nil, &RequestError{Request: requestName(r.field()), Message: m.errText}
		}
		if m.field != r.field() {
			return nil, fmt.Errorf("iterm2api: %s: unexpected response %d", requestName(r.field()), m.field)
		}
		return m.body, nil
	case <-ctx.Done():
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return nil, ctx.Err()
	}
}

func requestName(n protowire.Number) string {
	switch n {
	case fieldNotification:
		return "subscribe"
	case fieldListSessions:
		return "list sessions"
	case fieldCreateTab:
		return "create tab"
	case fieldSplitPane:
		return "split pane"
	case fieldActivate:
		return "activate"
	case fieldVariable:
		return "variable"
	case fieldFocus:
		return "focus"
	}
	return fmt.Sprintf("request %d", n)
}
