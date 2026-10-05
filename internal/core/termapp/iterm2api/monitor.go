package iterm2api

import (
	"context"
	"errors"
	"sync"
	"time"
)

// MonitorOptions configure a Monitor.
type MonitorOptions struct {
	// Options are passed to Connect.
	Options Options
	// Connect replaces Connect (tests).
	Connect func(ctx context.Context) (*Client, error)
	// MinBackoff and MaxBackoff bound the wait between reconnection attempts; zero means
	// 500 ms and 30 s.
	MinBackoff, MaxBackoff time.Duration
}

// Monitor holds one long-lived connection for notifications, reconnecting when iTerm2
// drops it (iTerm2 quit and came back, or the user restarted the API). Each reconnection
// asks for a fresh cookie, which does not prompt again once the user has consented.
//
// When the connection comes back, every watched session that is no longer in iTerm2's
// layout is reported as terminated, so a tab that closed while the connection was down
// is not missed.
type Monitor struct {
	opts MonitorOptions

	mu         sync.Mutex
	watched    map[string]chan struct{}
	terminated map[string]bool
	order      []string // terminated ids, oldest first, to bound the set
	client     *Client

	events *eventQueue
}

const keepTerminated = 1024

// NewMonitor makes a Monitor; Run starts it.
func NewMonitor(o MonitorOptions) *Monitor {
	if o.MinBackoff == 0 {
		o.MinBackoff = 500 * time.Millisecond
	}
	if o.MaxBackoff == 0 {
		o.MaxBackoff = 30 * time.Second
	}
	if o.Connect == nil {
		opts := o.Options
		o.Connect = func(ctx context.Context) (*Client, error) { return Connect(ctx, opts) }
	}
	return &Monitor{
		opts:       o,
		watched:    map[string]chan struct{}{},
		terminated: map[string]bool{},
		events:     newEventQueue(),
	}
}

// Events delivers every notification (and Reconnected) in order until Run returns. The
// caller must drain it.
func (m *Monitor) Events() <-chan Event { return m.events.out }

// Watch returns a channel that is closed when session terminates (at once if it already
// has).
func (m *Monitor) Watch(session string) <-chan struct{} {
	m.mu.Lock()
	defer m.mu.Unlock()
	ch, ok := m.watched[session]
	if !ok {
		ch = make(chan struct{})
		m.watched[session] = ch
	}
	if m.terminated[session] {
		closeOnce(ch)
	}
	return ch
}

// Unwatch forgets a session.
func (m *Monitor) Unwatch(session string) {
	m.mu.Lock()
	delete(m.watched, session)
	m.mu.Unlock()
}

// Client returns the current connection, or nil while there is none. Requests on it may
// fail with ErrClosed if it drops.
func (m *Monitor) Client() *Client {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.client
}

func closeOnce(ch chan struct{}) {
	select {
	case <-ch:
	default:
		close(ch)
	}
}

func (m *Monitor) markTerminated(session string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.terminated[session] {
		m.terminated[session] = true
		m.order = append(m.order, session)
		if len(m.order) > keepTerminated {
			delete(m.terminated, m.order[0])
			m.order = m.order[1:]
		}
	}
	if ch, ok := m.watched[session]; ok {
		closeOnce(ch)
	}
}

// reconcile reports watched sessions that are missing from the layout as terminated.
func (m *Monitor) reconcile(l Layout) {
	present := map[string]bool{}
	for _, s := range l.Sessions() {
		present[s] = true
	}
	m.mu.Lock()
	var gone []string
	for s, ch := range m.watched {
		select {
		case <-ch:
			continue
		default:
		}
		if !present[s] {
			gone = append(gone, s)
		}
	}
	m.mu.Unlock()
	for _, s := range gone {
		m.markTerminated(s)
		m.events.push(Event{Kind: SessionTerminated, SessionID: s})
	}
}

// Run connects, subscribes and delivers events until ctx ends. It returns at once with
// Connect's error if the first connection fails (the caller then uses its AppleScript
// path), and with ErrNotAuthorized or ErrTooOld if a reconnection is refused, so a
// declined consent is never asked again in a loop. A lost connection is retried with
// backoff while iTerm2 is away.
func (m *Monitor) Run(ctx context.Context) error {
	defer m.events.close()
	first := true
	backoff := m.opts.MinBackoff
	for {
		c, err := m.session(ctx, first)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if first || errors.Is(err, ErrNotAuthorized) || errors.Is(err, ErrTooOld) {
				return err
			}
			t := time.NewTimer(backoff)
			select {
			case <-ctx.Done():
				t.Stop()
				return ctx.Err()
			case <-t.C:
			}
			backoff = min(backoff*2, m.opts.MaxBackoff)
			continue
		}
		first = false
		backoff = m.opts.MinBackoff
		stop := context.AfterFunc(ctx, func() { _ = c.Close() })
		for e := range c.Events() {
			if e.Kind == SessionTerminated {
				m.markTerminated(e.SessionID)
			}
			m.events.push(e)
		}
		stop()
		m.mu.Lock()
		m.client = nil
		m.mu.Unlock()
		if ctx.Err() != nil {
			return ctx.Err()
		}
	}
}

// session connects, subscribes and reconciles; on success the client is current.
func (m *Monitor) session(ctx context.Context, first bool) (*Client, error) {
	c, err := m.opts.Connect(ctx)
	if err != nil {
		return nil, err
	}
	if err := c.Subscribe(ctx); err != nil {
		_ = c.Close()
		return nil, err
	}
	l, err := c.ListSessions(ctx)
	if err != nil {
		_ = c.Close()
		return nil, err
	}
	m.reconcile(l)
	if !first {
		m.events.push(Event{Kind: Reconnected})
	}
	m.mu.Lock()
	m.client = c
	m.mu.Unlock()
	return c, nil
}
