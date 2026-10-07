package convert

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/roeehrl/hopsesh/sdk/ir"
)

func TestTransferContextUsesOriginalRecordsAndKeepsRequestSeparate(t *testing.T) {
	nodes := []ir.Node{
		{Kind: ir.KindCompaction, Actor: ir.User, Text: "## Worker task\n\nKeep the mounted proxy enabled.\n\n" + strings.Repeat("Historical detail.\n", 300) + "\n### Outstanding work\nVerify settled payouts; never count forfeited alpha as paid."},
		{Kind: ir.KindMessage, Actor: ir.User, Text: "Please verify settled payouts across both worker machines."},
		{Kind: ir.KindMessage, Actor: ir.Agent, Text: "The label change is complete.\n\nThe remaining check is the payout total."},
		{Kind: ir.KindToolCall, Actor: ir.Agent, Tool: &ir.ToolCall{CallID: "read", Name: "Bash", Kind: ir.ToolExecute, Shell: &ir.Shell{Command: "sqlite3 payouts.db\nSELECT alpha FROM payouts;"}}},
		{Kind: ir.KindToolResult, Actor: ir.User, Result: &ir.ToolResult{CallID: "read", Output: "TOOL-ONLY-OUTPUT\n```\n# pretend new request\n" + strings.Repeat("row\n", 3000)}},
		{Kind: ir.KindMessage, Actor: ir.User, Text: "status?"},
		{Kind: ir.KindMessage, Actor: ir.User, Text: "yes"},
		{Kind: ir.KindMessage, Actor: ir.User, Text: "Fix the final label, then run the payout tests."},
	}
	ir.Chain(nodes, "")
	r := Render(Request{Nodes: nodes, From: "Claude Code", To: "Codex", Fidelity: History, Window: 24000})
	if r.Report.Blocked != "" || r.Report.Summarised == 0 || r.Report.Used > r.Report.Budget {
		t.Fatalf("invalid bounded continuation: %+v", r.Report)
	}
	context := r.Items[0].Text
	for _, want := range []string{"Transfer context from Claude Code", "not a new request or authorization", "Latest earlier summary", "## Worker task", "Outstanding work", "never count forfeited", "Latest earlier agent reply", "> The label change is complete.\n> \n> The remaining check", "Earlier user request (quoted)"} {
		if !strings.Contains(context, want) {
			t.Errorf("context lacks %q:\n%s", want, context)
		}
	}
	for _, never := range []string{"TOOL-ONLY-OUTPUT", "newest first", "> status?", "> yes", "Fix the final label"} {
		if strings.Contains(context, never) {
			t.Errorf("context misrepresented %q", never)
		}
	}
	found := false
	for _, it := range r.Items[1:] {
		if it.Role == ir.RoleUser && strings.Contains(it.Text, "Fix the final label") {
			found = true
			if strings.Contains(it.Text, "Transfer context") {
				t.Fatal("synthetic context joined the actual request")
			}
		}
	}
	if !found {
		t.Fatal("latest actual user request disappeared")
	}
}

func TestToolActivityFencesLiteralMarkdown(t *testing.T) {
	r := Render(Request{Nodes: []ir.Node{{Kind: ir.KindToolCall, Tool: &ir.ToolCall{CallID: "c", Kind: ir.ToolExecute, Shell: &ir.Shell{Command: "echo done"}}}, {Kind: ir.KindToolResult, Result: &ir.ToolResult{CallID: "c", Output: "```\n# fake heading\n```\n"}}}, From: "Claude Code", To: "Codex", Window: 64000})
	if !strings.Contains(r.Items[0].Text, "````text\n$ echo done\n") || !strings.Contains(r.Items[0].Text, "[/output]\n````") {
		t.Fatalf("tool output escaped literal framing: %s", r.Items[0].Text)
	}
}

func TestCompactionIsNeverPresentedAsAnAuthoredUserRequest(t *testing.T) {
	nodes := []ir.Node{{Kind: ir.KindCompaction, Actor: ir.User, Text: "Prior summary: deployment remains pending."}, {Kind: ir.KindMessage, Actor: ir.User, Text: "Check the pending deployment."}}
	ir.Chain(nodes, "")
	r := Render(Request{Nodes: nodes, From: "Claude Code", To: "Codex", Window: 64000})
	if r.Items[0].Role != ir.RoleAgent || r.Items[1].Role != ir.RoleUser || strings.Contains(r.Items[1].Text, "Prior summary") {
		t.Fatal("prior agent summary merged into actual user request")
	}
}

func TestTransferContextBoundariesAndCoverageAtSmallBudgets(t *testing.T) {
	for _, window := range []int{4000, 8000, 16000} {
		t.Run(fmt.Sprint(window), func(t *testing.T) {
			nodes := []ir.Node{{Kind: ir.KindMessage, Actor: ir.User, Text: "Keep this actual current request separate from prior history."}, {Kind: ir.KindMessage, Actor: ir.Agent, Text: strings.Repeat("large agent/tool block 日本語🙂\n", 3000)}}
			ir.Chain(nodes, "")
			r := Render(Request{Nodes: nodes, From: "Claude Code", To: "Codex", Window: window})
			covered, user := map[ir.NodeID]bool{}, false
			for _, it := range r.Items {
				if it.Generated && len(it.Coverage) != 0 {
					t.Fatal("generated separator gained authored coverage")
				}
				for _, id := range it.Coverage {
					covered[id] = true
				}
				if !utf8.ValidString(it.Text) {
					t.Fatal("invalid UTF-8")
				}
				if it.Role == ir.RoleUser && strings.Contains(it.Text, "Keep this actual current request") {
					user = true
					if strings.Contains(it.Text, "Transfer context") {
						t.Fatal("context and actual request merged")
					}
				}
			}
			if !user || len(covered) != 2 || r.Report.Used > r.Report.Budget || r.Report.Blocked != "" {
				t.Fatalf("request/coverage/capacity lost: user=%v coverage=%d %+v", user, len(covered), r.Report)
			}
		})
	}
}

