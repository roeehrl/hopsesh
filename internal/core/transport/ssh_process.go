package transport

import (
	"context"
	"os"
	"os/exec"
	"runtime"

	"github.com/roeehrl/hopsesh/internal/core/proc"
)

// These SSH subprocesses use Go-created pipes or the null device. Windows
// OpenSSH's inherited descriptor metadata describes its parent's handles, not
// these new handles. Reusing it can deliver output but hang before client exit.
func sshCommandContext(ctx context.Context, name string, args ...string) *exec.Cmd {
	cmd := proc.CommandContext(ctx, name, args...)
	if runtime.GOOS == "windows" {
		cmd.Env = proc.PipeEnvironment(os.Environ())
	}
	return cmd
}
