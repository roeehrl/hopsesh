package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/roeehrl/hopsesh/agents/claude"
	"github.com/roeehrl/hopsesh/agents/codex"
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
	home := t.TempDir()
	os.MkdirAll(filepath.Join(home, "sessions"), 0o700)
	if got := codexList(t, bin, home); strings.Contains(got, `"id"`) { // builds the index (empty)
		t.Fatalf("a fresh home lists nothing: %s", got)
	}
	m := &host.Machine{Name: "here", Local: true, Facts: host.Facts{OS: "darwin", Home: home, Env: map[string]string{"CODEX_HOME": home},
		Binaries: map[string]agent.BinaryFact{"codex": {Path: bin}}}}
	mod := codex.New()
	in := agent.Install{Agent: "codex", Version: "0.153.2", Binary: bin, Roots: map[string]string{"home": home}, Present: true}
	j, err := journal.New(t.TempDir(), "test")
	if err != nil {
		t.Fatal(err)
	}
	h, err := m.For(context.Background(), mod.Spec(), in, j)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	w, err := mod.Write(ctx, h, in, ir.WriteRequest{Mode: ir.WriteNew, Header: ir.Header{CWD: home, Title: "Find the codeword", Created: time.Now()},
		Items: []ir.Item{{Role: ir.RoleUser, Text: "What is the codeword?"}, {Role: ir.RoleAgent, Text: "PLUM-7"}}})
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
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	home := filepath.Join(root, "here", ".codex")
	os.MkdirAll(filepath.Join(home, "sessions"), 0o700)
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
	if err := j.Undo(func(string) (host.FS, error) { return host.LocalFS(), nil }); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(threads[0].Path); !os.IsNotExist(err) {
		t.Fatal("undo removes the imported thread")
	}
}
