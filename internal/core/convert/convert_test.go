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
		t.Fatalf("want user, agent, briefing, import notice; got %d items", len(res.Items))
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
	// Replayed with paths mapped: the command and the folder it ran in.
	ns := session()
	ns[2].Tool.Shell.Dir = "/src/demo/sub"
	mapped := Render(Request{Nodes: ns, From: "Codex", To: "OpenCode", Fidelity: History, Native: true, Window: 1000000,
		Mappings: []agent.Mapping{{From: "/src/demo", To: "/dst/demo"}}})
	if it := mapped.Items[1]; it.Tool == nil || it.Tool.Call.Shell.Command != "cat /dst/demo/notes.txt" || it.Tool.Call.Shell.Dir != "/dst/demo/sub" {
		t.Fatalf("mapped replay: %+v", it.Tool)
	}
	note := Render(Request{Nodes: session(), From: "Codex", To: "Claude Code", Fidelity: Note, Window: 1000000})
	if len(note.Items) != 2 {
		t.Fatalf("a note is only the briefing and its import notice: %d", len(note.Items))
	}
}

func TestImportNoticeDoesNotImpersonateDestination(t *testing.T) {
	for _, target := range []string{"Codex", "Claude Code"} {
		for _, fidelity := range []Fidelity{History, Note} {
			t.Run(target+"/"+string(fidelity), func(t *testing.T) {
				res := Render(Request{Nodes: session(), From: "Source agent", To: target, Fidelity: fidelity, Window: 64000})
				if res.Report.Blocked != "" || res.Report.Used > res.Report.Budget || len(res.Items) < 2 {
					t.Fatalf("import failed: %+v", res.Report)
				}
				notice := res.Items[len(res.Items)-1]
				if !notice.Generated || len(notice.Coverage) != 0 || !strings.HasPrefix(notice.Text, agent.NotePrefix) {
					t.Fatalf("notice must be attributed to Hopsesh, without authored revision coverage: %+v", notice)
				}
				for _, want := range []string{"not by " + target, "No agent response", "Send your next message"} {
					if !strings.Contains(notice.Text, want) {
						t.Fatalf("notice conceals import state: %s", notice.Text)
					}
				}
				if strings.Contains(notice.Text, "I'll") || strings.Contains(notice.Text, "Understood") {
					t.Fatalf("import must not fabricate a model commitment: %s", notice.Text)
				}
			})
		}
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
	if !strings.Contains(res.Items[0].Text, "Transfer context") || res.Items[0].Role != ir.RoleUser || res.Items[1].Role != ir.RoleAgent {
		t.Fatalf("context stays separate from the first kept user turn: %q", res.Items[0].Text[:60])
	}
}

func TestProjectionCoverageSurvivesCoalescingAndSummaries(t *testing.T) {
	nodes := session()
	for i := range nodes {
		nodes[i].Coverage = []ir.NodeID{nodes[i].ID}
	}
	res := Render(Request{Nodes: nodes, From: "Claude Code", To: "Codex", Fidelity: History, Window: 1000000})
	represented := map[ir.NodeID]bool{}
	for _, item := range res.Items {
		if item.Generated && len(item.Coverage) > 0 {
			t.Fatal("generated briefing acquired authored revision coverage")
		}
		for _, id := range item.Coverage {
			represented[id] = true
		}
		for _, f := range item.Fragments {
			for _, id := range f.Coverage {
				if !represented[id] {
					t.Fatal("fragment lost its parent's coverage")
				}
			}
		}
	}
	for _, n := range nodes {
		if n.Kind != ir.KindReasoning && !represented[n.ID] {
			t.Fatalf("coalescing omitted coverage for %s", n.Kind)
		}
	}
	var many []ir.Node
	for i := 0; i < 80; i++ {
		actor := ir.User
		if i%2 != 0 {
			actor = ir.Agent
		}
		many = append(many, ir.Node{Kind: ir.KindMessage, Actor: actor, Text: strings.Repeat("history ", 200)})
	}
	ir.Chain(many, "")
	for i := range many {
		many[i].Coverage = []ir.NodeID{many[i].ID}
	}
	summarized := Render(Request{Nodes: many, From: "Claude Code", To: "Codex", Fidelity: History, Window: 4000})
	if summarized.Report.Summarised == 0 {
		t.Fatal("budget fixture did not summarize")
	}
	represented = map[ir.NodeID]bool{}
	loss := false
	for _, it := range summarized.Items {
		for _, id := range it.Coverage {
			represented[id] = true
		}
		if it.Fidelity == "summarized" {
			loss = true
		}
	}
	if !loss {
		t.Fatal("summary omitted its fidelity annotation")
	}
	for _, n := range many {
		if !represented[n.ID] {
			t.Fatal("summary dropped provenance")
		}
	}
}

func TestGeneratedContextDoesNotBecomeNewWork(t *testing.T) {
	nodes := []ir.Node{{Kind: ir.KindMessage, Actor: ir.User, Text: "generated-old-briefing", Generated: true}, {Kind: ir.KindMessage, Actor: ir.User, Text: "actual authored continuation", Coverage: []ir.NodeID{"revision"}}}
	ir.Chain(nodes, "")
	r := Render(Request{Nodes: nodes, From: "Claude Code", To: "Codex", Fidelity: History, Window: 1000000})
	for _, it := range r.Items {
		if strings.Contains(it.Text, "generated-old-briefing") {
			t.Fatal("old generated context became imported work")
		}
	}
	r = Render(Request{Nodes: nodes, IncludeGenerated: true, From: "Cloud", To: "Codex", Fidelity: History, Window: 1000000})
	for _, it := range r.Items {
		if strings.Contains(it.Text, "generated-old-briefing") && (!it.Generated || len(it.Coverage) != 0) {
			t.Fatal("cloud context became authored work during coalescing")
		}
	}
}

// A result whose call came in an earlier read (a subagent's answer that arrived after its
// call was carried) is rendered on its own, with its coverage; one whose call is in the
// same read is rendered once, with the call.
func TestLoneResultIsRendered(t *testing.T) {
	answer := []ir.Node{
		{Kind: ir.KindToolResult, Actor: ir.User, Result: &ir.ToolResult{CallID: "c9", Status: ir.StatusCompleted, Output: "The subagent found FIG-3.\n"}},
		{Kind: ir.KindMessage, Actor: ir.User, Text: "Thanks."},
	}
	ir.Chain(answer, "earlier")
	res := Render(Request{Nodes: answer, From: "OpenCode", To: "Claude Code", Fidelity: History, Window: 272000})
	if len(res.Items) < 1 || res.Items[0].Role != ir.RoleAgent || !strings.Contains(res.Items[0].Text, "result of an earlier call") ||
		!strings.Contains(res.Items[0].Text, "FIG-3") {
		t.Fatalf("the lone result is not rendered: %+v", res.Items)
	}
	covered := false
	for _, id := range res.Items[0].Coverage {
		covered = covered || id == answer[0].ID
	}
	if !covered {
		t.Fatalf("the rendered result does not cover its node: %v", res.Items[0].Coverage)
	}
	full := Render(Request{Nodes: session(), From: "Claude Code", To: "Codex", Fidelity: History, Window: 272000})
	for _, it := range full.Items {
		if strings.Contains(it.Text, "result of an earlier call") {
			t.Fatalf("a result rendered with its call was rendered again: %s", it.Text)
		}
	}
	// The same for any source: a long Task call's answer on Claude Code's incremental read.
	task := []ir.Node{
		{Kind: ir.KindToolResult, Actor: ir.User, Result: &ir.ToolResult{CallID: "toolu_9", Status: ir.StatusFailed, Output: "The task stopped at FIG-4.\n"}},
		{Kind: ir.KindMessage, Actor: ir.User, Text: "Go on."},
	}
	ir.Chain(task, "earlier")
	claude := Render(Request{Nodes: task, From: "Claude Code", To: "Codex", Fidelity: History, Window: 272000})
	if len(claude.Items) < 1 || !strings.Contains(claude.Items[0].Text, "[prior agent · Claude Code · result of an earlier call · failed]") ||
		!strings.Contains(claude.Items[0].Text, "FIG-4") {
		t.Fatalf("the lone Claude Code result is not rendered: %+v", claude.Items)
	}
	// A call that is not rendered (hopsesh's own, left out) does not hide its result.
	ns := session()
	ns[2].Generated = true
	skipped := Render(Request{Nodes: ns, From: "Codex", To: "Claude Code", Fidelity: History, Window: 272000})
	shown := false
	for _, it := range skipped.Items {
		if strings.Contains(it.Text, "$ cat") {
			t.Fatalf("a generated call was rendered: %s", it.Text)
		}
		shown = shown || strings.Contains(it.Text, "result of an earlier call") && strings.Contains(it.Text, "PLUM-7")
	}
	if !shown {
		t.Fatalf("the result of a skipped call was dropped: %+v", skipped.Items)
	}
}
