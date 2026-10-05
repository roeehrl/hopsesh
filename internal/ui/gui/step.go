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
	"github.com/roeehrl/hopsesh/internal/core/termapp"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

// A hand-off whose cloud driver starts the session only in a terminal the user answers
// (claude --cloud "<briefing>"): the window opens the user's terminal on `hopsesh
// terminal-step <id>`, a line hopsesh builds itself, which runs the driver through the
// relay there and leaves the outcome in a file. The hand-off waits for that file, or for a
// link the user pastes (when hopsesh saw none, or the terminal could not be opened), or
// for the user to stop waiting. The window polls HandoffStep to show where it stands.

// stepWait is how long a hand-off waits for its terminal step.
const stepWait = 25 * time.Minute

// HandoffStepDTO is the terminal step a hand-off waits for, as the window shows it.
type HandoffStepDTO struct {
	// State: waiting (for the terminal), no-link (it ended without a session hopsesh saw),
	// no-terminal (the window could not open one).
	State      string `json:"state"`
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
	opened, err := a.openStep(id, s)
	if err != nil {
		set("no-terminal", "hopsesh could not open a terminal: "+err.Error()+". Stop waiting and hand it off from the command line (hopsesh handoff … --to "+s.Cloud+") instead.")
	}
	ctx, cancel := context.WithTimeout(ctx, stepWait)
	defer cancel()
	// Where the terminal can say so (iTerm2 with its Python API on), a tab closed before
	// the step wrote its outcome ends the wait at once instead of after 25 minutes.
	var exited <-chan int
	if err == nil {
		exited, _ = core.WatchLaunch(ctx, opened.Handle)
	}
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
			if res, done, err := stepOutcome(o, err, &noSession, set); done {
				return res, err
			}
		case <-tick.C:
			if noSession != nil {
				continue
			}
			o, err := core.StepOutcomeOf(id)
			if res, done, err := stepOutcome(o, err, &noSession, set); done {
				return res, err
			}
		}
	}
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