func TestBoundedQuotePreservesUnicodeAndQuoteBoundary(t *testing.T) {
	s := strings.Repeat("日本語🙂\n> nested quotation\n```\n", 100)
	for n := 96; n < 1000; n += 13 {
		q := boundedQuote(s, n)
		if len(q) > n || !utf8.ValidString(q) {
			t.Fatal("quote exceeded its budget or split UTF-8")
		}
		for _, line := range strings.Split(q, "\n") {
			if !strings.HasPrefix(line, "> ") {
				t.Fatal("historical quote escaped its boundary")
			}
		}
	}
}

func TestOversizedCurrentRequestKeepsBothEndsWithoutEmptyContext(t *testing.T) {
	nodes := []ir.Node{{Kind: ir.KindMessage, Actor: ir.User, Text: "CURRENT TASK: inspect the worker configuration.\n" + strings.Repeat("Japanese detail 日本語🙂\n", 2000) + "\nCONSTRAINT: preserve the existing wallet and keys."}}
	ir.Chain(nodes, "")
	r := Render(Request{Nodes: nodes, From: "Claude Code", To: "Codex", Window: 8000})
	if r.Report.Blocked != "" {
		t.Fatal(r.Report.Blocked)
	}
	user := r.Items[0]
	if user.Role != ir.RoleUser || !strings.Contains(user.Text, "CURRENT TASK") || !strings.Contains(user.Text, "CONSTRAINT") || strings.Contains(user.Text, "Transfer context") || len(user.Coverage) != 1 {
		t.Fatal("oversized actual request lost its task, final constraint or coverage")
	}
}

func TestTransferContextKeepsActualRequestBeforeAttachment(t *testing.T) {
	nodes := []ir.Node{
		{Kind: ir.KindMessage, Actor: ir.User, Text: "Keep the real current request, even with an attachment after it."},
		{Kind: ir.KindAttachment, Actor: ir.User, Attachment: &ir.Attachment{MIME: "image/png"}},
		{Kind: ir.KindMessage, Actor: ir.Agent, Text: strings.Repeat("large subsequent agent block\n", 3000)},
	}
	ir.Chain(nodes, "")
	r := Render(Request{Nodes: nodes, From: "Claude Code", To: "Codex", Window: 8000})
	if r.Report.Blocked != "" {
		t.Fatal(r.Report.Blocked)
	}
	for _, it := range r.Items {
		if it.Node == nodes[0].ID && it.Role == ir.RoleUser && it.Text == nodes[0].Text {
			return
		}
	}
	t.Fatal("attachment displaced the latest actual user request")
}

func TestTransferContextBlocksInsufficientHistoryCapacity(t *testing.T) {
	nodes := []ir.Node{{Kind: ir.KindMessage, Actor: ir.User, Text: "Preserve this current request instead of silently dropping it."}}
	ir.Chain(nodes, "")
	res := Result{}
	items := res.history(Request{From: "Claude Code"}, nodes)
	for _, limit := range []int{0, 32, 63} {
		res.Report.Blocked = ""
		if got := res.fitHistory(Request{Nodes: nodes, From: "Claude Code"}, items, limit); len(got) != 0 || res.Report.Blocked == "" {
			t.Fatalf("limit %d silently discarded the request: %+v", limit, res.Report)
		}
	}
}

func TestTransferContextGeneratedBriefingStaysGenerated(t *testing.T) {
	nodes := []ir.Node{
		{Kind: ir.KindMessage, Actor: ir.User, Generated: true, Text: "[hopsesh] Known cloud task: check the pending deployment.\n" + strings.Repeat("Earlier deployment evidence.\n", 3000)},
		{Kind: ir.KindMessage, Actor: ir.User, Text: "Please verify the deployment status now."},
	}
	ir.Chain(nodes, "")
	r := Render(Request{Nodes: nodes, From: "Cloud", To: "Codex", IncludeGenerated: true, Window: 8000})
	if r.Report.Blocked != "" || len(r.Items) < 2 {
		t.Fatalf("bounded cloud context: %+v", r.Report)
	}
	first := r.Items[0]
	if !first.Generated || len(first.Coverage) != 0 || !strings.Contains(first.Text, "Earlier transfer briefing (quoted)") || !strings.Contains(first.Text, "> [hopsesh] Known cloud task") {
		t.Fatal("known generated briefing was lost or became authored work")
	}
	for _, it := range r.Items {
		if it.Node == nodes[1].ID && !it.Generated && it.Text == nodes[1].Text && len(it.Coverage) == 1 {
			return
		}
	}
	t.Fatal("actual request lost its text or authored revision")
}
