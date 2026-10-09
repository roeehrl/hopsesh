package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/roeehrl/hopsesh/internal/agents/all"
	"github.com/roeehrl/hopsesh/internal/config"
	"github.com/roeehrl/hopsesh/internal/testkit"
)

func TestCommandReleasesCatalogOnSuccessAndError(t *testing.T) {
	home := t.TempDir()
	for k, v := range testkit.Env(home) {
		t.Setenv(k, v)
	}
	if err := testkit.DemoHome(home); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"ls", "--no-git", "--host", "local", "--json"}, {"show", "missing-session"}} {
		cmd := NewRoot(new(bytes.Buffer), all.Registry())
		cmd.SetArgs(args)
		err := cmd.Execute()
		if (args[0] == "show") != (err != nil) {
			t.Fatalf("%v: unexpected command result: %v", args, err)
		}
		catalog := filepath.Join(config.StateDir(), "catalog")
		if _, err := os.Stat(filepath.Join(catalog, "sessions-v1.sqlite")); err != nil {
			t.Fatalf("command did not exercise the catalog: %v", err)
		}
		if err := os.RemoveAll(catalog); err != nil {
			t.Fatalf("%v retained an open catalog: %v", args, err)
		}
	}
}
