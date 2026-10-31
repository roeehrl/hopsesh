// Package serve is how hopsesh reaches the hopsesh running in the user's login session on
// the same machine (hopsesh serve): a private socket in a folder only the user can open,
// one connection per conversation. A connection starts with one JSON line saying what it
// carries, answered by one JSON line; then it carries that.
//
// hopsesh serve runs where the user's sign-ins and apps are (a Mac's Keychain, its desktop
// apps), which a process started over SSH cannot reach, and it owns the terminals of
// sessions run on this machine for another one, so they outlive the connection that
// shows them.
package serve

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Modes: what a connection carries after its first line.
const (
	// ModePeer is the peer protocol (internal/core/peer), as hopsesh peer --stdio speaks it.
	ModePeer = "peer"
	// ModeTerminal is one terminal tab's stream (internal/core/pty), in frames.
	ModeTerminal = "terminal"
	// ModePing asks only whether hopsesh serve is there, and which.
	ModePing = "ping"
	// ModeClient is hopsesh's client API (internal/core/clientapi), for other programs
	// on this machine.
	ModeClient = "client"
)

// Hello is a connection's first line.
type Hello struct {
	Mode string `json:"mode"`
}

// Welcome is the answer to it.
type Welcome struct {
	OK      bool   `json:"ok"`
	Version string `json:"version,omitempty"` // hopsesh serve's version
	PID     int    `json:"pid,omitempty"`
	Error   string `json:"error,omitempty"`
}

// ErrNotRunning means no hopsesh serve answers on this machine.
var ErrNotRunning = errors.New("hopsesh is not running in the login session on this machine")

// ErrRunning means another hopsesh serve already runs.
var ErrRunning = errors.New("hopsesh serve is already running")

// Dir is the private folder of the socket and its lock.
func Dir(home string) string { return filepath.Join(home, ".hopsesh", "run") }

// SocketPath is where hopsesh serve listens. A Unix socket's path is limited to about
// 100 bytes; a longer home folder uses a private folder under /tmp instead.
func SocketPath(home string) string {
	p := filepath.Join(Dir(home), "serve.sock")
	if len(p) > 100 && os.PathSeparator == '/' {
		return filepath.Join("/tmp", fmt.Sprintf("hopsesh-%d", os.Getuid()), "serve.sock")
	}
	return p
}

// private makes dir (0700) and refuses one that is a link, someone else's, or open to others.
func private(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	fi, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if !fi.IsDir() || fi.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%s is not a folder", dir)
	}
	return checkOwner(dir, fi)
}

// Listener is hopsesh serve's socket, held with its lock.
type Listener struct {
	net.Listener
	lock *os.File
	path string
}

// Close stops listening and releases the lock.
func (l *Listener) Close() error {
	err := l.Listener.Close()
	_ = os.Remove(l.path)
	_ = l.lock.Close()
	return err
}

