package agent

import "strings"

// Mark titles: modules whose agent shows session titles mark a copy left behind by
// retitling it, in one form every module shares, so any hopsesh reads any agent's marks.
const (
	movedPrefix     = "↪ moved to "
	continuedPrefix = "↪ continued in "
	titleSep        = " · "
)

// MarkTitle is the title of a copy left behind; MarkContinued uses m.AgentName ("Codex").
func MarkTitle(m Mark, title string) string {
	var t string
	switch m.Kind {
	case MarkContinued:
		t = continuedPrefix + m.AgentName
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
func MarkPrefixes() []string { return []string{movedPrefix, continuedPrefix} }
