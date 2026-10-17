package cli

import (
	"encoding/json"
	"errors"
	"github.com/roeehrl/hopsesh/internal/core/move"
	"github.com/roeehrl/hopsesh/sdk/agent"
	"github.com/spf13/cobra"
)

func cloudCheckpointImportCmd() *cobra.Command {
	var opt move.Options
	var target string
	var dry bool
	c := &cobra.Command{Use: "import <approved-cloud-fingerprint>", Short: "Review and import a portable conversation checkpoint into a new local session", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		r, err := newRun(cmd)
		if err != nil {
			return err
		}
		ctx, cancel := ctxTimeout(2)
		defer cancel()
		inv := r.scan(cmd, "local", true)
		defer inv.Close()
		p, in, checkpoint, err := r.app.PlanCloudCheckpoint(ctx, inv, args[0], agent.ID(target), opt)
		if err != nil {
			return err
		}
		review := struct {
			Plan       *move.Plan `json:"plan"`
			Checkpoint any        `json:"checkpoint"`
			SHA256     string     `json:"sha256"`
		}{p, checkpoint.Checkpoint, checkpoint.SHA256}
		if dry {
			return json.NewEncoder(cmd.OutOrStdout()).Encode(review)
		}
		if !r.yes {
			if err = json.NewEncoder(cmd.OutOrStdout()).Encode(review); err != nil {
				return err
			}
			if !r.confirm("Import this reviewed checkpoint into a new local session?") {
				return errors.New("checkpoint reviewed; import was not requested")
			}
		}
		result, err := r.app.Apply(ctx, p, in, nil)
		if err != nil {
			return err
		}
		return json.NewEncoder(cmd.OutOrStdout()).Encode(struct {
			Plan   *move.Plan   `json:"plan"`
			Result *move.Result `json:"result"`
		}{p, result})
	}}
	c.Flags().StringVar(&opt.TargetDir, "to", "", "existing local working directory (required; no code is transferred)")
	c.Flags().StringVar(&target, "in", "", "destination agent; defaults to the source agent")
	c.Flags().StringVar(&opt.TargetProfile, "profile", "", "destination runtime profile")
	c.Flags().StringVar(&opt.OperationID, "operation-id", "", "stable operation ID for a reviewed checkpoint or interrupted retry")
	c.Flags().BoolVar(&opt.Fork, "fork", false, "create an independent branch from the approved checkpoint")
	c.Flags().BoolVar(&opt.Redact, "redact", false, "redact detected secrets from the portable copy")
	c.Flags().BoolVar(&dry, "dry-run", false, "review the frozen checkpoint and plan without writing a native session")
	c.Flags().Bool("yes", false, "import the reviewed checkpoint without an interactive prompt")
	return c
}
