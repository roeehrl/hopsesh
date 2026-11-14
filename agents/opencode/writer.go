package opencode

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"time"

	"github.com/roeehrl/hopsesh/sdk/agent"
	"github.com/roeehrl/hopsesh/sdk/ir"
)

// A session continued from another agent is written as OpenCode's own transfer (what its
// `session export` prints and its `session import` reads), placed as JSON lines at the
// session's anchor, and imported by OpenCode's program; hopsesh never writes the database.
// The write is the install: the core runs no AfterInstall after it. OpenCode has no way to
// add to a session, so a writer only makes new ones (Profile.PortableAppend is false): a
// return to a session continued here before starts a new one that continues it.
//
// The transfer holds settled messages only, in order, with ascending ids: a prompt is a
// user message; a reply an assistant message with one text item; a shell call (native
// replay) an assistant message with one completed shell tool item; and each turn of
// replies ends with an idle message, as OpenCode's own turns do. Every id and time is made
// from the request (its session id, operation and items), so writing the same request
// again gives the same messages: a recovery checks the placed file against it and imports
// what an interrupted write had not.

var (
	_ agent.Writer         = (*Module)(nil)
	_ agent.WriteRecoverer = (*Module)(nil)
)

// writtenProvider is the provider named on replies hopsesh writes. OpenCode needs a model
// on every reply, and replays a reply's provider state only to the model that made it; a
// provider no one has means it never takes these replies for its own model's. The
// session itself names no model, so OpenCode continues it with the model it would pick
// for a new session.
const writtenProvider = "hopsesh"

// writtenAgent is the OpenCode agent named on replies hopsesh writes (OpenCode needs one;
// the session names none, so OpenCode continues it with its default agent).
const writtenAgent = "build"

// Profile is a window from the catalog when the model is known (ContextCapacity), shell
// calls replayed as OpenCode's own, and no appends to an existing session (decision D4).
func (*Module) Profile(agent.Install) ir.Profile {
	return ir.Profile{Window: ir.FallbackWindow, NativeReplay: true}
}

// Write writes a new OpenCode session from the request: its transfer at the session's
// anchor, imported by OpenCode's program under the id the core chose (req.SessionID), or,
// without one in OpenCode's form, an id made from the operation's.
func (m *Module) Write(ctx context.Context, h agent.Host, in agent.Install, req ir.WriteRequest) (ir.WriteResult, error) {
	if req.Mode != ir.WriteNew {
		return ir.WriteResult{}, fmt.Errorf("%w: OpenCode cannot add to an existing session; hopsesh continues it in a new one", agent.ErrUnsupported)
	}
	if strings.TrimSpace(req.Header.CWD) == "" {
		return ir.WriteResult{}, errors.New("an OpenCode session needs a folder to be imported in")
	}
	capacity, err := m.ContextCapacity(ctx, h, in, nil)
	if err != nil {
		return ir.WriteResult{}, err
	}
	if err := capacity.Check(req.Items); err != nil {
		return ir.WriteResult{}, err
	}
	sid := writtenID(req)
	if sid == "" {
		sid = m.NewSessionID(in, time.Now())
	}
	svc, err := writeService(ctx, h, in)
	if err != nil {
		return ir.WriteResult{}, err
	}
	w, err := render(h, req, sid, time.Now())
	if err != nil {
		return ir.WriteResult{}, err
	}
	if err := h.FS().WriteFile(anchorOf(h, in, sid), w.body, 0o600); err != nil {
		return ir.WriteResult{}, err
	}
	return m.importWritten(ctx, h, in, svc, req, w)
}

// RecoverWrite finishes a Write that was interrupted: it finds the session by the id Write
// gave it, checks the file Write placed is the one the request makes (writing it when
// Write had not), imports it when OpenCode does not have it yet, and returns the result
// Write would have. A session there with other messages was used since: ErrDiverged.
func (m *Module) RecoverWrite(ctx context.Context, h agent.Host, in agent.Install, req ir.WriteRequest) (ir.WriteResult, error) {
	if req.Mode != ir.WriteNew {
		return ir.WriteResult{}, fmt.Errorf("%w: OpenCode cannot add to an existing session", agent.ErrUnsupported)
	}
	sid := writtenID(req)
	if sid == "" {
		return ir.WriteResult{}, fmt.Errorf("%w: the interrupted write named no session and no operation", agent.ErrNotFound)
	}
	anchor := anchorOf(h, in, sid)
	placed, err := agent.ReadNative(ctx, h.FS(), anchor)
	base := time.Now()
	switch {
	case errors.Is(err, fs.ErrNotExist):
		placed = nil
	case err != nil:
		return ir.WriteResult{}, err
	default:
		s, _, err := decodeJSONL(anchor, placed)
		if err != nil {
			return ir.WriteResult{}, err
		}
		base = time.UnixMilli(s.Created)
	}
	w, err := render(h, req, sid, base)
	if err != nil {
		return ir.WriteResult{}, err
	}
	if placed == nil {
		if err := h.FS().WriteFile(anchor, w.body, 0o600); err != nil {
			return ir.WriteResult{}, err
		}
	} else if !bytes.Equal(placed, w.body) {
		return ir.WriteResult{}, fmt.Errorf("%w: %s is not the session the interrupted write placed", agent.ErrDiverged, anchor)
	}
	svc, err := writeService(ctx, h, in)
	if err != nil {
		return ir.WriteResult{}, err
	}
	return m.importWritten(ctx, h, in, svc, req, w)
}

