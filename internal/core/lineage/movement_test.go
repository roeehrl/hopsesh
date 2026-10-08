package lineage

import (
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/roeehrl/hopsesh/sdk/agent"
	"github.com/roeehrl/hopsesh/sdk/ir"
)

// Fixtures use real branch/replica identities and validate before each assertion.
// Explicit parents let tests express concurrency without relying on append order.
type movementFixture struct {
	m   *Manifest
	ids map[string]ReplicaID
}

func newMovementFixture(names ...string) *movementFixture {
	f := &movementFixture{m: New("movement"), ids: map[string]ReplicaID{}}
	for _, name := range names {
		f.replica(name, f.m.Branch)
	}
	return f
}

func (f *movementFixture) replica(name, branch string) ReplicaID {
	id := f.m.Upsert(Replica{Line: branch, Endpoint: name, Location: name,
		Key: agent.SessionKey{Agent: "claude", Session: agent.SessionID(name)}})
	f.ids[name] = id
	return id
}

func (f *movementFixture) hop(t *testing.T, id, from, to string, notify bool, parents ...string) {
	t.Helper()
	h := Hop{ID: id, From: f.ids[from], To: f.ids[to], Kind: HopMove, Notify: notify,
		Time: time.Unix(int64(-len(f.m.Hops)), 0)}
	h.Fork = f.m.Replica(h.From).Line != f.m.Replica(h.To).Line
	if err := f.m.AppendHop(h); err != nil {
		t.Fatal(err)
	}
	// AppendHop defaults to all branch tips. An explicit empty set here means a
	// concurrent root operation, as when two independently read manifests merge.
	f.m.Hops[len(f.m.Hops)-1].Parents = slices.Clone(parents)
}

func (f *movementFixture) fork(t *testing.T, id, from, to string, notify bool, parents ...string) {
	t.Helper()
	view := f.m.ForBranch(f.m.Replica(f.ids[from]).Line)
	branch := view.Fork(id, nil)
	f.m.Branches = view.Branches
	f.replica(to, branch)
	f.hop(t, id, from, to, notify, parents...)
}

func (f *movementFixture) undo(t *testing.T, id string) {
	t.Helper()
	if err := f.m.UndoOperation(id); err != nil {
		t.Fatal(err)
	}
}

func (f *movementFixture) check(t *testing.T, current, departure string, returns ...string) {
	t.Helper()
	for _, reversed := range []bool{false, true} {
		m := f.m.Clone()
		if reversed {
			slices.Reverse(m.Hops)
			slices.Reverse(m.Replicas)
		}
		if err := m.Validate(); err != nil {
			t.Fatalf("invalid fixture: %v", err)
		}
		before := string(m.Encode())
		h, ok := m.Departure(f.ids[current])
		if ok != (departure != "") || h.ID != departure {
			t.Errorf("Departure(%s), reversed=%t: (%s, %t), want %q", current, reversed, h.ID, ok, departure)
		}
		got := m.ReturnReplicas(f.ids[current])
		want := make([]ReplicaID, len(returns))
		for i, name := range returns {
			want[i] = f.ids[name]
		}
		actual := make([]ReplicaID, len(got))
		for i, r := range got {
			actual[i] = r.ID
		}
		if !slices.Equal(actual, want) {
			t.Errorf("ReturnReplicas(%s), reversed=%t: %v, want %v (%v)", current, reversed, actual, want, returns)
		}
		if string(m.Encode()) != before {
			t.Fatal("movement resolution mutated the manifest")
		}
	}
}

func TestMovementEmptyAndUnknown(t *testing.T) {
	for _, m := range []*Manifest{nil, New("empty")} {
		if len(m.ActiveHops()) != 0 || len(m.ReturnReplicas("missing")) != 0 {
			t.Fatal("empty lineage has movement")
		}
		if h, ok := m.Departure("missing"); ok || h.ID != "" {
			t.Fatal("unknown replica has a departure")
		}
	}
}

