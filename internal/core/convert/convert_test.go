package convert

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/roeehrl/hopsesh/sdk/agent"
	"github.com/roeehrl/hopsesh/sdk/ir"
)

func session() []ir.Node {
	zero := 0
	in, _ := json.Marshal(map[string]string{"command": "cat /src/demo/notes.txt"})
	ns := []ir.Node{
		{Kind: ir.KindMessage, Actor: ir.User, Text: "What is the codeword in /src/demo/notes.txt?"},
		{Kind: ir.KindReasoning, Actor: ir.Agent, Reasoning: &ir.Reasoning{Opaque: true}},
		{Kind: ir.KindToolCall, Actor: ir.Agent, Tool: &ir.ToolCall{CallID: "c1", Name: "Bash", Input: in, Kind: ir.ToolExecute, Shell: &ir.Shell{Command: "cat /src/demo/notes.txt"}}},
		{Kind: ir.KindToolResult, Actor: ir.User, Result: &ir.ToolResult{CallID: "c1", Status: ir.StatusCompleted, ExitCode: &zero, Output: "The codeword is PLUM-7.\n"}},
		{Kind: ir.KindPlan, Actor: ir.Agent, Plan: []ir.PlanEntry{{Content: "tell the user", Status: "in_progress"}}},
		{Kind: ir.KindMessage, Actor: ir.Agent, Text: "It is PLUM-7."},
	}
	ir.Chain(ns, "")
	return ns
}

func TestRenderHistory(t *testing.T) {
	res := Render(Request{Nodes: session(), From: "Claude Code", To: "Codex", Fidelity: History, Window: 272000,
		Mappings: []agent.Mapping{{From: "/src/demo", To: "/dst/demo"}}, Briefing: Briefing{SourceLoc: "studio", TargetLoc: "laptop", Head: "abc1234", Branch: "main"}})
	if len(res.Items) != 4 {
		t.Fatalf("want user, agent, briefing, acknowledgement; got %d items", len(res.Items))
	}
	if res.Items[0].Role != ir.RoleUser || res.Items[1].Role != ir.RoleAgent || res.Items[2].Role != ir.RoleUser || res.Items[3].Role != ir.RoleAgent {
		t.Fatal("roles must alternate")
	}
	agentText := res.Items[1].Text
	for _, want := range []string{"[prior agent · Claude Code · execute · exit 0]", "$ cat /dst/demo/notes.txt", "PLUM-7", "It is PLUM-7."} {
		if !strings.Contains(agentText, want) {
			t.Errorf("history lacks %q:\n%s", want, agentText)
		}
	}
	if strings.Contains(res.Items[0].Text, "/src/demo") {
		t.Error("paths must be mapped")
	}
	brief := res.Items[2].Text
	for _, want := range []string{"moved from Claude Code", "to Codex on laptop", "abc1234 on main", "[in_progress] tell the user", "hidden reasoning was not carried"} {
		if !strings.Contains(brief, want) {
			t.Errorf("briefing lacks %q:\n%s", want, brief)
		}
	}
	if res.Report.Reasoning != 1 || res.Report.ToolCalls != 1 || res.Report.Messages != 2 {
		t.Fatalf("report %+v", res.Report)
	}
}

func TestRenderNativeAndNote(t *testing.T) {
	res := Render(Request{Nodes: session(), From: "Codex", To: "Claude Code", Fidelity: History, Native: true, Window: 1000000})
	found := false
	for _, it := range res.Items {
		if it.Tool != nil && it.Tool.Call.Shell.Command == "cat /src/demo/notes.txt" && strings.Contains(it.Tool.Result.Output, "PLUM-7") {
			found = true
		}
	}
	if !found || res.Report.NativeCalls != 1 {
		t.Fatalf("native replay: %+v", res.Report)
	}
	note := Render(Request{Nodes: session(), From: "Codex", To: "Claude Code", Fidelity: Note, Window: 1000000})
	if len(note.Items) != 2 {
		t.Fatalf("a note is only the briefing and its acknowledgement: %d", len(note.Items))
	}
}

func TestBudgetSummarisesOldest(t *testing.T) {
	var ns []ir.Node
	for i := 0; i < 200; i++ {
		ns = append(ns, ir.Node{Kind: ir.KindMessage, Actor: ir.User, Text: strings.Repeat("question ", 100)},
			ir.Node{Kind: ir.KindMessage, Actor: ir.Agent, Text: strings.Repeat("answer ", 100)})
	}
	ir.Chain(ns, "")
	res := Render(Request{Nodes: ns, From: "Claude Code", To: "Codex", Fidelity: History, Window: 40000})
	if res.Report.Summarised == 0 || res.Report.Used > res.Report.Budget*12/10 {
		t.Fatalf("report %+v", res.Report)
	}
	if !strings.Contains(res.Items[0].Text, "are summarised here to fit") || res.Items[0].Role != ir.RoleUser || res.Items[1].Role != ir.RoleAgent {
		t.Fatalf("the digest leads the first kept user turn and roles alternate: %q", res.Items[0].Text[:60])
	}
}
