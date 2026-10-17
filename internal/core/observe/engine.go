// Package observe schedules shared passive collection. Clients subscribe to one
// source; connecting another client does not create another polling loop.
package observe

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sync"
	"time"
)

type Options struct {
	Reconcile time.Duration
	Debounce  time.Duration
	MaxDelay  time.Duration
	Timeout   time.Duration
	FreshFor  time.Duration
	// Gap spaces change-driven collections while someone looks at a client (0: none).
	Gap time.Duration
	// Idle spaces change-driven and fallback collections while nobody does (0: no
	// difference). Agents write their sessions many times a second; an observer nobody
	// is looking at must not collect for each write. Snapshots then stay fresh for
	// Idle, so relay peers do not see this source's evidence expire in between.
	Idle time.Duration
}

func Defaults() Options {
	return Options{Reconcile: time.Minute, Debounce: 250 * time.Millisecond, MaxDelay: 2 * time.Second, Timeout: 30 * time.Second, FreshFor: 90 * time.Second}
}

func (o Options) valid() bool {
	return o.Reconcile > 0 && o.Debounce > 0 && o.MaxDelay >= o.Debounce && o.Timeout > 0 && o.FreshFor > 0 && o.Gap >= 0 && o.Idle >= 0
}

// spacing is the least time between collection starts that a change may cause, and
// reconcile the fallback interval, for the current attention.
func (o Options) spacing(attended bool) time.Duration {
	if attended || o.Idle == 0 {
		return o.Gap
	}
	return o.Idle
}
func (o Options) reconcile(attended bool) time.Duration {
	if attended {
		return o.Reconcile
	}
	return max(o.Reconcile, o.Idle)
}
func (o Options) freshFor(attended bool) time.Duration {
	return o.FreshFor + o.reconcile(attended) - o.Reconcile
}

type Snapshot struct {
	Epoch       string          `json:"epoch"`
	Sequence    uint64          `json:"sequence"`
	AttemptedAt time.Time       `json:"attemptedAt"`
	ObservedAt  time.Time       `json:"observedAt"`
	ExpiresAt   time.Time       `json:"expiresAt"`
	Paused      bool            `json:"paused"`
	Error       string          `json:"error,omitempty"`
	Data        json.RawMessage `json:"data,omitempty"`
}

// Fresh uses the source's successful observation time, never subscriber delivery.
func (s Snapshot) Fresh(now time.Time) bool {
	return !s.Paused && s.Error == "" && !s.ObservedAt.IsZero() && !now.Before(s.ObservedAt) && now.Before(s.ExpiresAt)
}
func (s Snapshot) clone() Snapshot { s.Data = append(json.RawMessage(nil), s.Data...); return s }

type Collector func(context.Context) (json.RawMessage, error)

// Metrics are per-incarnation counters. Reading them neither collects evidence
// nor introduces a timer; they contain no source paths or conversation data.
type Metrics struct {
	Subscribers         int    `json:"subscribers"`
	Attended            int    `json:"attended"`
	Notifications       uint64 `json:"notifications"`
	Coalesced           uint64 `json:"coalesced"`
	Collections         uint64 `json:"collections"`
	Failures            uint64 `json:"failures"`
	CollectionNanos     int64  `json:"collectionNanos"`
	LastCollectionNanos int64  `json:"lastCollectionNanos"`
	Paused              bool   `json:"paused"`
	Collecting          bool   `json:"collecting"`
	Pending             bool   `json:"pending"`
}
type Engine struct {
	mu        sync.Mutex
	opts      Options
	collect   Collector
	latest    Snapshot
	subs      map[chan Snapshot]bool
	wake      chan struct{}
	configure chan struct{}
	paused    bool
	urgent    bool // the pending request skips the change spacing
	attended  int  // subscribers someone is looking at
	onAttend  func(bool)
	started   bool
	closed    bool
	metrics   Metrics
}

func New(o Options, collect Collector) (*Engine, error) {
	if collect == nil || !o.valid() {
		return nil, errors.New("invalid observation scheduler options")
	}
	var epoch [16]byte
	if _, err := rand.Read(epoch[:]); err != nil {
		return nil, err
	}
	return &Engine{opts: o, collect: collect, latest: Snapshot{Epoch: hex.EncodeToString(epoch[:])}, subs: map[chan Snapshot]bool{}, wake: make(chan struct{}, 1), configure: make(chan struct{}, 1)}, nil
}

// UpdateOptions changes scheduling at a single serialized boundary. It does not
// renew the freshness of any cached observation.
func (e *Engine) UpdateOptions(o Options) error {
	if !o.valid() {
		return errors.New("invalid observation scheduler options")
	}
	e.mu.Lock()
	changed := e.opts != o
	e.opts = o
	e.mu.Unlock()
	if changed {
		select {
		case e.configure <- struct{}{}:
		default:
		}
	}
	return nil
}
func (e *Engine) Options() Options { e.mu.Lock(); defer e.mu.Unlock(); return e.opts }
func (e *Engine) Latest() Snapshot { e.mu.Lock(); defer e.mu.Unlock(); return e.latest.clone() }

