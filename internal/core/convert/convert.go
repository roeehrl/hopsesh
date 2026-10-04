// Package convert renders a conversation read from one agent for another agent's writer:
// what the receiving agent should see (its own words and the user's as messages, the
// other agent's tool activity as clearly labelled text), within a budget, with a report of
// everything left out and a briefing at the end. Brief renders a briefing alone, for a
// target hopsesh cannot write to (a cloud session's first prompt). It never calls a model.
package convert

import (
	"bytes"
	"fmt"
	"strings"
	"time"

	"github.com/roeehrl/hopsesh/internal/core/rewrite"
	"github.com/roeehrl/hopsesh/sdk/agent"
	"github.com/roeehrl/hopsesh/sdk/ir"
)

// Fidelity is how much of the source the target gets.
type Fidelity string

const (
	// History: the whole conversation, tool activity as text (oldest steps summarised
	// when over budget).
	History Fidelity = "history"
	// Note: only the briefing, in a new session.
	Note Fidelity = "note"
)

// BudgetShare is the share of the target's context the converted history may use.
const BudgetShare = 0.30

// outputMax bounds one tool output in the rendered history.
const outputMax = 4000

// Request is one rendering.
type Request struct {
	Nodes      []ir.Node
	From       string // the source agent's name ("Claude Code")
	To         string // the target agent's name ("Codex")
	Fidelity   Fidelity
	Native     bool            // render exact tool calls natively (the target writer supports it)
	Window     int             // the target's context, in tokens
	Mappings   []agent.Mapping // source paths → target paths
	Briefing   Briefing
	Redact     func([]byte) ([]byte, int)
	SkipBefore int // render only nodes from this index (round-trip deltas); earlier ones are context
}

// Result is a rendering.
type Result struct {
	Items  []ir.Item `json:"-"`
	Report Report    `json:"report"`
}

// Report is what a conversion keeps and leaves out.
type Report struct {
	Fidelity    Fidelity `json:"fidelity"`
	Budget      int      `json:"budgetTokens"`
	Used        int      `json:"usedTokens"`
	Messages    int      `json:"messages"`
	ToolCalls   int      `json:"toolCalls"`
	NativeCalls int      `json:"nativeCalls,omitempty"`
	Reasoning   int      `json:"reasoningDropped"`
	Attachments int      `json:"attachmentsAsPlaceholders,omitempty"`
	Truncated   int      `json:"outputsShortened"`
	Summarised  int      `json:"stepsSummarised"`
	Redactions  int      `json:"redactions,omitempty"`
	PathsMapped int      `json:"pathsMapped"`
	Summary     string   `json:"summary"`
}

// Briefing is what the receiving agent is told at the end of the history.
type Briefing struct {
	FromVersion string
	SourceID    string
	SourceLoc   string
	TargetLoc   string
	When        time.Time
	Branch      string
	Head        string // the commit at transfer time
	Dirty       int
	Plan        []ir.PlanEntry
	Missing     []string // differences in instructions, skills, MCP servers
	ToolNames   string   // the target's own tool names, to use instead of the history's
	Note        string   // a handoff note the source agent wrote, if any
	Rules       []Rules  // the user's instructions for every project, carried when asked
	// For a cloud briefing (Brief with BriefCloud):
	Title       string   // the session's title
	Unpushed    int      // commits the cloud gets that were not on the remote
	Withheld    []string // files that stay on the machine (credential-like, or not chosen)
	NotCarried  []string // anything else the cloud does not get ("the user's personal CLAUDE.md")
	HistoryFile string   // the conversation committed on the branch (".hopsesh/handoff.md"), if it is
	// Checkout: the cloud may start on another branch (its driver cannot choose one), so the
	// briefing asks it to check out Branch of Repo (host/owner/repo) first.
	Checkout bool
	Repo     string
}

// Rules are the text of one global instruction file.
type Rules struct {
	File string
	Text string
}

