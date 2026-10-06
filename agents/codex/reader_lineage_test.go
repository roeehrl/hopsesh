package codex

import (
	"context"
	"encoding/json"
	"github.com/roeehrl/hopsesh/sdk/agent"
	"github.com/roeehrl/hopsesh/sdk/ir"
	"testing"
	"time"
)

func TestStructuredNewTurnDoesNotRewriteLegacyProjection(t *testing.T) {
	rs := thread(0)
	fh, file := rolloutOf(t, rs)
	m := New()
	in := agent.Install{Agent: id, Roots: map[string]string{home: "/home/u/.codex"}, Present: true}
	h := agent.Confine(fh, m.Spec(), in)
	s := agent.Summary{Key: agent.SessionKey{Agent: id, Session: t1}, Path: file}
	before, err := m.Read(context.Background(), h, in, s, ir.Cursor{})
	if err != nil {
		t.Fatal(err)
	}
	rs = append(rs, rec{"timestamp": "2026-10-01T11:00:00Z", "type": "turn_context", "payload": rec{"model": "new"}}, event("01:00", rec{"type": "item_completed", "item": rec{"type": "CommandExecution", "id": "new-command", "command": []string{"echo", "new"}, "status": "completed", "exit_code": 0, "aggregated_output": "new"}}))
	var raw []byte
	for _, r := range rs {
		b, _ := json.Marshal(r)
		raw = append(raw, b...)
		raw = append(raw, '\n')
	}
	fh.Put(file, raw, time.Now())
	after, err := m.Read(context.Background(), h, in, s, ir.Cursor{})
	if err != nil {
		t.Fatal(err)
	}
	if len(after.Nodes) <= len(before.Nodes) {
		t.Fatal("structured new turn was not read")
	}
	for i, n := range before.Nodes {
		if n.Native.Anchor != after.Nodes[i].Native.Anchor || ir.ContentHash(n) != ir.ContentHash(after.Nodes[i]) {
			t.Fatal("structured new turn changed existing legacy records")
		}
	}
}
