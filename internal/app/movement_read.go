package app

import (
	"context"
	"slices"
	"sync"
	"time"

	"github.com/roeehrl/hopsesh/sdk/agent"
	"github.com/roeehrl/hopsesh/sdk/ir"
)

// GUI refreshes should not parse a large unchanged transcript every few seconds.
// This bounded cache is only a read optimization, never an authority for a move.
// Plans and hook receipts independently revalidate their own inputs.
type movementReadCache struct {
	mu      sync.Mutex
	entries map[string]movementRead
}
type movementRead struct {
	size     int64
	modified time.Time
	stamp    agent.SessionStamp // a session kept in a database: its stamp, not its file's
	segment  ir.Segment
}

func (a *App) readMovementSegment(ctx context.Context, r agent.Reader, h agent.Host, in agent.Install, e Entry, endpoint string) (ir.Segment, error) {
	contentReads.Add(1)
	transcriptReads.Add(1)
	ctx = a.limited(ctx)
	if a.movementReads == nil {
		return r.Read(ctx, h, in, e.Session, ir.Cursor{})
	}
	if mod, ok := r.(agent.Module); ok && agent.IsVirtual(mod) {
		return a.readVirtualSegment(ctx, mod, r, h, in, e, endpoint)
	}
	info, err := h.FS().Stat(e.Session.Path)
	if err != nil {
		return ir.Segment{}, err
	}
	key := endpoint + "\x00" + e.Machine + "\x00" + e.Session.Key.String() + "\x00" + in.BindingID() + "\x00" + in.Version + "\x00" + e.Session.Path
	c := a.movementReads
	c.mu.Lock()
	cached, ok := c.entries[key]
	c.mu.Unlock()
	if ok && cached.size == info.Size() && cached.modified.Equal(info.ModTime()) {
		seg := cached.segment
		seg.Nodes = slices.Clone(seg.Nodes)
		return seg, nil
	}
	seg, err := r.Read(ctx, h, in, e.Session, ir.Cursor{})
	if err != nil {
		return seg, err
	}
	after, err := h.FS().Stat(e.Session.Path)
	if err == nil && after.Size() == info.Size() && after.ModTime().Equal(info.ModTime()) && info.Size() <= 4<<20 {
		copy := seg
		copy.Nodes = slices.Clone(seg.Nodes)
		c.mu.Lock()
		// At most eight small native files; clear rather than retaining unbounded
		// transcripts and account-specific content for the lifetime of the GUI.
		if len(c.entries) >= 8 {
			c.entries = map[string]movementRead{}
		}
		c.entries[key] = movementRead{size: info.Size(), modified: info.ModTime(), segment: copy}
		c.mu.Unlock()
	}
	return seg, nil
}

// readVirtualSegment is readMovementSegment for a session its agent keeps in a database:
// cached by the session's stamp, since its anchor file (maybe missing) does not change
// when the session is used.
func (a *App) readVirtualSegment(ctx context.Context, mod agent.Module, r agent.Reader, h agent.Host, in agent.Install, e Entry, endpoint string) (ir.Segment, error) {
	before, ok := sessionStamp(ctx, mod, h, in, e.Session, e.Cached)
	if !ok {
		return r.Read(ctx, h, in, e.Session, ir.Cursor{})
	}
	key := endpoint + "\x00" + e.Machine + "\x00" + e.Session.Key.String() + "\x00" + in.BindingID() + "\x00" + in.Version + "\x00stamp"
	c := a.movementReads
	c.mu.Lock()
	cached, hit := c.entries[key]
	c.mu.Unlock()
	if hit && cached.stamp == before {
		seg := cached.segment
		seg.Nodes = slices.Clone(seg.Nodes)
		return seg, nil
	}
	seg, err := r.Read(ctx, h, in, e.Session, ir.Cursor{})
	if err != nil {
		return seg, err
	}
	var size int
	for _, n := range seg.Nodes {
		size += len(n.Text)
	}
	if after, ok := sessionStamp(ctx, mod, h, in, e.Session, e.Cached); ok && after == before && size <= 4<<20 {
		copy := seg
		copy.Nodes = slices.Clone(seg.Nodes)
		c.mu.Lock()
		if len(c.entries) >= 8 {
			c.entries = map[string]movementRead{}
		}
		c.entries[key] = movementRead{stamp: before, segment: copy}
		c.mu.Unlock()
	}
	return seg, nil
}
