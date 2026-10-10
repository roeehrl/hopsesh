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
	segment  ir.Segment
}

func (a *App) readMovementSegment(ctx context.Context, r agent.Reader, h agent.Host, in agent.Install, e Entry, endpoint string) (ir.Segment, error) {
	ctx = a.limited(ctx)
	if a.movementReads == nil {
		return r.Read(ctx, h, in, e.Session, ir.Cursor{})
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
		c.entries[key] = movementRead{info.Size(), info.ModTime(), copy}
		c.mu.Unlock()
	}
	return seg, nil
}