// importWritten has OpenCode import the session placed at its anchor (nothing when it has
// it with these messages already), records its title as one hopsesh gave, and reads it
// back for the result.
func (m *Module) importWritten(ctx context.Context, h agent.Host, in agent.Install, svc service, req ir.WriteRequest, w *written) (ir.WriteResult, error) {
	anchor := anchorOf(h, in, w.sid)
	p, err := parsePlaced(anchor, w.body)
	if err != nil {
		return ir.WriteResult{}, err
	}
	if err := m.importSession(ctx, h, in, svc, w.sid, p.ids(), req.Header.CWD, func() (string, error) { return p.stage(h, in) }); err != nil {
		return ir.WriteResult{}, err
	}
	if strings.TrimSpace(req.Header.Title) != "" {
		recordTitle(ctx, h, in, w.sid, req.Header.Title)
	}
	seg, err := m.Read(ctx, h, in, agent.Summary{Key: sessionKey(in, string(w.sid)), Path: anchor}, ir.Cursor{})
	if err != nil {
		return ir.WriteResult{}, err
	}
	res := ir.WriteResult{SessionID: string(w.sid), Path: anchor, To: seg.Cursor.Offset, Cursor: seg.Cursor}
	for _, n := range seg.Nodes {
		if n.Native == nil {
			continue
		}
		if it, ok := w.provenance[n.Native.Anchor]; ok {
			res.Projection = append(res.Projection, ir.ProjectionFor(n, it))
		}
	}
	return res, nil
}

// writtenID is the id Write gives the session: the one the core asked for when it is in
// OpenCode's form (its import takes only ids starting with "ses"), else one made from the
// operation's id, so a recovery finds it; "" when the request has neither.
func writtenID(req ir.WriteRequest) agent.SessionID {
	if strings.HasPrefix(req.SessionID, "ses") {
		return agent.SessionID(req.SessionID)
	}
	if req.OperationID == "" {
		return ""
	}
	sum := sha256.Sum256([]byte("hopsesh-opencode-session\x00" + req.OperationID))
	return agent.SessionID("ses_" + hex.EncodeToString(sum[:6]) + tailOf(sum[6:]))
}

// tailOf is an id's 14 base62 characters made from sum (at least 14 bytes).
func tailOf(sum []byte) string {
	out := make([]byte, idTail)
	for i := range out {
		out[i] = base62[int(sum[i])%len(base62)]
	}
	return string(out)
}

// written is a session as Write places it: its JSON lines, and which item each of its
// nodes carries (by node anchor).
type written struct {
	sid        agent.SessionID
	body       []byte
	provenance map[string]ir.Item
}

// Messages as hopsesh writes them, in the members' order of OpenCode's own export.
type (
	writtenTime struct {
		Created   int64 `json:"created"`
		Completed int64 `json:"completed,omitempty"`
	}
	writtenUser struct {
		ID    string      `json:"id"`
		Time  writtenTime `json:"time"`
		Text  string      `json:"text"`
		Files []struct{}  `json:"files"`
		Type  string      `json:"type"`
	}
	writtenReply struct {
		ID      string      `json:"id"`
		Time    writtenTime `json:"time"`
		Type    string      `json:"type"`
		Agent   string      `json:"agent"`
		Model   modelRef    `json:"model"`
		Content []any       `json:"content"`
		Finish  string      `json:"finish"`
		Cost    float64     `json:"cost"`
		Tokens  tokens      `json:"tokens"`
	}
	writtenText struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	writtenTool struct {
		Type     string          `json:"type"`
		ID       string          `json:"id"`
		Name     string          `json:"name"`
		Executed bool            `json:"executed"`
		State    map[string]any  `json:"state"`
		Time     writtenToolTime `json:"time"`
	}
	writtenToolTime struct {
		Created   int64 `json:"created"`
		Ran       int64 `json:"ran"`
		Completed int64 `json:"completed"`
	}
	writtenIdle struct {
		ID      string      `json:"id"`
		Time    writtenTime `json:"time"`
		Type    string      `json:"type"`
		Outcome string      `json:"outcome"`
	}
)

