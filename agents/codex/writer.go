package codex

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/roeehrl/hopsesh/sdk/agent"
	"github.com/roeehrl/hopsesh/sdk/ir"
)

var _ agent.Writer = (*Module)(nil)

// window is a conservative fallback; ContextCapacity resolves smaller custom limits.
const window = ir.FallbackWindow

// Profile says Codex's own tool calls cannot be forged safely, so history arrives as text.
func (m *Module) Profile(agent.Install) ir.Profile {
	return ir.Profile{Window: window, PortableAppend: true}
}

// Write emits a legacy-mode rollout (no ordinals): session_meta, then each message as a
// response_item with its user_message/agent_message event (Codex titles threads from
// those events). Paginated appends preserve contiguous native ordinals.
func (m *Module) Write(ctx context.Context, h agent.Host, in agent.Install, req ir.WriteRequest) (ir.WriteResult, error) {
	fsys, pa := h.FS(), h.Path()
	capacity, err := m.ContextCapacity(ctx, h, in, nil)
	if err != nil {
		return ir.WriteResult{}, err
	}
	if err = capacity.Check(req.Items); err != nil {
		return ir.WriteResult{}, err
	}

	var file string
	var from int64
	var b strings.Builder
	index := 0
	var ordinal *uint64
	provenance := map[string]ir.Item{}
	sid := req.SessionID
	switch req.Mode {
	case ir.WriteNew:
		if sid == "" {
			sid = newUUID()
		}
		now := time.Now()
		file = pa.Join(in.Root(home), "sessions", now.Format("2006"), now.Format("01"), now.Format("02"),
			"rollout-"+now.Format("2006-01-02T15-04-05")+"-"+sid+".jsonl")
		created := req.Header.Created
		if created.IsZero() {
			created = now
		}
		index++
		writeLine(&b, created, "session_meta", map[string]any{
			"id": sid, "timestamp": stamp(created), "cwd": req.Header.CWD, "originator": "hopsesh",
			"cli_version": in.Version, "source": "cli", "model_provider": "openai",
		})
	case ir.WriteAppend:
		l, err := m.List(ctx, h, in)
		if err != nil {
			return ir.WriteResult{}, err
		}
		for _, s := range l.Sessions {
			if string(s.Key.Session) == sid {
				file = s.Path
			}
		}
		if file == "" {
			return ir.WriteResult{}, fmt.Errorf("%w: %s", agent.ErrNotFound, sid)
		}
		fi, err := fsys.Stat(file)
		if err != nil {
			return ir.WriteResult{}, err
		}
		if fi.Size() != req.Expect.Offset {
			return ir.WriteResult{}, fmt.Errorf("%w: %s changed since it was read", agent.ErrDiverged, sid)
		}
		head, err := fsys.ReadFile(file, 1<<30)
		if err != nil {
			return ir.WriteResult{}, err
		}
		mt, err := firstMeta(head)
		if err != nil {
			return ir.WriteResult{}, err
		}

		if req.Expect.Head != "" {
			seg, e := m.Read(ctx, h, in, agent.Summary{Key: agent.SessionKey{Agent: id, Session: agent.SessionID(sid)}, Path: file}, ir.Cursor{})
			if e != nil {
				return ir.WriteResult{}, e
			}
			if seg.Cursor.Head != req.Expect.Head {
				return ir.WriteResult{}, fmt.Errorf("%w: native head changed", agent.ErrDiverged)
			}
		}

		capacity, err = m.ContextCapacity(ctx, h, in, &agent.Summary{Path: file})
		if err != nil {
			return ir.WriteResult{}, err
		}
		if err = capacity.Check(req.Items); err != nil {
			return ir.WriteResult{}, err
		}
		from = fi.Size()
		recs, end, err := readLines(strings.NewReader(string(head)))
		if err != nil {
			return ir.WriteResult{}, err
		}
		if end != fi.Size() {
			return ir.WriteResult{}, fmt.Errorf("%w: incomplete native record", agent.ErrDiverged)
		}
		ordinal, err = paginatedNext(mt, recs)
		if err != nil {
			return ir.WriteResult{}, err
		}
		index = len(recs)
	default:
		return ir.WriteResult{}, fmt.Errorf("unknown write mode %q", req.Mode)
	}
	for _, it := range req.Items {
		provenance[fmt.Sprintf("line:%d/0", index+1)] = it
		index += 2
		ts := it.Time
		if ts.IsZero() {
			ts = time.Now()
		}
		if it.Role == ir.RoleUser {
			writeLine(&b, ts, "event_msg", map[string]any{"type": "user_message", "message": it.Text, "images": []string{}})
			writeLine(&b, ts, "response_item", map[string]any{"type": "message", "role": "user", "content": []any{map[string]any{"type": "input_text", "text": it.Text}}})
			continue
		}
		writeLine(&b, ts, "event_msg", map[string]any{"type": "agent_message", "message": it.Text})
		writeLine(&b, ts, "response_item", map[string]any{"type": "message", "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": it.Text}}})
	}
	body := []byte(b.String())
	if ordinal != nil && len(body) > 0 {
		var numbered strings.Builder
		next := *ordinal
		for _, raw := range strings.Split(strings.TrimSuffix(string(body), "\n"), "\n") {
			var record map[string]json.RawMessage
			if err := json.Unmarshal([]byte(raw), &record); err != nil {
				return ir.WriteResult{}, err
			}
			record["ordinal"], _ = json.Marshal(next)
			next++
			encoded, err := marshal(record)
			if err != nil {
				return ir.WriteResult{}, err
			}
			numbered.Write(encoded)
			numbered.WriteByte('\n')
		}
		body = []byte(numbered.String())
	}
	if req.Mode == ir.WriteNew {
		err = fsys.WriteFile(file, body, 0o600)
	} else {
		err = fsys.Append(file, body, agent.AppendOptions{NewLine: true})
	}
	if err != nil {
		return ir.WriteResult{}, err
	}
	if req.Header.Title != "" { // on an append, this also clears a "continued in" mark
		if err := setName(h, in, sid, req.Header.Title); err != nil {
			return ir.WriteResult{}, err
		}
	}
	fi, err := fsys.Stat(file)
	if err != nil {
		return ir.WriteResult{}, err
	}
	seg, err := m.Read(ctx, h, in, agent.Summary{Key: agent.SessionKey{Agent: id, Session: agent.SessionID(sid)}, Path: file}, ir.Cursor{})
	if err != nil {
		return ir.WriteResult{}, err
	}
	res := ir.WriteResult{SessionID: sid, Path: file, From: from, To: fi.Size(), Cursor: seg.Cursor}
	for _, n := range seg.Nodes {
		if n.Native != nil {
			if it, ok := provenance[n.Native.Anchor]; ok {
				res.Projection = append(res.Projection, ir.ProjectionFor(n, it))
			}
		}
	}
	return res, nil
}

func writeLine(b *strings.Builder, ts time.Time, typ string, payload any) {
	p, _ := marshal(payload)
	line, _ := marshal(map[string]any{"timestamp": stamp(ts), "type": typ, "payload": json.RawMessage(p)})
	b.Write(line)
	b.WriteByte('\n')
}

func stamp(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05.000Z") }

func marshal(v any) ([]byte, error) {
	var b strings.Builder
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return []byte(strings.TrimSuffix(b.String(), "\n")), nil
}

func newUUID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
