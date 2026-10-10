package cli

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/roeehrl/hopsesh/internal/app"
)

func unblockCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "unblock [<agent>/]<id-or-title>",
		Short: "Let a moved session's original take new prompts again (removes its block or advice)",
		Long: `After a move, hopsesh blocks (or advises against) new prompts in the copy left behind, so
the moved copy stays the one source of truth and moving back is a clean return. Moving back
removes the block by itself.

unblock removes it from one original now. If you continue there, the two copies diverge:
moving back later needs a comparison or a separate fork instead of a clean return.
--restore puts the block back. Run it on the machine where the original is.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			r, err := newRun(cmd)
			if err != nil {
				return err
			}
			defer r.app.Catalog.Close()
			ref := app.ParseRef(args[0])
			inv := r.scanFor(cmd, ref.Machine, ref)
			defer inv.Close()
			e, err := inv.Find(ref)
			if err != nil {
				return err
			}
			if e.Machine != app.LocalName() {
				return fmt.Errorf("the original is on %s: run hopsesh unblock there", e.Machine)
			}
			if restore, _ := cmd.Flags().GetBool("restore"); restore {
				if err := r.app.RestoreBlock(e.Session.Key); err != nil {
					return err
				}
				r.printf("The block is back on %q until you move it back here.\n", e.Session.Title)
				return nil
			}
			n := e.Departure
			if n == nil {
				n = e.Movement
			}
			if n == nil || n.Operation == "" {
				return errors.New("this session was not moved away: nothing to unblock")
			}
			if r.app.Released(e.Session.Key, n.Operation) {
				r.printf("%q is already unblocked.\n", e.Session.Title)
				return nil
			}
			where := n.AgentName + " on " + n.Machine
			r.printf("%q was moved to %s; that copy stays the active one.\n", e.Session.Title, where)
			r.printf("If you continue here, the two copies diverge, and moving back will need a comparison or a separate fork instead of a clean return.\n")
			if !r.confirm("Remove the block from this original?") {
				return errors.New("not unblocked (use --yes to confirm without asking)")
			}
			if err := r.app.ReleaseOriginal(e.Session.Key, n.Operation); err != nil {
				return err
			}
			r.printf("Unblocked. hopsesh unblock --restore %s puts the block back.\n", args[0])
			return nil
		},
	}
	cmd.Flags().Bool("restore", false, "put the block back")
	cmd.Flags().Bool("yes", false, "do not ask for confirmation")
	cmd.Flags().Bool("json", false, "output JSON")
	return cmd
}
