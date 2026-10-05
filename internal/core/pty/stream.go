package pty

import (
	"context"
	"encoding/binary"
	"errors"
	"net/url"
	"strings"
	"sync"
)

// The stream between a tab and its window (a Wails stream in the app). Every frame's
// first byte says what it is; the rest is its body.
//
// From the window:
//
//	'a' <tab id>          attach: the first frame, once; the tab's state, its scrollback
//	                      and then its live output follow
//	'i' <bytes>           input: the user's keys, paste or the emulator's answers, as UTF-8
//	'r' <cols> <rows>     resize: two big-endian uint16
//	'k' <n>               ack: the window drew n more bytes of output (big-endian uint32)
//	'c'                   close the tab (ending its program)
//	'l' <url>             open a link the user clicked: http(s) only, after the user
//	                      confirms it in a dialog hopsesh shows
//
// To the window:
//
//	'o' <bytes>           output, at most 64 KiB a frame (raw bytes: write them to the
//	                      emulator as a Uint8Array, which decodes UTF-8 across frames)
//	's' <json>            the tab's Info: state (running, waiting, exited), reason, exit
//	                      code, size
//	'e' <text>            something hopsesh refused or could not do
//
// Flow control: hopsesh stops reading the program while the window has more than 512 KiB
// of output it has not acked, and goes on once that is under 10 KiB. A window acks after
// the emulator wrote a chunk (xterm.js: write's callback).
const (
	opAttach = 'a'
	opInput  = 'i'
	opResize = 'r'
	opAck    = 'k'
	opClose  = 'c'
	opLink   = 'l'
)

// Conn is a tab window's connection: a Wails stream (*application.StreamConn) in the app,
// anything that sends and receives whole frames in tests.
type Conn interface {
	Send([]byte) error
	Receive() ([]byte, error)
	Context() context.Context
	Close() error
}

// Serve runs one window's connection to a tab: the first frame names the tab, which find
// returns (the app checks that the window may show it); then output and state go to the
// window and its input, sizes and acks to the tab until either side ends. openLink is
// asked to open a link the user clicked, already checked to be http(s) (nil: links are
// refused). Serve closes conn.
func Serve(conn Conn, find func(id string) (*Session, error), openLink func(url string)) error {
	defer conn.Close()
	first, err := conn.Receive()
	if err != nil {
		return err
	}
	if len(first) < 2 || first[0] != opAttach {
		_ = conn.Send(errorFrame("the first frame must name the tab"))
		return errors.New("no attach frame")
	}
	s, err := find(string(first[1:]))
	if err != nil {
		_ = conn.Send(errorFrame(err.Error()))
		return err
	}
	v := s.attach()
	ctx, cancel := context.WithCancel(conn.Context())
	defer cancel()
	defer s.detach(v)
	go func() {
		<-ctx.Done()
		s.detach(v)
	}()
	go func() {
		for {
			f, ok := v.next()
			if !ok {
				// The tab is gone, or another window took it: this window's stream ends.
				_ = conn.Close()
				return
			}
			if err := conn.Send(f); err != nil {
				cancel()
				return
			}
		}
	}()
	for {
		f, err := conn.Receive()
		if err != nil {
			return nil // the window went away
		}
		if len(f) == 0 {
			continue
		}
		switch body := f[1:]; f[0] {
		case opInput:
			if err := s.typed(body); err != nil {
				s.notice(v, err.Error())
			}
		case opResize:
			if len(body) == 4 {
				if err := s.resize(int(binary.BigEndian.Uint16(body)), int(binary.BigEndian.Uint16(body[2:]))); err != nil {
					s.notice(v, "resizing the terminal: "+err.Error())
				}
			}
		case opAck:
			if len(body) == 4 {
				s.acked(v, int(binary.BigEndian.Uint32(body)))
			}
		case opClose:
			_ = s.m.Close(s.id)
			return nil
		case opLink:
			target, ok := LinkTarget(string(body))
			switch {
			case !ok:
				s.notice(v, "hopsesh opens only web links (http and https)")
			case openLink == nil:
				s.notice(v, "links do not open from this tab")
			default:
				openLink(target)
			}
		default:
			s.notice(v, "unknown frame")
		}
	}
}

