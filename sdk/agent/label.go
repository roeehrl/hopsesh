package agent

import "strings"

// Legacy labels: earlier hopsesh versions renamed a session left behind by a move
// ("↪ moved to studio · fix tests"). hopsesh no longer changes titles to show movement;
// it reads these labels only so an old one does not show as part of a title.
const (
	legacyPrepared  = "↪ prepared in "
	legacyMoved     = "↪ moved to "
	legacyContinued = "↪ continued in "
	legacySep       = " · "
)

// StripLegacyLabel splits a title an older hopsesh labelled into what the label said and
// the original title; ok is false for an ordinary title, which is returned as it is.
func StripLegacyLabel(t string) (LegacyLabel, string, bool) {
	if rest, ok := strings.CutPrefix(t, legacyPrepared); ok {
		l, title, valid := StripLegacyLabel(legacyContinued + rest)
		l.Kind = LabelPrepared
		return l, title, valid
	}
	if rest, ok := strings.CutPrefix(t, legacyMoved); ok {
		loc, title, _ := strings.Cut(rest, legacySep)
		loc = strings.TrimSpace(loc)
		if loc == "" || strings.ContainsAny(loc, " \t") {
			return LegacyLabel{}, t, false
		}
		return LegacyLabel{Kind: LabelMoved, Location: loc}, title, true
	}
	if rest, ok := strings.CutPrefix(t, legacyContinued); ok {
		head, title, _ := strings.Cut(rest, legacySep)
		name, loc, _ := strings.Cut(head, " on ")
		if strings.TrimSpace(name) == "" {
			return LegacyLabel{}, t, false
		}
		return LegacyLabel{Kind: LabelContinued, AgentName: strings.TrimSpace(name), Location: strings.TrimSpace(loc)}, title, true
	}
	return LegacyLabel{}, t, false
}

// LegacyLabelPrefixes are the beginnings of every legacy label (for dropping an old
// label from a copy that moves on).
func LegacyLabelPrefixes() []string { return []string{legacyMoved, legacyContinued, legacyPrepared} }
