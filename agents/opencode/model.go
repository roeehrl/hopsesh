package opencode

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/roeehrl/hopsesh/sdk/agent"
)

// session is a session_v2 row, or a session's info from OpenCode's export, as the module
// reads it. Times are Unix milliseconds, 0 when unset.
type session struct {
	ID            string
	ProjectID     string
	WorkspaceID   string
	ParentID      string
	ForkSessionID string
	ForkBoundary  json.RawMessage
	Slug          string
	Directory     string
	Path          string // the subpath below the project
	Title         string
	HasTitle      bool
	Version       string
	Agent         string
	Model         json.RawMessage
	Metadata      json.RawMessage
	Revert        json.RawMessage
	Outcome       string
	Cost          float64
	Tokens        tokens
	Created       int64
	Updated       int64
	Busy          int64
	Idle          int64
	Viewed        int64
	Archived      int64
	Suspended     int64
	Compacting    int64
}

// tokens is OpenCode's TokenUsage.
type tokens struct {
	Input     int64 `json:"input"`
	Output    int64 `json:"output"`
	Reasoning int64 `json:"reasoning"`
	Cache     struct {
		Read  int64 `json:"read"`
		Write int64 `json:"write"`
	} `json:"cache"`
}

// modelRef is OpenCode's Model.Ref.
type modelRef struct {
	ID         string `json:"id"`
	ProviderID string `json:"providerID"`
	Variant    string `json:"variant,omitempty"`
}

func (r modelRef) String() string {
	if r.ID == "" {
		return ""
	}
	if r.ProviderID == "" {
		return r.ID
	}
	return r.ProviderID + "/" + r.ID
}

// info is a session's info as OpenCode's session export writes it (Session.Info), plus
// the fields hopsesh's JSON lines add: kind "info" on the first line, and the OpenCode
// version that last wrote the session.
type info struct {
	Kind        string          `json:"kind,omitempty"`
	ID          string          `json:"id"`
	ParentID    string          `json:"parentID,omitempty"`
	Fork        *forkRef        `json:"fork,omitempty"`
	ProjectID   string          `json:"projectID"`
	Agent       string          `json:"agent,omitempty"`
	Model       json.RawMessage `json:"model,omitempty"`
	Cost        float64         `json:"cost"`
	Tokens      tokens          `json:"tokens"`
	Outcome     string          `json:"outcome,omitempty"`
	Time        infoTime        `json:"time"`
	Title       *string         `json:"title,omitempty"`
	Location    location        `json:"location"`
	Subpath     string          `json:"subpath,omitempty"`
	Metadata    json.RawMessage `json:"metadata,omitempty"`
	Permissions json.RawMessage `json:"permissions,omitempty"`
	Revert      json.RawMessage `json:"revert,omitempty"`
	Version     string          `json:"version,omitempty"`
}

type forkRef struct {
	SessionID string          `json:"sessionID"`
	Boundary  json.RawMessage `json:"boundary"`
}

type infoTime struct {
	Created  int64 `json:"created"`
	Updated  int64 `json:"updated"`
	Busy     int64 `json:"busy,omitempty"`
	Idle     int64 `json:"idle,omitempty"`
	Viewed   int64 `json:"viewed,omitempty"`
	Archived int64 `json:"archived,omitempty"`
}

type location struct {
	Directory   string `json:"directory"`
	WorkspaceID string `json:"workspaceID,omitempty"`
}

// infoOf is a session's info as OpenCode's export makes it from the row (session/info.ts
// fromRow). The session's permission rules are left out: their column shares its name
// with a table the module never reads.
func infoOf(s session) info {
	in := info{ID: s.ID, ParentID: s.ParentID, ProjectID: s.ProjectID, Agent: s.Agent, Cost: s.Cost, Tokens: s.Tokens, Outcome: s.Outcome,
		Location: location{Directory: s.Directory, WorkspaceID: s.WorkspaceID}, Subpath: s.Path, Metadata: s.Metadata, Revert: s.Revert, Version: s.Version,
		Time: infoTime{Created: s.Created, Updated: s.Updated, Busy: s.Busy, Idle: s.Idle, Viewed: s.Viewed, Archived: s.Archived}}
	if s.HasTitle {
		t := s.Title
		in.Title = &t
	}
	if s.ForkSessionID != "" && len(s.ForkBoundary) > 0 {
		in.Fork = &forkRef{SessionID: s.ForkSessionID, Boundary: s.ForkBoundary}
	}
	if len(s.Model) > 0 {
		var m modelRef
		if json.Unmarshal(s.Model, &m) == nil && m.ID != "" {
			if m.Variant == "" {
				m.Variant = "default"
			}
			in.Model, _ = json.Marshal(m)
		}
	}
	return in
}

