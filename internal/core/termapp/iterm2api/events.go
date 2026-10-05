package iterm2api

import "sync"

// EventKind is what a notification reports.
type EventKind int

const (
	// SessionTerminated: a session ended and left its tab (its pane closed). The protocol
	// carries no exit status.
	SessionTerminated EventKind = iota + 1
	// SessionCreated: a new session appeared.
	SessionCreated
	// FocusChanged: the key window, selected tab, active pane or app activation changed.
	FocusChanged
	// Reconnected (Monitor only): the connection was lost and is back. Watched sessions
	// that disappeared meanwhile have already been reported as SessionTerminated.
	Reconnected
)

func (k EventKind) String() string {
	switch k {
	case SessionTerminated:
		return "session terminated"
	case SessionCreated:
		return "session created"
	case FocusChanged:
		return "focus changed"
	case Reconnected:
		return "reconnected"
	}
	return "unknown"
}

// Event is one notification from iTerm2.
type Event struct {
	Kind      EventKind
	SessionID string      // SessionTerminated, SessionCreated
	Focus     FocusChange // FocusChanged
}

// eventQueue is an unbounded FIFO in front of a channel, so a slow consumer never stalls
// the connection's reader (which also delivers responses) and no event is dropped.
type eventQueue struct {
	mu     sync.Mutex
	cond   *sync.Cond
	items  []Event
	closed bool
	out    chan Event
	// abandoned is closed when the owner will read no more (Client.Close), so the pump
	// does not wait forever on an undrained channel.
	abandoned chan struct{}
	once      sync.Once
}

func newEventQueue() *eventQueue {
	q := &eventQueue{out: make(chan Event), abandoned: make(chan struct{})}
	q.cond = sync.NewCond(&q.mu)
	go q.pump()
	return q
}

func (q *eventQueue) push(e Event) {
	q.mu.Lock()
	q.items = append(q.items, e)
	q.mu.Unlock()
	q.cond.Signal()
}

func (q *eventQueue) close() {
	q.mu.Lock()
	q.closed = true
	q.mu.Unlock()
	q.cond.Signal()
}

func (q *eventQueue) abandon() {
	q.once.Do(func() { close(q.abandoned) })
	q.close()
}

func (q *eventQueue) pump() {
	defer close(q.out)
	for {
		q.mu.Lock()
		for len(q.items) == 0 && !q.closed {
			q.cond.Wait()
		}
		if len(q.items) == 0 {
			q.mu.Unlock()
			return
		}
		e := q.items[0]
		q.items = q.items[1:]
		q.mu.Unlock()
		select {
		case q.out <- e:
		case <-q.abandoned:
			return
		}
	}
}