// Render turns nodes into items for the target writer.
func Render(r Request) Result {
	res := Result{Report: Report{Fidelity: r.Fidelity, Budget: int(float64(r.Window) * BudgetShare)}}
	if r.Window == 0 {
		res.Report.Budget = 200_000
	}
	nodes := r.Nodes
	if len(r.Briefing.Plan) == 0 {
		r.Briefing.Plan = latestPlan(nodes)
	}
	var items []ir.Item
	if r.Fidelity != Note {
		items = res.history(r, nodes[min(r.SkipBefore, len(nodes)):])
	}
	brief := briefing(r, &res.Report)
	now := time.Now().UTC()
	if k := len(items); k > 0 && items[k-1].Role == ir.RoleUser && items[k-1].Tool == nil {
		items[k-1].Text += "\n\n" + brief // the history ended on the user: keep roles alternating
	} else {
		items = append(items, ir.Item{Node: "hopsesh/briefing", Role: ir.RoleUser, Time: now, Text: brief})
	}
	items = append(items, ir.Item{Node: "hopsesh/ack", Role: ir.RoleAgent, Time: now, Text: "Understood. I'll check the working tree first, then continue from where the conversation stopped."})
	res.Items = items
	res.Report.Used = tokens(items)
	res.Report.Summary = summary(res.Report, r)
	return res
}

func (res *Result) history(r Request, nodes []ir.Node) []ir.Item {
	results := map[string]*ir.ToolResult{}
	for _, n := range nodes {
		if n.Kind == ir.KindToolResult && n.Result != nil {
			results[n.Result.CallID] = n.Result
		}
	}
	var items []ir.Item
	add := func(role ir.Role, node ir.NodeID, ts time.Time, text string) {
		text = res.mapText(r, text)
		if strings.TrimSpace(text) == "" {
			return
		}
		if k := len(items); k > 0 && items[k-1].Role == role && items[k-1].Tool == nil {
			items[k-1].Text += "\n\n" + text // keep roles alternating
			return
		}
		items = append(items, ir.Item{Node: node, Role: role, Time: ts, Text: text})
	}
	for _, n := range nodes {
		switch n.Kind {
		case ir.KindMessage:
			res.Report.Messages++
			role := ir.RoleAgent
			if n.Actor == ir.User {
				role = ir.RoleUser
			}
			add(role, n.ID, n.Time, n.Text)
		case ir.KindCompaction:
			add(ir.RoleUser, n.ID, n.Time, "[earlier conversation, as "+r.From+" summarised it]\n"+n.Text)
		case ir.KindReasoning:
			res.Report.Reasoning++
		case ir.KindAttachment:
			res.Report.Attachments++
			add(ir.RoleUser, n.ID, n.Time, fmt.Sprintf("[an attachment (%s) was here; it was not carried over]", n.Attachment.MIME))
		case ir.KindToolCall:
			res.Report.ToolCalls++
			out := results[n.Tool.CallID]
			if r.Native && nativeable(n.Tool) && out != nil {
				res.Report.NativeCalls++
				call, result := *n.Tool, *out
				if call.Shell != nil {
					sh := *call.Shell
					sh.Command = res.mapText(r, sh.Command)
					call.Shell = &sh
				}
				result.Output = res.mapText(r, shorten(result.Output, &res.Report))
				items = append(items, ir.Item{Node: n.ID, Role: ir.RoleAgent, Time: n.Time, Tool: &ir.ToolPair{Call: call, Result: result}})
				continue
			}
			add(ir.RoleAgent, n.ID, n.Time, flatten(r.From, n.Tool, out, &res.Report))
		}
	}
	return res.budget(r, items)
}

// budget keeps the newest items and summarises the oldest when over budget.
func (res *Result) budget(r Request, items []ir.Item) []ir.Item {
	limit := res.Report.Budget
	if tokens(items) <= limit {
		return items
	}
	keep := len(items)
	used := 0
	for keep > 0 && used+tokens(items[keep-1:keep]) <= limit*9/10 {
		keep--
		used += tokens(items[keep : keep+1])
	}
	for keep < len(items) && items[keep].Role != ir.RoleUser {
		keep++ // start the kept part on a user turn
	}
	old := items[:keep]
	res.Report.Summarised = len(old)
	kept := append([]ir.Item(nil), items[keep:]...)
	d := digest(r.From, old)
	if len(kept) > 0 { // the kept part starts on a user turn: the digest leads it
		kept[0].Text = d + "\n" + kept[0].Text
		return kept
	}
	return []ir.Item{{Node: "hopsesh/digest", Role: ir.RoleUser, Time: old[0].Time, Text: d}}
}