func (e *Engine) Metrics() Metrics {
	e.mu.Lock()
	defer e.mu.Unlock()
	m := e.metrics
	m.Subscribers, m.Attended, m.Paused = len(e.subs), e.attended, e.paused
	m.Pending = !e.closed && (m.Pending || len(e.wake) > 0)
	return m
}

// Subscribe replays the current snapshot, without renewing freshness. Slow clients
// receive the newest snapshot and cannot block the collector or accumulate a queue.
func (e *Engine) Subscribe() (<-chan Snapshot, func()) { return e.subscribe(false) }

// SubscribeAttended is Subscribe for a client someone is looking at. While at least one
// is subscribed, collections follow changes within Gap; the first one asks for a
// collection at once, since the latest snapshot may be up to Idle old.
func (e *Engine) SubscribeAttended() (<-chan Snapshot, func()) { return e.subscribe(true) }

func (e *Engine) subscribe(attended bool) (<-chan Snapshot, func()) {
	e.mu.Lock()
	ch := make(chan Snapshot, 1)
	if e.closed {
		e.mu.Unlock()
		close(ch)
		return ch, func() {}
	}
	e.subs[ch] = attended
	if e.latest.Sequence != 0 {
		ch <- e.latest.clone()
	}
	var hook func(bool)
	if attended {
		e.attended++
		if e.attended == 1 {
			e.requestLocked()
			hook = e.onAttend
		}
	}
	e.mu.Unlock()
	if hook != nil {
		hook(true)
	}
	var once sync.Once
	return ch, func() {
		once.Do(func() {
			e.mu.Lock()
			var hook func(bool)
			if looked, ok := e.subs[ch]; ok {
				delete(e.subs, ch)
				close(ch)
				if looked {
					e.attended--
					e.reconfigureLocked()
					if e.attended == 0 {
						hook = e.onAttend
					}
				}
			}
			e.mu.Unlock()
			if hook != nil {
				hook(false)
			}
		})
	}
}

// Attend counts as an attended client until release is called, without receiving
// snapshots: for a source someone watches from elsewhere, such as a relay peer.
func (e *Engine) Attend() (release func()) { _, leave := e.subscribe(true); return leave }

// Attended reports whether someone looks at any subscribed client.
func (e *Engine) Attended() bool { e.mu.Lock(); defer e.mu.Unlock(); return e.attended > 0 }

// OnAttention is called when the first attended client subscribes (true) and when the
// last one leaves (false), outside the engine's lock. Set it before Run.
func (e *Engine) OnAttention(f func(attended bool)) { e.mu.Lock(); e.onAttend = f; e.mu.Unlock() }

func (e *Engine) reconfigureLocked() {
	select {
	case e.configure <- struct{}{}:
	default:
	}
}

// Notify reports a change. Collections it causes are spaced by Gap or Idle.
func (e *Engine) Notify() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.notifyLocked()
}

// Request asks for a collection now: an explicit refresh, not a reported change.
func (e *Engine) Request() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.requestLocked()
}
func (e *Engine) requestLocked() {
	e.urgent = true
	e.notifyLocked()
}
func (e *Engine) notifyLocked() {
	e.metrics.Notifications++
	select {
	case e.wake <- struct{}{}:
	default:
		e.metrics.Coalesced++
	}
}

// Refresh joins the existing collector and waits for an attempt started after
// this request. A replay or a collection already in flight cannot satisfy it.
// Concurrent foreground requests still use the engine's single coalesced work.
func (e *Engine) Refresh(ctx context.Context) (Snapshot, error) {
	requested := time.Now()
	updates, close := e.Subscribe()
	defer close()
	e.Request()
	for {
		select {
		case <-ctx.Done():
			return Snapshot{}, ctx.Err()
		case snapshot, ok := <-updates:
			if !ok {
				return Snapshot{}, errors.New("observation owner stopped")
			}
			if snapshot.Paused {
				return Snapshot{}, errors.New("observation owner is suspended")
			}
			if !snapshot.AttemptedAt.After(requested) {
				continue
			}
			if snapshot.Error != "" {
				return snapshot, errors.New("fresh observation failed")
			}
			return snapshot, nil
		}
	}
}

// Pause invalidates cached freshness immediately. Resume requests a new observation.
// A collection started before suspension cannot publish a fresh post-resume result.
func (e *Engine) Pause(paused bool) {
	e.mu.Lock()
	if e.paused != paused {
		e.paused = paused
		e.latest.Paused = paused
		e.latest.ExpiresAt = time.Time{}
		e.publishLocked()
	}
	e.requestLocked()
	e.mu.Unlock()
}

func (e *Engine) publishLocked() {
	e.latest.Sequence++
	for ch := range e.subs {
		select {
		case <-ch:
		default:
		}
		ch <- e.latest.clone()
	}
}

