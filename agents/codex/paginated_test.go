package codex

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/roeehrl/hopsesh/sdk/agent"
	"github.com/roeehrl/hopsesh/sdk/ir"
)

func TestPaginatedAppendPreservesPrefixAndProjection(t *testing.T) {
	ctx := context.Background()
	m := New()
	h, in, sessions := setup(t)
	s := sessions[t1]
	original, err := h.FS().ReadFile(s.Path, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	for turn := 0; turn < 2; turn++ {
		seg, err := m.Read(ctx, h, in, s, ir.Cursor{})
		if err != nil {
			t.Fatal(err)
		}
		res, err := m.Write(ctx, h, in, ir.WriteRequest{Mode: ir.WriteAppend, SessionID: t1, Expect: seg.Cursor, Header: ir.Header{Title: "Returned conversation"}, Items: []ir.Item{{Node: ir.NodeID("returned-user"), Role: ir.RoleUser, Text: "RETURN-DELTA"}, {Node: ir.NodeID("returned-agent"), Role: ir.RoleAgent, Text: "RETURN-REPLY"}}})
		if err != nil {
			t.Fatal(err)
		}
		after, err := h.FS().ReadFile(s.Path, 1<<20)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.HasPrefix(after, original) {
			t.Fatal("native signed prefix rewritten")
		}
		records, end, err := readLines(bytes.NewReader(after))
		if err != nil || end != int64(len(after)) {
			t.Fatalf("records: %d %v", end, err)
		}
		mt, err := firstMeta(after)
		if err != nil {
			t.Fatal(err)
		}
		next, err := paginatedNext(mt, records)
		if err != nil || next == nil || *next != uint64(len(records)) {
			t.Fatalf("next %v %v", next, err)
		}
		if len(res.Projection) != 2 {
			t.Fatalf("lost provenance: %+v", res.Projection)
		}
		got, err := m.Read(ctx, h, in, s, seg.Cursor)
		if err != nil || len(got.Nodes) != 2 || got.Nodes[0].Text != "RETURN-DELTA" || got.Nodes[1].Text != "RETURN-REPLY" {
			t.Fatalf("delta %+v %v", got, err)
		}
		original = after
	}
}

func TestPaginatedRejectsIncompleteAncestryBeforeWriting(t *testing.T) {
	for _, kind := range []string{"missing", "gap", "duplicate", "reference", "incomplete-subagent", "malformed", "partial"} {
		t.Run(kind, func(t *testing.T) {
			ctx := context.Background()
			m := New()
			fh := newHost(t)
			in, err := m.Detect(ctx, fh)
			if err != nil {
				t.Fatal(err)
			}
			h := agent.Confine(fh, m.Spec(), in)
			ls, err := m.List(ctx, h, in)
			if err != nil {
				t.Fatal(err)
			}
			var s agent.Summary
			for _, v := range ls.Sessions {
				if string(v.Key.Session) == t1 {
					s = v
				}
			}
			before, err := fh.FS().ReadFile(s.Path, 1<<20)
			if err != nil {
				t.Fatal(err)
			}
			lines := strings.Split(strings.TrimSuffix(string(before), "\n"), "\n")
			index := len(lines) - 1
			if kind == "reference" || kind == "incomplete-subagent" {
				index = 0
			}
			var row map[string]any
			if err := json.Unmarshal([]byte(lines[index]), &row); err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "missing":
				delete(row, "ordinal")
			case "gap":
				row["ordinal"] = 999
			case "duplicate":
				row["ordinal"] = 0
			case "reference":
				row["payload"].(map[string]any)["history_base"] = map[string]any{"thread_id": t3, "end_ordinal_exclusive": 500, "end_byte_offset": 123}
			case "incomplete-subagent":
				row["payload"].(map[string]any)["subagent_history_start_ordinal"] = 999
			}
			raw, err := json.Marshal(row)
			if err != nil {
				t.Fatal(err)
			}
			lines[index] = string(raw)
			before = []byte(strings.Join(lines, "\n") + "\n")
			if kind == "malformed" {
				before = append(before, []byte("{invalid}\n")...)
			}
			if kind == "partial" {
				before = append(before, []byte("{partial")...)
			}
			fh.Put(s.Path, before, time.Now())
			if kind != "partial" {
				if _, err := m.Read(ctx, h, in, s, ir.Cursor{}); err == nil {
					t.Fatal("claimed complete history")
				}
			}
			_, err = m.Write(ctx, h, in, ir.WriteRequest{Mode: ir.WriteAppend, SessionID: t1, Expect: ir.Cursor{Offset: int64(len(before))}, Items: []ir.Item{{Role: ir.RoleUser, Text: "must not write"}}})
			if !errors.Is(err, agent.ErrDiverged) && !errors.Is(err, agent.ErrUnsupported) {
				t.Fatalf("want safe refusal, got %v", err)
			}
			after, _ := fh.FS().ReadFile(s.Path, 1<<20)
			if !bytes.Equal(before, after) {
				t.Fatal("refusal modified original")
			}
		})
	}
}

