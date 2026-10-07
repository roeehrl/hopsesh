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
}

func Defaults() Options {
	return Options{Reconcile: time.Minute, Debounce: 250 * time.Millisecond, MaxDelay: 2 * time.Second, Timeout: 30 * time.Second, FreshFor: 90 * time.Second}
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
type Engine struct {
	mu      sync.Mutex
	opts    Options
	collect Collector
	latest  Snapshot
	subs    map[chan Snapshot]bool
	wake    chan struct{}
	paused  bool
	started bool
	closed  bool
}

func New(o Options, collect Collector) (*Engine, error) {
	if collect == nil || o.Reconcile <= 0 || o.Debounce <= 0 || o.MaxDelay < o.Debounce || o.Timeout <= 0 || o.FreshFor <= 0 {
		return nil, errors.New("invalid observation scheduler options")
	}
	var epoch [16]byte
	if _, err := rand.Read(epoch[:]); err != nil {
		return nil, err
	}
	return &Engine{opts: o, collect: collect, latest: Snapshot{Epoch: hex.EncodeToString(epoch[:])}, subs: map[chan Snapshot]bool{}, wake: make(chan struct{}, 1)}, nil
}

func (e *Engine) Latest() Snapshot { e.mu.Lock(); defer e.mu.Unlock(); return e.latest.clone() }

// Subscribe replays the current snapshot, without renewing freshness. Slow clients
// receive the newest snapshot and cannot block the collector or accumulate a queue.
func (e *Engine) Subscribe() (<-chan Snapshot, func()) {
	e.mu.Lock()
	defer e.mu.Unlock()
	ch := make(chan Snapshot, 1)
	if e.closed {
		close(ch)
		return ch, func() {}
	}
	e.subs[ch] = true
	if e.latest.Sequence != 0 {
		ch <- e.latest.clone()
	}
	var once sync.Once
	return ch, func() {
		once.Do(func() {
			e.mu.Lock()
			defer e.mu.Unlock()
			if e.subs[ch] {
				delete(e.subs, ch)
				close(ch)
			}
		})
	}
}

func (e *Engine) Notify() {
	select {
	case e.wake <- struct{}{}:
	default:
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
	e.mu.Unlock()
	e.Notify()
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
		for ch := range e.subs {
			close(ch)
			delete(e.subs, ch)
		}
	}()
	timer := time.NewTimer(0)
	defer timer.Stop()
	due := timer.C
	results := make(chan result, 1)
	var active context.CancelFunc
	defer func() {
		if active != nil {
			active()
		}
	}()
	var dirty bool
	var first time.Time
	var generation uint64
	var previousPause bool
	arm := func(d time.Duration) { timer.Stop(); timer.Reset(d); due = timer.C }
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-e.wake:
			e.mu.Lock()
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
			delay := min(e.opts.Debounce, max(time.Duration(0), first.Add(e.opts.MaxDelay).Sub(now)))
			arm(delay)
		case <-due:
			due = nil
			e.mu.Lock()
			paused, seq := e.paused, e.latest.Sequence
			e.mu.Unlock()
			if paused {
				continue
			}
			dirty, first = false, time.Time{}
			cctx, cancel := context.WithTimeout(ctx, e.opts.Timeout)
			active, generation = cancel, seq
			go func() {
				started := time.Now()
				data, err := e.collect(cctx)
				if err == nil {
					err = cctx.Err()
				}
				if err == nil && !json.Valid(data) {
					err = errors.New("collector returned invalid JSON")
				}
				select {
				case results <- result{data: append(json.RawMessage(nil), data...), err: err, started: started, generation: seq}:
				case <-ctx.Done():
				}
			}()
		case r := <-results:
			active()
			active = nil
			e.mu.Lock()
			paused := e.paused
			if !paused && e.latest.Sequence == r.generation {
				e.latest.AttemptedAt = r.started
				e.latest.Error = ""
				if r.err != nil {
					e.latest.Error = r.err.Error()
				} else {
					e.latest.Data = r.data
					e.latest.ObservedAt = r.started
					e.latest.ExpiresAt = r.started.Add(e.opts.FreshFor)
				}
				e.publishLocked()
			} else {
				dirty = true
			}
			e.mu.Unlock()
			if paused {
				continue
			}
			if dirty {
				first = time.Now()
				arm(e.opts.Debounce)
			} else {
				arm(e.opts.Reconcile)
			}
		}
	}
}
