package jules

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/roeehrl/hopsesh/internal/testkit/fakecloud"
	"github.com/roeehrl/hopsesh/sdk/agent"
	"github.com/roeehrl/hopsesh/sdk/agent/agenttest"
)

const patch = "diff --git a/cloud-work/x.md b/cloud-work/x.md\nnew file mode 100644\n--- /dev/null\n+++ b/cloud-work/x.md\n@@ -0,0 +1 @@\n+Work done.\n"

func seeded(t *testing.T, dir string) fakecloud.Session {
	t.Helper()
	s, err := fakecloud.Open(dir).Seed(fakecloud.Session{Cloud: fakecloud.JulesCloud, Title: "Write unit tests", Repo: "github.com/example/demo",
		Branch: "main", State: fakecloud.StateDone, Diff: patch})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// Jules passes the cloud conformance kit against the stand-in jules: a session started from
// a checkout, worked on (its patch), listed and fetched.
func TestCloudConformance(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stand-in origin's hook needs a POSIX shell")
	}
	dir := t.TempDir()
	repo := fakecloud.DemoCheckout(t)
	agenttest.RunCloudWith(t, New(), fakecloud.Programs(dir, nil), agenttest.CloudOptions{
		Request: func(c agent.Cloud) agent.SendRequest { return sendRequest(repo) },
		Work: func(_ string, id agent.SessionID) {
			if err := fakecloud.Work(fakecloud.Proc{Vars: map[string]string{"FAKE_CLOUD_DIR": dir}}, string(id), false); err != nil {
				t.Fatal(err)
			}
		},
	})
}

// sendRequest is a hand-off as the core prepares it for Jules: the briefing asks Jules to
// check out the handoff branch.
func sendRequest(repo string) agent.SendRequest {
	b := "hopsesh/handoff/20261004-conform"
	return agent.SendRequest{Cloud: cloudName, Dir: repo, Repo: "github.com/example/demo", Branch: b, Base: "0123456789abcdef0123456789abcdef01234567",
		Brief: agent.NotePrefix + "This task continues a session (conformance).\nCode: branch " + b + ".\nYou may have started on another branch: before anything else, check this one out: `git fetch origin " + b + " && git checkout " + b + "`.\n",
		Title: "conformance"}
}

// SendCloud runs jules remote new with the repository and the briefing, and reads the new
// session's id from what it prints, or, failing that, from the listing; refusals map onto
// the SDK's errors.
func TestSendCloud(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stand-in origin's hook needs a POSIX shell")
	}
	dir := t.TempDir()
	repo := fakecloud.DemoCheckout(t)
	ctx := context.Background()
	m := New()
	var argv []string
	quiet := false
	fake := agenttest.NewFakeHost("/home/u")
	fake.AddBinary("jules", "0.1.42")
	progs := fakecloud.Programs(dir, nil)
	fake.Programs["jules"] = func(a []string, o agent.RunOptions) agent.Result {
		r := progs["jules"](a, o)
		if len(a) > 2 && a[2] == "new" {
			argv = a
			if quiet {
				r.Stdout = []byte("Done.\n")
			}
		}
		return r
	}
	in, _ := m.Detect(ctx, fake)
	h := agent.Confine(fake, m.Spec(), in)
	r := sendRequest(repo)
	cs, err := m.SendCloud(ctx, h, in, r)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(argv[1:5], " ") != "remote new --repo example/demo" || argv[5] != "--session" || argv[6] != r.Brief {
		t.Fatalf("jules ran as %q", argv)
	}
	st, err := fakecloud.Open(dir).Get(string(cs.Key.Session))
	if err != nil || st.Branch != r.Branch || cs.URL != URL(cs.Key.Session) || cs.State != agent.CloudRunning || cs.Branch != r.Branch {
		t.Fatalf("session %+v, in the cloud %+v %v", cs, st, err)
	}
	quiet = true
	cs2, err := m.SendCloud(ctx, h, in, r)
	if err != nil || cs2.Key.Session == "" || cs2.Key.Session == cs.Key.Session {
		t.Fatalf("found by the listing: %+v %v", cs2, err)
	}
	for fail, want := range map[string]error{"signed-out": agent.ErrSignedOut, "not-eligible": agent.ErrNotEligible, "repo-mismatch": agent.ErrRepoUnsupported} {
		h, in := host(t, dir, fail)
		if _, err := m.SendCloud(ctx, h, in, r); !errors.Is(err, want) {
			t.Errorf("%s: %v", fail, err)
		}
	}
	for in, want := range map[string]string{
		"Created session 1234567890 for example/demo.\n":    "1234567890",
		"https://jules.google.com/session/9876543210123\n":  "9876543210123",
		"Session ID: 5770320746137305562\nWorking on it…\n": "5770320746137305562",
	} {
		got := ""
		if id, ok := linkIn(in); ok {
			got = id
		} else if mm := newWords.FindStringSubmatch(in); mm != nil {
			got = mm[1]
		}
		if got != want {
			t.Errorf("%q: %q", in, got)
		}
	}
}

func host(t *testing.T, dir, fail string) (agent.Host, agent.Install) {
	t.Helper()
	fh := agenttest.NewFakeHost("/home/u")
	fh.AddBinary("jules", "0.1.42")
	vars := map[string]string{"FAKE_CLOUD_FAIL": fail}
	for k, p := range fakecloud.Programs(dir, vars) {
		fh.Programs[k] = p
	}
	m := New()
	in, _ := m.Detect(context.Background(), fh)
	return agent.Confine(fh, m.Spec(), in), in
}

