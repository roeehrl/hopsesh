package integrate

import (
	"os"
	"strings"
	"testing"
)

// The login shell's folders come first; folders only this process had stay, after them,
// each once.
func TestMergePath(t *testing.T) {
	sep := string(os.PathListSeparator)
	got := mergePath(strings.Join([]string{"/u/.local/bin", "/opt/homebrew/bin", "/usr/bin"}, sep), strings.Join([]string{"/usr/bin", "/bin", "", "/u/.local/bin"}, sep))
	want := strings.Join([]string{"/u/.local/bin", "/opt/homebrew/bin", "/usr/bin", "/bin"}, sep)
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}
