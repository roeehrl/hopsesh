package claude

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/roeehrl/hopsesh/sdk/agent"
	"github.com/roeehrl/hopsesh/sdk/ir"
)

var _ agent.Reader = (*Module)(nil)

// nativeFormat names Claude Code's transcript records in the IR.
const nativeFormat = "claude.jsonl"

// chained is a transcript record that belongs to the conversation chain.
type chained struct {
	Type              string                 `json:"type"`
	UUID              string                 `json:"uuid"`
	ParentUUID        *string                `json:"parentUuid"`
	LogicalParentUUID string                 `json:"logicalParentUuid"`
	IsSidechain       bool                   `json:"isSidechain"`
	IsMeta            bool                   `json:"isMeta"`
	IsCompactSummary  bool                   `json:"isCompactSummary"`
	Timestamp         string                 `json:"timestamp"`
	CWD               string                 `json:"cwd"`
	GitBranch         string                 `json:"gitBranch"`
	Version           string                 `json:"version"`
	SessionID         string                 `json:"sessionId"`
	Origin            *struct{ Kind string } `json:"origin"`
	Message           *struct {
		Role    string          `json:"role"`
		Model   string          `json:"model"`
		Content json.RawMessage `json:"content"`
		Usage   *contextUsage   `json:"usage"`
	} `json:"message"`
	// metadata records
	CustomTitle  string `json:"customTitle"`
	AITitle      string `json:"aiTitle"`
	LeafUUID     string `json:"leafUuid"`
	RelocatedCWD string `json:"relocatedCwd"`
}

// The last completed request reports the active prompt, including cached input.
// Iteration details repeat these totals and must not be counted again.
type contextUsage struct {
	Input       int `json:"input_tokens"`
	CacheRead   int `json:"cache_read_input_tokens"`
	CacheCreate int `json:"cache_creation_input_tokens"`
	Output      int `json:"output_tokens"`
}

type block struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	Content   json.RawMessage `json:"content"`
	IsError   bool            `json:"is_error"`
	Source    *struct {
		Type      string `json:"type"`
		MediaType string `json:"media_type"`
		Data      string `json:"data"`
	} `json:"source"`
}

