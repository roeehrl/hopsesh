package codex

import (
	"context"
	"encoding/json"
	"io"
	"strings"

	"github.com/roeehrl/hopsesh/sdk/agent"
	"github.com/roeehrl/hopsesh/sdk/ir"
)

var (
	_ agent.Previewer = (*Module)(nil)
	_ agent.Renamer   = (*Module)(nil)
)

// previewWindow is the tail a preview reads first; it doubles, up to previewMaxWindow,
// while it holds fewer messages than asked for. Variables so tests can use small files.
var (
	previewWindow    int64 = 256 << 10
	previewMaxWindow int64 = 2 << 20
)

// Preview reads the end of the rollout, within a tail window of the file (never the whole
// of it), and the first prompt from its head.
func (m *Module) Preview(ctx context.Context, h agent.Host, in agent.Install, s agent.Summary, n int) (agent.Preview, error) {
	if err := ctx.Err(); err != nil {
		return agent.Preview{}, err
	}
	f, err := h.FS().Open(s.Path)
	if err != nil {
		return agent.Preview{}, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return agent.Preview{}, err
	}
	size := fi.Size()
	var (
		buf   []byte
		start = size
		p     agent.Preview
		ls    []line
	)
	for win := min(previewWindow, previewMaxWindow); ; win = min(win*2, previewMaxWindow) {
		if err := ctx.Err(); err != nil {
			return agent.Preview{}, err
		}
		from := max(size-win, 0)
		if from < start {
			more := make([]byte, start-from)
			if _, err := f.ReadAt(more, from); err != nil && err != io.EOF {
				return agent.Preview{}, err
			}
			buf, start = append(more, buf...), from
		}
		ls = lines(buf, start > 0)
		p = agent.BuildPreview(previewItems(ls), n, start > 0)
		if p.Messages() >= n || start == 0 || win >= previewMaxWindow {
			break
		}
	}
	if err := ctx.Err(); err != nil {
		return agent.Preview{}, err
	}
	head := ls
	if start > 0 {
		b := make([]byte, min(size, headChunk))
		if _, err := f.ReadAt(b, 0); err != nil && err != io.EOF {
			return agent.Preview{}, err
		}
		head = lines(b, false)
	}
	for _, l := range head {
		if t := agent.PreviewText(userPrompt(l)); t != "" {
			p.First = &agent.PreviewItem{Role: agent.PreviewUser, Text: t, Time: parseTime(l.Timestamp)}
			break
		}
	}
	return p, nil
}

