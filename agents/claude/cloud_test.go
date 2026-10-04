package claude_test

import (
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/roeehrl/hopsesh/agents/claude"
	"github.com/roeehrl/hopsesh/internal/core/term"
	"github.com/roeehrl/hopsesh/internal/testkit/fakecloud"
	"github.com/roeehrl/hopsesh/sdk/agent"
	"github.com/roeehrl/hopsesh/sdk/agent/agenttest"
)

// Claude Code's cloud passes the cloud conformance kit against the stand-in cloud: it starts
// a session from a briefing in a checkout of a repository on the stand-in GitHub, follows
// it up, lists what it is told about (Claude Code has no list command), fetches with a
// teleport the user runs, reads its links back, and maps a login the cloud cannot use.
func TestCloudConformance(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stand-in origin's hook needs a POSIX shell")
	}
	dir := t.TempDir()
	repo := demoRepo(t)
	agenttest.RunCloudWith(t, claude.New(), fakecloud.Programs(dir, nil), agenttest.CloudOptions{
		Request: func(c agent.Cloud) agent.SendRequest {
			return agent.SendRequest{Cloud: c.Name, Dir: repo, Repo: "github.com/example/demo", Branch: "main", Brief: agent.NotePrefix + "This task continues a session (conformance).", Title: "conformance"}
		},
		Step: fakecloud.TerminalStep(dir, nil, nil),
	})
}

