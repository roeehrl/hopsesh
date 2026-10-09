package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/roeehrl/hopsesh/internal/app"
	"github.com/roeehrl/hopsesh/internal/core/move"
	"github.com/roeehrl/hopsesh/internal/core/termapp"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

// Terminal steps on the command line: a cloud driver that starts a session only in a
// terminal you answer (claude --cloud "<briefing>") runs in this one, through a relay. You
// see it and answer what it asks (Claude Code's question whether hopsesh's hand-off folder
// is trusted); hopsesh types nothing into it and reads only the session's link it prints.

// stepRunner runs terminal steps here, when standard input is a terminal and so is the
// output that is not the JSON (nil otherwise: a hand-off that needs one is blocked).
func (r *run) stepRunner() move.StepRunner {
	in, ok := r.in.(*os.File)
	if !ok || !term.IsTerminal(int(in.Fd())) {
		return nil
	}
	var out *os.File
	if f, ok := r.out.(*os.File); ok && !r.jsonOut && term.IsTerminal(int(f.Fd())) {
		out = f
	} else if term.IsTerminal(int(os.Stderr.Fd())) {
		out = os.Stderr // standard output is the JSON, or not a terminal
	}
	if out == nil {
		return nil
	}
	return func(_ context.Context, s move.TermStep) (move.StepResult, error) {
		fmt.Fprintf(out, "\n%s starts the session here, in hopsesh's hand-off folder for this repository:\n  %s\n", s.CloudTitle, s.Folder)
		fmt.Fprintf(out, "If it asks whether you trust this folder, answer it. hopsesh only reads the session's link it prints.\n\n")
		return r.app.RunStepHere(s, in, out, func() string { return askLink(in, out, s) })
	}
}

// askLink asks for the session's link when hopsesh saw none (Enter: none).
func askLink(in io.Reader, out io.Writer, s move.TermStep) string {
	fmt.Fprintf(out, "\nhopsesh saw no %s link. If a session started, paste its link (or press Enter to stop): ", s.CloudTitle)
	line, _ := bufio.NewReader(in).ReadString('\n')
	return strings.TrimSpace(line)
}

// terminalStepCmd is the hidden command the app opens a terminal window on: it runs a
// terminal step the app wrote and leaves the outcome for the app.
func terminalStepCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:    "terminal-step <id>",
		Short:  "Run a step the app handed to this terminal (used by the app)",
		Hidden: true,
		Args:   cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			useDirFlags(cmd)
			r, err := newRun(cmd)
			if err != nil {
				return err
			}
			defer r.app.Catalog.Close()
			tio := r.terminalIO(cmd)
			err = r.app.RunStepFile(args[0], tio)
			if tio.Hold {
				return holdUntilReturn(tio, err)
			}
			return err
		},
	}
	cmd.Flags().Bool("hold", false, "keep the tab open when the step ends (a terminal that closes it with its command)")
	addDirFlags(cmd)
	return cmd
}

// terminalOpenCmd is the hidden command a terminal app runs for a launch the app or the
// command line wrote (a ticket): it labels the tab, records it while the agent runs, and
// runs the agent's command.
func terminalOpenCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:    "terminal-open <ticket>",
		Short:  "Run a launch hopsesh handed to this terminal (used by hopsesh)",
		Hidden: true,
		Args:   cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			useDirFlags(cmd)
			r, err := newRun(cmd)
			if err != nil {
				return err
			}
			defer r.app.Catalog.Close()
			return r.app.RunTicket(args[0], r.terminalIO(cmd))
		},
	}
	cmd.Flags().Bool("hold", false, "keep the tab open as a shell when the agent ends (a terminal that closes it with its command)")
	addDirFlags(cmd)
	return cmd
}

// addDirFlags lets the launcher pass its own settings and state folders: a terminal app
// starts the verb without the launcher's environment, so an overridden HOPSESH_CONFIG_DIR
// or HOPSESH_STATE_DIR would otherwise be lost and the ticket not found.
func addDirFlags(cmd *cobra.Command) {
	cmd.Flags().String("config-dir", "", "hopsesh's settings folder (as HOPSESH_CONFIG_DIR)")
	cmd.Flags().String("state-dir", "", "hopsesh's state folder (as HOPSESH_STATE_DIR)")
}

func useDirFlags(cmd *cobra.Command) {
	for flag, env := range map[string]string{"config-dir": "HOPSESH_CONFIG_DIR", "state-dir": "HOPSESH_STATE_DIR"} {
		if v, _ := cmd.Flags().GetString(flag); v != "" {
			_ = os.Setenv(env, v)
		}
	}
}

// terminalIO is this process's terminal for hopsesh's verbs and --run: labels only when
// standard output is a terminal and not the JSON.
func (r *run) terminalIO(cmd *cobra.Command) app.TerminalIO {
	tio := app.TerminalIO{In: os.Stdin, Out: os.Stdout, Err: os.Stderr}
	if f, ok := r.out.(*os.File); ok && !r.jsonOut && term.IsTerminal(int(f.Fd())) {
		tio.Labels = true
	}
	if r.jsonOut {
		tio.Out = os.Stderr // standard output is the JSON
	}
	if f := cmd.Flags().Lookup("hold"); f != nil {
		tio.Hold, _ = cmd.Flags().GetBool("hold")
	}
	return tio
}

// holdUntilReturn keeps a step's tab open until the user presses Return.
func holdUntilReturn(tio app.TerminalIO, err error) error {
	if err != nil {
		fmt.Fprintf(tio.Out, "\nhopsesh: %v\n", err)
	}
	fmt.Fprint(tio.Out, "\nPress Return to close this tab. ")
	_, _ = bufio.NewReader(tio.In).ReadString('\n')
	return nil
}

// runHere runs a driver's command in this terminal and waits for it (a hop's teleport), as
// pull --run does; with --json it writes to standard error.
func (r *run) runHere(_ context.Context, run agent.Command) error {
	if len(run.Argv) == 0 {
		return errors.New("no command to run")
	}
	if !r.jsonOut {
		r.printf("\nRunning %s here; hopsesh hands the copy on when it ends.\n\n", strings.Join(run.Argv, " "))
	}
	return r.runInThisTerminal(app.Launch{Kind: termapp.KindStep, Run: run})
}

// runInThisTerminal runs a launch attached to this terminal (labels when it is one), as
// --run and the terminal UI do; a session is recorded while it runs, so "Show" finds it.
func (r *run) runInThisTerminal(l app.Launch) error {
	tio := app.TerminalIO{In: os.Stdin, Out: os.Stdout, Err: os.Stderr}
	if f, ok := r.out.(*os.File); ok && !r.jsonOut && term.IsTerminal(int(f.Fd())) {
		tio.Labels = true
	}
	if r.jsonOut {
		tio.Out = os.Stderr // standard output is the JSON
	}
	code, err := r.app.RunInThisTerminal(l, tio)
	if err != nil {
		return err
	}
	if code != 0 {
		return fmt.Errorf("%s exited with code %d", filepath.Base(l.Run.Argv[0]), code)
	}
	return nil
}
