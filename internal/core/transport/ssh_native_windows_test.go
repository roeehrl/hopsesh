package transport

import (
	"context"
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// The desktop and an SSH-hosted CLI may have no console. Run the same wire
// assertions in that real process state so proc's production hiding behavior
// participates; success in an interactive workflow shell is insufficient.
func TestNativeSSHWithoutConsole(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, "-test.run=^TestNativeSSHCommandAndDiagnostics$", "-test.v")
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NO_WINDOW, HideWindow: true}
	cmd.WaitDelay = 3 * time.Second
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("native SSH without console: %v\n%s", err, out)
	}
	t.Logf("%s", out)
}
