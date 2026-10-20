// Package presence says where a running session on this machine is open: in a tab of
// hopsesh's own terminal, a terminal app, an editor, tmux, an ssh login or an agent's
// desktop app. It reads one snapshot of the process table and walks up from the session's
// process to the first ancestor it knows; it never asks a terminal (no Apple Events), so
// it is cheap enough to run on every refresh.
package presence

import "strings"

// Proc is one process in a snapshot of this machine's process table.
type Proc struct {
	PID, PPID int
	// Name is the program's name ("iTerm2", "tmux", "Code Helper (Plugin)",
	// "WindowsTerminal.exe"), as the system keeps it: cut to 16 bytes on macOS and 15 on
	// Linux.
	Name string
}

// Table is a snapshot of the process table, by pid.
type Table map[int]Proc

// FromProcs builds a table from a list of processes (for tests, and for tables read
// elsewhere).
func FromProcs(ps []Proc) Table {
	t := make(Table, len(ps))
	for _, p := range ps {
		t[p.PID] = p
	}
	return t
}

// Kind is where a session's process runs.
type Kind string

const (
	KindHopsesh   Kind = "hopsesh" // a tab of hopsesh's own Terminal window
	KindITerm2    Kind = "iterm2"
	KindTerminal  Kind = "terminal" // macOS Terminal
	KindWT        Kind = "wt"       // Windows Terminal
	KindClaudeApp Kind = "claude-app"
	KindCodexApp  Kind = "codex-app"
	KindIDE       Kind = "ide" // VS Code, Cursor, Windsurf, Zed, JetBrains… (Host.App names it)
	KindTmux      Kind = "tmux"
	KindSSH       Kind = "ssh"
	KindUnknown   Kind = "unknown"
)

// Host is what runs a process: its kind, the app's name for people ("iTerm2", "VS Code",
// "Cursor"), and the host process's pid (0 when none was found). For an app with helper
// processes the pid is the topmost of them in the chain (VS Code itself, not its Code
// Helper); a host that detaches from its app (iTerm2's iTermServer, a tmux server) gives
// its own pid.
type Host struct {
	Kind Kind
	App  string
	PID  int
}

// maxDepth bounds the walk up the process tree.
const maxDepth = 64

// Classify walks up from pid's parent to the first ancestor it knows: hopsesh itself
// (self, or any process named like hopsesh's app), iTerm2, Terminal, tmux, an ssh login,
// an editor, Windows Terminal, the Claude app or the Codex app. The nearest one wins (a
// session in tmux inside iTerm2 is tmux's). The walk is bounded and safe against cycles
// (Windows keeps a parent's pid after the parent exits, and it can be reused). Nothing
// known gives KindUnknown.
func (t Table) Classify(pid, self int) Host {
	p, ok := t[pid]
	if !ok {
		return Host{Kind: KindUnknown}
	}
	seen := map[int]bool{pid: true}
	cur := p.PPID
	for range maxDepth {
		if cur <= 0 || seen[cur] {
			break
		}
		seen[cur] = true
		q, ok := t[cur]
		if !ok {
			break
		}
		if self > 0 && cur == self {
			return Host{Kind: KindHopsesh, App: "hopsesh", PID: cur}
		}
		if h, ok := t.known(q); ok {
			return t.topmost(h, q, seen)
		}
		cur = q.PPID
	}
	return Host{Kind: KindUnknown}
}

// known says what q is, if it is a host. Windows Terminal's OpenConsole is one only under
// Windows Terminal (elsewhere it is any console's helper, and the walk goes on).
func (t Table) known(q Proc) (Host, bool) {
	r, ok := lookup(q.Name)
	if !ok {
		return Host{}, false
	}
	if r.underWT {
		parent, ok := t[q.PPID]
		if !ok {
			return Host{}, false
		}
		if pr, ok := lookup(parent.Name); !ok || pr.kind != KindWT || pr.underWT {
			return Host{}, false
		}
		return Host{Kind: KindWT, App: r.app, PID: parent.PID}, true
	}
	return Host{Kind: r.kind, App: r.app, PID: q.PID}, true
}

