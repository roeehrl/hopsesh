//go:build !windows

package sessions

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// TestStopLocal stops a stand-in process registered as a live session, and refuses to
// signal a pid that the registry does not tie to the session.
func TestStopLocal(t *testing.T) {
	cfg := t.TempDir()
	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { _ = cmd.Wait(); close(done) }()
	defer func() { _ = cmd.Process.Kill() }()
	os.MkdirAll(filepath.Join(cfg, "sessions"), 0o700)
	b, _ := json.Marshal(map[string]any{"pid": cmd.Process.Pid, "sessionId": "s1", "cwd": "/x", "status": "idle"})
	os.WriteFile(filepath.Join(cfg, "sessions", "1.json"), b, 0o600)

	// Wrong session: nothing is signalled.
	if err := StopLocal(cfg, "other", cmd.Process.Pid, time.Second); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
		t.Fatal("a process not owned by the session was stopped")
	case <-time.After(300 * time.Millisecond):
	}
	if err := StopLocal(cfg, "s1", cmd.Process.Pid, 5*time.Second); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("not stopped")
	}
}
