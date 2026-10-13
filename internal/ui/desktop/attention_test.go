package desktop

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestAttentionDoesNotWaitForControllerOrUI(t *testing.T) {
	var callbacks []func()
	var applied []attentionUpdate
	c := &Controller{ctx: context.Background()}
	c.published.Store(&State{})
	c.attention.Store(newAttentionPublisher(c.ctx, func(f func()) { callbacks = append(callbacks, f) }, func(u attentionUpdate) { applied = append(applied, u) }))
	c.mu.Lock() // Another controller operation can be waiting for native UI work.
	done := make(chan struct{})
	go func() {
		for i := range 100 {
			c.SetAttention(i, i+1)
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		c.mu.Unlock()
		t.Fatal("refresh waited for a controller/UI lock before shutdown could join it")
	}
	c.mu.Unlock()
	if len(callbacks) != 1 || len(applied) != 0 {
		t.Fatalf("UI queue was not bounded or ran synchronously: queued=%d applied=%v", len(callbacks), applied)
	}
	callbacks[0]() // The UI resumes only after the refresh has already returned.
	if len(applied) != 1 || applied[0].needs != 99 || applied[0].tabs != 100 {
		t.Fatalf("the latest attention counts were lost: %v", applied)
	}
}

func TestAttentionPublicationDuringApplyAndShutdown(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var callbacks []func()
	var applied []attentionUpdate
	var p *attentionPublisher
	p = newAttentionPublisher(ctx, func(f func()) { callbacks = append(callbacks, f) }, func(u attentionUpdate) {
		applied = append(applied, u)
		if u.needs == 1 {
			p.update(attentionUpdate{needs: 2, enabled: true})
		}
	})
	p.update(attentionUpdate{needs: 1})
	callbacks[0]()
	if len(callbacks) != 2 {
		t.Fatal("a publication while native work ran was lost")
	}
	callbacks[1]()
	if len(applied) != 2 || applied[1].needs != 2 || !applied[1].enabled {
		t.Fatalf("latest update/preference was not applied: %v", applied)
	}
	p.update(attentionUpdate{needs: 3})
	cancel()
	callbacks[2]()
	p.update(attentionUpdate{needs: 4})
	if len(applied) != 2 || len(callbacks) != 3 {
		t.Fatal("shutdown allowed a stale native update or queued more UI work")
	}
}

func TestAttendedNeverQueriesNativeWindows(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := &Controller{ctx: ctx} // No native windows: polling them would panic/block.
	if c.Attended() {
		t.Fatal("an unfocused app was attended")
	}
	c.mainFocused.Store(true)
	if !c.Attended() {
		t.Fatal("focused main window was not attended")
	}
	c.mainFocused.Store(false)
	c.quickFocused.Store(true)
	if !c.Attended() {
		t.Fatal("focused quick access was not attended")
	}
	c.suspended.Store(true)
	if c.Attended() {
		t.Fatal("a locked/sleeping desktop was attended")
	}
	c.suspended.Store(false)
	cancel()
	if c.Attended() {
		t.Fatal("a stopped desktop was attended")
	}
}

func TestConcurrentAttentionCoalesces(t *testing.T) {
	var mu sync.Mutex
	var callbacks []func()
	p := newAttentionPublisher(context.Background(), func(f func()) { mu.Lock(); callbacks = append(callbacks, f); mu.Unlock() }, func(attentionUpdate) {})
	var wg sync.WaitGroup
	for i := range 50 {
		wg.Go(func() { p.update(attentionUpdate{needs: i}) })
	}
	wg.Wait()
	if len(callbacks) != 1 {
		t.Fatalf("concurrent refreshes queued %d callbacks", len(callbacks))
	}
	callbacks[0]()
}