// topmost climbs from the matched process q through ancestors of the same kind and app
// (Code Helper → Code), so the pid is the app's main process when the chain reaches it.
func (t Table) topmost(h Host, q Proc, seen map[int]bool) Host {
	for range maxDepth {
		up, ok := t[q.PPID]
		if !ok || q.PPID <= 0 || seen[q.PPID] {
			break
		}
		seen[q.PPID] = true
		r, ok := lookup(up.Name)
		if !ok || r.kind != h.Kind || r.app != h.App || r.underWT {
			break
		}
		h.PID, q = up.PID, up
	}
	return h
}

// rule is one known program. Names are matched exactly, or as a prefix when prefix is
// set (helper families: "Code Helper (Plugin)", "tmux: server").
type rule struct {
	name   string
	prefix bool
	kind   Kind
	app    string
	// underWT: only a host when its parent is Windows Terminal (OpenConsole).
	underWT bool
}

// unixNames are macOS and Linux program names, matched case-sensitively: the Claude and
// Codex apps are "Claude" and "Codex", their command-line tools "claude" and "codex".
var unixNames = []rule{
	{name: "hopsesh-app", kind: KindHopsesh, app: "hopsesh"},
	{name: "iTerm2", kind: KindITerm2, app: "iTerm2"},
	{name: "iTermServer", prefix: true, kind: KindITerm2, app: "iTerm2"}, // iTermServer-3.6.4
	{name: "Terminal", kind: KindTerminal, app: "Terminal"},
	{name: "tmux", prefix: true, kind: KindTmux, app: "tmux"}, // "tmux", Linux "tmux: server"
	{name: "sshd", prefix: true, kind: KindSSH, app: "ssh"},   // sshd, sshd-session
	{name: "mosh-server", kind: KindSSH, app: "mosh"},
	{name: "Claude", kind: KindClaudeApp, app: "Claude"},
	{name: "Claude Helper", prefix: true, kind: KindClaudeApp, app: "Claude"},
	{name: "Codex", kind: KindCodexApp, app: "Codex"},
	{name: "Codex Helper", prefix: true, kind: KindCodexApp, app: "Codex"},
	{name: "Codex (", prefix: true, kind: KindCodexApp, app: "Codex"}, // Codex (Service), Codex (Renderer)
	{name: "ChatGPT", kind: KindCodexApp, app: "ChatGPT"},             // the ChatGPT app runs Codex
	// Editors: the macOS app and its helpers, then the Linux program.
	{name: "Code", kind: KindIDE, app: "VS Code"},
	{name: "Code Helper", prefix: true, kind: KindIDE, app: "VS Code"},
	{name: "code", kind: KindIDE, app: "VS Code"},
	{name: "Code - Insiders", prefix: true, kind: KindIDE, app: "VS Code Insiders"},
	{name: "code-insiders", kind: KindIDE, app: "VS Code Insiders"},
	{name: "VSCodium", prefix: true, kind: KindIDE, app: "VSCodium"},
	{name: "codium", kind: KindIDE, app: "VSCodium"},
	{name: "Cursor", kind: KindIDE, app: "Cursor"},
	{name: "Cursor Helper", prefix: true, kind: KindIDE, app: "Cursor"},
	{name: "cursor", kind: KindIDE, app: "Cursor"},
	{name: "Windsurf", prefix: true, kind: KindIDE, app: "Windsurf"},
	{name: "windsurf", kind: KindIDE, app: "Windsurf"},
	{name: "zed", kind: KindIDE, app: "Zed"},
	{name: "zed-editor", kind: KindIDE, app: "Zed"},
	{name: "idea", kind: KindIDE, app: "IntelliJ IDEA"},
	{name: "goland", kind: KindIDE, app: "GoLand"},
	{name: "pycharm", kind: KindIDE, app: "PyCharm"},
	{name: "webstorm", kind: KindIDE, app: "WebStorm"},
	{name: "clion", kind: KindIDE, app: "CLion"},
	{name: "rider", kind: KindIDE, app: "Rider"},
	{name: "phpstorm", kind: KindIDE, app: "PhpStorm"},
	{name: "rubymine", kind: KindIDE, app: "RubyMine"},
	{name: "datagrip", kind: KindIDE, app: "DataGrip"},
	{name: "rustrover", kind: KindIDE, app: "RustRover"},
	{name: "studio", kind: KindIDE, app: "Android Studio"},
}

