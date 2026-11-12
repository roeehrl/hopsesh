package app

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/roeehrl/hopsesh/internal/agents/all"
	"github.com/roeehrl/hopsesh/internal/config"
)

func accountsApp(t *testing.T) (*App, string) {
	t.Helper()
	home := t.TempDir()
	home, _ = filepath.EvalSymlinks(home)
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("CODEX_HOME", "")
	t.Setenv("HOPSESH_CONFIG_DIR", filepath.Join(home, "config"))
	t.Setenv("HOPSESH_STATE_DIR", filepath.Join(home, "state"))
	t.Setenv("HOPSESH_MACHINE", "here")
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	a := New(cfg, all.Registry(), config.StateDir(), nil)
	t.Cleanup(func() { _ = a.Catalog.Close() })
	return a, home
}

// A new account folder is ~/.hopsesh/accounts/<agent>/<name>, keeps its sign-in where the
// default folder does, and can have a command of its own.
func TestNewAccountFolderLivesInHopseshAccounts(t *testing.T) {
	a, home := accountsApp(t)
	os.MkdirAll(filepath.Join(home, ".codex"), 0o700)
	os.WriteFile(filepath.Join(home, ".codex", "config.toml"), []byte("model = \"x\"\ncli_auth_credentials_store = \"file\"\n"), 0o600)
	p, err := a.AddAccount(context.Background(), "", "codex", "Client A", []string{"Work"}, AddAccountOptions{Command: runtime.GOOS != "windows"})
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(home, ".hopsesh", "accounts", "codex", "client-a")
	if p.Root != want || !p.Managed {
		t.Fatalf("root %s, want %s", p.Root, want)
	}
	b, err := os.ReadFile(filepath.Join(want, "config.toml"))
	if err != nil || !strings.Contains(string(b), `cli_auth_credentials_store = "file"`) || strings.Contains(string(b), "model") {
		t.Fatalf("storage not inherited alone: %q %v", b, err)
	}
	if runtime.GOOS != "windows" {
		cmd := filepath.Join(home, ".local", "bin", "codex-client-a")
		s, err := os.ReadFile(cmd)
		if err != nil || p.Command != "codex-client-a" || !strings.Contains(string(s), "export CODEX_HOME='"+want+"'") || !strings.Contains(string(s), "unset OPENAI_API_KEY") {
			t.Fatalf("command: %q %v", s, err)
		}
		if err := a.ForgetAccount(p.ID, p.Generation); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(cmd); !os.IsNotExist(err) {
			t.Fatal("forgetting the account left its command")
		}
		if _, err := os.Stat(want); err != nil {
			t.Fatal("forgetting the account removed its folder")
		}
	}
	// A folder that is there already (a forgotten account's) is never taken over by a
	// new one: adopting it is its own, explicit step.
	if _, err := a.AddAccount(context.Background(), "", "codex", "client a", nil, AddAccountOptions{}); err == nil {
		t.Fatal("an existing folder was taken again")
	}
}

// A command of that name the user has is never replaced.
func TestAccountCommandNeverReplacesTheUsers(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no account commands on Windows")
	}
	a, home := accountsApp(t)
	mine := filepath.Join(home, ".local", "bin", "claude-work")
	os.MkdirAll(filepath.Dir(mine), 0o755)
	os.WriteFile(mine, []byte("#!/bin/sh\necho mine\n"), 0o755)
	if _, err := a.AddAccount(context.Background(), "", "claude", "work", nil, AddAccountOptions{Command: true}); err == nil || !strings.Contains(err.Error(), "not hopsesh's") {
		t.Fatalf("%v", err)
	}
	if b, _ := os.ReadFile(mine); string(b) != "#!/bin/sh\necho mine\n" {
		t.Fatal("the user's command was replaced")
	}
}

// Folders hopsesh made in its state folder move to ~/.hopsesh/accounts on request.
func TestOldAccountFoldersMoveOutOfTheStateFolder(t *testing.T) {
	a, home := accountsApp(t)
	p, err := a.AddAccount(context.Background(), "", "claude", "Personal", nil, AddAccountOptions{})
	if err != nil {
		t.Fatal(err)
	}
	old := filepath.Join(a.StateDir, "profiles", p.ID, "claude")
	os.MkdirAll(filepath.Dir(old), 0o700)
	if err := os.Rename(p.Root, old); err != nil {
		t.Fatal(err)
	}
	if err := a.accountStore().Relocate(p.ID, p.Root, old); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(old, "marker"), []byte("x"), 0o600)
	moves, err := a.FoldersToMove()
	if err != nil || len(moves) != 1 || moves[0].To != filepath.Join(home, ".hopsesh", "accounts", "claude", "personal") {
		t.Fatalf("%+v %v", moves, err)
	}
	done, err := a.MoveFolders()
	if err != nil || len(done) != 1 {
		t.Fatalf("%+v %v", done, err)
	}
	if _, err := os.Stat(filepath.Join(done[0].To, "marker")); err != nil {
		t.Fatal("contents did not move")
	}
	ps, _ := a.Accounts()
	for _, q := range ps {
		if q.ID == p.ID && q.Root != done[0].To {
			t.Fatalf("registry still points at %s", q.Root)
		}
	}
	if left, _ := a.FoldersToMove(); len(left) != 0 {
		t.Fatalf("left: %+v", left)
	}
}