// digest summarises dropped items deterministically: the user's requests and what ran.
func digest(from string, items []ir.Item) string {
	var b strings.Builder
	fmt.Fprintf(&b, "[hopsesh: the first %d steps of this conversation with %s are summarised here to fit]\n", len(items), from)
	n := 0
	for _, it := range items {
		if it.Role == ir.RoleUser && n < 30 {
			fmt.Fprintf(&b, "- the user asked: %s\n", clip(it.Text, 200))
			n++
		}
	}
	return b.String()
}

// nativeable: only calls a target agent has an exact equivalent for.
func nativeable(c *ir.ToolCall) bool { return c.Shell != nil }

// flatten renders a tool call and its result in one grammar for every target.
func flatten(from string, c *ir.ToolCall, out *ir.ToolResult, rep *Report) string {
	var b strings.Builder
	head := fmt.Sprintf("[prior agent · %s · %s", from, c.Kind)
	if out != nil && out.ExitCode != nil {
		head += fmt.Sprintf(" · exit %d", *out.ExitCode)
	} else if out != nil && out.Status == ir.StatusFailed {
		head += " · failed"
	}
	b.WriteString(head + "]\n")
	switch {
	case c.Shell != nil:
		b.WriteString("$ " + c.Shell.Command + "\n")
	case c.Write != nil:
		fmt.Fprintf(&b, "wrote %s (%d lines)\n", c.Write.Path, strings.Count(c.Write.Content, "\n")+1)
	case c.Edit != nil && c.Edit.Diff != "":
		b.WriteString("edited " + c.Edit.Path + "\n" + shorten(c.Edit.Diff, rep) + "\n")
	case c.Edit != nil:
		fmt.Fprintf(&b, "edited %s\n", c.Edit.Path)
		if c.Edit.Old != "" || c.Edit.New != "" {
			b.WriteString(shorten("- "+strings.ReplaceAll(c.Edit.Old, "\n", "\n- ")+"\n+ "+strings.ReplaceAll(c.Edit.New, "\n", "\n+ "), rep) + "\n")
		}
	case c.Path != "":
		b.WriteString(string(c.Kind) + " " + c.Path + "\n")
	case c.Search != nil:
		b.WriteString("search " + c.Search.Pattern + "\n")
	case c.URL != "":
		b.WriteString("fetch " + c.URL + "\n")
	case c.Subagent != nil:
		b.WriteString("sub-agent: " + c.Subagent.Description + "\n")
	default:
		b.WriteString(c.Name + " " + clip(string(c.Input), 400) + "\n")
	}
	if out != nil && strings.TrimSpace(out.Output) != "" {
		lines := strings.Count(strings.TrimRight(out.Output, "\n"), "\n") + 1
		fmt.Fprintf(&b, "[output · %d line(s)]\n%s\n[/output]", lines, strings.TrimRight(shorten(out.Output, rep), "\n"))
	}
	return b.String()
}

// shorten keeps the head and tail of a long output.
func shorten(s string, rep *Report) string {
	if len(s) <= outputMax {
		return s
	}
	rep.Truncated++
	half := outputMax / 2
	return s[:half] + fmt.Sprintf("\n[… %d bytes left out …]\n", len(s)-outputMax) + s[len(s)-half:]
}

func (res *Result) mapText(r Request, s string) string {
	if len(r.Mappings) > 0 {
		var out bytes.Buffer
		if n, err := rewrite.Text(strings.NewReader(s), &out, r.Mappings); err == nil {
			res.Report.PathsMapped += n
			s = out.String()
		}
	}
	if r.Redact != nil {
		b, n := r.Redact([]byte(s))
		res.Report.Redactions += n
		s = string(b)
	}
	return s
}

