package cli

import (
	"fmt"

	"github.com/roeehrl/hopsesh/internal/app"
	"github.com/spf13/cobra"
)

func lineageCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "lineage", Short: "Inspect or archive session lineage metadata"}
	archive := &cobra.Command{Use: "archive [machine:][agent/]<id-or-title>", Short: "Archive unsupported lineage; preserve the native conversation (undoable)", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		r, err := newRun(cmd)
		if err != nil {
			return err
		}
		ref := app.ParseRef(args[0])
		inv := r.scanFor(cmd, ref.Machine, ref)
		defer inv.Close()
		e, err := inv.Find(ref)
		if err != nil {
			return err
		}
		ctx, cancel := ctxTimeout(1)
		defer cancel()
		j, err := r.app.ArchiveLineage(ctx, inv, e)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(cmd.OutOrStdout(), "Archived unsupported lineage; native session preserved. Undo: hopsesh undo %s\n", j.ID)
		return err
	}}
	retry := &cobra.Command{Use: "retry <journal-id>", Short: "Retry pending lineage acknowledgments without writing conversation bytes", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		r, err := newRun(cmd)
		if err != nil {
			return err
		}
		ctx, cancel := ctxTimeout(2)
		defer cancel()
		if err = r.app.RecoverReceipts(ctx, args[0]); err != nil {
			return err
		}
		_, err = fmt.Fprintln(cmd.OutOrStdout(), "Lineage acknowledgments recovered; conversation bytes preserved.")
		return err
	}}
	cmd.AddCommand(archive, retry)
	return cmd
}
