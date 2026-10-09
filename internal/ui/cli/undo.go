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
		Short: "Undo a move, continuation or hand-off (the newest one, or the one named)",
		Long: `Reverses everything a move, continuation or mark wrote: new files are removed, replaced
files come back, appended records are cut off, set-aside copies return. Writes on other
machines are undone over SSH. Clones and worktrees are kept. A hand-off's branch is deleted
(only while it is as hopsesh pushed it) and its mark taken off; the cloud session stays in
the cloud, to archive there (with delete_branch = never the branch stays too). A hop from
one cloud to another is undone as a whole, its hand-off first; a branch clean-up pushes the
branches back. --list shows what can be undone.

A move whose session was used afterwards is not undone, since that later work would be
lost; --force undoes it anyway.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			r, err := newRun(cmd)
			if err != nil {
				return err
			}
			defer r.app.Catalog.Close()
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
			for _, k := range j.Kept {
				r.printf("Kept the branch %s, as you chose (delete_branch = never).\n", k)
			}
			if len(j.Parts) > 0 {
				r.printf("Both legs of the hop were undone.\n")
			}
			for _, m := range j.Manual {
				r.printf("The %s session %s stays in the cloud; archive it there if you want it gone: %s\n", m.Cloud, m.Key.Session, m.URL)
			}
			return nil
		},
	}
	cmd.Flags().Bool("list", false, "list what can be undone")
	cmd.Flags().Bool("yes", false, "do not ask for confirmation")
	cmd.Flags().Bool("force", false, "undo even when the session was used since (that later work is lost)")
	cmd.Flags().Bool("json", false, "output JSON (with --list)")
	return cmd
}