type result struct {
	data       json.RawMessage
	err        error
	started    time.Time
	generation uint64
	elapsed    time.Duration
	freshFor   time.Duration
}

// Run owns one timer and at most one collection. Notifications arriving during a
// collection cause one later reconciliation. The fallback is rearmed after completion,
// rather than waking periodically during a slow or suspended collection.
func (e *Engine) Run(ctx context.Context) error {
	e.mu.Lock()
	if e.started || e.closed {
		e.mu.Unlock()
		return errors.New("observation engine already started or closed")
	}
	e.started = true
	e.mu.Unlock()
	defer func() {
		e.mu.Lock()
		defer e.mu.Unlock()
		e.closed = true
		e.metrics.Collecting, e.metrics.Pending = false, false
		for ch := range e.subs {
			close(ch)
			delete(e.subs, ch)
		}
	}()
	opts := e.Options()
	timer := time.NewTimer(0)
	defer timer.Stop()
	due := timer.C
	results := make(chan result, 1)
	var collectors sync.WaitGroup
	defer collectors.Wait()
	var active context.CancelFunc
	defer func() {
		if active != nil {
			active()
		}
	}()
	var dirty bool
	var first, lastStart time.Time
	var generation uint64
	var previousPause bool
	arm := func(d time.Duration) { timer.Stop(); timer.Reset(d); due = timer.C }
	attention := func() (attended, urgent bool) {
		e.mu.Lock()
		defer e.mu.Unlock()
		return e.attended > 0, e.urgent
	}
	// soon arms a change-driven collection: debounced, bounded by MaxDelay, and no
	// sooner than the spacing after the previous start unless explicitly requested.
	soon := func(now time.Time) {
		attended, urgent := attention()
		delay := min(opts.Debounce, max(time.Duration(0), first.Add(opts.MaxDelay).Sub(now)))
		if !urgent && !lastStart.IsZero() {
			delay = max(delay, lastStart.Add(opts.spacing(attended)).Sub(now))
		}
		arm(delay)
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-e.configure:
			opts = e.Options()
			if active == nil {
				attended, _ := attention()
				switch {
				case dirty && !first.IsZero():
					soon(time.Now())
				case lastStart.IsZero():
					arm(0)
				default:
					arm(opts.reconcile(attended))
				}
			}
		case <-e.wake:
			e.mu.Lock()
			e.metrics.Pending = true
			paused, seq := e.paused, e.latest.Sequence
			e.mu.Unlock()
			if paused != previousPause || (active != nil && seq != generation) {
				if active != nil {
					active()
				}
				previousPause = paused
			}
			if paused {
				timer.Stop()
				due = nil
				dirty = true
				continue
			}
			dirty = true
			if active != nil {
				continue
			}
			now := time.Now()
			if first.IsZero() {
				first = now
			}
			soon(now)
		case <-due:
			due = nil
			e.mu.Lock()
			paused, seq := e.paused, e.latest.Sequence
			attended := e.attended > 0
			if !paused {
				e.urgent = false
				e.metrics.Collections++
				e.metrics.Collecting, e.metrics.Pending = true, false
			}
			e.mu.Unlock()
			if paused {
				continue
			}
			dirty, first = false, time.Time{}
			lastStart = time.Now()
			fresh := opts.freshFor(attended)
			cctx, cancel := context.WithTimeout(ctx, opts.Timeout)
			active, generation = cancel, seq
			collectors.Add(1)
			go func() {
				defer collectors.Done()
				started := time.Now()
				data, err := e.collect(cctx)
				if err == nil {
					err = cctx.Err()
				}
				if err == nil && !json.Valid(data) {
					err = errors.New("collector returned invalid JSON")
				}
				select {
				case results <- result{data: append(json.RawMessage(nil), data...), err: err, started: started, generation: seq, elapsed: time.Since(started), freshFor: fresh}:
				case <-ctx.Done():
				}
			}()
		case r := <-results:
			active()
			active = nil
			e.mu.Lock()
			e.metrics.Collecting = false
			paused := e.paused
			e.metrics.LastCollectionNanos = int64(r.elapsed)
			e.metrics.CollectionNanos += int64(r.elapsed)
			if r.err != nil {
				e.metrics.Failures++
			}
			if !paused && e.latest.Sequence == r.generation {
				e.latest.AttemptedAt = r.started
				e.latest.Error = ""
				if r.err != nil {
					e.latest.Error = r.err.Error()
				} else {
					e.latest.Data = r.data
					e.latest.ObservedAt = r.started
					e.latest.ExpiresAt = r.started.Add(r.freshFor)
				}
				e.publishLocked()
			} else {
				dirty = true
			}
			e.metrics.Pending = dirty
			attended := e.attended > 0
			e.mu.Unlock()
			if paused {
				continue
			}
			if dirty {
				first = time.Now()
				soon(first)
			} else {
				arm(opts.reconcile(attended))
			}
		}
	}
}
