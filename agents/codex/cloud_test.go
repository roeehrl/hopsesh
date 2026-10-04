package codex_test

import (
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/roeehrl/hopsesh/agents/codex"
	"github.com/roeehrl/hopsesh/internal/testkit/fakecloud"
	"github.com/roeehrl/hopsesh/sdk/agent"
	"github.com/roeehrl/hopsesh/sdk/agent/agenttest"
)

// Codex cloud passes the cloud conformance kit against the stand-in cloud: it starts a task
// from a briefing on a branch of a repository on the stand-in GitHub, in an environment,
// lists it, brings it back as its title and diff, reads its links back, tests the login,
// and maps a login, a plan, an environment and a repository the cloud cannot use.
func TestCloudConformance(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stand-in origin's hook needs a POSIX shell")
	}
	repo := demoRepo(t)
	agenttest.RunCloudWith(t, codex.New(), fakecloud.Programs(t.TempDir(), nil), agenttest.CloudOptions{
		Request: func(c agent.Cloud) agent.SendRequest {
			return agent.SendRequest{Cloud: c.Name, Dir: repo, Repo: "github.com/example/demo", Branch: "main", Env: "env_demo",
				Brief: agent.NotePrefix + "This task continues a session (conformance).", Title: "conformance"}
		},
	})
}

// demoRepo is a checkout of github.com/example/demo, whose remote is a local bare
// repository standing in for GitHub, with main pushed.
func demoRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	gitConfig := filepath.Join(root, "gitconfig")
	for k, v := range map[string]string{"GIT_CONFIG_GLOBAL": gitConfig, "GIT_CONFIG_NOSYSTEM": "1", "GIT_AUTHOR_NAME": "Sam Doe", "GIT_AUTHOR_EMAIL": "sam@example.com",
		"GIT_COMMITTER_NAME": "Sam Doe", "GIT_COMMITTER_EMAIL": "sam@example.com", "FAKE_CLOUD_FAIL": "", "FAKE_CODEX_ENVS": "", "CODEX_STARTING_DIFF": ""} {
		t.Setenv(k, v)
	}
	o, err := fakecloud.NewOrigin(root, "https://github.com/example/demo.git")
	if err != nil {
		t.Fatal(err)
	}
	if err := o.Redirect(gitConfig); err != nil {
		t.Fatal(err)
	}
	repo := filepath.Join(root, "demo")
	for _, args := range [][]string{{"init", "-q", "-b", "main", repo}, {"-C", repo, "remote", "add", "origin", o.URL},
		{"-C", repo, "commit", "-q", "--allow-empty", "-m", "init"}, {"-C", repo, "push", "-q", "origin", "main"}} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	return repo
}

// cloudHost is a machine where codex answers with the stand-in cloud in dir (vars on top).
func cloudHost(t *testing.T, dir string, vars map[string]string) (agent.Host, agent.Install, *[]string, *[]agent.RunOptions) {
	t.Helper()
	fh := agenttest.NewFakeHost("/home/u")
	fh.AddBinary("codex", "codex-cli 0.153.2")
	var argvs []string
	var runs []agent.RunOptions
	prog := fakecloud.Programs(dir, vars)["codex"]
	fh.Programs["codex"] = func(argv []string, o agent.RunOptions) agent.Result {
		argvs, runs = append(argvs, strings.Join(argv, " ")), append(runs, o)
		return prog(argv, o)
	}
	m := codex.New()
	in, err := m.Detect(context.Background(), fh)
	if err != nil {
		t.Fatal(err)
	}
	return agent.Confine(fh, m.Spec(), in), in, &argvs, &runs
}

