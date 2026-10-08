package e2e

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/roeehrl/hopsesh/agents/claude"
	"github.com/roeehrl/hopsesh/agents/codex"
	"github.com/roeehrl/hopsesh/internal/core/convert"
	"github.com/roeehrl/hopsesh/internal/core/host"
	"github.com/roeehrl/hopsesh/internal/core/journal"
	"github.com/roeehrl/hopsesh/internal/core/move"
	"github.com/roeehrl/hopsesh/sdk/agent"
	"github.com/roeehrl/hopsesh/sdk/ir"
)

// realAgents skips unless HOPSESH_REAL_AGENTS=1: these tests run the agents installed on
// this machine (never against their real data folders).
func realAgents(t *testing.T, bin string) string {
	t.Helper()
	if os.Getenv("HOPSESH_REAL_AGENTS") != "1" {
		t.Skip("set HOPSESH_REAL_AGENTS=1 to run against the installed agents")
	}
	p, err := exec.LookPath(bin)
	if err != nil {
		t.Skipf("%s is not installed", bin)
	}
	return p
}

// codexHome prepares a Codex home for a real Codex to run in, and returns it. Its config
// turns plugins off: with them on, every Codex start clones OpenAI's plugin catalogue into
// <home>/.tmp in the background, through a git that outlives a one-shot app-server, so
// t.TempDir's cleanup raced the clone ("directory not empty"). If a Codex starts that sync
// anyway, the test fails here and says so, instead of failing only when the clone is slow.
func codexHome(t *testing.T, home string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(home, "sessions"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte("[features]\nplugins = false\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { // runs before the TempDir holding home is removed
		if m, _ := filepath.Glob(filepath.Join(home, ".tmp", "plugins*")); len(m) > 0 {
			t.Errorf("Codex synced its plugin catalogue with plugins turned off: %v", m)
		}
	})
	return home
}

// codexList lists threads the way `codex resume` does: from Codex's index only.
func codexList(t *testing.T, bin, home string) string {
	t.Helper()
	in := `{"id":1,"method":"initialize","params":{"clientInfo":{"name":"hopsesh-test","version":"1"}}}` + "\n" +
		`{"method":"initialized"}` + "\n" + `{"id":2,"method":"thread/list","params":{"useStateDbOnly":true}}` + "\n"
	cmd := exec.Command("sh", "-c", `{ cat; sleep 3; } | "$0" app-server`, bin)
	cmd.Env = append(os.Environ(), "CODEX_HOME="+home)
	cmd.Stdin = strings.NewReader(in)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("codex app-server: %v", err)
	}
	for _, l := range bytes.Split(out, []byte("\n")) {
		var r struct {
			ID     int             `json:"id"`
			Result json.RawMessage `json:"result"`
		}
		if json.Unmarshal(l, &r) == nil && r.ID == 2 {
			return string(r.Result)
		}
	}
	t.Fatalf("no thread/list answer: %s", out)
	return ""
}