// sessionOf is the session an export's (or a JSON lines file's) info describes.
func sessionOf(in info) session {
	s := session{ID: in.ID, ProjectID: in.ProjectID, ParentID: in.ParentID, Directory: in.Location.Directory, WorkspaceID: in.Location.WorkspaceID,
		Path: in.Subpath, Version: in.Version, Agent: in.Agent, Model: in.Model, Metadata: in.Metadata, Revert: in.Revert, Outcome: in.Outcome,
		Cost: in.Cost, Tokens: in.Tokens, Created: in.Time.Created, Updated: in.Time.Updated, Busy: in.Time.Busy, Idle: in.Time.Idle,
		Viewed: in.Time.Viewed, Archived: in.Time.Archived}
	if in.Title != nil {
		s.Title, s.HasTitle = *in.Title, true
	}
	if in.Fork != nil {
		s.ForkSessionID, s.ForkBoundary = in.Fork.SessionID, in.Fork.Boundary
	}
	return s
}

// message is one session message (session_message, with session_part rows merged in),
// in the shape OpenCode's export writes it. Raw keeps every field, including ones the
// module does not know.
type message struct {
	Raw json.RawMessage
	Seq int64 // database order (0 when read from an export)

	ID          string                     `json:"id"`
	Type        string                     `json:"type"`
	Time        msgTime                    `json:"time"`
	Metadata    map[string]json.RawMessage `json:"metadata"`
	Text        string                     `json:"text"`
	Description string                     `json:"description"`
	Files       []attachment               `json:"files"`
	Agent       string                     `json:"agent"`
	Model       modelRef                   `json:"model"`
	Content     []json.RawMessage          `json:"content"`
	Tokens      *tokens                    `json:"tokens"`
	Status      string                     `json:"status"`
	Summary     string                     `json:"summary"`
	Command     string                     `json:"command"`
	Exit        *float64                   `json:"exit"`
	Output      *shellOutput               `json:"output"`
	Location    *location                  `json:"location"`
	Skill       string                     `json:"skill"`
	Name        string                     `json:"name"`
}

type msgTime struct {
	Created   int64 `json:"created"`
	Completed int64 `json:"completed"`
}

type attachment struct {
	MIME string `json:"mime"`
	Name string `json:"name"`
}

type shellOutput struct {
	Output    string `json:"output"`
	Truncated bool   `json:"truncated"`
}

// item is one assistant content item: text, reasoning or a tool call.
type item struct {
	Type     string          `json:"type"`
	Text     string          `json:"text"`
	ID       string          `json:"id"`
	Name     string          `json:"name"`
	Native   json.RawMessage `json:"native"`
	State    toolState       `json:"state"`
	Executed *bool           `json:"executed"`
}

type toolState struct {
	Status   string                     `json:"status"`
	Input    json.RawMessage            `json:"input"`
	Content  []toolContent              `json:"content"`
	Metadata map[string]json.RawMessage `json:"metadata"`
	Error    *struct {
		Message string `json:"message"`
	} `json:"error"`
}

type toolContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
	Name string `json:"name"`
	MIME string `json:"mime"`
}

// decodeMessage reads one message in the export's shape. A field of an unexpected type
// (a message type newer than the module) is left empty; the message keeps its Raw.
func decodeMessage(raw json.RawMessage) (message, error) {
	var m message
	var mismatch *json.UnmarshalTypeError
	if err := json.Unmarshal(raw, &m); err != nil && !errors.As(err, &mismatch) {
		return message{}, err
	}
	if !strings.HasPrefix(m.ID, "msg_") || m.Type == "" {
		return message{}, fmt.Errorf("a message needs a msg_ id and a type")
	}
	m.Raw = raw
	return m, nil
}

