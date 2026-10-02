package codex

import (
	"context"
	"errors"
	"strings"
	"testing"

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
