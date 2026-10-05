package gui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/roeehrl/hopsesh/internal/app"
	"github.com/roeehrl/hopsesh/internal/core/integrate"
	"github.com/roeehrl/hopsesh/internal/core/move"
	"github.com/roeehrl/hopsesh/internal/core/pty"
	"github.com/roeehrl/hopsesh/internal/core/termapp"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

// A hand-off whose cloud driver starts the session only in a terminal the user answers
// (claude --cloud "<briefing>"). By default the driver runs in a tab of the hopsesh
// Terminal window (app.StepTab): the user answers it there, and once it ends the module's
// reader looks at what it printed (app.AwaitStep). With the user's terminal app chosen
// (Settings → Terminal), or on "Open in my terminal" in the tab, the window opens that app
// on `hopsesh terminal-step <id>`, a line hopsesh builds itself, which runs the driver
// through the relay there and leaves the outcome in a file. The hand-off waits for the
// tab or that file, or for a link the user pastes (when hopsesh saw none, or no terminal
// could be opened), or for the user to stop waiting. The window polls HandoffStep to show
// where it stands.

// stepWait is how long a hand-off waits for its terminal step.
const stepWait = 25 * time.Minute

// HandoffStepDTO is the terminal step a hand-off waits for, as the window shows it.
type HandoffStepDTO struct {
	// State: waiting (for the terminal), no-link (it ended without a session hopsesh saw),
	// no-terminal (the window could not open one).
	State string `json:"state"`
	// Where it runs: "here" (a tab of the hopsesh Terminal window, Tab) or "terminal" (the
	// user's terminal app).
	Where      string `json:"where"`
	Tab        string `json:"tab,omitempty"`
	CloudTitle string `json:"cloudTitle"`
	Driver     string `json:"driver"` // "claude"
	Folder     string `json:"folder"`
	Message    string `json:"message,omitempty"`
}

// pendingStep is the terminal step the current hand-off waits for.
type pendingStep struct {
	dto   HandoffStepDTO
	s     move.TermStep
	paste chan agent.CloudSession
	stop  chan struct{}
	out   chan struct{} // the tab's "Open in my terminal"
}

// stepDone is how a step's tab ended: what the module's reader made of its output.
type stepDone struct {
	tab string
	res move.StepResult
	err error
}

// stepProgram is the hopsesh program the terminal runs: the command-line tool inside the
// app (hopsesh.exe beside it on Windows), else this program. SetStepProgram replaces it.
var stepProgram = func() (string, error) {
	if cli, err := integrate.AppCLI(); err == nil {
		return cli, nil
	}
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	if b := strings.ToLower(filepath.Base(exe)); strings.HasPrefix(b, "hopsesh-app") {
		return "", errors.New("the hopsesh command-line tool is missing from the app; reinstall it")
	}
	return exe, nil
}

// SetStepProgram sets the program the terminal runs for a step (the browser tests: their
// own, which carries the command line).
func SetStepProgram(path string) { stepProgram = func() (string, error) { return path, nil } }

