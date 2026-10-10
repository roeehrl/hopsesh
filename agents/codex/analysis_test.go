package codex

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/roeehrl/hopsesh/sdk/agent"
	"github.com/roeehrl/hopsesh/sdk/ir"
)

type repeatedAnalysisRecord struct {
	record              []byte
	remaining, position int
}

func (r *repeatedAnalysisRecord) Read(p []byte) (int, error) {
	if r.remaining == 0 {
		return 0, io.EOF
	}
	n := copy(p, r.record[r.position:])
	r.position += n
	if r.position == len(r.record) {
		r.position = 0
		r.remaining--
	}
	return n, nil
}

func TestAnalysisLargeRepeatedCompaction(t *testing.T) {
	meta := `{"type":"session_meta","payload":{"id":"large","cwd":"/repo"}}` + "\n"
	replacement := `{"type":"message","role":"developer","content":"` + strings.Repeat("x", 1<<20) + `"}`
	record := []byte(`{"type":"compacted","payload":{"message":"","replacement_history":[` + replacement + `]}}` + "\n")
	last := `{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"LATEST-WORK"}]}}` + "\n"
	const copies = 260
	expected := int64(len(meta) + copies*len(record) + len(last))
	if expected <= ir.MaxTranscriptBytes {
		t.Fatal("fixture does not cross raw analysis cap")
	}
	rs, end, err := readAnalysisLines(context.Background(), io.MultiReader(strings.NewReader(meta), &repeatedAnalysisRecord{record: record, remaining: copies}, strings.NewReader(last)))
	if err != nil {
		t.Fatal(err)
	}
	if end != expected || len(rs) != copies+2 {
		t.Fatalf("lost native positions: end=%d records=%d", end, len(rs))
	}
	retained := 0
	for _, r := range rs {
		retained += len(r.Payload)
	}
	if retained > 10000 || rs[1].compactBytes == nil || *rs[1].compactBytes != len(replacement)+32 {
		t.Fatalf("retained repeated context or lost capacity: retained=%d", retained)
	}
	ns := fromResponse(rs[len(rs)-1], parseTime(""), false)
	if len(ns) != 1 || ns[0].Text != "LATEST-WORK" {
		t.Fatal("lost conversation after large metadata")
	}
}

func TestAnalysisPreservesRecordsAndFailsClosed(t *testing.T) {
	raw := `{"ordinal":0,"type":"session_meta","payload":{"history_mode":"paginated"}}` + "\n" +
		`{"ordinal":1,"type":"world_state","payload":{"private":"runtime state"}}` + "\n" +
		`{"ordinal":2,"type":"compacted","payload":{"message":"SUMMARY","replacement_history":[]}}` + "\n" +
		`{"ordinal":3,"type":"event_msg","payload":{"type":"item_completed","item":{"type":"CommandExecution"}}}` + "\n" +
		`{"ordinal":4,"type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"FINAL"}]}}` + "\n"
	rs, end, err := readAnalysisLines(context.Background(), strings.NewReader(raw+`{"type":"unfinished"`))
	if err != nil || end != int64(len(raw)) || len(rs) != 5 {
		t.Fatalf("partial tail/positions: %d %d %v", len(rs), end, err)
	}
	if len(rs[1].Payload) != 0 || !bytes.Contains(rs[2].Payload, []byte("SUMMARY")) || !bytes.Contains(rs[3].Payload, []byte("item_completed")) {
		t.Fatal("incorrect analysis projection")
	}
	var mt meta
	_ = json.Unmarshal(rs[0].Payload, &mt)
	if _, err = paginatedNext(mt, rs); err != nil {
		t.Fatal(err)
	}
	if _, _, err = readAnalysisLines(context.Background(), strings.NewReader(raw+"invalid\n")); !errors.Is(err, agent.ErrDiverged) {
		t.Fatalf("invalid paginated record accepted: %v", err)
	}
	if _, _, err = readAnalysisLinesWithLimits(context.Background(), strings.NewReader(raw), 10, 1024); err == nil || !strings.Contains(err.Error(), "no history was truncated") {
		t.Fatalf("retained limit: %v", err)
	}
	if _, _, err = readAnalysisLinesWithLimits(context.Background(), strings.NewReader(raw), 1024, 10); err == nil || !strings.Contains(err.Error(), "native record") {
		t.Fatalf("record limit: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err = readAnalysisLines(ctx, strings.NewReader(raw)); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation ignored: %v", err)
	}
}
