package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/roeehrl/hopsesh/internal/config"
)

// HOPSESH_CONFIG_DIR naming the default folder is still the default installation: the
// hook finds the endpoint identity in ~/.hopsesh, where a default install keeps it.
func TestNoticeEndpointTreatsDefaultConfigDirAsDefault(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	id := strings.Repeat("ab", 32)
	if err := os.MkdirAll(filepath.Join(home, ".hopsesh"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".hopsesh", "endpoint-id"), []byte(id+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOPSESH_CONFIG_DIR", config.DefaultDir())
	got, err := localNoticeEndpoint(t.Context())
	if err != nil || got != id {
		t.Fatalf("endpoint %q, %v; want the default install's %q", got, err, id)
	}
	t.Setenv("HOPSESH_CONFIG_DIR", filepath.Join(home, "separate"))
	if got, _ := localNoticeEndpoint(t.Context()); got == id {
		t.Fatal("a separate configuration folder borrowed the default endpoint")
	}
}
