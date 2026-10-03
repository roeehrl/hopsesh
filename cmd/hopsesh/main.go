// Command hopsesh moves coding-agent sessions between your machines, and between agents.
package main

import (
	"fmt"
	"os"

	"github.com/roeehrl/hopsesh/internal/agents/all"
	"github.com/roeehrl/hopsesh/internal/core/integrate"
	"github.com/roeehrl/hopsesh/internal/core/transport"
	"github.com/roeehrl/hopsesh/internal/ui/cli"
)

func main() {
	if transport.IsAskpass() {
		os.Exit(transport.AskpassMain(os.Args[1:])) // ssh asking for a password, see transport
	}
	reg := all.Registry()
	integrate.SetLoginVars(reg.LoginEnv())
	if err := cli.NewRoot(os.Stdout, reg).Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