// rowMessage builds a message from its session_message row as OpenCode's own history
// does: the row's data with its id and type, and, for an assistant, its session_part
// rows (each with the row's type) before any content still inline (F5), all upgraded
// from the shape OpenCode wrote before 2.1.
func rowMessage(msgID, typ string, seq int64, data string, parts []json.RawMessage) (message, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(data), &fields); err != nil {
		return message{}, fmt.Errorf("message %s: %w", msgID, err)
	}
	if fields == nil {
		fields = map[string]json.RawMessage{}
	}
	if typ == "assistant" && len(parts) > 0 {
		var inline []json.RawMessage
		if c, ok := fields["content"]; ok {
			_ = json.Unmarshal(c, &inline)
		}
		merged, err := json.Marshal(mergeParts(parts, inline))
		if err != nil {
			return message{}, err
		}
		fields["content"] = merged
	}
	upgradeMessage(typ, fields) // a row written before OpenCode 2.1, as OpenCode reads it
	fields["id"], _ = json.Marshal(msgID)
	fields["type"], _ = json.Marshal(typ)
	raw, err := json.Marshal(fields)
	if err != nil {
		return message{}, err
	}
	m, err := decodeMessage(raw)
	if err != nil {
		return message{}, fmt.Errorf("message %s: %w", msgID, err)
	}
	m.Seq = seq
	return m, nil
}

// mergeParts is an assistant's content: its part rows in ordinal order, then the items
// still inline in the message (an older build may have written both).
func mergeParts(parts, inline []json.RawMessage) []json.RawMessage {
	if out := slices.Concat(parts, inline); out != nil {
		return out
	}
	return []json.RawMessage{}
}

// partData is a session_part row's data with the row's type, as OpenCode decodes it.
func partData(typ, data string) (json.RawMessage, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(data), &fields); err != nil {
		return nil, err
	}
	if fields == nil {
		fields = map[string]json.RawMessage{}
	}
	fields["type"], _ = json.Marshal(typ)
	return json.Marshal(fields)
}

// settled is OpenCode's export filter (session/transfer.ts isSettled): an assistant
// message once it completed, a shell command or compaction once it stopped running, and
// every other message. Unsettled messages are neither exported, counted nor read.
func settled(m message) bool {
	switch m.Type {
	case "assistant":
		return m.Time.Completed != 0
	case "shell", "compaction":
		return m.Status != "running"
	}
	return true
}

// settledSQL is settled as an SQL condition on a session_message row m.
const settledSQL = `CASE m.type WHEN 'assistant' THEN json_extract(m.data, '$.time.completed') IS NOT NULL ` +
	`WHEN 'shell' THEN coalesce(json_extract(m.data, '$.status'), '') <> 'running' ` +
	`WHEN 'compaction' THEN coalesce(json_extract(m.data, '$.status'), '') <> 'running' ELSE 1 END`

// prompt is a user message's text as the person typed it.
func (m message) prompt() string {
	if raw, ok := m.Metadata["displayText"]; ok {
		var s string
		if json.Unmarshal(raw, &s) == nil && strings.TrimSpace(s) != "" {
			return s
		}
	}
	return m.Text
}

// items decodes an assistant's content; an item that does not decode is kept as an
// unknown one, so its position stays.
func (m message) items() []item {
	out := make([]item, len(m.Content))
	var mismatch *json.UnmarshalTypeError
	for i, raw := range m.Content {
		if err := json.Unmarshal(raw, &out[i]); err != nil && !errors.As(err, &mismatch) {
			out[i] = item{Type: "unknown"}
		}
	}
	return out
}

// canonical re-encodes JSON with sorted keys and its numbers as they were, so a record
// read from the database and from a file give the same bytes.
func canonical(raw json.RawMessage) json.RawMessage {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var v any
	if d.Decode(&v) != nil {
		return raw
	}
	b, err := json.Marshal(v)
	if err != nil {
		return raw
	}
	return b
}

// sessionKey is a session's key in this install.
func sessionKey(in agent.Install, sid string) agent.SessionKey {
	return agent.SessionKey{Agent: id, Profile: in.ProfileID(), Session: agent.SessionID(sid)}
}
