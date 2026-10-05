// Package cli is the hopsesh command line.
package cli

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/roeehrl/hopsesh/internal/core/registry"
	"github.com/roeehrl/hopsesh/internal/version"
)

// NewRoot builds the command line for the agent modules in reg. Output goes to out so
// tests can capture it.
func NewRoot(out io.Writer, reg *registry.Registry) *cobra.Command {
	modules = reg
	root := &cobra.Command{
		Use:   "hopsesh",
		Short: "Move coding-agent sessions between your machines, and between agents",
		Long: `hopsesh finds your coding-agent sessions (Claude Code, Codex) on this machine and your
other machines (over your own SSH or Tailscale), shows them grouped by repository, and
moves the one you pick here, or continues it in another agent: it checks or clones the
repo, copies the session, rewrites its paths and prints the command to continue it.

Unofficial; not affiliated with or endorsed by Anthropic or OpenAI.`,
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			r, err := newRun(cmd)
			if err != nil {
				return err
			}
			if !r.interactive() {
				return cmd.Help()
			}
			return r.runTUI()
		},
	}
	root.PersistentFlags().Bool("password-stdin", false, "for machines that log in with a password: read it from standard input (for scripts)")
	root.SetOut(out)
	root.SetErr(out)
	root.AddCommand(
		versionCmd(), updateCmd(), agentsCmd(), hostsCmd(), cloudsCmd(), trustCmd(), doctorCmd(),
		lsCmd(), showCmd(), pullCmd(), planCmd(), pushCmd(), handoffCmd(), followupCmd(), receiveCmd(), peerCmd(), undoCmd(), skillCmd(),
		openCmd(), terminalsCmd(), terminalStepCmd(), terminalOpenCmd(),
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
