package inventory

import (
	"testing"
	"time"

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
