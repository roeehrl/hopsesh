package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/roeehrl/hopsesh/internal/config"
	"github.com/roeehrl/hopsesh/internal/core/host"
	"github.com/roeehrl/hopsesh/internal/core/observe"
	"github.com/roeehrl/hopsesh/internal/core/transport"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

// RemoteObservation retains the last evidence independently of a new attempt.
// Binding prevents an edited or revoked machine from reappearing from cache.
type RemoteObservation struct {
	Binding  config.Host      `json:"binding"`
	Phase    string           `json:"phase"`
	Started  time.Time        `json:"started,omitempty"`
	Finished time.Time        `json:"finished,omitempty"`
	Status   string           `json:"status"`
	Error    string           `json:"error,omitempty"`
	Hint     string           `json:"hint,omitempty"`
	Facts    host.Facts       `json:"facts"`
	Snapshot observe.Snapshot `json:"snapshot"`
}

type remoteRefresh struct {
	name  string
	reply chan remoteRefreshReply
}
type remoteRefreshReply struct {
	state RemoteObservation
	err   error
}
type remoteResult struct {
	binding config.Host
	state   RemoteObservation
}
type remoteSlot struct {
	binding         config.Host
	due             time.Time
	failures        int
	waiters         []chan remoteRefreshReply
	relayGeneration uint64
}

// One owner, one deadline timer, one active remote collection. A failed
// authentication needs an explicit retry; network failures back off without
// prompting for passwords, accepting host keys or starting vendor programs.
type remoteObserver struct {
	mu         sync.Mutex
	states     map[string]RemoteObservation
	requests   chan remoteRefresh
	relayReady chan struct{}
	collect    func(context.Context, config.Host) RemoteObservation
	interval   time.Duration
	timeout    time.Duration
}

func newRemoteObserver(a *App) *remoteObserver {
	return &remoteObserver{states: map[string]RemoteObservation{}, requests: make(chan remoteRefresh), relayReady: make(chan struct{}, 1), collect: a.observeRemote, interval: 5 * time.Minute, timeout: 30 * time.Second}
}

// A local connection transition can unblock failed remote scans. This is a
// coalesced owner signal, not a second polling loop or new user permission.
func (r *remoteObserver) relayConnected() {
	select {
	case r.relayReady <- struct{}{}:
	default:
	}
}

func remoteNeedsExplicitRetry(status string) bool {
	return status == StatusAuth || status == StatusHostKey || status == StatusKeyChanged || status == StatusTSCheck
}
func (r *remoteObserver) latest() []RemoteObservation {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]RemoteObservation, 0, len(r.states))
	for _, s := range r.states {
		s.Snapshot.Data = append(json.RawMessage(nil), s.Snapshot.Data...)
		out = append(out, s)
	}
	slices.SortFunc(out, func(a, b RemoteObservation) int {
		if a.Binding.Name < b.Binding.Name {
			return -1
		}
		if a.Binding.Name > b.Binding.Name {
			return 1
		}
		return 0
	})
	return out
}
func (r *remoteObserver) publish(s RemoteObservation, notify func()) {
	r.mu.Lock()
	r.states[s.Binding.Name] = s
	r.mu.Unlock()
	notify()
}
func (r *remoteObserver) refresh(ctx context.Context, name string) (RemoteObservation, error) {
	req := remoteRefresh{name: name, reply: make(chan remoteRefreshReply, 1)}
	select {
	case r.requests <- req:
	case <-ctx.Done():
		return RemoteObservation{}, ctx.Err()
	}
	select {
	case out := <-req.reply:
		return out.state, out.err
	case <-ctx.Done():
		return RemoteObservation{}, ctx.Err()
	}
}

