package app

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/roeehrl/hopsesh/internal/core/audit"
	"github.com/roeehrl/hopsesh/internal/core/host"
	"github.com/roeehrl/hopsesh/internal/core/move"
	"github.com/roeehrl/hopsesh/internal/core/term"
	"github.com/roeehrl/hopsesh/internal/core/termapp"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

// Terminal steps: a cloud driver's command that starts a session only in a terminal the
// user answers (claude --cloud "<briefing>"). The command line and the terminal UI run it
// in their own terminal through a relay (internal/core/term); the app opens a terminal
// window on `hopsesh terminal-step <id>`, which does the same and leaves the outcome in a
// file the app waits for. Either way the user answers what the driver asks, hopsesh never
// types into it, and only the module's reader looks at what it printed: no output is
// kept, logged or journaled.

// StepRelay is the relay that runs a terminal step in a terminal: the driver's argument
// list in its folder, with this process's environment less the cloud's Unset and plus the
// step's Env, and a capture for the module's reader.
func StepRelay(s move.TermStep) *term.Relay {
	env := append(host.Without(os.Environ(), s.Run.Unset), s.Run.Env...)
	r := term.New(s.Run.Argv, s.Run.Dir, env)
	r.Capture = &term.Capture{}
	return r
}

// ReadStep reads the session a terminal step started from what its relay saw, through the
// module (agent.CloudStepReader), and forgets the output.
func (a *App) ReadStep(s move.TermStep, r *term.Relay) (agent.CloudSession, error) {
	out := agent.StepOutput{Width: r.Cols, Code: r.Code}
	if r.Capture != nil {
		out.Text = r.Capture.Text()
		r.Capture.Reset()
	}
	mod, ok := a.Module(s.Agent)
	if !ok {
		return agent.CloudSession{}, fmt.Errorf("%s is not enabled", s.Agent)
	}
	rd, ok := mod.(agent.CloudStepReader)
	if !ok {
		return agent.CloudSession{}, fmt.Errorf("%w: %s cannot read what its driver printed", agent.ErrUnsupported, mod.Spec().Name)
	}
	return rd.ReadStep(s.Cloud, out)
}

// PastedStep is the session a link the user pasted names, for a terminal step whose output
// named none (or that ran where hopsesh could not watch it).
func (a *App) PastedStep(s move.TermStep, link string) (agent.CloudSession, error) {
	mod, cl, id, ok := a.ParseCloudLink(link)
	if !ok || cl != s.Cloud || mod.Spec().ID != s.Agent {
		return agent.CloudSession{}, fmt.Errorf("%q is not a link to a %s session", strings.TrimSpace(link), s.CloudTitle)
	}
	cs := agent.CloudSession{Key: agent.SessionKey{Agent: s.Agent, Session: id}, Cloud: cl, State: agent.CloudRunning, Updated: time.Now().UTC()}
	if l, ok := mod.(agent.CloudLinker); ok {
		cs.URL = l.CloudURL(cl, id)
	}
	return cs, nil
}

// RunStepHere runs a terminal step in this process's terminal (in, out) through a relay,
// and reads the session from what it printed. Where this system has no pseudo-terminal, the
// driver runs on the terminal directly, unwatched, and the session's link comes from the
// user (ask; nil: no session).
func (a *App) RunStepHere(s move.TermStep, in io.Reader, out io.Writer, ask func() string) (move.StepResult, error) {
	r := StepRelay(s)
	r.SetStdin(in)
	r.SetStdout(out)
	err := r.Run()
	if errors.Is(err, term.ErrNoPseudoTerminal) {
		return a.runUnwatched(s, in, out, ask)
	}
	if err != nil {
		return move.StepResult{}, err
	}
	return a.afterStep(s, r, ask)
}

// afterStep reads what a relayed step printed; with no session in it, it asks the user
// for the link (ask returns "" for none).
func (a *App) afterStep(s move.TermStep, r *term.Relay, ask func() string) (move.StepResult, error) {
	cs, err := a.ReadStep(s, r)
	if errors.Is(err, agent.ErrNoSession) && ask != nil {
		if link := strings.TrimSpace(ask()); link != "" {
			cs, perr := a.PastedStep(s, link)
			if perr != nil {
				return move.StepResult{}, perr
			}
			return move.StepResult{Session: cs, Pasted: true}, nil
		}
	}
	if err != nil {
		return move.StepResult{}, err
	}
	return move.StepResult{Session: cs}, nil
}

