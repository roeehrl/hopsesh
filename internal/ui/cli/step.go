package cli

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/roeehrl/hopsesh/internal/core/move"
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
	return &cobra.Command{
		Use:    "terminal-step <id>",
		Short:  "Run a step the app handed to this terminal (used by the app)",
		Hidden: true,
		Args:   cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			r, err := newRun(cmd)
			if err != nil {
				return err
			}
			return r.app.RunStepFile(args[0], os.Stdin, os.Stdout)
		},
	}
}
