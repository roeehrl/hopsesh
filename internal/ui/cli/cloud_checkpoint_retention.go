package cli

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"
)

func cloudCheckpointCacheCmd() *cobra.Command {
	c := &cobra.Command{Use: "checkpoints", Short: "List cached cloud import reviews without reading conversation text", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		r, err := newRun(cmd)
		if err != nil {
			return err
		}
		rows, err := r.app.CloudCheckpointCache(cmd.Context())
		if err != nil {
			return err
		}
		return json.NewEncoder(cmd.OutOrStdout()).Encode(rows)
	}}
	var yes bool
	remove := &cobra.Command{Use: "remove <operation-id>", Short: "Remove a cached review; preserve sessions, lineage and undo", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		r, err := newRun(cmd)
		if err != nil {
			return err
		}
		if !yes && !r.confirm("Remove this cached checkpoint? Its review and retry ID will be retired. Native sessions, lineage and undo journals stay available.") {
			return nil
		}
		if err := r.app.RemoveCloudCheckpoint(cmd.Context(), args[0]); err != nil {
			return err
		}
		_, err = fmt.Fprintln(cmd.OutOrStdout(), "Cached checkpoint removed. Sessions, lineage and undo journals preserved.")
		return err
	}}
	remove.Flags().BoolVar(&yes, "yes", false, "confirm removal of this cached review and retirement of its retry ID")
	c.AddCommand(remove)
	return c
}
