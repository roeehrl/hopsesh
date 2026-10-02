package lineage

import (
	"testing"
	"time"

	"github.com/roeehrl/hopsesh/sdk/agent"
)

func TestMergeKeepsNewerAndRemapsHops(t *testing.T) {
	t0 := time.Date(2026, 10, 2, 10, 0, 0, 0, time.UTC)
	claude := agent.SessionKey{Agent: "claude", Session: "a"}
	codex := agent.SessionKey{Agent: "codex", Session: "b"}
	m := New("L")
	m.Upsert(Replica{Key: claude, Location: "studio", Head: "h1", Time: t0})
	o := New("L")
	j := o.Upsert(Replica{Key: codex, Location: "laptop", Head: "x1", Time: t0.Add(time.Minute)})
	i := o.Upsert(Replica{Key: claude, Location: "studio", Head: "h2", Time: t0.Add(2 * time.Minute)})
	o.Hops = append(o.Hops, Hop{Time: t0.Add(time.Minute), From: i, To: j, Kind: HopContinue})
	m.Merge(o)
	r, _, ok := m.Find(claude, "studio")
	if !ok || r.Head != "h2" {
		t.Fatalf("newer state must win: %+v", r)
	}
	if len(m.Hops) != 1 || m.Replicas[m.Hops[0].From].Key != claude || m.Replicas[m.Hops[0].To].Key != codex {
		t.Fatalf("hops: %+v", m.Hops)
	}
	m.Merge(o)
	if len(m.Hops) != 1 {
		t.Fatal("merging twice must not duplicate hops")
	}
	back, err := Parse(m.Encode())
	if err != nil || back.Logical != "L" || len(back.Replicas) != 2 {
		t.Fatalf("round trip: %+v %v", back, err)
	}
	if _, err := Parse([]byte(`{"hopsesh":"lineage/0"}`)); err == nil {
		t.Fatal("another format must be refused")
	}
}
