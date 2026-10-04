package copilot

import (
	"context"
	"encoding/json"
	"errors"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/roeehrl/hopsesh/internal/testkit/fakecloud"
	"github.com/roeehrl/hopsesh/sdk/agent"
	"github.com/roeehrl/hopsesh/sdk/agent/agenttest"
	"github.com/roeehrl/hopsesh/sdk/ir"
)

// seeded adds a finished Copilot task with a pull request on a copilot/… branch to the
// stand-in cloud in dir.
func seeded(t *testing.T, dir string) fakecloud.Session {
	t.Helper()
	s, err := fakecloud.Open(dir).Seed(fakecloud.Session{Cloud: fakecloud.CopilotCloud, Title: "Add rate limiting", Repo: "github.com/example/demo",
		Branch: "main", Base: "0123456789abcdef0123456789abcdef01234567", Result: "copilot/add-rate-limiting", PR: 101, State: fakecloud.StateDone,
		Messages: []fakecloud.Message{{Role: "user", Text: "Add rate limiting"}, {Role: "assistant", Text: "I added a token bucket."}}})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// The Copilot cloud agent passes the cloud conformance kit against the stand-in gh: a task
// started from a checkout's handoff branch, worked on, listed and fetched.
func TestCloudConformance(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stand-in origin's hook needs a POSIX shell")
	}
	dir := t.TempDir()
	repo := fakecloud.DemoCheckout(t)
	agenttest.RunCloudWith(t, New(), fakecloud.Programs(dir, nil), agenttest.CloudOptions{
		Request: func(c agent.Cloud) agent.SendRequest { return sendRequest(c.Name, repo) },
		Work: func(_ string, id agent.SessionID) {
			if err := fakecloud.Work(fakecloud.Proc{Vars: map[string]string{"FAKE_CLOUD_DIR": dir}}, string(id), false); err != nil {
				t.Fatal(err)
			}
		},
	})
}

// sendRequest is a hand-off from a checkout as the core prepares it.
func sendRequest(cloud, repo string) agent.SendRequest {
	return agent.SendRequest{Cloud: cloud, Dir: repo, Repo: "github.com/example/demo", Branch: "hopsesh/handoff/20261004-conform",
		Base: "0123456789abcdef0123456789abcdef01234567", Brief: agent.NotePrefix + "This task continues a session (conformance).\nOpen: finish it.", Title: "Finish it"}
}

