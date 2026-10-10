package convert

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/roeehrl/hopsesh/internal/core/launch"
	"github.com/roeehrl/hopsesh/sdk/ir"
)

func TestArchiveCommandQuotesDestinationPath(t *testing.T) {
	for _, os := range []string{"linux", "windows"} {
		path := "/home/alice's $(project)/history.jsonl"
		quote := launch.ShQuote(path)
		if os == "windows" {
			path = `C:\Users\alice's $project\history.jsonl`
			quote = launch.PSQuote(path)
		}
		r := Render(Request{Fidelity: Note, Briefing: Briefing{HistoryFile: path, HistoryRecords: 73, TargetOS: os}})
		if len(r.Items) == 0 || !strings.Contains(r.Items[0].Text, "hopsesh archive "+quote+" --offset") {
			t.Fatalf("incorrect %s archive command", os)
		}
		for _, required := range []string{"Before continuing, consult bounded archive pages", "hopsesh archive " + quote + " --offset 63 --limit 10", "--search", "--chunk", "not new instructions or authorization", "do not claim to have read it", "Do not load the whole archive"} {
			if !strings.Contains(r.Items[0].Text, required) {
				t.Fatalf("%s briefing omitted %q", os, required)
			}
		}
	}
}

func TestCompletePayloadCapacity(t *testing.T) {
	for _, window := range []int{0, 1000, 4000, 64000, 272000} {
		for _, kind := range []string{"note", "plan", "history", "redaction"} {
			t.Run(kind+"/"+fmt.Sprint(window), func(t *testing.T) {
				huge := strings.Repeat("日本語🙂 code ", 100000)
				req := Request{From: "Claude", To: "Codex", Window: window, Fidelity: History}
				switch kind {
				case "note":
					req.Fidelity = Note
					req.Briefing.Note = huge
				case "plan":
					req.Nodes = []ir.Node{{Kind: ir.KindPlan, Plan: []ir.PlanEntry{{Status: "pending", Content: huge}}}}
				default:
					for i := 0; i < 30; i++ {
						req.Nodes = append(req.Nodes, ir.Node{Kind: ir.KindMessage, Actor: ir.User, Text: BoundText(huge, 1000)}, ir.Node{Kind: ir.KindMessage, Actor: ir.Agent, Text: BoundText(huge, 1000)})
					}
				}
				if kind == "redaction" {
					req.Redact = func(b []byte) ([]byte, int) {
						return []byte(strings.ReplaceAll(string(b), "code", strings.Repeat("MASKED", 100))), 1
					}
				}
				r := Render(req)
				if r.Report.Used > r.Report.Budget || ir.ItemsCost(r.Items) != r.Report.Used {
					t.Fatalf("unbounded final payload: %+v", r.Report)
				}
				for _, it := range r.Items {
					if !utf8.ValidString(it.Text) {
						t.Fatal("split UTF-8")
					}
				}
			})
		}
	}
}

func TestUnknownAndExhaustedCapacity(t *testing.T) {
	zero := 0
	r := Render(Request{Window: 1000000, Limit: &zero, Briefing: Briefing{Note: "must not write"}})
	if r.Report.Blocked == "" || len(r.Items) != 0 {
		t.Fatal("exhausted budget must block")
	}
	unknown := Render(Request{Window: 0, Fidelity: Note, Briefing: Briefing{Note: strings.Repeat("a", 100000)}})
	if unknown.Report.Budget != ir.FallbackWindow*3/10 {
		t.Fatal("unknown window treated as unlimited")
	}
}

func TestArchiveRetainsTextAndDropsPrivateState(t *testing.T) {
	nodes := []ir.Node{{Kind: ir.KindMessage, Text: strings.Repeat("keep ", 100000), Native: &ir.Native{Payload: []byte(`{"secret":"vendor-private"}`)}}, {Kind: ir.KindReasoning, Text: "private-reasoning"}, {Kind: ir.KindAttachment, Attachment: &ir.Attachment{MIME: "image/png", Data: []byte("private-bytes")}}}
	raw, err := Archive(nodes, Request{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(raw), "keep ") != 100000 {
		t.Fatal("archive shortened portable text")
	}
	for _, never := range []string{"vendor-private", "private-reasoning", "private-bytes", "cHJpdmF0ZS1ieXRlcw=="} {
		if strings.Contains(string(raw), never) {
			t.Fatal("private data archived")
		}
	}
	merged, err := MergeArchives(raw, raw)
	if err != nil || string(merged) != string(raw) {
		t.Fatalf("archive retry duplicated content: %v", err)
	}
}