// windowsNames are Windows program names without ".exe", lower case (Windows names match
// case-insensitively). The Claude and Codex apps are left out: their programs are named
// like the command-line tools (claude.exe, codex.exe), so a name cannot tell them apart;
// LiveInfo.App says when the Claude app runs a session. The ChatGPT app, which runs Codex,
// is known.
var windowsNames = []rule{
	{name: "hopsesh-app", kind: KindHopsesh, app: "hopsesh"},
	{name: "windowsterminal", kind: KindWT, app: "Windows Terminal"},
	{name: "openconsole", kind: KindWT, app: "Windows Terminal", underWT: true},
	{name: "tmux", kind: KindTmux, app: "tmux"},
	{name: "sshd", prefix: true, kind: KindSSH, app: "ssh"},
	{name: "chatgpt", kind: KindCodexApp, app: "ChatGPT"},
	{name: "code", kind: KindIDE, app: "VS Code"},
	{name: "code - insiders", kind: KindIDE, app: "VS Code Insiders"},
	{name: "vscodium", kind: KindIDE, app: "VSCodium"},
	{name: "cursor", kind: KindIDE, app: "Cursor"},
	{name: "windsurf", kind: KindIDE, app: "Windsurf"},
	{name: "zed", kind: KindIDE, app: "Zed"},
	{name: "idea64", kind: KindIDE, app: "IntelliJ IDEA"},
	{name: "goland64", kind: KindIDE, app: "GoLand"},
	{name: "pycharm64", kind: KindIDE, app: "PyCharm"},
	{name: "webstorm64", kind: KindIDE, app: "WebStorm"},
	{name: "clion64", kind: KindIDE, app: "CLion"},
	{name: "rider64", kind: KindIDE, app: "Rider"},
	{name: "phpstorm64", kind: KindIDE, app: "PhpStorm"},
	{name: "rubymine64", kind: KindIDE, app: "RubyMine"},
	{name: "datagrip64", kind: KindIDE, app: "DataGrip"},
	{name: "rustrover64", kind: KindIDE, app: "RustRover"},
	{name: "studio64", kind: KindIDE, app: "Android Studio"},
}

// cut is the shortest length at which a name may have been cut by the system (Linux keeps
// 15 bytes, macOS 16): such a name also matches a known name it begins.
const cut = 15

// lookup finds the rule for a program name. A name ending in ".exe" is a Windows name.
func lookup(name string) (rule, bool) {
	rules := unixNames
	if n := strings.ToLower(name); strings.HasSuffix(n, ".exe") {
		name, rules = strings.TrimSuffix(n, ".exe"), windowsNames
	}
	if name == "" {
		return rule{}, false
	}
	for _, r := range rules {
		if name == r.name || r.prefix && strings.HasPrefix(name, r.name) ||
			len(name) >= cut && strings.HasPrefix(r.name, name) {
			return r, true
		}
	}
	return rule{}, false
}

// Ancestors retains only the bounded process chains needed to classify sessions.
// Local IPC does not need unrelated process names or any command-line arguments.
func (t Table) Ancestors(pids []int) Table {
	out := Table{}
	for _, pid := range pids {
		seen := map[int]bool{}
		for range maxDepth {
			p, ok := t[pid]
			if !ok || pid <= 0 || seen[pid] {
				break
			}
			seen[pid] = true
			out[pid] = p
			pid = p.PPID
		}
	}
	return out
}
