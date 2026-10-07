// Command hopsesh moves coding-agent sessions between your machines, and between agents.
package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/roeehrl/hopsesh/internal/agents/all"
	"github.com/roeehrl/hopsesh/internal/core/integrate"
	"github.com/roeehrl/hopsesh/internal/core/transport"
	"github.com/roeehrl/hopsesh/internal/ui/cli"
	"github.com/roeehrl/hopsesh/internal/update"
)

func main() {
	if transport.IsAskpass() {
		os.Exit(transport.AskpassMain(os.Args[1:])) // ssh asking for a password, see transport
	}
	reg := all.Registry()
	integrate.SetLoginVars(reg.LoginEnv())
	root := cli.NewRoot(os.Stdout, reg)
	prepare := root.PersistentPreRunE
	root.PersistentPreRunE = func(cmd *cobra.Command, args []string) error {
		if err := prepare(cmd, args); err != nil {
			return err
		}
		// A passive invocation must not remove backups from an earlier update.
		if cmd.Annotations["hopsesh.passive"] != "true" {
			update.CleanUp()
		}
		return nil
	}
	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
