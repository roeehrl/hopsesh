package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"time"
)

// ConPTY owns all three console handles. This child preserves console input and
// stderr while routing only the CLI's JSON stdout to an exclusively created file.
// It uses an argument vector, never shell interpolation or redirection.
func terminalChild(args []string) int {
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, "terminal-exec requires an output path and executable")
		return 2
	}
	out, err := os.OpenFile(args[0], os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, args[1], args[2:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, out, os.Stderr
	err = cmd.Run()
	closeErr := out.Close()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		if cmd.ProcessState != nil && cmd.ProcessState.ExitCode() > 0 {
			return cmd.ProcessState.ExitCode()
		}
		return 1
	}
	if closeErr != nil {
		fmt.Fprintln(os.Stderr, closeErr)
		return 1
	}
	return 0
}
