package codex

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/roeehrl/hopsesh/sdk/agent"
	"github.com/roeehrl/hopsesh/sdk/ir"
)

var _ agent.Reader = (*Module)(nil)

const (
	nativeFormat = "codex.rollout"
	maxOutput    = 256 << 10
)

// item is the item of an item_completed event (Codex's structured record of what ran).
type item struct {
	Type             string                     `json:"type"`
	Command          []string                   `json:"command"`
	CWD              string                     `json:"cwd"`
	AggregatedOutput string                     `json:"aggregated_output"`
	ExitCode         *int                       `json:"exit_code"`
	Status           string                     `json:"status"`
	Changes          map[string]json.RawMessage `json:"changes"`
	Server           string                     `json:"server"`
	Tool             string                     `json:"tool"`
	Arguments        json.RawMessage            `json:"arguments"`
	Result           json.RawMessage            `json:"result"`
	Kind             string                     `json:"kind"`
	Query            string                     `json:"query"`
	ID               string                     `json:"id"`
}

type change struct {
	Type        string `json:"type"`
	Content     string `json:"content"`
	UnifiedDiff string `json:"unified_diff"`
	MovePath    string `json:"move_path"`
}

type responseItem struct {
	Type    string `json:"type"`
	Role    string `json:"role"`
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	Name      string          `json:"name"`
	Arguments string          `json:"arguments"`
	Input     string          `json:"input"`
	CallID    string          `json:"call_id"`
	Output    json.RawMessage `json:"output"`
	Summary   []struct {
		Text string `json:"text"`
	} `json:"summary"`
	EncryptedContent string `json:"encrypted_content"`
}

// Read lifts a rollout into IR: the user's messages (not the context Codex injects), the
// agent's replies, and what ran, from Codex's structured item_completed records (falling
// back to raw function calls in rollouts that have none).
func (m *Module) Read(_ context.Context, h agent.Host, in agent.Install, s agent.Summary, from ir.Cursor) (ir.Segment, error) {
	f, err := h.FS().Open(s.Path)
	if err != nil {
		return ir.Segment{}, err
	}
	defer f.Close()
	recs, end, err := readLines(f)
	if err != nil {
		return ir.Segment{}, &agent.FormatError{Path: s.Path, Err: err}
	}
	if len(recs) == 0 || recs[0].Type != "session_meta" {
		return ir.Segment{}, &agent.FormatError{Path: s.Path, Line: 1, Err: fmt.Errorf("a rollout starts with session_meta")}
	}
	var mt meta
	_ = json.Unmarshal(recs[0].Payload, &mt)
	seg := ir.Segment{Header: ir.Header{Agent: string(id), Version: mt.CLIVersion, SessionID: mt.ID, CWD: mt.CWD, Title: s.Title, Created: parseTime(mt.Timestamp)}}
	if mt.Git != nil {
		seg.Header.GitBranch = mt.Git.Branch
	}
	structured := false
	for _, r := range recs {
		if r.Type == "event_msg" && strings.Contains(string(r.Payload), `"item_completed"`) {
			structured = true
			break
		}
	}
	for _, r := range recs {
		ts := parseTime(r.Timestamp)
		switch r.Type {
		case "turn_context":
			var tc struct {
				Model string `json:"model"`
			}
			if json.Unmarshal(r.Payload, &tc) == nil && tc.Model != "" {
				seg.Header.Model = tc.Model
			}
		case "compacted":
			var c struct {
				Message string `json:"message"`
			}
			if json.Unmarshal(r.Payload, &c) == nil && strings.TrimSpace(c.Message) != "" {
				seg.Nodes = append(seg.Nodes, ir.Node{Kind: ir.KindCompaction, Actor: ir.Agent, Time: ts, Text: c.Message})
			}
		case "response_item":
			seg.Nodes = append(seg.Nodes, fromResponse(r, ts, structured)...)
		case "event_msg":
			if structured {
				seg.Nodes = append(seg.Nodes, fromEvent(r, ts)...)
			}
		}
	}
	ir.Chain(seg.Nodes, "")
	seg.Cursor = ir.Cursor{Offset: end}
	if n := len(seg.Nodes); n > 0 {
		seg.Cursor.Head = seg.Nodes[n-1].ID
	}
	if from.Head == "" {
		return seg, nil
	}
	for i, n := range seg.Nodes {
		if n.ID == from.Head {
			seg.Nodes = seg.Nodes[i+1:]
			return seg, nil
		}
	}
	return ir.Segment{}, fmt.Errorf("%w: %s no longer continues from the point read before", agent.ErrDiverged, s.Key)
}