// fork writes a by-reference fork of the rollout at parentPath, as Codex 0.166 makes one:
// its session_meta names the parent's first k records by ordinal and byte offset, and its
// own records are numbered on from k. It returns the fork's path and file length.
func fork(t *testing.T, h agent.Host, parentPath, parentID, id string, k int, texts ...string) (string, int) {
	t.Helper()
	raw, err := h.FS().ReadFile(parentPath, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.SplitAfter(string(raw), "\n")
	offset := 0
	for _, l := range lines[:k] {
		offset += len(l)
	}
	var first map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &first); err != nil {
		t.Fatal(err)
	}
	// A parent that is a fork itself has its own session_meta first, numbered on from
	// its inherited prefix: the fork's first record is at that ordinal plus k.
	start := int(first["ordinal"].(float64))
	p := first["payload"].(map[string]any)
	p["id"], p["forked_from_id"], p["forked_from_ordinal_exclusive"] = id, parentID, start+k
	p["history_base"] = map[string]any{"thread_id": parentID, "end_ordinal_exclusive": start + k, "end_byte_offset": offset}
	first["ordinal"] = start + k
	rec := func(r map[string]any) string {
		b, err := json.Marshal(r)
		if err != nil {
			t.Fatal(err)
		}
		return string(b) + "\n"
	}
	out := rec(first)
	n := start + k + 1
	out += rec(map[string]any{"timestamp": "2026-10-02T10:00:00Z", "ordinal": n, "type": "turn_context", "payload": map[string]any{"model": "gpt-5.5"}})
	for i, text := range texts {
		role, kind := "user", "input_text"
		if i%2 == 1 {
			role, kind = "assistant", "output_text"
		}
		n++
		out += rec(map[string]any{"timestamp": "2026-10-02T10:00:01Z", "ordinal": n, "type": "response_item",
			"payload": map[string]any{"type": "message", "role": role, "content": []map[string]any{{"type": kind, "text": text}}}})
	}
	path := "/home/u/.codex/sessions/2026/10/02/rollout-2026-10-02T10-00-00-" + id + ".jsonl"
	if err := h.FS().WriteFile(path, []byte(out), 0o600); err != nil {
		t.Fatal(err)
	}
	return path, len(out)
}

