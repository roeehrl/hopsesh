package tui

import (
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/exp/teatest/v2"
	"github.com/charmbracelet/x/vt"

	"github.com/roeehrl/hopsesh/internal/agents/all"
	"github.com/roeehrl/hopsesh/internal/app"
	"github.com/roeehrl/hopsesh/internal/config"
	"github.com/roeehrl/hopsesh/internal/core/move"
	"github.com/roeehrl/hopsesh/internal/testkit"
)

// newModel is the TUI on a demo home (Claude Code and Codex sessions in one repository).
func newModel(t *testing.T) *model {
	t.Helper()
	h := t.TempDir()
	for k, v := range testkit.Env(h) {
		t.Setenv(k, v)
	}
	if err := testkit.DemoHome(h); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	a := app.New(cfg, all.Registry(), config.StateDir(), nil)
	return &model{deps: Deps{App: a, Describe: func(app.Entry) string { return "" }}, mode: modeLoading, started: time.Now(), opts: a.DefaultOptions()}
}

// screen is what a terminal would show of the TUI's output (it redraws only what
// changed, so the output stream alone does not hold whole lines).
type screen struct {
	mu sync.Mutex
	em *vt.Emulator
}

func watch(t *testing.T, tm *teatest.TestModel, w, h int) *screen {
	t.Helper()
	s := &screen{em: vt.NewEmulator(w, h)}
	done := make(chan struct{})
	t.Cleanup(func() { close(done) })
	go func() { _, _ = io.Copy(io.Discard, s.em) }() // the terminal's replies to queries
	go func() {
		buf := make([]byte, 32<<10)
		for {
			select {
			case <-done:
				return
			default:
			}
			n, err := tm.Output().Read(buf)
			if n > 0 {
				s.mu.Lock()
				_, _ = s.em.Write(buf[:n])
				s.mu.Unlock()
			}
			if err != nil && err != io.EOF {
				return
			}
			if n == 0 {
				time.Sleep(10 * time.Millisecond)
			}
		}
	}()
	return s
}

func (s *screen) waitFor(t *testing.T, text string) {
	t.Helper()
	var last string
	for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		s.mu.Lock()
		last = s.em.String()
		s.mu.Unlock()
		if strings.Contains(last, text) {
			return
		}
	}
	t.Fatalf("%q never appeared; the screen:\n%s", text, last)
}

// Browse the sessions, filter them, plan a continuation in Codex and carry it out.
func TestBrowsePlanAndContinue(t *testing.T) {
	tm := teatest.NewTestModel(t, newModel(t), teatest.WithInitialTermSize(120, 40))
	scr := watch(t, tm, 120, 40)
	scr.waitFor(t, "Find the codeword")

	tm.Type("/")
	tm.Type("Find the")
	tm.Send(tea.KeyPressMsg{Code: tea.KeyEnter})
	tm.Send(tea.KeyPressMsg{Code: tea.KeyEnter}) // plan the session in its own agent: it is already here
	scr.waitFor(t, "already here")
	tm.Type("a") // continue it in the next agent instead
	scr.waitFor(t, "Continue in Codex")
	tm.Type("y")
	scr.waitFor(t, "continues in Codex")

	tm.Type("q")
	fm := tm.FinalModel(t, teatest.WithFinalTimeout(10*time.Second)).(*model)
	if fm.result == nil || fm.plan == nil || fm.plan.Continue == nil {
		t.Fatalf("the continuation did not happen: mode %v, err %v", fm.mode, fm.err)
	}
	if fm.inv != nil {
		fm.inv.Close()
	}
}

// Keys that would carry out a plan wait while a new one is worked out.
func TestNoApplyWhilePlanning(t *testing.T) {
	m := newModel(t)
	m.mode, m.plan, m.planning = modePlan, &move.Plan{}, true
	if _, cmd := m.key("y"); cmd != nil || m.mode != modePlan {
		t.Fatalf("y applied a plan that is being replaced (mode %v)", m.mode)
	}
}

// q leaves the list without doing anything.
func TestQuit(t *testing.T) {
	tm := teatest.NewTestModel(t, newModel(t), teatest.WithInitialTermSize(100, 30))
	watch(t, tm, 100, 30).waitFor(t, "Find the codeword")
	tm.Type("q")
	fm := tm.FinalModel(t, teatest.WithFinalTimeout(10*time.Second)).(*model)
	if fm.exit != nil {
		t.Fatalf("quitting asks for nothing: %+v", fm.exit)
	}
	if fm.inv != nil {
		fm.inv.Close()
	}
}
