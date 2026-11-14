// Package testkit builds a demo home for tests and the browser test server: a git
// repository, Claude Code sessions, Codex threads and OpenCode sessions from the agents'
// fixtures, with paths pointed at it. It is test infrastructure, not part of hopsesh.
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
	"github.com/roeehrl/hopsesh/internal/testkit/fakeopencode"
)

// Env is the environment that points hopsesh and the agents at a demo home h: its own
// configuration, this machine named "studio", Tailscale left out of discovery, and (outside
// Windows) a PATH of system folders only, so no real agent program runs. A variable with
// an empty value is one to unset: Setenv, Apply and Environ do. (OpenCode reads an empty
// OPENCODE_DB as set: the data folder itself, which no database opens.)
func Env(h string) map[string]string {
	env := map[string]string{
		"HOME": h, "USERPROFILE": h, "HOPSESH_CONFIG_DIR": filepath.Join(h, "config"),
		"HOPSESH_STATE_DIR": filepath.Join(h, "state"), "HOPSESH_MACHINE": "studio", "CLAUDE_CONFIG_DIR": "", "CODEX_HOME": "",
		// OpenCode's folders follow the XDG variables: never this machine's real ones.
		"XDG_DATA_HOME": "", "XDG_CONFIG_HOME": "", "XDG_STATE_HOME": "", "XDG_CACHE_HOME": "",
		"OPENCODE_CONFIG_DIR": "", "OPENCODE_DB": "",
		"HOPSESH_TAILSCALE":    "off", // never show this machine's real tailnet
		"HOPSESH_APP_RUNTIMES": "off", // never run an agent bundled in this machine's real desktop app
	}
	if runtime.GOOS != "windows" {
		env["PATH"] = "/usr/bin:/bin:/usr/sbin:/sbin" // git and ssh, no agents
	}
	return env
}

// Setenv sets env in a test's environment (t.Setenv, restored after the test), unsetting
// the variables whose value is empty.
func Setenv(t interface{ Setenv(k, v string) }, env map[string]string) {
	for k, v := range env {
		t.Setenv(k, v)
		if v == "" {
			os.Unsetenv(k)
		}
	}
}

// Apply sets env in this process, unsetting the variables whose value is empty.
func Apply(env map[string]string) {
	for k, v := range env {
		if v == "" {
			os.Unsetenv(k)
		} else {
			os.Setenv(k, v)
		}
	}
}

// Environ is base ("NAME=value" entries, as os.Environ gives them) with env applied: its
// variables replaced, and those whose value is empty left out.
func Environ(base []string, env map[string]string) []string {
	var out []string
	for _, kv := range base {
		k, _, _ := strings.Cut(kv, "=")
		if _, ok := env[k]; !ok {
			out = append(out, kv)
		}
	}
	for k, v := range env {
		if v != "" {
			out = append(out, k+"="+v)
		}
	}
	return out
}

// AgentVars are the variables that point the agents at their data. A test that builds its
// own environment from this process's must not pass them on: on a machine that sets them,
// hopsesh under test would read (and run the agents on) the machine's real sessions.
var AgentVars = []string{"CLAUDE_CONFIG_DIR", "CODEX_HOME", "XDG_DATA_HOME", "XDG_CONFIG_HOME", "XDG_STATE_HOME",
	"XDG_CACHE_HOME", "OPENCODE_CONFIG_DIR", "OPENCODE_DB"}

// UnsetAgentEnv unsets AgentVars for a test (restored after it), for a test with a home of
// its own: the agents then keep their data under that home. Set the variables a test
// wants after calling it.
func UnsetAgentEnv(t interface{ Setenv(k, v string) }) {
	for _, k := range AgentVars {
		t.Setenv(k, "")
		os.Unsetenv(k)
	}
}

// OpenCodeEnv points OpenCode's folders into dir, for a test that keeps this machine's
// home: OpenCode's data, state and cache under dir, its configuration in dir/config, and
// OPENCODE_DB unset. Use it with Environ, or Setenv.
func OpenCodeEnv(dir string) map[string]string {
	return map[string]string{
		"XDG_DATA_HOME": filepath.Join(dir, "data"), "XDG_STATE_HOME": filepath.Join(dir, "state"),
		"XDG_CACHE_HOME": filepath.Join(dir, "cache"), "OPENCODE_CONFIG_DIR": filepath.Join(dir, "config"), "OPENCODE_DB": "",
	}
}