func (r *remoteObserver) run(ctx context.Context, engine *observe.Engine) {
	updates, unsubscribe := engine.Subscribe()
	defer unsubscribe()
	timer := time.NewTimer(time.Hour)
	timer.Stop()
	defer timer.Stop()
	var due <-chan time.Time
	slots := map[string]*remoteSlot{}
	results := make(chan remoteResult, 1)
	var active *remoteSlot
	var cancel context.CancelFunc
	var relayGeneration uint64
	var tasks sync.WaitGroup
	paused := engine.Latest().Paused
	defer tasks.Wait()
	defer func() {
		if cancel != nil {
			cancel()
		}
	}()
	reply := func(s *remoteSlot, state RemoteObservation, err error) {
		for _, ch := range s.waiters {
			ch <- remoteRefreshReply{state, err}
		}
		s.waiters = nil
	}
	configure := func() {
		cfg, err := config.Load()
		if err != nil {
			// Invalid settings cannot keep stale authorization alive.
			cfg.Hosts = nil
		}
		wanted := map[string]config.Host{}
		for _, h := range cfg.Hosts {
			if h.Allowed {
				wanted[h.Name] = h
			}
		}
		for name, s := range slots {
			if h, ok := wanted[name]; !ok || h != s.binding {
				if active == s && cancel != nil {
					cancel()
				}
				reply(s, RemoteObservation{}, errors.New("machine approval or connection changed during scan"))
				delete(slots, name)
				r.mu.Lock()
				delete(r.states, name)
				r.mu.Unlock()
				engine.Notify()
			}
		}
		for name, h := range wanted {
			if slots[name] != nil {
				continue
			}
			slots[name] = &remoteSlot{binding: h, due: time.Now()}
			r.publish(RemoteObservation{Binding: h, Phase: "queued"}, engine.Notify)
		}
	}
	arm := func() {
		timer.Stop()
		due = nil
		if active != nil || engine.Latest().Paused {
			return
		}
		var earliest time.Time
		for _, s := range slots {
			if !s.due.IsZero() && (earliest.IsZero() || s.due.Before(earliest)) {
				earliest = s.due
			}
		}
		if !earliest.IsZero() {
			timer.Reset(max(time.Duration(0), time.Until(earliest)))
			due = timer.C
		}
	}
	configure()
	arm()
	for {
		select {
		case <-ctx.Done():
			return
		case _, ok := <-updates:
			if !ok {
				return
			}
			configure()
			nowPaused := engine.Latest().Paused
			if paused && !nowPaused {
				for _, s := range slots {
					s.due = time.Now()
				}
			}
			paused = nowPaused
			if nowPaused && cancel != nil {
				cancel()
			}
		case req := <-r.requests:
			configure()
			s := slots[req.name]
			if s == nil {
				req.reply <- remoteRefreshReply{err: fmt.Errorf("machine %q is not enabled", req.name)}
				break
			}
			if engine.Latest().Paused {
				req.reply <- remoteRefreshReply{err: errors.New("remote observation is paused")}
				break
			}
			if len(s.waiters) >= 32 {
				req.reply <- remoteRefreshReply{err: errors.New("remote scan request limit reached")}
				break
			}
			s.waiters = append(s.waiters, req.reply)
			if active != s {
				s.due = time.Now()
			}
		case <-r.relayReady:
			relayGeneration++
			configure()
			r.mu.Lock()
			for name, s := range slots {
				state := r.states[name]
				if s != active && s.binding.RelayID != "" && state.Error != "" && !remoteNeedsExplicitRetry(state.Status) {
					s.due = time.Now()
				}
			}
			r.mu.Unlock()
		case <-due:
			var next *remoteSlot
			for _, s := range slots {
				if !s.due.IsZero() && !s.due.After(time.Now()) && (next == nil || s.due.Before(next.due)) {
					next = s
				}
			}
			if next == nil {
				break
			}
			active = next
			next.relayGeneration = relayGeneration
			next.due = time.Time{}
			r.mu.Lock()
			state := r.states[next.binding.Name]
			r.mu.Unlock()
			state.Phase, state.Started = "scanning", time.Now().UTC()
			r.publish(state, engine.Notify)
			child, done := context.WithTimeout(ctx, r.timeout)
			cancel = done
			tasks.Go(func() {
				state := r.collect(child, next.binding)
				done()
				select {
				case results <- remoteResult{next.binding, state}:
				case <-ctx.Done():
				}
			})
		case result := <-results:
			s := active
			active = nil
			cancel = nil
			if slots[result.binding.Name] != s || s.binding != result.binding {
				break
			}
			state := result.state
			state.Binding = result.binding
			state.Phase = "done"
			state.Finished = time.Now().UTC()
			r.mu.Lock()
			previous := r.states[result.binding.Name]
			r.mu.Unlock()
			state.Started = previous.Started
			state.Snapshot.Sequence = previous.Snapshot.Sequence + 1
			state.Snapshot.AttemptedAt = state.Started
			if state.Error != "" {
				if len(state.Snapshot.Data) == 0 {
					state.Facts = previous.Facts
					state.Snapshot = previous.Snapshot
				}
				state.Snapshot.Sequence = previous.Snapshot.Sequence + 1
				state.Snapshot.AttemptedAt, state.Snapshot.Error = state.Started, state.Error
				s.failures++
				if !remoteNeedsExplicitRetry(state.Status) {
					if s.binding.RelayID != "" && s.relayGeneration < relayGeneration {
						// The connection became ready while this failed attempt was
						// in flight; do not lose its transition behind the result.
						s.due = time.Now()
					} else {
						s.due = time.Now().Add(min(time.Hour, time.Minute*time.Duration(1<<min(s.failures-1, 6))))
					}
				}
			} else {
				s.failures = 0
				if state.Snapshot.ObservedAt.IsZero() {
					state.Snapshot.Epoch = engine.Latest().Epoch
					state.Snapshot.ObservedAt = state.Finished
					state.Snapshot.ExpiresAt = state.Finished.Add(r.interval + time.Minute)
				}
				// Relayed freshness belongs to the source. Receipt on this
				// owner cannot extend its validity; refresh before it expires.
				remaining := time.Until(state.Snapshot.ExpiresAt)
				s.due = state.Finished.Add(min(r.interval, max(time.Second, remaining*2/3)))
			}
			r.publish(state, engine.Notify)
			reply(s, state, nil)
		}
		arm()
	}
}

