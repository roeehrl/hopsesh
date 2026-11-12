// Package ir is the conversation representation every agent module reads into and writes
// from, so a session can continue in another agent: N readers and N writers instead of a
// converter for every pair. It keeps what a coding conversation means (messages, tool
// calls with their intent, results, plans) and, beside it, each record's native form so a
// writer for the same agent can emit it unchanged.
package ir

import (
	"encoding/json"
	"time"
)

// NodeID is a content address: SHA-256 (hex) of the canonical form of a node's
// model-visible content and its parent's id. The same conversation read twice, or from a
// copy with other timestamps and native ids, gets the same ids.
type NodeID string

// Kind is what a node is.
type Kind string

const (
	KindMessage    Kind = "message"
	KindReasoning  Kind = "reasoning"
	KindToolCall   Kind = "tool_call"
	KindToolResult Kind = "tool_result"
	KindPlan       Kind = "plan"
	KindCompaction Kind = "compaction"
	KindAttachment Kind = "attachment"
)

// Actor is who produced a node.
type Actor string

const (
	User  Actor = "user"
	Agent Actor = "agent"
)

// Fragment retains individual source coverage when adjacent messages coalesce.
type Fragment struct {
	Text     string   `json:"text"`
	Coverage []NodeID `json:"coverage"`
}

// Node is one step of a conversation.
type Node struct {
	Fragments []Fragment `json:"fragments,omitempty"`
	ID        NodeID     `json:"id"`
	Parent    NodeID     `json:"parent,omitempty"`
	Kind      Kind       `json:"kind"`
	Actor     Actor      `json:"actor"`
	Time      time.Time  `json:"time"`
	// Text is the message, the reasoning summary or the compaction summary.
	Text       string      `json:"text,omitempty"`
	Tool       *ToolCall   `json:"tool,omitempty"`
	Result     *ToolResult `json:"result,omitempty"`
	Plan       []PlanEntry `json:"plan,omitempty"`
	Attachment *Attachment `json:"attachment,omitempty"`
	Reasoning  *Reasoning  `json:"reasoning,omitempty"`
	Native     *Native     `json:"native,omitempty"`
	Generated  bool        `json:"generated,omitempty"` // written by hopsesh (briefing, acknowledgement)
	Coverage   []NodeID    `json:"coverage,omitempty"`  // logical revisions represented by this node
}

// Native is a node's record as its agent wrote it.
type Native struct {
	Format  string          `json:"format"` // "claude.jsonl", "codex.rollout"
	Payload json.RawMessage `json:"payload"`
	Anchor  string          `json:"anchor,omitempty"` // stable native record identity, independent of rewritten paths
}

// ToolKind is the intent of a tool call (the Agent Client Protocol's kinds).
type ToolKind string

const (
	ToolRead     ToolKind = "read"
	ToolEdit     ToolKind = "edit"
	ToolDelete   ToolKind = "delete"
	ToolMove     ToolKind = "move"
	ToolSearch   ToolKind = "search"
	ToolExecute  ToolKind = "execute"
	ToolThink    ToolKind = "think"
	ToolFetch    ToolKind = "fetch"
	ToolPlan     ToolKind = "plan"
	ToolSubagent ToolKind = "subagent"
	ToolOther    ToolKind = "other"
)

// ToolCall is a tool the agent called. Exactly one of the intent fields is set when the
// reader understood the call; Name and Input always keep the original.
type ToolCall struct {
	CallID string          `json:"callId"`
	Name   string          `json:"name"`  // the agent's tool name: "Bash", "exec_command", "mcp__github__x"
	Input  json.RawMessage `json:"input"` // exact input
	Kind   ToolKind        `json:"kind"`

	Shell    *Shell    `json:"shell,omitempty"`
	Edit     *Edit     `json:"edit,omitempty"`
	Write    *Write    `json:"write,omitempty"`
	Path     string    `json:"path,omitempty"` // read, delete
	Search   *Search   `json:"search,omitempty"`
	URL      string    `json:"url,omitempty"` // fetch
	Subagent *Subagent `json:"subagent,omitempty"`
	MCP      *MCP      `json:"mcp,omitempty"`
}

// Shell is a command run in a shell.
type Shell struct {
	Command string `json:"command"`
	Dir     string `json:"dir,omitempty"`
}

// Edit changes part of a file: either Old/New text, or a unified diff.
type Edit struct {
	Path string `json:"path"`
	Old  string `json:"old,omitempty"`
	New  string `json:"new,omitempty"`
	Diff string `json:"diff,omitempty"`
}

// Write creates or replaces a whole file.
type Write struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

// Search looks for files or text.
type Search struct {
	Pattern string `json:"pattern"`
	Path    string `json:"path,omitempty"`
}

// Subagent hands a task to a sub-agent.
type Subagent struct {
	Description string `json:"description,omitempty"`
	Prompt      string `json:"prompt,omitempty"`
}

// MCP is a call to a tool of an MCP server.
type MCP struct {
	Server string `json:"server"`
	Tool   string `json:"tool"`
}

