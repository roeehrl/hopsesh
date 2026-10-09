package app

import (
	"context"
	"time"

	"github.com/roeehrl/hopsesh/internal/core/host"
	"github.com/roeehrl/hopsesh/internal/core/observe"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

// WatchSessions uses the same bounded directory watcher as the 0.5 runtime.
// Reconciliation still covers network filesystems, overflow, and missed events.
func (a *App) WatchSessions(ctx context.Context, changed func(), current ...func() *App) {
	wake := make(chan struct{}, 1)
	watch, err := observe.NewFiles(4096, func() {
		select {
		case wake <- struct{}{}:
		default:
		}
	})
	if err != nil {
		// Resource limits must not disable reconciliation in a desktop-managed
		// frontend, where the browser does not schedule its own local reads.
		tick := time.NewTicker(time.Minute)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				changed()
			}
		}
	}
	defer watch.Close()
	watch.SetChanged(func(path string) {
		if a.Catalog != nil {
			a.Catalog.InvalidatePath(path)
		}
	})
	roots := func() []string {
		core := a
		if len(current) > 0 {
			core = current[0]()
		}
		return core.sessionWatchRoots(ctx)
	}

	_ = watch.SetRoots(roots())
	tick := time.NewTicker(time.Minute)
	defer tick.Stop()
	var timer *time.Timer
	var due <-chan time.Time
	defer func() {
		if timer != nil {
			timer.Stop()
		}
	}()
	for {
		select {
		case <-ctx.Done():
			return
		case <-wake:
			if timer == nil {
				timer = time.NewTimer(300 * time.Millisecond)
				due = timer.C
			}
		case <-watch.Problems():
			if a.Catalog != nil {
				a.Catalog.Invalidate()
			}
			if timer == nil {
				timer = time.NewTimer(300 * time.Millisecond)
				due = timer.C
			}
		case <-due:
			timer = nil
			due = nil
			_ = watch.SetRoots(roots())
			changed()
		case <-tick.C:
			_ = watch.SetRoots(roots())
			changed()
		}
	}
}

func (a *App) sessionWatchRoots(ctx context.Context) []string {
	facts := host.ProbeLocalFast(ctx, a.Specs())
	hm := &host.Machine{Local: true, Name: LocalName(), Facts: facts}
	var roots []string
	add := func(id agent.ID, in agent.Install) {
		if mod, ok := a.Module(id); ok {
			if provider, ok := mod.(agent.SessionWatchProvider); ok {
				roots = append(roots, provider.SessionWatchPaths(in, hm.Path())...)
				return
			}
		}
		for _, root := range in.Roots {
			roots = append(roots, root)
		}
	}
	for _, spec := range a.Specs() {
		h, err := hm.For(ctx, spec, agent.Install{}, nil)
		if err == nil {
			add(spec.ID, agent.DefaultInstall(spec, h))
		}
	}
	endpoint, _ := hm.ReadIdentity(ctx)
	profiles, _ := a.Accounts()
	for _, p := range profiles {
		if p.Machine == LocalName() || endpoint != "" && p.Endpoint == endpoint {
			add(p.Agent, agent.Install{Agent: p.Agent, Profile: &p, Roots: map[string]string{"home": p.Root}})
		}
	}
	return roots
}
