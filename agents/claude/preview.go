package claude

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

// Preview reads the end of the transcript's active branch: from the newest message back
// along parentUuid, within a tail window of the file (never the whole of it).
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
		recs  []record
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
		recs = parseLines(buf, start > 0)
		items, rooted := previewItems(recs)
		p = agent.BuildPreview(items, n, start > 0 && !rooted)
		if p.Messages() >= n || start == 0 || rooted || win >= previewMaxWindow {
			break
		}
	}
	if err := ctx.Err(); err != nil {
		return agent.Preview{}, err
	}
	head := recs
	if start > 0 {
		b := make([]byte, min(size, liteChunk))
		if _, err := f.ReadAt(b, 0); err != nil && err != io.EOF {
			return agent.Preview{}, err
		}
		head = parseLines(b, false)
	}
	for _, r := range head {
		if t := agent.PreviewText(userText(r)); t != "" {
			p.First = &agent.PreviewItem{Role: agent.PreviewUser, Text: t, Time: parseTime(r.Timestamp)}
			break
		}
	}
	return p, nil
}

// previewItems walks the active branch back from the newest message (the tail-only form of
// activeBranch) and returns its items, oldest first. rooted reports that the walk reached
// the start of the conversation inside the window.
func previewItems(recs []record) (items []agent.PreviewItem, rooted bool) {
	byUUID := map[string]int{}
	leaf := -1
	for i, r := range recs {
		if r.UUID == "" {
			continue
		}
		byUUID[r.UUID] = i
		if !r.IsSidechain && (r.Type == "user" || r.Type == "assistant") {
			leaf = i
		}
	}
	if leaf < 0 {
		return nil, false
	}
	var chain []int
	seen := map[int]bool{}
	for i := leaf; !seen[i]; {
		seen[i] = true
		chain = append(chain, i)
		r := recs[i]
		parent := r.LogicalParentUUID // across a compaction boundary
		if r.ParentUUID != nil {
			parent = *r.ParentUUID
		}
		if parent == "" {
			rooted = true
			break
		}
		j, ok := byUUID[parent]
		if !ok {
			break // the branch goes on before the window
		}
		i = j
	}
	for k := len(chain) - 1; k >= 0; k-- {
		items = append(items, previewOf(recs[chain[k]])...)
	}
	return items, rooted
}

// previewOf turns one record of the branch into preview items: a prompt, the agent's text
// blocks, a line per tool call, or a compaction marker. Thinking, tool results, meta and
// sidechain records give none.
func previewOf(r record) []agent.PreviewItem {
	ts := parseTime(r.Timestamp)
	switch {
	case r.IsSidechain || r.IsMeta:
		return nil
	case r.Type == "system" && r.Subtype == "compact_boundary", r.Type == "user" && r.IsCompactSummary:
		return []agent.PreviewItem{{Role: agent.PreviewCompacted, Time: ts}}
	case r.Type == "user":
		if t := userText(r); t != "" {
			return []agent.PreviewItem{{Role: agent.PreviewUser, Text: t, Time: ts}}
		}
	case r.Type == "assistant" && r.Message != nil:
		var s string
		if json.Unmarshal(r.Message.Content, &s) == nil {
			return []agent.PreviewItem{{Role: agent.PreviewAgent, Text: s, Time: ts}}
		}
		var blocks []block
		if json.Unmarshal(r.Message.Content, &blocks) != nil {
			return nil
		}
		var out []agent.PreviewItem
		for _, b := range blocks {
			switch b.Type {
			case "text":
				out = append(out, agent.PreviewItem{Role: agent.PreviewAgent, Text: b.Text, Time: ts})
			case "tool_use":
				out = append(out, agent.PreviewItem{Role: agent.PreviewTools, Time: ts, Tools: map[ir.ToolKind]int{toolCall(b).Kind: 1}})
			}
		}
		return out
	}
	return nil
}

// userText is a user record's prompt in full, as realPrompt decides what is one, with a
// mark for an attached image its text does not mention. An interruption notice is not a
// prompt.
func userText(r record) string {
	if r.IsSidechain {
		return ""
	}
	t := promptText(r)
	if t == "" || strings.HasPrefix(t, "[Request interrupted by user") {
		return ""
	}
	if !strings.Contains(t, "[Image") && hasImage(r.Message.Content) {
		t += " " + agent.ImageMark
	}
	return t
}

// hasImage reports whether message content holds an image block.
func hasImage(raw json.RawMessage) bool {
	var blocks []struct {
		Type string `json:"type"`
	}
	if json.Unmarshal(raw, &blocks) != nil {
		return false
	}
	for _, b := range blocks {
		if b.Type == "image" {
			return true
		}
	}
	return false
}

// Rename retitles the session with a custom-title record, what /rename writes, keeping a
// copy's "moved" mark and the file's time.
func (m *Module) Rename(_ context.Context, h agent.Host, in agent.Install, s agent.Summary, title string) error {
	t, err := agent.CheckTitle(title)
	if err != nil {
		return err
	}
	if s.Mark != nil {
		t = agent.MarkTitle(*s.Mark, t)
	}
	rec := encodeRecord(map[string]any{"type": "custom-title", "customTitle": t, "sessionId": string(s.Key.Session)})
	return h.FS().Append(s.Path, append(rec, '\n'), agent.AppendOptions{NewLine: true, KeepMtime: true, Standalone: true})
}
