package pty

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"sync"
	"time"

	"github.com/roeehrl/hopsesh/internal/core/term"
)

// backend is a started program in its pseudo-terminal (start_unix.go, start_windows.go).
type backend interface {
	// Read is what the program prints; it ends (an error) once the terminal is closed or
	// every holder of the program's end is gone.
	Read(p []byte) (int, error)
	// Write is the user's input.
	Write(p []byte) (int, error)
	Resize(cols, rows int) error
	// Wait waits for the program and returns its exit code (-1 unknown, 128+n for signal
	// n).
	Wait() int
	// Hangup asks the program to end (SIGHUP to its process group; closing the
	// pseudoconsole on Windows); Kill makes it.
	Hangup()
	Kill()
	// CloseTerminal closes the pseudo-terminal, which ends Read.
	CloseTerminal()
	// ClosesOnExit: the output ends by itself once the program and its children are gone
	// (Unix); a pseudoconsole keeps it open until it is closed.
	ClosesOnExit() bool
	// Name is what the terminal is ("pty", "conpty (bundled)", "conpty (system)").
	Name() string
}

// Flow control between a tab and its window, in bytes of output the window has not yet
// said it drew (Serve's acks): above flowHigh hopsesh stops reading the program (so the
// program waits on its own output, as in any terminal), and goes on below flowLow.
const (
	flowHigh = 512 << 10
	flowLow  = 10 << 10
	// maxFrame is the largest output frame sent at once; reads in between are merged
	// into one frame up to it.
	maxFrame = 64 << 10
	// maxPendingInput is how much of the user's input may wait for a program that does
	// not read it.
	maxPendingInput = 4 << 20
)

// Session is one tab: its program, its terminal and what it printed.
type Session struct {
	m    *Manager
	id   string
	spec Spec
	be   backend

	mu      sync.Mutex
	flow    *sync.Cond // the reader waits on it for the window to catch up
	info    Info
	scroll  *ring
	capture *term.Capture
	note    *notifier
	view    *viewer
	idle    *time.Timer
	// printedSince: the program printed something since the user last typed.
	printedSince bool
	closing      bool // stop flow control: the program ended or is being ended
	gone         bool // forgotten: nothing more is kept

	input   chan []byte
	pending int // bytes in input
	done    chan struct{}
	ended   chan struct{} // the output is finished (after done)
}

func newSession(m *Manager, id string, spec Spec, cols, rows int) *Session {
	title := spec.Title
	if title == "" {
		title = filepath.Base(spec.Argv[0])
	}
	s := &Session{m: m, id: id, spec: spec, scroll: newRing(m.opts.Scrollback),
		input: make(chan []byte, 256), done: make(chan struct{}), ended: make(chan struct{}),
		info: Info{ID: id, Title: title, Program: filepath.Base(spec.Argv[0]), Dir: spec.Dir, State: Running, Code: -1,
			Cols: cols, Rows: rows, Capture: spec.Capture == CaptureStep, Private: spec.Private, Started: time.Now().UTC()}}
	s.flow = sync.NewCond(&s.mu)
	if spec.Capture == CaptureStep {
		s.capture = &term.Capture{}
	}
	if !spec.Private {
		s.note = &notifier{}
	}
	return s
}

// ID is the tab's id.
func (s *Session) ID() string { return s.id }

// Info is the tab as it is now.
func (s *Session) Info() Info {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.info
}

// Done is closed once the program ended and its output was read.
func (s *Session) Done() <-chan struct{} { return s.ended }

// Wait waits for the program to end (or ctx) and returns its exit code.
func (s *Session) Wait(ctx context.Context) (int, error) {
	select {
	case <-s.ended:
		return s.Info().Code, nil
	case <-ctx.Done():
		return -1, ctx.Err()
	}
}

// run starts the tab's goroutines on its started program.
func (s *Session) run(be backend) {
	s.mu.Lock()
	s.be = be
	s.info.Backend = be.Name()
	s.mu.Unlock()
	output := make(chan struct{})
	go s.read(output)
	go s.feed()
	go func() {
		code := be.Wait()
		close(s.done)
		s.mu.Lock()
		s.closing = true
		s.flow.Broadcast()
		s.mu.Unlock()
		drained := func(d time.Duration) bool {
			select {
			case <-output:
				return true
			case <-time.After(d):
				return false
			}
		}
		// A child that kept the terminal (a background job) must not keep the tab open.
		if !be.ClosesOnExit() || !drained(1500*time.Millisecond) {
			be.CloseTerminal()
			drained(3 * time.Second)
		}
		s.mu.Lock()
		if s.idle != nil {
			s.idle.Stop()
		}
		s.info.State, s.info.Reason, s.info.Code = Exited, "", code
		info := s.info
		s.toViewer(frameState, stateJSON(info))
		s.mu.Unlock()
		close(s.ended)
		s.m.changed(info)
	}()
}

// read copies what the program prints into the scrollback, the capture and the window,
// holding back while the window is behind.
func (s *Session) read(output chan<- struct{}) {
	defer close(output)
	buf := make([]byte, 32<<10)
	for {
		s.mu.Lock()
		for !s.closing && s.view != nil && s.view.paused {
			s.flow.Wait()
		}
		s.mu.Unlock()
		n, err := s.be.Read(buf)
		if n > 0 {
			s.printed(buf[:n])
		}
		if err != nil {
			return
		}
	}
}

