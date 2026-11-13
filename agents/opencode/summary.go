package opencode

import (
	"regexp"
	"strings"
	"time"

	"github.com/roeehrl/hopsesh/sdk/agent"
)

// untitledRE is OpenCode's fallback title for a session it has not named yet
// (util/session-title-fallback.ts).
var untitledRE = regexp.MustCompile(`^(New session|Child session) - \d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d{3}Z$`)

// untitled reports whether a session has no title of its own: none, or OpenCode's
// fallback. OpenCode names a session only while it is untitled, so any other title is
// one it generated or one the person (or hopsesh) gave it.
func untitled(s session) bool {
	return !s.HasTitle || strings.TrimSpace(s.Title) == "" || untitledRE.MatchString(s.Title)
}

// summaryOf is a listed session as hopsesh shows it. Its size is its number of settled
// messages, the same whether read from the database or from an export.
func summaryOf(h agent.Host, in agent.Install, l listed, subagents int) agent.Summary {
	s := l.session
	sum := agent.Summary{
		Key:          sessionKey(in, s.ID),
		CWD:          nativeDir(h, s.Directory),
		LastActivity: millis(s.Updated),
		Size:         l.Settled,
		AgentVersion: s.Version,
		NativeParent: agent.SessionID(s.ForkSessionID),
		Subagents:    subagents,
		Path:         anchorOf(h, in, agent.SessionID(s.ID)),
		LastPrompt:   clip(agent.PreviewText(l.LastPrompt)),
	}
	if untitled(s) {
		// Named by its first prompt; the API tier knows no prompts, so it stays untitled.
		if t := agent.PromptTitle(l.FirstPrompt); t != "" {
			sum.Title, sum.TitleSource = t, "prompt"
		}
	} else {
		sum.Title, sum.TitleSource = s.Title, "generated"
		if lab, orig, ok := agent.StripLegacyLabel(s.Title); ok {
			sum.LegacyLabel, sum.Title = &lab, orig
		}
	}
	if s.Archived != 0 {
		sum.Kinds = []string{kindArchived}
	}
	return sum
}

// nativeDir is a stored directory in the machine's own form: OpenCode stores "/" on
// every OS.
func nativeDir(h agent.Host, dir string) string {
	if dir == "" || h.Path().Sep() == "/" {
		return dir
	}
	return h.Path().Clean(strings.ReplaceAll(dir, "/", h.Path().Sep()))
}

func millis(ms int64) time.Time {
	if ms == 0 {
		return time.Time{}
	}
	return time.UnixMilli(ms).UTC()
}

// clip shortens a prompt for a listing.
func clip(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > 200 {
		return string(r[:199]) + "…"
	}
	return s
}
