package agent

import "strings"

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