// demoRepo is a checkout of github.com/example/demo, whose remote is a local bare
// repository standing in for GitHub, with main pushed.
func demoRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	gitConfig := filepath.Join(root, "gitconfig")
	for k, v := range map[string]string{"GIT_CONFIG_GLOBAL": gitConfig, "GIT_CONFIG_NOSYSTEM": "1", "GIT_AUTHOR_NAME": "Sam Doe", "GIT_AUTHOR_EMAIL": "sam@example.com",
		"GIT_COMMITTER_NAME": "Sam Doe", "GIT_COMMITTER_EMAIL": "sam@example.com", "FAKE_CLOUD_FAIL": "", "CCR_FORCE_BUNDLE": ""} {
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

// SendCloud checks the login and returns `claude --cloud <brief>` for the user's terminal,
// in the handoff branch's folder, without the variables the cloud must not inherit; with
// the bundle, it sets CCR_FORCE_BUNDLE=1. It runs nothing but auth status itself.
func TestSendIsATerminalStep(t *testing.T) {
	var argvs []string
	fh := agenttest.NewFakeHost(home)
	fh.AddBinary("claude", "2.1.284 (Claude Code)")
	fh.Programs["claude"] = func(argv []string, _ agent.RunOptions) agent.Result {
		argvs = append(argvs, strings.Join(argv[1:], " "))
		return agent.Result{Stdout: []byte(maxLogin)}
	}
	fh.Put(home+"/.claude/projects/.keep", nil, time.Now())
	m := claude.New()
	in, _ := m.Detect(context.Background(), fh)
	h := agent.Confine(fh, m.Spec(), in)
	ctx := context.Background()
	r := agent.SendRequest{Cloud: "claude-cloud", Dir: "/home/u/.local/state/hopsesh/handoff/github.com/example/demo", Repo: "github.com/example/demo",
		Branch: "hopsesh/handoff/20261004-0b6c6a8e", Base: "4c1e9a2", Brief: agent.NotePrefix + "This task continues a Claude Code session.", Title: "Fix the parser"}
	sent, err := m.SendCloud(ctx, h, in, r)
	if err != nil {
		t.Fatal(err)
	}
	run := sent.Run
	if run == nil || strings.Join(run.Argv, "|") != "claude|--cloud|"+r.Brief || run.Dir != r.Dir || len(run.Env) != 0 ||
		strings.Join(run.Unset, " ") != "CLAUDE_CODE_CHILD_SESSION ANTHROPIC_API_KEY" || sent.Session.Key.Session != "" {
		t.Fatalf("send: %+v", sent)
	}
	if strings.Join(argvs, ",") != "auth status --json" {
		t.Errorf("SendCloud ran %v", argvs)
	}
	r.Code = agent.ViaBundle
	if sent, err = m.SendCloud(ctx, h, in, r); err != nil || strings.Join(sent.Run.Env, " ") != "CCR_FORCE_BUNDLE=1" {
		t.Errorf("bundle: %+v %v", sent.Run, err)
	}
	if _, err := m.SendCloud(ctx, h, in, agent.SendRequest{Brief: "no prefix", Dir: "/w"}); err == nil {
		t.Error("a briefing without hopsesh's prefix is refused")
	}
	if _, ok := any(m).(agent.CloudFollower); ok {
		t.Error("Claude Code has no non-interactive follow-up; the module must not claim one")
	}
	if cl, _ := m.Spec().FindCloud("claude-cloud"); !strings.Contains(cl.NoFollowUp, "claude.ai") {
		t.Errorf("the cloud says why there is no follow-up: %q", cl.NoFollowUp)
	}
}

// ReadStep reads what `claude --cloud` printed in a terminal, as 2.1.284 printed it: the
// link (with its query), checked against the teleport command; colours, a cursor that
// moves, and a link broken where the terminal ends; and the refusals and the ends without
// a session.
func TestReadStep(t *testing.T) {
	m := claude.New()
	const id = "session_01ABCDEFGHJKMNPQRSTVWXYZ01"
	ok := "Created cloud session: Session ready\nView: https://claude.ai/code/" + id + "?from=cli&m=0\nResume with: claude --teleport " + id + "\n"
	for name, tc := range map[string]struct {
		raw   string
		width int
		code  int
		want  string
		err   error
		words string
	}{
		"plain":             {raw: ok, want: id},
		"colours":           {raw: "\x1b[1mCreated cloud session:\x1b[22m Session ready\r\n\x1b[1mView:\x1b[22m \x1b]8;;https://claude.ai/code/" + id + "\x1b\\https://claude.ai/code/" + id + "?from=cli&m=0\x1b]8;;\x1b\\\r\n\x1b[2mResume with:\x1b[22m claude --teleport " + id + "\r\n", want: id},
		"cursor moves":      {raw: "\x1b[2J\x1b[HQuick safety check: Is this a project you created or one you trust?\x1b[3;1H❯ 1. Yes\x1b[2J\x1b[HCreated cloud session: Session ready\x1b[2;1HView: https://claude.ai/code/" + id + "?from=cli&m=0\x1b[3;1HResume with: claude --teleport " + id, want: id},
		"wrapped link":      {raw: "View: https://claude.ai/code/session_01ABCDEFGHJKMN\nPQRSTVWXYZ01?from=cli&m=0\nResume with: claude --teleport " + id + "\n", width: 50, want: id},
		"wrapped, no width": {raw: "View: https://claude.ai/code/session_01ABCDEFGHJKMN\nPQRSTVWXYZ01?from=cli&m=0\nResume with: claude --teleport " + id + "\n", want: id},
		"link only":         {raw: "View: https://claude.ai/code/cse_01ABCDEFGHJKMNPQRSTVWXYZ01\n", want: id},
		"teleport only":     {raw: "Resume with: claude --teleport " + id + "\n", want: id},
		"no terminal":       {raw: "Error: --cloud requires an interactive terminal. Non-interactive invocations (piped stdout, --init-only, --sdk-url) run locally and would silently ignore --cloud. Drop --cloud, or run from a TTY.\n", code: 1, words: "found no terminal"},
		"print":             {raw: "Error: --cloud cannot be combined with --print.\n", code: 1, words: "claude refused: Error: --cloud cannot be combined with --print."},
		"signed out":        {raw: "Error: Unable to get organization UUID\n", code: 1, err: agent.ErrSignedOut},
		"not eligible":      {raw: "Error: Claude Code on the web is not available for your plan or organization.\n", code: 1, err: agent.ErrNotEligible},
		"repository":        {raw: "Error: Claude Code can't send this repository to the cloud: it is inside a submodule.\n", code: 1, err: agent.ErrRepoUnsupported},
		"trust, no":         {raw: "Quick safety check: Is this a project you created or one you trust?\n❯ 1. Yes, I trust this folder\n  2. No, exit\n", code: 1, err: agent.ErrNoSession, words: "asked whether you trust"},
		"stopped":           {raw: "Quick safety check: Is this a project you created or one you trust?\n", code: 130, err: agent.ErrNoSession, words: "stopped"},
		"another host":      {raw: "View: https://claude.ai.evil.example/code/" + id + "\n", err: agent.ErrNoSession, words: "without printing a session link"},
		"a nested link":     {raw: "View: \x1b]8;;https://evil.example/?u=https://claude.ai/code/" + id + "\x1b\\open\x1b]8;;\x1b\\\n", err: agent.ErrNoSession, words: "without printing a session link"},
		"nothing":           {raw: "", code: 0, err: agent.ErrNoSession, words: "without printing a session link"},
		"something else":    {raw: "something unexpected\n", code: 3, err: agent.ErrNoSession, words: "exit status 3"},
	} {
		t.Run(name, func(t *testing.T) {
			cs, err := m.ReadStep("claude-cloud", agent.StepOutput{Text: term.Plain([]byte(tc.raw)), Width: tc.width, Code: tc.code})
			if tc.want != "" {
				if err != nil || string(cs.Key.Session) != tc.want || cs.URL != "https://claude.ai/code/"+tc.want || cs.Cloud != "claude-cloud" ||
					cs.Key.Agent != "claude" || cs.State != agent.CloudRunning {
					t.Fatalf("%+v %v", cs, err)
				}
				return
			}
			if err == nil || tc.err != nil && !errors.Is(err, tc.err) || !strings.Contains(err.Error(), tc.words) {
				t.Fatalf("want %v with %q, got %v", tc.err, tc.words, err)
			}
		})
	}
	if _, err := m.ReadStep("codex-cloud", agent.StepOutput{Text: ok}); !errors.Is(err, agent.ErrUnsupported) {
		t.Errorf("another cloud: %v", err)
	}
}

// The stand-in claude refuses what the real one refuses: --cloud with -p, and --cloud
// without a terminal; in a terminal, it prints the session's three lines.
func TestStandInRefusesLikeTheRealOne(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stand-in origin's hook needs a POSIX shell")
	}
	dir, repo := t.TempDir(), demoRepo(t)
	progs := fakecloud.Programs(dir, nil)
	r := progs["claude"]([]string{"claude", "-p", "[hopsesh] x", "--cloud", "--output-format", "json"}, agent.RunOptions{Dir: repo})
	if r.Code != 1 || !strings.Contains(string(r.Stderr), "--cloud cannot be combined with --print") {
		t.Errorf("-p with --cloud: %d %s", r.Code, r.Stderr)
	}
	r = progs["claude"]([]string{"claude", "--cloud", "[hopsesh] x"}, agent.RunOptions{Dir: repo})
	if r.Code != 1 || !strings.Contains(string(r.Stderr), "--cloud requires an interactive terminal") {
		t.Errorf("--cloud without a terminal: %d %s", r.Code, r.Stderr)
	}
	out := fakecloud.TerminalStep(dir, nil, nil)(agent.Command{Argv: []string{"claude", "--cloud", "[hopsesh] x"}, Dir: repo})
	cs, err := claude.New().ReadStep("claude-cloud", out)
	if err != nil || !strings.HasPrefix(string(cs.Key.Session), "session_01") || !strings.Contains(out.Text, "Created cloud session: Session ready") {
		t.Errorf("in a terminal: %+v %v\n%s", cs, err, out.Text)
	}
}

