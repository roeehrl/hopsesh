package convert

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/roeehrl/hopsesh/sdk/ir"
)

func turns(n, size int) []ir.Node {
	var nodes []ir.Node
	for i := 0; i < n; i++ {
		actor := ir.User
		if i%2 == 1 {
			actor = ir.Agent
		}
		nodes = append(nodes, ir.Node{ID: ir.NodeID(fmt.Sprint("n", i)), Kind: ir.KindMessage, Actor: actor, Text: fmt.Sprintf("turn %d %s", i, strings.Repeat("w", size))})
	}
	return nodes
}

func TestRecentTurnsOnlyOmitsWithoutCoverage(t *testing.T) {
	nodes := turns(60, 2000)
	res := Render(Request{Nodes: nodes, From: "Codex", To: "Claude Code", Fidelity: History, Window: 64000, Limits: ir.Limits{Older: ir.OlderRecent}})
	if res.Report.Blocked != "" {
		t.Fatal(res.Report.Blocked)
	}
	if res.Report.Omitted == 0 || res.Report.Older != ir.OlderRecent || res.Report.OldestIncluded == "" {
		t.Fatalf("report: %+v", res.Report)
	}
	var notice *ir.Item
	for i, it := range res.Items {
		if it.Node == "hopsesh/digest" {
			t.Fatal("recent-only mode produced an extract")
		}
		if it.Node == "hopsesh/omitted" {
			notice = &res.Items[i]
		}
	}
	if notice == nil || len(notice.Coverage) != 0 || !strings.Contains(notice.Text, "preserved archive") {
		t.Fatalf("omitted history must be labeled and never counted as covered: %+v", notice)
	}
	if !strings.Contains(res.Report.Summary, "left out") {
		t.Fatalf("summary hides omission: %s", res.Report.Summary)
	}
}

func TestUserBudgetOnlyLowers(t *testing.T) {
	nodes := turns(10, 100)
	low := Render(Request{Nodes: nodes, Fidelity: History, Window: 64000, Limits: ir.Limits{ContextBudget: 8000}})
	if low.Report.Budget != 8000 || low.Report.UserBudget != 8000 || low.Report.Used > 8000 {
		t.Fatalf("budget not applied: %+v", low.Report)
	}
	high := Render(Request{Nodes: nodes, Fidelity: History, Window: 64000, Limits: ir.Limits{ContextBudget: 1_000_000}})
	if high.Report.Budget != 64000*3/10 || high.Report.UserBudget != 0 {
		t.Fatalf("a user budget raised the model allowance: %+v", high.Report)
	}
}

func TestArchiveLimitIsAnErrorNotATruncation(t *testing.T) {
	_, err := Archive(turns(10, 200_000), Request{Limits: ir.Limits{ArchiveBytes: 1 << 20}})
	le, ok := ir.AsLimit(err)
	if !ok || le.Stage != ir.StageArchive || le.Setting() != "history.archive_mb" {
		t.Fatalf("want archive LimitError, got %v", err)
	}
}

func TestArchiveWriterMergesPriorRecordByRecord(t *testing.T) {
	prior, err := Archive(turns(6, 50), Request{})
	if err != nil {
		t.Fatal(err)
	}
	w := NewArchiveWriter(Request{})
	if err = w.AddArchive(bytes.NewReader(prior)); err != nil {
		t.Fatal(err)
	}
	if err = w.AddNodes(turns(8, 50)); err != nil { // the first 6 repeat
		t.Fatal(err)
	}
	if got := bytes.Count(w.Bytes(), []byte{'\n'}); got != 8 || w.Records() != 8 {
		t.Fatalf("merge kept %d records, want 8", got)
	}
	if !bytes.HasPrefix(w.Bytes(), prior) {
		t.Fatal("prior records must come first, unchanged")
	}
}

func TestExtractSurplusGoesToRecentTurns(t *testing.T) {
	res := Render(Request{Nodes: turns(200, 1500), From: "Codex", To: "Claude Code", Fidelity: History, Window: 64000})
	if res.Report.Blocked != "" {
		t.Fatal(res.Report.Blocked)
	}
	if res.Report.Used < res.Report.Budget*7/10 || res.Report.Used > res.Report.Budget {
		t.Fatalf("working context %d of %d: budget left unused", res.Report.Used, res.Report.Budget)
	}
}
