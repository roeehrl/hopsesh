package opencode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"strings"

	"github.com/roeehrl/hopsesh/sdk/agent"
	"github.com/roeehrl/hopsesh/sdk/ir"
)

// nativeFormat names OpenCode 2.x records in the IR.
const nativeFormat = "opencode.v2"

// Read lifts a session into IR, from the database, else from OpenCode's export, or where
// nothing runs (a peer's package, staged files) from the session's JSON lines at s.Path. Its cursor counts settled messages, the same in every mode, and its head is the
// last node read.
func (m *Module) Read(ctx context.Context, h agent.Host, in agent.Install, s agent.Summary, from ir.Cursor) (ir.Segment, error) {
	sess, msgs, err := m.readSession(ctx, h, in, s)
	if err != nil {
		return ir.Segment{}, err
	}
	return segment(sess, msgs, from)
}

// readSession reads a session's settled messages from wherever they are.
func (m *Module) readSession(ctx context.Context, h agent.Host, in agent.Install, s agent.Summary) (session, []message, error) {
	if r, ok, err := openSQL(ctx, h, in); ok {
		var (
			sess session
			msgs []message
		)
		if err == nil {
			sess, msgs, err = r.transcript(ctx, s.Key.Session)
		}
		if !readUnsupported(err) {
			return sess, msgs, err
		}
	}
	sess, msgs, err := exportSession(ctx, h, probeService(ctx, h), s.Key.Session)
	if err == nil || !notRun(err) || s.Path == "" {
		return sess, msgs, err
	}
	// Nothing runs here (a peer's package, staged files): the file hopsesh placed.
	b, ferr := agent.ReadNative(ctx, h.FS(), s.Path)
	if ferr != nil {
		if errors.Is(ferr, fs.ErrNotExist) {
			return session{}, nil, err
		}
		return session{}, nil, ferr
	}
	return decodeJSONL(s.Path, b)
}

// segment lifts messages to IR and returns what follows from: its Offset is a count of
// settled messages, and its Head must be the last node of those (else ErrDiverged).
func segment(sess session, msgs []message, from ir.Cursor) (ir.Segment, error) {
	seg := ir.Segment{Header: ir.Header{Agent: string(id), Version: sess.Version, SessionID: sess.ID, CWD: sess.Directory, Created: millis(sess.Created)}}
	if !untitled(sess) {
		seg.Header.Title = sess.Title
	}
	var (
		nodes []ir.Node
		owner []int // the message each node came from
	)
	for i, msg := range msgs {
		if msg.Type == "assistant" && msg.Model.ID != "" {
			seg.Header.Model = msg.Model.String()
		}
		for _, n := range lift(msg) {
			nodes = append(nodes, n)
			owner = append(owner, i)
		}
	}
	ir.Chain(nodes, "")
	seg.Cursor = ir.Cursor{Offset: int64(len(msgs))}
	if len(nodes) > 0 {
		seg.Cursor.Head = nodes[len(nodes)-1].ID
	}
	if from.Offset == 0 && from.Head == "" {
		seg.Nodes = nodes
		return seg, nil
	}
	if from.Offset == 0 { // a cursor that names only its node
		for i, n := range nodes {
			if n.ID == from.Head {
				seg.Nodes = nodes[i+1:]
				return seg, nil
			}
		}
		return ir.Segment{}, fmt.Errorf("%w: the OpenCode session %s no longer continues from the point read before", agent.ErrDiverged, sess.ID)
	}
	if from.Offset > int64(len(msgs)) {
		return ir.Segment{}, fmt.Errorf("%w: the OpenCode session %s has fewer messages than were read before", agent.ErrDiverged, sess.ID)
	}
	start := len(nodes)
	var head ir.NodeID
	for i := range nodes {
		if int64(owner[i]) < from.Offset {
			head = nodes[i].ID
		} else {
			start = i
			break
		}
	}
	if head != from.Head {
		return ir.Segment{}, fmt.Errorf("%w: the OpenCode session %s no longer continues from the point read before", agent.ErrDiverged, sess.ID)
	}
	seg.Nodes = nodes[start:]
	return seg, nil
}

