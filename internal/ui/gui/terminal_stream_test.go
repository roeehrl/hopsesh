package gui

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"
)

// A stalled receiver must get current settings/metadata when its pending refresh
// is delivered, even if the state changed after that refresh was queued.
func TestTerminalRefreshSnapshotsAtDelivery(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c := &stalledTerminalConn{ctx: ctx, cancel: cancel, sent: make(chan []byte), release: make(chan struct{})}
	terms := NewTerminals("test")
	var font atomic.Value
	font.Store("old")
	terms.Prefs = func() TermPrefs { return TermPrefs{Font: font.Load().(string)} }
	done := make(chan struct{})
	go func() { defer close(done); terms.ServeList(c) }()
	receive := func() []byte {
		t.Helper()
		select {
		case b := <-c.sent:
			return b
		case <-ctx.Done():
			t.Fatal("terminal stream timed out")
			return nil
		}
	}
	receive() // The first send now waits, so a refresh remains queued.
	terms.sendList()
	font.Store("current")
	close(c.release)
	var got struct {
		Prefs TermPrefs `json:"prefs"`
	}
	if err := json.Unmarshal(receive(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Prefs.Font != "current" {
		t.Fatalf("delivered stale snapshot: font=%q", got.Prefs.Font)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("terminal stream did not stop")
	}
}

type stalledTerminalConn struct {
	ctx     context.Context
	cancel  context.CancelFunc
	sent    chan []byte
	release chan struct{}
}

func (c *stalledTerminalConn) Context() context.Context { return c.ctx }
func (c *stalledTerminalConn) Close() error             { c.cancel(); return nil }
func (c *stalledTerminalConn) Receive() ([]byte, error) { <-c.ctx.Done(); return nil, c.ctx.Err() }
func (c *stalledTerminalConn) Send(b []byte) error {
	select {
	case c.sent <- b:
	case <-c.ctx.Done():
		return c.ctx.Err()
	}
	select {
	case <-c.release:
		return nil
	case <-c.ctx.Done():
		return c.ctx.Err()
	}
}