// runUnwatched runs a step's driver on the terminal itself (no pseudo-terminal here), and
// asks for the link it printed.
func (a *App) runUnwatched(s move.TermStep, in io.Reader, out io.Writer, ask func() string) (move.StepResult, error) {
	c := exec.Command(s.Run.Argv[0], s.Run.Argv[1:]...) //nolint:gosec // the module's driver, by its argument list
	c.Dir, c.Env = s.Run.Dir, append(host.Without(os.Environ(), s.Run.Unset), s.Run.Env...)
	c.Stdin, c.Stdout, c.Stderr = in, out, out
	_ = c.Run()
	if ask != nil {
		if link := strings.TrimSpace(ask()); link != "" {
			cs, err := a.PastedStep(s, link)
			if err != nil {
				return move.StepResult{}, err
			}
			return move.StepResult{Session: cs, Pasted: true}, nil
		}
	}
	return move.StepResult{}, fmt.Errorf("%w: hopsesh could not watch %s here, and no link was pasted", agent.ErrNoSession, s.Run.Argv[0])
}

// A terminal step handed to `hopsesh terminal-step <id>`: the app writes it, the command
// runs it and writes its outcome beside it, the app reads that and removes both.

// stepID is what a step's id looks like.
var stepID = regexp.MustCompile(`^[0-9a-f]{16}$`)

// maxStepAge is how long a written step may wait for its terminal.
const maxStepAge = time.Hour

type stepFile struct {
	Step move.TermStep `json:"step"`
	Made time.Time     `json:"made"`
}

// StepOutcome is how a step handed to a terminal ended: the session it started, or what
// stopped it (NoSession: it ended without a session or a refusal, so a pasted link may
// still come).
type StepOutcome struct {
	Session   *agent.CloudSession `json:"session,omitempty"`
	Error     string              `json:"error,omitempty"`
	NoSession bool                `json:"noSession,omitempty"`
	Code      int                 `json:"code"`
}

func (a *App) stepPath(id, suffix string) string {
	return filepath.Join(a.StateDir, "steps", id+suffix)
}

// SaveStep writes a terminal step for `hopsesh terminal-step <id>` and returns its id.
func (a *App) SaveStep(s move.TermStep) (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	id := hex.EncodeToString(b)
	data, err := json.Marshal(stepFile{Step: s, Made: time.Now().UTC()})
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Join(a.StateDir, "steps"), 0o700); err != nil {
		return "", err
	}
	return id, os.WriteFile(a.stepPath(id, ".json"), data, 0o600)
}