// runStep is the window's step runner (app.App.Steps).
func (a *App) runStep(ctx context.Context, s move.TermStep) (move.StepResult, error) {
	core := a.snapshot()
	ps := &pendingStep{s: s, paste: make(chan agent.CloudSession, 1), stop: make(chan struct{}),
		dto: HandoffStepDTO{State: "waiting", CloudTitle: s.CloudTitle, Driver: strings.TrimSuffix(filepath.Base(s.Run.Argv[0]), ".exe"), Folder: s.Folder}}
	a.mu.Lock()
	a.step = ps
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		if a.step == ps {
			a.step = nil
		}
		a.mu.Unlock()
	}()
	set := func(state, msg string) {
		a.mu.Lock()
		ps.dto.State, ps.dto.Message = state, msg
		a.mu.Unlock()
	}
	id, err := core.SaveStep(s)
	if err != nil {
		return move.StepResult{}, err
	}
	defer core.DropStep(id)
	ctx, cancel := context.WithTimeout(ctx, stepWait)
	defer cancel()
	ps.out = make(chan struct{}, 1)
	done := make(chan stepDone, 1)
	tab := ""
	// Where the user's terminal can say so (iTerm2 with its Python API on), a tab closed
	// before the step wrote its outcome ends the wait at once instead of after 25 minutes.
	var exited <-chan int
	external := func() {
		a.mu.Lock()
		ps.dto.Where, ps.dto.Tab = WhereTerminal, ""
		a.mu.Unlock()
		opened, err := a.openStep(id, s)
		if err != nil {
			set("no-terminal", "hopsesh could not open a terminal: "+err.Error()+". Stop waiting and hand it off from the command line (hopsesh handoff … --to "+s.Cloud+") instead.")
			return
		}
		exited, _ = core.WatchLaunch(ctx, opened.Handle)
	}
	if route("", core.Cfg.AppResume(), true) == WhereHere && a.Terms != nil {
		if tab, err = a.stepTab(ctx, s, ps, done); err != nil {
			a.fellBack(err)
			external()
		}
	} else {
		external()
	}
	defer func() {
		// A step left behind (the user stopped waiting, or the wait ran out) is ended: the
		// cloud session it would start is no longer awaited.
		if tab != "" {
			if t, ok := a.Terms.Manager().Get(tab); ok && t.Info().State != pty.Exited {
				_ = a.Terms.close(tab)
			}
		}
	}()
	tick := time.NewTicker(400 * time.Millisecond)
	defer tick.Stop()
	var noSession error
	for {
		select {
		case cs := <-ps.paste:
			return move.StepResult{Session: cs, Pasted: true}, nil
		case <-ps.stop:
			if noSession != nil {
				return move.StepResult{}, noSession
			}
			return move.StepResult{}, fmt.Errorf("%w: you stopped waiting for the terminal", agent.ErrNoSession)
		case <-ctx.Done():
			return move.StepResult{}, fmt.Errorf("%w: hopsesh stopped waiting for the terminal after 25 minutes", agent.ErrNoSession)
		case <-ps.out:
			// "Open in my terminal": the tab ends, and the step starts again there (nothing
			// exists in the cloud until the user answers).
			if tab != "" {
				_ = a.Terms.close(tab)
				tab = ""
			}
			external()
		case d := <-done:
			if d.tab != tab {
				continue // a tab the step left for the user's terminal app
			}
			switch {
			case d.err == nil:
				if d.res.Session.URL != "" {
					a.Terms.setMeta(tab, func(m *TabMeta) { m.Link = d.res.Session.URL })
				}
				return d.res, nil
			case errors.Is(d.err, agent.ErrNoSession):
				noSession = d.err
				set("no-link", strings.TrimPrefix(d.err.Error(), agent.ErrNoSession.Error()+": "))
			default:
				return move.StepResult{}, d.err
			}
		case _, ok := <-exited:
			exited = nil
			if !ok || noSession != nil {
				continue
			}
			o, err := core.StepOutcomeOf(id)
			if err == nil && o == nil {
				noSession = fmt.Errorf("%w: the terminal tab was closed before %s finished", agent.ErrNoSession, s.CloudTitle)
				set("no-link", "The terminal tab was closed before "+s.CloudTitle+" finished. If a session started, paste its link; otherwise stop waiting and undo.")
				continue
			}
			if res, fin, err := stepOutcome(o, err, &noSession, set); fin {
				return res, err
			}
		case <-tick.C:
			if noSession != nil || tab != "" {
				continue
			}
			o, err := core.StepOutcomeOf(id)
			if res, fin, err := stepOutcome(o, err, &noSession, set); fin {
				return res, err
			}
		}
	}
}