func TestMovementActiveHopsAndUndoneAncestry(t *testing.T) {
	f := newMovementFixture("A", "B", "C", "D")
	f.hop(t, "z-first", "A", "B", true)
	f.hop(t, "y-backup", "B", "C", true, "z-first")
	f.m.Hops[1].Backup = true
	f.hop(t, "x-undone", "B", "C", true, "y-backup")
	f.hop(t, "a-last", "B", "D", true, "x-undone")
	f.undo(t, "x-undone")
	got := f.m.ActiveHops()
	if len(got) != 2 || got[0].ID != "z-first" || got[1].ID != "a-last" {
		t.Fatalf("active causal history: %+v", got)
	}
	f.check(t, "A", "a-last")
	f.check(t, "D", "", "B", "A")
	f.undo(t, "a-last")
	f.check(t, "A", "z-first")
	f.check(t, "D", "")
}

func TestMovementConcurrentTips(t *testing.T) {
	t.Run("departures", func(t *testing.T) {
		f := newMovementFixture("A", "B", "C")
		f.hop(t, "z-left", "A", "B", true)
		f.hop(t, "a-right", "A", "C", false)
		f.check(t, "A", "") // opt-out cannot make the other tip authoritative
		f.check(t, "B", "", "A")
		f.check(t, "C", "", "A")
	})
	t.Run("arrivals", func(t *testing.T) {
		f := newMovementFixture("A", "B", "C", "D")
		f.hop(t, "left", "A", "B", true)
		f.hop(t, "right", "A", "C", true)
		f.hop(t, "z-arrival", "B", "D", true, "left")
		f.hop(t, "a-arrival", "C", "D", true, "right")
		f.check(t, "D", "")
	})
	t.Run("forks", func(t *testing.T) {
		f := newMovementFixture("A")
		f.fork(t, "z-fork", "A", "B", true)
		f.fork(t, "a-fork", "A", "C", true)
		f.check(t, "A", "")
	})
	t.Run("move and fork", func(t *testing.T) {
		f := newMovementFixture("A", "B")
		f.hop(t, "move", "A", "B", true)
		f.fork(t, "fork", "A", "C", true)
		f.check(t, "A", "")
	})
}

func TestMovementForkKeepsOriginalAvailable(t *testing.T) {
	f := newMovementFixture("A", "B")
	f.hop(t, "inbound", "A", "B", true)
	// A new branch's first hop need not name the parent's inbound operation.
	f.fork(t, "fork", "B", "child", true)
	f.check(t, "B", "fork", "A")
	f.check(t, "child", "")
	if h, ok := f.m.Departure(f.ids["B"]); !ok || !h.Fork || h.From != f.ids["B"] {
		t.Fatal("source needs a separate-fork notice, not retirement of its branch")
	}
	f.undo(t, "fork")
	f.check(t, "B", "", "A")
}

func TestMovementSiblingAndParentExclusions(t *testing.T) {
	f := newMovementFixture("A", "B")
	f.hop(t, "parent-route", "A", "B", true)
	f.fork(t, "child", "B", "C", true)
	f.replica("D", f.m.Replica(f.ids["C"]).Line)
	f.hop(t, "child-route", "C", "D", true, "child")
	f.fork(t, "sibling", "B", "E", true)
	f.check(t, "D", "", "C")
	f.check(t, "C", "child-route")
	f.check(t, "E", "")
	f.check(t, "A", "parent-route")
	f.hop(t, "parent-return", "B", "A", true, "parent-route")
	f.check(t, "C", "child-route") // parent return cannot clear a child's departure
	f.check(t, "D", "", "C")
}

func TestMovementEveryRelevantDepartureMustNotify(t *testing.T) {
	for disabled := 0; disabled < 3; disabled++ {
		t.Run(fmt.Sprint(disabled), func(t *testing.T) {
			f := newMovementFixture("A", "B", "C", "D")
			f.hop(t, "first", "A", "B", disabled != 0)
			f.hop(t, "middle", "B", "C", disabled != 1, "first")
			f.hop(t, "last", "C", "D", disabled != 2, "middle")
			f.check(t, "A", "")
			f.check(t, "D", "", "C", "B", "A") // returns do not depend on notices
		})
	}
	t.Run("new visit resets opt out", func(t *testing.T) {
		f := newMovementFixture("A", "B", "C")
		f.hop(t, "old", "A", "B", false)
		f.hop(t, "back", "B", "A", false, "old")
		f.hop(t, "new", "A", "C", true, "back")
		f.check(t, "A", "new", "B")
	})
	t.Run("latest fork opted out", func(t *testing.T) {
		f := newMovementFixture("A")
		f.fork(t, "old", "A", "B", true)
		f.fork(t, "new", "A", "C", false, "old")
		f.check(t, "A", "")
	})
}