// StepOutcomeOf is a written step's outcome once its terminal wrote one (nil until then).
func (a *App) StepOutcomeOf(id string) (*StepOutcome, error) {
	if !stepID.MatchString(id) {
		return nil, errors.New("not a step")
	}
	b, err := os.ReadFile(a.stepPath(id, ".done.json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var o StepOutcome
	if err := json.Unmarshal(b, &o); err != nil {
		return nil, err
	}
	return &o, nil
}

// DropStep removes a written step and its outcome.
func (a *App) DropStep(id string) {
	if stepID.MatchString(id) {
		_ = os.Remove(a.stepPath(id, ".json"))
		_ = os.Remove(a.stepPath(id, ".done.json"))
	}
}

// loadStep reads a written step, and refuses one that is old, or that would run anything
// but its cloud's driver in a hand-off folder of hopsesh's (a step file is hopsesh's own,
// in its private state folder; this is a second check).
func (a *App) loadStep(id string) (move.TermStep, error) {
	if !stepID.MatchString(id) {
		return move.TermStep{}, errors.New("not a step id")
	}
	b, err := os.ReadFile(a.stepPath(id, ".json"))
	if err != nil {
		return move.TermStep{}, fmt.Errorf("the step is gone (it ran already, or the app stopped waiting): %w", err)
	}
	var f stepFile
	if err := json.Unmarshal(b, &f); err != nil {
		return move.TermStep{}, err
	}
	s := f.Step
	if time.Since(f.Made) > maxStepAge {
		return s, errors.New("the step is more than an hour old; start the hand-off again")
	}
	mod, ok := a.Module(s.Agent)
	if !ok {
		return s, fmt.Errorf("%s is not enabled", s.Agent)
	}
	cl, ok := mod.Spec().FindCloud(s.Cloud)
	if !ok || len(s.Run.Argv) == 0 {
		return s, fmt.Errorf("%s has no cloud %s", mod.Spec().Name, s.Cloud)
	}
	if base := strings.TrimSuffix(strings.ToLower(filepath.Base(s.Run.Argv[0])), ".exe"); base != cl.Driver {
		return s, fmt.Errorf("the step runs %s, not %s", s.Run.Argv[0], cl.Driver)
	}
	if rel, err := filepath.Rel(filepath.Join(a.StateDir, "handoff"), s.Run.Dir); err != nil || strings.HasPrefix(rel, "..") {
		return s, fmt.Errorf("the step runs outside hopsesh's hand-off folders (%s)", s.Run.Dir)
	}
	return s, nil
}

// RunStepFile runs a written step in this terminal (the hidden `hopsesh terminal-step
// <id>`) and writes its outcome for the app: the session's id and link, or what stopped
// it. It says in the terminal what is going on, before and after, and labels the tab
// (tio.Labels) while the step runs.
func (a *App) RunStepFile(id string, tio TerminalIO) error {
	in, out := tio.In, tio.Out
	s, err := a.loadStep(id)
	if err != nil {
		if stepID.MatchString(id) {
			_ = a.writeOutcome(id, StepOutcome{Code: -1, Error: "hopsesh could not run the step: " + err.Error()})
		}
		return err
	}
	_ = os.Remove(a.stepPath(id, ".json")) // runs once
	if tio.Labels {
		_, _ = out.Write(termapp.Sequences(termapp.Labels{Title: s.Title, Agent: s.CloudTitle, Machine: LocalName()}, tio.getenv))
		defer func() { _, _ = out.Write(termapp.ClearSequences(tio.getenv)) }()
	}
	fmt.Fprintf(out, "hopsesh: starting the %s session for “%s” with %s.\n", s.CloudTitle, s.Title, filepath.Base(s.Run.Argv[0]))
	fmt.Fprintf(out, "It runs in hopsesh's hand-off folder for this repository:\n  %s\n", s.Folder)
	fmt.Fprintf(out, "If it asks whether you trust this folder, answer it here. hopsesh only reads the session's link it prints.\n\n")
	o := StepOutcome{Code: -1}
	r := StepRelay(s)
	r.SetStdin(in)
	r.SetStdout(out)
	err = r.Run()
	switch {
	case errors.Is(err, term.ErrNoPseudoTerminal):
		// Unwatched: the user pastes the link in the app.
		c := exec.Command(s.Run.Argv[0], s.Run.Argv[1:]...) //nolint:gosec // the module's driver, checked above
		c.Dir, c.Env = s.Run.Dir, append(host.Without(os.Environ(), s.Run.Unset), s.Run.Env...)
		c.Stdin, c.Stdout, c.Stderr = in, out, out
		_ = c.Run()
		o.NoSession, o.Error = true, "hopsesh could not watch "+filepath.Base(s.Run.Argv[0])+" here"
	case err != nil:
		o.Error = err.Error()
	default:
		o.Code = r.Code
		cs, rerr := a.ReadStep(s, r)
		switch {
		case rerr == nil:
			o.Session = &cs
		case errors.Is(rerr, agent.ErrNoSession):
			o.NoSession, o.Error = true, strings.TrimPrefix(rerr.Error(), agent.ErrNoSession.Error()+": ")
		default:
			o.Error = rerr.Error()
		}
	}
	a.Audit.Write(audit.Entry{Action: "cloud.step", Detail: map[string]any{"cloud": s.Cloud, "code": o.Code, "session": o.Session != nil}})
	if err := a.writeOutcome(id, o); err != nil {
		return err
	}
	fmt.Fprintln(out)
	switch {
	case o.Session != nil:
		fmt.Fprintf(out, "✓ hopsesh has the session's link: %s\n  You can close this window; the app shows the rest.\n", o.Session.URL)
	case o.NoSession:
		fmt.Fprintf(out, "✗ No session started: %s.\n  If one did, paste its link in the app; otherwise undo the hand-off there.\n", strings.TrimSuffix(o.Error, "."))
	default:
		fmt.Fprintf(out, "✗ %s\n  The app shows what happened and offers Undo.\n", o.Error)
	}
	return nil
}

// writeOutcome leaves a step's outcome for the app, whole or not at all.
func (a *App) writeOutcome(id string, o StepOutcome) error {
	b, err := json.Marshal(o)
	if err != nil {
		return err
	}
	tmp := a.stepPath(id, ".done.json.tmp")
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, a.stepPath(id, ".done.json"))
}
