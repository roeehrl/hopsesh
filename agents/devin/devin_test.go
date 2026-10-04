package devin

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/roeehrl/hopsesh/internal/testkit/fakecloud"
	"github.com/roeehrl/hopsesh/sdk/agent"
	"github.com/roeehrl/hopsesh/sdk/agent/agenttest"
)

func seeded(t *testing.T, dir string, pr bool) fakecloud.Session {
	t.Helper()
	s := fakecloud.Session{Cloud: fakecloud.DevinCloud, Title: "Upgrade the router", Repo: "github.com/example/demo", Branch: "main"}
	if pr {
		s.Result, s.PR, s.State = "devin/1759500000-upgrade-the-router", 42, fakecloud.StateDone
	}
	s, err := fakecloud.Open(dir).Seed(s)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// Devin passes the cloud conformance kit against the stand-in devin.
func TestCloudConformance(t *testing.T) {
	dir := t.TempDir()
	agenttest.RunCloudWith(t, New(), fakecloud.Programs(dir, nil), agenttest.CloudOptions{
		Seed: func(string) agent.SessionID { return agent.SessionID(seeded(t, dir, true).ID) },
	})
}

func host(t *testing.T, dir, fail string) (agent.Host, agent.Install) {
	t.Helper()
	fh := agenttest.NewFakeHost("/home/u")
	fh.AddBinary("devin", "devin 2026.9.24")
	for k, p := range fakecloud.Programs(dir, map[string]string{"FAKE_CLOUD_FAIL": fail}) {
		fh.Programs[k] = p
	}
	m := New()
	in, _ := m.Detect(context.Background(), fh)
	return agent.Confine(fh, m.Spec(), in), in
}

func TestListFetchAndRefusals(t *testing.T) {
	dir := t.TempDir()
	done, open := seeded(t, dir, true), seeded(t, dir, false)
	ctx := context.Background()
	m := New()
	h, in := host(t, dir, "")
	l, err := m.ListCloud(ctx, h, in, agent.CloudQuery{})
	if err != nil || len(l.Sessions) != 2 || len(l.Errors) != 0 {
		t.Fatalf("ListCloud (the local session left out): %+v %v", l, err)
	}
	for _, cs := range l.Sessions {
		if cs.Key.Session == agent.SessionID(done.ID) && (cs.Branch != done.Result || cs.PR != "#42" || cs.State != agent.CloudDone || cs.Repo != "github.com/example/demo") {
			t.Errorf("finished session: %+v", cs)
		}
	}
	f, err := m.FetchCloud(ctx, h, in, agent.SessionID(done.ID), agent.FetchTarget{})
	if err != nil || f.Code.Way != agent.ViaPR || f.Code.Branch != done.Result || f.Segment != nil {
		t.Errorf("FetchCloud: %+v %v", f, err)
	}
	if _, err := m.FetchCloud(ctx, h, in, agent.SessionID(open.ID), agent.FetchTarget{}); !errors.Is(err, agent.ErrUnsupported) {
		t.Errorf("no branch yet: %v", err)
	}
	if _, err := m.FetchCloud(ctx, h, in, "devin-00000000000000000000000000000000", agent.FetchTarget{}); !errors.Is(err, agent.ErrNotFound) {
		t.Errorf("unknown: %v", err)
	}
	if ct, err := m.TestCloud(ctx, h, in, cloudName); err != nil || ct.Account == "" {
		t.Errorf("TestCloud: %+v %v", ct, err)
	}
	for fail, want := range map[string]error{"signed-out": agent.ErrSignedOut, "not-eligible": agent.ErrNotEligible} {
		h, in := host(t, dir, fail)
		if _, err := m.ListCloud(ctx, h, in, agent.CloudQuery{}); !errors.Is(err, want) {
			t.Errorf("%s: %v", fail, err)
		}
	}
}

// The list's fields are not documented: ids in either form, the session's pull request
// in the API's or other likely shapes, times as text or Unix seconds, local sessions left
// out.
func TestRecordShapes(t *testing.T) {
	for in, want := range map[string]struct {
		cloud             bool
		sid, repo, br, pr string
	}{
		`{"id":"devin-0123456789abcdef0123456789abcdef","status":"working"}`:                                                                                            {true, "devin-0123456789abcdef0123456789abcdef", "", "", ""},
		`{"session_id":"0123456789abcdef0123456789abcdef","location":"cloud","repos":["example/demo"],"branch":"devin/x"}`:                                              {true, "devin-0123456789abcdef0123456789abcdef", "example/demo", "devin/x", ""},
		`{"id":"a1","url":"https://app.devin.ai/sessions/0123456789abcdef","pull_requests":[{"pr_url":"https://github.com/example/demo/pull/7","head_ref":"devin/y"}]}`: {true, "a1", "example/demo", "devin/y", "#7"},
		`{"id":"0b6f8f1e-2c55-4a0e-9a59-3e4f1d2c7a10","location":"local"}`:                                                                                              {false, "", "", "", ""},
		`{"id":"0b6f8f1e-2c55-4a0e-9a59-3e4f1d2c7a10"}`:                                                                                                                 {false, "", "", "", ""},
	} {
		var r record
		if err := json.Unmarshal([]byte(in), &r); err != nil {
			t.Fatal(err)
		}
		if r.cloud() != want.cloud {
			t.Errorf("%s: cloud %v", in, r.cloud())
			continue
		}
		if !want.cloud {
			continue
		}
		br, pr := r.branch()
		if r.sid() != want.sid || r.repo() != want.repo || br != want.br || pr != want.pr {
			t.Errorf("%s: %q %q %q %q", in, r.sid(), r.repo(), br, pr)
		}
	}
	if got := stamp(json.RawMessage(`1759500000`)); got.Unix() != 1759500000 {
		t.Errorf("unix seconds: %v", got)
	}
	if got := stamp(json.RawMessage(`"2026-10-03T10:00:00Z"`)); got.Year() != 2026 {
		t.Errorf("RFC 3339: %v", got)
	}
	for in, want := range map[string]agent.CloudState{"working": agent.CloudRunning, "blocked": agent.CloudIdle, "finished": agent.CloudDone,
		"expired": agent.CloudFailed, "suspended": agent.CloudIdle, "": agent.CloudUnknown} {
		if got := state(in); got != want {
			t.Errorf("state(%q) = %s", in, got)
		}
	}
}
