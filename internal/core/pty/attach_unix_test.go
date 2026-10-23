//go:build !windows

package pty

import (
	"bytes"
	"context"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/roeehrl/hopsesh/internal/core/serve"
)

type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) { s.mu.Lock(); defer s.mu.Unlock(); return s.b.Write(p) }
func (s *syncBuf) String() string              { s.mu.Lock(); defer s.mu.Unlock(); return s.b.String() }

// A tab run by hopsesh serve is shown, typed in and resized from another terminal, and
// its exit code comes back.
func TestAttachRunsATabFromAfar(t *testing.T) {
	m := NewManager(Options{})
	defer m.CloseAll()
	s, err := m.Start(Spec{Argv: []string{"/bin/sh", "-c", `stty size; IFS= read -r x; echo "got:$x"; exit 3`}})
	if err != nil {
		t.Fatal(err)
	}
	far, near := net.Pipe()
	ctx := context.Background()
	go func() {
		_ = Serve(serve.NewFrameConn(ctx, far, far, far.Close), func(id string) (*Session, error) { return m.mustGet(id) }, nil)
	}()
	sizes := make(chan [2]int, 1)
	sizes <- [2]int{120, 40}
	in := strings.NewReader("")
	out := &syncBuf{}
	done := make(chan int, 1)
	var keys bytes.Buffer
	keys.WriteString("hello\n")
	go func() {
		code, err := Attach(serve.NewFrameConn(ctx, near, near, near.Close), s.ID(), slowReader{&keys, in}, out, sizes, nil)
		if err != nil {
			t.Error(err)
		}
		done <- code
	}()
	select {
	case code := <-done:
		if code != 3 || !strings.Contains(out.String(), "got:hello") {
			t.Fatalf("code %d, output %q", code, out.String())
		}
	case <-time.After(10 * time.Second):
		t.Fatalf("no exit; output %q", out.String())
	}
}

// slowReader gives its keys after the tab is attached and sized.
type slowReader struct {
	keys *bytes.Buffer
	rest *strings.Reader
}

func (r slowReader) Read(p []byte) (int, error) {
	if r.keys.Len() > 0 {
		time.Sleep(300 * time.Millisecond)
		return r.keys.Read(p)
	}
	select {}
}

func (m *Manager) mustGet(id string) (*Session, error) {
	s, ok := m.Get(id)
	if !ok {
		return nil, ErrClosed
	}
	return s, nil
}