// Read lifts the transcript's active branch (from the leaf Claude Code would resume back
// to the start) into IR. From a cursor it returns only the nodes after cursor.Head, which
// must still be on the branch (ErrDiverged otherwise).
func (m *Module) Read(ctx context.Context, h agent.Host, in agent.Install, s agent.Summary, from ir.Cursor) (ir.Segment, error) {
	f, err := h.FS().Open(s.Path)
	if err != nil {
		return ir.Segment{}, err
	}
	defer f.Close()
	recs, end, err := readRecords(&ir.BoundedReader{Context: ctx, Reader: f})
	if err != nil {
		return ir.Segment{}, &agent.FormatError{Path: s.Path, Err: err}
	}
	branch := activeBranch(recs)
	seg := ir.Segment{Header: ir.Header{Agent: string(id), SessionID: string(s.Key.Session), CWD: s.CWD, Title: s.Title, GitBranch: s.GitBranch}}
	for _, i := range branch {
		r := recs[i]
		if seg.Header.Created.IsZero() {
			seg.Header.Created = parseTime(r.Timestamp)
		}
		if r.Version != "" {
			seg.Header.Version = r.Version
		}
		if r.Message != nil && r.Message.Model != "" && r.Message.Model != "<synthetic>" {
			seg.Header.Model = r.Message.Model
		}
		lifted := nodes(r)
		for j := range lifted {
			if lifted[j].Native == nil {
				lifted[j].Native = &ir.Native{Format: nativeFormat}
			}
			lifted[j].Native.Anchor = fmt.Sprintf("%s/%d", r.UUID, j)
		}
		seg.Nodes = append(seg.Nodes, lifted...)
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

// readRecords decodes every complete line. end is the offset after the last complete
// line (a line still being written is left for the next read).
func readRecords(r io.Reader) ([]chained, int64, error) {
	br := bufio.NewReaderSize(r, 1<<20)
	var recs []chained
	var off int64
	for {
		line, err := br.ReadBytes('\n')
		if err == io.EOF {
			return recs, off, nil // a partial last line is not consumed
		}
		if err != nil {
			return nil, 0, err
		}
		off += int64(len(line))
		var c chained
		if json.Unmarshal(line, &c) != nil || c.Type == "" {
			continue
		}
		recs = append(recs, c)
	}
}

func isChain(t string) bool {
	return t == "user" || t == "assistant" || t == "system" || t == "attachment"
}

// activeBranch returns record indexes from the root to the leaf Claude Code resumes: the
// last last-prompt record's leaf, else the last non-sidechain message.
func activeBranch(recs []chained) []int {
	byUUID := map[string]int{}
	leaf := -1
	for i, r := range recs {
		if isChain(r.Type) && r.UUID != "" {
			byUUID[r.UUID] = i
			if !r.IsSidechain && (r.Type == "user" || r.Type == "assistant") {
				leaf = i
			}
		}
	}
	for i := len(recs) - 1; i >= 0; i-- {
		if recs[i].Type == "last-prompt" && recs[i].LeafUUID != "" {
			if j, ok := byUUID[recs[i].LeafUUID]; ok {
				leaf = j
			}
			break
		}
	}
	var out []int
	seen := map[int]bool{}
	for i := leaf; i >= 0 && !seen[i]; {
		seen[i] = true
		out = append(out, i)
		r := recs[i]
		parent := ""
		if r.ParentUUID != nil {
			parent = *r.ParentUUID
		} else if r.LogicalParentUUID != "" {
			parent = r.LogicalParentUUID // across a compaction boundary
		}
		j, ok := byUUID[parent]
		if !ok {
			break
		}
		i = j
	}
	for a, b := 0, len(out)-1; a < b; a, b = a+1, b-1 {
		out[a], out[b] = out[b], out[a]
	}
	return out
}

// nodes turns one chained record into IR nodes.
func nodes(r chained) []ir.Node {
	if r.Message == nil || r.IsMeta || r.IsSidechain {
		return nil
	}
	ts := parseTime(r.Timestamp)
	if r.Type == "user" && r.Origin != nil && r.Origin.Kind != "" && r.Origin.Kind != "human" {
		return nil // task notifications, peer messages: not the user
	}
	var s string
	if json.Unmarshal(r.Message.Content, &s) == nil {
		switch {
		case r.Type == "user" && r.IsCompactSummary:
			return []ir.Node{{Kind: ir.KindCompaction, Actor: ir.User, Time: ts, Text: s}}
		case r.Type == "user":
			return []ir.Node{{Kind: ir.KindMessage, Actor: ir.User, Time: ts, Text: s}}
		case r.Type == "assistant":
			return []ir.Node{{Kind: ir.KindMessage, Actor: ir.Agent, Time: ts, Text: s}}
		}
		return nil
	}
	var blocks []block
	if json.Unmarshal(r.Message.Content, &blocks) != nil {
		return nil
	}
	actor := ir.User
	if r.Type == "assistant" {
		actor = ir.Agent
	}
	var out []ir.Node
	var text []string
	flush := func() {
		if len(text) > 0 {
			kind := ir.KindMessage
			if r.IsCompactSummary {
				kind = ir.KindCompaction
			}
			out = append(out, ir.Node{Kind: kind, Actor: actor, Time: ts, Text: strings.Join(text, "\n\n")})
			text = nil
		}
	}
	for _, b := range blocks {
		switch b.Type {
		case "text":
			if strings.TrimSpace(b.Text) != "" {
				text = append(text, b.Text)
			}
		case "thinking", "redacted_thinking":
			flush()
			nb, _ := json.Marshal(b)
			out = append(out, ir.Node{Kind: ir.KindReasoning, Actor: ir.Agent, Time: ts, Reasoning: &ir.Reasoning{Opaque: true},
				Native: &ir.Native{Format: nativeFormat, Payload: nb}})
		case "tool_use":
			flush()
			call := toolCall(b)
			out = append(out, ir.Node{Kind: ir.KindToolCall, Actor: ir.Agent, Time: ts, Tool: &call})
			if call.Kind == ir.ToolPlan {
				if plan := todos(b.Input); len(plan) > 0 {
					out = append(out, ir.Node{Kind: ir.KindPlan, Actor: ir.Agent, Time: ts, Plan: plan})
				}
			}
		case "tool_result":
			flush()
			res := ir.ToolResult{CallID: b.ToolUseID, Status: ir.StatusCompleted, Output: resultText(b.Content)}
			if b.IsError {
				res.Status = ir.StatusFailed
			}
			out = append(out, ir.Node{Kind: ir.KindToolResult, Actor: ir.User, Time: ts, Result: &res})
		case "image":
			flush()
			if b.Source != nil && b.Source.Type == "base64" {
				data, err := base64.StdEncoding.DecodeString(b.Source.Data)
				if err == nil {
					out = append(out, ir.Node{Kind: ir.KindAttachment, Actor: actor, Time: ts, Attachment: &ir.Attachment{MIME: b.Source.MediaType, Data: data}})
				}
			}
		}
	}
	flush()
	return out
}

func resultText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var blocks []block
	if json.Unmarshal(raw, &blocks) != nil {
		return ""
	}
	var parts []string
	for _, b := range blocks {
		if b.Type == "text" {
			parts = append(parts, b.Text)
		}
	}
	return strings.Join(parts, "\n")
}

// toolCall maps a Claude Code tool to its intent.
func toolCall(b block) ir.ToolCall {
	c := ir.ToolCall{CallID: b.ID, Name: b.Name, Input: b.Input, Kind: ir.ToolOther}
	var in struct {
		Command     string `json:"command"`
		FilePath    string `json:"file_path"`
		Notebook    string `json:"notebook_path"`
		OldString   string `json:"old_string"`
		NewString   string `json:"new_string"`
		Content     string `json:"content"`
		Pattern     string `json:"pattern"`
		Path        string `json:"path"`
		URL         string `json:"url"`
		Query       string `json:"query"`
		Description string `json:"description"`
		Prompt      string `json:"prompt"`
	}
	_ = json.Unmarshal(b.Input, &in)
	switch b.Name {
	case "Bash":
		c.Kind, c.Shell = ir.ToolExecute, &ir.Shell{Command: in.Command}
	case "Read":
		c.Kind, c.Path = ir.ToolRead, in.FilePath
	case "Edit":
		c.Kind, c.Edit = ir.ToolEdit, &ir.Edit{Path: in.FilePath, Old: in.OldString, New: in.NewString}
	case "MultiEdit":
		c.Kind, c.Edit = ir.ToolEdit, &ir.Edit{Path: in.FilePath}
	case "NotebookEdit":
		c.Kind, c.Edit = ir.ToolEdit, &ir.Edit{Path: in.Notebook}
	case "Write":
		c.Kind, c.Write = ir.ToolEdit, &ir.Write{Path: in.FilePath, Content: in.Content}
	case "Glob", "Grep":
		c.Kind, c.Search = ir.ToolSearch, &ir.Search{Pattern: in.Pattern, Path: in.Path}
	case "WebFetch":
		c.Kind, c.URL = ir.ToolFetch, in.URL
	case "WebSearch":
		c.Kind, c.Search = ir.ToolFetch, &ir.Search{Pattern: in.Query}
	case "TodoWrite", "TaskCreate", "TaskUpdate":
		c.Kind = ir.ToolPlan
	case "Task", "Agent":
		c.Kind, c.Subagent = ir.ToolSubagent, &ir.Subagent{Description: in.Description, Prompt: in.Prompt}
	default:
		if rest, ok := strings.CutPrefix(b.Name, "mcp__"); ok {
			server, tool, _ := strings.Cut(rest, "__")
			c.MCP = &ir.MCP{Server: server, Tool: tool}
		}
	}
	return c
}

func todos(raw json.RawMessage) []ir.PlanEntry {
	var in struct {
		Todos []struct {
			Content string `json:"content"`
			Status  string `json:"status"`
		} `json:"todos"`
	}
	if json.Unmarshal(raw, &in) != nil {
		return nil
	}
	var out []ir.PlanEntry
	for _, t := range in.Todos {
		out = append(out, ir.PlanEntry{Content: t.Content, Status: t.Status})
	}
	return out
}

func parseTime(s string) time.Time {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}
	}
	return t.UTC()
}
