package presence

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// chain builds a table where each process is the parent of the next: names[0] is the
// topmost (pid 100, parent launchd), the last is the session (the highest pid).
func chain(names ...string) (Table, int) {
	ps := []Proc{{PID: 1, Name: "launchd"}}
	for i, n := range names {
		ps = append(ps, Proc{PID: 100 + i, PPID: max(1, 99+i), Name: n})
	}
	ps[1].PPID = 1
	return FromProcs(ps), 99 + len(names)
}

func TestClassify(t *testing.T) {
	cases := []struct {
		name  string
		chain []string
		want  Host
	}{
		{"iTerm2", []string{"iTerm2", "login", "-zsh", "claude"}, Host{KindITerm2, "iTerm2", 100}},
		{"iTerm2 session server, cut to 16 bytes", []string{"iTermServer-3.6.", "zsh", "claude"}, Host{KindITerm2, "iTerm2", 100}},
		{"macOS Terminal", []string{"Terminal", "login", "zsh", "2.1.289"}, Host{KindTerminal, "Terminal", 100}},
		{"tmux inside iTerm2 is tmux", []string{"iTerm2", "zsh", "tmux", "zsh", "claude"}, Host{KindTmux, "tmux", 102}},
		{"Linux tmux server", []string{"systemd", "tmux: server", "bash", "claude"}, Host{KindTmux, "tmux", 101}},
		{"ssh login", []string{"sshd", "sshd-session", "sshd-session", "bash", "codex"}, Host{KindSSH, "ssh", 100}},
		{"VS Code terminal over ssh: the nearest wins", []string{"Code", "Code Helper", "zsh", "ssh", "sshd-session", "bash", "claude"}, Host{KindSSH, "ssh", 104}},
		{"VS Code integrated terminal climbs to the app", []string{"Code", "Code Helper", "zsh", "claude"}, Host{KindIDE, "VS Code", 100}},
		{"VS Code extension host, cut", []string{"Code", "Code Helper (Plu", "claude"}, Host{KindIDE, "VS Code", 100}},
		{"Cursor helper, cut", []string{"Cursor", "Cursor Helper (P", "zsh", "claude"}, Host{KindIDE, "Cursor", 100}},
		{"Windsurf helper alone", []string{"Windsurf Helper ", "zsh", "claude"}, Host{KindIDE, "Windsurf", 100}},
		{"VS Code Insiders, cut", []string{"Code - Insiders ", "zsh", "claude"}, Host{KindIDE, "VS Code Insiders", 100}},
		{"Linux VS Code", []string{"code", "code", "bash", "claude"}, Host{KindIDE, "VS Code", 100}},
		{"Zed", []string{"zed", "zsh", "claude"}, Host{KindIDE, "Zed", 100}},
		{"JetBrains", []string{"goland", "zsh", "claude"}, Host{KindIDE, "GoLand", 100}},
		{"Claude app", []string{"Claude", "disclaimer", "claude"}, Host{KindClaudeApp, "Claude", 100}},
		{"Claude app helper", []string{"Claude", "Claude Helper", "zsh", "claude"}, Host{KindClaudeApp, "Claude", 100}},
		{"a claude command-line tool is not the app", []string{"iTerm2", "zsh", "claude", "bash", "claude"}, Host{KindITerm2, "iTerm2", 100}},
		{"ChatGPT runs Codex", []string{"ChatGPT", "codex"}, Host{KindCodexApp, "ChatGPT", 100}},
		{"Codex app", []string{"Codex", "codex"}, Host{KindCodexApp, "Codex", 100}},
		{"hopsesh's app by name", []string{"hopsesh-app", "2.1.289"}, Host{KindHopsesh, "hopsesh", 100}},
		{"Windows Terminal", []string{"explorer.exe", "WindowsTerminal.exe", "pwsh.exe", "claude.exe"}, Host{KindWT, "Windows Terminal", 101}},
		{"Windows names ignore case", []string{"WINDOWSTERMINAL.EXE", "cmd.exe", "node.exe", "codex.exe"}, Host{KindWT, "Windows Terminal", 100}},
		{"OpenConsole under Windows Terminal", []string{"WindowsTerminal.exe", "OpenConsole.exe", "pwsh.exe", "claude.exe"}, Host{KindWT, "Windows Terminal", 100}},
		{"OpenConsole elsewhere is not Windows Terminal", []string{"Code.exe", "OpenConsole.exe", "pwsh.exe", "claude.exe"}, Host{KindIDE, "VS Code", 100}},
		{"Windows hopsesh", []string{"hopsesh-app.exe", "claude.exe"}, Host{KindHopsesh, "hopsesh", 100}},
		{"Windows claude.exe is not taken for the app", []string{"WindowsTerminal.exe", "pwsh.exe", "claude.exe", "claude.exe"}, Host{KindWT, "Windows Terminal", 100}},
		{"a Windows name is not a Unix one", []string{"terminal.exe", "claude.exe"}, Host{Kind: KindUnknown}},
		{"nothing known", []string{"launchd", "zsh", "claude"}, Host{Kind: KindUnknown}},
		{"a short name is not cut", []string{"Cod", "claude"}, Host{Kind: KindUnknown}},
	}
	for _, c := range cases {
		tb, pid := chain(c.chain...)
		if got := tb.Classify(pid, 0); got != c.want {
			t.Errorf("%s: %+v, want %+v", c.name, got, c.want)
		}
	}
}