// briefing is the last message before the receiving agent's first turn.
func briefing(r Request, rep *Report) string {
	bf := r.Briefing
	var b strings.Builder
	fmt.Fprintf(&b, agent.NotePrefix+"This conversation was moved from %s", r.From)
	if bf.FromVersion != "" {
		fmt.Fprintf(&b, " %s", bf.FromVersion)
	}
	if bf.SourceID != "" {
		fmt.Fprintf(&b, " (session %s)", shortID(bf.SourceID))
	}
	if bf.SourceLoc != "" {
		fmt.Fprintf(&b, " on %s", bf.SourceLoc)
	}
	fmt.Fprintf(&b, " to %s", r.To)
	if bf.TargetLoc != "" {
		fmt.Fprintf(&b, " on %s", bf.TargetLoc)
	}
	if !bf.When.IsZero() {
		fmt.Fprintf(&b, " at %s", bf.When.UTC().Format("2006-01-02 15:04 UTC"))
	}
	b.WriteString(".\n")
	if r.Fidelity != Note {
		fmt.Fprintf(&b, "The history above is a text rendering: lines tagged [prior agent · %s · …] are %s's tool calls, not yours", r.From, r.From)
		if rep.Truncated > 0 {
			fmt.Fprintf(&b, "; %d long output(s) were shortened", rep.Truncated)
		}
		if rep.Summarised > 0 {
			fmt.Fprintf(&b, "; the first %d steps are summarised", rep.Summarised)
		}
		if rep.Reasoning > 0 {
			fmt.Fprintf(&b, "; its hidden reasoning was not carried")
		}
		b.WriteString(".\n")
	}
	if bf.ToolNames != "" {
		fmt.Fprintf(&b, "Use your own tools (%s); do not call the tool names in the history.\n", bf.ToolNames)
	} else {
		b.WriteString("Use your own tools; do not call the tool names in the history.\n")
	}
	b.WriteString("Verify first: run git status, git log -3 --oneline and git diff --stat.")
	if bf.Head != "" {
		fmt.Fprintf(&b, " At transfer time HEAD was %s", bf.Head)
		if bf.Branch != "" {
			fmt.Fprintf(&b, " on %s", bf.Branch)
		}
		if bf.Dirty > 0 {
			fmt.Fprintf(&b, " with %d modified file(s)", bf.Dirty)
		}
		b.WriteString(".")
	}
	b.WriteString(" The working tree is the source of truth.\n")
	if len(bf.Plan) > 0 {
		b.WriteString("Open plan (recreate it with your own plan tool):\n")
		for _, p := range bf.Plan {
			fmt.Fprintf(&b, "- [%s] %s\n", p.Status, p.Content)
		}
	}
	for _, m := range bf.Missing {
		b.WriteString("Note: " + m + "\n")
	}
	for _, rl := range bf.Rules {
		fmt.Fprintf(&b, "The user's standing instructions for %s (%s), carried over at their request:\n%s\n", r.From, rl.File, rl.Text)
	}
	if bf.Note != "" {
		fmt.Fprintf(&b, "%s's handoff note:\n%s\n", r.From, bf.Note)
	}
	b.WriteString("Continue the task unless the user redirects; do not redo finished work.")
	return b.String()
}

func summary(rep Report, r Request) string {
	var parts []string
	if r.Fidelity == Note {
		parts = append(parts, "only a briefing (no history)")
	} else {
		parts = append(parts, fmt.Sprintf("kept %d message(s)", rep.Messages))
		if rep.ToolCalls > 0 {
			parts = append(parts, fmt.Sprintf("%d tool call(s) as text", rep.ToolCalls-rep.NativeCalls))
		}
		if rep.NativeCalls > 0 {
			parts = append(parts, fmt.Sprintf("%d as %s's own tool calls", rep.NativeCalls, r.To))
		}
	}
	if rep.Reasoning > 0 {
		parts = append(parts, fmt.Sprintf("%d reasoning block(s) dropped (they belong to %s)", rep.Reasoning, r.From))
	}
	if rep.Truncated > 0 {
		parts = append(parts, fmt.Sprintf("%d output(s) shortened to %d KB", rep.Truncated, outputMax/1000))
	}
	if rep.Summarised > 0 {
		parts = append(parts, fmt.Sprintf("the first %d step(s) summarised to fit %d tokens", rep.Summarised, rep.Budget))
	}
	if rep.Attachments > 0 {
		parts = append(parts, fmt.Sprintf("%d attachment(s) left out", rep.Attachments))
	}
	return strings.Join(parts, "; ")
}

// tokens estimates tokens as characters / 4.
func tokens(items []ir.Item) int {
	n := 0
	for _, it := range items {
		n += len(it.Text)
		if it.Tool != nil {
			n += len(it.Tool.Call.Input) + len(it.Tool.Result.Output)
		}
	}
	return n / 4
}

func clip(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len([]rune(s)) <= n {
		return s
	}
	return string([]rune(s)[:n]) + "…"
}

func shortID(s string) string {
	if len(s) > 8 {
		return s[:8]
	}
	return s
}
