package claude

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/roeehrl/hopsesh/sdk/agent"
	"github.com/roeehrl/hopsesh/sdk/agent/agenttest"
	"github.com/roeehrl/hopsesh/sdk/ir"
)

func branchRecord(typ, uuid, parent string) map[string]any {
	return map[string]any{"type": typ, "uuid": uuid, "parentUuid": parent, "sessionId": s1,
		"cwd": "/home/u/git/demo", "version": "2.1.284"}
}

func branchMessage(typ, uuid, parent, text string) map[string]any {
	r := branchRecord(typ, uuid, parent)
	r["message"] = map[string]any{"role": typ, "content": text}
	return r
}

func branchCheckpoint(leaf string) map[string]any {
	return map[string]any{"type": "last-prompt", "leafUuid": leaf, "sessionId": s1}
}

func branchJSON(t *testing.T, records ...map[string]any) []byte {
	t.Helper()
	var b bytes.Buffer
	for _, r := range records {
		if err := json.NewEncoder(&b).Encode(r); err != nil {
			t.Fatal(err)
		}
	}
	return b.Bytes()
}

func TestActiveBranchCheckpointOrderAndAncestry(t *testing.T) {
	u := branchMessage("user", "u", "", "question")
	old := branchMessage("assistant", "old", "u", "old answer")
	next := branchMessage("assistant", "next", "u", "next answer")
	// These deliberately disagree with file order. Clock skew and preserved turn
	// timestamps cannot decide whether a response extends a checkpoint.
	u["timestamp"] = "2030-01-01T00:00:00Z"
	next["timestamp"] = "2000-01-01T00:00:00Z"
	side := branchMessage("assistant", "side", "u", "side answer")
	side["isSidechain"] = true
	compact := branchRecord("system", "boundary", "")
	compact["parentUuid"] = nil
	compact["logicalParentUuid"] = "u"
	summary := branchMessage("user", "summary", "boundary", "compact summary")
	summary["isCompactSummary"] = true
	explicitRoot := branchRecord("system", "root", "")
	explicitRoot["logicalParentUuid"] = "u"

	for _, tc := range []struct {
		name    string
		records []map[string]any
		want    []string
		fail    bool
	}{
		{"stale checkpoint", []map[string]any{u, branchCheckpoint("u"), next}, []string{"u", "next"}, false},
		{"explicit rewind", []map[string]any{u, old, branchCheckpoint("u")}, []string{"u"}, false},
		{"rewind then continuation", []map[string]any{u, old, branchCheckpoint("u"), next}, []string{"u", "next"}, false},
		{"unrelated newer sibling", []map[string]any{u, old, branchCheckpoint("old"), next}, []string{"u", "old"}, false},
		{"ambiguous siblings", []map[string]any{u, branchCheckpoint("u"), old, next}, nil, true},
		{"later checkpoint resolves siblings", []map[string]any{u, branchCheckpoint("u"), old, next, branchCheckpoint("old")}, []string{"u", "old"}, false},
		{"sidechain sibling", []map[string]any{u, branchCheckpoint("u"), side, next}, []string{"u", "next"}, false},
		{"descendant through sidechain", []map[string]any{u, branchCheckpoint("u"), side, branchMessage("assistant", "child", "side", "child")}, []string{"u"}, false},
		{"compaction logical parent", []map[string]any{u, branchCheckpoint("u"), compact, summary}, []string{"u", "boundary", "summary"}, false},
		{"explicit empty parent wins", []map[string]any{u, branchCheckpoint("u"), explicitRoot}, []string{"u"}, false},
		{"missing checkpoint leaf", []map[string]any{u, branchCheckpoint("missing")}, nil, true},
		{"sidechain checkpoint leaf", []map[string]any{u, side, branchCheckpoint("side")}, nil, true},
		{"forward checkpoint leaf", []map[string]any{branchCheckpoint("u"), u}, nil, true},
		{"cyclic ancestry", []map[string]any{branchMessage("user", "u", "old", "question"), old}, nil, true},
		{"repeated continuation anchor", []map[string]any{u, branchCheckpoint("u"), next, next}, nil, true},
		{"no checkpoint", []map[string]any{u, old, side}, []string{"u", "old"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			recs, _, err := readRecords(bytes.NewReader(branchJSON(t, tc.records...)))
			if err != nil {
				t.Fatal(err)
			}
			indexes, err := activeBranch(recs)
			if tc.fail {
				if !errors.Is(err, agent.ErrDiverged) {
					t.Fatalf("unverified branch accepted: %v %v", indexes, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			var got []string
			for _, i := range indexes {
				got = append(got, recs[i].UUID)
			}
			if !slices.Equal(got, tc.want) {
				t.Fatalf("branch %v, want %v", got, tc.want)
			}
		})
	}
}

// Mirrors the ordering that exposed the legacy bug: the checkpoint is written
// after the prompt's attachment, before the assistant/tool response. Everything
// in this response is already on disk when a future handoff reads it.
func staleBranchFixture(t *testing.T) (*agenttest.FakeHost, agent.Host, agent.Install, agent.Summary, []byte) {
	t.Helper()
	fh, h, in, ss := setup(t)
	s := ss[s1]
	u := branchMessage("user", "prompt", "", "check protections")
	u["timestamp"] = "2030-01-01T00:00:00Z"
	call := branchRecord("assistant", "call", "attachment")
	call["timestamp"] = "2000-01-01T00:00:00Z"
	call["message"] = map[string]any{"role": "assistant", "model": "claude-sonnet-5", "content": []any{
		map[string]any{"type": "thinking", "thinking": "check the rules", "signature": "synthetic-test-signature"},
		map[string]any{"type": "tool_use", "id": "tool-test", "name": "Bash", "input": map[string]any{"command": "echo protected"}},
	}}
	result := branchRecord("user", "result", "call")
	result["message"] = map[string]any{"role": "user", "content": []any{
		map[string]any{"type": "tool_result", "tool_use_id": "tool-test", "content": "protected"},
	}}
	reply := branchMessage("assistant", "reply", "result", "protections checked")
	reply["message"].(map[string]any)["model"] = "claude-sonnet-5"
	reply["message"].(map[string]any)["usage"] = map[string]int{"input_tokens": 10, "cache_read_input_tokens": 2000, "output_tokens": 5}
	body := branchJSON(t, u, branchRecord("attachment", "attachment", "prompt"), branchCheckpoint("attachment"),
		call, result, reply, branchRecord("system", "stop", "reply"))
	fh.Put(s.Path, body, time.Now())
	return fh, h, in, s, body
}

func endBranchMetadata(t *testing.T) []byte {
	t.Helper()
	exit := branchRecord("system", "exit", "stop")
	exit["subtype"], exit["content"] = "local_command", "/exit"
	out := branchRecord("system", "exit-output", "exit")
	out["subtype"], out["content"] = "local_command", "<local-command-stdout>/exit isn't available in this environment.</local-command-stdout>"
	out["commandOutcome"] = map[string]any{"kind": "unavailable_headless"}
	return branchJSON(t, map[string]any{"type": "queue-operation"}, exit, out,
		map[string]any{"type": "bridge-session"}, branchCheckpoint("exit-output"), map[string]any{"type": "cost-state"})
}

func TestFutureHandoffStaleCheckpointAndEndMetadata(t *testing.T) {
	fh, h, in, s, body := staleBranchFixture(t)
	m, ctx := New(), context.Background()
	before, err := m.Read(ctx, h, in, s, ir.Cursor{})
	if err != nil {
		t.Fatal(err)
	}
	if len(before.Nodes) != 5 || before.Nodes[4].Text != "protections checked" {
		t.Fatalf("handoff would omit the completed response: %v", before.Nodes)
	}
	capacity, err := m.ContextCapacity(ctx, h, in, &s)
	if err != nil || capacity.Existing != 2015 {
		t.Fatalf("capacity omitted the same response: %+v %v", capacity, err)
	}
	// Model a future portable handoff's module receipt. The final response is
	// available before shutdown; only explicitly supplied coverage is recorded.
	items := []ir.Item{{Node: "handoff/prompt", Role: ir.RoleUser, Text: "check protections", Coverage: []ir.NodeID{before.Nodes[0].ID}},
		{Node: "handoff/reply", Role: ir.RoleAgent, Text: "protections checked", Coverage: []ir.NodeID{before.Nodes[4].ID}}}
	handoff, err := m.Write(ctx, h, in, ir.WriteRequest{OperationID: "future-handoff", Mode: ir.WriteNew,
		SessionID: "future-handoff", Header: ir.Header{CWD: s.CWD}, Items: items})
	if err != nil || len(handoff.Projection) != 2 {
		t.Fatalf("handoff receipt: %+v %v", handoff, err)
	}
	for i, p := range handoff.Projection {
		if !slices.Equal(p.Coverage, items[i].Coverage) || p.Generated {
			t.Fatalf("handoff invented coverage: %+v", p)
		}
	}
	fh.Put(s.Path, append(body, endBranchMetadata(t)...), time.Now())
	after, err := m.Read(ctx, h, in, s, ir.Cursor{})
	if err != nil || !reflect.DeepEqual(before.Nodes, after.Nodes) || after.Cursor.Head != before.Cursor.Head || after.Cursor.Offset <= before.Cursor.Offset {
		t.Fatalf("end metadata changed conversation evidence: %v %v", after.Cursor, err)
	}
	delta, err := m.Read(ctx, h, in, s, before.Cursor)
	if err != nil || len(delta.Nodes) != 0 {
		t.Fatalf("end metadata became new work: %v %v", delta.Nodes, err)
	}
	afterCapacity, err := m.ContextCapacity(ctx, h, in, &s)
	if err != nil || afterCapacity != capacity {
		t.Fatalf("end metadata changed capacity: %+v %v", afterCapacity, err)
	}
	if err := m.Mark(ctx, h, in, s, agent.Mark{Kind: agent.MarkPrepared, AgentName: "Codex"}); err != nil {
		t.Fatal(err)
	}
	marked, err := m.Read(ctx, h, in, s, ir.Cursor{})
	if err != nil || !reflect.DeepEqual(after.Nodes, marked.Nodes) || marked.Cursor.Offset <= after.Cursor.Offset {
		t.Fatalf("title marker changed conversation evidence: %v %v", marked.Cursor, err)
	}
	// A plan made before the metadata/mark is still stale for a native append.
	if _, err := m.Write(ctx, h, in, ir.WriteRequest{Mode: ir.WriteAppend, SessionID: s1, Expect: before.Cursor}); !errors.Is(err, agent.ErrDiverged) {
		t.Fatalf("stale physical cursor accepted: %v", err)
	}
	// Actual work after the end metadata must remain visible and unacknowledged.
	markedBody, _ := fh.Get(s.Path)
	fh.Put(s.Path, append(markedBody, branchJSON(t, branchMessage("user", "independent", "exit-output", "independent work"))...), time.Now())
	independent, err := m.Read(ctx, h, in, s, marked.Cursor)
	if err != nil || len(independent.Nodes) != 1 || independent.Nodes[0].Text != "independent work" || independent.Nodes[0].Generated || len(independent.Nodes[0].Coverage) != 0 {
		t.Fatalf("independent work hidden or acknowledged: %v %v", independent.Nodes, err)
	}
}

func TestAppendAndRecoveryUseCompletedCheckpointBranch(t *testing.T) {
	for _, ended := range []bool{false, true} {
		t.Run(map[bool]string{false: "stale checkpoint", true: "end metadata"}[ended], func(t *testing.T) {
			fh, h, in, s, body := staleBranchFixture(t)
			if ended {
				fh.Put(s.Path, append(body, endBranchMetadata(t)...), time.Now())
			}
			m, ctx := New(), context.Background()
			before, err := m.Read(ctx, h, in, s, ir.Cursor{})
			if err != nil {
				t.Fatal(err)
			}
			original, _ := fh.Get(s.Path)
			req := ir.WriteRequest{OperationID: "return", Mode: ir.WriteAppend, SessionID: s1, Expect: before.Cursor,
				Header: ir.Header{CWD: s.CWD}, Items: []ir.Item{{Node: "return/prompt", Role: ir.RoleUser, Text: "returned work", Coverage: []ir.NodeID{"returned-revision"}}}}
			w, err := m.Write(ctx, h, in, req)
			if err != nil {
				t.Fatal(err)
			}
			written, _ := fh.Get(s.Path)
			if !bytes.HasPrefix(written, original) {
				t.Fatal("append changed original records")
			}
			recs, _, err := readRecords(bytes.NewReader(written[len(original):]))
			if err != nil {
				t.Fatal(err)
			}
			parent := "stop"
			if ended {
				parent = "exit-output"
			}
			if recs[0].parentUUID() != parent {
				t.Fatalf("append used stale native parent %q, want %q", recs[0].parentUUID(), parent)
			}
			delta, err := m.Read(ctx, h, in, s, before.Cursor)
			if err != nil || len(delta.Nodes) != 1 || delta.Nodes[0].Text != "returned work" || delta.Nodes[0].Parent != before.Cursor.Head {
				t.Fatalf("append lost original response: %v %v", delta.Nodes, err)
			}
			recovered, err := m.RecoverWrite(ctx, h, in, req)
			if err != nil || recovered.Cursor != w.Cursor || !reflect.DeepEqual(recovered.Projection, w.Projection) {
				t.Fatalf("recovery disagrees with append: %+v %v", recovered, err)
			}
		})
	}
}

func TestLegacyOmittedResponseDoesNotBecomeAcknowledged(t *testing.T) {
	fh, h, in, s, body := staleBranchFixture(t)
	m, ctx := New(), context.Background()
	// Simulate the old reader's evidence at the stale attachment checkpoint,
	// while retaining the actual physical offset containing the completed work.
	recs, _, err := readRecords(bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	prompt := nodes(recs[0])
	ir.Chain(prompt, "")
	legacy := ir.Cursor{Head: prompt[0].ID, Offset: int64(len(body))}
	delta, err := m.Read(ctx, h, in, s, legacy)
	if err != nil || len(delta.Nodes) != 4 {
		t.Fatalf("legacy omitted work disappeared: %v %v", delta.Nodes, err)
	}
	for _, n := range delta.Nodes {
		if n.Generated || len(n.Coverage) != 0 {
			t.Fatal("reader fabricated acknowledgement of omitted work")
		}
	}
	// Matching bytes alone cannot authorize appending to the legacy logical head.
	if _, err := m.Write(ctx, h, in, ir.WriteRequest{Mode: ir.WriteAppend, SessionID: s1, Expect: legacy}); !errors.Is(err, agent.ErrDiverged) {
		t.Fatalf("legacy head accepted despite unacknowledged response: %v", err)
	}
	fh.Put(s.Path, append(body, endBranchMetadata(t)...), time.Now())
	after, err := m.Read(ctx, h, in, s, legacy)
	if err != nil || !reflect.DeepEqual(delta.Nodes, after.Nodes) {
		t.Fatalf("end metadata changed legacy evidence: %v %v", after.Nodes, err)
	}
	// Even a completed operation cannot authorize a legacy request whose old
	// byte range already contains the omitted response. All projection/count
	// checks would pass; the corrected prefix-head check must reject it.
	req := ir.WriteRequest{OperationID: "legacy", Mode: ir.WriteAppend, SessionID: s1, Expect: legacy,
		Header: ir.Header{CWD: s.CWD}, Items: []ir.Item{{Node: "legacy/return", Role: ir.RoleUser, Text: "return"}}}
	current, err := m.Read(ctx, h, in, s, ir.Cursor{})
	if err != nil {
		t.Fatal(err)
	}
	valid := req
	valid.Expect = current.Cursor
	if _, err := m.Write(ctx, h, in, valid); err != nil {
		t.Fatal(err)
	}
	if _, err := m.RecoverWrite(ctx, h, in, req); !errors.Is(err, agent.ErrDiverged) {
		t.Fatalf("legacy recovery fabricated success: %v", err)
	}
}

func TestAmbiguousCheckpointRejectedByModuleConsumers(t *testing.T) {
	fh, h, in, ss := setup(t)
	s := ss[s1]
	body := branchJSON(t, branchMessage("user", "u", "", "question"), branchCheckpoint("u"),
		branchMessage("assistant", "a", "u", "answer A"), branchMessage("assistant", "b", "u", "answer B"))
	fh.Put(s.Path, body, time.Now())
	m, ctx := New(), context.Background()
	if _, err := m.Read(ctx, h, in, s, ir.Cursor{}); !errors.Is(err, agent.ErrDiverged) {
		t.Fatalf("reader chose an ambiguous branch: %v", err)
	}
	if _, err := m.ContextCapacity(ctx, h, in, &s); !errors.Is(err, agent.ErrDiverged) {
		t.Fatalf("capacity chose an ambiguous branch: %v", err)
	}
	req := ir.WriteRequest{Mode: ir.WriteAppend, SessionID: s1, Expect: ir.Cursor{Offset: int64(len(body))}, Header: ir.Header{CWD: s.CWD}}
	if _, err := m.Write(ctx, h, in, req); !errors.Is(err, agent.ErrDiverged) {
		t.Fatalf("writer chose an ambiguous branch: %v", err)
	}
	if _, err := m.RecoverWrite(ctx, h, in, req); !errors.Is(err, agent.ErrDiverged) {
		t.Fatalf("recovery chose an ambiguous branch: %v", err)
	}
	after, _ := fh.Get(s.Path)
	if !bytes.Equal(body, after) {
		t.Fatal("failed module operation changed the transcript")
	}
}