// previewItems pairs event/response copies of each message, keeping unmatched events
// (including a live turn's newest message). A window can contain both formats.
func previewItems(ls []line) []agent.PreviewItem {
	structured := false
	pairedEvents := map[int]bool{}
	pending := map[string][]int{}
	for i, l := range ls {
		if l.Type == "event_msg" {
			for _, nd := range fromEvent(l, parseTime(l.Timestamp)) {
				structured = structured || nd.Kind == ir.KindToolCall
			}
		}
		role, text := previewMessage(l)
		if role == "" || text == "" {
			continue
		}
		key := role + "\x00" + strings.TrimSpace(text)
		candidates := pending[key]
		match := -1
		for j, prev := range candidates {
			if ls[prev].Type != l.Type {
				match = j
				break
			}
		}
		if match < 0 {
			pending[key] = append(candidates, i)
			continue
		}
		prev := candidates[match]
		pending[key] = append(candidates[:match], candidates[match+1:]...)
		if l.Type == "event_msg" {
			pairedEvents[i] = true
		} else {
			pairedEvents[prev] = true
		}
	}
	var out []agent.PreviewItem
	add := func(role agent.PreviewRole, text string, l line) {
		out = append(out, agent.PreviewItem{Role: role, Text: text, Time: parseTime(l.Timestamp)})
	}
	call := func(k ir.ToolKind, l line) {
		out = append(out, agent.PreviewItem{Role: agent.PreviewTools, Time: parseTime(l.Timestamp), Tools: map[ir.ToolKind]int{k: 1}})
	}
	for i, l := range ls {
		switch l.Type {
		case "compacted":
			add(agent.PreviewCompacted, "", l)
		case "response_item":
			var ri responseItem
			if json.Unmarshal(l.Payload, &ri) != nil {
				continue
			}
			switch ri.Type {
			case "message":
				switch ri.Role {
				case "user":
					if t := userPrompt(l); t != "" {
						add(agent.PreviewUser, withImages(t, imageParts(l.Payload)), l)
					}
				case "assistant":
					var parts []string
					for _, c := range ri.Content {
						if c.Type == "output_text" {
							parts = append(parts, c.Text)
						}
					}
					add(agent.PreviewAgent, strings.Join(parts, "\n\n"), l)
				}
			case "compaction", "compaction_summary":
				add(agent.PreviewCompacted, "", l)
			case "function_call", "custom_tool_call":
				if !structured {
					call(legacyCall(ri).Kind, l)
				}
			case "local_shell_call":
				if !structured {
					call(ir.ToolExecute, l)
				}
			case "web_search_call":
				if !structured {
					call(ir.ToolFetch, l)
				}
			}
		case "event_msg":
			var e struct {
				Type    string   `json:"type"`
				Message string   `json:"message"`
				Images  []string `json:"images"`
			}
			if json.Unmarshal(l.Payload, &e) != nil {
				continue
			}
			switch e.Type {
			case "user_message":
				if t := userText(e.Message); t != "" && !pairedEvents[i] {
					add(agent.PreviewUser, withImages(t, len(e.Images)), l)
				}
			case "agent_message":
				if !pairedEvents[i] {
					add(agent.PreviewAgent, e.Message, l)
				}
			case "context_compacted":
				add(agent.PreviewCompacted, "", l)
			case "item_completed":
				if structured {
					for _, nd := range fromEvent(l, parseTime(l.Timestamp)) {
						if nd.Kind == ir.KindToolCall {
							call(nd.Tool.Kind, l)
						}
					}
				}
			}
		}
	}
	return out
}

// imageParts counts the images attached to a response item message.
func imageParts(payload json.RawMessage) int {
	var r struct {
		Content []struct {
			Type string `json:"type"`
		} `json:"content"`
	}
	_ = json.Unmarshal(payload, &r)
	n := 0
	for _, c := range r.Content {
		if c.Type == "input_image" {
			n++
		}
	}
	return n
}

// withImages adds a mark to a prompt for the images attached to it.
func withImages(text string, images int) string {
	return text + strings.Repeat(" "+agent.ImageMark, images)
}

// Rename names the thread in session_index.jsonl, as Codex's own rename does (a legacy
// title label goes with the old name).
func (m *Module) Rename(_ context.Context, h agent.Host, in agent.Install, s agent.Summary, title string) error {
	t, err := agent.CheckTitle(title)
	if err != nil {
		return err
	}
	return setName(h, in, string(s.Key.Session), t)
}

// previewMessage extracts only public user/assistant text for duplicate matching.
func previewMessage(l line) (string, string) {
	switch l.Type {
	case "event_msg":
		var e struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		}
		if json.Unmarshal(l.Payload, &e) == nil {
			switch e.Type {
			case "user_message":
				return "user", e.Message
			case "agent_message":
				return "assistant", e.Message
			}
		}
	case "response_item":
		var r responseItem
		if json.Unmarshal(l.Payload, &r) == nil && r.Type == "message" && (r.Role == "user" || r.Role == "assistant") {
			var parts []string
			for _, c := range r.Content {
				if c.Type == "input_text" || c.Type == "output_text" {
					parts = append(parts, c.Text)
				}
			}
			return r.Role, strings.Join(parts, "\n")
		}
	}
	return "", ""
}
