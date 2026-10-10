package repos

import (
	"context"
	"fmt"
	"os/exec"
	"time"

	"github.com/roeehrl/hopsesh/internal/core/proc"
)

// Git for Windows' shell creates native child processes too. Scope termination
// to this command's tree, with a bounded fallback if taskkill cannot complete.
func configureProbeCancellation(cmd *exec.Cmd) {
	cmd.Cancel = func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := proc.CommandContext(ctx, "taskkill", "/PID", fmt.Sprint(cmd.Process.Pid), "/T", "/F").Run(); err == nil {
			return nil
		}
		return cmd.Process.Kill()
	}
}
