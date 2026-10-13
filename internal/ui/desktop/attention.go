package desktop

import (
	"context"
	"sync/atomic"
)

type attentionUpdate struct {
	needs, tabs int
	enabled     bool
}

// attentionPublisher coalesces cosmetic native updates without making callers
// wait for the UI thread. Shutdown may join those callers from that same thread.
type attentionPublisher struct {
	ctx      context.Context
	dispatch func(func())
	apply    func(attentionUpdate)
	latest   atomic.Pointer[attentionUpdate]
	queued   atomic.Bool
}

func newAttentionPublisher(ctx context.Context, dispatch func(func()), apply func(attentionUpdate)) *attentionPublisher {
	return &attentionPublisher{ctx: ctx, dispatch: dispatch, apply: apply}
}

func (p *attentionPublisher) update(u attentionUpdate) {
	if p.ctx.Err() != nil {
		return
	}
	if old := p.latest.Swap(&u); old != nil && *old == u {
		return
	}
	if p.queued.CompareAndSwap(false, true) {
		p.dispatch(func() {
			// Reset before loading: a concurrent publication queues another callback
			// if this one has already selected an older value.
			p.queued.Store(false)
			if p.ctx.Err() == nil {
				p.apply(*p.latest.Load())
			}
		})
	}
}