// A by-reference fork reads as its parent's history up to the fork, then its own: also a
// fork of a fork. Its parent goes on after the fork without that reaching the fork.
func TestPaginatedForkReadsInheritedHistory(t *testing.T) {
	const f1, f2 = "01a0fe1c-0000-7000-8000-0000000000f1", "01a0fe1c-0000-7000-8000-0000000000f2"
	ctx := context.Background()
	m := New()
	h, in, byID := setup(t)
	parent := byID[t1]
	ps, err := m.Read(ctx, h, in, parent, ir.Cursor{})
	if err != nil {
		t.Fatal(err)
	}
	// The parent's first 13 records are inherited; its last two (token count, task
	// complete) were written after the fork.
	p1, _ := fork(t, h, parent.Path, t1, f1, 13, "FORK-Q", "FORK-A")
	s1 := agent.Summary{Key: agent.SessionKey{Agent: id, Session: f1}, Path: p1}
	seg, err := m.Read(ctx, h, in, s1, ir.Cursor{})
	if err != nil {
		t.Fatal(err)
	}
	if seg.Header.SessionID != f1 || len(seg.Nodes) != len(ps.Nodes)+2 || seg.Nodes[len(seg.Nodes)-2].Text != "FORK-Q" || seg.Nodes[len(seg.Nodes)-1].Text != "FORK-A" {
		t.Fatalf("fork: header %q, %d nodes (parent %d): %+v", seg.Header.SessionID, len(seg.Nodes), len(ps.Nodes), seg.Nodes)
	}
	for i, n := range ps.Nodes {
		if ir.ContentHash(n) != ir.ContentHash(seg.Nodes[i]) || n.Native.Anchor != seg.Nodes[i].Native.Anchor {
			t.Fatalf("inherited node %d differs: %+v vs %+v", i, n, seg.Nodes[i])
		}
	}
	got, err := m.VerifyNativeFork(ctx, h, in, parent, s1)
	if err != nil || len(got) != len(ps.Nodes) {
		t.Fatalf("verify fork: %d inherited, want %d: %v", len(got), len(ps.Nodes), err)
	}
	if _, err := m.ContextCapacity(ctx, h, in, &s1); err != nil {
		t.Fatalf("capacity of a fork: %v", err)
	}

	// A fork of the fork, at its end: all of both.
	p2, _ := fork(t, h, p1, f1, f2, 4, "GRAND-Q", "GRAND-A")
	s2 := agent.Summary{Key: agent.SessionKey{Agent: id, Session: f2}, Path: p2}
	seg2, err := m.Read(ctx, h, in, s2, ir.Cursor{})
	if err != nil || len(seg2.Nodes) != len(seg.Nodes)+2 || seg2.Nodes[len(seg2.Nodes)-1].Text != "GRAND-A" || seg2.Nodes[len(seg.Nodes)-1].Text != "FORK-A" {
		t.Fatalf("fork of a fork: %d nodes, want %d: %v", len(seg2.Nodes), len(seg.Nodes)+2, err)
	}
	if got, err := m.VerifyNativeFork(ctx, h, in, s1, s2); err != nil || len(got) != len(seg.Nodes) {
		t.Fatalf("verify fork of a fork: %d inherited, want %d: %v", len(got), len(seg.Nodes), err)
	}
	// Writing to a fork still needs its own records' numbering: refused, unchanged.
	if _, err := m.Write(ctx, h, in, ir.WriteRequest{Mode: ir.WriteAppend, SessionID: f1, Expect: seg.Cursor, Items: []ir.Item{{Role: ir.RoleUser, Text: "must not write"}}}); err == nil {
		t.Fatal("appended to a by-reference fork")
	}
}

// A fork whose named base is not exactly as declared reads as nothing.
func TestPaginatedForkRefusesBadBase(t *testing.T) {
	const f1 = "01a0fe1c-0000-7000-8000-0000000000f1"
	for _, kind := range []string{"mid-record", "count", "missing", "cyclic", "short"} {
		t.Run(kind, func(t *testing.T) {
			ctx := context.Background()
			m := New()
			h, in, byID := setup(t)
			parent := byID[t1]
			p1, _ := fork(t, h, parent.Path, t1, f1, 13, "FORK-Q", "FORK-A")
			raw, _ := h.FS().ReadFile(p1, 1<<20)
			head, rest, _ := strings.Cut(string(raw), "\n")
			var row map[string]any
			_ = json.Unmarshal([]byte(head), &row)
			base := row["payload"].(map[string]any)["history_base"].(map[string]any)
			want := agent.ErrDiverged
			switch kind {
			case "mid-record":
				base["end_byte_offset"] = base["end_byte_offset"].(float64) - 5
			case "count":
				base["end_ordinal_exclusive"] = 12.0
			case "missing":
				base["thread_id"] = "01a0fe1c-0000-7000-8000-0000000000ff"
				want = agent.ErrUnsupported
			case "cyclic":
				base["thread_id"] = f1
			case "short":
				base["end_byte_offset"] = 1 << 20
			}
			b, _ := json.Marshal(row)
			if err := h.FS().WriteFile(p1, []byte(string(b)+"\n"+rest), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := m.Read(ctx, h, in, agent.Summary{Key: agent.SessionKey{Agent: id, Session: f1}, Path: p1}, ir.Cursor{})
			if !errors.Is(err, want) {
				t.Fatalf("got %v, want %v", err, want)
			}
		})
	}
}
