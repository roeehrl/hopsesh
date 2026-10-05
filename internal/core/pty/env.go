package pty

import (
	"os"
	"runtime"
	"strings"
)

// Env is how a tab's environment is made. By default a tab gets the user's environment
// (what a terminal they opened would have), less the variables that tell a program it runs
// inside another terminal, multiplexer or editor; Strict keeps only a short allowlist
// instead. Either way the terminal's identity is set (TERM=xterm-256color,
// COLORTERM=truecolor, TERM_PROGRAM=hopsesh), then Unset is removed and Set added.
// Nothing here injects a credential: hopsesh never adds one, and a cloud step's Unset
// removes the ones a vendor's program must not see (ANTHROPIC_API_KEY,
// CLAUDE_CODE_CHILD_SESSION).
type Env struct {
	// Base is the environment to start from (nil: this process's).
	Base []string
	// Strict keeps only the allowlist (the system's essentials, locale, temporary
	// folders, proxies and certificate settings) and the names in Keep.
	Strict bool
	// Keep are more names Strict keeps (an agent's login variables, such as
	// CLAUDE_CONFIG_DIR).
	Keep []string
	// Unset are names removed after the rest.
	Unset []string
	// Set are KEY=VALUE pairs added last; they win over anything before.
	Set []string
}

// terminalIdentity is what the tab says the terminal is. TERM must be xterm-256color: the
// emulator in the window (xterm.js) answers the device-attributes query only for an
// xterm-like name, and a program that waits for that answer would hang without it.
func terminalIdentity(version string) []string {
	if version == "" {
		version = "dev"
	}
	return []string{"TERM=xterm-256color", "COLORTERM=truecolor", "TERM_PROGRAM=hopsesh", "TERM_PROGRAM_VERSION=" + version}
}

// foreignTerminal reports whether a variable says the program runs inside another
// terminal, a multiplexer or an editor's terminal. Claude Code, for one, changes how it
// draws on TMUX, ZELLIJ, WT_SESSION and an editor's variables, which would be wrong in a
// hopsesh tab.
func foreignTerminal(name string) bool {
	n := strings.ToUpper(name)
	switch n {
	case "TERM", "COLORTERM", "TERM_PROGRAM", "TERM_PROGRAM_VERSION", "TERM_SESSION_ID",
		"TMUX", "TMUX_PANE", "ZELLIJ", "STY", "WINDOW",
		"WT_SESSION", "WT_PROFILE_ID", "LC_TERMINAL", "LC_TERMINAL_VERSION",
		"VTE_VERSION", "TERMINAL_EMULATOR", "COLORFGBG", "INSIDE_EMACS", "TERMINFO":
		return true
	}
	for _, p := range []string{"ZELLIJ_", "VSCODE_", "ITERM_", "KITTY_", "ALACRITTY_", "WEZTERM_", "GHOSTTY_", "KONSOLE_", "TERMINAL_"} {
		if strings.HasPrefix(n, p) {
			return true
		}
	}
	return false
}

// allowed is the Strict allowlist.
func allowed(name string) bool {
	n := strings.ToUpper(name)
	if strings.HasPrefix(n, "LC_") && n != "LC_TERMINAL" && n != "LC_TERMINAL_VERSION" {
		return true
	}
	switch n {
	case "PATH", "HOME", "USER", "LOGNAME", "SHELL", "LANG", "LANGUAGE", "TZ", "TMPDIR",
		"XDG_RUNTIME_DIR", "XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME", "XDG_CACHE_HOME",
		"DISPLAY", "WAYLAND_DISPLAY", "SSH_AUTH_SOCK",
		"HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY", "ALL_PROXY",
		"SSL_CERT_FILE", "SSL_CERT_DIR", "NODE_EXTRA_CA_CERTS":
		return true
	}
	if runtime.GOOS == "windows" {
		switch n {
		case "SYSTEMROOT", "SYSTEMDRIVE", "WINDIR", "COMSPEC", "PATHEXT", "USERPROFILE", "HOMEDRIVE", "HOMEPATH",
			"APPDATA", "LOCALAPPDATA", "PROGRAMDATA", "PROGRAMFILES", "PROGRAMFILES(X86)", "PROGRAMW6432",
			"COMMONPROGRAMFILES", "COMMONPROGRAMFILES(X86)", "COMMONPROGRAMW6432", "USERNAME", "USERDOMAIN",
			"COMPUTERNAME", "TEMP", "TMP", "PROCESSOR_ARCHITECTURE", "NUMBER_OF_PROCESSORS", "OS", "PUBLIC",
			"ALLUSERSPROFILE", "PSMODULEPATH":
			return true
		}
	}
	return false
}

// sameName compares variable names as the system does: case-insensitively on Windows.
func sameName(a, b string) bool {
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

// Build is the environment a tab's program gets (see Env). version is hopsesh's, for
// TERM_PROGRAM_VERSION.
func (e Env) Build(version string) []string {
	base := e.Base
	if base == nil {
		base = os.Environ()
	}
	out := make([]string, 0, len(base)+8)
	for _, kv := range base {
		k, _, ok := strings.Cut(kv, "=")
		if !ok || k == "" {
			continue
		}
		if e.Strict {
			keep := allowed(k)
			for _, name := range e.Keep {
				keep = keep || sameName(k, name)
			}
			if !keep {
				continue
			}
		} else if foreignTerminal(k) {
			continue
		}
		out = append(out, kv)
	}
	out = append(out, terminalIdentity(version)...)
	if len(e.Unset) > 0 {
		kept := out[:0]
		for _, kv := range out {
			k, _, _ := strings.Cut(kv, "=")
			drop := false
			for _, u := range e.Unset {
				drop = drop || sameName(k, u)
			}
			if !drop {
				kept = append(kept, kv)
			}
		}
		out = kept
	}
	out = append(out, e.Set...)
	return dedup(out)
}

// dedup keeps the last value of each name, in the order the names first came.
func dedup(env []string) []string {
	last := map[string]int{}
	key := func(kv string) string {
		k, _, _ := strings.Cut(kv, "=")
		if runtime.GOOS == "windows" {
			return strings.ToUpper(k)
		}
		return k
	}
	for i, kv := range env {
		last[key(kv)] = i
	}
	out := make([]string, 0, len(last))
	seen := map[string]bool{}
	for _, kv := range env {
		k := key(kv)
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, env[last[k]])
	}
	return out
}
