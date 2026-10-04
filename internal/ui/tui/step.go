package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/roeehrl/hopsesh/internal/app"
	"github.com/roeehrl/hopsesh/internal/core/host"
	"github.com/roeehrl/hopsesh/internal/core/move"
	"github.com/roeehrl/hopsesh/internal/core/term"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

// Terminal steps in the terminal UI: a cloud driver that starts a session only in a
// terminal you answer (claude --cloud "<briefing>") gets this terminal for as long as it
// runs (tea.Exec pauses the UI and resumes it afterwards). You answer what it asks;
// hopsesh types nothing into it and reads only the session's link it prints. When it
// printed none, the UI asks for the link.

// stepRun asks the UI, from the hand-off running in the background, to run a step; the
// hand-off waits for the reply.
type stepRun struct {
	s     move.TermStep
	reply chan stepReply
}

type stepReply struct {
	res move.StepResult
	err error
}

// stepRan is a step's end: the session read from what it printed, or why there is none.
type stepRan struct {
	run       stepRun
	cs        agent.CloudSession
	err       error
	unwatched bool // no pseudo-terminal here: run it on the terminal itself
}

// stepPaste is a step that ended without a session the UI could see: the user may paste
// its link.
type stepPaste struct {
	run  stepRun
	why  error
	text string
	err  string
}

// stepper is the App's step runner while the UI runs: it hands the step to the UI and
// waits for its end.
func stepper(send func(tea.Msg)) move.StepRunner {
	return func(ctx context.Context, s move.TermStep) (move.StepResult, error) {
		reply := make(chan stepReply, 1)
		send(stepRun{s: s, reply: reply})
		select {
		case r := <-reply:
			return r.res, r.err
		case <-ctx.Done():
			return move.StepResult{}, ctx.Err()
		}
	}
}

// runStep gives the terminal to the step's relay until the driver ends.
func (m *model) runStep(msg stepRun) (tea.Model, tea.Cmd) {
	return m, tea.Exec(m.stepExec(msg))
}

// stepExec is the relay for a step and what it reports once it ran.
func (m *model) stepExec(msg stepRun) (*term.Relay, tea.ExecCallback) {
	a := m.deps.App
	r := app.StepRelay(msg.s)
	return r, func(err error) tea.Msg {
		switch {
		case errors.Is(err, term.ErrNoPseudoTerminal):
			return stepRan{run: msg, unwatched: true}
		case err != nil:
			return stepRan{run: msg, err: err}
		}
		cs, err := a.ReadStep(msg.s, r)
		return stepRan{run: msg, cs: cs, err: err}
	}
}

// stepDone answers the hand-off, or asks for the link when the step showed none.
func (m *model) stepDone(msg stepRan) (tea.Model, tea.Cmd) {
	if msg.unwatched {
		s := msg.run.s
		c := exec.Command(s.Run.Argv[0], s.Run.Argv[1:]...) //nolint:gosec // the module's driver, by its argument list
		c.Dir, c.Env = s.Run.Dir, append(host.Without(os.Environ(), s.Run.Unset), s.Run.Env...)
		return m, tea.ExecProcess(c, func(error) tea.Msg {
			return stepRan{run: msg.run, err: fmt.Errorf("%w: hopsesh could not watch %s here", agent.ErrNoSession, s.Run.Argv[0])}
		})
	}
	if errors.Is(msg.err, agent.ErrNoSession) {
		m.stepPaste = &stepPaste{run: msg.run, why: msg.err}
		return m, nil
	}
	msg.run.reply <- stepReply{res: move.StepResult{Session: msg.cs}, err: msg.err}
	return m, nil
}

// stepPasteKey edits the pasted link: enter uses it, esc ends the hand-off without one.
func (m *model) stepPasteKey(k string) (tea.Model, tea.Cmd) {
	sp := m.stepPaste
	switch k {
	case "esc", "ctrl+c":
		m.stepPaste = nil
		sp.run.reply <- stepReply{err: sp.why}
	case "enter":
		if strings.TrimSpace(sp.text) == "" {
			m.stepPaste = nil
			sp.run.reply <- stepReply{err: sp.why}
			return m, nil
		}
		cs, err := m.deps.App.PastedStep(sp.run.s, sp.text)
		if err != nil {
			sp.err = err.Error()
			return m, nil
		}
		m.stepPaste = nil
		sp.run.reply <- stepReply{res: move.StepResult{Session: cs, Pasted: true}}
	case "backspace":
		if r := []rune(sp.text); len(r) > 0 {
			sp.text = string(r[:len(r)-1])
		}
	default:
		if len([]rune(k)) == 1 {
			sp.text += k
		}
	}
	return m, nil
}

// viewStepPaste asks for the link of a session the step did not show.
func (m *model) viewStepPaste(b *strings.Builder) {
	sp := m.stepPaste
	why := strings.TrimPrefix(sp.why.Error(), agent.ErrNoSession.Error()+": ")
	fmt.Fprintf(b, "\n  %s %s\n", warnSt.Render("!"), bold.Render("hopsesh saw no "+sp.run.s.CloudTitle+" link"))
	fmt.Fprintf(b, "   %s.\n", strings.TrimSuffix(why, "."))
	b.WriteString("   If a session started, paste its link and press enter. Esc stops here: the hand-off then says what to undo.\n\n")
	fmt.Fprintf(b, "   link  %s▏\n", sp.text)
	if sp.err != "" {
		b.WriteString("   " + errSt.Render(sp.err) + "\n")
	}
}
