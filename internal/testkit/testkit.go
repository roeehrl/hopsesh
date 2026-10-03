// Package testkit builds a demo home for tests and the browser test server: a git
// repository, Claude Code sessions and Codex threads from the agents' fixtures, with paths
// pointed at it. It is test infrastructure, not part of hopsesh.
package testkit

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/roeehrl/hopsesh/agents/claude"
)

// Env is the environment that points hopsesh and both agents at a demo home h: its own
// configuration, this machine named "studio", Tailscale left out of discovery, and (outside
// Windows) a PATH of system folders only, so no real agent program runs.
func Env(h string) map[string]string {
	env := map[string]string{
		"HOME": h, "USERPROFILE": h, "HOPSESH_CONFIG_DIR": filepath.Join(h, "config"),
		"HOPSESH_STATE_DIR": filepath.Join(h, "state"), "HOPSESH_MACHINE": "studio", "CLAUDE_CONFIG_DIR": "", "CODEX_HOME": "",
		"HOPSESH_TAILSCALE": "off", // never show this machine's real tailnet
	}
	if runtime.GOOS != "windows" {
		env["PATH"] = "/usr/bin:/bin:/usr/sbin:/sbin" // git and ssh, no agents
	}
	return env
}

// DemoHome fills h (which should be empty) with a repository at git/demo whose remote is
// github.com/example/demo, Claude Code 2.1.284 sessions and Codex 0.153.2 threads in it.
func DemoHome(h string) error {
	repo := repoRoot()
	demo := filepath.Join(h, "git", "demo")
	if err := os.MkdirAll(demo, 0o700); err != nil {
		return err
	}
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"remote", "add", "origin", "https://github.com/example/demo.git"},
		{"-c", "user.name=demo", "-c", "user.email=demo@example.com", "commit", "-q", "--allow-empty", "-m", "init"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = demo
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("git %v: %v %s", args, err, out)
		}
	}
	esc, _ := json.Marshal(demo)
	jsonDemo := string(esc[1 : len(esc)-1])
	copyTree := func(src, dst string) error {
		return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			rel, _ := filepath.Rel(src, p)
			// No account files, and no running-session registry (it would look open).
			if strings.Contains(rel, "auth.json") || strings.Contains(rel, "credentials") || strings.HasPrefix(filepath.ToSlash(rel), "sessions/4242") || strings.Contains(rel, "locks") {
				return nil
			}
			b, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			b = []byte(strings.ReplaceAll(string(b), "/home/u/git/demo", jsonDemo))
			rel = strings.ReplaceAll(rel, "-home-u-git-demo", claude.Slug(demo))
			out := filepath.Join(dst, rel)
			if err := os.MkdirAll(filepath.Dir(out), 0o700); err != nil {
				return err
			}
			return os.WriteFile(out, b, 0o600)
		})
	}
	if err := copyTree(filepath.Join(repo, "agents", "claude", "testdata", "2.1.284"), filepath.Join(h, ".claude")); err != nil {
		return err
	}
	return copyTree(filepath.Join(repo, "agents", "codex", "testdata", "0.153.2"), filepath.Join(h, ".codex"))
}

// repoRoot is the module's folder (this file's, two levels up).
func repoRoot() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "..")
}