// lift is one message's nodes (§4 of the design): prompts, replies, reasoning, tool calls
// with their results, compactions, the person's own shell commands, and notes for a
// folder change or a skill. OpenCode's bookkeeping (system, synthetic, idle, agent and
// model switches) is left out.
func lift(msg message) []ir.Node {
	ts := millis(msg.Time.Created)
	native := func(anchor string, payload json.RawMessage) *ir.Native {
		return &ir.Native{Format: nativeFormat, Anchor: anchor, Payload: canonical(payload)}
	}
	var out []ir.Node
	switch msg.Type {
	case "user":
		if t := msg.prompt(); strings.TrimSpace(t) != "" {
			out = append(out, ir.Node{Kind: ir.KindMessage, Actor: ir.User, Time: ts, Text: t, Native: native(msg.ID, msg.Raw)})
		}
		for k, f := range msg.Files {
			out = append(out, ir.Node{Kind: ir.KindAttachment, Actor: ir.User, Time: ts, Attachment: &ir.Attachment{MIME: f.MIME, Name: f.Name},
				Native: &ir.Native{Format: nativeFormat, Anchor: fmt.Sprintf("%s/file/%d", msg.ID, k)}})
		}
	case "assistant":
		for j, it := range msg.items() {
			anchor := fmt.Sprintf("%s/%d", msg.ID, j)
			payload := msg.Content[j]
			switch it.Type {
			case "text":
				if strings.TrimSpace(it.Text) != "" {
					out = append(out, ir.Node{Kind: ir.KindMessage, Actor: ir.Agent, Time: ts, Text: it.Text, Native: native(anchor, payload)})
				}
			case "reasoning":
				opaque := strings.TrimSpace(it.Text) == "" && hasFields(it.Native)
				if strings.TrimSpace(it.Text) != "" || opaque {
					out = append(out, ir.Node{Kind: ir.KindReasoning, Actor: ir.Agent, Time: ts, Text: it.Text, Reasoning: &ir.Reasoning{Opaque: opaque}, Native: native(anchor, payload)})
				}
			case "tool":
				call, res := toolNodes(it)
				out = append(out, ir.Node{Kind: ir.KindToolCall, Actor: ir.Agent, Time: ts, Tool: call, Native: native(anchor, payload)})
				if res != nil {
					out = append(out, ir.Node{Kind: ir.KindToolResult, Actor: ir.User, Time: ts, Result: res, Native: &ir.Native{Format: nativeFormat, Anchor: anchor + "/result"}})
				}
			}
		}
	case "compaction":
		if msg.Status == "completed" && strings.TrimSpace(msg.Summary) != "" {
			out = append(out, ir.Node{Kind: ir.KindCompaction, Actor: ir.Agent, Time: ts, Text: msg.Summary, Native: native(msg.ID, msg.Raw)})
		}
	case "shell":
		input, _ := json.Marshal(map[string]string{"command": msg.Command})
		call := &ir.ToolCall{CallID: msg.ID, Name: "shell", Input: input, Kind: ir.ToolExecute, Shell: &ir.Shell{Command: msg.Command}}
		res := &ir.ToolResult{CallID: msg.ID, Status: ir.StatusCompleted, ExitCode: exitCode(msg.Exit)}
		if msg.Output != nil {
			res.Output = msg.Output.Output
		}
		// A shell's status is running, exited, timeout or killed (schema/src/shell.ts).
		if msg.Status == "timeout" || msg.Status == "killed" || res.ExitCode != nil && *res.ExitCode != 0 {
			res.Status = ir.StatusFailed
		}
		out = append(out,
			ir.Node{Kind: ir.KindToolCall, Actor: ir.User, Time: ts, Tool: call, Native: native(msg.ID, msg.Raw)},
			ir.Node{Kind: ir.KindToolResult, Actor: ir.User, Time: ts, Result: res, Native: &ir.Native{Format: nativeFormat, Anchor: msg.ID + "/result"}})
	case "location-switched":
		if msg.Location != nil && msg.Location.Directory != "" {
			out = append(out, ir.Node{Kind: ir.KindMessage, Actor: ir.User, Time: ts, Text: "[OpenCode moved the session to " + msg.Location.Directory + "]", Native: native(msg.ID, msg.Raw)})
		}
	case "skill":
		name := msg.Name
		if name == "" {
			name = msg.Skill
		}
		out = append(out, ir.Node{Kind: ir.KindMessage, Actor: ir.User, Time: ts, Text: "[used the skill " + name + "]", Native: native(msg.ID, msg.Raw)})
	}
	return out
}

