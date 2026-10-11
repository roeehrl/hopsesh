package claude

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/roeehrl/hopsesh/sdk/agent"
	"github.com/roeehrl/hopsesh/sdk/agent/agenttest"
)

const (
	s1 = "0b6c6a8e-1d2f-4c3b-9a7e-5f4d3c2b1a01"
	s2 = "0b6c6a8e-1d2f-4c3b-9a7e-5f4d3c2b1a02"
)

func newHost(t *testing.T) *agenttest.FakeHost {
	h := agenttest.NewFakeHost("/home/u")
	if err := h.Load("testdata/2.1.284", "/home/u/.claude"); err != nil {
		t.Fatal(err)
	}
	h.AddBinary("claude", "2.1.284 (Claude Code)")
	h.PIDs[4242] = true
	h.Programs["claude"] = func(argv []string, _ agent.RunOptions) agent.Result {
		if strings.Join(argv[1:], " ") == "auth status --json" {
			return agent.Result{Stdout: []byte(`{"loggedIn":true,"authMethod":"claude.ai","apiProvider":"firstParty","orgId":"org-1","subscriptionType":"max","email":"x@example.com"}`)}
		}
		return agent.Result{Code: 1}
	}
	return h
}

func TestConformance(t *testing.T) { agenttest.Run(t, New(), newHost) }

func setup(t *testing.T) (*agenttest.FakeHost, agent.Host, agent.Install, map[string]agent.Summary) {
	m := New()
	fh := newHost(t)
	in, err := m.Detect(context.Background(), fh)
	if err != nil {
		t.Fatal(err)
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
	return fh, h, in, byID
}

func TestListAndBundle(t *testing.T) {
	_, h, in, byID := setup(t)
	a, b := byID[s1], byID[s2]
	if a.Title != "Find the codeword" || a.CWD != "/home/u/git/demo" || a.Subagents != 1 || a.GitBranch != "main" {
		t.Fatalf("summary 1: %+v", a)
	}
	if b.LegacyLabel == nil || b.LegacyLabel.Kind != agent.LabelMoved || b.LegacyLabel.Location != "studio" || b.Title != "Feature flag" {
		t.Fatalf("a copy an older hopsesh labelled: %+v %+v", b, b.LegacyLabel)
	}
	if b.WorktreeRoot != "/home/u/git/demo" {
		t.Fatalf("worktree root %q", b.WorktreeRoot)
	}
	bun, err := New().Bundle(context.Background(), h, in, a)
	if err != nil {
		t.Fatal(err)
	}
	var rels []string
	for _, f := range bun.Files {
		rels = append(rels, f.Rel+":"+string(f.Rewrite))
	}
	got := strings.Join(rels, " ")
	for _, want := range []string{
		"projects/-home-u-git-demo/" + s1 + ".jsonl:jsonl",
		"projects/-home-u-git-demo/" + s1 + "/subagents/agent-1.jsonl:jsonl",
		"projects/-home-u-git-demo/" + s1 + "/tool-results/toolu_1.txt:text",
		"file-history/" + s1 + "/abc@v1:none",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("bundle lacks %s: %s", want, got)
		}
	}
}

func TestPlanMoveElsewhere(t *testing.T) {
	_, h, in, byID := setup(t)
	m := New()
	s := byID[s1]
	bun, _ := m.Bundle(context.Background(), h, in, s)
	p := agent.Placement{Key: agent.SessionKey{Agent: id, Session: "new-id"}, SourceID: s1, CWD: "/Users/me/src/demo", Title: "Find the codeword (from box)", Location: "laptop"}
	mp, err := m.PlanMove(in, in, s, bun, p)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range mp.Files {
		switch f.From.Role {
		case agent.RoleMain:
			if f.ToRel != "projects/-Users-me-src-demo/new-id.jsonl" {
				t.Errorf("main goes to %s", f.ToRel)
			}
			if len(f.Append) != 2 || !strings.Contains(string(f.Append[0]), `"relocatedCwd":"/Users/me/src/demo"`) || !strings.Contains(string(f.Append[1]), `"customTitle":"Find the codeword (from box)"`) {
				t.Errorf("appended records: %q", f.Append)
			}
		default:
			if strings.Contains(f.ToRel, s1) {
				t.Errorf("a side file keeps the old id: %s", f.ToRel)
			}
		}
	}
	if mp.Policy.Rename != [2]string{s1, "new-id"} {
		t.Errorf("rename %v", mp.Policy.Rename)
	}
}

