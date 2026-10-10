package repos

import (
	"os"
	"os/exec"
	"testing"
)

// Diagnostic branch only: distinguish shell-wrapper PATH changes from a slow
// or uncancelled production Git child. Uses only this test's disposable fixture.
func TestProbeWindowsShellDiagnostic(t *testing.T) {
	_ = slowWorld(t)
	sh, err := findSh()
	if err != nil {
		t.Fatal(err)
	}
	git, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	out, err := exec.CommandContext(t.Context(), sh, "-c", "command -v git").CombinedOutput()
	t.Logf("shell=%q Go-selected-git=%q shell-selected-git=%q MSYSTEM=%q err=%v", sh, git, out, os.Getenv("MSYSTEM"), err)
	if err != nil {
		t.Fatal(err)
	}
}