func hasFields(raw json.RawMessage) bool {
	var m map[string]json.RawMessage
	return json.Unmarshal(raw, &m) == nil && len(m) > 0
}

func exitCode(f *float64) *int {
	if f == nil {
		return nil
	}
	n := int(*f)
	return &n
}

// toolNodes is a tool item's call, understood by OpenCode's tool name, and its result (nil
// while it has none).
func toolNodes(it item) (*ir.ToolCall, *ir.ToolResult) {
	// Sorted keys: OpenCode's export and its database may order an input's keys
	// differently, and a node's id covers its input's bytes.
	input := canonical(it.State.Input)
	if len(input) == 0 {
		input = json.RawMessage("{}")
	}
	var in struct {
		Command     string `json:"command"`
		Workdir     string `json:"workdir"`
		Path        string `json:"path"`
		FilePath    string `json:"filePath"`
		OldString   string `json:"oldString"`
		NewString   string `json:"newString"`
		Content     string `json:"content"`
		Patch       string `json:"patch"`
		PatchText   string `json:"patchText"`
		Pattern     string `json:"pattern"`
		Query       string `json:"query"`
		URL         string `json:"url"`
		Description string `json:"description"`
		Prompt      string `json:"prompt"`
	}
	_ = json.Unmarshal(input, &in)
	file := in.Path
	if file == "" {
		file = in.FilePath
	}
	call := &ir.ToolCall{CallID: it.ID, Name: it.Name, Input: input, Kind: ir.ToolOther}
	switch it.Name {
	case "shell":
		call.Kind, call.Shell = ir.ToolExecute, &ir.Shell{Command: in.Command, Dir: in.Workdir}
	case "read":
		call.Kind, call.Path = ir.ToolRead, file
	case "edit":
		call.Kind, call.Edit = ir.ToolEdit, &ir.Edit{Path: file, Old: in.OldString, New: in.NewString}
	case "patch":
		diff := in.Patch
		if diff == "" {
			diff = in.PatchText
		}
		call.Kind, call.Edit = ir.ToolEdit, &ir.Edit{Path: file, Diff: diff}
	case "write":
		call.Kind, call.Write = ir.ToolEdit, &ir.Write{Path: file, Content: in.Content}
	case "glob", "grep":
		call.Kind, call.Search = ir.ToolSearch, &ir.Search{Pattern: in.Pattern, Path: file}
	case "websearch":
		call.Kind, call.Search = ir.ToolSearch, &ir.Search{Pattern: in.Query}
	case "webfetch":
		call.Kind, call.URL = ir.ToolFetch, in.URL
	case "subagent":
		call.Kind, call.Subagent = ir.ToolSubagent, &ir.Subagent{Description: in.Description, Prompt: in.Prompt}
	}
	var res *ir.ToolResult
	switch it.State.Status {
	case "completed":
		res = &ir.ToolResult{CallID: it.ID, Status: ir.StatusCompleted, Output: contentText(it.State.Content)}
	case "error":
		out := contentText(it.State.Content)
		if it.State.Error != nil && it.State.Error.Message != "" {
			out = strings.TrimSpace(strings.Join([]string{it.State.Error.Message, out}, "\n"))
		}
		res = &ir.ToolResult{CallID: it.ID, Status: ir.StatusFailed, Output: out}
	}
	if res != nil && it.Name == "shell" {
		if raw, ok := it.State.Metadata["exit"]; ok {
			var f float64
			if json.Unmarshal(raw, &f) == nil {
				res.ExitCode = exitCode(&f)
				if *res.ExitCode != 0 {
					res.Status = ir.StatusFailed
				}
			}
		}
	}
	return call, res
}

// contentText is a tool's text output (files by name).
func contentText(cs []toolContent) string {
	var parts []string
	for _, c := range cs {
		switch c.Type {
		case "text":
			parts = append(parts, c.Text)
		default:
			if c.Name != "" {
				parts = append(parts, "["+c.Name+"]")
			}
		}
	}
	return strings.Join(parts, "\n")
}
