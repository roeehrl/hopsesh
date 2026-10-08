package pty

import (
	"context"
	"testing"
	"time"
)

func handoverSession() *Session {
	m := NewManager(Options{Scrollback: 1024})
	return newSession(m, "one", Spec{Argv: []string{"test"}}, 80, 24)
}

func TestCheckpointReplacesTruncatedReplayAndReleasesOutput(t *testing.T) {
	s := handoverSession()
	s.printed([]byte("old tail"))
	old := s.attach()
	s.prepare(old)
	done := make(chan struct{})
	go func() { s.printed([]byte("after checkpoint")); close(done) }()
	select {
	case <-done:
		t.Fatal("output crossed checkpoint barrier")
	case <-time.After(10 * time.Millisecond):
	}
	s.acceptCheckpoint(old, []byte("\x1bc\x1b[?1049hFULL ALTERNATE SCREEN"))
	next := s.attach()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("output not released")
	}
	var output string
	s.mu.Lock()
	for _, f := range next.items {
		if f[0] == frameOutput {
			output += string(f[1:])
		}
	}
	closed := old.closed
	s.mu.Unlock()
	if !closed || output != "\x1bc\x1b[?1049hFULL ALTERNATE SCREENafter checkpoint" {
		t.Fatalf("state lost or duplicated: %q", output)
	}
}

func TestInvalidCheckpointKeepsOriginalViewer(t *testing.T) {
	s := handoverSession()
	v := s.attach()
	s.prepare(v)
	s.acceptCheckpoint(v, nil)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.handingOff || v.closed || s.view != v || len(s.checkpoint) != 0 {
		t.Fatal("failed handover abandoned original view")
	}
}

func TestStaleAcknowledgementCannotReleaseNewOwnerBackpressure(t *testing.T) {
	s := handoverSession()
	old := s.attach()
	fresh := s.attach()
	s.mu.Lock()
	fresh.paused = true
	fresh.unacked = flowHigh
	s.mu.Unlock()
	s.acked(old, flowHigh)
	s.mu.Lock()
	defer s.mu.Unlock()
	if !fresh.paused || fresh.unacked != flowHigh {
		t.Fatal("old owner changed new owner flow control")
	}
}

// A malicious/late transport can deliver one final frame even after Close.
// Replacement ownership must be checked by the session, not only transport.
type lateConn struct {
	input  chan []byte
	output chan []byte
	ctx    context.Context
}

func (c *lateConn) Receive() ([]byte, error) {
	select {
	case b := <-c.input:
		return b, nil
	case <-c.ctx.Done():
		return nil, c.ctx.Err()
	}
}
func (c *lateConn) Send(b []byte) error {
	select {
	case c.output <- b:
		return nil
	case <-c.ctx.Done():
		return c.ctx.Err()
	}
}
func (c *lateConn) Close() error             { return nil }
func (c *lateConn) Context() context.Context { return c.ctx }
func TestLateFramesCannotControlReplacementViewer(t *testing.T) {
	for _, frame := range [][]byte{{'i', 'x'}, {'r', 0, 40, 0, 12}, {'c'}, {'k', 0, 8, 0, 0}} {
		t.Run(string(frame[:1]), func(t *testing.T) {
			s := handoverSession()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			c := &lateConn{make(chan []byte, 2), make(chan []byte, 16), ctx}
			done := make(chan error, 1)
			c.input <- []byte("aone")
			go func() { done <- Serve(c, func(string) (*Session, error) { return s, nil }, nil) }()
			select {
			case <-c.output:
			case <-time.After(time.Second):
				t.Fatal("old viewer did not attach")
			}
			fresh := s.attach()
			c.input <- frame
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("stale command was accepted")
			}
			s.mu.Lock()
			if s.view != fresh || s.info.Cols != 80 || s.info.Rows != 24 || s.gone || s.pending != 0 {
				t.Error("stale view changed session")
			}
			s.mu.Unlock()
		})
	}
}
