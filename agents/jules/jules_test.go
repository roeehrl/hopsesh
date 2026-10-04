package jules

import (
	"context"
	"errors"
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

// Jules passes the cloud conformance kit against the stand-in jules.
func TestCloudConformance(t *testing.T) {
	dir := t.TempDir()
	agenttest.RunCloudWith(t, New(), fakecloud.Programs(dir, nil), agenttest.CloudOptions{
		Seed: func(string) agent.SessionID { return agent.SessionID(seeded(t, dir).ID) },
	})
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