func TestListFetchAndRefusals(t *testing.T) {
	dir := t.TempDir()
	s := seeded(t, dir)
	if _, err := fakecloud.Open(dir).Seed(fakecloud.Session{Cloud: fakecloud.JulesCloud, Title: "Still planning it", Repo: "github.com/example/other"}); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	m := New()
	h, in := host(t, dir, "")
	l, err := m.ListCloud(ctx, h, in, agent.CloudQuery{Repo: "github.com/example/demo"})
	if err != nil || len(l.Sessions) != 1 || len(l.Errors) != 0 {
		t.Fatalf("ListCloud: %+v %v", l, err)
	}
	cs := l.Sessions[0]
	if cs.Key.Session != agent.SessionID(s.ID) || cs.State != agent.CloudDone || cs.Title != "Write unit tests" || cs.URL != URL(cs.Key.Session) ||
		time.Since(cs.Updated) > time.Hour {
		t.Errorf("session: %+v", cs)
	}
	f, err := m.FetchCloud(ctx, h, in, cs.Key.Session, agent.FetchTarget{Code: agent.ViaDiff})
	if err != nil || f.Code.Way != agent.ViaDiff || string(f.Code.Diff) != patch || f.Segment != nil {
		t.Errorf("FetchCloud: %+v %v", f, err)
	}
	ct, err := m.TestCloud(ctx, h, in, cloudName)
	if err != nil || !strings.Contains(ct.Checks[0].Text, "2 connected") {
		t.Errorf("TestCloud: %+v %v", ct, err)
	}
	for fail, want := range map[string]error{"signed-out": agent.ErrSignedOut, "not-eligible": agent.ErrNotEligible} {
		h, in := host(t, dir, fail)
		if _, err := m.ListCloud(ctx, h, in, agent.CloudQuery{}); !errors.Is(err, want) {
			t.Errorf("%s: %v", fail, err)
		}
		if _, err := m.FetchCloud(ctx, h, in, cs.Key.Session, agent.FetchTarget{}); !errors.Is(err, want) {
			t.Errorf("%s: fetch %v", fail, err)
		}
	}
	if _, err := m.FetchCloud(ctx, h, in, "--apply", agent.FetchTarget{}); !errors.Is(err, agent.ErrNotFound) {
		t.Errorf("a flag as an id: %v", err)
	}
}

// The list's layout is not documented: columns by the header, rows read by their cells'
// shapes when a description holds two spaces or a column is empty.
func TestParseSessions(t *testing.T) {
	now := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	out := `ID                   Description                            Repo           Last active  Status
5770320746137305562  Update README with Jules integration   example/repo   2h ago       Completed
424616855579134593   Fix  the build                         example/repo   5m ago       In Progress
12345678             No repository here                                    yesterday    Awaiting User Feedback
!!!                  broken
`
	rows, errs := parseSessions(out, now)
	if len(rows) != 3 || len(errs) != 1 {
		t.Fatalf("rows %+v errs %v", rows, errs)
	}
	if r := rows[0]; r.id != "5770320746137305562" || r.repo != "example/repo" || r.state != agent.CloudDone || !r.updated.Equal(now.Add(-2*time.Hour)) {
		t.Errorf("row 0: %+v", r)
	}
	if r := rows[1]; r.title != "Fix  the build" || r.state != agent.CloudRunning || r.repo != "example/repo" || !r.updated.Equal(now.Add(-5*time.Minute)) {
		t.Errorf("row 1: %+v", r)
	}
	if r := rows[2]; r.repo != "" || r.state != agent.CloudIdle || !r.updated.Equal(now.Add(-24*time.Hour)) {
		t.Errorf("row 2: %+v", r)
	}
	// No header at all: the documented column order.
	rows, errs = parseSessions("777777  A task  example/demo  3 days ago  Failed\n", now)
	if len(rows) != 1 || len(errs) != 0 || rows[0].state != agent.CloudFailed || rows[0].repo != "example/demo" {
		t.Errorf("headerless: %+v %v", rows, errs)
	}
	if rows, errs := parseSessions("No sessions found.\n", now); len(rows)+len(errs) != 0 {
		t.Errorf("empty: %+v %v", rows, errs)
	}
}

func TestPatchOf(t *testing.T) {
	for in, want := range map[string]string{
		patch:                          patch,
		"Pulling session 1…\n" + patch: patch,
		"--- a/x\n+++ b/x\n":           "--- a/x\n+++ b/x\n",
		"Nothing to pull.\n":           "",
	} {
		if got := string(patchOf([]byte(in))); got != want {
			t.Errorf("patchOf(%q) = %q", in, got)
		}
	}
}

// A session link counts only on Jules's own host: another host before or after it, user
// info or a port does not.
func TestLinkInHostileHosts(t *testing.T) {
	for in, want := range map[string]string{
		"https://jules.google.com/session/1234567890\n":                   "1234567890",
		"Open https://jules.google/session/1234567890.\n":                 "1234567890",
		"https://jules.google.com.evil.example/session/1234567890\n":      "",
		"https://evil.example/jules.google.com/session/1234567890\n":      "",
		"https://evil.example/?next=https://jules.google.com/session/123": "",
		"https://user@jules.google.com/session/1234567890\n":              "",
		"https://jules.google.com:444/session/1234567890\n":               "",
		"http://jules.google.com/session/1234567890\n":                    "",
		"https://evil-jules.google.com/session/1234567890\n":              "",
	} {
		if got, _ := linkIn(in); got != want {
			t.Errorf("linkIn(%q) = %q, want %q", in, got, want)
		}
	}
}
