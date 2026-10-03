// Package integrate connects hopsesh to the rest of the user's machine: the command-line
// tool on PATH (for app installs), the user's login environment, and the hopsesh skill
// for every agent. All are opt-in and reversible, and none ever replaces something
// hopsesh did not create.
package integrate

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/roeehrl/hopsesh/internal/core/proc"
)

// The login environment: apps started from Finder do not inherit the shell's PATH or
// the variables that move an agent's data folder (CLAUDE_CONFIG_DIR, CODEX_HOME).
var (
	loginMu   sync.Mutex
	loginVars map[string]string
	loginDone bool
)

func loginShell() string {
	if sh := os.Getenv("SHELL"); sh != "" {
		return sh
	}
	return "/bin/zsh"
}

// SetLoginVars names the variables (besides PATH) LoginEnv reads; call once at start-up,
// before the first LoginEnv.
func SetLoginVars(vars []string) {
	loginMu.Lock()
	defer loginMu.Unlock()
	wanted = vars
}

var wanted []string

var varName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// LoginEnv returns PATH and the SetLoginVars variables as an interactive login shell sets
// them (cached; empty values when it cannot be read).
func LoginEnv() map[string]string {
	loginMu.Lock()
	defer loginMu.Unlock()
	if loginDone {
		return loginVars
	}
	loginDone = true
	loginVars = map[string]string{}
	if runtime.GOOS == "windows" {
		return loginVars // programs inherit the user's environment there; see loginPATH
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var b strings.Builder
	b.WriteString(`printf '\n__HOPSESH__PATH=%s\n' "$PATH"`)
	for _, v := range wanted {
		if varName.MatchString(v) {
			fmt.Fprintf(&b, `; printf '__HOPSESH__%s=%%s\n' "${%s:-}"`, v, v)
		}
	}
	cmd := proc.CommandContext(ctx, loginShell(), "-lic", b.String())
	cmd.Stdin = nil
	out, err := cmd.Output()
	if err != nil && len(out) == 0 {
		return loginVars
	}
	for _, line := range strings.Split(string(out), "\n") {
		if k, v, ok := strings.Cut(strings.TrimPrefix(line, "__HOPSESH__"), "="); ok && strings.HasPrefix(line, "__HOPSESH__") {
			loginVars[k] = strings.TrimSpace(v)
		}
	}
	return loginVars
}

// AdoptLoginEnv sets each SetLoginVars variable in this process from the login shell when
// the process does not have it (an app started from Finder), so every part of hopsesh, and
// the agents it starts, use the folders the user's shell would.
func AdoptLoginEnv() { adoptLoginEnv(os.Getenv, LoginEnv) }

func adoptLoginEnv(getenv func(string) string, login func() map[string]string) {
	env := login()
	for _, v := range wanted {
		if getenv(v) == "" && env[v] != "" {
			_ = os.Setenv(v, expandHome(env[v]))
		}
	}
}

// LoginPathHas reports whether dir is on the PATH a new terminal gets.
func LoginPathHas(dir string) bool {
	for _, e := range filepath.SplitList(loginPATH()) {
		if samePath(expandHome(e), dir) {
			return true
		}
	}
	return false
}

// LookLoginPath finds a command on the PATH a new terminal gets.
func LookLoginPath(name string) string {
	for _, d := range filepath.SplitList(loginPATH()) {
		if d == "" {
			continue
		}
		if f := lookIn(expandHome(d), name); f != "" {
			return f
		}
	}
	return ""
}

func samePath(a, b string) bool {
	a, b = filepath.Clean(a), filepath.Clean(b)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

func expandHome(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		h, _ := os.UserHomeDir()
		return filepath.Join(h, strings.TrimPrefix(p, "~"))
	}
	return p
}
