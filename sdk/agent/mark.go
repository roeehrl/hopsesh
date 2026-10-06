package agent

import "strings"

// Mark titles: modules whose agent shows session titles mark a copy left behind by
// retitling it, in one form every module shares, so any hopsesh reads any agent's marks.
const (
	preparedPrefix  = "↪ prepared in "
	movedPrefix     = "↪ moved to "
	continuedPrefix = "↪ continued in "
	titleSep        = " · "
)

// MarkTitle is the title of a copy left behind; MarkContinued uses m.AgentName ("Codex").
func MarkTitle(m Mark, title string) string {
	var t string
	switch m.Kind {
	case MarkPrepared, MarkContinued:
		t = continuedPrefix + m.AgentName
		if m.Kind == MarkPrepared {
			t = preparedPrefix + m.AgentName
		}
		if m.Location != "" {
			t += " on " + m.Location
		}
	default:
		t = movedPrefix + m.Location
	}
	if title != "" {
		t += titleSep + title
	}
	return t
}

// ParseMarkTitle reads a mark title back: the mark (AgentName set for MarkContinued), the
// original title, and ok=false when t is an ordinary title.
func ParseMarkTitle(t string) (Mark, string, bool) {
	if rest, ok := strings.CutPrefix(t, preparedPrefix); ok {
		mark, title, valid := ParseMarkTitle(continuedPrefix + rest)
		mark.Kind = MarkPrepared
		return mark, title, valid
	}
	if rest, ok := strings.CutPrefix(t, movedPrefix); ok {
		loc, title, _ := strings.Cut(rest, titleSep)
		loc = strings.TrimSpace(loc)
		if loc == "" || strings.ContainsAny(loc, " \t") {
			return Mark{}, t, false
		}
		return Mark{Kind: MarkMoved, Location: loc}, title, true
	}
	if rest, ok := strings.CutPrefix(t, continuedPrefix); ok {
		head, title, _ := strings.Cut(rest, titleSep)
		name, loc, _ := strings.Cut(head, " on ")
		if strings.TrimSpace(name) == "" {
			return Mark{}, t, false
		}
		return Mark{Kind: MarkContinued, AgentName: strings.TrimSpace(name), Location: strings.TrimSpace(loc)}, title, true
	}
	return Mark{}, t, false
}

// MarkPrefixes are the beginnings of every mark title (for dropping marks from a copy
// that moves on).
func MarkPrefixes() []string { return []string{movedPrefix, continuedPrefix, preparedPrefix} }

// NotePrefix begins every message hopsesh itself adds to a conversation (a move's first
// prompt, a continuation's briefing). Session lists do not show such a message as the
// user's last prompt.
const NotePrefix = "[hopsesh] "

// OwnText is the person's own part of a user message: "" for a message hopsesh added,
// and the text before hopsesh's note when one was added to the person's last message (a
// continuation's briefing joins it so that roles keep alternating).
func OwnText(text string) string {
	if strings.HasPrefix(strings.TrimSpace(text), NotePrefix) {
		return ""
	}
	if i := strings.Index(text, "\n\n"+NotePrefix); i >= 0 {
		text = text[:i]
	}
	return strings.TrimSpace(text)
}
