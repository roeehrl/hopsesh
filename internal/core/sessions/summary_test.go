package sessions

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/roeehrl/hopsesh/internal/core/fsys"
)

type line map[string]any

func writeTranscript(t *testing.T, dir, id string, lines []line) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	for _, l := range lines {
		j, err := json.Marshal(l)
		if err != nil {
			t.Fatal(err)
		}
		b.Write(j)
		b.WriteByte('\n')
	}
	p := filepath.Join(dir, id+".jsonl")
	if err := os.WriteFile(p, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func user(cwd, ts string, content any, extra line) line {
	l := line{"type": "user", "uuid": ts, "parentUuid": nil, "sessionId": "s1", "cwd": cwd, "gitBranch": "main",
		"version": "2.1.284", "entrypoint": "cli", "timestamp": ts, "message": line{"role": "user", "content": content}}
	for k, v := range extra {
		l[k] = v
	}
	return l
}

func TestSummarizePriorities(t *testing.T) {
	root := t.TempDir()
	cfg := filepath.Join(root, ".claude")
	proj := filepath.Join(cfg, "projects", "-repo")
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
	s, err := Summarize(fsys.Local{}, p)
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
	if s.ProjectDir != "-repo" || s.ID != "s1" || s.Version != "2.1.284" || !s.HasMessages {
		t.Errorf("fields: %+v", s)
	}
	if s.LastActivity.Format("15:04:05") != "10:00:05" {
		t.Errorf("last activity %v", s.LastActivity)
	}
}

func TestSummarizeCustomTitleSidecarAndRelocated(t *testing.T) {
	root := t.TempDir()
	proj := filepath.Join(root, "projects", "-new")
	p := writeTranscript(t, proj, "s2", []line{
		user("/old", "2026-10-01T10:00:00Z", "first thing", nil),
		{"type": "relocated", "sessionId": "s2", "relocatedCwd": "/new"},
	})
	if err := os.MkdirAll(filepath.Join(proj, "s2", "subagents"), 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(proj, "s2", "custom-title.json"), []byte(`{"customTitle":"Named by user"}`), 0o600)
	os.WriteFile(filepath.Join(proj, "s2", "subagents", "agent-1.jsonl"), []byte("{}\n"), 0o600)
	s, err := Summarize(fsys.Local{}, p)
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
	root := t.TempDir()
	proj := filepath.Join(root, "projects", "-big")
	big := strings.Repeat("x", 100*1024) // first prompt line larger than the 64 KB head
	lines := []line{user("/big", "2026-10-01T09:00:00Z", big, nil)}
	for i := 0; i < 2000; i++ {
		lines = append(lines, line{"type": "assistant", "uuid": "a", "sessionId": "s3", "cwd": "/big/elsewhere",
			"timestamp": "2026-10-01T09:30:00Z", "message": line{"role": "assistant", "content": "filler filler filler"}})
	}
	lines = append(lines, line{"type": "custom-title", "customTitle": "Big one"})
	p := writeTranscript(t, proj, "s3", lines)
	s, err := Summarize(fsys.Local{}, p)
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

func TestLocatorListFindAndRegistry(t *testing.T) {
	cfg := t.TempDir()
	writeTranscript(t, filepath.Join(cfg, "projects", "-a"), "id1", []line{user("/a", "2026-10-01T10:00:00Z", "one", nil)})
	writeTranscript(t, filepath.Join(cfg, "projects", "-b"), "id1", []line{user("/b", "2026-10-02T10:00:00Z", "dup", nil)})
	writeTranscript(t, filepath.Join(cfg, "projects", "-b"), "id2", []line{{"type": "custom-title", "customTitle": "no messages"}})
	os.MkdirAll(filepath.Join(cfg, "sessions"), 0o755)
	os.WriteFile(filepath.Join(cfg, "sessions", "123.json"), []byte(`{"pid":123,"sessionId":"id1","status":"busy","cwd":"/a"}`), 0o600)
	os.WriteFile(filepath.Join(cfg, "sessions", "456.json"), []byte(`{"pid":456,"sessionId":"id9","status":"idle"}`), 0o600)
	os.WriteFile(filepath.Join(cfg, "sessions", "123.abc.key"), []byte(`secret`), 0o600)

	loc := Locator{FS: fsys.Local{}, ConfigDir: cfg}
	list, err := loc.List(ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].LastPrompt != "dup" {
		t.Fatalf("list: %d first=%+v", len(list), list[0])
	}
	dups, _ := loc.Find("id1")
	if len(dups) != 2 {
		t.Errorf("find dups: %v", dups)
	}
	live, err := loc.LiveRegistry(func(p []int) map[int]bool { return map[int]bool{123: true} })
	if err != nil || len(live) != 1 || live[0].SessionID != "id1" || !live[0].Alive {
		t.Errorf("live: %+v %v", live, err)
	}
}

func TestMovedMark(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "s1.jsonl")
	body := `{"type":"user","uuid":"u1","parentUuid":null,"sessionId":"s1","cwd":"/Users/alice/p","timestamp":"2026-10-01T10:00:00Z","message":{"role":"user","content":"hello"}}` + "\n" +
		`{"type":"custom-title","customTitle":"fix tests","sessionId":"s1"}` + "\n" +
		`{"type":"custom-title","customTitle":"↪ moved to laptop · fix tests","sessionId":"s1"}` + "\n"
	if err := os.WriteFile(f, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := Summarize(fsys.Local{}, f)
	if err != nil {
		t.Fatal(err)
	}
	if s.MovedTo != "laptop" || s.Title != "fix tests" {
		t.Fatalf("got movedTo=%q title=%q", s.MovedTo, s.Title)
	}
	if s.LastActivity.Format(time.RFC3339) != "2026-10-01T10:00:00Z" {
		t.Fatalf("the mark must not change last activity: %v", s.LastActivity)
	}
}