// A hand-off runs `codex cloud exec --env --branch [--attempts] <brief>` and reads the task's
// link; a small diff on a pushed branch goes in CODEX_STARTING_DIFF.
func TestSendCloud(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stand-in origin's hook needs a POSIX shell")
	}
	repo := demoRepo(t)
	h, in, argvs, runs := cloudHost(t, t.TempDir(), map[string]string{"FAKE_CODEX_ENVS": "env_1=acme-api,env_2=acme-web"})
	m := codex.New()
	ctx := context.Background()
	brief := agent.NotePrefix + "Fix the parser."
	sent, err := m.SendCloud(ctx, h, in, agent.SendRequest{Dir: repo, Repo: "github.com/example/demo", Branch: "main", Env: "acme-api", Brief: brief, Attempts: 2, Title: "Fix it"})
	cs := sent.Session
	if sent.Run != nil {
		t.Fatal("codex cloud exec needs no terminal")
	}
	if err != nil || !strings.HasPrefix(string(cs.Key.Session), "task_e_") || cs.URL != "https://chatgpt.com/codex/tasks/"+string(cs.Key.Session) ||
		cs.Branch != "main" || cs.Env != "acme-api" || cs.Attempts != 2 || cs.State != agent.CloudRunning || cs.Title != "Fix it" {
		t.Fatalf("exec: %+v %v", cs, err)
	}
	if got := (*argvs)[len(*argvs)-1]; got != "/bin/codex cloud exec --env acme-api --branch main --attempts 2 "+brief {
		t.Errorf("argv: %s", got)
	}
	diff := "diff --git a/x b/x\n--- a/x\n+++ b/x\n@@ -0,0 +1 @@\n+x\n"
	if _, err := m.SendCloud(ctx, h, in, agent.SendRequest{Dir: repo, Repo: "github.com/example/demo", Branch: "main", Env: "env_2", Brief: brief, Code: agent.ViaStartingDiff, Diff: []byte(diff)}); err != nil {
		t.Fatal(err)
	}
	if env := (*runs)[len(*runs)-1].Env; len(env) != 1 || env[0] != "CODEX_STARTING_DIFF="+diff {
		t.Errorf("starting diff: %q", env)
	}
	for _, r := range []agent.SendRequest{
		{Repo: "gitlab.example.com/acme/api", Branch: "main", Env: "env_1", Brief: brief},
		{Repo: "github.com/example/demo", Branch: "main", Brief: brief},
		{Repo: "github.com/example/demo", Branch: "main", Env: "env_1", Brief: "no prefix"},
		{Repo: "github.com/example/demo", Branch: "main", Env: "env_1", Brief: brief, Code: agent.ViaStartingDiff},
		{Repo: "github.com/example/demo", Branch: "main", Env: "env_1", Brief: brief, Code: agent.ViaStartingDiff, Diff: make([]byte, 300<<10)},
		{Repo: "github.com/example/demo", Branch: "main", Env: "env_1", Brief: brief, Attempts: 5},
		{Repo: "github.com/example/demo", Branch: "main", Env: "env_1", Brief: brief, Code: agent.ViaBundle},
	} {
		n := len(*argvs)
		if _, err := m.SendCloud(ctx, h, in, r); err == nil || len(*argvs) != n {
			t.Errorf("%+v: refused before running codex? %v", r, err)
		}
	}
	if _, err := m.SendCloud(ctx, h, in, agent.SendRequest{Dir: repo, Repo: "github.com/example/demo", Branch: "main", Env: "env_9", Brief: brief}); !errors.Is(err, agent.ErrNoEnvironment) ||
		!strings.Contains(err.Error(), "not found") {
		t.Errorf("an unknown environment: %v", err)
	}
	if _, err := m.SendCloud(ctx, h, in, agent.SendRequest{Dir: repo, Repo: "github.com/example/demo", Branch: "nope", Env: "env_1", Brief: brief}); err == nil {
		t.Error("a branch that is not on the remote")
	}
}

