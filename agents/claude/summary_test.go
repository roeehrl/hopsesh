package claude

import (
	"encoding/json"
	"path"
	"strings"
	"testing"
	"time"

	"github.com/roeehrl/hopsesh/sdk/agent"
	"github.com/roeehrl/hopsesh/sdk/agent/agenttest"
)

type line map[string]any

var fake = agenttest.NewFakeHost("/home/u")

func writeTranscript(t *testing.T, dir, id string, lines []line) string {
	t.Helper()
	var b strings.Builder
	for _, l := range lines {
		j, err := json.Marshal(l)
		if err != nil {
			t.Fatal(err)
		}
		b.Write(j)
		b.WriteByte('\n')
	}
	p := path.Join(dir, id+".jsonl")
	fake.Put(p, []byte(b.String()), time.Now())
	return p
}

func summ(p string) (*info, error) { return summarize(fake.FS(), agent.PosixPath{}, p, nil, nil) }

func user(cwd, ts string, content any, extra line) line {
	l := line{"type": "user", "uuid": ts, "parentUuid": nil, "sessionId": "s1", "cwd": cwd, "gitBranch": "main",
		"version": "2.1.284", "entrypoint": "cli", "timestamp": ts, "message": line{"role": "user", "content": content}}
	for k, v := range extra {
		l[k] = v
	}
	return l
}

func TestSummarizePriorities(t *testing.T) {
	root := "/t/" + t.Name()
	cfg := path.Join(root, ".claude")
	proj := path.Join(cfg, "projects", "-repo")
	p := writeTranscript(t, proj, "s1", []line{
		{"type": "queue-operation", "operation": "enqueue"}, // line 1 is often not a message
		user("/repo", "2026-10-01T10:00:00Z", "<command-name>/clear</command-name><command-args></command-args>", nil),
		user("/repo", "2026-10-01T10:00:01Z", "fix the flaky test", nil),
		user("/repo", "2026-10-01T10:00:02Z", []line{{"type": "tool_result", "content": "ok"}}, nil),
		user("/repo", "2026-10-01T10:00:03Z", "meta stuff", line{"isMeta": true}),
		user("/repo/sub", "2026-10-01T10:00:04Z", "now in a subdir", nil), // shell moved; project cwd must stay /repo
		user("/repo", "2026-10-01T10:00:05Z", "a peer message", line{"origin": line{"kind": "peer"}}),
		{"type": "ai-title", "aiTitle": "Fix flaky test"},
		{"type": "last-prompt", "lastPrompt": "run it\nagain"},
	})
	s, err := summ(p)
	if err != nil {
		t.Fatal(err)
	}
	if s.Title != "Fix flaky test" || s.TitleSource != "ai" {
		t.Errorf("title %q/%q", s.Title, s.TitleSource)
	}
	if s.LastPrompt != "run it again" {
		t.Errorf("last prompt %q", s.LastPrompt)
	}
	if s.CWD != "/repo" || s.LastCWD != "/repo" {
		t.Errorf("cwd %q last %q", s.CWD, s.LastCWD)
	}
	if s.ID != "s1" || s.Version != "2.1.284" || !s.HasMessages {
		t.Errorf("fields: %+v", s)
	}
	if s.LastActivity.Format("15:04:05") != "10:00:05" {
		t.Errorf("last activity %v", s.LastActivity)
	}
}

func TestSummarizeCustomTitleSidecarAndRelocated(t *testing.T) {
	root := "/t/" + t.Name()
	proj := path.Join(root, "projects", "-new")
	p := writeTranscript(t, proj, "s2", []line{
		user("/old", "2026-10-01T10:00:00Z", "first thing", nil),
		{"type": "relocated", "sessionId": "s2", "relocatedCwd": "/new"},
	})
	fake.Put(path.Join(proj, "s2", "custom-title.json"), []byte(`{"customTitle":"Named by user"}`), time.Now())
	fake.Put(path.Join(proj, "s2", "subagents", "agent-1.jsonl"), []byte("{}\n"), time.Now())
	s, err := summ(p)
	if err != nil {
		t.Fatal(err)
	}
	if s.Title != "Named by user" || s.TitleSource != "custom" {
		t.Errorf("sidecar title: %q/%q", s.Title, s.TitleSource)
	}
	if s.CWD != "/new" {
		t.Errorf("relocated cwd not used: %q", s.CWD)
	}
	if s.LastPrompt != "first thing" || s.Subagents != 1 {
		t.Errorf("prompt %q subagents %d", s.LastPrompt, s.Subagents)
	}
}