// A thread hopsesh writes into a Codex home whose index is already built shows up in
// Codex's own list, under its name, once installed.
func TestCodexListsWrittenThread(t *testing.T) {
	bin := realAgents(t, "codex")
	home := codexHome(t, t.TempDir())
	if got := codexList(t, bin, home); strings.Contains(got, `"id"`) { // builds the index (empty)
		t.Fatalf("a fresh home lists nothing: %s", got)
	}
	m := &host.Machine{Name: "here", Local: true, Facts: host.Facts{OS: runtime.GOOS, Home: home, Env: map[string]string{"CODEX_HOME": home},
		Binaries: map[string]agent.BinaryFact{"codex": {Path: bin}}}}
	mod := codex.New()
	in := agent.Install{Agent: "codex", Version: "0.153.2", Binary: bin, Roots: map[string]string{"home": home}, Present: true}
	j, err := journal.New(t.TempDir(), journal.KindMove, "test")
	if err != nil {
		t.Fatal(err)
	}
	h, err := m.For(context.Background(), mod.Spec(), in, j)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	nodes := []ir.Node{{Kind: ir.KindMessage, Actor: ir.User, Text: "What is the codeword?"}, {Kind: ir.KindMessage, Actor: ir.Agent, Text: "PLUM-7"}}
	ir.Chain(nodes, "")
	prepared := convert.Render(convert.Request{Nodes: nodes, From: "Claude Code", To: "Codex", Fidelity: convert.History, Window: 64000})
	w, err := mod.Write(ctx, h, in, ir.WriteRequest{Mode: ir.WriteNew, Header: ir.Header{CWD: home, Title: "Find the codeword", Created: time.Now()}, Items: prepared.Items})
	if err != nil {
		t.Fatal(err)
	}
	if got := codexList(t, bin, home); strings.Contains(got, w.SessionID) {
		t.Skip("this Codex lists new rollouts by itself; nothing to prove")
	}
	key := agent.SessionKey{Agent: "codex", Session: agent.SessionID(w.SessionID)}
	if err := mod.AfterInstall(ctx, h, in, key, agent.Placement{Key: key, Name: "Find the codeword"}); err != nil {
		t.Fatal(err)
	}
	got := codexList(t, bin, home)
	if !strings.Contains(got, w.SessionID) || !strings.Contains(got, "Find the codeword") {
		t.Fatalf("Codex's own list must show the thread under its name: %s", got)
	}
}

