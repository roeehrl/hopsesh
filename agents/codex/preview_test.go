package codex

import (
	"context"
	"encoding/json"
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

type rec map[string]any

// rolloutOf writes records as a rollout on a fresh in-memory machine.
func rolloutOf(t *testing.T, recs []rec) (*agenttest.FakeHost, string) {
	t.Helper()
	var b strings.Builder
	for _, r := range recs {
		j, err := json.Marshal(r)
		if err != nil {
			t.Fatal(err)
		}
		b.Write(j)
		b.WriteByte('\n')
	}
	h := agenttest.NewFakeHost("/home/u")
	p := "/home/u/.codex/sessions/2026/10/01/rollout-2026-10-01T13-00-00-" + t1 + ".jsonl"
	h.Put(p, []byte(b.String()), time.Now())
	return h, p
}

func resp(ts string, payload rec) rec {
	return rec{"timestamp": "2026-10-01T10:" + ts + "Z", "type": "response_item", "payload": payload}
}

func event(ts string, payload rec) rec {
	return rec{"timestamp": "2026-10-01T10:" + ts + "Z", "type": "event_msg", "payload": payload}
}

func said(role, typ, text string) rec {
	return rec{"type": "message", "role": role, "content": []rec{{"type": typ, "text": text}}}
}

// thread is a rollout with each message written twice (event and response item), raw
// tool calls, a compaction and an image.
func thread(padding int) []rec {
	rs := []rec{
		{"timestamp": "2026-10-01T10:00:00Z", "type": "session_meta", "payload": rec{"id": t1, "cwd": "/repo", "cli_version": "0.153.2", "source": "cli"}},
		event("00:01", rec{"type": "task_started"}),
		resp("00:02", said("developer", "input_text", "You are Codex.")),
		resp("00:03", said("user", "input_text", "<environment_context><cwd>/repo</cwd></environment_context>")),
		event("00:04", rec{"type": "user_message", "message": "Rename the flag", "images": []string{}}),
		resp("00:04", said("user", "input_text", "Rename the flag")),
		resp("00:05", rec{"type": "reasoning", "summary": []rec{{"type": "summary_text", "text": "think"}}}),
		event("00:06", rec{"type": "agent_message", "message": "Looking at the flag."}),
		resp("00:06", said("assistant", "output_text", "Looking at the flag.")),
		resp("00:07", rec{"type": "function_call", "name": "shell", "arguments": `{"command":["bash","-lc","rg flag"]}`, "call_id": "c1"}),
		resp("00:08", rec{"type": "function_call_output", "call_id": "c1", "output": "x"}),
		resp("00:09", rec{"type": "custom_tool_call", "name": "apply_patch", "input": "*** Begin Patch", "call_id": "c2"}),
		resp("00:10", rec{"type": "custom_tool_call_output", "call_id": "c2", "output": "ok"}),
		resp("00:11", rec{"type": "function_call", "name": "update_plan", "arguments": "{}", "call_id": "c3"}),
		event("00:12", rec{"type": "agent_message", "message": "Renamed it."}),
		resp("00:12", said("assistant", "output_text", "Renamed it.")),
		event("00:13", rec{"type": "task_complete"}),
	}
	for i := 0; i < padding; i++ {
		rs = append(rs,
			resp("01:00", said("user", "input_text", fmt.Sprintf("padding prompt %d %s", i, strings.Repeat("x", 100)))),
			resp("01:01", said("assistant", "output_text", fmt.Sprintf("padding reply %d", i))))
	}
	return append(rs,
		rec{"timestamp": "2026-10-01T10:02:00Z", "type": "compacted", "payload": rec{"message": "summary of the work"}},
		event("02:01", rec{"type": "context_compacted"}),
		event("03:00", rec{"type": "user_message", "message": "Now update the docs", "images": []string{"data:image/png;base64,AAAA"}}),
		resp("03:00", rec{"type": "message", "role": "user", "content": []rec{{"type": "input_text", "text": "Now update the docs"}, {"type": "input_image", "image_url": "data:image/png;base64,AAAA"}}}),
		resp("03:01", said("assistant", "output_text", "Docs updated.")),
	)
}

func shape(p agent.Preview) string {
	var out []string
	for _, it := range p.Items {
		switch it.Role {
		case agent.PreviewTools:
			out = append(out, fmt.Sprintf("tools(execute=%d edit=%d plan=%d)", it.Tools[ir.ToolExecute], it.Tools[ir.ToolEdit], it.Tools[ir.ToolPlan]))
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

func TestPreviewThread(t *testing.T) {
	h, file := rolloutOf(t, thread(0))
	p := preview(t, h, file, 10)
	want := "user:Rename the flag | tools(execute=1 edit=1 plan=1) | agent:Renamed it. | compacted | user:Now update the docs ‹image› | agent:Docs updated."
	if got := shape(p); got != want || p.More {
		t.Fatalf("preview:\n got %s\nwant %s (more=%v)", got, want, p.More)
	}
	if p.First == nil || p.First.Text != "Rename the flag" || p.First.Time.Format("15:04:05") != "10:00:04" {
		t.Fatalf("first: %+v", p.First)
	}
	p = preview(t, h, file, 3)
	if got := shape(p); got != "agent:Renamed it. | compacted | user:Now update the docs ‹image› | agent:Docs updated." || !p.More {
		t.Fatalf("n=3: %s (more=%v)", got, p.More)
	}
}

// A rollout with events only (no response item messages) is read from its events.
func TestPreviewEventsOnly(t *testing.T) {
	h, file := rolloutOf(t, []rec{
		{"timestamp": "2026-10-01T10:00:00Z", "type": "session_meta", "payload": rec{"id": t1, "cwd": "/repo"}},
		event("00:01", rec{"type": "user_message", "message": "hello there"}),
		event("00:02", rec{"type": "agent_message", "message": "Hi."}),
	})
	if got := shape(preview(t, h, file, 5)); got != "user:hello there | agent:Hi." {
		t.Fatalf("events: %s", got)
	}
}

// Structured item_completed records count the tools, as Read reads them.
func TestPreviewStructured(t *testing.T) {
	h, in, byID := previewSetup(t)
	p, err := New().Preview(context.Background(), h, in, byID[t1], 5)
	if err != nil {
		t.Fatal(err)
	}
	want := "user:What is the codeword in notes.txt? | tools(execute=1 edit=1 plan=0) | agent:The codeword is FIG-3; I saved it to answer.txt."
	if got := shape(p); got != want {
		t.Fatalf("structured:\n got %s\nwant %s", got, want)
	}
}

func TestPreviewTailWindow(t *testing.T) {
	defer func(w, m int64) { previewWindow, previewMaxWindow = w, m }(previewWindow, previewMaxWindow)
	previewWindow, previewMaxWindow = 2<<10, 8<<10
	fh, file := rolloutOf(t, thread(1500))
	b, _ := fh.Get(file)
	size := int64(len(b))
	if size < 2*headChunk {
		t.Fatalf("the rollout is too small to test a window: %d", size)
	}
	var read atomic.Int64
	h := countingHost{fh, &read}
	p := preview(t, h, file, 2)
	if got := shape(p); got != "user:Now update the docs ‹image› | agent:Docs updated." || !p.More {
		t.Fatalf("n=2: %s (more=%v)", got, p.More)
	}
	if p.First == nil || p.First.Text != "Rename the flag" {
		t.Fatalf("the first prompt comes from the head: %+v", p.First)
	}
	if r := read.Load(); r > previewWindow+headChunk {
		t.Fatalf("read %d bytes of %d for two messages", r, size)
	}
	read.Store(0)
	p = preview(t, h, file, 1000)
	if p.Messages() < 10 || !p.More || strings.Contains(shape(p), "Rename the flag") {
		t.Fatalf("a capped window: %d messages, more=%v", p.Messages(), p.More)
	}
	if r := read.Load(); r > previewMaxWindow+headChunk {
		t.Fatalf("read %d bytes of %d", r, size)
	}
}

func TestRename(t *testing.T) {
	h, in, byID := previewSetup(t)
	m, ctx := New(), context.Background()
	if err := m.Rename(ctx, h, in, byID[t1], " Codeword hunt "); err != nil {
		t.Fatal(err)
	}
	b, err := h.FS().ReadFile("/home/u/.codex/session_index.jsonl", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	ls := strings.Split(strings.TrimSpace(string(b)), "\n")
	var e struct {
		ID   string `json:"id"`
		Name string `json:"thread_name"`
	}
	if json.Unmarshal([]byte(ls[len(ls)-1]), &e) != nil || e.ID != t1 || e.Name != "Codeword hunt" {
		t.Fatalf("appended line: %s", ls[len(ls)-1])
	}
	l, _ := m.List(ctx, h, in)
	for _, s := range l.Sessions {
		if string(s.Key.Session) == t1 && (s.Title != "Codeword hunt" || s.TitleSource != "custom") {
			t.Fatalf("renamed: %q/%q", s.Title, s.TitleSource)
		}
	}
	for _, bad := range []string{"", "a\tb", strings.Repeat("x", 201)} {
		if err := m.Rename(ctx, h, in, byID[t1], bad); err == nil {
			t.Errorf("Rename(%q) accepted", bad)
		}
	}
	if !agent.Has(m, agent.CapPreview) || !agent.Has(m, agent.CapRename) {
		t.Fatalf("capabilities: %v", agent.Capabilities(m))
	}
}

func previewSetup(t *testing.T) (agent.Host, agent.Install, map[string]agent.Summary) {
	fh := agenttest.NewFakeHost("/home/u")
	if err := fh.Load("testdata/0.153.2", "/home/u/.codex"); err != nil {
		t.Fatal(err)
	}
	fh.AddBinary("codex", "codex-cli 0.153.2")
	m, ctx := New(), context.Background()
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
	return h, in, byID
}

func TestPreviewMixedMessageFormats(t *testing.T) {
	h, file := rolloutOf(t, []rec{
		event("00:01", rec{"type": "user_message", "message": "first"}),
		resp("00:01", said("user", "input_text", "first")),
		resp("00:02", said("assistant", "output_text", "done")),
		event("00:02", rec{"type": "agent_message", "message": "done"}),
		event("00:03", rec{"type": "user_message", "message": "second"}),
		event("00:04", rec{"type": "agent_message", "message": "latest answer"}),
	})
	if got := shape(preview(t, h, file, 10)); got != "user:first | agent:done | user:second | agent:latest answer" {
		t.Fatal(got)
	}
}

func TestPreviewCanceledAndNonPowerOfTwoBound(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := New().Preview(ctx, nil, agent.Install{}, agent.Summary{}, 4); err != context.Canceled {
		t.Fatalf("%v", err)
	}
	defer func(w, m int64) { previewWindow, previewMaxWindow = w, m }(previewWindow, previewMaxWindow)
	previewWindow, previewMaxWindow = 2<<10, 5<<10
	h, file := rolloutOf(t, thread(1500))
	var read atomic.Int64
	preview(t, countingHost{h, &read}, file, 10000)
	if n := read.Load(); n > previewMaxWindow+headChunk {
		t.Fatalf("read %d bytes", n)
	}
}

func TestSummarySkipsCommandTitleAndFallsBackToReply(t *testing.T) {
	h, file := rolloutOf(t, []rec{
		{"type": "session_meta", "payload": rec{"id": t1, "cwd": "/repo"}},
		resp("00:01", said("user", "input_text", "/clear")),
		resp("00:02", said("user", "input_text", "[Pasted text #1 +30 lines]")),
		resp("00:03", said("assistant", "output_text", "## Ready to continue. Next sentence.")),
	})
	fi, err := h.FS().Stat(file)
	if err != nil {
		t.Fatal(err)
	}
	s, err := summarize(h, rollout{path: file, info: fi})
	if err != nil || s == nil || s.Title != "Ready to continue" || s.TitleSource != "reply" {
		t.Fatalf("%+v %v", s, err)
	}
}
