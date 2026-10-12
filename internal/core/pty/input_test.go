package pty

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

// Only feed is running: the writer supplies actual accepted byte counts while
// the normal queue, ownership and error frames remain in use.
type inputWriter struct {
	backend
	mu      sync.Mutex
	limit   int
	err     error
	data    []byte
	calls   int
	entered chan struct{}
	release <-chan struct{}
}

func (w *inputWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.calls == 0 && w.entered != nil {
		close(w.entered)
		<-w.release
	}
	n := len(p)
	if n > w.limit {
		n = w.limit
	}
	w.data = append(w.data, p[:n]...)
	w.calls++
	err := w.err
	if n == len(p) && errors.Is(err, io.ErrShortWrite) {
		err = nil
	}
	return n, err
}

func inputSession(t *testing.T, w *inputWriter) (*Session, *viewer) {
	t.Helper()
	s := handoverSession()
	s.be = w
	v := s.attach()
	stopped := make(chan struct{})
	go func() { s.feed(); close(stopped) }()
	t.Cleanup(func() {
		close(s.done)
		select {
		case <-stopped:
		case <-time.After(time.Second):
			t.Error("input feeder did not exit")
		}
	})
	return s, v
}

func inputDrained(t *testing.T, s *Session) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		s.mu.Lock()
		pending := s.pending
		s.mu.Unlock()
		if pending == 0 {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("input remained queued")
}

func inputErrors(s *Session, v *viewer) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var messages []string
	for _, frame := range v.items {
		if frame[0] == frameError {
			messages = append(messages, string(frame[1:]))
		}
	}
	return strings.Join(messages, "\n")
}

func TestInputShortWritesPreserveControlSequencesAndMessageOrder(t *testing.T) {
	for _, err := range []error{nil, io.ErrShortWrite} {
		t.Run(fmt.Sprint(err), func(t *testing.T) { testInputShortWrites(t, err) })
	}
}

func testInputShortWrites(t *testing.T, err error) {
	w := &inputWriter{limit: 2, err: err}
	s, v := inputSession(t, w)
	first := []byte("\x1b[200~日本語\n\x1b[201~")
	second := []byte("\x1b]11;rgb:1f1f/1f1f/1c1c\x1b\\")
	if err := s.typed(first); err != nil {
		t.Fatal(err)
	}
	if err := s.typed(second); err != nil {
		t.Fatal(err)
	}
	inputDrained(t, s)
	w.mu.Lock()
	defer w.mu.Unlock()
	if want := append(append([]byte(nil), first...), second...); !bytes.Equal(w.data, want) {
		t.Fatalf("terminal input lost or reordered: got %q, want %q", w.data, want)
	}
	if message := inputErrors(s, v); message != "" {
		t.Fatalf("successful input reported failure: %s", message)
	}
}

func TestInputWriteFailureIsVisibleAndStopsFurtherInput(t *testing.T) {
	w := &inputWriter{limit: 2, err: io.ErrClosedPipe}
	s, v := inputSession(t, w)
	if err := s.typed([]byte("\x1b[200~private paste\x1b[201~")); err != nil {
		t.Fatal(err)
	}
	inputDrained(t, s)
	message := inputErrors(s, v)
	if !strings.Contains(message, "2 of 25 bytes") || !strings.Contains(message, io.ErrClosedPipe.Error()) {
		t.Fatalf("missing input failure: %q", message)
	}
	if strings.Contains(message, "private paste") {
		t.Fatalf("input content leaked into error: %q", message)
	}
	if err := s.typed([]byte("next key")); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("input failure was not preserved: %v", err)
	}
	fresh := s.attach()
	if inputErrors(s, fresh) != message {
		t.Fatal("replacement terminal window lost the input failure")
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if !bytes.Equal(w.data, []byte("\x1b[")) || w.calls != 1 {
		t.Fatalf("failed input was retried or extended: %q, %d calls", w.data, w.calls)
	}
}

func TestInputZeroProgressFailsInsteadOfSpinning(t *testing.T) {
	w := &inputWriter{limit: 0}
	s, v := inputSession(t, w)
	if err := s.typed([]byte("x")); err != nil {
		t.Fatal(err)
	}
	inputDrained(t, s)
	if !strings.Contains(inputErrors(s, v), "0 of 1 bytes") {
		t.Fatal("zero-progress input was silently discarded")
	}
	if err := s.typed([]byte("again")); err == nil {
		t.Fatal("accepted input after writer stopped making progress")
	}
}

func TestInputFailureDoesNotDeliverAlreadyQueuedKeys(t *testing.T) {
	release := make(chan struct{})
	w := &inputWriter{limit: 2, err: io.ErrClosedPipe, entered: make(chan struct{}), release: release}
	s, _ := inputSession(t, w)
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	t.Cleanup(unblock)
	if err := s.typed([]byte("\x1b[200~")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-w.entered:
	case <-time.After(time.Second):
		t.Fatal("first input did not reach writer")
	}
	if err := s.typed([]byte("queued prompt\r")); err != nil {
		t.Fatal(err)
	}
	unblock()
	inputDrained(t, s)
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.calls != 1 || !bytes.Equal(w.data, []byte("\x1b[")) {
		t.Fatalf("queued keys extended failed input: calls=%d, data=%q", w.calls, w.data)
	}
}
