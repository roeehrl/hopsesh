package claude

import (
	"context"
	"crypto/rand"
	"crypto/sha1"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/roeehrl/hopsesh/sdk/agent"
	"github.com/roeehrl/hopsesh/sdk/ir"
)

var _ agent.Writer = (*Module)(nil)

// window is a conservative fallback; effective account/model capacity may differ.
const window = ir.FallbackWindow

// Profile says Claude Code takes long histories, and tool calls can be replayed natively.
func (m *Module) Profile(agent.Install) ir.Profile {
	return ir.Profile{Window: window, NativeReplay: true, PortableAppend: true}
}

// Write emits Claude Code transcript records: a chain of user and assistant records with
// the envelope Claude Code needs to resume, a title, and a last-prompt record naming the
// new leaf (Claude Code resumes from the leaf its newest last-prompt names).
func (m *Module) Write(ctx context.Context, h agent.Host, in agent.Install, req ir.WriteRequest) (ir.WriteResult, error) {
	pa, fsys := h.Path(), h.FS()
	capacity, err := m.ContextCapacity(ctx, h, in, nil)
	if err != nil {
		return ir.WriteResult{}, err
	}
	if req.Mode == ir.WriteNew {
		if err = capacity.Check(req.Items); err != nil {
			return ir.WriteResult{}, err
		}
	}

	w := writer{version: in.Version, cwd: req.Header.CWD, branch: req.Header.GitBranch, model: claudeModel(req.Header.Model)}
	var file string
	var from int64
	switch req.Mode {
	case ir.WriteNew:
		w.session = req.SessionID
		if w.session == "" {
			w.session = newUUID()
		}
		dir, err := fsys.RealPath(req.Header.CWD)
		if err != nil {
			return ir.WriteResult{}, fmt.Errorf("the working directory %s: %w", req.Header.CWD, err)
		}
		file = pa.Join(in.Root(home), "projects", Slug(dir), w.session+".jsonl")
		if _, err := fsys.Stat(file); err == nil {
			return ir.WriteResult{}, fmt.Errorf("%s already exists", file)
		}
	case ir.WriteAppend:
		w.session = req.SessionID
		l, err := m.List(ctx, h, in)
		if err != nil {
			return ir.WriteResult{}, err
		}
		var s *agent.Summary
		for i := range l.Sessions {
			if string(l.Sessions[i].Key.Session) == req.SessionID {
				s = &l.Sessions[i]
			}
		}
		if s == nil {
			return ir.WriteResult{}, fmt.Errorf("%w: %s", agent.ErrNotFound, req.SessionID)
		}
		file = s.Path
		fi, err := fsys.Stat(file)
		if err != nil {
			return ir.WriteResult{}, err
		}
		if fi.Size() != req.Expect.Offset {
			return ir.WriteResult{}, fmt.Errorf("%w: %s changed since it was read", agent.ErrDiverged, req.SessionID)
		}
		if req.Expect.Head != "" {
			seg, e := m.Read(ctx, h, in, *s, ir.Cursor{})
			if e != nil {
				return ir.WriteResult{}, e
			}
			if seg.Cursor.Head != req.Expect.Head {
				return ir.WriteResult{}, fmt.Errorf("%w: native head changed", agent.ErrDiverged)
			}
		}

		capacity, err = m.ContextCapacity(ctx, h, in, s)
		if err != nil {
			return ir.WriteResult{}, err
		}
		if err = capacity.Check(req.Items); err != nil {
			return ir.WriteResult{}, err
		}
		from = fi.Size()
		f, err := fsys.Open(file)
		if err != nil {
			return ir.WriteResult{}, err
		}
		recs, _, err := readRecords(f)
		f.Close()
		if err != nil {
			return ir.WriteResult{}, err
		}
		b, err := activeBranch(recs)
		if err != nil {
			return ir.WriteResult{}, err
		}
		if len(b) > 0 {
			w.parent = recs[b[len(b)-1]].UUID
		}
		w.cwd = s.CWD
	default:
		return ir.WriteResult{}, fmt.Errorf("unknown write mode %q", req.Mode)
	}
	body := w.records(req)
	if req.Mode == ir.WriteNew {
		err = fsys.WriteFile(file, body, 0o600)
	} else {
		err = fsys.Append(file, body, agent.AppendOptions{NewLine: true})
	}
	if err != nil {
		return ir.WriteResult{}, err
	}
	res := ir.WriteResult{SessionID: w.session, Path: file, From: from, To: from + int64(len(body))}
	fi, err := fsys.Stat(file)
	if err != nil {
		return res, err
	}
	sum := agent.Summary{Key: agent.SessionKey{Agent: id, Session: agent.SessionID(w.session)}, Path: file, CWD: w.cwd}
	seg, err := m.Read(ctx, h, in, sum, ir.Cursor{})
	if err != nil {
		return res, err
	}
	res.To = fi.Size()
	res.Cursor = seg.Cursor
	for _, n := range seg.Nodes {
		if n.Native != nil {
			if it, ok := w.provenance[n.Native.Anchor]; ok {
				res.Projection = append(res.Projection, ir.ProjectionFor(n, it))
			}
		}
	}
	return res, nil
}

