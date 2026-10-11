package app

import (
	"context"
	"time"

	"github.com/roeehrl/hopsesh/internal/core/host"
	"github.com/roeehrl/hopsesh/internal/core/observe"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

// WatchOptions tune WatchSessions for a front end.
type WatchOptions struct {
	// Active reports whether anyone is looking (nil: always). While nobody is, the
	// watcher holds no watches at all: every file event costs the process tens of
	// wakeups, and agents write their sessions many times a second. The front end's own
	// background refresh covers that time; looking again re-arms and refreshes once.
	Active func() bool
	// Current returns the app whose configuration names the roots (nil: this one).
	Current func() *App
}

// activeCheck is how often an inactive watcher asks whether anyone is looking again.
const activeCheck = 2 * time.Second

// WatchSessions uses the same bounded directory watcher as the 0.5 runtime.
// Reconciliation still covers network filesystems, overflow, and missed events.
func (a *App) WatchSessions(ctx context.Context, changed func(), opts WatchOptions) {
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
	roots := func() []string {
		core := a
		if opts.Current != nil {
			core = opts.Current()
		}
		return core.sessionWatchRoots(ctx)
	}

	active := func() bool { return opts.Active == nil || opts.Active() }
	armed := active()
	rooted := time.Time{}
	if armed {
		_ = watch.SetRoots(roots())
		rooted = time.Now()
	}
	tick := time.NewTicker(time.Minute)
	defer tick.Stop()
	var check <-chan time.Time
	if opts.Active != nil {
		t := time.NewTicker(activeCheck)
		defer t.Stop()
		check = t.C
	}
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
		case <-check:
			switch now := active(); {
			case now && !armed:
				armed = true
				_ = watch.SetRoots(roots())
				rooted = time.Now()
				changed()
			case !now && armed:
				armed = false
				_ = watch.SetRoots(nil)
				if timer != nil {
					timer.Stop()
					timer, due = nil, nil
				}
			}
		case <-wake:
			if !armed {
				continue
			}
			if timer == nil {
				timer = time.NewTimer(300 * time.Millisecond)
				due = timer.C
			}
		case <-watch.Problems():
			// Browsing summaries are keyed by each file's size and mtime, so a missed
			// event needs a rescan, not a cleared cache.
			if timer == nil {
				timer = time.NewTimer(300 * time.Millisecond)
				due = timer.C
			}
		case <-due:
			timer = nil
			due = nil
			// Rereading the roots probes the agents' installs: not on every write.
			if time.Since(rooted) >= 30*time.Second {
				_ = watch.SetRoots(roots())
				rooted = time.Now()
			}
			changed()
		case <-tick.C:
			if !armed {
				continue // the front end's background refresh covers this
			}
			_ = watch.SetRoots(roots())
			rooted = time.Now()
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
