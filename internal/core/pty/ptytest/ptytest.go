// Package ptytest is a tab's window for tests: it connects to a tab through pty.Serve
// over an in-memory stream, draws the output in a terminal emulator (charmbracelet/x/vt)
// that answers the program's queries as xterm.js does in the app, acks what it drew, and
// types what the test types (as the user would; hopsesh never does).
package ptytest

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/charmbracelet/x/vt"

	"github.com/roeehrl/hopsesh/internal/core/pty"
)

// Conn is an in-memory stream: Send hands a frame to the window (blocking while Depth
// frames wait, like a Wails stream's bounded buffer), Receive takes the window's next
// frame.
type Conn struct {
	toWindow chan []byte
	toTab    chan []byte
	ctx      context.Context
	cancel   context.CancelFunc
}

// NewConn is a stream holding at most depth frames for the window.
func NewConn(depth int) *Conn {
	ctx, cancel := context.WithCancel(context.Background())
	return &Conn{toWindow: make(chan []byte, depth), toTab: make(chan []byte, 1024), ctx: ctx, cancel: cancel}
}

func (c *Conn) Send(f []byte) error {
	select {
	case c.toWindow <- f:
		return nil
	case <-c.ctx.Done():
		return errors.New("closed")
	}
}

func (c *Conn) Receive() ([]byte, error) {
	select {
	case f := <-c.toTab:
		return f, nil
	case <-c.ctx.Done():
		return nil, errors.New("closed")
	}
}

func (c *Conn) Context() context.Context { return c.ctx }
func (c *Conn) Close() error             { c.cancel(); return nil }

// Window is a tab's window.
type Window struct {
	Conn *Conn
	// Served is Serve's result once it returned.
	Served chan error

	answered atomic.Int64 // replies the emulator sent (not under mu: the emulator writes them while drawing)

	mu    sync.Mutex
	emu   *vt.Emulator
	infos []pty.Info
	errs  []string
	drawn int // output bytes drawn
	acked int
	noAck bool
}

// Options shape a Window.
type Options struct {
	Cols, Rows int
	// Depth is how many frames the stream holds for the window (0: 256).
	Depth int
	// NoAck: the window draws but never acks (until Ack).
	NoAck bool
	// Paused: the window does not read the stream until Resume.
	Paused bool
}

// Open attaches a window to tab id, served by find (and openLink for links).
func Open(find func(string) (*pty.Session, error), openLink func(string), id string, o Options) *Window {
	if o.Cols == 0 {
		o.Cols = 100
	}
	if o.Rows == 0 {
		o.Rows = 30
	}
	if o.Depth == 0 {
		o.Depth = 256
	}
	w := &Window{Conn: NewConn(o.Depth), Served: make(chan error, 1), emu: vt.NewEmulator(o.Cols, o.Rows), noAck: o.NoAck}
	w.send(append([]byte{'a'}, id...))
	w.Resize(o.Cols, o.Rows)
	go func() { w.Served <- pty.Serve(w.Conn, find, openLink) }()
	go w.answer()
	if !o.Paused {
		go w.draw()
	}
	return w
}

func (w *Window) send(f []byte) {
	select {
	case w.Conn.toTab <- f:
	case <-w.Conn.ctx.Done():
	}
}

// Resume starts reading the stream of a window opened Paused.
func (w *Window) Resume() { go w.draw() }

// draw takes the frames hopsesh sends.
func (w *Window) draw() {
	for {
		var f []byte
		select {
		case f = <-w.Conn.toWindow:
		case <-w.Conn.ctx.Done():
			return
		}
		if len(f) == 0 {
			continue
		}
		switch f[0] {
		case 'o':
			w.mu.Lock()
			_, _ = w.emu.Write(f[1:])
			w.drawn += len(f) - 1
			ack := !w.noAck
			w.mu.Unlock()
			if ack {
				w.Ack(len(f) - 1)
			}
		case 's':
			var i pty.Info
			_ = json.Unmarshal(f[1:], &i)
			w.mu.Lock()
			w.infos = append(w.infos, i)
			w.mu.Unlock()
		case 'e':
			w.mu.Lock()
			w.errs = append(w.errs, string(f[1:]))
			w.mu.Unlock()
		}
	}
}

