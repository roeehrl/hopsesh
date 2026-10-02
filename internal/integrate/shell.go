// Package integrate connects hopsesh to the rest of the user's machine: the command-line
// tool on PATH (for app installs) and the hopsesh skill for Claude Code. Both are opt-in
// and reversible, and neither ever replaces something hopsesh did not create.
package integrate

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// loginEnv reads a few variables as the user's login shell sets them: apps started from
// Finder do not inherit the shell's PATH or CLAUDE_CONFIG_DIR.
var (
	loginOnce sync.Once
	loginVars map[string]string
)

func loginShell() string {
	if sh := os.Getenv("SHELL"); sh != "" {
		return sh
	}
	return "/bin/zsh"
}

// LoginEnv returns PATH, CLAUDE_CONFIG_DIR and HOME from an interactive login shell
// (cached; empty values when it cannot be read).
func LoginEnv() map[string]string {
	loginOnce.Do(func() {
		loginVars = map[string]string{}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		script := `printf '\n__HOPSESH__PATH=%s\n__HOPSESH__CLAUDE_CONFIG_DIR=%s\n' "$PATH" "$CLAUDE_CONFIG_DIR"`
		cmd := exec.CommandContext(ctx, loginShell(), "-lic", script)
		cmd.Stdin = nil
		out, err := cmd.Output()
		if err != nil && len(out) == 0 {
			return
		}
		for _, line := range strings.Split(string(out), "\n") {
			if k, v, ok := strings.Cut(strings.TrimPrefix(line, "__HOPSESH__"), "="); ok && strings.HasPrefix(line, "__HOPSESH__") {
				loginVars[k] = strings.TrimSpace(v)
			}
		}
	})
	return loginVars
}

// AdoptLoginEnv sets CLAUDE_CONFIG_DIR in this process from the login shell when the
// process does not have it (an app started from Finder), so every part of hopsesh, and
// the programs it starts, use the same Claude Code folder.
func AdoptLoginEnv() { adoptLoginEnv(os.Getenv, LoginEnv) }

func adoptLoginEnv(getenv func(string) string, login func() map[string]string) {
	if getenv("CLAUDE_CONFIG_DIR") != "" {
		return
	}
	if d := login()["CLAUDE_CONFIG_DIR"]; d != "" {
		_ = os.Setenv("CLAUDE_CONFIG_DIR", expandHome(d))
	}
}

// ClaudeConfigDir is Claude Code's config folder: CLAUDE_CONFIG_DIR from this process or
// the login shell, else ~/.claude.
func ClaudeConfigDir() string {
	if d := os.Getenv("CLAUDE_CONFIG_DIR"); d != "" {
		return d
	}
	if d := LoginEnv()["CLAUDE_CONFIG_DIR"]; d != "" {
		return expandHome(d)
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".claude")
}

// LoginPathHas reports whether dir is on the login shell's PATH (falling back to this
// process's PATH when the shell cannot be read).
func LoginPathHas(dir string) bool {
	p := LoginEnv()["PATH"]
	if p == "" {
		p = os.Getenv("PATH")
	}
	for _, e := range filepath.SplitList(p) {
		if filepath.Clean(expandHome(e)) == filepath.Clean(dir) {
			return true
		}
	}
	return false
}

// LookLoginPath finds a command on the login shell's PATH.
func LookLoginPath(name string) string {
	p := LoginEnv()["PATH"]
	if p == "" {
		p = os.Getenv("PATH")
	}
	for _, d := range filepath.SplitList(p) {
		f := filepath.Join(expandHome(d), name)
		if fi, err := os.Stat(f); err == nil && !fi.IsDir() && fi.Mode()&0o111 != 0 {
			return f
		}
	}
	return ""
}

func expandHome(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		h, _ := os.UserHomeDir()
		return filepath.Join(h, strings.TrimPrefix(p, "~"))
	}
	return p
}
