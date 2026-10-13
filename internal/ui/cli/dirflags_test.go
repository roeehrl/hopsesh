package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"

	"github.com/roeehrl/hopsesh/internal/config"
)

// The protection hooks always pass --config-dir and --state-dir. On a default install
// those name the default folders, which must stay the default installation: an explicit
// HOPSESH_CONFIG_DIR moves this machine's endpoint identity, and the hook then never
// recognised the original it had to block.
func TestDirFlagsNamingDefaultsChangeNothing(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	for _, k := range []string{"HOPSESH_CONFIG_DIR", "HOPSESH_STATE_DIR", "XDG_CONFIG_HOME", "XDG_STATE_HOME"} {
		t.Setenv(k, "")
		os.Unsetenv(k)
	}
	run := func(cfg, state string) {
		cmd := &cobra.Command{}
		addDirFlags(cmd)
		_ = cmd.Flags().Set("config-dir", cfg)
		_ = cmd.Flags().Set("state-dir", state)
		useDirFlags(cmd)
	}
	run(config.DefaultDir(), config.DefaultStateDir())
	if v, w := os.Getenv("HOPSESH_CONFIG_DIR"), os.Getenv("HOPSESH_STATE_DIR"); v != "" || w != "" {
		t.Fatalf("default folders became explicit: %q %q", v, w)
	}
	other := filepath.Join(home, "elsewhere")
	run(other, other)
	if os.Getenv("HOPSESH_CONFIG_DIR") != other || os.Getenv("HOPSESH_STATE_DIR") != other {
		t.Fatal("a different folder was not applied")
	}
}
