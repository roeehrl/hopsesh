package claude

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/roeehrl/hopsesh/sdk/agent"
	"github.com/roeehrl/hopsesh/sdk/agent/agenttest"
	"github.com/roeehrl/hopsesh/sdk/ir"
)

// countingHost counts the bytes a module reads from files.
type countingHost struct {
	agent.Host
	read *atomic.Int64
}

func (h countingHost) FS() agent.FS { return countingFS{h.Host.FS(), h.read} }

type countingFS struct {
	agent.FS
	read *atomic.Int64
}

func (f countingFS) Open(p string) (agent.File, error) {
	file, err := f.FS.Open(p)
	return countingFile{file, f.read}, err
}

type countingFile struct {
	agent.File
	read *atomic.Int64
}

func (f countingFile) Read(b []byte) (int, error) {
	n, err := f.File.Read(b)
	f.read.Add(int64(n))
	return n, err
}

func (f countingFile) ReadAt(b []byte, off int64) (int, error) {
	n, err := f.File.ReadAt(b, off)
	f.read.Add(int64(n))
	return n, err
}

// msg is a chained transcript record.
func msg(typ, uuid, parent, ts string, content any, extra line) line {
	l := line{"type": typ, "uuid": uuid, "sessionId": "p1", "cwd": "/repo", "timestamp": ts, "isSidechain": false,
		"message": line{"role": typ, "content": content}}
	if parent == "" {
		l["parentUuid"] = nil
	} else {
		l["parentUuid"] = parent
	}
	for k, v := range extra {
		l[k] = v
	}
	return l
}

func text(s string) []line { return []line{{"type": "text", "text": s}} }
func tool(name string) []line {
	return []line{{"type": "tool_use", "id": "t-" + name, "name": name, "input": line{}}}
}

func result() []line { return []line{{"type": "tool_result", "tool_use_id": "t", "content": "ok"}} }

// conversation is a transcript with a finished turn of tools, a compaction, and a branch
// the user rewound away from.
func conversation(padding int) []line {
	ls := []line{
		{"type": "queue-operation", "operation": "enqueue"},
		msg("user", "u1", "", "2026-10-01T10:00:00Z", "Please refactor the parser", nil),
		msg("assistant", "a1", "u1", "2026-10-01T10:00:01Z", text("Sure."), nil),
	}
	prev := "a1"
	for i := 0; i < padding; i++ {
		u, a := fmt.Sprintf("pu%d", i), fmt.Sprintf("pa%d", i)
		ls = append(ls, msg("user", u, prev, "2026-10-01T10:01:00Z", fmt.Sprintf("padding prompt %d %s", i, strings.Repeat("x", 100)), nil),
			msg("assistant", a, u, "2026-10-01T10:01:01Z", text(fmt.Sprintf("padding reply %d", i)), nil))
		prev = a
	}
	return append(ls,
		msg("user", "u2", prev, "2026-10-01T10:02:00Z", "Also add tests", nil),
		msg("assistant", "a2", "u2", "2026-10-01T10:02:01Z", text("I'll start with the reader."), nil),
		msg("assistant", "a3", "a2", "2026-10-01T10:02:02Z", tool("Read"), nil),
		msg("user", "r1", "a3", "2026-10-01T10:02:03Z", result(), line{"toolUseResult": line{"stdout": "ok"}}),
		msg("assistant", "a4", "r1", "2026-10-01T10:02:04Z", tool("Edit"), nil),
		msg("assistant", "a5", "a4", "2026-10-01T10:02:05Z", tool("Bash"), nil),
		msg("user", "r2", "a5", "2026-10-01T10:02:06Z", result(), line{"toolUseResult": line{"stdout": "ok"}}),
		msg("assistant", "a6", "r2", "2026-10-01T10:02:07Z", []line{{"type": "thinking", "thinking": "hmm", "signature": "s"}}, nil),
		msg("assistant", "a7", "a6", "2026-10-01T10:02:08Z", text("Done: the tests pass.\n\n```\nok parser\n```"), nil),
		line{"type": "system", "subtype": "compact_boundary", "uuid": "c1", "parentUuid": nil, "logicalParentUuid": "a7",
			"sessionId": "p1", "timestamp": "2026-10-01T10:03:00Z", "content": "Conversation compacted"},
		msg("user", "c2", "c1", "2026-10-01T10:03:01Z", "This session is being continued from a previous conversation.", line{"isCompactSummary": true}),
		msg("user", "x1", "c2", "2026-10-01T10:04:00Z", "abandoned prompt", nil),
		msg("assistant", "x2", "x1", "2026-10-01T10:04:01Z", text("abandoned reply"), nil),
		msg("user", "m1", "c2", "2026-10-01T10:05:00Z", "<system-reminder>internal</system-reminder>", line{"isMeta": true}),
		msg("user", "u4", "m1", "2026-10-01T10:05:01Z", "Now the docs ![diagram](d.png)", nil),
		msg("assistant", "a8", "u4", "2026-10-01T10:05:02Z", text("Docs updated."), nil),
		line{"type": "custom-title", "customTitle": "Parser", "sessionId": "p1"},
		line{"type": "last-prompt", "lastPrompt": "Now the docs", "sessionId": "p1", "leafUuid": "a8"},
	)
}

