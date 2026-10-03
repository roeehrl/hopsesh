// Package proc starts child processes. In a program without a console (the Windows app is
// a window program) each console child would open a console window of its own, so there
// children get none; everywhere else these are exec.Command and exec.CommandContext.
package proc

import (
	"context"
	"os/exec"
)

// Command is exec.Command, without a console window for the child where that matters.
func Command(name string, arg ...string) *exec.Cmd {
	cmd := exec.Command(name, arg...)
	hide(cmd)
	return cmd
}

// CommandContext is exec.CommandContext, without a console window for the child where
// that matters.
func CommandContext(ctx context.Context, name string, arg ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, name, arg...)
	hide(cmd)
	return cmd
}
