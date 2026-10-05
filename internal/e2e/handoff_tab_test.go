package e2e

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/roeehrl/hopsesh/internal/app"
	"github.com/roeehrl/hopsesh/internal/core/move"
	"github.com/roeehrl/hopsesh/internal/core/pty"
	"github.com/roeehrl/hopsesh/internal/core/pty/ptytest"
)

// tabRunner runs a hand-off's terminal step in an app terminal tab (internal/core/pty)
// instead of a relay: the tab's window is ptytest's, whose emulator answers the program's
// queries, and the user types the answer to the trust question there. hopsesh reads the
// session's link through the same module reader as for the relay.
func (u *atTerminal) tabRunner(a *app.App, m *pty.Manager) move.StepRunner {
	return func(ctx context.Context, s move.TermStep) (move.StepResult, error) {
		u.mu.Lock()
		u.steps++
		answer := u.answer
		u.mu.Unlock()
		sess, err := m.Start(app.StepTab(s))
		if err != nil {
			return move.StepResult{}, err
		}
		find := func(id string) (*pty.Session, error) {
			if t, ok := m.Get(id); ok {
				return t, nil
			}
			return nil, errors.New("no such tab")
		}
		w := ptytest.Open(find, nil, sess.ID(), ptytest.Options{Cols: 100, Rows: 30})
		defer w.Detach()
		watched := make(chan struct{})
		go func() { // the user, reading the tab
			defer close(watched)
			for {
				select {
				case <-sess.Done():
					return
				case <-time.After(30 * time.Millisecond):
				}
				if strings.Contains(w.Screen(), "Quick safety check") {
					u.mu.Lock()
					u.asked++
					u.mu.Unlock()
					w.Type(answer)
					return
				}
			}
		}()
		res, err := a.AwaitStep(ctx, s, sess)
		<-watched
		u.mu.Lock()
		u.last = w.Screen()
		u.mu.Unlock()
		_ = m.Close(sess.ID())
		return res, err
	}
}

// The hand-off's claude --cloud step in an app tab: the stand-in claude sees a terminal,
// asks whether the folder is trusted, the user answers in the tab, and hopsesh reads the
// session's link from the tab's capture with the module's reader; a refusal reads as no
// session; the tab is gone afterwards.
func TestHandoffInAppTab(t *testing.T) {
	ctx := context.Background()
	m := pty.NewManager(pty.Options{Version: "test"})
	t.Cleanup(m.CloseAll)

	w := newHandoffWorld(t)
	a := w.cloudWorld.app()
	a.Steps = w.user.tabRunner(a, m)
	inv, p := w.planHandoff(a, a.HandoffDefaults("claude-cloud"))
	res, err := a.Apply(ctx, p, move.Input{}, nil)
	inv.Close()
	if err != nil || !strings.HasPrefix(res.Handoff.Session, "session_01") || res.Handoff.Pasted {
		t.Fatalf("hand-off in a tab: %v %+v\n%s", err, res.Handoff, w.user.last)
	}
	if w.user.asked != 1 || !strings.Contains(w.user.last, "View: https://claude.ai/code/"+res.Handoff.Session) {
		t.Fatalf("asked %d times; the tab showed:\n%s", w.user.asked, w.user.last)
	}
	if len(m.List()) != 0 {
		t.Fatalf("tabs left: %+v", m.List())
	}

	w2 := newHandoffWorld(t)
	w2.user.answer = "2"
	a2 := w2.cloudWorld.app()
	a2.Steps = w2.user.tabRunner(a2, m)
	inv, p = w2.planHandoff(a2, a2.HandoffDefaults("claude-cloud"))
	defer inv.Close()
	res, err = a2.Apply(ctx, p, move.Input{}, nil)
	if err == nil || res.Handoff.Failed != move.StepStart || !strings.Contains(err.Error(), "asked whether you trust") {
		t.Fatalf("refused in a tab: %v %+v", err, res.Handoff)
	}
	if os.Getenv("ANTHROPIC_API_KEY") == "" {
		t.Fatal("the world lost its API key variable; the step's Unset is untested")
	}
}