func TestLiveStopLegacyLabelAccount(t *testing.T) {
	fh, h, in, byID := setup(t)
	m := New()
	ctx := context.Background()
	live, err := m.Live(ctx, h, in, []agent.SessionID{s1, s2})
	if err != nil || live[s1].State != agent.Live || live[s1].PID != 4242 || live[s2].State != agent.Ended {
		t.Fatalf("live: %+v %v", live, err)
	}
	if live[s1].App {
		t.Fatal("a terminal session reads as the Claude app's")
	}
	// The Claude app runs its sessions with entrypoint claude-desktop.
	fh.Put("/home/u/.claude/sessions/4343.json", []byte(`{"pid":4343,"sessionId":"`+s2+`","entrypoint":"claude-desktop","status":"idle"}`), time.Now())
	fh.PIDs[4343] = true
	if live, err = m.Live(ctx, h, in, []agent.SessionID{s2}); err != nil || live[s2].State != agent.Live || !live[s2].App {
		t.Fatalf("a Claude app session: %+v %v", live, err)
	}
	if err := m.Stop(ctx, h, in, byID[s1], time.Second); err != nil || fh.PIDs[4242] {
		t.Fatalf("stop: %v", err)
	}
	// A label an older hopsesh wrote reads back apart from the title.
	rec := encodeRecord(map[string]any{"type": "custom-title", "customTitle": "↪ continued in Codex on laptop · Find the codeword", "sessionId": s1})
	if err := h.FS().Append(byID[s1].Path, append(rec, '\n'), agent.AppendOptions{NewLine: true, KeepMtime: true, Standalone: true}); err != nil {
		t.Fatal(err)
	}
	l, _ := m.List(ctx, h, in)
	for _, s := range l.Sessions {
		if string(s.Key.Session) == s1 && (s.LegacyLabel == nil || s.LegacyLabel.AgentName != "Codex" || s.Title != "Find the codeword") {
			t.Fatalf("labelled copy reads back as %+v %+v", s, s.LegacyLabel)
		}
	}
	acct, err := m.Account(ctx, h, in)
	if err != nil || acct.Key == "" || !acct.RemoteControl || acct.Label != "max" {
		t.Fatalf("account: %+v %v", acct, err)
	}
	if _, err := h.FS().ReadFile("/home/u/.claude/sessions/4242.abc.key", 100); !errors.Is(err, agent.ErrDenied) {
		t.Fatal("session keys must be unreadable")
	}
}