// Continuing a Claude Code session in Codex through Codex's own importer: the history comes
// from the importer, hopsesh's briefing is added, and undo removes the imported thread.
func TestCodexImportRoute(t *testing.T) {
	bin := realAgents(t, "codex")
	root := t.TempDir()
	here := newLocation(t, "here", root)
	seed(t, here)
	// Codex's importer takes only sessions it finds itself, in the Claude Code folder of
	// the user it runs as.
	t.Setenv("HOME", here.m.Facts.Home)
	t.Setenv("USERPROFILE", here.m.Facts.Home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	home := codexHome(t, filepath.Join(root, "here", ".codex"))
	ci := agent.Install{Agent: "codex", Version: "0.153.2", Binary: bin, Roots: map[string]string{"home": home}, Present: true}
	here.m.Facts.Binaries["codex"] = agent.BinaryFact{Path: bin}
	in := move.Input{Source: move.Side{Machine: here.m, Module: claude.New(), Install: here.in}, Session: list(t, here)[sid],
		Target: move.Side{Machine: here.m, Module: codex.New(), Install: ci}}
	ctx := context.Background()
	p, err := move.Build(ctx, in, move.Options{Via: move.ViaImport, Mark: true})
	if err != nil || len(p.Blockers) > 0 || p.Continue.Via != move.ViaImport {
		t.Fatalf("plan: %v %v", err, p)
	}
	env := move.Env{StateDir: t.TempDir()}
	res, err := move.Apply(ctx, p, in, env)
	if err != nil {
		t.Fatal(err)
	}
	threads := listAgent(t, here, codex.New(), ci)
	if len(threads) != 1 {
		t.Fatalf("one imported thread: %+v", threads)
	}
	seg := readAll(t, here, codex.New(), ci, threads[0])
	if !mentions(seg, "PLUM-7") || !mentions(seg, "moved from Claude Code") {
		t.Fatalf("the importer's history and hopsesh's briefing: %+v", seg.Nodes)
	}
	if !strings.Contains(res.Command, string(threads[0].Key.Session)) {
		t.Fatalf("the command resumes the imported thread: %s", res.Command)
	}
	j, err := journal.Load(env.StateDir, res.Journal)
	if err != nil {
		t.Fatal(err)
	}
	if err := j.Undo(ctx, journal.Files(func(string) (host.FS, error) { return host.LocalFS(), nil }), false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(threads[0].Path); !os.IsNotExist(err) {
		t.Fatal("undo removes the imported thread")
	}
}

// Codex reports its login itself; a fresh home is not logged in.
func TestCodexAccount(t *testing.T) {
	bin := realAgents(t, "codex")
	home := codexHome(t, t.TempDir())
	m := &host.Machine{Name: "here", Local: true, Facts: host.Facts{OS: runtime.GOOS, Home: home, Env: map[string]string{"CODEX_HOME": home},
		Binaries: map[string]agent.BinaryFact{"codex": {Path: bin}}}}
	in := agent.Install{Agent: "codex", Version: "0.153.2", Binary: bin, Roots: map[string]string{"home": home}, Present: true}
	h, err := m.For(context.Background(), codex.New().Spec(), in, nil)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	a, err := codex.New().Account(context.Background(), h, in)
	if err != nil || a.Key != "" || a.Label != "not logged in" {
		t.Fatalf("account: %+v %v", a, err)
	}
	if time.Since(start) > 15*time.Second {
		t.Fatal("the probe should end as soon as Codex answers")
	}
}

// Quitting a real Codex TUI that has a thread open: hopsesh finds the process holding the
// thread's writer lock, checks it is codex and idle, and quits it; the rollout stays whole.
func TestCodexStop(t *testing.T) {
	bin := realAgents(t, "codex")
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 drives the TUI")
	}
	home, cwd := codexHome(t, t.TempDir()), t.TempDir()
	login := exec.Command(bin, "login", "--with-api-key")
	login.Env = append(os.Environ(), "CODEX_HOME="+home)
	login.Stdin = strings.NewReader("sk-hopsesh-test-not-a-real-key") // never used: no prompt is sent
	if out, err := login.CombinedOutput(); err != nil {
		t.Fatalf("login: %v %s", err, out)
	}
	m := &host.Machine{Name: "here", Local: true, Facts: host.Facts{OS: runtime.GOOS, Home: home, Env: map[string]string{"CODEX_HOME": home},
		Binaries: map[string]agent.BinaryFact{"codex": {Path: bin}}}}
	mod := codex.New()
	in := agent.Install{Agent: "codex", Version: "0.153.2", Binary: bin, Roots: map[string]string{"home": home}, Present: true}
	j, _ := journal.New(t.TempDir(), journal.KindMove, "test")
	h, _ := m.For(context.Background(), mod.Spec(), in, j)
	ctx := context.Background()
	w, err := mod.Write(ctx, h, in, ir.WriteRequest{Mode: ir.WriteNew, Header: ir.Header{CWD: cwd, Title: "Stop me", Created: time.Now()},
		Items: []ir.Item{{Role: ir.RoleUser, Text: "What is the codeword?"}, {Role: ir.RoleAgent, Text: "PLUM-7"}}})
	if err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(w.Path)
	tui := exec.Command(py, "testdata/codex_tui.py", bin, home, w.SessionID, cwd)
	out, _ := tui.StdoutPipe()
	if err := tui.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tui.Process.Kill(); _ = tui.Wait() }()
	line, _ := bufio.NewReader(out).ReadString('\n')
	if !strings.HasPrefix(line, "open ") {
		t.Fatalf("Codex did not open the thread: %q", line)
	}
	sid := agent.SessionID(w.SessionID)
	if live, _ := mod.Live(ctx, h, in, []agent.SessionID{sid}); live[sid].State != agent.Live || live[sid].Status != "idle" {
		t.Fatalf("live: %+v", live[sid])
	}
	s := agent.Summary{Key: agent.SessionKey{Agent: "codex", Session: sid}, Path: w.Path}
	if err := mod.Stop(ctx, h, in, s, 10*time.Second); err != nil {
		t.Fatal(err)
	}
	if live, _ := mod.Live(ctx, h, in, []agent.SessionID{sid}); live[sid].State != agent.Ended {
		t.Fatalf("after stop: %+v", live[sid])
	}
	after, _ := os.ReadFile(w.Path)
	if !strings.HasPrefix(string(after), string(before)) {
		t.Fatal("the rollout must stay whole")
	}
}