// A listing pages through `codex cloud list --json` with --cursor, filters by environment,
// asks for a recorded task the pages left out, and keeps going past an unreadable task.
func TestListCloud(t *testing.T) {
	dir := t.TempDir()
	st := fakecloud.Open(dir)
	var ids []string
	for i := 0; i < 23; i++ {
		env := "env_1"
		if i%2 == 1 {
			env = "env_2"
		}
		s, err := st.Seed(fakecloud.Session{Cloud: fakecloud.CodexCloud, Title: "task", Env: env, EnvLabel: "acme-" + env, State: fakecloud.StateDone,
			Diff: "diff --git a/a b/a\n--- a/a\n+++ b/a\n@@ -1 +1 @@\n-a\n+b\n"})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, s.ID)
	}
	h, in, argvs, _ := cloudHost(t, dir, map[string]string{"FAKE_CODEX_ENVS": "env_1=acme-env_1,env_2=acme-env_2"})
	m := codex.New()
	ctx := context.Background()
	l, err := m.ListCloud(ctx, h, in, agent.CloudQuery{Cloud: "codex-cloud", Limit: 40})
	if err != nil || len(l.Sessions) != 23 || len(l.Errors) != 0 {
		t.Fatalf("paged listing: %d %v %v", len(l.Sessions), l.Errors, err)
	}
	if !strings.Contains(strings.Join(*argvs, "\n"), "--cursor fake-cursor-20") {
		t.Errorf("no second page: %v", *argvs)
	}
	s := l.Sessions[0]
	if s.State != agent.CloudDone || s.Env == "" || !strings.HasPrefix(s.EnvLabel, "acme-") || s.Changes != "+1 −1 · 1 file" || s.Updated.IsZero() || s.Attempts != 1 {
		t.Errorf("a task: %+v", s)
	}
	l, err = m.ListCloud(ctx, h, in, agent.CloudQuery{Cloud: "codex-cloud", Env: "env_2", Limit: 5})
	if err != nil || len(l.Sessions) != 5 || l.Sessions[0].Env != "env_2" {
		t.Errorf("--env: %+v %v", l.Sessions, err)
	}
	if !strings.Contains((*argvs)[len(*argvs)-1], "--env env_2") {
		t.Errorf("argv: %s", (*argvs)[len(*argvs)-1])
	}
	oldest := agent.SessionID(ids[0])
	l, err = m.ListCloud(ctx, h, in, agent.CloudQuery{Cloud: "codex-cloud", Limit: 2, Known: []agent.SessionID{oldest, "task_e_gone0000", "not a task"}})
	if err != nil || len(l.Sessions) != 3 || l.Sessions[2].Key.Session != oldest || l.Sessions[2].State != agent.CloudDone || len(l.Errors) != 2 {
		t.Errorf("known tasks: %+v %+v %v", l.Sessions, l.Errors, err)
	}
	h, in, _, _ = cloudHost(t, dir, map[string]string{"FAKE_CLOUD_FAIL": "signed-out"})
	if _, err := m.ListCloud(ctx, h, in, agent.CloudQuery{}); !errors.Is(err, agent.ErrSignedOut) {
		t.Errorf("signed out: %v", err)
	}
}

// Bringing a task back: its title and state from status, its diff once it is done; a
// running task brings no diff and says so.
func TestFetchCloud(t *testing.T) {
	dir := t.TempDir()
	st := fakecloud.Open(dir)
	diff := "diff --git a/README.md b/README.md\n--- a/README.md\n+++ b/README.md\n@@ -1 +1,2 @@\n a\n+b\n"
	done, _ := st.Seed(fakecloud.Session{Cloud: fakecloud.CodexCloud, Title: "Add a line", Env: "env_1", EnvLabel: "acme-api", State: fakecloud.StateDone, Diff: diff})
	running, _ := st.Seed(fakecloud.Session{Cloud: fakecloud.CodexCloud, Title: "Still at it", Env: "env_1", State: fakecloud.StateRunning})
	h, in, _, _ := cloudHost(t, dir, nil)
	m := codex.New()
	ctx := context.Background()
	f, err := m.FetchCloud(ctx, h, in, agent.SessionID(done.ID), agent.FetchTarget{Code: agent.ViaDiff})
	if err != nil || f.Code.Way != agent.ViaDiff || string(f.Code.Diff) != diff || f.Run != nil || f.Adopt != nil || f.Segment == nil {
		t.Fatalf("done: %+v %v", f, err)
	}
	seg := f.Segment
	if seg.Header.Title != "Add a line" || len(seg.Nodes) != 2 || seg.Nodes[0].Text != "Add a line" || seg.Nodes[0].ID == "" ||
		!strings.Contains(seg.Nodes[1].Text, "README.md") || !strings.Contains(seg.Nodes[1].Text, "acme-api") || !strings.Contains(seg.Nodes[1].Text, "+1 −0 · 1 file") {
		t.Errorf("segment: %+v", seg.Nodes)
	}
	if len(f.Loss) == 0 {
		t.Error("the loss is listed")
	}
	f, err = m.FetchCloud(ctx, h, in, agent.SessionID(running.ID), agent.FetchTarget{Code: agent.ViaDiff})
	if err != nil || len(f.Code.Diff) != 0 || !strings.Contains(strings.Join(f.Loss, "\n"), "still running") {
		t.Errorf("running: %+v %v", f, err)
	}
	if _, err := m.FetchCloud(ctx, h, in, "task_e_nothere00", agent.FetchTarget{}); !errors.Is(err, agent.ErrNotFound) {
		t.Errorf("a task that is not there: %v", err)
	}
	if _, err := m.FetchCloud(ctx, h, in, "../../etc", agent.FetchTarget{}); !errors.Is(err, agent.ErrNotFound) {
		t.Errorf("not an id: %v", err)
	}
}