// Listen takes the lock (ErrRunning when another hopsesh serve holds it) and listens.
func Listen(home string) (*Listener, error) {
	sock := SocketPath(home)
	for _, d := range []string{Dir(home), filepath.Dir(sock)} {
		if err := private(d); err != nil {
			return nil, err
		}
	}
	lock, err := os.OpenFile(filepath.Join(Dir(home), "serve.lock"), os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	if err = lockFile(lock); err != nil {
		lock.Close()
		return nil, ErrRunning
	}
	// Under the lock no other hopsesh serve listens: a socket left there is stale.
	if fi, err := os.Lstat(sock); err == nil {
		if fi.Mode()&os.ModeSocket == 0 {
			lock.Close()
			return nil, fmt.Errorf("%s is not a socket; not replacing it", sock)
		}
		_ = os.Remove(sock)
	}
	ln, err := net.Listen("unix", sock)
	if err != nil {
		lock.Close()
		return nil, err
	}
	_ = os.Chmod(sock, 0o600)
	return &Listener{Listener: ln, lock: lock, path: sock}, nil
}

// Accept returns the next connection from this user (others are closed), with its first
// line.
func (l *Listener) Accept() (net.Conn, *bufio.Reader, Hello, error) {
	for {
		c, err := l.Listener.Accept()
		if err != nil {
			return nil, nil, Hello{}, err
		}
		if !sameUser(c) {
			c.Close()
			continue
		}
		_ = c.SetReadDeadline(time.Now().Add(10 * time.Second))
		r := bufio.NewReader(c)
		var h Hello
		line, err := r.ReadBytes('\n')
		if err != nil || json.Unmarshal(line, &h) != nil {
			c.Close()
			continue
		}
		_ = c.SetReadDeadline(time.Time{})
		return c, r, h, nil
	}
}

// Reply answers a connection's first line.
func Reply(c net.Conn, w Welcome) error {
	b, _ := json.Marshal(w)
	_, err := c.Write(append(b, '\n'))
	return err
}

// Dial connects to hopsesh serve on this machine for mode (ErrNotRunning when none
// answers).
func Dial(ctx context.Context, home, mode string) (net.Conn, *bufio.Reader, Welcome, error) {
	var w Welcome
	var d net.Dialer
	dctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	c, err := d.DialContext(dctx, "unix", SocketPath(home))
	if err != nil {
		return nil, nil, w, ErrNotRunning
	}
	b, _ := json.Marshal(Hello{Mode: mode})
	_ = c.SetDeadline(time.Now().Add(10 * time.Second))
	if _, err = c.Write(append(b, '\n')); err != nil {
		c.Close()
		return nil, nil, w, ErrNotRunning
	}
	r := bufio.NewReader(c)
	line, err := r.ReadBytes('\n')
	if err != nil || json.Unmarshal(line, &w) != nil {
		c.Close()
		return nil, nil, w, ErrNotRunning
	}
	_ = c.SetDeadline(time.Time{})
	if !w.OK {
		c.Close()
		return nil, nil, w, errors.New("hopsesh serve: " + w.Error)
	}
	return c, r, w, nil
}

// Running reports hopsesh serve's version on this machine ("" when none answers).
func Running(ctx context.Context, home string) string {
	c, _, w, err := Dial(ctx, home, ModePing)
	if err != nil {
		return ""
	}
	c.Close()
	return w.Version
}

// Proxy copies between a connection to hopsesh serve and standard input and output until
// either ends: how hopsesh peer --stdio hands a conversation over SSH to it.
func Proxy(c net.Conn, buffered *bufio.Reader, in io.Reader, out io.Writer) error {
	go func() {
		// The input ending (the other machine is done asking) still lets the answers
		// come back; then hopsesh serve ends the conversation.
		_, _ = io.Copy(c, in)
		if cw, ok := c.(interface{ CloseWrite() error }); ok {
			_ = cw.CloseWrite()
		} else {
			_ = c.Close()
		}
	}()
	_, err := io.Copy(out, buffered)
	c.Close()
	return err
}

// MaxFrame bounds one frame of a terminal stream.
const MaxFrame = 1 << 20

// WriteFrame writes one frame: its length (big-endian uint32), then it.
func WriteFrame(w io.Writer, p []byte) error {
	if len(p) > MaxFrame {
		return fmt.Errorf("frame of %d bytes is too large", len(p))
	}
	var n [4]byte
	binary.BigEndian.PutUint32(n[:], uint32(len(p)))
	if _, err := w.Write(n[:]); err != nil {
		return err
	}
	_, err := w.Write(p)
	return err
}

// ReadFrame reads one frame.
func ReadFrame(r io.Reader) ([]byte, error) {
	var n [4]byte
	if _, err := io.ReadFull(r, n[:]); err != nil {
		return nil, err
	}
	size := binary.BigEndian.Uint32(n[:])
	if size > MaxFrame {
		return nil, fmt.Errorf("frame of %d bytes is too large", size)
	}
	p := make([]byte, size)
	_, err := io.ReadFull(r, p)
	return p, err
}

// FrameConn carries whole frames over a byte stream: a terminal tab's stream between
// hopsesh serve and what shows the tab (pty.Conn).
type FrameConn struct {
	ctx    context.Context
	cancel context.CancelFunc
	r      io.Reader
	w      io.Writer
	close  func() error
	mu     sync.Mutex
	once   sync.Once
}

// NewFrameConn reads frames from r and writes them to w; close ends both.
func NewFrameConn(ctx context.Context, r io.Reader, w io.Writer, close func() error) *FrameConn {
	ctx, cancel := context.WithCancel(ctx)
	return &FrameConn{ctx: ctx, cancel: cancel, r: r, w: w, close: close}
}

func (f *FrameConn) Context() context.Context { return f.ctx }

func (f *FrameConn) Send(p []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.ctx.Err() != nil {
		return f.ctx.Err()
	}
	return WriteFrame(f.w, p)
}

func (f *FrameConn) Receive() ([]byte, error) {
	p, err := ReadFrame(f.r)
	if err != nil {
		f.cancel()
	}
	return p, err
}

func (f *FrameConn) Close() error {
	var err error
	f.once.Do(func() {
		f.cancel()
		if f.close != nil {
			err = f.close()
		}
	})
	return err
}
