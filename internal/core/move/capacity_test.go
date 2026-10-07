package move

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/roeehrl/hopsesh/agents/codex"
	"github.com/roeehrl/hopsesh/internal/core/convert"
	"github.com/roeehrl/hopsesh/internal/core/host"
	"github.com/roeehrl/hopsesh/internal/core/lineage"
	"github.com/roeehrl/hopsesh/sdk/agent"
	"github.com/roeehrl/hopsesh/sdk/agent/agenttest"
	"github.com/roeehrl/hopsesh/sdk/ir"
)

func TestCloudPortableReturnRollsOverFullContext(t *testing.T) {
	ctx := context.Background()
	fh := agenttest.NewFakeHost("/home/alice")
	m := codex.New()
	in := agent.Install{Agent: "codex", Roots: map[string]string{"home": "/home/alice/.codex"}}
	h := agent.Confine(fh, m.Spec(), in)
	w, err := m.Write(ctx, h, in, ir.WriteRequest{Mode: ir.WriteNew, Header: ir.Header{CWD: "/home/alice/git/demo"}, Items: []ir.Item{{Role: ir.RoleUser, Text: "original"}}})
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(map[string]any{"type": "response_item", "payload": map[string]any{"type": "message", "role": "user", "content": []any{map[string]any{"type": "input_text", "text": strings.Repeat("existing ", 10000)}}}})
	if err = h.FS().Append(w.Path, append(payload, '\n'), agent.AppendOptions{}); err != nil {
		t.Fatal(err)
	}
	original := agent.Summary{Key: agent.SessionKey{Agent: "codex", Session: agent.SessionID(w.SessionID)}, Path: w.Path, CWD: "/home/alice/git/demo"}
	seg, err := m.Read(ctx, h, in, original, ir.Cursor{})
	if err != nil {
		t.Fatal(err)
	}
	before, _ := fh.Get(w.Path)
	full := []ir.Node{{Kind: ir.KindMessage, Actor: ir.User, Text: strings.Repeat("cloud task ", 10000)}, {Kind: ir.KindMessage, Actor: ir.Agent, Text: "Completed the migration; verify tests next."}}
	req, res, rolled, err := portableWrite(ctx, h, m, in, &original, full, ir.WriteRequest{OperationID: "test-cloud", Mode: ir.WriteAppend, SessionID: w.SessionID, Expect: seg.Cursor, Header: ir.Header{CWD: original.CWD}}, convert.Request{Nodes: full, From: "Cloud", To: "Codex", Fidelity: convert.History, Briefing: convert.Briefing{When: time.Now()}})
	if err != nil {
		t.Fatal(err)
	}
	if !rolled || req.Mode != ir.WriteNew || req.SessionID == w.SessionID || res.Report.Used > res.Report.Budget {
		t.Fatalf("not a bounded rollover: %+v", res.Report)
	}
	if _, err = m.Write(ctx, h, in, req); err != nil {
		t.Fatal(err)
	}
	after, _ := fh.Get(w.Path)
	if string(before) != string(after) {
		t.Fatal("cloud rollover rewrote original")
	}
	archived, ok := fh.Get(res.Report.Archive)
	if !ok || !strings.Contains(string(archived), "cloud task") || !strings.Contains(string(archived), "existing existing") {
		t.Fatal("cloud history archive missing")
	}
	stale := req
	stale.Expect = ir.Cursor{}
	if _, _, _, err := portableWrite(ctx, h, m, in, &original, full, stale, convert.Request{}); err == nil {
		t.Fatal("changed original accepted during rollover")
	}
}

type capacityReader struct {
	*codex.Module
	cursor ir.Cursor
}

func (m *capacityReader) Read(context.Context, agent.Host, agent.Install, agent.Summary, ir.Cursor) (ir.Segment, error) {
	return ir.Segment{Cursor: m.cursor}, nil
}

func TestContextRetainedCopyRouting(t *testing.T) {
	key := agent.SessionKey{Agent: "codex", Session: "original"}
	newKey := agent.SessionKey{Agent: "codex", Session: "replacement"}
	cursor := ir.Cursor{Head: "unchanged", Offset: 100}
	for _, tc := range []struct {
		name                                    string
		missing, undone, edited, explicit, live bool
		want                                    int
	}{
		{name: "replacement present", want: 0},
		{name: "replacement missing", missing: true, want: 1},
		{name: "replacement undone", undone: true, want: 1},
		{name: "original edited", edited: true, want: 1},
		{name: "original selected", explicit: true, want: 1},
		{name: "original running", live: true, want: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := lineage.NewNative("local", key)
			old := m.Upsert(lineage.Replica{Key: key, Endpoint: "local"})
			to := m.Upsert(lineage.Replica{Key: newKey, Endpoint: "local"})
			if err := m.AppendHop(lineage.Hop{ID: "recovery", From: old, To: to, Kind: lineage.HopContinue, Rollover: &lineage.Rollover{Replica: old, Cursor: cursor}}); err != nil {
				t.Fatal(err)
			}
			reader := &capacityReader{Module: codex.New(), cursor: cursor}
			if tc.edited {
				reader.cursor.Head = "new work"
			}
			side := Side{Machine: &host.Machine{Local: true, Facts: host.Facts{Endpoint: "local"}}, Module: reader, Install: agent.Install{Agent: "codex"}}
			in := Input{Source: side, Target: side, Session: agent.Summary{Key: newKey}, Lineage: m, Copies: []Copy{{Summary: agent.Summary{Key: key}}}}
			if tc.missing {
				in.Session.Key.Session = "other"
			}
			if tc.undone {
				// Compensation may arrive from a different replica than the stale hop.
				in.Copies[0].Lineage = m.Clone()
				if err := in.Copies[0].Lineage.UndoOperation("recovery"); err != nil {
					t.Fatal(err)
				}
			}
			if tc.live {
				in.Copies[0].Live.State = agent.Live
			}
			opt := Options{}
			if tc.explicit {
				opt.TargetSession = string(key.Session)
			}
			if got := activeCopies(context.Background(), in, opt); len(got) != tc.want {
				t.Fatalf("got %d copies, want %d", len(got), tc.want)
			}
		})
	}
}
