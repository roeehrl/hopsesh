package lineage

import (
	"testing"
	"time"

	"github.com/roeehrl/hopsesh/sdk/agent"
	"github.com/roeehrl/hopsesh/sdk/ir"
)

func TestMergeCausalReceipts(t *testing.T) {
	m := New("L")
	id := m.Upsert(Replica{Key: agent.SessionKey{Agent: "claude", Session: "a"}, Location: "studio"})
	seg := ir.Segment{Nodes: []ir.Node{{Kind: ir.KindMessage, Text: "one"}}, Cursor: ir.Cursor{Head: "h1", Offset: 10}}
	if _, err := m.Observe(id, &seg); err != nil {
		t.Fatal(err)
	}
	o := m.Clone()
	seg.Nodes = append(seg.Nodes, ir.Node{Kind: ir.KindMessage, Text: "two"})
	seg.Cursor = ir.Cursor{Head: "h2", Offset: 20}
	if _, err := o.Observe(id, &seg); err != nil {
		t.Fatal(err)
	}
	if err := m.Merge(o); err != nil {
		t.Fatal(err)
	}
	r, _, ok := m.Find(agent.SessionKey{Agent: "claude", Session: "a"}, "studio")
	if !ok || r.Head != "h2" {
		t.Fatalf("causal state: %+v", r)
	}
	before := len(m.States)
	if err := m.Merge(o); err != nil {
		t.Fatal(err)
	}
	if len(m.States) != before {
		t.Fatal("merge duplicated receipts")
	}
	if _, err := Parse(m.Encode()); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"lineage/1", "lineage/2"} {
		if _, err := Parse([]byte(`{"hopsesh":"` + f + `"}`)); err == nil {
			t.Fatal("old format accepted")
		}
	}
}

// A cloud copy is a replica without a file: its URL, branch and the code side of the
// handoff survive a round trip through the manifest.
func TestCloudReplica(t *testing.T) {
	t0 := time.Date(2026, 10, 4, 9, 0, 0, 0, time.UTC)
	m := New("L")
	from := m.Upsert(Replica{Key: agent.SessionKey{Agent: "claude", Session: "a"}, Location: "studio", Time: t0})
	to := m.Upsert(Replica{Key: agent.SessionKey{Agent: "codex", Session: "task_e_1"}, Location: "codex-cloud", Time: t0,
		URL: "https://chatgpt.com/codex/tasks/task_e_1", Branch: "hopsesh/handoff/20261004-aaaaaaaa"})
	m.AppendHop(Hop{Time: t0, From: from, To: to, Kind: HopHandoff, Fidelity: string(agent.FidBrief),
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
	if back.Format != Format {
		t.Fatalf("format %q", back.Format)
	}
}