// printed takes in one read of output.
func (s *Session) printed(p []byte) {
	s.mu.Lock()
	if s.gone {
		s.mu.Unlock()
		return
	}
	s.scroll.write(p)
	if s.capture != nil {
		_, _ = s.capture.Write(p)
	}
	s.toViewer(frameOutput, p)
	var changed *Info
	if s.note != nil {
		switch s.note.feed(p) {
		case SignalBell:
			changed = s.setState(Waiting, ReasonBell)
		case SignalNotification:
			changed = s.setState(Waiting, ReasonNotification)
		}
	}
	if changed == nil && s.info.State == Waiting && s.info.Reason == ReasonIdle {
		changed = s.setState(Running, "")
	}
	s.printedSince = true
	if s.m.opts.IdleAfter > 0 && s.info.State == Running {
		if s.idle == nil {
			s.idle = time.AfterFunc(s.m.opts.IdleAfter, s.quiet)
		} else {
			s.idle.Reset(s.m.opts.IdleAfter)
		}
	}
	s.mu.Unlock()
	if changed != nil {
		s.m.changed(*changed)
	}
}

// quiet: the program printed something and has been quiet since.
func (s *Session) quiet() {
	s.mu.Lock()
	var changed *Info
	if s.info.State == Running && s.printedSince {
		changed = s.setState(Waiting, ReasonIdle)
	}
	s.mu.Unlock()
	if changed != nil {
		s.m.changed(*changed)
	}
}

// setState moves the tab to st (s.mu held) and returns its info if that changed anything,
// after telling the window.
func (s *Session) setState(st State, reason string) *Info {
	if s.info.State == Exited || s.info.State == st && s.info.Reason == reason {
		return nil
	}
	if st == Waiting && s.info.State == Waiting && s.info.Reason != ReasonIdle && reason == ReasonIdle {
		return nil // a notification outranks going quiet
	}
	s.info.State, s.info.Reason = st, reason
	i := s.info
	s.toViewer(frameState, stateJSON(i))
	return &i
}

// typed is the user's input, from the tab's window: queued for the program.
func (s *Session) typed(p []byte) error {
	if len(p) == 0 {
		return nil
	}
	s.mu.Lock()
	if s.info.State == Exited || s.gone {
		s.mu.Unlock()
		return errors.New("the program has ended")
	}
	if s.pending+len(p) > maxPendingInput {
		s.mu.Unlock()
		return errors.New("the program is not reading its input")
	}
	s.pending += len(p)
	s.printedSince = false
	if s.idle != nil {
		s.idle.Stop()
	}
	changed := s.setState(Running, "")
	s.mu.Unlock()
	if changed != nil {
		s.m.changed(*changed)
	}
	select {
	case s.input <- append([]byte(nil), p...):
		return nil
	case <-s.done:
		return errors.New("the program has ended")
	}
}

// feed writes the queued input into the program, in order.
func (s *Session) feed() {
	for {
		select {
		case p := <-s.input:
			_, _ = s.be.Write(p)
			clear(p)
			s.mu.Lock()
			s.pending -= len(p)
			s.mu.Unlock()
		case <-s.done:
			return
		}
	}
}

// resize gives the program a terminal of cols by rows.
func (s *Session) resize(cols, rows int) error {
	cols, rows = clampSize(cols, rows)
	s.mu.Lock()
	if s.info.State == Exited || s.be == nil {
		s.mu.Unlock()
		return nil
	}
	if s.info.Cols == cols && s.info.Rows == rows {
		s.mu.Unlock()
		return nil
	}
	s.info.Cols, s.info.Rows = cols, rows
	s.mu.Unlock()
	return s.be.Resize(cols, rows)
}

// end ends the program (asking first, then making it) and waits for it, a few seconds at
// most.
func (s *Session) end() {
	select {
	case <-s.ended:
		return
	default:
	}
	s.mu.Lock()
	s.closing = true
	s.flow.Broadcast()
	be := s.be
	s.mu.Unlock()
	if be == nil {
		return
	}
	be.Hangup()
	select {
	case <-s.ended:
		return
	case <-time.After(2 * time.Second):
	}
	be.Kill()
	be.CloseTerminal()
	select {
	case <-s.ended:
	case <-time.After(3 * time.Second):
	}
}

// forget zeroes what the tab kept and lets go of its window.
func (s *Session) forget() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.gone = true
	s.scroll.wipe()
	if s.capture != nil {
		s.capture.Reset()
		s.capture = nil
	}
	if s.idle != nil {
		s.idle.Stop()
	}
	if s.view != nil {
		s.view.close()
		s.view = nil
	}
	s.flow.Broadcast()
}

// The frames a tab sends its window (see Serve): the first byte says what it is.
const (
	frameOutput = 'o' // what the program printed
	frameState  = 's' // the tab's Info, as JSON
	frameError  = 'e' // something hopsesh could not do, as text
)

// stateJSON is a state frame's body.
func stateJSON(i Info) []byte {
	b, _ := json.Marshal(i)
	return b
}

// toViewer queues a frame for the tab's window, if one is attached (s.mu held). Output
// counts against flow control.
func (s *Session) toViewer(kind byte, p []byte) {
	if s.view != nil {
		s.view.push(kind, p)
	}
}