// stepTab runs a terminal step in a tab of the hopsesh Terminal window and returns the
// tab; done gets what the module's reader made of its output once it ended.
func (a *App) stepTab(ctx context.Context, s move.TermStep, ps *pendingStep, done chan<- stepDone) (string, error) {
	base := app.StepTab(s)
	spec, err := a.tabSpec(s.Run, tabTitle("Hand off", s.Title))
	if err != nil {
		return "", err
	}
	spec.Env.Set = append(spec.Env.Set, base.Env.Set...)
	spec.Capture = base.Capture
	name := s.CloudTitle
	if m, ok := a.snapshot().Module(s.Agent); ok {
		name = m.Spec().Name
	}
	info, err := a.Terms.Open(spec, TabSetup{
		Meta: TabMeta{Kind: TabStep, Command: displayCommand(s.Run.Argv), Agent: name, Cloud: s.Cloud, CloudTitle: s.CloudTitle, External: true},
		External: func(string) error {
			select {
			case ps.out <- struct{}{}:
			default:
			}
			return nil
		},
	})
	if err != nil {
		return "", err
	}
	sess, ok := a.Terms.Manager().Get(info.ID)
	if !ok {
		return "", errors.New("the step's tab closed at once")
	}
	a.mu.Lock()
	ps.dto.Where, ps.dto.Tab = WhereHere, info.ID
	a.mu.Unlock()
	go func() {
		res, err := a.snapshot().AwaitStep(ctx, s, sess)
		done <- stepDone{tab: info.ID, res: res, err: err}
	}()
	return info.ID, nil
}

// openStep opens the user's terminal on the step: a line hopsesh built, never one taken
// from the window.
func (a *App) openStep(id string, s move.TermStep) (termapp.Opened, error) {
	prog, err := stepProgram()
	if err != nil {
		return termapp.Opened{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	o, err := a.snapshot().OpenStepInTerminal(ctx, prog, testTerminal, id, s.Run.Dir)
	if err != nil {
		return o, err
	}
	a.noteOpened(o)
	return o, nil
}

// stepOutcome is what a step's written outcome means for the wait: a result (done), or
// that it ended without a session (noSession set: a pasted link may still come), or
// nothing yet.
func stepOutcome(o *app.StepOutcome, err error, noSession *error, set func(state, msg string)) (move.StepResult, bool, error) {
	switch {
	case err != nil:
		return move.StepResult{}, true, err
	case o == nil:
	case o.Session != nil:
		return move.StepResult{Session: *o.Session}, true, nil
	case o.NoSession:
		*noSession = fmt.Errorf("%w: %s", agent.ErrNoSession, o.Error)
		set("no-link", o.Error)
	default:
		return move.StepResult{}, true, errors.New(o.Error)
	}
	return move.StepResult{}, false, nil
}

// HandoffStep is the terminal step the hand-off being applied waits for (nil: none).
func (a *App) HandoffStep() *HandoffStepDTO {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.step == nil {
		return nil
	}
	d := a.step.dto
	return &d
}

// HandoffPasteLink gives the waiting hand-off the session's link the user pasted (the one
// Claude Code printed in the terminal).
func (a *App) HandoffPasteLink(link string) error {
	a.mu.Lock()
	ps := a.step
	a.mu.Unlock()
	if ps == nil {
		return errors.New("no hand-off is waiting for a link")
	}
	cs, err := a.snapshot().PastedStep(ps.s, link)
	if err != nil {
		return err
	}
	select {
	case ps.paste <- cs:
		return nil
	default:
		return errors.New("a link was given already")
	}
}

// HandoffStopWaiting ends the wait: the hand-off stops at its start step, and Undo removes
// what it did.
func (a *App) HandoffStopWaiting() {
	a.mu.Lock()
	ps := a.step
	a.mu.Unlock()
	if ps != nil {
		select {
		case <-ps.stop:
		default:
			close(ps.stop)
		}
	}
}