const home = "/home/u"

// host is a machine with Claude Code signed in as auth (a JSON line), and its config folder.
func host(t *testing.T, auth string) (*agenttest.FakeHost, agent.Host, agent.Install) {
	t.Helper()
	fh := agenttest.NewFakeHost(home)
	fh.AddBinary("claude", "2.1.284 (Claude Code)")
	fh.Programs["claude"] = func(argv []string, _ agent.RunOptions) agent.Result {
		switch strings.Join(argv[1:], " ") {
		case "auth status --json":
			return agent.Result{Stdout: []byte(auth)}
		case "--help":
			return agent.Result{Stdout: []byte("  --teleport [session]  Resume a teleport session\n")}
		}
		return agent.Result{Code: 1}
	}
	fh.Put(home+"/.claude/projects/.keep", nil, time.Now())
	m := claude.New()
	in, err := m.Detect(context.Background(), fh)
	if err != nil {
		t.Fatal(err)
	}
	return fh, agent.Confine(fh, m.Spec(), in), in
}

const maxLogin = `{"loggedIn":true,"authMethod":"claude.ai","apiProvider":"firstParty","orgId":"org-1","subscriptionType":"max"}`

func TestParseCloudLink(t *testing.T) {
	m := claude.New()
	for in, want := range map[string]string{
		"https://claude.ai/code/session_01ABCdef234":            "session_01ABCdef234",
		"claude.ai/code/session_01ABCdef234?tab=diff":           "session_01ABCdef234",
		"  cse_01ABCdef234 ":                                    "session_01ABCdef234",
		"session_01ABCdef234":                                   "session_01ABCdef234",
		"https://claude.ai/code/session_01ABCdef234/terminal#x": "session_01ABCdef234",
	} {
		cl, id, ok := m.ParseCloudLink(in)
		if !ok || cl != "claude-cloud" || string(id) != want {
			t.Errorf("%q: %s %s %v", in, cl, id, ok)
		}
	}
	for _, bad := range []string{"", "session_", "https://example.com/code/session_01ABCdef234", "0b6c6a8e-1d2f-4c3b-9a7e-5f4d3c2b1a01", "session_01 x",
		"http://claude.ai/code/session_01ABCdef234", "https://claude.ai.evil.example/code/session_01ABCdef234", "https://user@claude.ai/code/session_01ABCdef234",
		"https://claude.ai:8443/code/session_01ABCdef234", "https://evil.example/claude.ai/code/session_01ABCdef234", "https://claude.ai/share/session_01ABCdef234"} {
		if _, _, ok := m.ParseCloudLink(bad); ok {
			t.Errorf("%q read as a link", bad)
		}
	}
	if u := m.CloudURL("claude-cloud", "session_01ABCdef234"); u != "https://claude.ai/code/session_01ABCdef234" {
		t.Error(u)
	}
}