func (a *App) observeRemote(ctx context.Context, h config.Host) RemoteObservation {
	state := RemoteObservation{Binding: h}
	cfg, err := config.Load()
	if err != nil {
		state.Status, state.Error = StatusError, "Machine settings unavailable"
		return state
	}
	current := cfg.FindHost(h.Name)
	if current == nil || !current.Allowed || *current != h {
		state.Status, state.Error = StatusError, "Machine approval changed"
		return state
	}
	source := *a
	source.Cfg = cfg
	source.Passwords = nil
	if h.UsesPassword() {
		state.Status, state.Error, state.Hint = StatusAuth, "A password scan needs your explicit action", "Use Scan to sign in; background scans never ask for a password"
		return state
	}
	if h.RelayID != "" {
		m, entries, snapshot := source.scanRelaySnapshot(ctx, h, false)
		state.Status, state.Error, state.Hint = m.Status, m.Error, m.Hint
		data, e := json.Marshal(Observation{Version: m.Hopsesh, Receive: m.Receive != nil && *m.Receive, OS: m.OS, Machine: h.Name, InventoryComplete: m.Status == StatusOK, Agents: m.Agents, Entries: entries})
		if e != nil {
			state.Error = e.Error()
			return state
		}
		state.Snapshot = snapshot
		state.Snapshot.Data = data
		return state
	}
	conn, err := transport.NewConn(h.Destination, a.StateDir, a.Audit)
	if err != nil {
		state.Status, state.Error, state.Hint = classify(err, h)
		return state
	}
	defer conn.Close()
	if h.TailscaleName != "" && h.TailscaleName != h.Destination {
		conn.Fallbacks = []string{h.TailscaleName}
	}
	facts, err := host.ObserveRemote(ctx, conn, a.Specs())
	if err != nil {
		state.Status, state.Error, state.Hint = classify(err, h)
		return state
	}
	m := &host.Machine{Name: h.Name, Conn: conn, Facts: facts, Log: a.Log}
	defer m.Close()
	obs, err := source.observeMachine(ctx, m)
	if err != nil {
		state.Status, state.Error, state.Hint = classify(err, h)
		return state
	}
	obs.WatchRoots, obs.Processes = nil, nil
	state.Facts = m.Facts
	state.Status = StatusOK
	state.Snapshot.Data, err = json.Marshal(obs)
	if err != nil {
		state.Status, state.Error = StatusError, err.Error()
	}
	return state
}

func (a *App) RemoteInventory(ctx context.Context, state RemoteObservation) *Inventory {
	m := &Machine{Kind: agent.AtMachine, Name: state.Binding.Name, Destination: state.Binding.Destination, Status: state.Status, Error: state.Error, Hint: state.Hint}
	if state.Binding.RelayID != "" {
		m.Destination = "relay"
	}
	inv := &Inventory{Machines: []*Machine{m}}
	h := a.Cfg.FindHost(state.Binding.Name)
	if h == nil || !h.Allowed || *h != state.Binding {
		m.Status, m.Error = StatusError, "Machine approval changed"
		return inv
	}
	var obs Observation
	if err := json.Unmarshal(state.Snapshot.Data, &obs); err != nil {
		return inv
	}
	m.OS, m.Agents, m.Hopsesh = obs.OS, obs.Agents, obs.Version
	if h.RelayID != "" {
		receive := obs.Receive && state.Snapshot.Fresh(time.Now())
		m.Receive = &receive
	}
	inv.Entries = obs.Entries
	if !state.Snapshot.Fresh(time.Now()) {
		for i := range inv.Entries {
			inv.Entries[i].Live = agent.LiveInfo{State: agent.Unknown}
			inv.Entries[i].ObservedAt = time.Time{}
		}
		return inv
	}
	if h.RelayID == "" && !h.UsesPassword() {
		conn, err := transport.NewConn(h.Destination, a.StateDir, a.Audit)
		if err == nil {
			if h.TailscaleName != "" && h.TailscaleName != h.Destination {
				conn.Fallbacks = []string{h.TailscaleName}
			}
			m.host = &host.Machine{Name: h.Name, Conn: conn, Facts: state.Facts, Log: a.Log}
		}
	}
	return inv
}