func readLines(r io.Reader) ([]line, int64, error) {
	br := bufio.NewReaderSize(r, 1<<20)
	var out []line
	var off int64
	for {
		b, err := br.ReadBytes('\n')
		if err == io.EOF {
			return out, off, nil // a partial last line is not consumed
		}
		if err != nil {
			return nil, 0, err
		}
		off += int64(len(b))
		var l line
		if json.Unmarshal(b, &l) == nil && l.Type != "" {
			out = append(out, l)
		}
	}
}

func fromResponse(r line, ts time.Time, structured bool) []ir.Node {
	var ri responseItem
	if json.Unmarshal(r.Payload, &ri) != nil {
		return nil
	}
	switch ri.Type {
	case "message":
		var parts []string
		for _, c := range ri.Content {
			if c.Type == "input_text" || c.Type == "output_text" {
				parts = append(parts, c.Text)
			}
		}
		text := strings.Join(parts, "\n")
		switch ri.Role {
		case "user":
			if t := userText(text); t != "" {
				return []ir.Node{{Kind: ir.KindMessage, Actor: ir.User, Time: ts, Text: t}}
			}
		case "assistant":
			if strings.TrimSpace(text) != "" {
				return []ir.Node{{Kind: ir.KindMessage, Actor: ir.Agent, Time: ts, Text: text}}
			}
		}
	case "reasoning":
		var summary []string
		for _, s := range ri.Summary {
			summary = append(summary, s.Text)
		}
		return []ir.Node{{Kind: ir.KindReasoning, Actor: ir.Agent, Time: ts, Text: strings.Join(summary, "\n"),
			Reasoning: &ir.Reasoning{Opaque: ri.EncryptedContent != ""}, Native: &ir.Native{Format: nativeFormat, Payload: r.Payload}}}
	case "function_call", "custom_tool_call":
		if structured {
			return nil // the item_completed records say the same, structured
		}
		return []ir.Node{{Kind: ir.KindToolCall, Actor: ir.Agent, Time: ts, Tool: legacyCall(ri)}}
	case "function_call_output", "custom_tool_call_output":
		if structured {
			return nil
		}
		return []ir.Node{{Kind: ir.KindToolResult, Actor: ir.User, Time: ts, Result: &ir.ToolResult{CallID: ri.CallID, Status: ir.StatusCompleted, Output: outputText(ri.Output)}}}
	}
	return nil
}

// legacyCall maps a raw function call (rollouts without item_completed records).
func legacyCall(ri responseItem) *ir.ToolCall {
	input := json.RawMessage(ri.Arguments)
	if ri.Type == "custom_tool_call" {
		input, _ = json.Marshal(ri.Input)
	}
	c := &ir.ToolCall{CallID: ri.CallID, Name: ri.Name, Input: input, Kind: ir.ToolOther}
	switch ri.Name {
	case "shell", "exec_command", "local_shell":
		var a struct {
			Command any    `json:"command"`
			Cmd     string `json:"cmd"`
			Workdir string `json:"workdir"`
		}
		_ = json.Unmarshal([]byte(ri.Arguments), &a)
		cmd := a.Cmd
		switch v := a.Command.(type) {
		case string:
			cmd = v
		case []any:
			if len(v) > 0 {
				cmd, _ = v[len(v)-1].(string)
			}
		}
		c.Kind, c.Shell = ir.ToolExecute, &ir.Shell{Command: cmd, Dir: a.Workdir}
	case "apply_patch":
		c.Kind, c.Edit = ir.ToolEdit, &ir.Edit{Diff: ri.Input}
	case "update_plan":
		c.Kind = ir.ToolPlan
	}
	return c
}