// SendCloud runs gh agent-task create with the briefing on standard input, the handoff
// branch as the base and the repository named; it reads the agent session's link gh prints.
// When gh prints only that the job is queued, the new task is the one the listing did not
// have before. Refusals map onto the SDK's errors.
func TestSendCloud(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stand-in origin's hook needs a POSIX shell")
	}
	listRetry = time.Millisecond
	dir := t.TempDir()
	repo := fakecloud.DemoCheckout(t)
	seeded(t, dir)
	ctx := context.Background()
	m := New()
	var calls []string
	var stdin string
	fake := agenttest.NewFakeHost("/home/u")
	fake.AddBinary("gh", "gh version 2.97.0 (2026-07-31)")
	progs := fakecloud.Programs(dir, nil)
	fake.Programs["gh"] = func(argv []string, o agent.RunOptions) agent.Result {
		calls = append(calls, strings.Join(argv[1:], " "))
		if len(argv) > 2 && argv[2] == "create" {
			stdin = string(o.Stdin)
		}
		return progs["gh"](argv, o)
	}
	in, _ := m.Detect(ctx, fake)
	h := agent.Confine(fake, m.Spec(), in)
	r := sendRequest(cloudName, repo)
	cs, err := m.send(ctx, h, r)
	if err != nil {
		t.Fatal(err)
	}
	if want := "agent-task create -F - --base hopsesh/handoff/20261004-conform -R example/demo"; !contains(calls, want) || stdin != r.Brief {
		t.Fatalf("gh ran as %q with %q", calls, stdin)
	}
	st, err := fakecloud.Open(dir).Get(string(cs.Key.Session))
	if err != nil || st.Branch != r.Branch || st.Messages[0].Text != r.Brief {
		t.Fatalf("the task: %+v %v", st, err)
	}
	if cs.Key.Agent != id || cs.Cloud != cloudName || cs.State != agent.CloudRunning || cs.Repo != "github.com/example/demo" || cs.PR == "" ||
		cs.URL != "https://github.com/example/demo/pull/"+strings.TrimPrefix(cs.PR, "#")+"/agent-sessions/"+st.ID {
		t.Errorf("session: %+v", cs)
	}

	// Queued: no link, so the listing finds the new task.
	hq, _ := host(t, dir, "queued")
	cs, err = m.send(ctx, hq, r)
	if err != nil || cs.Key.Session == "" || cs.PR != "" {
		t.Fatalf("queued: %+v %v", cs, err)
	}
	if st, err := fakecloud.Open(dir).Get(string(cs.Key.Session)); err != nil || st.PR != 0 {
		t.Errorf("queued task: %+v %v", st, err)
	}

	for fail, want := range map[string]error{"signed-out": agent.ErrSignedOut, "not-eligible": agent.ErrNotEligible, "repo-mismatch": agent.ErrRepoUnsupported} {
		h, _ := host(t, dir, fail)
		if _, err := m.send(ctx, h, r); !errors.Is(err, want) {
			t.Errorf("%s: %v", fail, err)
		}
	}
	bad := r
	bad.Branch = "nope/not-pushed"
	if _, err := m.send(ctx, h, bad); !errors.Is(err, agent.ErrRepoUnsupported) {
		t.Errorf("a base branch GitHub does not have: %v", err)
	}
	for _, x := range []agent.SendRequest{{Repo: "gitlab.com/x/y", Branch: "b", Brief: r.Brief}, {Repo: r.Repo, Brief: r.Brief}, {Repo: r.Repo, Branch: "b", Brief: "no prefix"},
		{Repo: r.Repo, Branch: "b", Brief: r.Brief, Code: agent.ViaBundle}} {
		if _, err := m.send(ctx, h, x); err == nil {
			t.Errorf("sent %+v", x)
		}
	}
}

func contains(xs []string, x string) bool {
	for _, y := range xs {
		if y == x {
			return true
		}
	}
	return false
}

func host(t *testing.T, dir string, fail string) (agent.Host, agent.Install) {
	t.Helper()
	fh := agenttest.NewFakeHost("/home/u")
	fh.AddBinary("gh", "gh version 2.97.0 (2026-07-31)")
	vars := map[string]string{}
	if fail != "" {
		vars["FAKE_CLOUD_FAIL"] = fail
	}
	for k, p := range fakecloud.Programs(dir, vars) {
		fh.Programs[k] = p
	}
	m := New()
	in, err := m.Detect(context.Background(), fh)
	if err != nil {
		t.Fatal(err)
	}
	return agent.Confine(fh, m.Spec(), in), in
}