// render makes the session req asks for, as id sid, written at base (the session's
// creation time, and the times of items that have none).
func render(h agent.Host, req ir.WriteRequest, sid agent.SessionID, base time.Time) (*written, error) {
	w := &written{sid: sid, provenance: map[string]ir.Item{}}
	salt := string(sid) + "\x00" + req.OperationID
	// Times: each item's own, kept increasing (a millisecond apart at least); before the
	// first item with a time, or with none, from base.
	last := base.UnixMilli() - int64(2*len(req.Items)) - 2
	for _, it := range req.Items {
		if !it.Time.IsZero() {
			last = it.Time.UnixMilli() - 1
			break
		}
	}
	next := func(t time.Time) int64 {
		ms := last + 1
		if !t.IsZero() && t.UnixMilli() > ms {
			ms = t.UnixMilli()
		}
		last = ms
		return ms
	}
	n := 0
	msgID := func(ms int64) string {
		n++
		sum := sha256.Sum256([]byte(fmt.Sprintf("%s\x00msg\x00%d", salt, n)))
		return "msg_" + idTime(ms, 1, false) + tailOf(sum[:])
	}
	model := modelRef{ID: nonEmptyText(req.Header.Model, "unknown"), ProviderID: writtenProvider}
	var msgs []any
	replying := false
	idle := func() {
		if replying {
			ms := next(time.Time{})
			msgs = append(msgs, writtenIdle{ID: msgID(ms), Time: writtenTime{Created: ms}, Type: "idle", Outcome: "succeeded"})
			replying = false
		}
	}
	reply := func(ms int64, content any, finish string) string {
		mid := msgID(ms)
		msgs = append(msgs, writtenReply{ID: mid, Time: writtenTime{Created: ms, Completed: ms}, Type: "assistant", Agent: writtenAgent, Model: model,
			Content: []any{content}, Finish: finish})
		replying = true
		return mid
	}
	for k, it := range req.Items {
		switch {
		case it.Tool != nil && it.Tool.Call.Shell != nil:
			sum := sha256.Sum256([]byte(fmt.Sprintf("%s\x00call\x00%d", salt, k)))
			ms := next(it.Time)
			tool := writtenTool{Type: "tool", ID: "call_" + hex.EncodeToString(sum[:4]), Name: "shell", State: shellState(it.Tool),
				Time: writtenToolTime{Created: ms, Ran: ms, Completed: ms}}
			mid := reply(ms, tool, "tool-calls")
			w.provenance[mid+"/0"] = it
			res := it
			res.Node = it.Node + "/result"
			w.provenance[mid+"/0/result"] = res
		case strings.TrimSpace(it.Text) == "":
			// Nothing a reader would see: OpenCode's history keeps no empty message.
		case it.Role == ir.RoleUser:
			idle()
			ms := next(it.Time)
			mid := msgID(ms)
			msgs = append(msgs, writtenUser{ID: mid, Time: writtenTime{Created: ms}, Text: it.Text, Files: []struct{}{}, Type: "user"})
			w.provenance[mid] = it
		default:
			w.provenance[reply(next(it.Time), writtenText{Type: "text", Text: it.Text}, "stop")+"/0"] = it
		}
	}
	idle()
	title := req.Header.Title
	in := info{Kind: "info", ID: string(sid), ProjectID: "global", Time: infoTime{Created: base.UnixMilli(), Updated: max(base.UnixMilli(), last)},
		Location: location{Directory: storedDir(h, req.Header.CWD)}}
	if strings.TrimSpace(title) != "" {
		in.Title = &title
	}
	var b bytes.Buffer
	line, err := json.Marshal(in)
	if err != nil {
		return nil, err
	}
	b.Write(append(line, '\n'))
	for _, msg := range msgs {
		line, err := json.Marshal(msg)
		if err != nil {
			return nil, err
		}
		b.Write(append(line, '\n'))
	}
	w.body = b.Bytes()
	return w, nil
}

// shellState is a shell call's state as OpenCode records a completed one: its command,
// its output as one text item, and its exit status. A call that failed without an exit
// status is a tool error.
func shellState(t *ir.ToolPair) map[string]any {
	input := map[string]any{"command": t.Call.Shell.Command}
	out := t.Result.Output
	if t.Result.Status == ir.StatusFailed && t.Result.ExitCode == nil {
		return map[string]any{"status": "error", "input": input, "error": map[string]any{"type": "tool.execution", "message": nonEmptyText(out, "The command failed.")}}
	}
	md := map[string]any{"status": "completed", "truncated": false}
	if t.Result.ExitCode != nil {
		md["exit"] = *t.Result.ExitCode
	}
	return map[string]any{"status": "completed", "input": input, "content": []any{writtenText{Type: "text", Text: out}}, "metadata": md}
}

// storedDir is a folder as OpenCode stores it: with "/" on Windows too.
func storedDir(h agent.Host, dir string) string {
	if h.Path().Sep() == "/" {
		return dir
	}
	return strings.ReplaceAll(dir, h.Path().Sep(), "/")
}

func nonEmptyText(s, fallback string) string {
	if strings.TrimSpace(s) == "" {
		return fallback
	}
	return s
}

// writtenBy reports whether a message is one hopsesh wrote (its reply's provider).
func writtenBy(msg message) bool {
	return msg.Type == "assistant" && msg.Model.ProviderID == writtenProvider
}