type writer struct {
	session, version, cwd, branch, model string
	operation                            string
	parent                               string // uuid the next record chains to
	provenance                           map[string]ir.Item
}

// records renders items as transcript lines (each ending in a newline).
func (w *writer) records(req ir.WriteRequest) []byte {
	var b strings.Builder
	w.operation = req.OperationID
	if w.operation == "" {
		w.operation = string(req.Expect.Head)
	}
	w.provenance = map[string]ir.Item{}
	lastPrompt := ""
	for _, it := range req.Items {
		switch {
		case it.Tool != nil:
			call := map[string]any{"type": "tool_use", "id": "toolu_" + w.uuid(it, "call")[:24], "name": it.Tool.Call.Name, "input": json.RawMessage(it.Tool.Call.Input)}
			w.line(&b, it, "assistant", []any{call}, "tool_use")
			res := map[string]any{"type": "tool_result", "tool_use_id": call["id"], "content": it.Tool.Result.Output, "is_error": it.Tool.Result.Status == ir.StatusFailed}
			w.line(&b, ir.Item{Node: it.Node + "/result", Time: it.Time, Coverage: it.Coverage, Generated: it.Generated, Fidelity: it.Fidelity}, "user", []any{res}, "")
		case it.Role == ir.RoleUser:
			w.line(&b, it, "user", it.Text, "")
			if own := agent.OwnText(it.Text); own != "" {
				lastPrompt = own
			}
		default:
			w.line(&b, it, "assistant", []any{map[string]any{"type": "text", "text": it.Text}}, "end_turn")
		}
	}
	if req.Header.Title != "" { // on an append, this also clears a "continued in" mark
		b.Write(encodeRecord(map[string]any{"type": "custom-title", "customTitle": req.Header.Title, "sessionId": w.session}))
		b.WriteByte('\n')
	}
	lp := map[string]any{"type": "last-prompt", "leafUuid": w.parent, "sessionId": w.session}
	if lastPrompt != "" {
		lp["lastPrompt"] = lastPrompt
	}
	b.Write(encodeRecord(lp))
	b.WriteByte('\n')
	return []byte(b.String())
}

// line writes one chained record. content is a string (user) or blocks.
func (w *writer) line(b *strings.Builder, it ir.Item, typ string, content any, stop string) {
	u := w.uuid(it, typ)
	w.provenance[u+"/0"] = it
	var parent any
	if w.parent != "" {
		parent = w.parent
	}
	ts := it.Time
	if ts.IsZero() {
		ts = time.Now()
	}
	msg := map[string]any{"role": typ, "content": content}
	if typ == "assistant" {
		msg = map[string]any{"id": fmt.Sprintf("msg_hopsesh_%s", u[:8]), "type": "message", "role": "assistant", "model": w.model,
			"content": content, "stop_reason": stop, "stop_sequence": nil, "usage": map[string]int{"input_tokens": 0, "output_tokens": 0}}
	}
	// Field order follows Claude Code's own records.
	rec := []kv{
		{"parentUuid", parent}, {"isSidechain", false}, {"userType", "external"}, {"cwd", w.cwd},
		{"sessionId", w.session}, {"version", w.version}, {"gitBranch", w.branch}, {"type", typ},
		{"message", msg}, {"uuid", u}, {"timestamp", ts.UTC().Format("2006-01-02T15:04:05.000Z")}, {"entrypoint", "cli"},
	}
	b.Write(encodeOrdered(rec))
	b.WriteByte('\n')
	w.parent = u
}

// uuid is deterministic per session and node, so writing the same conversion twice gives
// the same records (a name-based UUID, version 5 layout).
func (w *writer) uuid(it ir.Item, salt string) string {
	sum := sha1.Sum([]byte("hopsesh:" + w.session + ":" + w.operation + ":" + string(it.Node) + ":" + salt))
	sum[6] = (sum[6] & 0x0f) | 0x50
	sum[8] = (sum[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", sum[0:4], sum[4:6], sum[6:8], sum[8:10], sum[10:16])
}

// claudeModel keeps a Claude model id, and labels anything else as Claude Code's own
// "<synthetic>" (messages it did not get from a model).
func claudeModel(m string) string {
	if strings.HasPrefix(m, "claude-") {
		return m
	}
	return "<synthetic>"
}

type kv struct {
	k string
	v any
}

func encodeOrdered(fields []kv) []byte {
	var b strings.Builder
	b.WriteByte('{')
	for i, f := range fields {
		if i > 0 {
			b.WriteByte(',')
		}
		kb, _ := json.Marshal(f.k)
		vb, _ := marshalNoEscape(f.v)
		b.Write(kb)
		b.WriteByte(':')
		b.Write(vb)
	}
	b.WriteByte('}')
	return []byte(b.String())
}

func newUUID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