// A login the cloud cannot use is refused with the SDK's errors, before anything else runs.
func TestLoginRefusals(t *testing.T) {
	ctx := context.Background()
	for auth, want := range map[string]error{
		`{"loggedIn":false}`: agent.ErrSignedOut,
		`{"loggedIn":true,"authMethod":"api-key","apiProvider":"firstParty"}`:                             agent.ErrSignedOut,
		`{"loggedIn":true,"authMethod":"claude.ai","apiProvider":"bedrock"}`:                              agent.ErrSignedOut,
		`{"loggedIn":true,"authMethod":"claude.ai","apiProvider":"firstParty","subscriptionType":"free"}`: agent.ErrNotEligible,
	} {
		_, h, in := host(t, auth)
		m := claude.New()
		if _, err := m.ListCloud(ctx, h, in, agent.CloudQuery{Cloud: "claude-cloud"}); !errors.Is(err, want) {
			t.Errorf("%s: list: %v", auth, err)
		}
		if _, err := m.FetchCloud(ctx, h, in, "session_01ABCdef234", agent.FetchTarget{Dir: "/w"}); !errors.Is(err, want) {
			t.Errorf("%s: fetch: %v", auth, err)
		}
		if ct, err := m.TestCloud(ctx, h, in, "claude-cloud"); !errors.Is(err, want) || len(ct.Checks) != 1 || ct.Checks[0].OK {
			t.Errorf("%s: test: %+v %v", auth, ct, err)
		}
	}
	_, h, in := host(t, maxLogin)
	ct, err := claude.New().TestCloud(ctx, h, in, "claude-cloud")
	if err != nil || ct.Account != "claude.ai · max" || len(ct.Checks) != 3 || !ct.Checks[1].OK || ct.Checks[2].OK {
		t.Fatalf("test: %+v %v", ct, err)
	}
}

// The listing is what hopsesh knows (ids in either form, once each) plus the local
// sessions Remote Control mirrors; it is partial, and the states are unknown.
func TestListCloudKnownAndMirrors(t *testing.T) {
	fh, h, in := host(t, maxLogin)
	rec := `{"type":"user","uuid":"u1","parentUuid":null,"sessionId":"loc-1","cwd":"/home/u/git/demo","timestamp":"2026-10-01T10:00:00Z","message":{"role":"user","content":"refactor the auth middleware"}}` + "\n" +
		`{"type":"bridge-session","bridgeSessionId":"cse_01MirrorAbc123","lastSequenceNum":4,"sessionId":"loc-1","ownerAccountUuid":"acct-1","ownerOrganizationUuid":"org-1"}` + "\n"
	fh.Put(home+"/.claude/projects/-home-u-git-demo/loc-1.jsonl", []byte(rec), time.Now())
	m := claude.New()
	ls, err := m.List(context.Background(), h, in)
	if err != nil || len(ls.Sessions) != 1 {
		t.Fatalf("list: %+v %v", ls, err)
	}
	mr := ls.Sessions[0].Mirror
	if mr == nil || mr.ID != "session_01MirrorAbc123" || mr.URL != "https://claude.ai/code/session_01MirrorAbc123" || mr.Account == "" {
		t.Fatalf("mirror: %+v", mr)
	}
	for _, local := range [][]agent.Summary{ls.Sessions, nil} {
		l, err := m.ListCloud(context.Background(), h, in, agent.CloudQuery{Cloud: "claude-cloud", Local: local,
			Known: []agent.SessionID{"session_01KnownXyz789", "cse_01KnownXyz789", "bogus"}})
		if err != nil || !l.Partial || len(l.Sessions) != 2 || len(l.Errors) != 1 {
			t.Fatalf("listing: %+v %v", l, err)
		}
		k, mir := l.Sessions[0], l.Sessions[1]
		if k.Key.Session != "session_01KnownXyz789" || k.State != agent.CloudUnknown || k.Mirror || k.URL == "" {
			t.Errorf("known: %+v", k)
		}
		if !mir.Mirror || mir.Local != "loc-1" || mir.Title != "refactor the auth middleware" || mir.Account != mr.Account {
			t.Errorf("mirror entry: %+v", mir)
		}
	}
}

