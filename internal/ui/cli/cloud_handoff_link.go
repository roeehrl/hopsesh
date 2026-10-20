package cli

import (
	"encoding/json"
	"errors"

	"github.com/spf13/cobra"
)

func cloudHandoffsCmd() *cobra.Command {
	return &cobra.Command{Use: "handoffs", Short: "List recent saved cloud handoffs with verified native receipts", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		r, err := newRun(cmd)
		if err != nil {
			return err
		}
		rows, err := r.app.CloudHandoffOptions()
		if err != nil {
			return err
		}
		return json.NewEncoder(cmd.OutOrStdout()).Encode(rows)
	}}
}

func cloudLinkHandoffCmd() *cobra.Command {
	var fork, dry bool
	c := &cobra.Command{Use: "link-handoff <logical-task-id> <saved-handoff-id>", Short: "Associate a cloud task with its reviewed saved handoff before its first checkpoint", Args: cobra.ExactArgs(2), RunE: func(cmd *cobra.Command, args []string) error {
		r, err := newRun(cmd)
		if err != nil {
			return err
		}
		plan, err := r.app.PlanCloudHandoffLink(args[0], args[1], fork)
		if err != nil {
			return err
		}
		if dry {
			return json.NewEncoder(cmd.OutOrStdout()).Encode(plan)
		}
		if !r.yes {
			if err := json.NewEncoder(cmd.OutOrStdout()).Encode(plan); err != nil {
				return err
			}
			if !r.confirm("Associate this cloud task with the reviewed saved handoff? Checkpoint import will verify its briefing or native prefix.") {
				return errors.New("handoff reviewed; association was not requested")
			}
		}
		result, err := r.app.LinkCloudHandoff(cmd.Context(), args[0], args[1], fork, plan.Review)
		if err != nil {
			return err
		}
		return json.NewEncoder(cmd.OutOrStdout()).Encode(result)
	}}
	c.Flags().BoolVar(&fork, "fork", false, "this is an independent cloud fork of the saved handoff")
	c.Flags().BoolVar(&dry, "dry-run", false, "review verified ancestry without saving an association")
	c.Flags().Bool("yes", false, "save the specified reviewed association without an interactive prompt")
	return c
}