func TestClassifySelfAndBounds(t *testing.T) {
	// hopsesh's own pid, whatever its name (a development build).
	tb, pid := chain("iTerm2", "zsh", "go-build-main", "zsh", "claude")
	if got := tb.Classify(pid, 102); got != (Host{KindHopsesh, "hopsesh", 102}) {
		t.Errorf("self: %+v", got)
	}
	// An unknown pid, a missing parent.
	if got := tb.Classify(9999, 0); got.Kind != KindUnknown {
		t.Errorf("unknown pid: %+v", got)
	}
	tb = FromProcs([]Proc{{PID: 10, PPID: 20, Name: "claude"}})
	if got := tb.Classify(10, 0); got != (Host{Kind: KindUnknown}) {
		t.Errorf("missing parent: %+v", got)
	}
	// A cycle (Windows reuses a dead parent's pid), and one through the session itself.
	tb = FromProcs([]Proc{{PID: 10, PPID: 20, Name: "claude.exe"}, {PID: 20, PPID: 30, Name: "pwsh.exe"}, {PID: 30, PPID: 20, Name: "cmd.exe"}})
	if got := tb.Classify(10, 0); got.Kind != KindUnknown {
		t.Errorf("cycle: %+v", got)
	}
	tb = FromProcs([]Proc{{PID: 10, PPID: 20, Name: "claude"}, {PID: 20, PPID: 10, Name: "zsh"}})
	if got := tb.Classify(10, 0); got.Kind != KindUnknown {
		t.Errorf("cycle through the session: %+v", got)
	}
	// A parent that is its own parent above a known app stops the climb.
	tb = FromProcs([]Proc{{PID: 10, PPID: 20, Name: "claude"}, {PID: 20, PPID: 30, Name: "Code Helper"}, {PID: 30, PPID: 30, Name: "Code"}})
	if got := tb.Classify(10, 0); got != (Host{KindIDE, "VS Code", 30}) {
		t.Errorf("self-parented app: %+v", got)
	}
	// The walk is bounded: a host further up than maxDepth is not found.
	names := []string{"iTerm2"}
	for range maxDepth + 5 {
		names = append(names, "zsh")
	}
	tb, pid = chain(append(names, "claude")...)
	if got := tb.Classify(pid, 0); got.Kind != KindUnknown {
		t.Errorf("depth bound: %+v", got)
	}
	tb, pid = chain(append(names[len(names)-maxDepth+1:], "claude")...)
	tb[100] = Proc{PID: 100, PPID: 1, Name: "iTerm2"}
	if got := tb.Classify(pid, 0); got.Kind != KindITerm2 {
		t.Errorf("within the bound: %+v", got)
	}
}

// A /proc tree: names with spaces and parentheses, folders that are not processes, a
// process that vanished, a stat file that is not one.
func TestReadProc(t *testing.T) {
	root := t.TempDir()
	put := func(dir, stat string) {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatal(err)
		}
		if stat != "" {
			if err := os.WriteFile(filepath.Join(root, dir, "stat"), []byte(stat), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	put("1", "1 (systemd) S 0 1 1 0 -1 4194560 0 0\n")
	put("812", "812 (tmux: server) S 1 812 812 0 -1 0\n")
	put("900", "900 (weird) name)) R 812 900 900 34816 900 0\n")
	put("901", "901 (bash) S 900 901 901 34817 901\n")
	put("950", "") // exited while read
	put("960", "garbage")
	put("970", "971 (other) S 1 1\n") // not this folder's process
	put("self", "901 (bash) S 900\n")
	put("net", "")
	prev := procRoot
	procRoot = root
	defer func() { procRoot = prev }()
	tb, err := readProc(context.Background(), procRoot)
	if err != nil {
		t.Fatal(err)
	}
	want := Table{
		1:   {PID: 1, PPID: 0, Name: "systemd"},
		812: {PID: 812, PPID: 1, Name: "tmux: server"},
		900: {PID: 900, PPID: 812, Name: "weird) name)"},
		901: {PID: 901, PPID: 900, Name: "bash"},
	}
	if len(tb) != len(want) {
		t.Fatalf("table %+v, want %+v", tb, want)
	}
	for pid, p := range want {
		if tb[pid] != p {
			t.Errorf("pid %d: %+v, want %+v", pid, tb[pid], p)
		}
	}
	if got := tb.Classify(901, 0); got != (Host{KindTmux, "tmux", 812}) {
		t.Errorf("classify: %+v", got)
	}
}

// On this machine, the table has this test's process with its parent.
func TestSnapshot(t *testing.T) {
	switch runtime.GOOS {
	case "darwin", "linux", "windows":
	default:
		t.Skip("no process table on " + runtime.GOOS)
	}
	start := time.Now()
	tb, err := Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%d processes in %v", len(tb), time.Since(start))
	me, ok := tb[os.Getpid()]
	if !ok || me.PPID != os.Getppid() || me.Name == "" {
		t.Fatalf("this process: %+v (ok %v), want parent %d", me, ok, os.Getppid())
	}
	if _, ok := tb[os.Getppid()]; !ok {
		t.Errorf("the parent %d is missing", os.Getppid())
	}
}

func BenchmarkSnapshot(b *testing.B) {
	for b.Loop() {
		if _, err := Snapshot(context.Background()); err != nil {
			b.Fatal(err)
		}
	}
}