// FetchCloud returns the teleport for the user's terminal, run without the variables the
// cloud must not inherit, and says where its copy will appear.
func TestFetchCloud(t *testing.T) {
	_, h, in := host(t, maxLogin)
	f, err := claude.New().FetchCloud(context.Background(), h, in, "cse_01ABCdef234", agent.FetchTarget{Dir: "/home/u/git/demo-fix"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(f.Run.Argv, " ") != "claude --teleport session_01ABCdef234" || f.Run.Dir != "/home/u/git/demo-fix" ||
		strings.Join(f.Run.Unset, " ") != "CLAUDE_CODE_CHILD_SESSION ANTHROPIC_API_KEY" {
		t.Errorf("run: %+v", f.Run)
	}
	if f.Adopt.Root != "home" || f.Adopt.Dir != "projects/-home-u-git-demo-fix" || f.Adopt.Session != "session_01ABCdef234" || len(f.Loss) == 0 {
		t.Errorf("adopt: %+v %v", f.Adopt, f.Loss)
	}
	f, err = claude.New().FetchCloud(context.Background(), h, in, "", agent.FetchTarget{Dir: "/w"})
	if err != nil || strings.Join(f.Run.Argv, " ") != "claude --teleport" || f.Adopt.Session != "" {
		t.Errorf("the picker: %+v %v", f, err)
	}
}

// Adopted finds the teleported copy by its teleported-from record and counts its
// messages; a reply split over records counts once.
func TestAdopted(t *testing.T) {
	fh, h, in := host(t, maxLogin)
	m := claude.New()
	since := time.Now().Add(-time.Minute)
	a := agent.Adopt{Root: "home", Dir: "projects/-w", Since: since, Session: "session_01ABCdef234"}
	if _, err := m.Adopted(context.Background(), h, in, a); !errors.Is(err, agent.ErrNotFound) {
		t.Fatalf("nothing there yet: %v", err)
	}
	user := func(u, text string) string {
		return `{"type":"user","uuid":"` + u + `","sessionId":"t1","cwd":"/w","timestamp":"2026-10-04T10:00:00Z","message":{"role":"user","content":"` + text + `"}}`
	}
	asst := func(u, msg string) string {
		return `{"type":"assistant","uuid":"` + u + `","sessionId":"t1","cwd":"/w","timestamp":"2026-10-04T10:00:01Z","message":{"id":"` + msg + `","role":"assistant","content":[{"type":"text","text":"ok"}]}}`
	}
	stub := func(remote string, n int) string {
		return `{"type":"teleported-from","remoteSessionId":"` + remote + `","messageCount":` + string(rune('0'+n)) + `}`
	}
	// Another session's copy, then this one's, split reply and all.
	fh.Put(home+"/.claude/projects/-w/other.jsonl", []byte(stub("session_01Other0000", 2)+"\n"+user("a", "x")+"\n"), time.Now())
	fh.Put(home+"/.claude/projects/-w/t1.jsonl", []byte(strings.Join([]string{stub("session_01ABCdef234", 3), user("u1", "fix it"), asst("a1", "m1"), asst("a2", "m1"), user("u2", "thanks")}, "\n")+"\n"), time.Now())
	got, err := m.Adopted(context.Background(), h, in, a)
	if err != nil || got.Session.Key.Session != "t1" || got.Restored != 3 || got.Expected != 3 || !got.Stated || got.Remote != "session_01ABCdef234" {
		t.Fatalf("adopted: %+v %v", got, err)
	}
	// Partial: the cloud had more than the copy holds.
	fh.Put(home+"/.claude/projects/-w/t1.jsonl", []byte(stub("session_01ABCdef234", 9)+"\n"+user("u1", "fix it")+"\n"), time.Now())
	if got, _ = m.Adopted(context.Background(), h, in, a); got.Restored != 1 || got.Expected != 9 {
		t.Fatalf("partial: %+v", got)
	}
	// The picker: any teleported copy; an old file is not one.
	fh.Put(home+"/.claude/projects/-w/old.jsonl", []byte(stub("session_01Old000000", 1)+"\n"), since.Add(-time.Hour))
	a.Session = ""
	if got, err = m.Adopted(context.Background(), h, in, a); err != nil || got.Remote == "session_01Old000000" {
		t.Fatalf("picker: %+v %v", got, err)
	}
}
