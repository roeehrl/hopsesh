package agent

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/roeehrl/hopsesh/sdk/ir"
)

func TestPreviewText(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"1. First\n2. Second\n   - Nested", "1. First\n2. Second\n   - Nested"},
		{"hello   world", "hello   world"},
		{"one\ntwo\n\n\nthree\t four", "one\ntwo\n\n\nthree\t four"},
		{"look:\n```go\nfunc a() {}\nfunc b() {}\n```\nthen run it", "look:\n```go\nfunc a() {}\nfunc b() {}\n```\nthen run it"},
		{"```\nx\n```", "```\nx\n```"},
		{"open fence\n~~~\na\nb\nc", "open fence\n~~~\na\nb\nc"},
		{"see ![shot](a.png) and [Image #2] here", "see ‹image› and ‹image› here"},
		{"bell\x07 and\x1b[0m escape\r\nnext", "bell and escape\nnext"},
		{NotePrefix + "This conversation was moved here", ""},
		{"my question\n\n" + NotePrefix + "briefing", "my question"},
		{"   \n\n  ", ""},
	} {
		if got := PreviewText(c.in); got != c.want {
			t.Errorf("PreviewText(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestPreviewTextCut(t *testing.T) {
	long := strings.Repeat("word ", 400) // 2000 characters
	got := PreviewText(long)
	if n := utf8.RuneCountInString(got); n > MaxPreviewText || n < MaxPreviewText-10 {
		t.Fatalf("cut to %d characters", n)
	}
	if !strings.HasSuffix(got, "word…") {
		t.Fatalf("not cut at a word: %q", got[len(got)-20:])
	}
	wide := strings.Repeat("é", 2000) // one word, no boundary to cut at
	if got := PreviewText(wide); utf8.RuneCountInString(got) != MaxPreviewText || !strings.HasSuffix(got, "é…") {
		t.Fatalf("a single long word: %d characters", utf8.RuneCountInString(got))
	}
}

func TestBuildPreview(t *testing.T) {
	at := func(m int) time.Time { return time.Date(2026, 10, 1, 10, m, 0, 0, time.UTC) }
	user := func(m int, s string) PreviewItem { return PreviewItem{Role: PreviewUser, Text: s, Time: at(m)} }
	agentText := func(m int, s string) PreviewItem { return PreviewItem{Role: PreviewAgent, Text: s, Time: at(m)} }
	tool := func(m int, k ir.ToolKind) PreviewItem {
		return PreviewItem{Role: PreviewTools, Time: at(m), Tools: map[ir.ToolKind]int{k: 1}}
	}
	items := []PreviewItem{
		user(0, "first"),
		agentText(1, "done"),
		{Role: PreviewCompacted, Time: at(2)},
		{Role: PreviewCompacted, Time: at(2)}, // a boundary and its summary: one marker
		user(3, "second"),
		agentText(4, "Let me look."),
		tool(5, ir.ToolRead),
		tool(6, ir.ToolRead),
		agentText(7, "Now the edit."),
		tool(8, ir.ToolEdit),
		agentText(9, "All fixed:"),
		agentText(9, "the test passes."),
		user(10, NotePrefix+"hopsesh's own"), // dropped
	}
	p := BuildPreview(items, 2, false)
	var got []string
	for _, it := range p.Items {
		s := string(it.Role) + ":" + it.Text
		if it.Role == PreviewTools {
			s += "read=" + string(rune('0'+it.Tools[ir.ToolRead])) + ",edit=" + string(rune('0'+it.Tools[ir.ToolEdit]))
		}
		got = append(got, s)
	}
	want := "user:second | tools:read=2,edit=1 | agent:All fixed:\n\nthe test passes."
	if strings.Join(got, " | ") != want || !p.More || p.Messages() != 2 {
		t.Fatalf("got %q more=%v", strings.Join(got, " | "), p.More)
	}
	if p.Items[1].Time != at(8) || p.Items[2].Time != at(9) {
		t.Fatalf("times %v %v", p.Items[1].Time, p.Items[2].Time)
	}

	all := BuildPreview(items, 10, false)
	if all.More || all.Messages() != 4 || all.Items[2].Role != PreviewCompacted || len(all.Items) != 6 {
		t.Fatalf("every message: %+v", all)
	}
	if BuildPreview(items, 10, true).More != true {
		t.Fatal("a window that did not reach the start has earlier messages")
	}
	three := BuildPreview(items, 3, false)
	if three.Items[0].Text != "done" {
		t.Fatalf("the third-last message starts the preview: %+v", three.Items[0])
	}
	if three.Items[1].Role != PreviewCompacted {
		t.Fatalf("a marker between returned messages is kept: %+v", three.Items)
	}

	// A turn still running tools after its last text: the text comes first.
	live := BuildPreview([]PreviewItem{user(0, "go"), agentText(1, "On it."), tool(2, ir.ToolExecute)}, 5, false)
	if len(live.Items) != 3 || live.Items[1].Role != PreviewAgent || live.Items[2].Role != PreviewTools {
		t.Fatalf("a turn that ends in tools: %+v", live.Items)
	}
	if none := BuildPreview(items, 0, false); len(none.Items) != 0 || !none.More {
		t.Fatalf("n=0: %+v", none)
	}
}

func TestCheckTitle(t *testing.T) {
	if got, err := CheckTitle("  Fix the parser  "); err != nil || got != "Fix the parser" {
		t.Fatalf("%q %v", got, err)
	}
	for _, bad := range []string{"", "   ", "two\nlines", "tab\there", strings.Repeat("x", MaxTitle+1), "↪ moved to x · t"} {
		if _, err := CheckTitle(bad); err == nil {
			t.Errorf("CheckTitle(%q) accepted", bad)
		}
	}
	if _, err := CheckTitle(strings.Repeat("é", MaxTitle)); err != nil {
		t.Errorf("a title of MaxTitle characters: %v", err)
	}
}

func TestPreviewPrivacyAndTurnBoundaries(t *testing.T) {
	for _, s := range []string{
		"public <system-reminder>private</system-reminder> answer",
		"public \x1b]8;;https://private.example\x07answer\x1b]8;;\x1b\\",
	} {
		if got := PreviewText(s); strings.Join(strings.Fields(got), " ") != "public answer" {
			t.Fatalf("got %q", got)
		}
	}
	p := BuildPreview([]PreviewItem{
		{Role: PreviewUser, Text: "question"},
		{Role: PreviewAgent, Text: "first block"},
		{Role: PreviewUser, Text: NotePrefix + "injected"},
		{Role: PreviewAgent, Text: NotePrefix + "private"},
		{Role: PreviewAgent, Text: "second block"},
	}, 4, false)
	if p.Messages() != 2 || p.Items[1].Text != "first block\n\nsecond block" {
		t.Fatalf("%+v", p)
	}
}

func TestPromptTitle(t *testing.T) {
	for _, s := range []string{"/deploy production", "[Pasted text #1 +40 lines]", "! ls", "[Request interrupted by user]", NotePrefix + "private"} {
		if got := PromptTitle(s); got != "" {
			t.Errorf("%q: %q", s, got)
		}
	}
	got := PromptTitle(strings.Repeat("word ", 50))
	if len([]rune(got)) > 80 || !strings.HasSuffix(got, "word…") {
		t.Fatal(got)
	}
}