// ToolStatus is how a tool call ended.
type ToolStatus string

const (
	StatusCompleted ToolStatus = "completed"
	StatusFailed    ToolStatus = "failed"
)

// ToolResult is what a tool call returned.
type ToolResult struct {
	CallID   string     `json:"callId"`
	Status   ToolStatus `json:"status"`
	ExitCode *int       `json:"exitCode,omitempty"`
	Output   string     `json:"output"`
	// OutputBytes is the size of the full output when Output was shortened by the reader.
	OutputBytes int64 `json:"outputBytes,omitempty"`
}

// PlanEntry is one item of the agent's plan or to-do list.
type PlanEntry struct {
	Content string `json:"content"`
	Status  string `json:"status"` // pending | in_progress | completed
}

// Attachment is an image or file the user attached.
type Attachment struct {
	MIME string `json:"mime"`
	Data []byte `json:"data,omitempty"`
	Name string `json:"name,omitempty"`
}

// Reasoning is the agent's thinking. It never crosses vendors: Opaque content is bound to
// the model, account or conversation prefix that produced it.
type Reasoning struct {
	Opaque bool `json:"opaque"` // signed or encrypted content (in Native)
}

// Header describes a session as a whole.
type Header struct {
	Agent     string    `json:"agent"`
	Version   string    `json:"version,omitempty"`
	SessionID string    `json:"sessionId"`
	CWD       string    `json:"cwd"`
	Title     string    `json:"title,omitempty"`
	GitBranch string    `json:"gitBranch,omitempty"`
	Model     string    `json:"model,omitempty"`
	Created   time.Time `json:"created"`
}

// Cursor is where a read stopped: the byte offset after the last complete record of the
// session's main file, and the last node read.
type Cursor struct {
	Offset int64  `json:"offset"`
	Head   NodeID `json:"head,omitempty"`
}

// Segment is what a Reader returns: the session's active branch from a cursor on.
type Segment struct {
	Header Header `json:"header"`
	Nodes  []Node `json:"nodes"`
	Cursor Cursor `json:"cursor"`
}

// Profile is what a writer can take.
type Profile struct {
	Window       int  `json:"window"`       // the target model's usable context, in tokens
	NativeReplay bool `json:"nativeReplay"` // can render tool calls as its own tool pairs
	// PortableAppend writes ordinary text turns into an existing session in the
	// selected root. It preserves its old records and never imports source-private
	// state. This is independent of permission to replay native tool/reasoning data.
	PortableAppend bool `json:"portableAppend,omitempty"`
}

// WriteMode is whether a write creates a session or extends one.
type WriteMode string

const (
	WriteNew    WriteMode = "new"
	WriteAppend WriteMode = "append"
)

// WriteRequest asks a writer for native records.
type WriteRequest struct {
	OperationID string    `json:"operationId,omitempty"` // stable transfer identity; scopes generated native record IDs
	Mode        WriteMode `json:"mode"`
	// SessionID is the session to append to (WriteAppend), or the id to use for a new one
	// ("" lets the writer choose).
	SessionID string `json:"sessionId,omitempty"`
	// Expect is where the session's main file must still end for an append (it was read up
	// to there); anything else is ErrDiverged.
	Expect Cursor `json:"expect"`
	Header Header `json:"header"`
	Items  []Item `json:"items"`
}

// Role is who a rendered item speaks as.
type Role string

const (
	RoleUser  Role = "user"
	RoleAgent Role = "agent"
)

// Item is one rendered unit for a writer.
type Item struct {
	Fragments []Fragment `json:"fragments,omitempty"`
	Node      NodeID     `json:"node"`
	Role      Role       `json:"role"`
	Time      time.Time  `json:"time"`
	Text      string     `json:"text,omitempty"`
	Tool      *ToolPair  `json:"tool,omitempty"`   // native replay only
	Native    *Native    `json:"native,omitempty"` // same-agent passthrough
	Coverage  []NodeID   `json:"coverage,omitempty"`
	Generated bool       `json:"generated,omitempty"`
	Fidelity  string     `json:"fidelity,omitempty"`
}

// ToolPair is a tool call and its result, for native replay.
type ToolPair struct {
	Call   ToolCall   `json:"call"`
	Result ToolResult `json:"result"`
}

// WriteResult is what a writer wrote.
type WriteResult struct {
	SessionID  string       `json:"sessionId"`
	Path       string       `json:"path"`
	From       int64        `json:"from"` // byte range written into Path
	To         int64        `json:"to"`
	Cursor     Cursor       `json:"cursor"` // the session's new end
	Projection []Projection `json:"projection,omitempty"`
}

// Projection binds the exact native representation to the logical revisions it carries.
// It lives in the portable sidecar, never in vendor-specific transcript fields.
type Projection struct {
	Fragments []Fragment `json:"fragments,omitempty"`
	Anchor    string     `json:"anchor"`
	Hash      string     `json:"hash"`
	Coverage  []NodeID   `json:"coverage,omitempty"`
	Generated bool       `json:"generated,omitempty"`
	Fidelity  string     `json:"fidelity,omitempty"`
}