// DemoHome fills h (which should be empty) with a repository at git/demo whose remote is
// github.com/example/demo, Claude Code 2.1.284 sessions, Codex 0.153.2 threads and
// OpenCode 2.3.0 sessions (DemoOpenCode) in it.
func DemoHome(h string) error {
	return demoHome(h, true)
}

// DemoHomeClaudeCodex is DemoHome without OpenCode's sessions: the world the browser specs
// are written for until they cover OpenCode (PR 8 of the OpenCode plan).
func DemoHomeClaudeCodex(h string) error {
	return demoHome(h, false)
}

func demoHome(h string, opencode bool) error {
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
	if err := copyTree(filepath.Join(repo, "agents", "codex", "testdata", "0.153.2"), filepath.Join(h, ".codex")); err != nil {
		return err
	}
	if !opencode {
		return nil
	}
	return demoOpenCode(filepath.Join(h, ".local", "share", "opencode", "opencode.db"), demo)
}

// DemoOpenCode are the OpenCode sessions of the demo home, from the 2.3.0 sandbox capture:
// a short one, one with a subagent (its child session is counted, not listed) and one whose
// reply is in session_part rows.
var DemoOpenCode = []string{
	"ses_e29aa1ec8ffes7x6RCUekHyBbf", // "renamed by api"
	"ses_e29a8385effeUhBGvRVeVFrDLo", // "subagent", with the child ses_e29a832f5ffebHEWUP5fBLspvU
	"ses_e29a5465fffek5VRD43HVvlx4P", // "part rows"
}

// demoOpenCode makes OpenCode's database at file with the DemoOpenCode sessions in demo:
// the fixture's rows, with its folder and project (OpenCode's id for the repository's
// origin) replaced by the demo's.
func demoOpenCode(file, demo string) error {
	b, err := os.ReadFile(filepath.Join(repoRoot(), "agents", "opencode", "testdata", "2.3.0", "data.sql"))
	if err != nil {
		return err
	}
	dir := strings.ReplaceAll(filepath.ToSlash(demo), "'", "''") // in the JSON columns, "/" needs no escaping
	var rows []string
	for _, l := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(l, `INSERT INTO "migration"`) {
			continue // the stand-in's schema records them
		}
		l = strings.ReplaceAll(l, "/home/u/git/demo", dir)
		rows = append(rows, strings.ReplaceAll(l, fixtureProject, demoProject))
	}
	db, err := fakeopencode.Open(file)
	if err != nil {
		return err
	}
	defer db.Close()
	if _, err := db.Exec(strings.Join(rows, "\n")); err != nil {
		return fmt.Errorf("OpenCode's demo sessions: %w", err)
	}
	// A session's folder is as OpenCode resolved it: with the OS's separators (path.resolve
	// gives "\" on Windows).
	if native := filepath.FromSlash(demo); native != filepath.ToSlash(demo) {
		if _, err := db.Exec("UPDATE session_v2 SET directory = ? WHERE directory = ?", native, filepath.ToSlash(demo)); err != nil {
			return err
		}
	}
	keep := "'" + strings.Join(append(DemoOpenCode, "ses_e29a832f5ffebHEWUP5fBLspvU"), "','") + "'"
	for _, q := range []string{
		"DELETE FROM session_part WHERE session_id NOT IN (" + keep + ")",
		"DELETE FROM session_message WHERE session_id NOT IN (" + keep + ")",
		"DELETE FROM session_v2 WHERE id NOT IN (" + keep + ")",
		"DELETE FROM worktree WHERE project_id NOT IN (SELECT project_id FROM session_v2)",
		"DELETE FROM project WHERE id NOT IN (SELECT project_id FROM session_v2)",
	} {
		if _, err := db.Exec(q); err != nil {
			return err
		}
	}
	return nil
}

// The project ids of the fixture's repository and of the demo's (OpenCode's hash of
// "git-remote:github.com/example/demo").
const (
	fixtureProject = "e1eece87d8935f099635f505ed301e85a3e1ae27"
	demoProject    = "1acf5bf348c263d377a06f8672f17cc316a9337a"
)

// repoRoot is the module's folder (this file's, two levels up).
func repoRoot() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "..")
}
