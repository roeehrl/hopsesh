package codex

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/roeehrl/hopsesh/sdk/agent"
	"github.com/roeehrl/hopsesh/sdk/agent/agenttest"
	"github.com/roeehrl/hopsesh/sdk/ir"
)

const (
	t1 = "01a0fe1c-0000-7000-8000-000000000001"
	t3 = "01a0fe1c-0000-7000-8000-000000000003"
)

func newHost(t *testing.T) *agenttest.FakeHost {
	h := agenttest.NewFakeHost("/home/u")
	if err := h.Load("testdata/0.153.2", "/home/u/.codex"); err != nil {
		t.Fatal(err)
	}
	h.AddBinary("codex", "codex-cli 0.153.2")
	h.LocksHeld["/home/u/.codex/thread-writer-locks/"+t1+".lock"] = true
	return h
}

func TestConformance(t *testing.T) { agenttest.Run(t, New(), newHost) }

// The cloud is declared as data for now: the kit checks the declaration.
func TestCloudConformance(t *testing.T) { agenttest.RunCloud(t, New(), nil) }

func setup(t *testing.T) (agent.Host, agent.Install, map[string]agent.Summary) {
	m := New()
	fh := newHost(t)
	in, err := m.Detect(context.Background(), fh)
	if err != nil || in.Version != "0.153.2" {
		t.Fatalf("detect: %+v %v", in, err)
	}
	h := agent.Confine(fh, m.Spec(), in)
	l, err := m.List(context.Background(), h, in)
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]agent.Summary{}
	for _, s := range l.Sessions {
		byID[string(s.Key.Session)] = s
	}
	return h, in, byID
}

func TestList(t *testing.T) {
	_, _, byID := setup(t)
	if len(byID) != 2 {
		t.Fatalf("sub-agent threads are not listed: %v", byID)
	}
	a := byID[t1]
	if a.Title != "What is the codeword in notes.txt?" || a.CWD != "/home/u/git/demo" || a.GitBranch != "main" || a.AgentVersion != "0.153.2" {
		t.Fatalf("thread 1: %+v", a)
	}
	if b := byID[t3]; b.Title != "README cleanup" || b.TitleSource != "custom" {
		t.Fatalf("names from session_index.jsonl, the last entry winning: %+v", b)
	}
}

func TestLiveAndSecrets(t *testing.T) {
	h, in, _ := setup(t)
	live, err := New().Live(context.Background(), h, in, []agent.SessionID{t1, t3})
	if err != nil || live[t1].State != agent.Live || live[t3].State != agent.Ended {
		t.Fatalf("live: %+v %v", live, err)
	}
	if _, err := h.FS().ReadFile("/home/u/.codex/auth.json", 100); !errors.Is(err, agent.ErrDenied) {
		t.Fatal("auth.json must be unreadable")
	}
}

func TestReadStructuredAndLegacy(t *testing.T) {
	h, in, byID := setup(t)
	seg, err := New().Read(context.Background(), h, in, byID[t1], ir.Cursor{})
	if err != nil {
		t.Fatal(err)
	}
	var kinds []string
	for _, n := range seg.Nodes {
		kinds = append(kinds, string(n.Kind))
	}
	if got := strings.Join(kinds, " "); got != "message reasoning tool_call tool_result tool_call tool_result message" {
		t.Fatalf("kinds: %s", got)
	}
	sh := seg.Nodes[2].Tool
	if sh.Shell.Command != "cat notes.txt" || sh.Shell.Dir != "/home/u/git/demo" || *seg.Nodes[3].Result.ExitCode != 0 {
		t.Fatalf("shell: %+v %+v", sh, seg.Nodes[3].Result)
	}
	if w := seg.Nodes[4].Tool.Write; w == nil || w.Path != "/home/u/git/demo/answer.txt" || w.Content != "FIG-3\n" {
		t.Fatalf("file change: %+v", seg.Nodes[4].Tool)
	}
	if seg.Header.Model != "gpt-5.6" || seg.Header.GitBranch != "main" {
		t.Fatalf("header: %+v", seg.Header)
	}
	legacy, err := New().Read(context.Background(), h, in, byID[t3], ir.Cursor{})
	if err != nil {
		t.Fatal(err)
	}
	if len(legacy.Nodes) != 4 || legacy.Nodes[1].Tool.Shell.Command != "ls" || legacy.Nodes[2].Result.Output != "README.md\n" {
		t.Fatalf("legacy rollout: %+v", legacy.Nodes)
	}
}

