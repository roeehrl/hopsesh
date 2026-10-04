package claude_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/roeehrl/hopsesh/agents/claude"
	"github.com/roeehrl/hopsesh/internal/testkit/fakecloud"
	"github.com/roeehrl/hopsesh/sdk/agent"
	"github.com/roeehrl/hopsesh/sdk/agent/agenttest"
)

// Claude Code's cloud passes the cloud conformance kit against the stand-in cloud: it lists
// what it is told about (Claude Code has no list command), fetches with a teleport the
// user runs, reads its links back, and maps a login the cloud cannot use.
func TestCloudConformance(t *testing.T) {
	dir := t.TempDir()
	agenttest.RunCloudWith(t, claude.New(), fakecloud.Programs(dir, nil), agenttest.CloudOptions{
		Seed: func(cloud string) agent.SessionID {
			s, err := fakecloud.Open(dir).Seed(fakecloud.Session{Cloud: fakecloud.ClaudeCloud, Title: "seeded", Repo: "github.com/example/demo", Branch: "main"})
			if err != nil {
				t.Fatal(err)
			}
			return agent.SessionID(s.ID)
		},
	})
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
	for _, bad := range []string{"", "session_", "https://example.com/code/session_01ABCdef234", "0b6c6a8e-1d2f-4c3b-9a7e-5f4d3c2b1a01", "session_01 x"} {
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
