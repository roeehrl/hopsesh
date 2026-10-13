package gui

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/roeehrl/hopsesh/agents/claude"
	"github.com/roeehrl/hopsesh/internal/core/move"
	"github.com/roeehrl/hopsesh/internal/core/registry"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

// Pause the first cached selection's native listing, allowing a newer review
// to publish and borrow its inventory before the older read finishes.
type cachedSelectionGate struct {
	*claude.Module
	armed   atomic.Bool
	calls   atomic.Int32
	entered chan struct{}
	release chan struct{}
}

func (m *cachedSelectionGate) Spec() agent.Spec {
	s := m.Module.Spec()
	s.Accounts = nil
	s.Binaries = []agent.Binary{{Name: "claude"}}
	return s
}

func (m *cachedSelectionGate) List(ctx context.Context, h agent.Host, in agent.Install) (agent.Listing, error) {
	if m.armed.Load() && m.calls.Add(1) == 1 {
		close(m.entered)
		select {
		case <-m.release:
		case <-ctx.Done():
			return agent.Listing{}, ctx.Err()
		}
	}
	return m.Module.List(ctx, h, in)
}

func TestSupersededSelectionPreservesNewReviewAndActiveInventory(t *testing.T) {
	for _, active := range []bool{false, true} {
		name := "review"
		if active {
			name = "active_apply"
		}
		t.Run(name, func(t *testing.T) {
			home(t)
			mod := &cachedSelectionGate{Module: claude.New(), entered: make(chan struct{}), release: make(chan struct{})}
			reg, err := registry.New(mod)
			if err != nil {
				t.Fatal(err)
			}
			a := NewApp(reg, WithoutSessionWatching())
			t.Cleanup(a.Shutdown)
			if _, err := a.Scan(); err != nil {
				t.Fatal(err)
			}
			inv, e := a.inv, a.inv.Entries[0]
			e.Cached = true
			oldCore, oldGeneration, err := a.reviewSnapshot()
			if err != nil {
				t.Fatal(err)
			}
			mod.armed.Store(true)
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			var once sync.Once
			unblock := func() { once.Do(func() { close(mod.release) }) }
			defer unblock()
			result := make(chan error, 1)
			go func() {
				_, _, err := a.selectionInventory(ctx, oldCore, inv, e, oldGeneration)
				result <- err
			}()
			select {
			case <-mod.entered:
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			newCore, generation, err := a.reviewSnapshot()
			if err != nil {
				t.Fatal(err)
			}
			fresh, _, err := a.selectionInventory(ctx, newCore, inv, e, generation)
			if err != nil {
				t.Fatal(err)
			}
			side := move.Side{Machine: fresh.Local().Host(), Module: mod}
			p := &move.Plan{Kind: move.KindContinue}
			if err := a.publishReview(generation, p, move.Input{Source: side, Target: side}); err != nil {
				t.Fatal(err)
			}
			if active {
				_, _, _, release, err := a.beginPlanAction("")
				if err != nil {
					t.Fatal(err)
				}
				defer release()
			}
			unblock()
			if err := <-result; err != errReviewChanged && err != errActionRunning {
				t.Fatalf("old selection was not rejected: %v", err)
			}
			a.mu.Lock()
			preserved := a.inv == fresh && a.plan == p && a.input.Source.Machine == side.Machine && a.actionRunning == active
			a.mu.Unlock()
			if !preserved {
				t.Fatal("old read replaced or closed a newer review/action inventory")
			}
			// The superseded core/generation must also fail before starting another
			// native read, even with an otherwise current inventory pointer.
			calls := mod.calls.Load()
			if _, _, err := a.selectionInventory(ctx, oldCore, fresh, e, oldGeneration); err == nil || mod.calls.Load() != calls {
				t.Fatal("stale generation started a new selection read")
			}
		})
	}
}
