package lineage

import (
	"encoding/json"
	"errors"
	"math/rand"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/roeehrl/hopsesh/sdk/agent"
	"github.com/roeehrl/hopsesh/sdk/ir"
)

func observeText(t *testing.T, m *Manifest, id ReplicaID, texts ...string) State {
	t.Helper()
	var seg ir.Segment
	for i, text := range texts {
		seg.Nodes = append(seg.Nodes, ir.Node{Kind: ir.KindMessage, Actor: ir.User, Text: text, Native: &ir.Native{Anchor: string(rune('a' + i))}})
	}
	ir.Chain(seg.Nodes, "")
	seg.Cursor.Offset = int64(len(texts) * 10)
	seg.Cursor.Head = seg.Nodes[len(seg.Nodes)-1].ID
	st, err := m.Observe(id, &seg)
	if err != nil {
		t.Fatal(err)
	}
	return st
}
func TestCausalMergeOrderAndConcurrentWork(t *testing.T) {
	m := New("family")
	id := m.Upsert(Replica{Key: agent.SessionKey{Agent: "claude", Session: "original"}, Endpoint: "A", Location: "A"})
	observeText(t, m, id, "base")
	a, b := m.Clone(), m.Clone()
	observeText(t, a, id, "base", "A work")
	observeText(t, b, id, "base", "B work")
	ab, ba := a.Clone(), b.Clone()
	if err := ab.Merge(b); err != nil {
		t.Fatal(err)
	}
	if err := ba.Merge(a); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(ab, ba) {
		t.Fatal("causal merge depends on arrival order")
	}
	if _, ok := ab.LatestState(id); ok {
		t.Fatal("concurrent tips must not silently choose a winner")
	}
	before := string(ab.Encode())
	if err := ab.Merge(b); err != nil {
		t.Fatal(err)
	}
	if string(ab.Encode()) != before {
		t.Fatal("merge is not idempotent")
	}
	for n := 0; n < 30; n++ {
		shuffled := ab.Clone()
		r := rand.New(rand.NewSource(int64(n)))
		r.Shuffle(len(shuffled.Revisions), func(i, j int) {
			shuffled.Revisions[i], shuffled.Revisions[j] = shuffled.Revisions[j], shuffled.Revisions[i]
		})
		r.Shuffle(len(shuffled.States), func(i, j int) { shuffled.States[i], shuffled.States[j] = shuffled.States[j], shuffled.States[i] })
		if err := shuffled.Validate(); err != nil {
			t.Fatal(err)
		}
	}
}
func TestProjectionAcknowledgesOnlyDestination(t *testing.T) {
	m := New("F")
	a := m.Upsert(Replica{Key: agent.SessionKey{Agent: "claude", Session: "a"}, Location: "A"})
	b := m.Upsert(Replica{Key: agent.SessionKey{Agent: "codex", Session: "b"}, Location: "B"})
	c := m.Upsert(Replica{Key: agent.SessionKey{Agent: "claude", Session: "c"}, Location: "C"})
	st := observeText(t, m, a, "base")
	m.Deliver(b, ir.Cursor{}, nil, st.Heads, []string{"reasoning omitted"})
	m.Deliver(c, ir.Cursor{}, nil, st.Heads, nil)
	advanced := observeText(t, m, b, "new B work")
	m.Deliver(c, ir.Cursor{}, nil, advanced.Heads, nil)
	original, _ := m.LatestState(a)
	if Subset(m.Covered(advanced.Heads), m.Covered(original.Heads)) {
		t.Fatal("C's delivery must not acknowledge A")
	}
	if original.Head != st.Head {
		t.Fatal("destination delivery moved source native checkpoint")
	}
}
func TestObservationIsAtomicOnRewindAndEdit(t *testing.T) {
	m := New("F")
	id := m.Upsert(Replica{Key: agent.SessionKey{Agent: "claude", Session: "a"}, Location: "A"})
	observeText(t, m, id, "one", "two")
	before := string(m.Encode())
	for _, nodes := range [][]ir.Node{{{Kind: ir.KindMessage, Text: "edited", Native: &ir.Native{Anchor: "a"}}}, {{Kind: ir.KindMessage, Actor: ir.User, Text: "one", Native: &ir.Native{Anchor: "a"}}}} {
		seg := ir.Segment{Nodes: nodes}
		if _, err := m.Observe(id, &seg); !errors.Is(err, agent.ErrDiverged) {
			t.Fatalf("unverified edit accepted: %v", err)
		}
		if string(m.Encode()) != before {
			t.Fatal("failed observation mutated the causal graph")
		}
	}
}
func TestJourneyCausalOrderForkAndUndo(t *testing.T) {
	for _, route := range []string{"ABABA", "ABCA", "ABCBCAB"} {
		m := New("F")
		ids := map[byte]ReplicaID{}
		for _, c := range []byte{'A', 'B', 'C'} {
			ids[c] = m.Upsert(Replica{Key: agent.SessionKey{Agent: "claude", Session: agent.SessionID(string(c))}, Location: string(c)})
		}
		for i := 0; i < len(route)-1; i++ {
			m.AppendHop(Hop{ID: string(rune('a' + i)), From: ids[route[i]], To: ids[route[i+1]], Kind: HopMove, Time: time.Unix(int64(-i), 0)})
		}
		slices.Reverse(m.Hops)
		j := m.Journey()
		want := map[string]int{"ABABA": 2, "ABCA": 1, "ABCBCAB": 1}[route]
		if j.Transfers != len(route)-1 || j.RoundTrips != want {
			t.Fatalf("clock/order changed journey %s: %+v", route, j)
		}
		last := string(rune('a' + len(route) - 2))
		if err := m.UndoOperation(last); err != nil {
			t.Fatal(err)
		}
		if m.Journey().Transfers != len(route)-2 {
			t.Fatal("undo counted as active travel")
		}
		branch := m.Fork("fork", nil)
		fork := m.Upsert(Replica{Line: branch, Key: agent.SessionKey{Agent: "codex", Session: "fork"}, Location: "A"})
		m.AppendHop(Hop{ID: "fork", From: ids['B'], To: fork, Fork: true, Kind: HopContinue})
		fm := m.ForBranch(branch)
		if j := fm.Journey(); !j.Fork || j.Transfers != 0 || j.RoundTrips != 0 {
			t.Fatalf("fork must start its own route: %+v", j)
		}
	}
}
func TestMalformedGraphRefused(t *testing.T) {
	m := New("F")
	id := m.Upsert(Replica{Key: agent.SessionKey{Agent: "claude", Session: "a"}, Location: "A"})
	observeText(t, m, id, "one")
	for _, mutate := range []func(*Manifest){func(m *Manifest) { m.Replicas = append(m.Replicas, m.Replicas[0]) }, func(m *Manifest) { m.Revisions[0].Parents = []ir.NodeID{m.Revisions[0].ID} }, func(m *Manifest) { m.States[0].Projection[0].Coverage = []ir.NodeID{"missing"} }, func(m *Manifest) { m.Branches[0].Parent = m.Branch }, func(m *Manifest) { m.Format = "lineage/2" }} {
		bad := m.Clone()
		mutate(bad)
		body, _ := json.Marshal(bad)
		if _, err := Parse(body); err == nil {
			t.Fatal("malformed graph accepted")
		}
	}
}

