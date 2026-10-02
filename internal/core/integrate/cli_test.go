package integrate

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestPathBlock(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the command link and PATH block are for macOS and Linux")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SHELL", "/bin/zsh")
	prof := filepath.Join(home, ".zprofile")
	os.WriteFile(prof, []byte("export FOO=1\n"), 0o644)
	if f, err := AddToPath(); err != nil || f != prof {
		t.Fatalf("%s %v", f, err)
	}
	AddToPath() // idempotent
	b, _ := os.ReadFile(prof)
	if strings.Count(string(b), blockStart) != 1 || !strings.Contains(string(b), `export PATH="$HOME/.local/bin:$PATH"`) {
		t.Fatalf("profile:\n%s", b)
	}
	if err := RemovePathBlock(); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(prof); string(b) != "export FOO=1\n" {
		t.Fatalf("after remove: %q", b)
	}
}

func TestCheckCLIStates(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the command link is for macOS and Linux")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	link := LinkPath()
	if CheckCLI().State != CLIMissing {
		t.Fatal("missing")
	}
	os.MkdirAll(filepath.Dir(link), 0o755)
	app := filepath.Join(home, "Applications", "hopsesh.app", "Contents", "Resources", "bin", "hopsesh")
	os.MkdirAll(filepath.Dir(app), 0o755)
	os.WriteFile(app, []byte("#!/bin/sh\n"), 0o755)
	os.Symlink(app, link)
	if st := CheckCLI(); st.State != CLIOtherApp || st.Target != app {
		t.Fatalf("link into a hopsesh.app (not this process): %+v", st)
	}
	os.Remove(app)
	if CheckCLI().State != CLIDangling {
		t.Fatal("dangling")
	}
	os.Remove(link)
	os.WriteFile(link, []byte("bin"), 0o755)
	if CheckCLI().State != CLIStandalone {
		t.Fatal("standalone")
	}
	if _, err := InstallCLI(false); err == nil {
		t.Fatal("not an app: install must refuse")
	}
	if err := UninstallCLI(); err == nil {
		t.Fatal("a standalone copy is never removed by the app")
	}
}

func TestAdoptLoginEnv(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	login := func() map[string]string { return map[string]string{"CLAUDE_CONFIG_DIR": "/tmp/claude-alt"} }
	adoptLoginEnv(func(string) string { return "" }, login)
	if got := os.Getenv("CLAUDE_CONFIG_DIR"); got != "/tmp/claude-alt" {
		t.Fatalf("CLAUDE_CONFIG_DIR = %q", got)
	}
	// A value already in the process wins.
	adoptLoginEnv(func(string) string { return "/mine" }, func() map[string]string { return map[string]string{"CLAUDE_CONFIG_DIR": "/other"} })
	if got := os.Getenv("CLAUDE_CONFIG_DIR"); got != "/tmp/claude-alt" {
		t.Fatalf("overwrote an existing value: %q", got)
	}
}
