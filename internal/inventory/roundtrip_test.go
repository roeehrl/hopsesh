package inventory

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/roeehrl/hopsesh/internal/core/fsys"
	"github.com/roeehrl/hopsesh/internal/core/hops"
	"github.com/roeehrl/hopsesh/internal/core/sessions"
)

func TestNewestCopy(t *testing.T) {
	t0 := time.Date(2026, 10, 2, 10, 0, 0, 0, time.UTC)
	cases := []struct {
		name string
		cs   []Copy
		want int
	}{
		{"newest unmarked wins", []Copy{{Machine: "a", LastActive: t0}, {Machine: "b", LastActive: t0.Add(time.Hour)}}, 1},
		{"a marked copy loses to the copy it moved to", []Copy{{Machine: "a", LastActive: t0.Add(2 * time.Minute), MovedTo: "b"}, {Machine: "b", LastActive: t0}}, 1},
		{"a marked copy that kept being used wins", []Copy{{Machine: "a", LastActive: t0.Add(time.Hour), MovedTo: "b"}, {Machine: "b", LastActive: t0}}, 0},
		{"all marked: newest", []Copy{{Machine: "a", LastActive: t0, MovedTo: "b"}, {Machine: "b", LastActive: t0.Add(time.Minute), MovedTo: "a"}}, 1},
	}
	for _, c := range cases {
		if got := newest(c.cs); got != c.want {
			t.Errorf("%s: got %d, want %d", c.name, got, c.want)
		}
	}
}

func TestGroupByRepoMergesCopies(t *testing.T) {
	t0 := time.Date(2026, 10, 2, 10, 0, 0, 0, time.UTC)
	mk := func(id string, at time.Time, movedTo string) Session {
		return Session{Summary: sessions.Summary{ID: id, Title: "x", LastActivity: at, MovedTo: movedTo}}
	}
	here := &Machine{Name: "studio", Local: true, Sessions: []Session{mk("s1", t0, "laptop"), mk("s2", t0, "")}}
	laptop := &Machine{Name: "laptop", Sessions: []Session{mk("s1", t0.Add(time.Hour), "")}}
	groups := GroupByRepo([]*Machine{here, laptop}, nil)
	var n int
	for _, g := range groups {
		for _, e := range g.Entries {
			n++
			if e.Session.ID == "s1" {
				if e.Machine != "laptop" || len(e.Copies) != 2 {
					t.Fatalf("s1 should show the laptop copy with 2 copies: %+v", e)
				}
			}
		}
	}
	if n != 2 {
		t.Fatalf("want 2 entries (one per session), got %d", n)
	}
}

func TestPendingMarks(t *testing.T) {
	st := t.TempDir()
	dir := t.TempDir()
	hopAt := time.Date(2026, 10, 2, 10, 0, 0, 0, time.UTC)
	write := func(name, extra string) string {
		f := filepath.Join(dir, name+".jsonl")
		os.WriteFile(f, []byte(`{"type":"user","uuid":"u1","sessionId":"`+name+`","cwd":"/p","timestamp":"2026-10-02T09:00:00Z","message":{"role":"user","content":"hi"}}`+"\n"+extra), 0o600)
		return f
	}
	quiet := write("quiet", "")
	busy := write("busy", `{"type":"user","uuid":"u2","sessionId":"busy","timestamp":"2026-10-02T11:00:00Z","message":{"role":"user","content":"more"}}`+"\n")
	running := write("running", "")
	for _, id := range []string{"quiet", "busy", "running"} {
		hops.Append(st, hops.Hop{Time: hopAt, SessionID: id, From: "laptop", To: "studio", Mark: hops.MarkPending})
	}
	mk := func(id, f string, live bool) Session {
		s, _ := sessions.Summarize(fsys.Local{}, f)
		out := Session{Summary: *s}
		if live {
			out.Live = &sessions.LiveEntry{PID: 1, SessionID: id}
		}
		return out
	}
	m := &Machine{Name: "laptop", fs: fsys.Local{}, Sessions: []Session{mk("quiet", quiet, false), mk("busy", busy, false), mk("running", running, true)}}
	sc := &Scanner{StateDir: st}
	sc.applyPendingMarks(m)
	want := map[string]string{"quiet": hops.MarkDone, "busy": hops.MarkDiverged, "running": hops.MarkPending}
	for id, w := range want {
		if h, _ := hops.Last(st, id); h.Mark != w {
			t.Errorf("%s: mark %s, want %s", id, h.Mark, w)
		}
	}
	if s, _ := sessions.Summarize(fsys.Local{}, quiet); s.MovedTo != "studio" {
		t.Fatalf("quiet copy not marked: %+v", s)
	}
}