// One session open in several processes: every one is in Procs, the waiting one is the
// main process, the newest name wins, and Stop quits them all.
func TestLiveSeveralProcesses(t *testing.T) {
	fh, h, in, byID := setup(t)
	m := New()
	ctx := context.Background()
	t0 := time.Unix(1_790_000_000, 0)
	fh.Put("/home/u/.claude/sessions/5001.json", []byte(`{"pid":5001,"sessionId":"`+s2+`","entrypoint":"cli","status":"busy","name":"old name"}`), t0)
	fh.Put("/home/u/.claude/sessions/5002.json", []byte(`{"pid":5002,"sessionId":"`+s2+`","entrypoint":"claude-desktop","status":"idle","waitingFor":"permission","name":"parser fix"}`), t0.Add(-time.Minute))
	fh.Put("/home/u/.claude/sessions/5003.json", []byte(`{"pid":5003,"sessionId":"`+s2+`","entrypoint":"cli","status":"idle","name":"newest name"}`), t0.Add(time.Minute))
	fh.Put("/home/u/.claude/sessions/5004.json", []byte(`{"pid":5004,"sessionId":"`+s2+`","entrypoint":"cli","status":"idle"}`), t0.Add(time.Hour))
	fh.PIDs[5001], fh.PIDs[5002], fh.PIDs[5003] = true, true, true // 5004 has exited
	live, err := m.Live(ctx, h, in, []agent.SessionID{s1, s2})
	if err != nil {
		t.Fatal(err)
	}
	if got := live[s1].Procs; len(got) != 1 || got[0].PID != 4242 || got[0].App || got[0].Waiting || got[0].ObservedAt.IsZero() {
		t.Fatalf("a single terminal process: %+v", got)
	}
	l := live[s2]
	if l.State != agent.Live || l.PID != 5002 || !l.App || l.Status != "waiting for permission" || l.Name != "newest name" {
		t.Fatalf("the waiting process is the main one: %+v", l)
	}
	want := []agent.LiveProc{{PID: 5002, App: true, Waiting: true, ObservedAt: t0.Add(-time.Minute)}, {PID: 5003, ObservedAt: t0.Add(time.Minute)}, {PID: 5001, ObservedAt: t0}}
	if !slices.Equal(l.Procs, want) {
		t.Fatalf("procs %+v, want %+v", l.Procs, want)
	}
	// Without a waiting one, the latest to write its entry is the main one.
	delete(fh.PIDs, 5002)
	if live, _ = m.Live(ctx, h, in, []agent.SessionID{s2}); live[s2].PID != 5003 || live[s2].App || len(live[s2].Procs) != 2 {
		t.Fatalf("the newest entry is the main one: %+v", live[s2])
	}
	fh.PIDs[5002] = true
	if err := m.Stop(ctx, h, in, byID[s2], time.Second); err != nil || fh.PIDs[5001] || fh.PIDs[5002] || fh.PIDs[5003] || !fh.PIDs[4242] {
		t.Fatalf("stop quits every process of the session and no other: %v %v", err, fh.PIDs)
	}
}

func TestRules(t *testing.T) {
	r := agent.Rules{Bin: "hopsesh", Allow: [][]string{{"ls"}}, Ask: [][]string{{"pull"}}}
	b, err := mergeRules([]byte(`{"model":"opus","permissions":{"allow":["Bash(git status)"]}}`), r)
	if err != nil {
		t.Fatal(err)
	}
	if !hasRules(b, r) || !strings.Contains(string(b), `"model": "opus"`) || !strings.Contains(string(b), "Bash(git status)") {
		t.Fatalf("merged settings: %s", b)
	}
	back, _ := removeRules(b, r)
	if hasRules(back, r) || !strings.Contains(string(back), "Bash(git status)") {
		t.Fatalf("removed settings: %s", back)
	}
	if _, err := mergeRules([]byte(`{oops`), r); err == nil {
		t.Fatal("invalid JSON must not be overwritten")
	}
}

func TestResume(t *testing.T) {
	c := New().Resume(agent.Install{}, agent.SessionKey{Agent: id, Session: "x"}, agent.Placement{CWD: "/r"}, agent.ResumeOptions{Fork: true, RemoteControl: true, Name: "fix@laptop"})
	if strings.Join(c.Argv, " ") != "claude --resume x --fork-session --remote-control fix@laptop" || c.Dir != "/r" {
		t.Fatalf("%+v", c)
	}
}

// A resumed session must not inherit the markers of a Claude Code session that started the
// terminal: with CLAUDE_CODE_CHILD_SESSION it would save no transcript.
func TestResumeDropsSessionMarkers(t *testing.T) {
	c := New().Resume(agent.Install{}, agent.SessionKey{Session: "s1"}, agent.Placement{CWD: "/w"}, agent.ResumeOptions{})
	for _, v := range []string{"CLAUDE_CODE_CHILD_SESSION", "CLAUDECODE"} {
		if !slices.Contains(c.Unset, v) {
			t.Errorf("resume keeps %s: %v", v, c.Unset)
		}
	}
}

