package opencode

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/roeehrl/hopsesh/sdk/agent"
	"github.com/roeehrl/hopsesh/sdk/ir"
)

// Preview reads the end of a session: its newest messages from the database (a window
// that grows until it holds n messages), or the whole session from OpenCode's export, and
// its first prompt.
func (m *Module) Preview(ctx context.Context, h agent.Host, in agent.Install, s agent.Summary, n int) (agent.Preview, error) {
	if r, ok, err := openSQL(ctx, h, in); ok {
		var p agent.Preview
		if err == nil {
			p, err = r.preview(ctx, s.Key.Session, n)
		}
		if !readUnsupported(err) {
			return p, err
		}
	}
	_, msgs, err := m.readSession(ctx, h, in, s)
	if err != nil {
		return agent.Preview{}, err
	}
	p := agent.BuildPreview(previewItems(msgs), n, false)
	p.First = firstPrompt(msgs)
	return p, nil
}

func (r sqlReader) preview(ctx context.Context, sid agent.SessionID, n int) (agent.Preview, error) {
	window := max(4*n, 32)
	for {
		_, msgs, earlier, err := r.tail(ctx, sid, window)
		if err != nil {
			return agent.Preview{}, err
		}
		p := agent.BuildPreview(previewItems(msgs), n, earlier)
		if p.Messages() >= n || !earlier || window >= 1<<14 {
			if !earlier {
				p.First = firstPrompt(msgs)
			} else {
				p.First = r.firstPrompt(ctx, sid)
			}
			return p, nil
		}
		window *= 4
	}
}

const firstPromptQuery = `SELECT id, type, seq, data FROM session_message WHERE session_id = ? AND type = 'user' ORDER BY seq LIMIT 1`

func (r sqlReader) firstPrompt(ctx context.Context, sid agent.SessionID) *agent.PreviewItem {
	rows, err := r.sq.Query(ctx, r.db, firstPromptQuery, string(sid))
	if err != nil {
		return nil
	}
	msgs, err := joinParts(rows, agent.Rows{})
	if err != nil {
		return nil
	}
	return firstPrompt(msgs)
}

func firstPrompt(msgs []message) *agent.PreviewItem {
	for _, msg := range msgs {
		if msg.Type != "user" {
			continue
		}
		if t := agent.PreviewText(msg.prompt()); t != "" {
			return &agent.PreviewItem{Role: agent.PreviewUser, Text: t, Time: millis(msg.Time.Created)}
		}
	}
	return nil
}

// previewItems are messages as a preview shows them: prompts, replies, a line for each
// turn's tool calls (the person's own shell commands too), and compactions.
func previewItems(msgs []message) []agent.PreviewItem {
	var out []agent.PreviewItem
	for _, msg := range msgs {
		ts := millis(msg.Time.Created)
		switch msg.Type {
		case "user":
			out = append(out, agent.PreviewItem{Role: agent.PreviewUser, Text: msg.prompt(), Time: ts})
		case "assistant":
			for _, it := range msg.items() {
				switch it.Type {
				case "text":
					out = append(out, agent.PreviewItem{Role: agent.PreviewAgent, Text: it.Text, Time: ts})
				case "tool":
					call, _ := toolNodes(it)
					out = append(out, agent.PreviewItem{Role: agent.PreviewTools, Time: ts, Tools: map[ir.ToolKind]int{call.Kind: 1}})
				}
			}
		case "shell":
			out = append(out, agent.PreviewItem{Role: agent.PreviewTools, Time: ts, Tools: map[ir.ToolKind]int{ir.ToolExecute: 1}})
		case "compaction":
			if msg.Status == "completed" {
				out = append(out, agent.PreviewItem{Role: agent.PreviewCompacted, Time: ts})
			}
		}
	}
	return out
}

// Rename gives the session a new title the way OpenCode's own rename does (PATCH
// /api/session/<id>), so OpenCode and its desktop app show it. OpenCode names a session
// only while it is untitled, so the title stays.
func (m *Module) Rename(ctx context.Context, h agent.Host, in agent.Install, s agent.Summary, title string) error {
	t, err := agent.CheckTitle(title)
	if err != nil {
		return err
	}
	svc := probeService(ctx, h)
	if svc.standalone() {
		// A private server opens the database: its OpenCode must read what is there.
		newest := ""
		if r, ok, err := openSQL(ctx, h, in); ok {
			if err == nil {
				newest, err = r.maxVersion(ctx)
			}
			if err != nil && !readUnsupported(err) {
				return err
			}
		}
		if err := checkStandalone(svc, in.Version, newest); err != nil {
			return err
		}
	}
	body, _ := json.Marshal(map[string]string{"title": t})
	res, err := api(ctx, h, svc, "PATCH", "/api/session/"+string(s.Key.Session), body)
	if err != nil {
		return err
	}
	if res.Code != 0 {
		return fmt.Errorf("OpenCode did not rename the session: %w", apiError("PATCH", "/api/session/"+string(s.Key.Session), res))
	}
	return nil
}