func TestMovementRepeatedRoutesAndReturnSuppression(t *testing.T) {
	for _, route := range []string{"ABABA", "ABCA", "ABCBCAB"} {
		t.Run(route, func(t *testing.T) {
			f := newMovementFixture("A", "B", "C")
			for i := 1; i < len(route); i++ {
				var parents []string
				if i > 1 {
					parents = []string{fmt.Sprint(i - 1)}
				}
				f.hop(t, fmt.Sprint(i), string(route[i-1]), string(route[i]), true, parents...)
				for _, current := range []byte{'A', 'B', 'C'} {
					arrival := -1
					visited := route[0] == current
					for j := 1; j <= i; j++ {
						if route[j] == current {
							arrival, visited = j, true
						}
					}
					var returns []string
					seen := map[byte]bool{current: true}
					for j := arrival - 1; j >= 0; j-- {
						if !seen[route[j]] {
							returns = append(returns, string(route[j]))
							seen[route[j]] = true
						}
					}
					departure := ""
					if visited && current != route[i] {
						departure = fmt.Sprint(i)
					}
					f.check(t, string(current), departure, returns...)
				}
			}
			f.undo(t, fmt.Sprint(len(route)-1))
			previous := string(route[len(route)-2])
			if h, ok := f.m.Departure(f.ids[previous]); ok {
				t.Fatalf("undo must restore return suppression: %+v", h)
			}
		})
	}
}

func TestMovementRolloverReturnCandidates(t *testing.T) {
	f := newMovementFixture("A", "B")
	old := observeText(t, f.m, f.ids["A"], "original work")
	f.hop(t, "out", "A", "B", true)
	// Same endpoint/profile/agent, different native session: the original is full.
	f.ids["fresh"] = f.m.Upsert(Replica{Endpoint: "A", Location: "A", Key: agent.SessionKey{Agent: "claude", Session: "fresh"}})
	f.hop(t, "rollover", "B", "fresh", true, "out")
	f.m.Hops[1].Rollover = &Rollover{Replica: f.ids["A"], Cursor: ir.Cursor{Head: old.Head, Offset: old.Offset}}
	f.check(t, "fresh", "", "B", "A")
	f.hop(t, "onward", "fresh", "B", true, "rollover")
	f.check(t, "B", "", "fresh", "A")
	t.Run("new original work remains a candidate", func(t *testing.T) {
		changed := &movementFixture{m: f.m.Clone(), ids: f.ids}
		observeText(t, changed.m, f.ids["A"], "original work", "independent work")
		changed.check(t, "B", "", "fresh", "A")
	})
	t.Run("ambiguous original work remains a candidate", func(t *testing.T) {
		left, right := f.m.Clone(), f.m.Clone()
		observeText(t, left, f.ids["A"], "original work", "left")
		observeText(t, right, f.ids["A"], "original work", "right")
		if err := left.Merge(right); err != nil {
			t.Fatal(err)
		}
		changed := &movementFixture{m: left, ids: f.ids}
		changed.check(t, "B", "", "fresh", "A")
	})
	// Undo removes the replacement visit, never the original's historical visit.
	f.undo(t, "onward")
	f.undo(t, "rollover")
	f.check(t, "B", "", "A")
}

func TestMovementReturnIdentityIsReplicaScoped(t *testing.T) {
	f := newMovementFixture("A", "B")
	original := f.m.Replica(f.ids["A"])
	other := original
	other.Key.Profile = "other-profile"
	f.ids["other"] = f.m.Upsert(other)
	f.hop(t, "out", "A", "B", true)
	f.hop(t, "other-profile", "B", "other", true, "out")
	// Same endpoint, agent and native ID does not make a different profile a
	// return to the original. Candidates must not collapse by machine name.
	f.check(t, "A", "other-profile")
	f.check(t, "other", "", "B", "A")
	f.hop(t, "actual-return", "other", "A", true, "other-profile")
	f.check(t, "A", "", "other", "B")
}