func TestPlanMoveAndRules(t *testing.T) {
	h, in, byID := setup(t)
	m := New()
	s := byID[t1]
	b, _ := m.Bundle(context.Background(), h, in, s)
	mp, _ := m.PlanMove(in, in, s, b, agent.Placement{Key: agent.SessionKey{Agent: id, Session: "new"}, SourceID: t1, CWD: "/x"})
	if !strings.HasSuffix(mp.Files[0].ToRel, "-new.jsonl") || mp.Policy.Rename != [2]string{t1, "new"} || mp.Policy.Protect[0] != "encrypted_content" {
		t.Fatalf("plan: %+v", mp)
	}
	r := string(renderRules(agent.Rules{Bin: "hopsesh", Allow: [][]string{{"ls"}}, Ask: [][]string{{"pull"}}}))
	if !strings.Contains(r, `prefix_rule(pattern=["hopsesh", "ls"], decision="allow")`) || !strings.Contains(r, `prefix_rule(pattern=["hopsesh", "pull"], decision="prompt")`) {
		t.Fatalf("rules:\n%s", r)
	}
	if c := m.Resume(in, agent.SessionKey{Agent: id, Session: "x"}, agent.Placement{CWD: "/r"}, agent.ResumeOptions{Fork: true}); strings.Join(c.Argv, " ") != "codex fork x" {
		t.Fatalf("fork: %v", c.Argv)
	}
}

// Marks and titles are thread names in session_index.jsonl; hopsesh's own messages are
// not shown as prompts, and a thread holding only a briefing is still listed.
func TestMarkTitleAndNotes(t *testing.T) {
	h, in, byID := setup(t)
	m, ctx := New(), context.Background()
	if err := m.Mark(ctx, h, in, byID[t3], agent.Mark{Kind: agent.MarkContinued, Agent: "claude", AgentName: "Claude Code", Location: "studio"}); err != nil {
		t.Fatal(err)
	}
	res, err := m.Write(ctx, h, in, ir.WriteRequest{Mode: ir.WriteNew, Header: ir.Header{CWD: "/home/u/git/demo", Title: "Fix the parser"},
		Items: []ir.Item{{Role: ir.RoleUser, Text: agent.NotePrefix + "This conversation was moved from Claude Code."}}})
	if err != nil {
		t.Fatal(err)
	}
	l, err := m.List(ctx, h, in)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]agent.Summary{}
	for _, s := range l.Sessions {
		got[string(s.Key.Session)] = s
	}
	if s := got[t3]; s.Mark == nil || s.Mark.AgentName != "Claude Code" || s.Mark.Location != "studio" || s.Title != "README cleanup" {
		t.Fatalf("marked thread: %+v %+v", s, s.Mark)
	}
	n, ok := got[res.SessionID]
	if !ok || n.Title != "Fix the parser" || n.LastPrompt != "" {
		t.Fatalf("a briefing-only thread is listed with its title and no prompt: %+v", n)
	}

	// A history that ended on the person carries the briefing in their last message; the
	// list shows only their part.
	res, err = m.Write(ctx, h, in, ir.WriteRequest{Mode: ir.WriteNew, Header: ir.Header{CWD: "/home/u/git/demo"},
		Items: []ir.Item{{Role: ir.RoleUser, Text: "document the cursor format\n\n" + agent.NotePrefix + "This conversation was moved from Claude Code."},
			{Role: ir.RoleAgent, Text: "Understood."}}})
	if err != nil {
		t.Fatal(err)
	}
	l, _ = m.List(ctx, h, in)
	for _, s := range l.Sessions {
		if string(s.Key.Session) == res.SessionID && (s.LastPrompt != "document the cursor format" || s.Title != "document the cursor format") {
			t.Fatalf("the briefing is shown as the person's prompt: %q / %q", s.Title, s.LastPrompt)
		}
	}
}

