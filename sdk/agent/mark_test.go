package agent

import "testing"

func TestMarkTitles(t *testing.T) {
	for _, c := range []struct {
		m     Mark
		title string
		want  string
	}{
		{Mark{Kind: MarkMoved, Location: "studio"}, "fix tests", "↪ moved to studio · fix tests"},
		{Mark{Kind: MarkMoved, Location: "studio"}, "", "↪ moved to studio"},
		{Mark{Kind: MarkContinued, Location: "macbook", AgentName: "Codex"}, "fix tests", "↪ continued in Codex on macbook · fix tests"},
		{Mark{Kind: MarkContinued, AgentName: "Claude Code"}, "x · y", "↪ continued in Claude Code · x · y"},
	} {
		got := MarkTitle(c.m, c.title)
		if got != c.want {
			t.Errorf("MarkTitle = %q, want %q", got, c.want)
		}
		m, title, ok := ParseMarkTitle(got)
		if !ok || m != c.m || title != c.title {
			t.Errorf("ParseMarkTitle(%q) = %+v %q %v", got, m, title, ok)
		}
	}
	if _, _, ok := ParseMarkTitle("↪ moved to two words"); ok {
		t.Error("a location has no spaces")
	}
	if _, title, ok := ParseMarkTitle("fix the build"); ok || title != "fix the build" {
		t.Error("an ordinary title is not a mark")
	}
}

func TestOwnText(t *testing.T) {
	for in, want := range map[string]string{
		"fix the build": "fix the build",
		NotePrefix + "This session was moved here": "",
		"  " + NotePrefix + "briefing":             "",
		"document the cursor format\n\n" + NotePrefix + "This conversation was moved from Claude Code": "document the cursor format",
		"quote a note: " + NotePrefix + "inline":                                                       "quote a note: " + NotePrefix + "inline",
	} {
		if got := OwnText(in); got != want {
			t.Errorf("OwnText(%q) = %q, want %q", in, got, want)
		}
	}
}
