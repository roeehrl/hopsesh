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

// window is a conservative usable context for Codex's default models.
const window = 272_000

// Profile: Codex's own tool calls cannot be forged safely, so history arrives as text.
func (m *Module) Profile(agent.Install) ir.Profile { return ir.Profile{Window: window} }

// Write emits a legacy-mode rollout (no ordinals): session_meta, then each message as a
// response_item with its user_message/agent_message event (Codex titles threads from
// those events). Appends go only to rollouts in that mode.
func (m *Module) Write(ctx context.Context, h agent.Host, in agent.Install, req ir.WriteRequest) (ir.WriteResult, error) {
	fsys, pa := h.FS(), h.Path()
	var file string
	var from int64
	var b strings.Builder
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
		if mt.HistoryMode == "paginated" {
			return ir.WriteResult{}, fmt.Errorf("%w: Codex thread %s keeps paginated history, which hopsesh does not extend", agent.ErrUnsupported, sid)
		}
		from = fi.Size()
	default:
		return ir.WriteResult{}, fmt.Errorf("unknown write mode %q", req.Mode)
	}
	for _, it := range req.Items {
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
	var err error
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
	return ir.WriteResult{SessionID: sid, Path: file, From: from, To: fi.Size(), Cursor: seg.Cursor}, nil
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