// Stop quits only an idle thread's codex process (the one holding its writer lock), and
// Live says whether Codex is working.
func TestStopAndStatus(t *testing.T) {
	m := New()
	fh := newHost(t)
	lock := "/home/u/.codex/thread-writer-locks/" + t1 + ".lock"
	fh.LockHolders[lock] = []int{4242}
	fh.PIDs[4242], fh.PIDNames[4242] = true, "codex"
	ctx := context.Background()
	in, _ := m.Detect(ctx, fh)
	h := agent.Confine(fh, m.Spec(), in)
	var s agent.Summary
	l, _ := m.List(ctx, h, in)
	for _, x := range l.Sessions {
		if string(x.Key.Session) == t1 {
			s = x
		}
	}
	if live, _ := m.Live(ctx, h, in, []agent.SessionID{t1}); live[t1].Status != "idle" {
		t.Fatalf("a finished turn is idle: %+v", live[t1])
	}
	// Mid-turn: refused.
	rollout := s.Path
	b, _ := h.FS().ReadFile(rollout, 1<<20)
	busy := append(append([]byte(nil), b...), []byte(`{"timestamp":"2026-10-01T10:05:00.000Z","type":"event_msg","payload":{"type":"task_started","turn_id":"t9"}}`+"\n")...)
	fh.Put(rollout, busy, time.Now())
	if live, _ := m.Live(ctx, h, in, []agent.SessionID{t1}); live[t1].Status != "working" {
		t.Fatalf("an open turn is working: %+v", live[t1])
	}
	if err := m.Stop(ctx, h, in, s, time.Second); !errors.Is(err, ErrBusy) {
		t.Fatalf("stopping mid-turn must be refused: %v", err)
	}
	fh.Put(rollout, b, time.Now())
	// Another program holding the lock is never signalled.
	fh.PIDNames[4242] = "vim"
	if err := m.Stop(ctx, h, in, s, time.Second); err == nil || !fh.PIDs[4242] {
		t.Fatalf("a non-codex holder must be left alone: %v", err)
	}
	fh.PIDNames[4242] = "codex"
	if err := m.Stop(ctx, h, in, s, time.Second); err != nil || fh.PIDs[4242] {
		t.Fatalf("stop: %v (alive %v)", err, fh.PIDs[4242])
	}
	if live, _ := m.Live(ctx, h, in, []agent.SessionID{t1}); live[t1].State != agent.Ended {
		t.Fatalf("after stop: %+v", live[t1])
	}
}

// A copy that comes home replaces a marked one; the mark lives in the shared index, so
// installing names the thread again (its original title when the move carries none).
func TestAfterInstallClearsTheMark(t *testing.T) {
	h, in, byID := setup(t)
	m := New()
	ctx := context.Background()
	if err := m.Mark(ctx, h, in, byID[t3], agent.Mark{Kind: agent.MarkMoved, Location: "laptop"}); err != nil {
		t.Fatal(err)
	}
	if l, _ := m.List(ctx, h, in); find(l.Sessions, t3).Mark == nil {
		t.Fatal("the thread should be marked")
	}
	noCodex := in
	noCodex.Binary = "" // the mark is cleared without codex's app-server
	if err := m.AfterInstall(ctx, h, noCodex, byID[t3].Key, agent.Placement{}); err != nil {
		t.Fatal(err)
	}
	l, _ := m.List(ctx, h, in)
	if s := find(l.Sessions, t3); s.Mark != nil || s.Title != "README cleanup" {
		t.Fatalf("after coming home: mark %+v, title %q", s.Mark, s.Title)
	}
}

func find(ss []agent.Summary, id string) agent.Summary {
	for _, s := range ss {
		if string(s.Key.Session) == id {
			return s
		}
	}
	return agent.Summary{}
}