func TestListAndFetch(t *testing.T) {
	dir := t.TempDir()
	s := seeded(t, dir)
	running, err := fakecloud.Open(dir).Seed(fakecloud.Session{Cloud: fakecloud.CopilotCloud, Title: "Fix the flaky test", Repo: "github.com/example/other"})
	if err != nil {
		t.Fatal(err)
	}
	h, in := host(t, dir, "")
	ctx := context.Background()
	m := New()
	l, err := m.ListCloud(ctx, h, in, agent.CloudQuery{Cloud: cloudName})
	if err != nil || len(l.Sessions) != 2 {
		t.Fatalf("ListCloud: %+v %v", l, err)
	}
	by := map[agent.SessionID]agent.CloudSession{}
	for _, cs := range l.Sessions {
		by[cs.Key.Session] = cs
	}
	done := by[agent.SessionID(s.ID)]
	if done.Repo != "github.com/example/demo" || done.Branch != "copilot/add-rate-limiting" || done.PR != "#101" || done.State != agent.CloudDone ||
		done.URL != "https://github.com/example/demo/pull/101/agent-sessions/"+s.ID || done.Title != "Add rate limiting" {
		t.Errorf("done task: %+v", done)
	}
	if r := by[agent.SessionID(running.ID)]; r.State != agent.CloudRunning || r.Branch != "" || r.PR != "" {
		t.Errorf("running task: %+v", r)
	}
	l, err = m.ListCloud(ctx, h, in, agent.CloudQuery{Cloud: cloudName, Repo: "github.com/example/other"})
	if err != nil || len(l.Sessions) != 1 || l.Sessions[0].Key.Session != agent.SessionID(running.ID) {
		t.Errorf("filtered by repository: %+v %v", l, err)
	}

	f, err := m.FetchCloud(ctx, h, in, agent.SessionID(s.ID), agent.FetchTarget{Dir: "/home/u/git/demo-x", Code: agent.ViaPR})
	if err != nil {
		t.Fatal(err)
	}
	if f.Code.Way != agent.ViaPR || f.Code.Branch != "copilot/add-rate-limiting" || f.Code.PR != "#101" || f.Run != nil || f.Adopt != nil {
		t.Errorf("fetched code: %+v", f)
	}
	if f.Segment == nil || len(f.Segment.Nodes) != 2 || f.Segment.Nodes[0].Actor != ir.User || !strings.Contains(f.Segment.Nodes[1].Text, "token bucket") {
		t.Errorf("fetched log: %+v", f.Segment)
	}
	// A task without a pull request brings its log only.
	f, err = m.FetchCloud(ctx, h, in, agent.SessionID(running.ID), agent.FetchTarget{})
	if err != nil || f.Code.Way != "" || !strings.Contains(strings.Join(f.Loss, "\n"), "no code") {
		t.Errorf("no pull request yet: %+v %v", f, err)
	}
	if _, err := m.FetchCloud(ctx, h, in, "--web", agent.FetchTarget{}); !errors.Is(err, agent.ErrNotFound) {
		t.Errorf("an id that is a flag: %v", err)
	}
}

func TestRefusals(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	m := New()
	for fail, want := range map[string]error{"signed-out": agent.ErrSignedOut, "not-eligible": agent.ErrNotEligible} {
		h, in := host(t, dir, fail)
		if _, err := m.ListCloud(ctx, h, in, agent.CloudQuery{}); !errors.Is(err, want) {
			t.Errorf("%s: ListCloud %v", fail, err)
		}
		ct, err := m.TestCloud(ctx, h, in, cloudName)
		if !errors.Is(err, want) || len(ct.Checks) == 0 || ct.Checks[len(ct.Checks)-1].OK {
			t.Errorf("%s: TestCloud %+v %v", fail, ct, err)
		}
	}
	h, in := host(t, dir, "")
	ct, err := m.TestCloud(ctx, h, in, cloudName)
	if err != nil || ct.Account != "github.com · example-user" {
		t.Errorf("TestCloud: %+v %v", ct, err)
	}
	for _, c := range ct.Checks {
		if !c.OK {
			t.Errorf("check failed: %s", c.Text)
		}
	}
}

// The task fields' value types are not documented: repository may be an object, the pull
// request number a string, and a missing number is read from the link.
func TestTaskShapes(t *testing.T) {
	for in, want := range map[string]struct {
		repo string
		pr   int
	}{
		`{"id":"a","repository":"Example/Demo","pullRequestNumber":7}`:                                          {"Example/Demo", 7},
		`{"id":"a","repository":{"nameWithOwner":"example/demo"},"pullRequestNumber":"#8"}`:                     {"example/demo", 8},
		`{"id":"a","repository":{"name":"demo","owner":{"login":"example"}},"pullRequestNumber":null}`:          {"example/demo", 0},
		`{"id":"a","repository":{"full_name":"example/demo"},"pullRequestUrl":"https://github.com/x/y/pull/9"}`: {"example/demo", 9},
		`{"id":"a","repository":"https://github.com/example/demo"}`:                                             {"example/demo", 0},
	} {
		var tk task
		if err := json.Unmarshal([]byte(in), &tk); err != nil {
			t.Fatal(err)
		}
		if tk.repo() != want.repo || tk.pr() != want.pr {
			t.Errorf("%s: %q %d", in, tk.repo(), tk.pr())
		}
	}
	for in, want := range map[string]agent.CloudState{"queued": agent.CloudRunning, "IN_PROGRESS": agent.CloudRunning, "waiting_for_user": agent.CloudIdle,
		"completed": agent.CloudDone, "timed_out": agent.CloudFailed, "Cancelled": agent.CloudFailed, "something new": agent.CloudUnknown} {
		if got := state(in); got != want {
			t.Errorf("state(%q) = %s, want %s", in, got, want)
		}
	}
}