// The test reads the login without keeping an API key's line, checks the help and lists.
func TestTestCloud(t *testing.T) {
	m := codex.New()
	ctx := context.Background()
	h, in, argvs, _ := cloudHost(t, t.TempDir(), nil)
	ct, err := m.TestCloud(ctx, h, in, "codex-cloud")
	if err != nil || !strings.HasPrefix(ct.Account, "ChatGPT") || len(ct.Checks) != 3 {
		t.Fatalf("test: %+v %v", ct, err)
	}
	for _, c := range ct.Checks {
		if !c.OK {
			t.Errorf("check: %+v", c)
		}
	}
	if got := strings.Join(*argvs, "\n"); !strings.Contains(got, "/bin/codex login status") || !strings.Contains(got, "/bin/codex cloud list --limit 1 --json") {
		t.Errorf("argv: %s", got)
	}
	h, in, _, _ = cloudHost(t, t.TempDir(), map[string]string{"FAKE_CLOUD_FAIL": "signed-out"})
	ct, err = m.TestCloud(ctx, h, in, "codex-cloud")
	if !errors.Is(err, agent.ErrSignedOut) || len(ct.Checks) != 1 || ct.Checks[0].OK || strings.Contains(ct.Checks[0].Text, "sk-") ||
		!strings.Contains(ct.Checks[0].Text, "API key") {
		t.Errorf("an API key: %+v %v", ct, err)
	}
	h, in, _, _ = cloudHost(t, t.TempDir(), map[string]string{"FAKE_CODEX_LOGIN": "none"})
	if _, err := m.TestCloud(ctx, h, in, "codex-cloud"); !errors.Is(err, agent.ErrSignedOut) {
		t.Errorf("not logged in: %v", err)
	}
	h, in, _, _ = cloudHost(t, t.TempDir(), map[string]string{"FAKE_CLOUD_FAIL": "not-eligible"})
	if _, err := m.TestCloud(ctx, h, in, "codex-cloud"); !errors.Is(err, agent.ErrNotEligible) {
		t.Errorf("not eligible: %v", err)
	}
}

func TestParseCloudLink(t *testing.T) {
	m := codex.New()
	for s, want := range map[string]string{
		"https://chatgpt.com/codex/tasks/task_e_68f2c41a9b7c81909d3e":            "task_e_68f2c41a9b7c81909d3e",
		"chatgpt.com/codex/tasks/task_e_68f2c41a9b7c81909d3e?tab=diff#x":         "task_e_68f2c41a9b7c81909d3e",
		" task_e_68f2c41a9b7c81909d3e ":                                          "task_e_68f2c41a9b7c81909d3e",
		"https://chatgpt.com/codex/tasks/task_e_68f2c41a9b7c81909d3e/attempts/2": "task_e_68f2c41a9b7c81909d3e",
		"https://claude.ai/code/session_01abc":                                   "",
		"task_":                                                                  "",
		"https://chatgpt.com/codex/tasks/../../x":                                "",
	} {
		cl, id, ok := m.ParseCloudLink(s)
		if string(id) != want || ok != (want != "") || cl != "codex-cloud" {
			t.Errorf("%q: %s %s %v", s, cl, id, ok)
		}
	}
	if m.CloudURL("codex-cloud", "task_e_1234") != "https://chatgpt.com/codex/tasks/task_e_1234" {
		t.Error(m.CloudURL("codex-cloud", "task_e_1234"))
	}
}
