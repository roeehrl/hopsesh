// Command hopsesh finds Claude Code sessions on your other machines and continues one here.
package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/roeehrl/hopsesh/internal/cli"
	"github.com/roeehrl/hopsesh/internal/core/transport"
)

func main() {
	if transport.IsAskpass() {
		os.Exit(transport.AskpassMain(os.Args[1:])) // ssh asking for a password, see transport
	}
	if err := cli.NewRoot(os.Stdout).Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		if errors.Is(err, cli.ErrNotImplemented) {
			os.Exit(cli.ExitNotImplemented)
		}
		os.Exit(1)
	}
}