func TestResumeUsesDetectedMachineNativeDefaultConfig(t *testing.T) {
	// The detected machine's home deliberately differs from the launcher's.
	// An explicit native default must still read ~/.claude.json, not a new
	// <CLAUDE_CONFIG_DIR>/.claude.json that triggers first-run onboarding.
	for _, explicit := range []bool{false, true} {
		for _, registered := range []bool{false, true} {
			t.Run(strings.Join([]string{map[bool]string{false: "implicit", true: "explicit"}[explicit],
				map[bool]string{false: "unregistered", true: "registered"}[registered]}, "/"), func(t *testing.T) {
				fh := agenttest.NewFakeHost("/remote/alice")
				facts := fh.Facts()
				facts.Home = "/remote/alice"
				facts.Env = map[string]string{}
				if explicit {
					facts.Env["CLAUDE_CONFIG_DIR"] = "/remote/alice/.claude"
				}
				facts.Binaries = map[string]agent.BinaryFact{"claude": {Path: "/remote/bin/claude", Version: "2.1.284"}}
				in, err := New().Detect(context.Background(), desktopHost{fh, facts})
				if err != nil {
					t.Fatal(err)
				}
				// Install metadata must survive serialization across machines and
				// preserve the same launch policy without consulting local HOME.
				b, err := json.Marshal(in)
				if err != nil {
					t.Fatal(err)
				}
				in = agent.Install{}
				if err := json.Unmarshal(b, &in); err != nil {
					t.Fatal(err)
				}
				if registered {
					in.Profile = &agent.RuntimeProfile{ID: "default", Root: in.Root(home), Default: true}
				}
				c := New().Resume(in, agent.SessionKey{Agent: id, Session: s1}, agent.Placement{CWD: "/remote/repo"}, agent.ResumeOptions{})
				if !slices.Contains(c.Unset, "CLAUDE_CONFIG_DIR") {
					t.Fatalf("default can inherit alternate config: %+v", c)
				}
				for _, e := range c.Env {
					if strings.HasPrefix(e, "CLAUDE_CONFIG_DIR=") {
						t.Fatalf("default launch forces onboarding config: %+v", c)
					}
				}
				if !slices.Contains(c.Argv, s1) || c.Dir != "/remote/repo" || (registered && c.Argv[0] != "/remote/bin/claude") {
					t.Fatalf("changed exact-session launch: %+v", c)
				}
				if registered {
					for _, key := range New().Spec().Accounts.Unset {
						if !slices.Contains(c.Unset, key) {
							t.Fatalf("default retained credential override %s: %+v", key, c)
						}
					}
				}
			})
		}
	}
}

func TestResumeKeepsCustomProfileConfigIsolation(t *testing.T) {
	for _, tc := range []struct {
		name       string
		root       string
		defaultTag bool
		metadata   bool
	}{
		{"custom", "/remote/alice/work-claude", false, true},
		{"custom marked default", "/remote/alice/work-claude", true, true},
		{"custom named .claude", "/remote/other/.claude", true, true},
		{"native root selected as custom", "/remote/alice/.claude", false, true},
		{"missing detection metadata", "/remote/alice/.claude", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fh := agenttest.NewFakeHost("/remote/alice")
			facts := fh.Facts()
			facts.Env = map[string]string{"CLAUDE_CONFIG_DIR": tc.root}
			in, err := New().Detect(context.Background(), desktopHost{fh, facts})
			if err != nil {
				t.Fatal(err)
			}
			if !tc.metadata {
				in.NativeDefaultRoot = ""
			}
			in.Profile = &agent.RuntimeProfile{ID: "work", Root: tc.root, Default: tc.defaultTag}
			c := New().Resume(in, agent.SessionKey{Agent: id, Profile: "work", Session: s1}, agent.Placement{}, agent.ResumeOptions{})
			if !slices.Contains(c.Env, "CLAUDE_CONFIG_DIR="+tc.root) || !slices.Contains(c.Unset, "CLAUDE_CONFIG_DIR") {
				t.Fatalf("custom root can fall back to another profile: %+v", c)
			}
			for _, key := range New().Spec().Accounts.Unset {
				if !slices.Contains(c.Unset, key) {
					t.Fatalf("custom profile retained override %s: %+v", key, c)
				}
			}
		})
	}
}

func TestConsoleIsolationIsNotClaimedForSubscriptionOrAPIKey(t *testing.T) {
	for _, method := range []string{"console", "claude.ai", "api-key"} {
		a := account(authStatus{LoggedIn: true, Method: method, Provider: "firstParty"})
		if (a.IsolationWhy != "") != (method == "console") {
			t.Fatalf("%s: %+v", method, a)
		}
	}
}