func shape(p agent.Preview) string {
	var out []string
	for _, it := range p.Items {
		switch it.Role {
		case agent.PreviewTools:
			out = append(out, fmt.Sprintf("tools(read=%d edit=%d execute=%d)", it.Tools[ir.ToolRead], it.Tools[ir.ToolEdit], it.Tools[ir.ToolExecute]))
		case agent.PreviewCompacted:
			out = append(out, "compacted")
		default:
			out = append(out, string(it.Role)+":"+it.Text)
		}
	}
	return strings.Join(out, " | ")
}

func preview(t *testing.T, h agent.Host, file string, n int) agent.Preview {
	t.Helper()
	p, err := New().Preview(context.Background(), h, agent.Install{}, agent.Summary{Path: file}, n)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestPreviewActiveBranch(t *testing.T) {
	file := writeTranscript(t, "/t/"+t.Name(), "p1", conversation(0))
	p := preview(t, fake, file, 10)
	want := "user:Please refactor the parser | agent:Sure. | user:Also add tests | tools(read=1 edit=1 execute=1) | " +
		"agent:Done: the tests pass.\n\n‹code, 1 line› | compacted | user:Now the docs ‹image› | agent:Docs updated."
	if got := shape(p); got != want {
		t.Fatalf("preview:\n got %s\nwant %s", got, want)
	}
	if p.More || p.First == nil || p.First.Text != "Please refactor the parser" || p.First.Time.Format(time.RFC3339) != "2026-10-01T10:00:00Z" {
		t.Fatalf("more=%v first=%+v", p.More, p.First)
	}
	if p.Items[3].Time.Format("15:04:05") != "10:02:05" {
		t.Fatalf("the tool line has the last call's time: %v", p.Items[3].Time)
	}

	if got := shape(preview(t, fake, file, 2)); got != "user:Now the docs ‹image› | agent:Docs updated." {
		t.Fatalf("n=2: %s", got)
	}
	p = preview(t, fake, file, 3)
	if got := shape(p); got != "agent:Done: the tests pass.\n\n‹code, 1 line› | compacted | user:Now the docs ‹image› | agent:Docs updated." || !p.More {
		t.Fatalf("n=3: %s (more=%v)", got, p.More)
	}
}

func TestPreviewTailWindow(t *testing.T) {
	defer func(w, m int64) { previewWindow, previewMaxWindow = w, m }(previewWindow, previewMaxWindow)
	previewWindow, previewMaxWindow = 2<<10, 8<<10
	file := writeTranscript(t, "/t/"+t.Name(), "p1", conversation(600))
	b, _ := fake.Get(file)
	size := int64(len(b))
	if size < 4*liteChunk {
		t.Fatalf("the transcript is too small to test a window: %d", size)
	}
	var read atomic.Int64
	h := countingHost{fake, &read}

	p := preview(t, h, file, 2)
	if got := shape(p); got != "user:Now the docs ‹image› | agent:Docs updated." || !p.More {
		t.Fatalf("n=2: %s (more=%v)", got, p.More)
	}
	if p.First == nil || p.First.Text != "Please refactor the parser" {
		t.Fatalf("the first prompt comes from the head: %+v", p.First)
	}
	if r := read.Load(); r > previewWindow+liteChunk {
		t.Fatalf("read %d bytes of %d for two messages", r, size)
	}

	// More messages than the largest window holds: as many as it has, and More.
	read.Store(0)
	p = preview(t, h, file, 1000)
	if p.Messages() < 10 || !p.More || strings.Contains(shape(p), "Please refactor") {
		t.Fatalf("a capped window: %d messages, more=%v", p.Messages(), p.More)
	}
	if r := read.Load(); r > previewMaxWindow+liteChunk {
		t.Fatalf("read %d bytes of %d", r, size)
	}
	if !strings.HasPrefix(shape(p), "user:padding prompt") && !strings.HasPrefix(shape(p), "agent:padding reply") {
		t.Fatalf("the window starts in the padding: %.80s", shape(p))
	}
}

func TestPreviewEmpty(t *testing.T) {
	file := writeTranscript(t, "/t/"+t.Name(), "p1", []line{{"type": "custom-title", "customTitle": "x"}})
	p := preview(t, fake, file, 5)
	if len(p.Items) != 0 || p.More || p.First != nil {
		t.Fatalf("a transcript without messages: %+v", p)
	}
}

func TestRename(t *testing.T) {
	m, ctx := New(), context.Background()
	fh := newRenameHost(t)
	in, _ := m.Detect(ctx, fh)
	h := agent.Confine(fh, m.Spec(), in)
	l, err := m.List(ctx, h, in)
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]agent.Summary{}
	for _, s := range l.Sessions {
		byID[string(s.Key.Session)] = s
	}
	a, b := byID[s1], byID[s2]
	before, _ := fh.FS().Stat(a.Path)
	if err := m.Rename(ctx, h, in, a, "  Codeword hunt "); err != nil {
		t.Fatal(err)
	}
	body, _ := fh.Get(a.Path)
	last := strings.TrimSpace(string(body)[strings.LastIndex(strings.TrimRight(string(body), "\n"), "\n")+1:])
	if last != `{"type":"custom-title","customTitle":"Codeword hunt","sessionId":"`+s1+`"}` {
		t.Fatalf("appended record: %s", last)
	}
	if after, _ := fh.FS().Stat(a.Path); !after.ModTime().Equal(before.ModTime()) {
		t.Fatal("a rename must keep the file's time")
	}
	// A moved copy keeps its mark.
	if err := m.Rename(ctx, h, in, b, "Flags"); err != nil {
		t.Fatal(err)
	}
	l, _ = m.List(ctx, h, in)
	for _, s := range l.Sessions {
		switch string(s.Key.Session) {
		case s1:
			if s.Title != "Codeword hunt" || s.TitleSource != "custom" {
				t.Errorf("renamed: %q/%q", s.Title, s.TitleSource)
			}
		case s2:
			if s.Title != "Flags" || s.Mark == nil || s.Mark.Location != "studio" {
				t.Errorf("renamed copy: %q %+v", s.Title, s.Mark)
			}
		}
	}
	for _, bad := range []string{"", "a\nb", strings.Repeat("x", 201)} {
		if err := m.Rename(ctx, h, in, a, bad); err == nil {
			t.Errorf("Rename(%q) accepted", bad)
		}
	}
	if !agent.Has(m, agent.CapPreview) || !agent.Has(m, agent.CapRename) {
		t.Fatalf("capabilities: %v", agent.Capabilities(m))
	}
}

func newRenameHost(t *testing.T) *agenttest.FakeHost {
	h := agenttest.NewFakeHost("/home/u")
	if err := h.Load("testdata/2.1.284", "/home/u/.claude"); err != nil {
		t.Fatal(err)
	}
	h.AddBinary("claude", "2.1.284 (Claude Code)")
	return h
}
