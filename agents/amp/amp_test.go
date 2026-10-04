package amp

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/roeehrl/hopsesh/internal/testkit/fakecloud"
	"github.com/roeehrl/hopsesh/sdk/agent"
	"github.com/roeehrl/hopsesh/sdk/agent/agenttest"
	"github.com/roeehrl/hopsesh/sdk/ir"
)

func seeded(t *testing.T, dir string) fakecloud.Session {
	t.Helper()
	s, err := fakecloud.Open(dir).Seed(fakecloud.Session{Cloud: fakecloud.AmpCloud, Title: "Speed up the importer", State: fakecloud.StateIdle,
		Messages: []fakecloud.Message{{Role: "user", Text: "Speed up the importer"}, {Role: "assistant", Text: "I batched the inserts."}}})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// Amp passes the cloud conformance kit against the stand-in amp.
func TestCloudConformance(t *testing.T) {
	dir := t.TempDir()
	agenttest.RunCloudWith(t, New(), fakecloud.Programs(dir, nil), agenttest.CloudOptions{
		Seed: func(string) agent.SessionID { return agent.SessionID(seeded(t, dir).ID) },
	})
}

func host(t *testing.T, dir, fail string) (agent.Host, agent.Install) {
	t.Helper()
	fh := agenttest.NewFakeHost("/home/u")
	fh.AddBinary("amp", "0.0.1791107882-gfe04cc")
	for k, p := range fakecloud.Programs(dir, map[string]string{"FAKE_CLOUD_FAIL": fail}) {
		fh.Programs[k] = p
	}
	m := New()
	in, _ := m.Detect(context.Background(), fh)
	return agent.Confine(fh, m.Spec(), in), in
}

func TestListFetchAndRefusals(t *testing.T) {
	dir := t.TempDir()
	s := seeded(t, dir)
	ctx := context.Background()
	m := New()
	h, in := host(t, dir, "")
	l, err := m.ListCloud(ctx, h, in, agent.CloudQuery{})
	if err != nil || len(l.Sessions) != 1 || len(l.Errors) != 0 {
		t.Fatalf("ListCloud: %+v %v", l, err)
	}
	if cs := l.Sessions[0]; cs.Key.Session != agent.SessionID(s.ID) || cs.Title != "Speed up the importer" || cs.URL != "https://ampcode.com/threads/"+s.ID {
		t.Errorf("thread: %+v", cs)
	}
	f, err := m.FetchCloud(ctx, h, in, agent.SessionID(s.ID), agent.FetchTarget{})
	if err != nil || f.Segment == nil || f.Segment.Header.Title != "Speed up the importer" || len(f.Segment.Nodes) != 2 ||
		f.Segment.Nodes[0].Actor != ir.User || f.Segment.Nodes[1].Text != "I batched the inserts." || f.Code.Way != "" {
		t.Errorf("FetchCloud: %+v %v", f.Segment, err)
	}
	for fail, want := range map[string]error{"signed-out": agent.ErrSignedOut, "not-eligible": agent.ErrNotEligible} {
		h, in := host(t, dir, fail)
		if _, err := m.ListCloud(ctx, h, in, agent.CloudQuery{}); !errors.Is(err, want) {
			t.Errorf("%s: %v", fail, err)
		}
	}
	if _, err := m.FetchCloud(ctx, h, in, "--help", agent.FetchTarget{}); !errors.Is(err, agent.ErrNotFound) {
		t.Errorf("a flag as an id: %v", err)
	}
}

func TestParseThreads(t *testing.T) {
	now := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	out := `Title                         Last Updated   Visibility   Messages   Thread ID
─────────────────────────     ────────────   ──────────   ────────   ─────────
Speed up the importer         2h ago         Private      12         T-5f0c2a9e-1b7d-4c1e-9f3a-0a1b2c3d4e5f
Fix  login                    3 days ago     Workspace    4          T-aaaa1111
broken row                    ?              ?            ?          nope
`
	rows, errs := parseThreads(out, now)
	if len(rows) != 2 || len(errs) != 1 {
		t.Fatalf("%+v %v", rows, errs)
	}
	if r := rows[0]; r.title != "Speed up the importer" || r.messages != 12 || !r.updated.Equal(now.Add(-2*time.Hour)) {
		t.Errorf("row 0: %+v", r)
	}
	if r := rows[1]; r.id != "T-aaaa1111" || !r.updated.Equal(now.Add(-72*time.Hour)) {
		t.Errorf("row 1: %+v", r)
	}
	if rows, errs := parseThreads("No records found.\n", now); len(rows)+len(errs) != 0 {
		t.Errorf("empty: %v %v", rows, errs)
	}
}

func TestMarkdownSegment(t *testing.T) {
	seg := markdownSegment("T-x1234", []byte("# A title\n\n## User\n\nDo it\n\n## Assistant\n\nDone.\n\n```sh\nmake\n```\n"))
	if seg.Header.Title != "A title" || len(seg.Nodes) != 2 || seg.Nodes[1].Text != "Done.\n\n```sh\nmake\n```" {
		t.Errorf("%+v", seg)
	}
	seg = markdownSegment("T-x1234", []byte("Some export without headings.\n"))
	if len(seg.Nodes) != 1 || seg.Nodes[0].Actor != ir.Agent {
		t.Errorf("no headings: %+v", seg)
	}
	m := New()
	for _, s := range []string{"https://ampcode.com/threads/T-abcd1234.md", "T-abcd1234", "https://ampcode.com/threads/T-abcd1234?x=1"} {
		if cl, sid, ok := m.ParseCloudLink(s); !ok || cl != "amp" || sid != "T-abcd1234" {
			t.Errorf("ParseCloudLink(%q) = %s %s %v", s, cl, sid, ok)
		}
	}
}
