package cli

import (
	"fmt"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

func undoCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "undo [<journal-id or session-id>]",
		Short: "Undo a move or continuation (the newest one, or the one named)",
		Long: `Reverses everything a move, continuation or mark wrote: new files are removed, replaced
files come back, appended records are cut off, set-aside copies return. Writes on other
machines are undone over SSH. Clones and worktrees are kept. --list shows what can be undone.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			r, err := newRun(cmd)
			if err != nil {
				return err
			}
			if list, _ := cmd.Flags().GetBool("list"); list {
				js, err := r.app.Journals()
				if err != nil {
					return err
				}
				if r.jsonOut {
					return r.emitJSON(js)
				}
				tw := tabwriter.NewWriter(r.out, 0, 2, 2, ' ', 0)
				fmt.Fprintln(tw, "ID\tWHEN\tWHAT\tSTATE")
				for _, j := range js {
					state := "can undo"
					if j.Undone {
						state = "undone"
					}
					fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", j.ID, j.Time.Local().Format("2 Jan 15:04"), j.Title, state)
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
			j, err := r.app.Undo(ctx, match)
			if j != nil {
				r.printf("Undid %q (%d change(s)).\n", j.Title, len(j.Entries))
			}
			return err
		},
	}
	cmd.Flags().Bool("list", false, "list what can be undone")
	cmd.Flags().Bool("yes", false, "do not ask for confirmation")
	cmd.Flags().Bool("json", false, "output JSON (with --list)")
	return cmd
}
