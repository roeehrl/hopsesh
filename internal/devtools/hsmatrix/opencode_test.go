package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/roeehrl/hopsesh/internal/testkit/fakeopencode"
)

// The stand-in is told from a real OpenCode by the package its build recorded, without
// running the program: a program that is not the stand-in is refused and never runs.
func TestStandInIsReadNotRun(t *testing.T) {
	dir := t.TempDir()
	exe := ""
	if runtime.GOOS == "windows" {
		exe = ".exe"
	}
	bin := filepath.Join(dir, "opencode"+exe)
	if out, err := exec.Command("go", "build", "-o", bin, fakeAgent).CombinedOutput(); err != nil {
		t.Fatalf("go build: %v: %s", err, out)
	}
	if !isStandIn(bin) {
		t.Fatal("the stand-in built as opencode is not recognised")
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if isStandIn(self) {
		t.Fatal("another Go program was taken for the stand-in")
	}
	ran := filepath.Join(dir, "ran")
	script := filepath.Join(dir, "other-opencode")
	if err := os.WriteFile(script, []byte("#!/bin/sh\ntouch '"+ran+"'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if isStandIn(script) {
		t.Fatal("a script was taken for the stand-in")
	}
	if _, err := os.Stat(ran); err == nil {
		t.Fatal("the program was run to identify it")
	}
}

// A turn for a session that is not there is refused and leaves no message behind.
func TestOpenCodeTurnNeedsTheSession(t *testing.T) {
	db, err := fakeopencode.Open(filepath.Join(t.TempDir(), "opencode.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	err = ocAddTurn(db, "ses_missing", "a prompt", time.Now())
	if err == nil || !strings.Contains(err.Error(), "no OpenCode session ses_missing") {
		t.Fatalf("a turn for a missing session: %v", err)
	}
	var n int
	if err := db.QueryRow("SELECT count(*) FROM session_message").Scan(&n); err != nil || n != 0 {
		t.Fatalf("messages left: %d %v", n, err)
	}
}
