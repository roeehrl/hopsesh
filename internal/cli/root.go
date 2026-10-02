// Package cli implements the hopsesh command line.
package cli

import (
	"errors"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/roeehrl/hopsesh/internal/version"
)

// ErrNotImplemented is returned by commands that are planned but not built yet.
var ErrNotImplemented = errors.New("not implemented yet")

// ExitNotImplemented is the process exit code for planned-but-missing commands.
const ExitNotImplemented = 3

// NewRoot builds the root command. Output goes to out so tests can capture it.
func NewRoot(out io.Writer) *cobra.Command {
	root := &cobra.Command{
		Use:   "hopsesh",
		Short: "Find your Claude Code sessions on your other machines and continue one here",
		Long: `hopsesh finds Claude Code sessions on your machines (over your own SSH or
Tailscale), shows them grouped by repository, and moves the one you pick to this
machine: it checks or clones the repo, copies the session, rewrites its paths and
prints the command to resume it.

Unofficial; not affiliated with or endorsed by Anthropic.`,
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			a, err := newApp(cmd)
			if err != nil {
				return err
			}
			if !a.interactive() {
				return cmd.Help()
			}
			return a.runTUI()
		},
	}
	root.SetOut(out)
	root.SetErr(out)
	root.AddCommand(
		versionCmd(),
		updateCmd(),
		hostsCmd(),
		trustCmd(),
		doctorCmd(),
		lsCmd(),
		showCmd(),
		pullCmd(),
		planCmd(),
		importCmd(),
		undoCmd(),
		agentCmd(),
	)
	return root
}

func versionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the version",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, err := fmt.Fprintln(cmd.OutOrStdout(), version.String())
			return err
		},
	}
}