func TestLogText(t *testing.T) {
	sse := "data: {\"choices\":[{\"delta\":{\"content\":\"Hello \"}}]}\n\ndata: {\"choices\":[{\"delta\":{\"content\":\"world\"}}]}\ndata: [DONE]\n"
	if got := logText([]byte(sse)); got != "Hello world" {
		t.Errorf("server-sent chunks: %q", got)
	}
	if got := logText([]byte("\x1b[1mBold\x1b[0m line\n\nnext\n")); got != "Bold line\n\nnext" {
		t.Errorf("terminal text: %q", got)
	}
	seg := logSegment(task{ID: "x", Name: "Do it", CreatedAt: time.Now().UTC().Format(time.RFC3339)}, []byte("done"))
	if len(seg.Nodes) != 2 || seg.Nodes[1].Parent != seg.Nodes[0].ID {
		t.Errorf("segment: %+v", seg.Nodes)
	}
}

func TestActiveLogin(t *testing.T) {
	for in, want := range map[string]string{
		`{"hosts":{"github.com":[{"state":"success","active":true,"host":"github.com","login":"octo"}]}}`:                         "octo",
		`{"hosts":{"github.com":[{"state":"success","active":false,"login":"a"},{"state":"success","active":true,"login":"b"}]}}`: "b",
	} {
		if got, err := activeLogin([]byte(in)); err != nil || got != want {
			t.Errorf("%s: %q %v", in, got, err)
		}
	}
	for _, in := range []string{`{"hosts":{}}`, `{"hosts":{"github.com":[{"state":"error","active":true,"login":"a","error":"token expired"}]}}`} {
		if _, err := activeLogin([]byte(in)); !errors.Is(err, agent.ErrSignedOut) {
			t.Errorf("%s: %v", in, err)
		}
	}
}

// The agent session's link counts only on github.com itself and for the task's repository.
func TestCreatedLinkHostileHosts(t *testing.T) {
	sid := "0b6c6a8e-1d2f-4c3b-9a7e-5f4d3c2b1a01"
	for in, want := range map[string]struct {
		sid string
		pr  int
	}{
		"https://github.com/example/demo/pull/12/agent-sessions/" + sid + "\n":       {sid, 12},
		"https://github.com/example/demo/pull/12\n":                                  {"", 12},
		"https://github.com.evil.example/example/demo/pull/12/agent-sessions/" + sid: {"", 0},
		"https://evil.example/github.com/example/demo/pull/12/agent-sessions/" + sid: {"", 0},
		"https://evil.example/?u=https://github.com/example/demo/pull/12":            {"", 0},
		"https://me@github.com/example/demo/pull/12/agent-sessions/" + sid:           {"", 0},
		"https://github.com:444/example/demo/pull/12/agent-sessions/" + sid:          {"", 0},
		"http://github.com/example/demo/pull/12/agent-sessions/" + sid:               {"", 0},
		"https://github.com/other/repo/pull/12/agent-sessions/" + sid:                {"", 0},
		"https://github.com/example/demo/pull/12/agent-sessions/not-a-uuid":          {"", 0},
	} {
		s, _, pr := createdLink(in, "example/demo")
		if s != want.sid || pr != want.pr {
			t.Errorf("createdLink(%q) = %q, %d; want %q, %d", in, s, pr, want.sid, want.pr)
		}
	}
}
