package e2e

import (
	"bytes"
	"context"
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
	"github.com/roeehrl/hopsesh/sdk/agent"
	"github.com/roeehrl/hopsesh/sdk/ir"
)

// A Claude Code session changes folder the way /cd does: the transcript and its side folder
// move to the new folder's project folder, one relocated record follows every byte that was
// there, the id stays, and undo puts it all back.
func TestClaudeChangeDirKeepsTheSession(t *testing.T) {
	root := t.TempDir()
	l := newLocation(t, "here", root)
	seed(t, l)
	other := filepath.Join(filepath.Dir(l.repo), "demo-2")
	if err := os.MkdirAll(other, 0o700); err != nil {
		t.Fatal(err)
	}
	other, _ = filepath.EvalSymlinks(other)
	before := list(t, l)[sid]
	side := filepath.Join(filepath.Dir(before.Path), sid)
	if err := os.MkdirAll(filepath.Join(side, "subagents"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(side, "subagents", "agent-a.jsonl"), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	orig, _ := os.ReadFile(before.Path)

	ctx := context.Background()
	m := claude.New()
	state := t.TempDir()
	j, err := journal.New(state, journal.KindChangeDir, "cd")
	if err != nil {
		t.Fatal(err)
	}
	h, err := l.m.For(ctx, m.Spec(), l.in, j)
	if err != nil {
		t.Fatal(err)
	}
	p, err := m.PlanChangeDir(ctx, h, l.in, before, other)
	if err != nil || len(p.Blockers) > 0 || p.Method != agent.DirRecord {
		t.Fatalf("plan: %+v %v", p, err)
	}
	if err := m.ChangeDir(ctx, h, l.in, before, p); err != nil {
		t.Fatal(err)
	}
	after := list(t, l)[sid]
	want := filepath.Join(l.in.Root("home"), "projects", claude.Slug(other), sid+".jsonl")
	if after.CWD != other || after.Path != want {
		t.Fatalf("after: %s in %s, want %s in %s", after.Path, after.CWD, want, other)
	}
	b, _ := os.ReadFile(after.Path)
	rec := `{"type":"relocated","relocatedCwd":"` + jsonText(other) + `","sessionId":"` + sid + `"}` + "\n"
	if !bytes.HasPrefix(b, orig) || string(b[len(orig):]) != rec && string(b[len(orig):]) != "\n"+rec {
		t.Fatalf("the transcript must keep every byte and add one relocated record; added %q", b[len(orig):])
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(want), sid, "subagents", "agent-a.jsonl")); err != nil {
		t.Fatalf("the side folder moves with it: %v", err)
	}
	if _, err := os.Stat(before.Path); !os.IsNotExist(err) {
		t.Fatalf("the old transcript must be gone: %v", err)
	}
	// Already there, or already taken: refused.
	if p, _ := m.PlanChangeDir(ctx, h, l.in, after, other); len(p.Moves) != 0 {
		t.Fatalf("the same folder moves nothing: %+v", p)
	}

	if err := j.Undo(ctx, journal.Files(func(string) (host.FS, error) { return host.LocalFS(), nil }), false); err != nil {
		t.Fatal(err)
	}
	back, _ := os.ReadFile(before.Path)
	if !bytes.Equal(back, orig) || list(t, l)[sid].CWD != l.repo {
		t.Fatal("undo puts the transcript back as it was")
	}
	if _, err := os.Stat(filepath.Join(side, "subagents", "agent-a.jsonl")); err != nil {
		t.Fatalf("undo puts the side folder back: %v", err)
	}
	if got := m.InSession(l.in, other); got != "/cd "+other {
		t.Fatalf("in session: %q", got)
	}
}

// A session in a folder Claude Code manages as a worktree is left to Claude Code.
func TestClaudeChangeDirLeavesClaudeWorktrees(t *testing.T) {
	root := t.TempDir()
	l := newLocation(t, "here", root)
	seed(t, l)
	s := list(t, l)[sid]
	m := claude.New()
	h, err := l.m.For(context.Background(), m.Spec(), l.in, nil)
	if err != nil {
		t.Fatal(err)
	}
	p, err := m.PlanChangeDir(context.Background(), h, l.in, s, filepath.Join(l.repo, ".claude", "worktrees", "x"))
	if err != nil || len(p.Blockers) == 0 {
		t.Fatalf("plan: %+v %v", p, err)
	}
}

// Codex changes a thread's folder itself: a one-shot app-server resumes it in the new
// folder (no turn) and records the folder in the same rollout; the id stays. Runs against
// the installed codex, in a throwaway CODEX_HOME with no login (a TestCodex test: the
// weekly drift check runs it against the latest Codex).
func TestCodexChangeDirThroughAppServer(t *testing.T) {
	bin := realAgents(t, "codex")
	out, _ := exec.Command(bin, "--version").Output()
	home := codexHome(t, t.TempDir())
	from, to := t.TempDir(), t.TempDir()
	from, _ = filepath.EvalSymlinks(from)
	to, _ = filepath.EvalSymlinks(to)
	hm := &host.Machine{Name: "here", Local: true, Facts: host.Facts{OS: runtime.GOOS, Home: home, Env: map[string]string{"CODEX_HOME": home},
		Binaries: map[string]agent.BinaryFact{"codex": {Path: bin, Version: string(out)}}}}
	m := codex.New()
	in := agent.Install{Agent: "codex", Binary: bin, Version: agent.VersionOf(string(out)), Roots: map[string]string{"home": home}, Present: true}
	ctx := context.Background()
	h, err := hm.For(ctx, m.Spec(), in, nil)
	if err != nil {
		t.Fatal(err)
	}
	nodes := []ir.Node{{Kind: ir.KindMessage, Actor: ir.User, Text: "What is the codeword?"}, {Kind: ir.KindMessage, Actor: ir.Agent, Text: "PLUM-7"}}
	ir.Chain(nodes, "")
	prepared := convert.Render(convert.Request{Nodes: nodes, From: "Claude Code", To: "Codex", Fidelity: convert.History, Window: 64000})
	j, err := journal.New(t.TempDir(), journal.KindMove, "test")
	if err != nil {
		t.Fatal(err)
	}
	wh, err := hm.For(ctx, m.Spec(), in, j)
	if err != nil {
		t.Fatal(err)
	}
	w, err := m.Write(ctx, wh, in, ir.WriteRequest{Mode: ir.WriteNew, Header: ir.Header{CWD: from, Title: "Find the codeword", Created: time.Now()}, Items: prepared.Items})
	if err != nil {
		t.Fatal(err)
	}
	key := agent.SessionKey{Agent: "codex", Session: agent.SessionID(w.SessionID)}
	if err := m.AfterInstall(ctx, wh, in, key, agent.Placement{Key: key}); err != nil {
		t.Fatal(err)
	}
	ls, err := m.List(ctx, h, in)
	if err != nil || len(ls.Sessions) != 1 || ls.Sessions[0].CWD != from {
		t.Fatalf("list: %+v %v", ls.Sessions, err)
	}
	s := ls.Sessions[0]
	orig, _ := os.ReadFile(s.Path)
	p, err := m.PlanChangeDir(ctx, h, in, s, to)
	if err != nil || len(p.Blockers) > 0 || p.Method != agent.DirAgent || len(p.Moves) != 0 {
		t.Fatalf("plan: %+v %v", p, err)
	}
	if err := m.ChangeDir(ctx, h, in, s, p); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(s.Path)
	if !bytes.HasPrefix(b, orig) {
		t.Fatal("Codex must keep every record of the rollout")
	}
	ls, _ = m.List(ctx, h, in)
	if len(ls.Sessions) != 1 || ls.Sessions[0].Key != s.Key || ls.Sessions[0].CWD != to {
		t.Fatalf("after: %+v", ls.Sessions)
	}
	if l := codexList(t, bin, home); !strings.Contains(l, w.SessionID) || !strings.Contains(l, `"cwd":"`+jsonText(to)+`"`) {
		t.Fatalf("Codex's own list must show the thread in the new folder: %s", l)
	}
	if m.InSession(in, to) != "" {
		t.Fatal("Codex's own /cd forks the thread: nothing to type in the session")
	}
}