func TestNativeForkProjectionKeepsInheritedRevisionIdentity(t *testing.T) {
	m := New("F")
	parent := m.Upsert(Replica{Endpoint: "A", Location: "A", Key: agent.SessionKey{Agent: "codex", Session: "parent"}})
	st := observeText(t, m, parent, "base", "shared work")
	seg := ir.Segment{Nodes: []ir.Node{{Kind: ir.KindMessage, Actor: ir.User, Text: "base", Native: &ir.Native{Anchor: "child-a"}}, {Kind: ir.KindMessage, Actor: ir.User, Text: "shared work", Native: &ir.Native{Anchor: "child-b"}}, {Kind: ir.KindMessage, Actor: ir.User, Text: "fork work", Native: &ir.Native{Anchor: "child-c"}}}}
	ir.Chain(seg.Nodes, "")
	seg.Cursor.Head = seg.Nodes[len(seg.Nodes)-1].ID
	proof := []agent.NativeInheritance{{ParentAnchor: "a", ChildAnchor: "child-a", Hash: st.Projection[0].Hash}, {ParentAnchor: "b", ChildAnchor: "child-b", Hash: st.Projection[1].Hash}}
	child := Replica{Endpoint: "A", Location: "A", Key: agent.SessionKey{Agent: "codex", Session: "child"}}
	out, err := m.AdoptNativeFork(parent, child, proof, &seg)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Revisions) != len(m.Revisions)+1 {
		t.Fatal("inherited records counted as new authorship")
	}
	if out.Branch == m.Branch || out.Family != m.Family || !out.Journey().Fork {
		t.Fatal("native fork lineage missing")
	}
	if len(m.Revisions) != 2 {
		t.Fatal("parent changed")
	}
	reversed := m.Clone()
	slices.Reverse(reversed.States)
	again, err := reversed.AdoptNativeFork(parent, child, proof, &seg)
	if err != nil {
		t.Fatal(err)
	}
	if err = out.Merge(again); err != nil {
		t.Fatal("native fork discovery depends on serialization order", err)
	}
	bad := proof
	bad[0].Hash = "unverified"
	if _, err = m.AdoptNativeFork(parent, child, bad, &seg); err == nil {
		t.Fatal("unverified inheritance accepted")
	}
}
func TestStrictLineageRejectsTrailingJSON(t *testing.T) {
	if _, err := Parse(append(New("F").Encode(), []byte(" {}")...)); err == nil {
		t.Fatal("trailing object accepted")
	}
}
