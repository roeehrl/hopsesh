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
