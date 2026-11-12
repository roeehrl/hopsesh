package agent

import "testing"

func TestStripLegacyLabel(t *testing.T) {
	for _, c := range []struct {
		in    string
		l     LegacyLabel
		title string
	}{
		{"↪ prepared in Codex on studio · fix tests", LegacyLabel{Kind: LabelPrepared, Location: "studio", AgentName: "Codex"}, "fix tests"},
		{"↪ moved to studio · fix tests", LegacyLabel{Kind: LabelMoved, Location: "studio"}, "fix tests"},
		{"↪ moved to studio", LegacyLabel{Kind: LabelMoved, Location: "studio"}, ""},
		{"↪ continued in Codex on macbook · fix tests", LegacyLabel{Kind: LabelContinued, Location: "macbook", AgentName: "Codex"}, "fix tests"},
		{"↪ continued in Claude Code · x · y", LegacyLabel{Kind: LabelContinued, AgentName: "Claude Code"}, "x · y"},
	} {
		l, title, ok := StripLegacyLabel(c.in)
		if !ok || l != c.l || title != c.title {
			t.Errorf("StripLegacyLabel(%q) = %+v %q %v", c.in, l, title, ok)
		}
	}
	if _, _, ok := StripLegacyLabel("↪ moved to two words"); ok {
		t.Error("a location has no spaces")
	}
	if _, title, ok := StripLegacyLabel("fix the build"); ok || title != "fix the build" {
		t.Error("an ordinary title is not a label")
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