// outputText reads a function call's output: plain text, or (older shells) a JSON
// object, possibly itself encoded as a string, with the text under "output".
func outputText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		raw = json.RawMessage(s)
	}
	var o struct {
		Output *string `json:"output"`
	}
	if json.Unmarshal(raw, &o) == nil && o.Output != nil {
		return clipOutput(*o.Output)
	}
	return clipOutput(s)
}

// fromEvent maps an item_completed record to a tool call and its result.
func fromEvent(r line, ts time.Time) []ir.Node {
	var e struct {
		Type string `json:"type"`
		Item item   `json:"item"`
	}
	if json.Unmarshal(r.Payload, &e) != nil || e.Type != "item_completed" {
		return nil
	}
	it := e.Item
	status := ir.StatusCompleted
	if it.Status == "failed" {
		status = ir.StatusFailed
	}
	pair := func(call ir.ToolCall, res ir.ToolResult) []ir.Node {
		call.CallID, res.CallID = it.ID, it.ID
		return []ir.Node{
			{Kind: ir.KindToolCall, Actor: ir.Agent, Time: ts, Tool: &call},
			{Kind: ir.KindToolResult, Actor: ir.User, Time: ts, Result: &res},
		}
	}
	switch it.Type {
	case "CommandExecution":
		cmd := ""
		if n := len(it.Command); n > 0 {
			cmd = it.Command[n-1] // strips the "/bin/zsh -lc" wrapper
		}
		input, _ := json.Marshal(map[string]any{"command": it.Command})
		res := ir.ToolResult{Status: status, ExitCode: it.ExitCode, Output: clipOutput(it.AggregatedOutput)}
		if len(it.AggregatedOutput) > maxOutput {
			res.OutputBytes = int64(len(it.AggregatedOutput))
		}
		return pair(ir.ToolCall{Name: "shell", Input: input, Kind: ir.ToolExecute, Shell: &ir.Shell{Command: cmd, Dir: strings.TrimPrefix(it.CWD, "file://")}}, res)
	case "FileChange":
		var out []ir.Node
		paths := make([]string, 0, len(it.Changes))
		for p := range it.Changes {
			paths = append(paths, p)
		}
		sort.Strings(paths) // a map: order must not depend on the run
		for _, p := range paths {
			raw := it.Changes[p]
			var ch change
			_ = json.Unmarshal(raw, &ch)
			input, _ := json.Marshal(map[string]any{"path": p, "change": raw})
			call := ir.ToolCall{Name: "apply_patch", Input: input, Kind: ir.ToolEdit}
			switch {
			case ch.Type == "delete":
				call.Kind, call.Path = ir.ToolDelete, p
			case ch.Content != "" && ch.UnifiedDiff == "":
				call.Write = &ir.Write{Path: p, Content: ch.Content}
			default:
				call.Edit = &ir.Edit{Path: p, Diff: ch.UnifiedDiff}
			}
			out = append(out, pair(call, ir.ToolResult{Status: status})...)
		}
		return out
	case "McpToolCall":
		input := it.Arguments
		return pair(ir.ToolCall{Name: "mcp__" + it.Server + "__" + it.Tool, Input: input, Kind: ir.ToolOther, MCP: &ir.MCP{Server: it.Server, Tool: it.Tool}},
			ir.ToolResult{Status: status, Output: clipOutput(string(it.Result))})
	case "Extension":
		if it.Kind == "web.search" {
			input, _ := json.Marshal(map[string]string{"query": it.Query})
			return pair(ir.ToolCall{Name: "web_search", Input: input, Kind: ir.ToolFetch, Search: &ir.Search{Pattern: it.Query}}, ir.ToolResult{Status: status})
		}
	}
	return nil
}

func clipOutput(s string) string {
	if len(s) > maxOutput {
		return s[:maxOutput]
	}
	return s
}

func parseTime(s string) time.Time {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}
	}
	return t.UTC()
}
