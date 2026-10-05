package gui

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/roeehrl/hopsesh/internal/agents/all"
	"github.com/roeehrl/hopsesh/internal/config"
	"github.com/roeehrl/hopsesh/internal/core/termapp"
)

// watchingTerminal is a terminal app that reports when a launch's tab ends (as iTerm2 with
// its Python API does): the code the test sends.
type watchingTerminal struct {
	codes chan int
	lines []string
}

func (w *watchingTerminal) ID() string                      { return "watching" }
func (w *watchingTerminal) Name() string                    { return "Watching Terminal" }
func (w *watchingTerminal) Available(context.Context) error { return nil }
func (w *watchingTerminal) Open(_ context.Context, l termapp.Launch) (termapp.Handle, error) {
	w.lines = append(w.lines, termapp.Line(l))
	return termapp.Handle{Terminal: w.ID(), Ref: "tab-1"}, nil
}
func (w *watchingTerminal) Exited(context.Context, termapp.Handle) (<-chan int, error) {
	ch := make(chan int, 1)
	go func() { ch <- <-w.codes; close(ch) }()
	return ch, nil
}

// A session opened in the user's terminal app: when that terminal says the launch ended,
// the window hears how ("exited N", or closed), and ExternalExits keeps it.
func TestExternalExit(t *testing.T) {
	home(t)
	fakeClaude(t)
	a := NewApp(all.Registry())
	defer a.Shutdown()
	w := &watchingTerminal{codes: make(chan int, 2)}
	a.core.Terminals = termapp.NewSet(w)
	testTerminal = w
	defer func() { testTerminal = nil }()
	SetStepProgram("/usr/bin/true")
	a.core.Cfg.Terminal.Resume = config.ResumeTerminal
	var (
		mu  sync.Mutex
		got []ExternalExitDTO
	)
	a.Emitter = func(name string, data any) {
		if name == ExternalExitEvent {
			mu.Lock()
			got = append(got, data.(ExternalExitDTO))
			mu.Unlock()
		}
	}
	scan, err := a.Scan()
	if err != nil {
		t.Fatal(err)
	}
	e := findEntry(t, scan, "claude/"+sid)
	if r, err := a.ResumeSession(e.Machine, e.Key, ""); err != nil || r.Where != WhereTerminal || len(w.lines) != 1 {
		t.Fatalf("%+v %v %v", r, err, w.lines)
	}
	w.codes <- 3
	deadline := time.Now().Add(5 * time.Second)
	for {
		mu.Lock()
		n := len(got)
		mu.Unlock()
		if n > 0 || time.Now().After(deadline) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	mu.Lock()
	first := append([]ExternalExitDTO{}, got...)
	mu.Unlock()
	if len(first) != 1 || first[0].Code != 3 || first[0].Closed || first[0].Key != e.Key || first[0].Machine != e.Machine ||
		first[0].Terminal != "Watching Terminal" || !strings.Contains(first[0].Title, "codeword") {
		t.Fatalf("%+v", first)
	}
	if x := a.ExternalExits(); len(x) == 0 || x[len(x)-1].Code != 3 {
		t.Fatalf("kept: %+v", x)
	}
	a.noteExit(ExternalExitDTO{Kind: "step", Title: "Fix the parser"}, -1)
	mu.Lock()
	last := got[len(got)-1]
	mu.Unlock()
	if last.Code != -1 || !last.Closed {
		t.Fatalf("closed: %+v", last)
	}
}
