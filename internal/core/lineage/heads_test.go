package lineage

import (
	"fmt"
	"math/rand/v2"
	"slices"
	"testing"
	"time"

	"github.com/roeehrl/hopsesh/sdk/ir"
)

// headsByWalks is the original definition: drop every id another id descends from.
func headsByWalks(m *Manifest, ids []ir.NodeID) []ir.NodeID {
	ids = unique(ids)
	keep := map[ir.NodeID]bool{}
	for _, id := range ids {
		keep[id] = true
	}
	for _, id := range ids {
		for a := range m.Covered([]ir.NodeID{id}) {
			if a != id {
				delete(keep, a)
			}
		}
	}
	var out []ir.NodeID
	for id := range keep {
		out = append(out, id)
	}
	return unique(out)
}

func randomDAG(n int, r *rand.Rand) *Manifest {
	m := &Manifest{}
	for i := range n {
		rev := Revision{ID: ir.NodeID(fmt.Sprint(i))}
		for range r.IntN(3) {
			if i > 0 {
				rev.Parents = append(rev.Parents, ir.NodeID(fmt.Sprint(r.IntN(i))))
			}
		}
		m.Revisions = append(m.Revisions, rev)
	}
	return m
}

func TestHeadsMatchesPerIDWalks(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2))
	for range 200 {
		m := randomDAG(1+r.IntN(60), r)
		var ids []ir.NodeID
		for range r.IntN(20) {
			ids = append(ids, ir.NodeID(fmt.Sprint(r.IntN(len(m.Revisions)+5)))) // unknown ids too
		}
		if got, want := m.Heads(ids), headsByWalks(m, ids); !slices.Equal(got, want) {
			t.Fatalf("Heads(%v) = %v, want %v", ids, got, want)
		}
	}
}

// A long chain with every node a candidate (a native fork's inherited coverage) must
// not take one graph walk per node.
func TestHeadsIsLinear(t *testing.T) {
	m := &Manifest{}
	var ids []ir.NodeID
	for i := range 50000 {
		rev := Revision{ID: ir.NodeID(fmt.Sprint(i))}
		if i > 0 {
			rev.Parents = []ir.NodeID{ir.NodeID(fmt.Sprint(i - 1))}
		}
		m.Revisions = append(m.Revisions, rev)
		ids = append(ids, rev.ID)
	}
	start := time.Now()
	heads := m.Heads(ids)
	if len(heads) != 1 || heads[0] != "49999" {
		t.Fatalf("heads %v", heads)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("Heads over a 50,000-node chain took %v", d)
	}
}
