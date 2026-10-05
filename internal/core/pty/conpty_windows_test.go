//go:build windows

package pty_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/roeehrl/hopsesh/internal/core/pty"
	"github.com/roeehrl/hopsesh/internal/core/pty/ptytest"
)

// roundTrip runs the da1 role in a tab of m and checks the pseudoconsole in between:
// the backend, the program's answer to its query, keys, a resize and the exit code.
func roundTrip(t *testing.T, m *pty.Manager, backend string) {
	t.Helper()
	s := start(t, m, role(t, "da1"))
	if got := s.Info().Backend; got != backend {
		t.Fatalf("backend %q, want %q", got, backend)
	}
	w := open(t, m, s, ptytest.Options{Cols: 90, Rows: 25})
	must(t, w.WaitFor("ready size=", wait))
	must(t, w.WaitFor("da1=", wait))
	// What the pseudoconsole does with the query: passes it to the emulator (which
	// answers \x1b[?62;1;6;22c) or answers it itself.
	t.Logf("%s: the emulator answered %d queries; the screen:\n%s", backend, w.Answered(), w.Screen())
	w.Type("a")
	must(t, w.WaitFor("got:61", wait))
	w.Resize(110, 35)
	must(t, w.WaitFor("resized 110x35", wait))
	w.Type("q")
	i, err := w.WaitState(pty.Exited, wait)
	must(t, err)
	if i.Code != 3 {
		t.Fatalf("exit code %d, want 3", i.Code)
	}
}

// Without the bundled pair beside it, a tab uses the system's pseudoconsole.
func TestSystemConpty(t *testing.T) {
	m := tabs(t, pty.Options{ConptyDir: t.TempDir()})
	roundTrip(t, m, "conpty (system)")
}

// With conpty.dll and OpenConsole.exe in its folder (CI fetches them with
// internal/devtools/conptyfetch into HOPSESH_CONPTY_DIR), a tab uses them.
func TestBundledConpty(t *testing.T) {
	dir := os.Getenv("HOPSESH_CONPTY_DIR")
	if dir == "" {
		if os.Getenv("CI") != "" {
			t.Fatal("CI runs this with HOPSESH_CONPTY_DIR")
		}
		t.Skip("HOPSESH_CONPTY_DIR is not set (go run ./internal/devtools/conptyfetch -out <dir>)")
	}
	dir, _ = filepath.Abs(dir)
	if _, _, ok := pty.BundledConpty(dir); !ok {
		t.Fatalf("no conpty.dll with its OpenConsole.exe in %s", dir)
	}
	m := tabs(t, pty.Options{ConptyDir: dir})
	roundTrip(t, m, "conpty (bundled)")
}

// A batch file is not run: cmd.exe would parse its arguments.
func TestBatchFilesRefused(t *testing.T) {
	dir := t.TempDir()
	bat := filepath.Join(dir, "tool.cmd")
	if err := os.WriteFile(bat, []byte("@echo hi\r\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	m := tabs(t, pty.Options{})
	_, err := m.Start(pty.Spec{Argv: []string{bat, "a&b"}, Dir: dir})
	if err == nil || !strings.Contains(err.Error(), "batch file") {
		t.Fatalf("started a batch file: %v", err)
	}
	t.Setenv("PATH", dir+";"+os.Getenv("PATH"))
	if _, err := m.Start(pty.Spec{Argv: []string{"tool"}, Dir: dir}); err == nil || !strings.Contains(err.Error(), "batch file") {
		t.Fatalf("started a batch file found on PATH: %v", err)
	}
}