// answer sends the emulator's replies to the program's queries, as a terminal does.
func (w *Window) answer() {
	buf := make([]byte, 1024)
	for {
		n, err := w.emu.Read(buf)
		if n > 0 {
			w.answered.Add(1)
			w.send(append([]byte{'i'}, buf[:n]...))
		}
		if err != nil {
			return
		}
	}
}

// Type sends keys, as the user types them.
func (w *Window) Type(s string) { w.send(append([]byte{'i'}, s...)) }

// Resize sends the window's new size (and resizes its emulator).
func (w *Window) Resize(cols, rows int) {
	w.mu.Lock()
	w.emu.Resize(cols, rows)
	w.mu.Unlock()
	f := []byte{'r', 0, 0, 0, 0}
	binary.BigEndian.PutUint16(f[1:], uint16(cols)) //nolint:gosec // test sizes
	binary.BigEndian.PutUint16(f[3:], uint16(rows)) //nolint:gosec // test sizes
	w.send(f)
}

// Ack says n more bytes were drawn.
func (w *Window) Ack(n int) {
	w.mu.Lock()
	w.acked += n
	w.mu.Unlock()
	f := []byte{'k', 0, 0, 0, 0}
	binary.BigEndian.PutUint32(f[1:], uint32(n)) //nolint:gosec // test sizes
	w.send(f)
}

// AckAll acks everything drawn so far and acks from now on.
func (w *Window) AckAll() {
	w.mu.Lock()
	n := w.drawn - w.acked
	w.noAck = false
	w.mu.Unlock()
	if n > 0 {
		w.Ack(n)
	}
}

// CloseTab asks hopsesh to close the tab.
func (w *Window) CloseTab() { w.send([]byte{'c'}) }

// Link asks hopsesh to open a link.
func (w *Window) Link(u string) { w.send(append([]byte{'l'}, u...)) }

// Detach is the window going away.
func (w *Window) Detach() {
	_ = w.Conn.Close()
	// Ends the emulator's replies (its Close would race with the reader).
	if pw, ok := w.emu.InputPipe().(*io.PipeWriter); ok {
		_ = pw.Close()
	}
}

// Screen is what the emulator shows.
func (w *Window) Screen() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.emu.String()
}

// Drawn is how many bytes of output the window drew.
func (w *Window) Drawn() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.drawn
}

// Answered is how many replies to queries the emulator sent.
func (w *Window) Answered() int { return int(w.answered.Load()) }

// Last is the last state hopsesh sent (zero before any).
func (w *Window) Last() pty.Info {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.infos) == 0 {
		return pty.Info{}
	}
	return w.infos[len(w.infos)-1]
}

// States are the states hopsesh sent, in order.
func (w *Window) States() []pty.Info {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]pty.Info(nil), w.infos...)
}

// Errors are the refusals hopsesh sent.
func (w *Window) Errors() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]string(nil), w.errs...)
}

// WaitFor waits until the screen shows text.
func (w *Window) WaitFor(text string, d time.Duration) error {
	return w.Until(func() bool { return strings.Contains(w.Screen(), text) }, d, func() string {
		return fmt.Sprintf("%q never appeared; the screen:\n%s", text, w.Screen())
	})
}

// WaitState waits until hopsesh says the tab is in state st.
func (w *Window) WaitState(st pty.State, d time.Duration) (pty.Info, error) {
	err := w.Until(func() bool { return w.Last().State == st }, d, func() string {
		return fmt.Sprintf("the tab never became %s (last %+v); the screen:\n%s", st, w.Last(), w.Screen())
	})
	return w.Last(), err
}

// Until polls ok for up to d.
func (w *Window) Until(ok func() bool, d time.Duration, why func() string) error {
	end := time.Now().Add(d)
	for time.Now().Before(end) {
		if ok() {
			return nil
		}
		time.Sleep(20 * time.Millisecond)
	}
	if ok() {
		return nil
	}
	return errors.New(why())
}
