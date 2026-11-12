//go:build windows

package host

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/windows"

	"github.com/roeehrl/hopsesh/sdk/agent"
)

func TestWindowsLockProbeMissingAndUnlocked(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "not-created", "thread.lock")
	if got := probeLock(missing); got != agent.LockFree {
		t.Fatalf("missing lock: %s", got)
	}
	if _, err := os.Stat(filepath.Dir(missing)); !os.IsNotExist(err) {
		t.Fatalf("probe created the lock directory: %v", err)
	}
	for _, content := range [][]byte{nil, []byte("writer-lock sentinel")} {
		p := filepath.Join(dir, fmt.Sprintf("%d.lock", len(content)))
		if err := os.WriteFile(p, content, 0600); err != nil {
			t.Fatal(err)
		}
		if err := windows.SetFileAttributes(windows.StringToUTF16Ptr(p), windows.FILE_ATTRIBUTE_READONLY); err != nil {
			t.Fatal(err)
		}
		for range 2 {
			states, err := (localLocks{}).Probe(t.Context(), []string{p, missing})
			if err != nil || states[p] != agent.LockFree || states[missing] != agent.LockFree {
				t.Fatalf("unlocked read-only file: %v %v", states, err)
			}
		}
		if err := windows.SetFileAttributes(windows.StringToUTF16Ptr(p), windows.FILE_ATTRIBUTE_NORMAL); err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(p)
		if err != nil || !bytes.Equal(got, content) {
			t.Fatalf("probe changed file: %q %v", got, err)
		}
		// No shared probe lock or open handle may survive the return.
		if err := os.Remove(p); err != nil {
			t.Fatalf("probe retained a handle: %v", err)
		}
	}
}

// A separate process holds the same whole-file exclusive Windows lock as Rust
// File::try_lock. Release is explicit while the process remains alive, so a free
// result cannot be explained merely by process exit or by testing one handle.
func TestWindowsWriterLockHelper(t *testing.T) {
	p := os.Getenv("HOPSESH_TEST_WRITER_LOCK")
	if p == "" {
		return
	}
	f, err := os.OpenFile(p, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var offset windows.Overlapped
	if err := windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, ^uint32(0), ^uint32(0), &offset); err != nil {
		t.Fatal(err)
	}
	fmt.Println("held")
	var command [1]byte
	if _, err := io.ReadFull(os.Stdin, command[:]); err != nil || command[0] != 'r' {
		t.Fatalf("release command: %q %v", command, err)
	}
	if err := windows.UnlockFileEx(windows.Handle(f.Fd()), 0, ^uint32(0), ^uint32(0), &offset); err != nil {
		t.Fatal(err)
	}
	fmt.Println("released")
	if _, err := io.ReadFull(os.Stdin, command[:]); err != nil || command[0] != 'q' {
		t.Fatalf("quit command: %q %v", command, err)
	}
}

func TestWindowsLockProbeHeldByAnotherProcessAndReleased(t *testing.T) {
	p := filepath.Join(t.TempDir(), "empty-thread.lock")
	if err := os.WriteFile(p, nil, 0600); err != nil {
		t.Fatal(err)
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, self, "-test.run=^TestWindowsWriterLockHelper$")
	cmd.Env = append(os.Environ(), "HOPSESH_TEST_WRITER_LOCK="+p)
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	in, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	finished := false
	t.Cleanup(func() {
		if !finished {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	})
	reader := bufio.NewReader(out)
	state := func(want string) {
		t.Helper()
		got, err := reader.ReadString('\n')
		if err != nil || got != want+"\n" {
			t.Fatalf("helper state %q, want %s: %v", got, want, err)
		}
	}
	state("held")
	for range 2 {
		states, err := (localLocks{}).Probe(t.Context(), []string{p})
		if err != nil || states[p] != agent.LockHeld {
			t.Fatalf("exclusive lock in another process: %v %v", states, err)
		}
	}
	if _, err := in.Write([]byte{'r'}); err != nil {
		t.Fatal(err)
	}
	state("released")
	if !processExists(cmd.Process.Pid) {
		t.Fatal("release was actually process exit")
	}
	for range 2 {
		if got := probeLock(p); got != agent.LockFree {
			t.Fatalf("released lock: %s", got)
		}
	}
	if _, err := in.Write([]byte{'q'}); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("helper exit: %v", err)
	}
	finished = true
	if got := probeLock(p); got != agent.LockFree {
		t.Fatalf("exited writer: %s", got)
	}
	if err := os.Remove(p); err != nil {
		t.Fatalf("probe retained lock/handle: %v", err)
	}
}

func TestWindowsLockProbeErrorsStayUnknown(t *testing.T) {
	for _, p := range []string{"", "invalid\x00path", t.TempDir(), "NUL"} {
		if got := probeLock(p); got != agent.LockUnknown {
			t.Errorf("invalid/directory/device %q: %s", p, got)
		}
	}
	p := filepath.Join(t.TempDir(), "sharing-denied.lock")
	if err := os.WriteFile(p, nil, 0600); err != nil {
		t.Fatal(err)
	}
	h, err := windows.CreateFile(windows.StringToUTF16Ptr(p), windows.GENERIC_READ|windows.GENERIC_WRITE, 0, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatal(err)
	}
	got := probeLock(p)
	if err := windows.CloseHandle(h); err != nil {
		t.Fatal(err)
	}
	if got != agent.LockUnknown {
		t.Fatalf("sharing error is not a writer lock: %s", got)
	}
	if got := probeLock(p); got != agent.LockFree {
		t.Fatalf("after denied share released: %s", got)
	}

	// An actual access denial must never become proof that a writer has ended.
	sd, err := windows.GetNamedSecurityInfo(p, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	original, _, err := sd.DACL()
	if err != nil {
		t.Fatal(err)
	}
	denied, err := windows.SecurityDescriptorFromString("D:P(D;;GR;;;WD)")
	if err != nil {
		t.Fatal(err)
	}
	dacl, _, err := denied.DACL()
	if err != nil {
		t.Fatal(err)
	}
	if err := windows.SetNamedSecurityInfo(p, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := windows.SetNamedSecurityInfo(p, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION, nil, nil, original, nil); err != nil {
			t.Error(err)
		}
	})
	if got := probeLock(p); got != agent.LockUnknown {
		t.Fatalf("read access denied: %s", got)
	}
}
