package cli

import (
	"errors"
	"fmt"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/roeehrl/hopsesh/internal/core/journal"
)

func undoCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "undo [<journal-id or session-id>]",
		Short: "Undo a move or continuation (the newest one, or the one named)",
		Long: `Reverses everything a move, continuation or mark wrote: new files are removed, replaced
files come back, appended records are cut off, set-aside copies return. Writes on other
machines are undone over SSH. Clones and worktrees are kept. --list shows what can be undone.

A move whose session was used afterwards is not undone, since that later work would be
lost; --force undoes it anyway.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			r, err := newRun(cmd)
			if err != nil {
				return err
			}
			if list, _ := cmd.Flags().GetBool("list"); list {
				acts, err := r.app.Activities()
				if err != nil {
					return err
				}
				if r.jsonOut {
					return r.emitJSON(acts)
				}
				tw := tabwriter.NewWriter(r.out, 0, 2, 2, ' ', 0)
				fmt.Fprintln(tw, "ID\tWHEN\tWHAT\tSTATE")
				for _, a := range acts {
					state := "can undo"
					if !a.CanUndo {
						state = a.Why
					}
					fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", a.Journal.ID, a.Journal.Time.Local().Format("2 Jan 15:04"), a.Journal.Title, state)
				}
				return tw.Flush()
			}
			match := ""
			if len(args) == 1 {
				match = args[0]
			}
			if !r.confirm(fmt.Sprintf("Undo %s?", nonEmpty(match, "the newest move"))) {
				return fmt.Errorf("not confirmed: run interactively or pass --yes")
			}
			ctx, cancel := ctxTimeout(5)
			defer cancel()
			force, _ := cmd.Flags().GetBool("force")
			j, err := r.app.Undo(ctx, match, force)
			if errors.Is(err, journal.ErrChanged) {
				return fmt.Errorf("%w\nUndoing now would lose that later work; hopsesh undo --force does it anyway", err)
			}
			if err != nil {
				return err
			}
			r.printf("Undid %q (%d change(s)).\n", j.Title, len(j.Entries))
			return nil
		},
	}
	cmd.Flags().Bool("list", false, "list what can be undone")
	cmd.Flags().Bool("yes", false, "do not ask for confirmation")
	cmd.Flags().Bool("force", false, "undo even when the session was used since (that later work is lost)")
	cmd.Flags().Bool("json", false, "output JSON (with --list)")
	return cmd
}