func errorFrame(msg string) []byte { return append([]byte{frameError}, msg...) }

// LinkTarget is the address a tab's link may open: an http or https URL with a host, at
// most 2,048 bytes and without control characters, in a canonical form (so the dialog
// shows what will open). Anything else (javascript:, file:, an app's own scheme) is
// refused.
func LinkTarget(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > 2048 {
		return "", false
	}
	for _, r := range raw {
		if r < 0x20 || r == 0x7f || r >= 0x80 && r < 0xa0 {
			return "", false
		}
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.Opaque != "" {
		return "", false
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
	default:
		return "", false
	}
	u.Scheme = strings.ToLower(u.Scheme)
	return u.String(), true
}

// viewer is the window attached to a tab: the frames waiting for it, and the output it
// has not acked yet. Its fields are guarded by the session's lock.
type viewer struct {
	ready   *sync.Cond
	items   [][]byte
	unacked int
	paused  bool // the reader waits until unacked is under flowLow
	closed  bool
}

// attach makes a viewer the tab's window (the one before, if any, lets go): it gets the
// tab's state, then its scrollback, then what comes.
func (s *Session) attach() *viewer {
	s.mu.Lock()
	defer s.mu.Unlock()
	v := &viewer{ready: sync.NewCond(&s.mu)}
	if s.gone {
		v.closed = true
		return v
	}
	if s.view != nil {
		s.view.close()
	}
	s.view = v
	v.push(frameState, stateJSON(s.info))
	if back := s.scroll.bytes(); len(back) > 0 {
		v.push(frameOutput, back)
		clear(back)
	}
	s.flow.Broadcast()
	return v
}

// detach lets go of v (the window went away).
func (s *Session) detach(v *viewer) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.view == v {
		s.view = nil
		s.flow.Broadcast()
	}
	v.close()
}

// acked: v drew n more bytes.
func (s *Session) acked(v *viewer, n int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v.unacked = max(v.unacked-n, 0)
	if v.paused && v.unacked < flowLow {
		v.paused = false
		s.flow.Broadcast()
	}
}

// notice tells v something hopsesh refused or could not do.
func (s *Session) notice(v *viewer, msg string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v.push(frameError, []byte(msg))
}

// push queues a frame (the session's lock held). Output is merged into the last output
// frame while that is under maxFrame, and counts against flow control.
func (v *viewer) push(kind byte, p []byte) {
	if v.closed {
		return
	}
	if kind != frameOutput {
		v.items = append(v.items, append([]byte{kind}, p...))
		v.ready.Broadcast()
		return
	}
	v.unacked += len(p)
	if v.unacked >= flowHigh {
		v.paused = true
	}
	for len(p) > 0 {
		if n := len(v.items); n > 0 && v.items[n-1][0] == frameOutput && len(v.items[n-1]) <= maxFrame {
			k := min(maxFrame+1-len(v.items[n-1]), len(p))
			v.items[n-1] = append(v.items[n-1], p[:k]...)
			p = p[k:]
			continue
		}
		k := min(maxFrame, len(p))
		f := make([]byte, 1, 1+k)
		f[0] = frameOutput
		v.items = append(v.items, append(f, p[:k]...))
		p = p[k:]
	}
	v.ready.Broadcast()
}

// next waits for v's next frame; false once v is closed.
func (v *viewer) next() ([]byte, bool) {
	v.ready.L.Lock()
	defer v.ready.L.Unlock()
	for len(v.items) == 0 && !v.closed {
		v.ready.Wait()
	}
	if v.closed {
		return nil, false
	}
	f := v.items[0]
	v.items[0] = nil
	v.items = v.items[1:]
	return f, true
}

// close ends v (the session's lock held).
func (v *viewer) close() {
	v.closed = true
	v.items = nil
	v.ready.Broadcast()
}
