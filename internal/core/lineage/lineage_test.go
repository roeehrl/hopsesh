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
	if _, err := Parse([]byte(`{"hopsesh":"lineage/1"}`)); err == nil {
		t.Fatal("another format must be refused")
	}
}

// A cloud copy is a replica without a file: its URL, branch and the code side of the
// handoff survive a round trip through the manifest.
func TestCloudReplica(t *testing.T) {
	t0 := time.Date(2026, 10, 4, 9, 0, 0, 0, time.UTC)
	m := New("L")
	from := m.Upsert(Replica{Key: agent.SessionKey{Agent: "claude", Session: "a"}, Location: "studio", Head: "h1", Time: t0})
	to := m.Upsert(Replica{Key: agent.SessionKey{Agent: "codex", Session: "task_e_1"}, Location: "codex-cloud", Time: t0,
		URL: "https://chatgpt.com/codex/tasks/task_e_1", Branch: "hopsesh/handoff/20261004-aaaaaaaa"})
	m.Hops = append(m.Hops, Hop{Time: t0, From: from, To: to, Kind: HopHandoff, Fidelity: string(agent.FidBrief),
		Code: &CodeHop{Way: agent.ViaBranch, Remote: "github.com/example/demo", Branch: "hopsesh/handoff/20261004-aaaaaaaa", Base: "4c1e9a2",
			Snapshot: "9f00d1e", Withheld: []string{".env"}, Redactions: 2}})
	back, err := Parse(m.Encode())
	if err != nil {
		t.Fatal(err)
	}
	r, i, ok := back.Find(agent.SessionKey{Agent: "codex", Session: "task_e_1"}, "codex-cloud")
	if !ok || r.URL == "" || r.Branch == "" || r.Head != "" {
		t.Fatalf("cloud replica: %+v", r)
	}
	h, ok := back.LastHopTo(i)
	if !ok || h.Kind != HopHandoff || h.Code == nil || h.Code.Way != agent.ViaBranch || h.Code.Redactions != 2 || h.Code.Withheld[0] != ".env" {
		t.Fatalf("handoff hop: %+v", h)
	}
	if back.Format != "lineage/2" {
		t.Fatalf("format %q", back.Format)
	}
}
