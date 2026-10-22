// Command hopsesh moves coding-agent sessions between your machines, and between agents.
package main

import (
	"fmt"
	"os"

	"github.com/roeehrl/hopsesh/internal/agents/all"
	"github.com/roeehrl/hopsesh/internal/core/convert"
	"github.com/roeehrl/hopsesh/internal/core/integrate"
	"github.com/roeehrl/hopsesh/internal/core/transport"
	"github.com/roeehrl/hopsesh/internal/ui/cli"
	"github.com/roeehrl/hopsesh/internal/update"
)

func main() {
	if transport.IsAskpass() {
		os.Exit(transport.AskpassMain(os.Args[1:])) // ssh asking for a password, see transport
	}
	update.CleanUp() // what an earlier update moved aside
	reg := all.Registry()
	integrate.SetLoginVars(reg.LoginEnv())
	convert.Program = integrate.SkillBin // the briefing names hopsesh as the agent's shell finds it
	if err := cli.NewRoot(os.Stdout, reg).Execute(); err != nil {
		if code, ok := cli.ExitStatus(err); ok {
			os.Exit(code)
		}
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
