package transport

import (
	"context"
	"encoding/base64"
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// Match Win32 OpenSSH's inherited descriptor record: no auxiliary handles and
// three asynchronous non-socket descriptors. Go's replacement pipes are fresh
// synchronous handles, so this metadata must not reach the new SSH process.
func TestNativeSSHWithInheritedDescriptors(t *testing.T) {
	t.Setenv(windowsSSHDescriptorState, base64.StdEncoding.EncodeToString([]byte{0, 0, 0, 0, 2, 2, 2, 0}))
	t.Setenv("OPENSSH_STDIO_MODE", "nonsock")
	TestNativeSSHCommandAndDiagnostics(t)
}

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