func TestSummarizeLargeFileHeadTail(t *testing.T) {
	root := "/t/" + t.Name()
	proj := path.Join(root, "projects", "-big")
	big := strings.Repeat("x", 100*1024) // first prompt line larger than the 64 KB head
	lines := []line{user("/big", "2026-10-01T09:00:00Z", big, nil)}
	for i := 0; i < 2000; i++ {
		lines = append(lines, line{"type": "assistant", "uuid": "a", "sessionId": "s3", "cwd": "/big/elsewhere",
			"timestamp": "2026-10-01T09:30:00Z", "message": line{"role": "assistant", "content": "filler filler filler"}})
	}
	lines = append(lines, line{"type": "custom-title", "customTitle": "Big one"})
	p := writeTranscript(t, proj, "s3", lines)
	s, err := summ(p)
	if err != nil {
		t.Fatal(err)
	}
	if s.CWD != "/big" {
		t.Errorf("launch cwd must be found past a >64KB first line: %q", s.CWD)
	}
	if s.Title != "Big one" {
		t.Errorf("tail custom title missed: %q", s.Title)
	}
	if s.LastCWD != "/big/elsewhere" {
		t.Errorf("last cwd %q", s.LastCWD)
	}
}

func TestRealPromptFilters(t *testing.T) {
	mk := func(content any, extra line) record {
		j, _ := json.Marshal(user("/r", "t", content, extra))
		var r record
		json.Unmarshal(j, &r)
		return r
	}
	cases := []struct {
		r    record
		want string
	}{
		{mk("hello", nil), "hello"},
		{mk("<command-name>/deploy</command-name><command-args>prod now</command-args>", nil), "/deploy prod now"},
		{mk("<command-name>/compact</command-name><command-args>x</command-args>", nil), ""},
		{mk("<bash-input>ls -la</bash-input>", nil), "! ls -la"},
		{mk("<system-reminder>x</system-reminder>", nil), ""},
		{mk("hi", line{"isCompactSummary": true}), ""},
		{mk("hi", line{"toolUseResult": line{"a": 1}}), ""},
		{mk([]line{{"type": "text", "text": "block text"}}, nil), "block text"},
		{mk("x", line{"origin": line{"kind": "task-notification"}}), ""},
		{mk(`<scheduled-task name="daily">run it</scheduled-task>`, nil), ""},
		{mk(agent.NotePrefix+"This conversation was moved here", nil), ""}, // hopsesh's own
	}
	for i, c := range cases {
		if got := realPrompt(c.r); got != c.want {
			t.Errorf("%d: got %q want %q", i, got, c.want)
		}
	}
	long := strings.Repeat("é", 250)
	if got := realPrompt(mk(long, nil)); len([]rune(got)) != 201 || !strings.HasSuffix(got, "…") {
		t.Errorf("clip: %d runes", len([]rune(got)))
	}
}

func TestMovedMark(t *testing.T) {
	f := "/t/moved/s1.jsonl"
	body := `{"type":"user","uuid":"u1","parentUuid":null,"sessionId":"s1","cwd":"/Users/alice/p","timestamp":"2026-10-01T10:00:00Z","message":{"role":"user","content":"hello"}}` + "\n" +
		`{"type":"custom-title","customTitle":"fix tests","sessionId":"s1"}` + "\n" +
		`{"type":"custom-title","customTitle":"↪ moved to laptop · fix tests","sessionId":"s1"}` + "\n"
	fake.Put(f, []byte(body), time.Now())
	s, err := summ(f)
	if err != nil {
		t.Fatal(err)
	}
	if s.Mark == nil || s.Mark.Location != "laptop" || s.Title != "fix tests" {
		t.Fatalf("got mark=%+v title=%q", s.Mark, s.Title)
	}
	if s.LastActivity.Format(time.RFC3339) != "2026-10-01T10:00:00Z" {
		t.Fatalf("the mark must not change last activity: %v", s.LastActivity)
	}
}

// A session whose prompts are all commands or hopsesh's notes is titled by the first
// sentence of the agent's first reply.
func TestSummarizeReplyTitle(t *testing.T) {
	root := "/t/" + t.Name()
	p := writeTranscript(t, path.Join(root, "projects", "-r"), "s4", []line{
		user("/r", "2026-10-01T10:00:00Z", "<command-name>/clear</command-name><command-args></command-args>", nil),
		user("/r", "2026-10-01T10:00:01Z", agent.NotePrefix+"This conversation was moved here", nil),
		{"type": "assistant", "uuid": "a1", "parentUuid": "x", "sessionId": "s4", "cwd": "/r", "timestamp": "2026-10-01T10:00:02Z",
			"message": line{"role": "assistant", "content": []line{{"type": "text", "text": "## I picked up the parser work where it stopped. Next I run the tests."}}}},
	})
	s, err := summ(p)
	if err != nil {
		t.Fatal(err)
	}
	if s.Title != "I picked up the parser work where it stopped" || s.TitleSource != "reply" || titleSource(s.TitleSource) != "reply" {
		t.Fatalf("title %q/%q", s.Title, s.TitleSource)
	}
	long := firstReply([]record{{Type: "assistant", Message: &struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	}{Content: json.RawMessage(`"` + strings.Repeat("word ", 30) + `"`)}}})
	if n := len([]rune(long)); n > maxReplyTitle || !strings.HasSuffix(long, "word…") {
		t.Fatalf("clipped reply title %q (%d)", long, n)
	}
}
